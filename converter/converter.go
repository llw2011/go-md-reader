package converter

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const maxOfficeXMLBytes = 32 * 1024 * 1024

func readZipEntryLimited(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("zip entry %s exceeds max allowed size (%d bytes)", f.Name, limit)
	}
	return data, nil
}

// Result represents the outcome of content conversion
type Result struct {
	Markdown    string
	IsConverted bool
	FormatNote  string
}

// ConvertToMarkdown inspects filename and content bytes, converting Office/Code/Data documents to Markdown
func ConvertToMarkdown(filename string, data []byte) Result {
	ext := strings.ToLower(filepath.Ext(filename))

	// 1. Check for modern OpenXML zip formats (.docx, .xlsx, .pptx)
	isZip := len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && data[2] == 0x03 && data[3] == 0x04

	if ext == ".docx" || (isZip && isDocxZip(data)) {
		md, err := DocxToMarkdown(bytes.NewReader(data), int64(len(data)))
		if err == nil && len(strings.TrimSpace(md)) > 0 {
			banner := "> [!TIP]\n> 📄 **已自动将 Word 文档 (.docx) 转换为 Markdown 视图**（可点击工具栏『导出 -> 另存为 .md』保存转换结果）。\n\n"
			return Result{
				Markdown:    banner + md,
				IsConverted: true,
				FormatNote:  "Word 文档转换",
			}
		}
	}

	if ext == ".xlsx" || (isZip && isXlsxZip(data)) {
		md, err := XlsxToMarkdown(bytes.NewReader(data), int64(len(data)))
		if err == nil && len(strings.TrimSpace(md)) > 0 {
			banner := "> [!TIP]\n> 📊 **已自动将 Excel 表格 (.xlsx) 转换为 Markdown 数据表**（点击表头可排序，悬浮表格可一键复制，点击『↔️ 宽屏』可展开全宽视图）。\n\n"
			return Result{
				Markdown:    banner + md,
				IsConverted: true,
				FormatNote:  "Excel 表格转换",
			}
		}
	}

	if ext == ".pptx" || (isZip && isPptxZip(data)) {
		md, err := PptxToMarkdown(bytes.NewReader(data), int64(len(data)))
		if err == nil && len(strings.TrimSpace(md)) > 0 {
			banner := "> [!TIP]\n> 🖥️ **已自动将 PowerPoint 演示文稿 (.pptx) 提取为 Markdown 幻灯片大纲**。\n\n"
			return Result{
				Markdown:    banner + md,
				IsConverted: true,
				FormatNote:  "PPT 演示文稿转换",
			}
		}
	}

	// 2. CSV / TSV Table Conversion
	if ext == ".csv" || ext == ".tsv" {
		sep := ','
		if ext == ".tsv" {
			sep = '\t'
		}
		md := delimitedToMarkdown(data, sep)
		if len(md) > 0 {
			banner := fmt.Sprintf("> [!TIP]\n> 📑 **已将 %s 数据表格式化为 Markdown 表格视图**（点击表头可排序）。\n\n", strings.ToUpper(strings.TrimPrefix(ext, ".")))
			return Result{
				Markdown:    banner + md,
				IsConverted: true,
				FormatNote:  strings.ToUpper(strings.TrimPrefix(ext, ".")) + " 表格转换",
			}
		}
	}

	// 3. JSON Auto-Formatting & Highlighting
	if ext == ".json" {
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, data, "", "  "); err == nil {
			md := fmt.Sprintf("# 📄 JSON 数据预览: `%s`\n\n```json\n%s\n```\n", filename, prettyJSON.String())
			return Result{
				Markdown:    md,
				IsConverted: true,
				FormatNote:  "JSON 格式化视图",
			}
		}
	}

	// 4. Source Code & Config Files Highlighting
	if lang := detectCodeLanguage(ext); lang != "" {
		md := fmt.Sprintf("# 💻 代码与配置预览: `%s`\n\n```%s\n%s\n```\n", filename, lang, string(data))
		return Result{
			Markdown:    md,
			IsConverted: true,
			FormatNote:  strings.ToUpper(lang) + " 代码视图",
		}
	}

	// 5. Legacy binary Office files (.doc, .xls, .ppt, CFBF format: D0 CF 11 E0)
	if len(data) >= 8 && data[0] == 0xD0 && data[1] == 0xCF && data[2] == 0x11 && data[3] == 0xE0 {
		text := extractLegacyOfficeText(data)
		banner := "> [!NOTE]\n> ⚠️ **检测到旧版二进制 Office 文件 (.doc / .xls)**\n> 本文件为 Office 97-2003 二进制格式。推荐使用 Office/WPS 另存为现代 `.docx` 或 `.xlsx` 以获取完整表格与样式。以下为自动提取的文本内容：\n\n"
		return Result{
			Markdown:    banner + text,
			IsConverted: true,
			FormatNote:  "旧版 Office 文本提取",
		}
	}

	// 6. Default: Standard Markdown / Plain text
	return Result{
		Markdown:    string(data),
		IsConverted: false,
		FormatNote:  "",
	}
}

