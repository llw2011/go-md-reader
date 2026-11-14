package converter

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
