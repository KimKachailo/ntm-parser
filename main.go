package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

const baseURL = "https://msi.admiralty.co.uk/NoticesToMariners/Weekly"

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

func downloadPDF(client *http.Client, fileName, batchID string) error {
	downloadURL := fmt.Sprintf(
		"https://msi.admiralty.co.uk/NoticesToMariners/DownloadFile?fileName=%s&batchId=%s&mimeType=application%%2Fpdf&frequency=Weekly",
		url.QueryEscape(fileName),
		url.QueryEscape(batchID),
	)

	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	file, err := os.Create(fileName)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	written, err := io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	fmt.Printf("Saved %s (%.1f KB)\n", fileName, float64(written)/1024)
	return nil
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

		if emptyCount >= 2 && noticeRe.MatchString(line) {
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

func extractNotice(pdfPath, noticeNumber string) (string, error) {
	cmd := exec.Command("pdftotext", "-layout", pdfPath, "-")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	text := string(output)

	block, err := extractBlock(text, noticeNumber, false)
	if err != nil {
		return "", err
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	contMarker := ""
	for _, line := range lines {
		if strings.Contains(line, noticeNumber) && strings.Contains(line, "(continued)") {
			contMarker = strings.TrimSpace(line)
			break
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

	return block, nil
}

func main() {
	jar, err := cookiejar.New(nil)
	if err != nil {
		log.Fatal(err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.Get(baseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	token, err := extractCSRFToken(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("CSRF token:", token)

	fileName, batchID, err := fetchFileInfo(client, token, "2026", "20")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("fileName:", fileName)
	fmt.Println("batchId: ", batchID)

	if _, err := os.Stat(fileName); os.IsNotExist(err) {
		if err := downloadPDF(client, fileName, batchID); err != nil {
			log.Fatal(err)
		}
	} else {
		fmt.Println("PDF already exists, skipping download")
	}

	notice, err := extractNotice(fileName, "2269(P)/26")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n--- Notice ---")
	fmt.Println(notice)
}