func detectCodeLanguage(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".xml":
		return "xml"
	case ".sql":
		return "sql"
	case ".sh", ".bash":
		return "bash"
	case ".ps1":
		return "powershell"
	case ".bat", ".cmd":
		return "batch"
	case ".ini", ".conf":
		return "ini"
	case ".css":
		return "css"
	case ".html", ".htm":
		return "html"
	default:
		return ""
	}
}

func isDocxZip(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			return true
		}
	}
	return false
}

func isXlsxZip(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if f.Name == "xl/workbook.xml" {
			return true
		}
	}
	return false
}

func isPptxZip(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") {
			return true
		}
	}
	return false
}

// delimitedToMarkdown parses CSV/TSV data into Markdown table
func delimitedToMarkdown(data []byte, comma rune) string {
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = comma
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil || len(records) == 0 {
		return string(data)
	}

	maxCols := 0
	for _, row := range records {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	if maxCols == 0 {
		return ""
	}

	var sb strings.Builder
	header := records[0]
	sb.WriteString("|")
	for i := 0; i < maxCols; i++ {
		val := ""
		if i < len(header) {
			val = strings.TrimSpace(header[i])
		}
		if val == "" {
			val = fmt.Sprintf("列 %d", i+1)
		}
		val = strings.ReplaceAll(val, "|", "\\|")
		sb.WriteString(" " + val + " |")
	}
	sb.WriteString("\n|")
	for i := 0; i < maxCols; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	for _, row := range records[1:] {
		sb.WriteString("|")
		for i := 0; i < maxCols; i++ {
			val := ""
			if i < len(row) {
				val = strings.TrimSpace(row[i])
			}
			if val == "" {
				val = " "
			}
			val = strings.ReplaceAll(val, "|", "\\|")
			sb.WriteString(" " + val + " |")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// extractLegacyOfficeText extracts readable UTF-16 / UTF-8 strings from binary .doc / .xls files
func extractLegacyOfficeText(data []byte) string {
	var sb strings.Builder
	var currentRunes []rune

	for i := 0; i+1 < len(data); i += 2 {
		val := uint16(data[i]) | (uint16(data[i+1]) << 8)
		if (val >= 0x20 && val <= 0x7E) || (val >= 0x4E00 && val <= 0x9FFF) || val == '\n' || val == '\r' || val == '\t' {
			r := utf16.Decode([]uint16{val})[0]
			currentRunes = append(currentRunes, r)
		} else {
			if len(currentRunes) >= 4 {
				str := strings.TrimSpace(string(currentRunes))
				if str != "" && !isGarbageString(str) {
					sb.WriteString(str + "\n\n")
				}
			}
			currentRunes = nil
		}
	}

	if sb.Len() == 0 {
		var curBytes []byte
		for _, b := range data {
			if (b >= 32 && b <= 126) || b == '\n' || b == '\t' {
				curBytes = append(curBytes, b)
			} else {
				if len(curBytes) >= 6 && utf8.Valid(curBytes) {
					sb.WriteString(strings.TrimSpace(string(curBytes)) + "\n\n")
				}
				curBytes = nil
			}
		}
	}

	return strings.TrimSpace(sb.String())
}

func isGarbageString(s string) bool {
	if strings.Contains(s, "CompObj") || strings.Contains(s, "WordDocument") || strings.Contains(s, "SummaryInformation") {
		return true
	}
	return false
}
