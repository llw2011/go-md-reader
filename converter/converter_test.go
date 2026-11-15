package converter

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func TestDocxConversion(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "*logistics_k3*.docx"))
	if len(files) == 0 {
		t.Skip("No docx test file found on Desktop")
	}

	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	res := ConvertToMarkdown(filepath.Base(files[0]), data)
	if !res.IsConverted {
		t.Fatalf("Expected IsConverted=true, got false")
	}
	if len(res.Markdown) == 0 {
		t.Fatalf("Expected non-empty markdown output")
	}

	if !strings.Contains(res.Markdown, "蜀中商学院") {
		t.Errorf("Expected extracted text to contain '蜀中商学院'")
	}
}

func TestXlsxConversion(t *testing.T) {
	// Create minimal valid xlsx in memory to test
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// xl/workbook.xml
	wbf, _ := zw.Create("xl/workbook.xml")
	wbf.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheets>
    <sheet name="Sheet1" sheetId="1" id="rId1"/>
  </sheets>
</workbook>`))

	// xl/sharedStrings.xml
	ssf, _ := zw.Create("xl/sharedStrings.xml")
	ssf.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">
  <si><t>产品名称</t></si>
  <si><t>销售额</t></si>
</sst>`))

	// xl/worksheets/sheet1.xml
	wsf, _ := zw.Create("xl/worksheets/sheet1.xml")
	wsf.Write([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
  <sheetData>
    <row r="1">
      <c r="A1" t="s"><v>0</v></c>
      <c r="B1" t="s"><v>1</v></c>
    </row>
    <row r="2">
      <c r="A2" t="inlineStr"><is><t>金盖克</t></is></c>
      <c r="B2"><v>98000</v></c>
    </row>
  </sheetData>
</worksheet>`))

	zw.Close()

	res := ConvertToMarkdown("sales_report.xlsx", buf.Bytes())
	if !res.IsConverted {
		t.Fatalf("Expected IsConverted=true for xlsx")
	}

	if !strings.Contains(res.Markdown, "产品名称") || !strings.Contains(res.Markdown, "金盖克") {
		t.Errorf("Expected markdown to contain sheet data, got:\n%s", res.Markdown)
	}
}

func TestHTMLToMarkdownConversion(t *testing.T) {
	htmlDoc := `<!DOCTYPE html><html><head><title>Ignored</title><style>body{}</style></head><body><h1>项目简介</h1><p>访问 <a href="https://example.com">官网</a> 获取<strong>更多</strong>信息。</p></body></html>`
	res := ConvertToMarkdown("guide.html", []byte(htmlDoc))
	if !res.IsConverted || res.FormatNote != "HTML 网页转换" {
		t.Fatalf("expected HTML conversion result, got %+v", res)
	}
	if !strings.Contains(res.Markdown, "# 项目简介") || !strings.Contains(res.Markdown, "[官网](https://example.com)") {
		t.Fatalf("unexpected HTML->Markdown output:\n%s", res.Markdown)
	}
}

func TestEpubConversion(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	cw, _ := zw.Create("META-INF/container.xml")
	_, _ = cw.Write([]byte(`<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`))

	ow, _ := zw.Create("OEBPS/content.opf")
	_, _ = ow.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>Go 并发之美</dc:title>
    <dc:creator>Gopher</dc:creator>
  </metadata>
  <manifest>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="c1"/>
    <itemref idref="c2"/>
  </spine>
</package>`))

	ch1, _ := zw.Create("OEBPS/ch1.xhtml")
	_, _ = ch1.Write([]byte(`<html><body><h2>第一章 Goroutine</h2><p>轻量协程示例。</p></body></html>`))

	ch2, _ := zw.Create("OEBPS/ch2.xhtml")
	_, _ = ch2.Write([]byte(`<html><body><h2>第二章 Channel</h2><p>通过通信共享内存。</p></body></html>`))

	zw.Close()

	res := ConvertToMarkdown("concurrency.epub", buf.Bytes())
	if !res.IsConverted || res.FormatNote != "EPUB 电子书转换" {
		t.Fatalf("expected EPUB conversion result, got %+v", res)
	}
	for _, want := range []string{"# Go 并发之美", "Gopher", "## 第一章 Goroutine", "## 第二章 Channel", "通过通信共享内存"} {
		if !strings.Contains(res.Markdown, want) {
			t.Fatalf("expected %q in EPUB Markdown:\n%s", want, res.Markdown)
		}
	}
}

func TestDecodeTextAndGB2312Files(t *testing.T) {
	sampleMD := "# 中文标题\n\n这是一个 GB2312 编码的文档：你好，世界！\n"
	gbkBytes, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(sampleMD))
	if err != nil {
		t.Fatalf("encode GBK failed: %v", err)
	}

	resMD := ConvertToMarkdown("notes.md", gbkBytes)
	if resMD.Markdown != sampleMD {
		t.Fatalf("expected decoded GB2312 markdown %q, got %q", sampleMD, resMD.Markdown)
	}

	resTXT := ConvertToMarkdown("readme.txt", gbkBytes)
	if resTXT.Markdown != sampleMD {
		t.Fatalf("expected decoded GB2312 txt %q, got %q", sampleMD, resTXT.Markdown)
	}

	sampleCSV := "姓名,部门\n张三,研发部\n"
	gbkCSV, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(sampleCSV))
	resCSV := ConvertToMarkdown("staff.csv", gbkCSV)
	if !strings.Contains(resCSV.Markdown, "张三") || !strings.Contains(resCSV.Markdown, "研发部") {
		t.Fatalf("expected decoded GB2312 CSV content, got:\n%s", resCSV.Markdown)
	}

	sampleHTML := "<html><body><h1>网页标题</h1><p>中文内容测试</p></body></html>"
	gbkHTML, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(sampleHTML))
	resHTML := ConvertToMarkdown("page.html", gbkHTML)
	if !strings.Contains(resHTML.Markdown, "网页标题") || !strings.Contains(resHTML.Markdown, "中文内容测试") {
		t.Fatalf("expected decoded GB2312 HTML content, got:\n%s", resHTML.Markdown)
	}

	utf16LE, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte("UTF16 测试文本"))
	if err != nil {
		t.Fatalf("encode UTF16 failed: %v", err)
	}
	if got := DecodeTextToUTF8(utf16LE); got != "UTF16 测试文本" {
		t.Fatalf("expected UTF16 LE decoded string, got %q", got)
	}

	utf8BOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte("BOM 中文")...)
	if got := DecodeTextToUTF8(utf8BOM); got != "BOM 中文" {
		t.Fatalf("expected UTF-8 BOM stripped string, got %q", got)
	}
}
