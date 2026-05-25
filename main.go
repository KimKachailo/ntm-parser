package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"

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
}
