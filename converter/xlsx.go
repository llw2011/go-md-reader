package converter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// SheetInfo represents workbook sheet metadata
type SheetInfo struct {
	Name    string
	RelID   string
	SheetID string
	Target  string
}

// XlsxToMarkdown converts an .xlsx workbook into clean GitHub-flavored Markdown tables
func XlsxToMarkdown(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("打开 xlsx 压缩包失败: %w", err)
	}

	// 1. Parse shared strings (xl/sharedStrings.xml)
	var sharedStrings []string
	var wbRels map[string]string

	for _, f := range zr.File {
		switch f.Name {
		case "xl/sharedStrings.xml":
			if data, err := readZipEntryLimited(f, maxOfficeXMLBytes); err == nil {
				sharedStrings = parseSharedStrings(data)
			}
		case "xl/_rels/workbook.xml.rels":
			if data, err := readZipEntryLimited(f, maxOfficeXMLBytes); err == nil {
				wbRels = parseWorkbookRels(data)
			}
		}
	}

	// 2. Parse sheet list from xl/workbook.xml
	var sheets []SheetInfo
	for _, f := range zr.File {
		if f.Name == "xl/workbook.xml" {
			if data, err := readZipEntryLimited(f, maxOfficeXMLBytes); err == nil {
				sheets = parseWorkbookSheets(data)
			}
			break
		}
	}

	if len(sheets) == 0 {
		idx := 1
		for _, f := range zr.File {
			if strings.HasPrefix(f.Name, "xl/worksheets/sheet") && strings.HasSuffix(f.Name, ".xml") {
				sheets = append(sheets, SheetInfo{
					Name:   fmt.Sprintf("Sheet%d", idx),
					Target: f.Name,
				})
				idx++
			}
		}
	} else {
		for i := range sheets {
			if wbRels != nil {
				if target, ok := wbRels[sheets[i].RelID]; ok && target != "" {
					tPath := strings.TrimPrefix(target, "/")
					if !strings.HasPrefix(tPath, "xl/") {
						tPath = "xl/" + tPath
					}
					sheets[i].Target = filepath.ToSlash(filepath.Clean(tPath))
				}
			}
			if sheets[i].Target == "" {
				sheets[i].Target = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
			}
		}
	}

	var sb strings.Builder

	// 3. Process each sheet
	for _, sheet := range sheets {
		var sheetFile *zip.File
		for _, f := range zr.File {
			if f.Name == sheet.Target || strings.HasSuffix(f.Name, "/"+sheet.Target) {
				sheetFile = f
				break
			}
		}

		if sheetFile == nil {
			continue
		}

		sheetData, err := readZipEntryLimited(sheetFile, maxOfficeXMLBytes)
		if err != nil {
			continue
		}

		mdTable, rowCount, colCount := parseSheetData(sheetData, sharedStrings)
		if strings.TrimSpace(mdTable) != "" {
			sb.WriteString(fmt.Sprintf("## 📊 工作表: %s `(%d 行 × %d 列)`\n\n", sheet.Name, rowCount, colCount))
			sb.WriteString(mdTable)
			sb.WriteString("\n\n---\n\n")
		}
	}

	if sb.Len() == 0 {
		return "> [!NOTE]\n> 该 Excel 文件中未检测到有效的数据表格内容。\n", nil
	}

	return strings.TrimSpace(sb.String()), nil
}

func parseWorkbookRels(data []byte) map[string]string {
	res := make(map[string]string)
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
			var id, target string
			for _, a := range se.Attr {
				if a.Name.Local == "Id" {
					id = a.Value
				} else if a.Name.Local == "Target" {
					target = a.Value
				}
			}
			if id != "" && target != "" {
				res[id] = target
			}
		}
	}
	return res
}

func parseSharedStrings(data []byte) []string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var stringsList []string
	var inT bool
	var inRPh bool
	var currentText strings.Builder

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := tok.(type) {
		case xml.StartElement:
			if elem.Name.Local == "rPh" {
				inRPh = true
			} else if elem.Name.Local == "t" && !inRPh {
				inT = true
			} else if elem.Name.Local == "si" {
				currentText.Reset()
				inRPh = false
			}
		case xml.EndElement:
			if elem.Name.Local == "rPh" {
				inRPh = false
			} else if elem.Name.Local == "t" {
				inT = false
			} else if elem.Name.Local == "si" {
				stringsList = append(stringsList, currentText.String())
			}
		case xml.CharData:
			if inT && !inRPh {
				currentText.Write(elem)
			}
		}
	}
	return stringsList
}

