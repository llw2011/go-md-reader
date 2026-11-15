package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultCatalogHasPinnedMITAddons(t *testing.T) {
	cat := DefaultCatalog()
	if len(cat) != 2 {
		t.Fatalf("expected 2 curated addons, got %d", len(cat))
	}
	for _, spec := range cat {
		if spec.License != "MIT" || len(spec.Files) == 0 {
			t.Fatalf("unexpected spec: %+v", spec)
		}
		for _, f := range spec.Files {
			if len(f.SHA256) != 64 || f.Size <= 0 {
				t.Fatalf("expected pinned 64-hex SHA256 and size for %s/%s", spec.ID, f.RelPath)
			}
		}
	}
}

func TestInstallToggleResolveAndRemove(t *testing.T) {
	jsPayload := []byte("console.log('katex loaded');")
	cssPayload := []byte(".katex { font-size: 1em; }")
	sumHex := func(b []byte) string {
		s := sha256.Sum256(b)
		return hex.EncodeToString(s[:])
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/katex/katex.min.js":
			_, _ = w.Write(jsPayload)
		case "/katex/katex.min.css":
			_, _ = w.Write(cssPayload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	catalog := []AddonSpec{
		{
			ID:      "katex",
			Name:    "KaTeX",
			Version: "0.19.1",
			License: "MIT",
			Files: []AddonFile{
				{RelPath: "katex.min.js", URLPath: "/katex/katex.min.js", Size: int64(len(jsPayload)), SHA256: sumHex(jsPayload)},
				{RelPath: "katex.min.css", URLPath: "/katex/katex.min.css", Size: int64(len(cssPayload)), SHA256: sumHex(cssPayload)},
			},
		},
	}

	root := t.TempDir()
	mgr := NewManager(root, srv.URL, catalog)

	var events []InstallProgress
	status, err := mgr.Install(context.Background(), "katex", func(p InstallProgress) {
		events = append(events, p)
	})
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if !status.Installed || !status.Enabled {
		t.Fatalf("expected installed & enabled status, got %+v", status)
	}
	if len(events) == 0 || events[len(events)-1].Phase != "ready" || events[len(events)-1].Percent != 100 {
		t.Fatalf("expected progress ending at ready 100%%, got %+v", events)
	}

	resolved, ok := mgr.ResolveFile("katex", "katex.min.js")
	if !ok || !strings.HasSuffix(resolved, filepath.Join("katex", "katex.min.js")) {
		t.Fatalf("ResolveFile failed: %q, ok=%v", resolved, ok)
	}
	if _, badOK := mgr.ResolveFile("katex", "../state.json"); badOK {
		t.Fatal("expected traversal path to be rejected by ResolveFile")
	}

	// Disable should persist and block ResolveFile without deleting files
	list, err := mgr.SetEnabled("katex", false)
	if err != nil || list[0].Enabled || !list[0].Installed {
		t.Fatalf("expected Installed=true Enabled=false after disable, got %+v, %v", list, err)
	}
	if _, ok := mgr.ResolveFile("katex", "katex.min.js"); ok {
		t.Fatal("expected ResolveFile to deny disabled addon")
	}

	// Remove deletes disk cache cleanly
	list2, err := mgr.Remove("katex")
	if err != nil || list2[0].Installed || list2[0].Enabled {
		t.Fatalf("expected removed status, got %+v, %v", list2, err)
	}
	if _, err := os.Stat(filepath.Join(root, "katex")); !os.IsNotExist(err) {
		t.Fatalf("expected addon folder removed from disk")
	}
}

func TestInstallRejectsSHA256Mismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tampered-script"))
	}))
	defer srv.Close()

	root := t.TempDir()
	catalog := []AddonSpec{
		{
			ID:      "mermaid",
			Name:    "Mermaid",
			License: "MIT",
			Files: []AddonFile{
				{RelPath: "mermaid.min.js", URLPath: "/mermaid.min.js", Size: 15, SHA256: strings.Repeat("0", 64)},
			},
		},
	}
	mgr := NewManager(root, srv.URL, catalog)

	if _, err := mgr.Install(context.Background(), "mermaid", nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected SHA-256 mismatch error, got %v", err)
	}
	if _, ok := mgr.ResolveFile("mermaid", "mermaid.min.js"); ok {
		t.Fatal("expected rejected file not to be resolvable")
	}
}
