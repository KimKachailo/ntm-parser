package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const (
	downloadURL    = "https://msi.admiralty.co.uk/NoticesToMariners/DownloadFile"
	userAgent      = "ntm-parser/1.0"
	maxHTMLSize    = 5 << 20
	maxPDFSize     = 150 << 20
	pdfTextTimeout = 45 * time.Second
)

var (
	errWeeklyPDFUnavailable = errors.New("weekly PDF unavailable")
	errNoticeNotInPDF       = errors.New("notice not present in PDF")
	errNoticeNotFound       = errors.New("notice not found")

	noticeNumberRE = regexp.MustCompile("(?i)^(\\d{1,4})(?:\\(([PT])\\))?/(\\d{2}|\\d{4})$")
	noticeHeaderRE = regexp.MustCompile("(?i)^(\\d{1,4}(?:\\([PT]\\))?/(?:\\d{2}|\\d{4}))(?:\\s|$)")
	weekFooterRE   = regexp.MustCompile("(?i)^\\s*Wk\\s*\\d+/\\d+")
)

type noticeResult struct {
	number string
	week   string
	year   string
	text   string
	err    error
}

type pdfCacheEntry struct {
	ready chan struct{}
	text  string
	err   error
}

type pdfCache struct {
	mu      sync.Mutex
	entries map[string]*pdfCacheEntry
}

func newPDFCache() *pdfCache {
	return &pdfCache{entries: make(map[string]*pdfCacheEntry)}
}

func (c *pdfCache) getOrLoad(ctx context.Context, key string, load func() (string, error)) (string, error) {
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok {
		c.mu.Unlock()
		select {
		case <-entry.ready:
			return entry.text, entry.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	entry := &pdfCacheEntry{ready: make(chan struct{})}
	c.entries[key] = entry
	c.mu.Unlock()

	entry.text, entry.err = load()
	close(entry.ready)
	return entry.text, entry.err
}

func readLimited(r io.Reader, limit int64, description string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s exceeds %d-byte limit", description, limit)
	}
	return data, nil
}

func extractCSRFToken(body io.Reader) (string, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse HTML: %w", err)
	}

	var walk func(*html.Node) (string, bool)
	walk = func(n *html.Node) (string, bool) {
		if n.Type == html.ElementNode && n.Data == "input" {
			var name, value string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "name":
					name = attr.Val
				case "value":
					value = attr.Val
				}
			}
			if name == "__RequestVerificationToken" && value != "" {
				return value, true
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if token, ok := walk(child); ok {
				return token, true
			}
		}
		return "", false
	}

	if token, ok := walk(doc); ok {
		return token, nil
	}
	return "", errors.New("CSRF token not found")
}

func extractFileInfo(body io.Reader) (string, string, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return "", "", fmt.Errorf("parse HTML: %w", err)
	}

	var walk func(*html.Node) (string, string, bool)
	walk = func(n *html.Node) (string, string, bool) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key != "href" || !strings.Contains(strings.ToLower(attr.Val), "downloadfile") {
					continue
				}
				parsed, parseErr := url.Parse(attr.Val)
				if parseErr != nil {
					continue
				}
				fileName := parsed.Query().Get("fileName")
				batchID := parsed.Query().Get("batchId")
				if strings.Contains(strings.ToLower(fileName), "wknm") && batchID != "" {
					return fileName, batchID, true
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if fileName, batchID, ok := walk(child); ok {
				return fileName, batchID, true
			}
		}
		return "", "", false
	}

	if fileName, batchID, ok := walk(doc); ok {
		return fileName, batchID, nil
	}
	return "", "", errWeeklyPDFUnavailable
}

