package converter

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type docxRel struct {
	ID         string
	Type       string
	Target     string
	TargetMode string
}

// DocxToMarkdown converts a .docx document into clean GitHub-flavored Markdown (including embedded images & links)
func DocxToMarkdown(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("打开 docx 压缩包失败: %w", err)
	}

	var docFile *zip.File
	var relsFile *zip.File
	mediaFiles := make(map[string]*zip.File)

	for _, f := range zr.File {
		switch {
		case f.Name == "word/document.xml":
			docFile = f
		case f.Name == "word/_rels/document.xml.rels":
			relsFile = f
		case strings.HasPrefix(f.Name, "word/media/"):
			mediaFiles[f.Name] = f
		}
	}

	if docFile == nil {
		return "", fmt.Errorf("未在 docx 中找到 word/document.xml")
	}

	// Parse relationships (images and hyperlinks)
	rels := make(map[string]docxRel)
	if relsFile != nil {
		if relData, err := readZipEntryLimited(relsFile, maxOfficeXMLBytes); err == nil {
			rels = parseDocxRels(relData)
		}
	}

	// Pre-encode media images into base64 data URIs (limit single image to 4MB to keep DOM snappy)
	imageMap := make(map[string]string)
	for rID, rel := range rels {
		if strings.Contains(rel.Type, "/image") {
			rawTarget := strings.TrimPrefix(filepath.ToSlash(rel.Target), "/")
			if !strings.HasPrefix(rawTarget, "word/") {
				rawTarget = "word/" + rawTarget
			}
			targetPath := filepath.ToSlash(filepath.Clean(rawTarget))
			if mf, ok := mediaFiles[targetPath]; ok && mf.UncompressedSize64 <= 4*1024*1024 {
				if imgBytes, err := readZipEntryLimited(mf, 4*1024*1024); err == nil {
					mime := detectImageMime(targetPath)
					if mime != "" && len(imgBytes) > 0 {
						b64 := base64.StdEncoding.EncodeToString(imgBytes)
						imageMap[rID] = fmt.Sprintf("data:%s;base64,%s", mime, b64)
					}
				}
			}
		}
	}

	xmlData, err := readZipEntryLimited(docFile, maxOfficeXMLBytes)
	if err != nil {
		return "", err
	}

	return parseDocumentXML(xmlData, rels, imageMap)
}

func parseDocxRels(data []byte) map[string]docxRel {
	res := make(map[string]docxRel)
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			var r docxRel
			for _, a := range se.Attr {
				switch a.Name.Local {
				case "Id":
					r.ID = a.Value
				case "Type":
					r.Type = a.Value
				case "Target":
					r.Target = a.Value
				case "TargetMode":
					r.TargetMode = a.Value
				}
			}
			if r.ID != "" {
				res[r.ID] = r
			}
		}
	}
	return res
}

func detectImageMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	default:
		return ""
	}
}

type docxTableState struct {
	rows        [][]string
	currentRow  []string
	currentCell strings.Builder
	inCell      bool
}

