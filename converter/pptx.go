package converter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PptxToMarkdown converts a .pptx presentation into structured Markdown
func PptxToMarkdown(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("打开 pptx 压缩包失败: %w", err)
	}

	type slideEntry struct {
		num  int
		file *zip.File
	}

	slideRegex := regexp.MustCompile(`^ppt/slides/slide(\d+)\.xml$`)
	var slides []slideEntry

	for _, f := range zr.File {
		m := slideRegex.FindStringSubmatch(f.Name)
		if len(m) == 2 {
			num, _ := strconv.Atoi(m[1])
			slides = append(slides, slideEntry{num: num, file: f})
		}
	}

	if len(slides) == 0 {
		return "", fmt.Errorf("未在 pptx 中找到幻灯片内容")
	}

	sort.Slice(slides, func(i, j int) bool {
		return slides[i].num < slides[j].num
	})

	var sb strings.Builder

	for _, s := range slides {
		data, err := readZipEntryLimited(s.file, maxOfficeXMLBytes)
		if err != nil {
			continue
		}

		title, lines := parseSlideXML(data)
		if title == "" && len(lines) == 0 {
			continue
		}

		if title != "" {
			sb.WriteString(fmt.Sprintf("## 🖥️ 幻灯片 %d: %s\n\n", s.num, title))
		} else {
			sb.WriteString(fmt.Sprintf("## 🖥️ 幻灯片 %d\n\n", s.num))
		}

		for _, line := range lines {
			sb.WriteString( fmt.Sprintf("- %s\n", line))
		}
		sb.WriteString("\n---\n\n")
	}

	return strings.TrimSpace(sb.String()), nil
}

func parseSlideXML(data []byte) (string, []string) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var paragraphs []string
	var currentPara strings.Builder
	var inP bool
	var inT bool

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "p":
				inP = true
				currentPara.Reset()
			case "t":
				if inP {
					inT = true
				}
			}
		case xml.EndElement:
			switch elem.Name.Local {
			case "t":
				inT = false
			case "p":
				inP = false
				txt := strings.TrimSpace(currentPara.String())
				if txt != "" {
					paragraphs = append(paragraphs, txt)
				}
			}
		case xml.CharData:
			if inT {
				currentPara.Write(elem)
			}
		}
	}

	if len(paragraphs) == 0 {
		return "", nil
	}

	return paragraphs[0], paragraphs[1:]
}
