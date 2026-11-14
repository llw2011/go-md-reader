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