func parseDocumentXML(data []byte, rels map[string]docxRel, imageMap map[string]string) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	var sb strings.Builder
	var tableStack []docxTableState
	curTable := func() *docxTableState {
		if len(tableStack) == 0 {
			return nil
		}
		return &tableStack[len(tableStack)-1]
	}

	var currentParagraph strings.Builder
	var isHeading int // 0 = normal, 1..4 = h1..h4
	var isBold bool
	var isItalic bool
	var isStrike bool
	var inP bool
	var inRPr bool
	var inPPr bool
	var isBullet bool
	var activeLinkURL string
	var linkTextBuf strings.Builder

	// Helper to format a completed table to markdown
	renderTableMarkdown := func(tableRows [][]string) string {
		if len(tableRows) == 0 {
			return ""
		}

		maxCols := 0
		for _, row := range tableRows {
			if len(row) > maxCols {
				maxCols = len(row)
			}
		}
		if maxCols == 0 {
			return ""
		}

		var tsb strings.Builder
		tsb.WriteString("\n")
		headerRow := tableRows[0]
		tsb.WriteString("|")
		for i := 0; i < maxCols; i++ {
			val := ""
			if i < len(headerRow) {
				val = cleanTableCell(headerRow[i])
			}
			if val == "" {
				val = " "
			}
			tsb.WriteString(" " + val + " |")
		}
		tsb.WriteString("\n|")
		for i := 0; i < maxCols; i++ {
			tsb.WriteString(" --- |")
		}
		tsb.WriteString("\n")

		for _, row := range tableRows[1:] {
			tsb.WriteString("|")
			for i := 0; i < maxCols; i++ {
				val := ""
				if i < len(row) {
					val = cleanTableCell(row[i])
				}
				if val == "" {
					val = " "
				}
				tsb.WriteString(" " + val + " |")
			}
			tsb.WriteString("\n")
		}
		tsb.WriteString("\n")
		return tsb.String()
	}

	writeToCurrentTarget := func(s string) {
		tbl := curTable()
		if activeLinkURL != "" {
			linkTextBuf.WriteString(s)
		} else if tbl != nil && tbl.inCell {
			tbl.currentCell.WriteString(s)
		} else if inP {
			currentParagraph.WriteString(s)
		}
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sb.String(), err
		}

		switch elem := token.(type) {
		case xml.StartElement:
			local := elem.Name.Local

			switch local {
			case "tbl":
				tableStack = append(tableStack, docxTableState{})

			case "tr":
				if tbl := curTable(); tbl != nil {
					tbl.currentRow = nil
				}

			case "tc":
				if tbl := curTable(); tbl != nil {
					tbl.inCell = true
					tbl.currentCell.Reset()
				}

			case "p":
				inP = true
				currentParagraph.Reset()
				isHeading = 0
				isBullet = false

			case "pPr":
				inPPr = true

			case "pStyle":
				if inPPr {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							v := strings.ToLower(a.Value)
							switch {
							case strings.Contains(v, "heading 1") || strings.Contains(v, "heading1") || v == "1" || strings.Contains(v, "标题 1") || strings.Contains(v, "标题1") || v == "title":
								isHeading = 1
							case strings.Contains(v, "heading 2") || strings.Contains(v, "heading2") || v == "2" || strings.Contains(v, "标题 2") || strings.Contains(v, "标题2") || v == "subtitle":
								isHeading = 2
							case strings.Contains(v, "heading 3") || strings.Contains(v, "heading3") || v == "3" || strings.Contains(v, "标题 3") || strings.Contains(v, "标题3"):
								isHeading = 3
							case strings.Contains(v, "heading 4") || strings.Contains(v, "heading4") || v == "4" || strings.Contains(v, "标题 4") || strings.Contains(v, "标题4"):
								isHeading = 4
							}
						}
					}
				}

			case "outlineLvl":
				if inPPr && isHeading == 0 {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							if lvl, err := strconv.Atoi(a.Value); err == nil && lvl >= 0 && lvl <= 3 {
								isHeading = lvl + 1
							}
						}
					}
				}

			case "numPr":
				if inPPr {
					isBullet = true
				}

			case "hyperlink":
				for _, a := range elem.Attr {
					if a.Name.Local == "id" {
						if rel, ok := rels[a.Value]; ok && strings.HasPrefix(rel.Target, "http") {
							activeLinkURL = rel.Target
							linkTextBuf.Reset()
						}
					}
				}

			case "blip", "imagedata":
				// Embedded image inside drawing or pict
				for _, a := range elem.Attr {
					if a.Name.Local == "embed" || a.Name.Local == "id" {
						if dataURI, ok := imageMap[a.Value]; ok {
							imgMarkdown := fmt.Sprintf("\n\n![文档内嵌图片](%s)\n\n", dataURI)
							if tbl := curTable(); tbl != nil && tbl.inCell {
								tbl.currentCell.WriteString(" [图片] ")
							} else if inP {
								currentParagraph.WriteString(imgMarkdown)
							}
						}
					}
				}

			case "r":
				isBold = false
				isItalic = false
				isStrike = false

			case "rPr":
				inRPr = true

			case "b":
				if inRPr {
					isBold = true
					for _, a := range elem.Attr {
						if a.Name.Local == "val" && (a.Value == "0" || a.Value == "false") {
							isBold = false
						}
					}
				}

			case "i":
				if inRPr {
					isItalic = true
					for _, a := range elem.Attr {
						if a.Name.Local == "val" && (a.Value == "0" || a.Value == "false") {
							isItalic = false
						}
					}
				}

			case "strike":
				if inRPr {
					isStrike = true
				}

			case "sz":
				if inPPr || inRPr {
					for _, a := range elem.Attr {
						if a.Name.Local == "val" {
							if valInt, err := strconv.Atoi(a.Value); err == nil {
								if valInt >= 40 && isHeading == 0 {
									isHeading = 1
								} else if valInt >= 32 && isHeading == 0 {
									isHeading = 2
								} else if valInt >= 28 && isHeading == 0 {
									isHeading = 3
								}
							}
						}
					}
				}

			case "t":
				var textContent string
				if err := decoder.DecodeElement(&textContent, &elem); err == nil && textContent != "" {
					formattedText := textContent
					if isBold && isItalic {
						formattedText = "***" + formattedText + "***"
					} else if isBold {
						formattedText = "**" + formattedText + "**"
					} else if isItalic {
						formattedText = "*" + formattedText + "*"
					}
					if isStrike {
						formattedText = "~~" + formattedText + "~~"
					}
					writeToCurrentTarget(formattedText)
				}

			case "br":
				if tbl := curTable(); tbl != nil && tbl.inCell {
					tbl.currentCell.WriteString("<br>")
				} else if inP {
					currentParagraph.WriteString("\n")
				}
			}

		case xml.EndElement:
			local := elem.Name.Local

			switch local {
			case "pPr":
				inPPr = false

			case "rPr":
				inRPr = false

			case "hyperlink":
				if activeLinkURL != "" {
					lText := strings.TrimSpace(linkTextBuf.String())
					if lText == "" {
						lText = activeLinkURL
					}
					urlStr := activeLinkURL
					activeLinkURL = ""
					writeToCurrentTarget(fmt.Sprintf("[%s](%s)", lText, urlStr))
				}

			case "p":
				inP = false
				pText := strings.TrimSpace(currentParagraph.String())
				// Merge adjacent bold tags like **** inside headings
				pText = strings.ReplaceAll(pText, "****", "")
				if tbl := curTable(); tbl != nil && tbl.inCell {
					if pText != "" {
						if tbl.currentCell.Len() > 0 {
							tbl.currentCell.WriteString("<br>")
						}
						tbl.currentCell.WriteString(pText)
					}
				} else {
					if pText != "" {
						if isHeading > 0 {
							// Strip redundant bold inside headings for cleaner TOC
							cleanHeading := strings.ReplaceAll(pText, "**", "")
							prefix := strings.Repeat("#", isHeading)
							sb.WriteString("\n" + prefix + " " + strings.TrimSpace(cleanHeading) + "\n\n")
						} else if isBullet {
							sb.WriteString("- " + pText + "\n")
						} else {
							sb.WriteString(pText + "\n\n")
						}
					}
				}

			case "tc":
				if tbl := curTable(); tbl != nil {
					tbl.inCell = false
					tbl.currentRow = append(tbl.currentRow, tbl.currentCell.String())
				}

			case "tr":
				if tbl := curTable(); tbl != nil {
					tbl.rows = append(tbl.rows, tbl.currentRow)
				}

			case "tbl":
				if len(tableStack) > 0 {
					completed := tableStack[len(tableStack)-1]
					tableStack = tableStack[:len(tableStack)-1]
					if parent := curTable(); parent != nil && parent.inCell {
						// Flatten nested table text cleanly into parent cell
						for _, row := range completed.rows {
							line := strings.Join(row, " | ")
							if strings.TrimSpace(line) != "" {
								if parent.currentCell.Len() > 0 {
									parent.currentCell.WriteString("<br>")
								}
								parent.currentCell.WriteString(line)
							}
						}
					} else {
						sb.WriteString(renderTableMarkdown(completed.rows))
					}
				}
			}
		}
	}

	result := sb.String()
	reBlank := regexp.MustCompile(`\n{3,}`)
	result = reBlank.ReplaceAllString(result, "\n\n")

	return strings.TrimSpace(result), nil
}

func cleanTableCell(text string) string {
	t := strings.TrimSpace(text)
	t = strings.ReplaceAll(t, "|", "\\|")
	t = strings.ReplaceAll(t, "\n", "<br>")
	return t
}