func parseWorkbookSheets(data []byte) []SheetInfo {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var res []SheetInfo

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			var name, relID, sheetID string
			for _, a := range se.Attr {
				if a.Name.Local == "name" {
					name = a.Value
				} else if a.Name.Local == "id" {
					relID = a.Value
				} else if a.Name.Local == "sheetId" {
					sheetID = a.Value
				}
			}
			if name != "" {
				res = append(res, SheetInfo{
					Name:    name,
					RelID:   relID,
					SheetID: sheetID,
				})
			}
		}
	}
	return res
}

func parseSheetData(data []byte, sharedStrings []string) (string, int, int) {
	decoder := xml.NewDecoder(bytes.NewReader(data))

	var rows [][]string
	var currentRow []string
	var currentCellVal strings.Builder
	var currentCellType string
	var currentCellRef string
	var inV bool
	var inIS bool
	var inT bool

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			switch elem.Name.Local {
			case "row":
				currentRow = nil

			case "c":
				currentCellVal.Reset()
				currentCellType = ""
				currentCellRef = ""
				for _, a := range elem.Attr {
					if a.Name.Local == "t" {
						currentCellType = a.Value
					} else if a.Name.Local == "r" {
						currentCellRef = a.Value
					}
				}

			case "v":
				inV = true

			case "is":
				inIS = true

			case "t":
				if inIS {
					inT = true
				}
			}

		case xml.EndElement:
			switch elem.Name.Local {
			case "v":
				inV = false

			case "t":
				inT = false

			case "is":
				inIS = false

			case "c":
				val := currentCellVal.String()
				switch currentCellType {
				case "s":
					if idx, err := strconv.Atoi(val); err == nil && idx >= 0 && idx < len(sharedStrings) {
						val = sharedStrings[idx]
					}
				case "b":
					if val == "1" {
						val = "TRUE"
					} else {
						val = "FALSE"
					}
				case "", "n":
					// Format numeric floating precision artifacts cleanly
					if strings.Contains(val, ".") {
						if f, err := strconv.ParseFloat(val, 64); err == nil {
							rounded := math.Round(f*1e6) / 1e6
							val = strconv.FormatFloat(rounded, 'f', -1, 64)
						}
					}
				}

				colIdx := colRefToIndex(currentCellRef)
				for len(currentRow) < colIdx {
					currentRow = append(currentRow, "")
				}
				currentRow = append(currentRow, cleanExcelCell(val))

			case "row":
				hasVal := false
				for _, c := range currentRow {
					if strings.TrimSpace(c) != "" {
						hasVal = true
						break
					}
				}
				if hasVal {
					rows = append(rows, currentRow)
				}
			}

		case xml.CharData:
			if inV || inT {
				currentCellVal.Write(elem)
			}
		}
	}

	if len(rows) == 0 {
		return "*（当前工作表为空）*", 0, 0
	}

	maxRows := 600
	totalRows := len(rows)
	truncated := false
	if totalRows > maxRows {
		rows = rows[:maxRows]
		truncated = true
	}

	maxCols := 0
	for _, r := range rows {
		if len(r) > maxCols {
			maxCols = len(r)
		}
	}

	if maxCols == 0 {
		return "*（当前工作表为空）*", 0, 0
	}

	var sb strings.Builder

	headerRow := rows[0]
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		val := ""
		if i < len(headerRow) {
			val = headerRow[i]
		}
		if val == "" {
			val = fmt.Sprintf("列 %s", colIndexToLetters(i))
		}
		sb.WriteString(" " + val + " |")
	}
	sb.WriteString("\n|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	for _, r := range rows[1:] {
		sb.WriteString("|")
		for i := 0; i < maxCols; i++ {
			val := ""
			if i < len(r) {
				val = r[i]
			}
			if val == "" {
				val = " "
			}
			sb.WriteString(" " + val + " |")
		}
		sb.WriteString("\n")
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n> [!NOTE]\n> 表格数据量较大（共 %d 行），已展示前 %d 行。\n", totalRows, maxRows))
	}

	return sb.String(), totalRows, maxCols
}

func cleanExcelCell(text string) string {
	t := strings.TrimSpace(text)
	t = strings.ReplaceAll(t, "|", "\\|")
	t = strings.ReplaceAll(t, "\n", "<br>")
	return t
}

var colRefRegex = regexp.MustCompile(`^([A-Z]+)`)

func colRefToIndex(ref string) int {
	matches := colRefRegex.FindStringSubmatch(strings.ToUpper(ref))
	if len(matches) < 2 {
		return 0
	}
	colStr := matches[1]
	idx := 0
	for _, ch := range colStr {
		idx = idx*26 + int(ch-'A'+1)
	}
	return idx - 1
}

func colIndexToLetters(index int) string {
	var res []rune
	for index >= 0 {
		res = append([]rune{rune('A' + (index % 26))}, res...)
		index = (index / 26) - 1
	}
	return string(res)
}
