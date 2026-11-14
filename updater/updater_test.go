package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v2.0.1", "v2.0.0", 1},
		{"2.1.0", "v2.0.9", 1},
		{"v2.0.0", "v2.0.0", 0},
		{"v2.0.0-beta.1", "v2.0.0", -1},
		{"v2.0.0", "v2.0.0-beta.1", 1},
		{"v1.9.9", "v2.0.0", -1},
	}
	for _, tc := range cases {
		if got := CompareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNormalizeRepo(t *testing.T) {
	if got, err := NormalizeRepo("https://github.com/alice/go-md-reader.git"); err != nil || got != "alice/go-md-reader" {
		t.Fatalf("unexpected normalize result: %q, %v", got, err)
	}
	if _, err := NormalizeRepo("https://evil.example.com/alice/repo"); err == nil {
		t.Fatal("expected error for non-github URL")
	}
	if _, err := NormalizeRepo("only-owner"); err == nil {
		t.Fatal("expected error for missing slash")
	}
}

func TestConfigMaskingAndPersistence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "ota.json")
	mgr := NewManager(cfgPath)

	view, err := mgr.SaveConfig("alice/go-md-reader", "ghp_1234567890abcdef", false, true)
	if err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	if !view.HasToken || view.TokenPreview != "ghp_****cdef" || view.Repo != "alice/go-md-reader" {
		t.Fatalf("unexpected view: %+v", view)
	}

	// Updating repo with empty token keeps existing token
	view2, err := mgr.SaveConfig("bob/go-md-reader", "", false, false)
	if err != nil || !view2.HasToken || view2.TokenPreview != "ghp_****cdef" {
		t.Fatalf("expected existing token preserved, got %+v, %v", view2, err)
	}

	// Clearing token wipes it
	view3, err := mgr.SaveConfig("bob/go-md-reader", "", true, false)
	if err != nil || view3.HasToken || view3.TokenPreview != "" {
		t.Fatalf("expected cleared token, got %+v, %v", view3, err)
	}
}

func TestCheckAndDownloadWithMockGitHub(t *testing.T) {
	payload := []byte("fake-new-exe-binary")
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/alice/go-md-reader/releases/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v2.1.0",
				Name:    "v2.1.0 Tray + OTA",
				Assets: []ghAsset{
					{
						ID:     1,
						Name:   "go-md-reader.exe",
						Size:   int64(len(payload)),
						URL:    srv.URL + "/repos/alice/go-md-reader/releases/assets/1",
						Digest: "sha256:" + hexSum,
					},
				},
			})
		case "/repos/alice/go-md-reader/releases/assets/1":
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origBase := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = origBase }()

	origVer := CurrentVersion
	CurrentVersion = "v2.0.0"
	defer func() { CurrentVersion = origVer }()

	mgr := NewManager(filepath.Join(t.TempDir(), "ota.json"))
	if _, err := mgr.SaveConfig("alice/go-md-reader", "secret-tok", false, false); err != nil {
		t.Fatal(err)
	}

	res, err := mgr.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate failed: %v", err)
	}
	if !res.HasUpdate || res.LatestVersion != "v2.1.0" || res.Asset == nil {
		t.Fatalf("unexpected CheckResult: %+v", res)
	}

	staged, err := mgr.DownloadAndStage(context.Background(), res.LatestVersion, res.Asset)
	if err != nil {
		t.Fatalf("DownloadAndStage failed: %v", err)
	}
	if !staged.VerifiedSHA256 || staged.SHA256 != hexSum {
		t.Fatalf("expected verified sha256 %s, got %+v", hexSum, staged)
	}
}
