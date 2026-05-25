package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
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
}