func fetchFileInfo(ctx context.Context, client *http.Client, token, year, week string) (string, string, error) {
	formData := url.Values{
		"year":                       {year},
		"week":                       {week},
		"__RequestVerificationToken": {token},
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL,
		strings.NewReader(formData.Encode()),
	)
	if err != nil {
		return "", "", fmt.Errorf("create weekly-page request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("request week %s/%s: %w", week, year, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("request week %s/%s: unexpected HTTP status %s", week, year, resp.Status)
	}
	body, err := readLimited(resp.Body, maxHTMLSize, "weekly page")
	if err != nil {
		return "", "", err
	}

	fileName, batchID, err := extractFileInfo(bytes.NewReader(body))
	if err != nil {
		if errors.Is(err, errWeeklyPDFUnavailable) {
			return "", "", fmt.Errorf("%w for week %s/%s", errWeeklyPDFUnavailable, week, year)
		}
		return "", "", err
	}
	return fileName, batchID, nil
}

func downloadPDFToMemory(ctx context.Context, client *http.Client, fileName, batchID string) ([]byte, error) {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("parse download URL: %w", err)
	}
	query := parsed.Query()
	query.Set("fileName", fileName)
	query.Set("batchId", batchID)
	query.Set("mimeType", "application/pdf")
	query.Set("frequency", "Weekly")
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create PDF request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download PDF: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download PDF: unexpected HTTP status %s", resp.Status)
	}
	data, err := readLimited(resp.Body, maxPDFSize, "PDF")
	if err != nil {
		return nil, err
	}

	header := data
	if len(header) > 1024 {
		header = header[:1024]
	}
	if !bytes.Contains(header, []byte("%PDF-")) {
		return nil, fmt.Errorf("download response is not a PDF (Content-Type %q)", resp.Header.Get("Content-Type"))
	}
	return data, nil
}

