package converter

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestXlsxSkipsPhoneticRubyText(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	wbf, _ := zw.Create("xl/workbook.xml")
	_, _ = wbf.Write([]byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="Data" sheetId="1" id="rId1"/></sheets></workbook>`))

	ssf, _ := zw.Create("xl/sharedStrings.xml")
	_, _ = ssf.Write([]byte(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
		<si><t>东京</t><rPh sb="0" eb="2"><t>トウキョウ</t></rPh></si>
	</sst>`))

	wsf, _ := zw.Create("xl/worksheets/sheet1.xml")
	_, _ = wsf.Write([]byte(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`))
	_ = zw.Close()

	md, err := XlsxToMarkdown(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("XlsxToMarkdown failed: %v", err)
	}
	if !strings.Contains(md, "东京") {
		t.Fatalf("expected 东京 in output, got:\n%s", md)
	}
	if strings.Contains(md, "トウキョウ") {
		t.Fatalf("expected phonetic <rPh> text to be excluded, got:\n%s", md)
	}
}

func TestDocxNestedTableAndLeadingSlashMedia(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	rels, _ := zw.Create("word/_rels/document.xml.rels")
	_, _ = rels.Write([]byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
		<Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/word/media/pic.png"/>
	</Relationships>`))

	img, _ := zw.Create("word/media/pic.png")
	_, _ = img.Write([]byte("fake-png-bytes"))

	doc, _ := zw.Create("word/document.xml")
	_, _ = doc.Write([]byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
		<w:body>
			<w:p><w:r><w:drawing><a:blip r:embed="rId5"/></w:drawing></w:r></w:p>
			<w:tbl>
				<w:tr><w:tc><w:p><w:r><w:t>OuterHeader</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>NestedHost</w:t></w:r></w:p></w:tc></w:tr>
				<w:tr><w:tc><w:p><w:r><w:t>Row1Col1</w:t></w:r></w:p></w:tc><w:tc>
					<w:tbl><w:tr><w:tc><w:p><w:r><w:t>InnerCell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
				</w:tc></w:tr>
			</w:tbl>
			<w:p><w:r><w:t>AfterTable</w:t></w:r></w:p>
		</w:body>
	</w:document>`))
	_ = zw.Close()

	md, err := DocxToMarkdown(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("DocxToMarkdown failed: %v", err)
	}
	if !strings.Contains(md, "data:image/png;base64,") {
		t.Fatalf("expected embedded image with leading /word/media path, got:\n%s", md)
	}
	if !strings.Contains(md, "Row1Col1") || !strings.Contains(md, "InnerCell") || !strings.Contains(md, "AfterTable") {
		t.Fatalf("expected intact outer and nested table plus trailing text, got:\n%s", md)
	}
}

func TestReadZipEntryLimitedRejectsOversize(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("big.xml")
	_, _ = f.Write([]byte("1234567890"))
	_ = zw.Close()

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readZipEntryLimited(zr.File[0], 5); err == nil {
		t.Fatal("expected error when zip entry exceeds size limit")
	}
}
