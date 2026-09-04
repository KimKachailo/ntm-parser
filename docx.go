package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

type docxPart struct {
	name    string
	content string
}

func validXMLRune(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

func xmlEscape(s string) string {
	s = strings.Map(func(r rune) rune {
		if validXMLRune(r) {
			return r
		}
		return -1
	}, s)
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func rPr(size int, bold bool) string {
	boldTag := ""
	if bold {
		boldTag = `<w:b/>`
	}
	return fmt.Sprintf(
		`<w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:cs="Times New Roman"/>%s`+
			`<w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr>`,
		boldTag, size*2, size*2,
	)
}

func pPr(indentTwips int) string {
	indent := ""
	if indentTwips > 0 {
		indent = fmt.Sprintf(`<w:ind w:left="%d"/>`, indentTwips)
	}
	return fmt.Sprintf(
		`<w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/>%s</w:pPr>`,
		indent,
	)
}

func boldPara(text string, size int) string {
	return fmt.Sprintf(
		`<w:p>%s<w:r>%s<w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		pPr(0),
		rPr(size, true),
		xmlEscape(text),
	)
}

func normalPara(text string, size, indentTwips int) string {
	return fmt.Sprintf(
		`<w:p>%s<w:r>%s<w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		pPr(indentTwips),
		rPr(size, false),
		xmlEscape(text),
	)
}

func emptyPara() string {
	return fmt.Sprintf(`<w:p>%s</w:p>`, pPr(0))
}

func isChartsLine(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "chart affected") ||
		strings.Contains(lower, "charts affected")
}

func isNumberedItem(s string) bool {
	index := 0
	for index < len(s) && unicode.IsDigit(rune(s[index])) {
		index++
	}
	return index > 0 && index < len(s) && s[index] == '.'
}

func resultHeading(result noticeResult, firstLine string) string {
	if result.week == "" || result.year == "" {
		return firstLine
	}
	return fmt.Sprintf("%s  [week %s/%s]", firstLine, result.week, result.year)
}

func buildNoticeXMLAt(results []noticeResult, now time.Time) string {
	var builder strings.Builder
	builder.WriteString(boldPara("NtM T&P Notices — "+now.Format("02 January 2006"), 12))
	builder.WriteString(emptyPara())

	for _, result := range results {
		switch {
		case errors.Is(result.err, errNoticeNotFound):
			builder.WriteString(boldPara("❌ "+result.number+" — NOT FOUND", 11))
			builder.WriteString(emptyPara())
			continue
		case result.err != nil:
			builder.WriteString(boldPara("⚠ "+result.number+" — ERROR", 11))
			builder.WriteString(normalPara(result.err.Error(), 10, 0))
			builder.WriteString(emptyPara())
			continue
		}

		lines := strings.Split(strings.ReplaceAll(result.text, "\r\n", "\n"), "\n")
		wroteHeading := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			if !wroteHeading {
				builder.WriteString(boldPara(resultHeading(result, trimmed), 11))
				builder.WriteString(emptyPara())
				wroteHeading = true
				continue
			}
			if isChartsLine(trimmed) {
				builder.WriteString(emptyPara())
				builder.WriteString(boldPara(trimmed, 10))
				continue
			}

			leadingSpaces := len(line) - len(strings.TrimLeft(line, " \t"))
			indent := 0
			switch {
			case leadingSpaces >= 8:
				indent = 720
			case leadingSpaces >= 3:
				indent = 360
			}

			if isNumberedItem(trimmed) {
				builder.WriteString(emptyPara())
			}
			builder.WriteString(normalPara(trimmed, 10, indent))
			if strings.HasSuffix(trimmed, ":") {
				builder.WriteString(emptyPara())
			}
		}

		builder.WriteString(emptyPara())
		builder.WriteString(
			`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="4" w:space="1" w:color="AAAAAA"/></w:pBdr>` +
				`<w:spacing w:after="0"/></w:pPr></w:p>`,
		)
		builder.WriteString(emptyPara())
	}

	return builder.String()
}

func buildNoticeXML(results []noticeResult) string {
	return buildNoticeXMLAt(results, time.Now())
}

func documentParts(results []noticeResult, now time.Time) []docxPart {
	bodyXML := buildNoticeXMLAt(results, now)
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + bodyXML + `
<w:sectPr>
  <w:pgSz w:w="11906" w:h="16838"/>
  <w:pgMar w:top="1134" w:right="850" w:bottom="1134" w:left="1701" w:header="709" w:footer="709" w:gutter="0"/>
</w:sectPr>
</w:body></w:document>`

	return []docxPart{
		{
			name: "[Content_Types].xml",
			content: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
		},
		{
			name: "_rels/.rels",
			content: `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		},
		{name: "word/document.xml", content: documentXML},
	}
}

func writeDocx(file *os.File, parts []docxPart) (err error) {
	writer := zip.NewWriter(file)
	defer func() {
		if closeErr := writer.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close DOCX archive: %w", closeErr)
		}
		if syncErr := file.Sync(); err == nil && syncErr != nil {
			err = fmt.Errorf("sync DOCX file: %w", syncErr)
		}
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close DOCX file: %w", closeErr)
		}
	}()

	for _, part := range parts {
		partWriter, createErr := writer.Create(part.name)
		if createErr != nil {
			return fmt.Errorf("create DOCX part %s: %w", part.name, createErr)
		}
		if _, writeErr := partWriter.Write([]byte(part.content)); writeErr != nil {
			return fmt.Errorf("write DOCX part %s: %w", part.name, writeErr)
		}
	}
	return nil
}

func saveResultsToDocxInDir(results []noticeResult, directory string, now time.Time) (string, error) {
	baseName := "ntm_notices_" + now.Format("20060102_150405")
	parts := documentParts(results, now)

	for suffix := 0; suffix < 1000; suffix++ {
		name := baseName + ".docx"
		if suffix > 0 {
			name = fmt.Sprintf("%s_%d.docx", baseName, suffix)
		}
		path := filepath.Join(directory, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("create DOCX %s: %w", path, err)
		}
		if err := writeDocx(file, parts); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}

	return "", fmt.Errorf("cannot allocate a unique DOCX filename in %s", directory)
}

func saveResultsToDocx(results []noticeResult) (string, error) {
	return saveResultsToDocxInDir(results, ".", time.Now())
}
