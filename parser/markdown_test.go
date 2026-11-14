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
