package parser

import (
	"bytes"
	stdhtml "html"
	"net/url"
	"strings"

	mathjax "github.com/litao91/goldmark-mathjax"
	"github.com/yuin/goldmark"
	emoji "github.com/yuin/goldmark-emoji"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// TOCItem represents a Table of Contents entry
type TOCItem struct {
	Level int    `json:"level"`
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ParseResult holds the rendered HTML and extracted TOC
type ParseResult struct {
	HTML      string    `json:"html"`
	TOC       []TOCItem `json:"toc"`
	WordCount int       `json:"wordCount"`
}

// MarkdownParser encapsulates goldmark parser instance
type MarkdownParser struct {
	md          goldmark.Markdown
	mediaPrefix string
}

// NewMarkdownParser creates a parser with GFM, Chroma syntax highlighting, and auto IDs
func NewMarkdownParser() *MarkdownParser {
	return NewMarkdownParserWithMediaPrefix("/media/")
}

// NewMarkdownParserWithMediaPrefix creates a parser that rewrites relative image links using prefix.
func NewMarkdownParserWithMediaPrefix(prefix string) *MarkdownParser {
	cleanPrefix := strings.TrimSpace(prefix)
	if cleanPrefix == "" {
		cleanPrefix = "/media/"
	} else if !strings.HasSuffix(cleanPrefix, "/") {
		cleanPrefix += "/"
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			emoji.Emoji,
			mathjax.MathJax,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(),
		),
	)
	return &MarkdownParser{md: md, mediaPrefix: cleanPrefix}
}

// extractNodeText recursively extracts plain text from an AST node
func extractNodeText(n ast.Node, source []byte) string {
	var buf bytes.Buffer
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			buf.Write(t.Segment.Value(source))
		} else {
			buf.WriteString(extractNodeText(c, source))
		}
	}
	return buf.String()
}

// Parse processes raw markdown and returns HTML + TOC + word count
func (p *MarkdownParser) Parse(source []byte) (*ParseResult, error) {
	frontmatterHTML, body := extractFrontmatter(source)
	reader := text.NewReader(body)
	doc := p.md.Parser().Parse(reader)

	var toc []TOCItem

	// Walk AST to extract TOC and rewrite relative local image paths to /media/
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		switch n.Kind() {
		case ast.KindHeading:
			h := n.(*ast.Heading)
			idAttr, exists := h.AttributeString("id")
			id := ""
			if exists {
				if b, ok := idAttr.([]byte); ok {
					id = string(b)
				}
			}
			titleText := strings.TrimSpace(extractNodeText(h, body))
			if titleText != "" {
				toc = append(toc, TOCItem{
					Level: h.Level,
					ID:    id,
					Title: titleText,
				})
			}

		case ast.KindImage:
			img := n.(*ast.Image)
			img.Destination = []byte(rewriteLocalMediaURLWithPrefix(string(img.Destination), p.mediaPrefix))
		}

		return ast.WalkContinue, nil
	})

	var htmlBuf bytes.Buffer
	if frontmatterHTML != "" {
		htmlBuf.WriteString(frontmatterHTML)
	}
	if err := p.md.Renderer().Render(&htmlBuf, body, doc); err != nil {
		return nil, err
	}

	runeCount := len([]rune(string(source)))

	return &ParseResult{
		HTML:      htmlBuf.String(),
		TOC:       toc,
		WordCount: runeCount,
	}, nil
}

type fmEntry struct {
	Key   string
	Value string
}

// extractFrontmatter parses a leading YAML frontmatter block into a safe HTML summary card.
func extractFrontmatter(source []byte) (string, []byte) {
	trimmed := bytes.TrimPrefix(source, []byte("\xEF\xBB\xBF"))
	normalized := strings.ReplaceAll(string(trimmed), "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return "", source
	}

	rest := normalized[4:]
	endIdx := strings.Index(rest, "\n---\n")
	endLen := 5
	if endIdx < 0 {
		if strings.HasSuffix(rest, "\n---") {
			endIdx = len(rest) - 4
			endLen = 4
		} else {
			return "", source
		}
	}

	block := rest[:endIdx]
	lines := strings.Split(block, "\n")
	var entries []fmEntry
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		colonIdx := strings.IndexByte(t, ':')
		if colonIdx <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:colonIdx])
		val := strings.TrimSpace(t[colonIdx+1:])
		val = strings.Trim(val, `"'`)
		if key != "" {
			entries = append(entries, fmEntry{Key: key, Value: val})
		}
	}

	if len(entries) == 0 {
		return "", source
	}

	var sb strings.Builder
	sb.WriteString(`<details class="doc-frontmatter" open><summary>📋 文档属性 (Frontmatter)</summary><div class="frontmatter-grid">`)
	for _, e := range entries {
		sb.WriteString(`<div class="fm-item"><span class="fm-key">`)
		sb.WriteString(stdhtml.EscapeString(e.Key))
		sb.WriteString(`</span><span class="fm-val">`)
		sb.WriteString(stdhtml.EscapeString(e.Value))
		sb.WriteString(`</span></div>`)
	}
	sb.WriteString(`</div></details>`)
	return sb.String(), []byte(rest[endIdx+endLen:])
}

// rewriteLocalMediaURL converts relative local image paths into properly segment-escaped /media/ URLs.
func rewriteLocalMediaURL(dest string) string {
	return rewriteLocalMediaURLWithPrefix(dest, "/media/")
}

func rewriteLocalMediaURLWithPrefix(dest, prefix string) string {
	lower := strings.ToLower(strings.TrimSpace(dest))
	if lower == "" ||
		strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "//") ||
		strings.HasPrefix(lower, "/media/") ||
		strings.HasPrefix(lower, "media/") {
		return dest
	}

	suffix := ""
	pathPart := dest
	if idx := strings.IndexAny(pathPart, "?#"); idx >= 0 {
		suffix = pathPart[idx:]
		pathPart = pathPart[:idx]
	}

	pathPart = strings.ReplaceAll(pathPart, "\\", "/")
	if unescaped, err := url.PathUnescape(pathPart); err == nil {
		pathPart = unescaped
	}
	for strings.HasPrefix(pathPart, "./") {
		pathPart = strings.TrimPrefix(pathPart, "./")
	}
	pathPart = strings.TrimLeft(pathPart, "/")

	rawSegs := strings.Split(pathPart, "/")
	escapedSegs := make([]string, 0, len(rawSegs))
	for _, seg := range rawSegs {
		if seg == "" || seg == "." {
			continue
		}
		escapedSegs = append(escapedSegs, url.PathEscape(seg))
	}
	if len(escapedSegs) == 0 {
		return dest
	}
	return prefix + strings.Join(escapedSegs, "/") + suffix
}
