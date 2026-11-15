package converter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type epubPackage struct {
	Metadata struct {
		Title   string `xml:"title"`
		Creator string `xml:"creator"`
	} `xml:"metadata"`
	Manifest []struct {
		ID        string `xml:"id,attr"`
		Href      string `xml:"href,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"manifest>item"`
	Spine []struct {
		IDRef  string `xml:"idref,attr"`
		Linear string `xml:"linear,attr"`
	} `xml:"spine>itemref"`
}

func isEpubZip(data []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false
	}
	for _, f := range zr.File {
		if f.Name == "META-INF/container.xml" {
			return true
		}
	}
	return false
}

// HTMLToMarkdown converts standalone HTML markup into clean Markdown using html-to-markdown/v2.
func HTMLToMarkdown(raw string) (string, error) {
	md, err := htmltomarkdown.ConvertString(raw)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(md), nil
}

// EpubToMarkdown extracts metadata and ordered spine chapters from an EPUB archive into Markdown.
func EpubToMarkdown(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("无法读取 EPUB 压缩包: %w", err)
	}

	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))] = f
	}

	opfPath := findOPFPath(files, zr.File)
	if opfPath == "" {
		return "", fmt.Errorf("未找到 EPUB OPF 清单文件")
	}
	opfFile := files[opfPath]
	if opfFile == nil {
		return "", fmt.Errorf("OPF 文件不存在: %s", opfPath)
	}
	opfData, err := readZipEntryLimited(opfFile, maxOfficeXMLBytes)
	if err != nil {
		return "", err
	}

	var pkg epubPackage
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return "", fmt.Errorf("解析 EPUB OPF 失败: %w", err)
	}

	manifest := make(map[string]string, len(pkg.Manifest))
	opfDir := path.Dir(opfPath)
	for _, item := range pkg.Manifest {
		if item.ID == "" || item.Href == "" {
			continue
		}
		mt := strings.ToLower(item.MediaType)
		hrefLower := strings.ToLower(item.Href)
		if mt != "" && !strings.Contains(mt, "html") && !strings.HasSuffix(hrefLower, ".xhtml") && !strings.HasSuffix(hrefLower, ".html") && !strings.HasSuffix(hrefLower, ".htm") {
			continue
		}
		target := strings.SplitN(item.Href, "#", 2)[0]
		if opfDir != "." && opfDir != "" {
			target = path.Join(opfDir, target)
		}
		manifest[item.ID] = path.Clean(target)
	}

	var sections []string
	if title := strings.TrimSpace(pkg.Metadata.Title); title != "" {
		header := "# " + title
		if author := strings.TrimSpace(pkg.Metadata.Creator); author != "" {
			header += "\n\n> 作者：" + author
		}
		sections = append(sections, header)
	}

	seen := make(map[string]bool)
	for _, ref := range pkg.Spine {
		if strings.EqualFold(ref.Linear, "no") {
			continue
		}
		chapterPath := manifest[ref.IDRef]
		if chapterPath == "" || seen[chapterPath] {
			continue
		}
		seen[chapterPath] = true
		zf := files[chapterPath]
		if zf == nil {
			continue
		}
		rawHTML, err := readZipEntryLimited(zf, maxOfficeXMLBytes)
		if err != nil {
			continue
		}
		chapterMD, err := HTMLToMarkdown(string(rawHTML))
		if err != nil || chapterMD == "" {
			continue
		}
		sections = append(sections, chapterMD)
	}

	if len(sections) == 0 {
		return "", fmt.Errorf("未从 EPUB 中提取到可读章节")
	}
	return strings.Join(sections, "\n\n---\n\n") + "\n", nil
}

func findOPFPath(files map[string]*zip.File, list []*zip.File) string {
	if cf := files["META-INF/container.xml"]; cf != nil {
		if data, err := readZipEntryLimited(cf, maxOfficeXMLBytes); err == nil {
			var c epubContainer
			if xml.Unmarshal(data, &c) == nil {
				for _, rf := range c.Rootfiles {
					if p := strings.TrimSpace(rf.FullPath); p != "" {
						return path.Clean(strings.ReplaceAll(p, "\\", "/"))
					}
				}
			}
		}
	}
	for _, f := range list {
		if strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
			return path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		}
	}
	return ""
}
