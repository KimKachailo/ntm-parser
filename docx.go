// docx.go
package main

import (
	"archive/zip"
	"fmt"
	"os"
	"strings"
	"time"
)

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func rPr(sz int, bold bool) string {
	boldTag := ""
	if bold {
		boldTag = `<w:b/>`
	}
	return fmt.Sprintf(
		`<w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman" w:cs="Times New Roman"/>%s`+
			`<w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr>`,
		boldTag, sz*2, sz*2,
	)
}

func pPr(indentTwips int) string {
	indStr := ""
	if indentTwips > 0 {
		indStr = fmt.Sprintf(`<w:ind w:left="%d"/>`, indentTwips)
	}
	return fmt.Sprintf(`<w:pPr><w:spacing w:after="0" w:line="240" w:lineRule="auto"/>%s</w:pPr>`, indStr)
}

func boldPara(text string, sz int) string {
	return fmt.Sprintf(`<w:p>%s<w:r>%s<w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		pPr(0), rPr(sz, true), xmlEscape(text))
}

func normalPara(text string, sz int, indentTwips int) string {
	return fmt.Sprintf(`<w:p>%s<w:r>%s<w:t xml:space="preserve">%s</w:t></w:r></w:p>`,
		pPr(indentTwips), rPr(sz, false), xmlEscape(text))
}

func emptyPara() string {
	return fmt.Sprintf(`<w:p>%s</w:p>`, pPr(0))
}

func isChartsLine(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "chart affected") || strings.Contains(lower, "charts affected")
}

func buildNoticeXML(results []noticeResult) string {
	var sb strings.Builder

	sb.WriteString(boldPara("NtM T&P Notices — "+time.Now().Format("02 January 2006"), 12))
	sb.WriteString(emptyPara())

	for _, r := range results {
		if r.text == "NOT FOUND" {
			sb.WriteString(boldPara("❌ "+r.number+" — NOT FOUND", 11))
			sb.WriteString(emptyPara())
			continue
		}

		lines := strings.Split(strings.ReplaceAll(r.text, "\r\n", "\n"), "\n")

		for i, line := range lines {
			trimmed := strings.TrimSpace(line)

			if trimmed == "" {
				continue
			}

			if trimmed == "" {
				continue
			}

			if i == 0 {
				sb.WriteString(boldPara(trimmed, 11))
				sb.WriteString(emptyPara())
				continue
			}

			if isChartsLine(trimmed) {
				sb.WriteString(emptyPara())
				sb.WriteString(boldPara(trimmed, 10))
				continue
			}

			leadingSpaces := len(line) - len(strings.TrimLeft(line, " \t"))
			indent := 0
			if leadingSpaces >= 8 {
				indent = 720
			} else if leadingSpaces >= 3 {
				indent = 360
			}

			isNumberedItem := len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.'
			if isNumberedItem {
				sb.WriteString(emptyPara())
			}

			sb.WriteString(normalPara(trimmed, 10, indent))

			if strings.HasSuffix(trimmed, ":") {
				sb.WriteString(emptyPara())
			}
		}

		sb.WriteString(emptyPara())
		sb.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="4" w:space="1" w:color="AAAAAA"/></w:pBdr>` +
			`<w:spacing w:after="0"/></w:pPr></w:p>`)
		sb.WriteString(emptyPara())
	}

	return sb.String()
}

func saveResultsToDocx(results []noticeResult) (string, error) {
	bodyXML := buildNoticeXML(results)

	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>` + bodyXML + `
<w:sectPr>
  <w:pgSz w:w="11906" w:h="16838"/>
  <w:pgMar w:top="1134" w:right="850" w:bottom="1134" w:left="1701" w:header="709" w:footer="709" w:gutter="0"/>
</w:sectPr>
</w:body></w:document>`

	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`,
		"word/document.xml": documentXML,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
</Relationships>`,
	}

	filename := "ntm_notices_" + time.Now().Format("20060102_1504") + ".docx"
	f, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			return "", err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return "", err
		}
	}

	return filename, nil
}