func extractPDFText(ctx context.Context, data []byte) (string, error) {
	tmp, err := os.CreateTemp("", "ntm-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temporary PDF: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		return "", fmt.Errorf("write temporary PDF: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temporary PDF: %w", err)
	}

	commandCtx, cancel := context.WithTimeout(ctx, pdfTextTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(commandCtx, "pdftotext", "-enc", "UTF-8", "-layout", tmpName, "-")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if commandCtx.Err() != nil {
			return "", fmt.Errorf("pdftotext: %w", commandCtx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if len(detail) > 500 {
			detail = detail[:500] + "..."
		}
		if detail != "" {
			return "", fmt.Errorf("pdftotext: %w: %s", err, detail)
		}
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	if !utf8.Valid(stdout.Bytes()) {
		return "", errors.New("pdftotext returned invalid UTF-8")
	}

	return stdout.String(), nil
}

func normalizeNoticeNumber(number string) (string, error) {
	number = strings.TrimSpace(number)
	match := noticeNumberRE.FindStringSubmatch(number)
	if match == nil {
		return "", fmt.Errorf("invalid notice number %q; expected e.g. 2269(P)/26", number)
	}

	qualifier := ""
	if match[2] != "" {
		qualifier = "(" + strings.ToUpper(match[2]) + ")"
	}
	return match[1] + qualifier + "/" + match[3], nil
}

func yearFromNotice(number string) (string, error) {
	normalized, err := normalizeNoticeNumber(number)
	if err != nil {
		return "", err
	}
	match := noticeNumberRE.FindStringSubmatch(normalized)
	year := match[3]
	if len(year) == 2 {
		year = "20" + year
	}
	return year, nil
}

func noticeHeaderNumber(line string) (string, bool) {
	if line == "" || line[0] == ' ' || line[0] == '\t' {
		return "", false
	}
	match := noticeHeaderRE.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	number, err := normalizeNoticeNumber(match[1])
	return number, err == nil
}

func continuationHeader(lines []string, index int) bool {
	if strings.Contains(strings.ToLower(lines[index]), "(continued)") {
		return true
	}
	return index+1 < len(lines) &&
		strings.Contains(strings.ToLower(strings.TrimSpace(lines[index+1])), "(continued)")
}

func trimSegment(lines []string) []string {
	for i, line := range lines {
		if weekFooterRE.MatchString(line) {
			lines = lines[:i]
			break
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func extractNotice(text, noticeNumber string) (string, error) {
	number, err := normalizeNoticeNumber(noticeNumber)
	if err != nil {
		return "", err
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\f", "\n")
	lines := strings.Split(text, "\n")

	var output []string
	foundMain := false
	for i := 0; i < len(lines); i++ {
		headerNumber, ok := noticeHeaderNumber(lines[i])
		if !ok || !strings.EqualFold(headerNumber, number) || strings.Contains(lines[i], "....") {
			continue
		}

		isContinuation := continuationHeader(lines, i)
		if !foundMain && isContinuation {
			continue
		}
		if foundMain && !isContinuation {
			continue
		}

		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if _, nextIsHeader := noticeHeaderNumber(lines[j]); nextIsHeader &&
				!strings.Contains(lines[j], "....") {
				end = j
				break
			}
		}

		segment := append([]string(nil), lines[i:end]...)
		if isContinuation {
			segment = segment[1:]
			if len(segment) > 0 &&
				strings.Contains(strings.ToLower(segment[0]), "(continued)") {
				segment = segment[1:]
			}
		} else {
			foundMain = true
		}
		segment = trimSegment(segment)
		if len(segment) > 0 {
			if len(output) > 0 {
				output = append(output, "")
			}
			output = append(output, segment...)
		}
		i = end - 1
	}

	if !foundMain || len(output) == 0 {
		return "", fmt.Errorf("%w: %s", errNoticeNotInPDF, number)
	}

	filtered := output[:0]
	for _, line := range output {
		if !strings.EqualFold(strings.TrimSpace(line), "(continued)") {
			filtered = append(filtered, line)
		}
	}
	return strings.TrimSpace(strings.Join(filtered, "\n")), nil
}

func weeksInISOYear(year int) int {
	_, week := time.Date(year, time.December, 28, 0, 0, 0, 0, time.UTC).ISOWeek()
	return week
}

func weeksToSearch(year string, now time.Time) ([]int, error) {
	targetYear, err := strconv.Atoi(year)
	if err != nil {
		return nil, fmt.Errorf("invalid year %q: %w", year, err)
	}
	currentYear, currentWeek := now.ISOWeek()
	if targetYear > currentYear {
		return nil, fmt.Errorf("notice year %d is in the future", targetYear)
	}

	maxWeek := weeksInISOYear(targetYear)
	if targetYear == currentYear {
		maxWeek = currentWeek
	}
	weeks := make([]int, 0, maxWeek)
	for week := maxWeek; week >= 1; week-- {
		weeks = append(weeks, week)
	}
	return weeks, nil
}

func getPDFText(
	ctx context.Context,
	client *http.Client,
	token, year, week string,
	cache *pdfCache,
) (string, error) {
	cacheKey := year + "-" + week
	return cache.getOrLoad(ctx, cacheKey, func() (string, error) {
		fileName, batchID, err := fetchFileInfo(ctx, client, token, year, week)
		if err != nil {
			return "", err
		}
		data, err := downloadPDFToMemory(ctx, client, fileName, batchID)
		if err != nil {
			return "", err
		}
		return extractPDFText(ctx, data)
	})
}

func findNotice(
	ctx context.Context,
	client *http.Client,
	token, number string,
	cache *pdfCache,
	now time.Time,
) noticeResult {
	normalized, err := normalizeNoticeNumber(number)
	if err != nil {
		return noticeResult{number: number, err: err}
	}
	year, err := yearFromNotice(normalized)
	if err != nil {
		return noticeResult{number: normalized, err: err}
	}
	weeks, err := weeksToSearch(year, now)
	if err != nil {
		return noticeResult{number: normalized, year: year, err: err}
	}

	successfulWeeks := 0
	failedWeeks := 0
	var firstFailure error
	for _, week := range weeks {
		if err := ctx.Err(); err != nil {
			return noticeResult{number: normalized, year: year, err: err}
		}
		weekString := strconv.Itoa(week)
		text, err := getPDFText(ctx, client, token, year, weekString, cache)
		if err != nil {
			if errors.Is(err, errWeeklyPDFUnavailable) {
				continue
			}
			failedWeeks++
			if firstFailure == nil {
				firstFailure = fmt.Errorf("week %s/%s: %w", weekString, year, err)
			}
			continue
		}
		successfulWeeks++

		noticeText, err := extractNotice(text, normalized)
		if err == nil {
			return noticeResult{
				number: normalized,
				week:   weekString,
				year:   year,
				text:   noticeText,
			}
		}
		if !errors.Is(err, errNoticeNotInPDF) {
			failedWeeks++
			if firstFailure == nil {
				firstFailure = fmt.Errorf("parse week %s/%s: %w", weekString, year, err)
			}
		}
	}

	if firstFailure != nil {
		return noticeResult{
			number: normalized,
			year:   year,
			err:    fmt.Errorf("search incomplete; %d week(s) failed: %w", failedWeeks, firstFailure),
		}
	}
	if successfulWeeks == 0 {
		return noticeResult{
			number: normalized,
			year:   year,
			err:    fmt.Errorf("no weekly PDFs could be retrieved for %s", year),
		}
	}
	return noticeResult{
		number: normalized,
		year:   year,
		err:    fmt.Errorf("%w: %s", errNoticeNotFound, normalized),
	}
}
