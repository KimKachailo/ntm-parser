package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/charmap"
)

type noticeResult struct {
	number string
	week   string
	year   string
	text   string
}

type pdfCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (c *pdfCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data[key]
	return v, ok
}

func (c *pdfCache) set(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = data
}

func extractCSRFToken(body io.Reader) (string, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return "", fmt.Errorf("parse html: %w", err)
	}

	var token string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
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
			if name == "__RequestVerificationToken" {
				token = value
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if token == "" {
		return "", fmt.Errorf("CSRF token not found")
	}
	return token, nil
}

func extractFileInfo(body io.Reader) (string, string, error) {
	doc, err := html.Parse(body)
	if err != nil {
		return "", "", fmt.Errorf("parse html: %w", err)
	}

	var fileName, batchID string

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && strings.Contains(attr.Val, "DownloadFile") {
					parsed, err := url.Parse(attr.Val)
					if err == nil {
						name := parsed.Query().Get("fileName")
						id := parsed.Query().Get("batchId")
						if strings.Contains(name, "wknm") {
							fileName = name
							batchID = id
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if fileName == "" || batchID == "" {
		return "", "", fmt.Errorf("DownloadFile link not found in HTML")
	}
	return fileName, batchID, nil
}

func fetchFileInfo(client *http.Client, token, year, week string) (string, string, error) {
	formData := url.Values{
		"year":                       {year},
		"week":                       {week},
		"__RequestVerificationToken": {token},
	}

	resp, err := client.Post(baseURL, "application/x-www-form-urlencoded", strings.NewReader(formData.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("post request: %w", err)
	}
	defer resp.Body.Close()

	return extractFileInfo(resp.Body)
}

func downloadPDFToMemory(client *http.Client, fileName, batchID string) ([]byte, error) {
	downloadURL := fmt.Sprintf(
		"https://msi.admiralty.co.uk/NoticesToMariners/DownloadFile?fileName=%s&batchId=%s&mimeType=application%%2Fpdf&frequency=Weekly",
		url.QueryEscape(fileName),
		url.QueryEscape(batchID),
	)

	resp, err := client.Get(downloadURL)
	if err != nil {
		return nil, fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func extractBlock(text, marker string, isContinued bool) (string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	startLine := -1
	noticeRe := regexp.MustCompile(`^\d{4}(?:\([TP]\))?/\d{2,4}`)

	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		if strings.Contains(line, "....") {
			continue
		}
		if isContinued {
			startLine = i
			break
		}
		after := strings.TrimLeft(strings.TrimPrefix(line, marker), " \t")
		if len(after) > 0 && after[0] >= 'A' && after[0] <= 'Z' {
			startLine = i
			break
		}
	}

	if startLine == -1 {
		return "", fmt.Errorf("block %q not found", marker)
	}

	var blockLines []string
	emptyCount := 0

	for i := startLine; i < len(lines); i++ {
		line := lines[i]

		if strings.TrimSpace(line) == "" {
			emptyCount++
			blockLines = append(blockLines, line)
			continue
		}

		if emptyCount >= 1 && noticeRe.MatchString(line) {
			for len(blockLines) > 0 && strings.TrimSpace(blockLines[len(blockLines)-1]) == "" {
				blockLines = blockLines[:len(blockLines)-1]
			}
			break
		}

		emptyCount = 0
		blockLines = append(blockLines, line)
	}

	result := strings.TrimSpace(strings.Join(blockLines, "\n"))

	if idx := regexp.MustCompile(`(?m)^\s*Wk\d+/\d+`).FindStringIndex(result); idx != nil {
		result = strings.TrimSpace(result[:idx[0]])
	}
	if idx := regexp.MustCompile(`(?m)^\s*\d+\.\d+\s*$`).FindStringIndex(result); idx != nil {
		result = strings.TrimSpace(result[:idx[0]])
	}

	return result, nil
}

func extractNoticeFromBytes(data []byte, noticeNumber string) (string, error) {
	tmp, err := os.CreateTemp("", "ntm-*.pdf")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	tmp.Write(data)
	tmp.Close()
	defer os.Remove(tmpName)

	cmd := exec.Command("pdftotext", "-layout", tmpName, "-")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}

	decoded, err := charmap.Windows1252.NewDecoder().String(string(output))
	text := decoded
	if err != nil {
		text = string(output)
	}

	block, err := extractBlock(text, noticeNumber, false)
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	contMarker := ""
	for i, line := range lines {
		if strings.Contains(line, noticeNumber) {
			if strings.Contains(line, "(continued)") {
				contMarker = strings.TrimSpace(line)
				break
			}
			if i+1 < len(lines) && strings.Contains(lines[i+1], "(continued)") {
				contMarker = strings.TrimSpace(line)
				break
			}
		}
	}

	if contMarker != "" {
		if cont, err := extractBlock(text, contMarker, true); err == nil {
			contLines := strings.SplitN(cont, "\n", 2)
			if len(contLines) == 2 {
				block = block + "\n" + strings.TrimSpace(contLines[1])
			}
		}
	}

	cleanedLines := strings.Split(block, "\n")
	var filtered []string
	for _, l := range cleanedLines {
		if !strings.EqualFold(strings.TrimSpace(l), "(continued)") {
			filtered = append(filtered, l)
		}
	}
	block = strings.Join(filtered, "\n")

	return block, nil
}

func yearFromNotice(number string) (string, error) {
	re := regexp.MustCompile(`/(\d{2,4})$`)
	m := re.FindStringSubmatch(number)
	if m == nil {
		return "", fmt.Errorf("cannot extract year from %q", number)
	}
	y := m[1]
	if len(y) == 2 {
		y = "20" + y
	}
	return y, nil
}

func startWeek() int {
	_, week := time.Now().ISOWeek()
	if week < 52 {
		return week + 1
	}
	return 52
}

func getPDFData(client *http.Client, token, year, week string, cache *pdfCache) ([]byte, error) {
	cacheKey := year + "-" + week

	if data, ok := cache.get(cacheKey); ok {
		return data, nil
	}

	fileName, batchID, err := fetchFileInfo(client, token, year, week)
	if err != nil {
		return nil, err
	}

	data, err := downloadPDFToMemory(client, fileName, batchID)
	if err != nil {
		return nil, err
	}

	cache.set(cacheKey, data)
	return data, nil
}

func findNotice(client *http.Client, token, number string, cache *pdfCache) noticeResult {
	year, err := yearFromNotice(number)
	if err != nil {
		return noticeResult{number, "", "", "NOT FOUND"}
	}

	for week := startWeek(); week >= 1; week-- {
		weekStr := fmt.Sprintf("%d", week)

		data, err := getPDFData(client, token, year, weekStr, cache)
		if err != nil {
			continue
		}

		text, err := extractNoticeFromBytes(data, number)
		if err != nil {
			continue
		}

		fmt.Printf("  Found %s in week %s\n", number, weekStr)
		return noticeResult{number, weekStr, year, text}
	}

	return noticeResult{number, "", year, "NOT FOUND"}
}
