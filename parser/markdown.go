package parser

import (
	"bytes"
	"net/url"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
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
	md goldmark.Markdown
}

// NewMarkdownParser creates a parser with GFM, Chroma syntax highlighting, and auto IDs
func NewMarkdownParser() *MarkdownParser {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
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
	return &MarkdownParser{md: md}
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
	reader := text.NewReader(source)
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
			titleText := strings.TrimSpace(extractNodeText(h, source))
			if titleText != "" {
				toc = append(toc, TOCItem{
					Level: h.Level,
					ID:    id,
					Title: titleText,
				})
			}

		case ast.KindImage:
			img := n.(*ast.Image)
			img.Destination = []byte(rewriteLocalMediaURL(string(img.Destination)))
		}

		return ast.WalkContinue, nil
	})

	var htmlBuf bytes.Buffer
	if err := p.md.Renderer().Render(&htmlBuf, source, doc); err != nil {
		return nil, err
	}

	runeCount := len([]rune(string(source)))

	return &ParseResult{
		HTML:      htmlBuf.String(),
		TOC:       toc,
		WordCount: runeCount,
	}, nil
}

// rewriteLocalMediaURL converts relative local image paths into properly segment-escaped /media/ URLs.
func rewriteLocalMediaURL(dest string) string {
	lower := strings.ToLower(strings.TrimSpace(dest))
	if lower == "" ||
		strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "data:") ||
		strings.HasPrefix(lower, "//") ||
		strings.HasPrefix(lower, "/media/") {
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
	return "/media/" + strings.Join(escapedSegs, "/") + suffix
}

// GenerateChromaCSS generates CSS definitions for Chroma syntax highlighting
func GenerateChromaCSS(styleName string) string {
	s := styles.Get(styleName)
	if s == nil {
		s = styles.Fallback
	}
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	var buf bytes.Buffer
	_ = formatter.WriteCSS(&buf, s)
	return buf.String()
}
