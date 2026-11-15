package parser

import (
	"strings"
	"testing"
)

func TestRewriteLocalMediaURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"images/pic 1.png", "/media/images/pic%201.png"},
		{`.\sub\my image.jpg`, "/media/sub/my%20image.jpg"},
		{"assets/already%20escaped.png?w=100", "/media/assets/already%20escaped.png?w=100"},
		{"https://example.com/a.png", "https://example.com/a.png"},
		{"data:image/png;base64,AAAA", "data:image/png;base64,AAAA"},
	}

	for _, tc := range cases {
		got := rewriteLocalMediaURL(tc.in)
		if got != tc.want {
			t.Errorf("rewriteLocalMediaURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParsePreservesMediaSubdirSlashesAndTOC(t *testing.T) {
	p := NewMarkdownParser()
	res, err := p.Parse([]byte("# Title One\n\n![alt](assets/folder/shot.png)\n\n![alt2](<assets/folder/screen shot.png>)\n"))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(res.TOC) != 1 || res.TOC[0].Title != "Title One" {
		t.Fatalf("unexpected TOC: %+v", res.TOC)
	}
	if !strings.Contains(res.HTML, `src="/media/assets/folder/shot.png"`) {
		t.Fatalf("expected preserved subdirectory slash in image src, got HTML:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, `src="/media/assets/folder/screen%20shot.png"`) {
		t.Fatalf("expected encoded filename with preserved slash in image src, got HTML:\n%s", res.HTML)
	}
}

func TestParseYAMLFrontmatterAndFootnote(t *testing.T) {
	p := NewMarkdownParser()
	md := "---\ntitle: \"Release Notes\"\nauthor: <script>alert(1)</script>\ndate: 2026-11-14\n---\n\n# Heading\n\nBody text[^1].\n\n[^1]: Footnote body\n"
	res, err := p.Parse([]byte(md))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if !strings.Contains(res.HTML, `doc-frontmatter`) || !strings.Contains(res.HTML, `Release Notes`) {
		t.Fatalf("expected frontmatter metadata block, got HTML:\n%s", res.HTML)
	}
	if strings.Contains(res.HTML, `<script>alert(1)</script>`) {
		t.Fatalf("expected HTML escaping inside frontmatter, got XSS HTML:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, `footnotes`) || !strings.Contains(res.HTML, `Footnote body`) {
		t.Fatalf("expected GFM footnote rendering, got HTML:\n%s", res.HTML)
	}
}

func TestParseThematicBreakNotTreatedAsFrontmatter(t *testing.T) {
	p := NewMarkdownParser()
	md := "# Intro\n\n---\n\nParagraph\n"
	res, err := p.Parse([]byte(md))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if strings.Contains(res.HTML, `doc-frontmatter`) {
		t.Fatalf("unexpected frontmatter block for horizontal rule")
	}
}

func TestParseEmojiShortcodesAndMathDelimiters(t *testing.T) {
	p := NewMarkdownParser()
	md := "Launch :rocket: with $a_i + b_j = c_k$\n\n$$\n\\sum_{i=1}^{n} x_i^2\n$$\n"
	res, err := p.Parse([]byte(md))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if strings.Contains(res.HTML, "cdn.jsdelivr.net") || strings.Contains(res.HTML, ":rocket:") {
		t.Fatalf("expected offline emoji entity without CDN or literal :rocket:, got HTML:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, `class="math inline"`) || !strings.Contains(res.HTML, `a_i + b_j = c_k`) {
		t.Fatalf("expected preserved inline math without <em> mangling, got HTML:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, `class="math display"`) || !strings.Contains(res.HTML, `\sum_{i=1}^{n} x_i^2`) {
		t.Fatalf("expected preserved block math, got HTML:\n%s", res.HTML)
	}
}

func TestCodeHighlightingUsesThemeClassesAndPreservesMermaid(t *testing.T) {
	p := NewMarkdownParser()
	md := "```go\npackage main\nfunc main() { println(\"hi\") }\n```\n\n```mermaid\ngraph TD\n  A-->B\n```\n"
	res, err := p.Parse([]byte(md))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if !strings.Contains(res.HTML, `class="chroma"`) {
		t.Fatalf("expected CSS class-based .chroma highlighting, got HTML:\n%s", res.HTML)
	}
	if strings.Contains(res.HTML, `style="color:`) {
		t.Fatalf("unexpected inline style color attributes on highlighted code, got HTML:\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, `language-mermaid`) {
		t.Fatalf("expected preserved mermaid code fence class, got HTML:\n%s", res.HTML)
	}
}
