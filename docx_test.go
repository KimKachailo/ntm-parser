package main

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestXMLEscape(t *testing.T) {
	t.Parallel()

	got := xmlEscape("A&B <C> \"D\"\x01")
	want := "A&amp;B &lt;C&gt; &" + "quot;D&" + "quot;"
	if got != want {
		t.Fatalf("xmlEscape = %q, want %q", got, want)
	}
}

func TestSaveResultsToDocxCreatesValidUniqueArchives(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	now := time.Date(2026, time.September, 4, 12, 34, 56, 0, time.UTC)
	results := []noticeResult{{
		number: "2269(P)/26",
		week:   "20",
		year:   "2026",
		text:   "2269(P)/26 MEXICO & TEST\n    10. Item\x01",
	}}

	first, err := saveResultsToDocxInDir(results, directory, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := saveResultsToDocxInDir(results, directory, now)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("two saves used the same path %s", first)
	}
	if filepath.Ext(first) != ".docx" || filepath.Ext(second) != ".docx" {
		t.Fatalf("unexpected paths %q, %q", first, second)
	}

	reader, err := zip.OpenReader(first)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	if len(reader.File) != 3 {
		t.Fatalf("archive contains %d parts, want 3", len(reader.File))
	}
	var document string
	for _, file := range reader.File {
		if file.Name != "word/document.xml" {
			continue
		}
		part, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil {
			t.Fatal(err)
		}
		document = string(data)
	}
	if document == "" {
		t.Fatal("word/document.xml not found")
	}
	if strings.ContainsRune(document, '\x01') {
		t.Fatal("document contains an invalid XML control character")
	}
	if !strings.Contains(document, "MEXICO &amp; TEST") || !strings.Contains(document, "week 20/2026") {
		t.Fatalf("document does not contain escaped text and metadata:\n%s", document)
	}

	decoder := xml.NewDecoder(strings.NewReader(document))
	for {
		if _, err := decoder.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("invalid document XML: %v", err)
		}
	}
}

func TestBuildNoticeXMLDistinguishesNotFoundAndError(t *testing.T) {
	t.Parallel()

	xmlText := buildNoticeXMLAt([]noticeResult{
		{number: "1(P)/26", err: errNoticeNotFound},
		{number: "2(T)/26", err: errors.New("network failed")},
	}, time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))

	if !strings.Contains(xmlText, "1(P)/26 — NOT FOUND") {
		t.Error("missing NOT FOUND result")
	}
	if !strings.Contains(xmlText, "2(T)/26 — ERROR") || !strings.Contains(xmlText, "network failed") {
		t.Error("missing technical error result")
	}
}
