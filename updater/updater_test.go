package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
	mgr.stageRoot = t.TempDir()
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

func TestDownloadProgressAndIdempotentCoalescingAndCacheReuse(t *testing.T) {
	payload := bytes.Repeat([]byte("ABCDEF0123456789"), 8192) // 128 KB
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])

	var assetHits atomic.Int32
	var releaseHits atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/alice/go-md-reader/releases/latest":
			releaseHits.Add(1)
			time.Sleep(30 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v2.4.0",
				Name:    "v2.4.0",
				Assets: []ghAsset{
					{
						ID:     42,
						Name:   "go-md-reader.exe",
						Size:   int64(len(payload)),
						URL:    srv.URL + "/repos/alice/go-md-reader/releases/assets/42",
						Digest: "sha256:" + hexSum,
					},
				},
			})
		case "/repos/alice/go-md-reader/releases/assets/42":
			assetHits.Add(1)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			fl, _ := w.(http.Flusher)
			for off := 0; off < len(payload); off += len(payload) / 4 {
				end := off + len(payload)/4
				if end > len(payload) {
					end = len(payload)
				}
				_, _ = w.Write(payload[off:end])
				if fl != nil {
					fl.Flush()
				}
				time.Sleep(15 * time.Millisecond)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origBase := apiBaseURL
	apiBaseURL = srv.URL
	defer func() { apiBaseURL = origBase }()

	origVer := CurrentVersion
	CurrentVersion = "v2.3.0"
	defer func() { CurrentVersion = origVer }()

	mgr := NewManager(filepath.Join(t.TempDir(), "ota.json"))
	mgr.stageRoot = t.TempDir()
	_, _ = mgr.SaveConfig("alice/go-md-reader", "", false, false)

	// 1. Concurrent CheckUpdate coalesces into 1 HTTP request
	var chkWG sync.WaitGroup
	var res *CheckResult
	for i := 0; i < 4; i++ {
		chkWG.Add(1)
		go func(idx int) {
			defer chkWG.Done()
			r, err := mgr.CheckUpdate(context.Background())
			if err != nil {
				t.Errorf("concurrent CheckUpdate[%d] failed: %v", idx, err)
				return
			}
			if idx == 0 {
				res = r
			}
		}(i)
	}
	chkWG.Wait()
	if got := releaseHits.Load(); got != 1 {
		t.Fatalf("expected concurrent CheckUpdate to issue 1 HTTP request, got %d", got)
	}

	// 2. Concurrent DownloadAndStageWithProgress coalesces into 1 download and streams % progress
	var dlWG sync.WaitGroup
	var mu sync.Mutex
	var eventsA, eventsB []DownloadProgress
	var stA, stB *StagedUpdate
	var errA, errB error

	dlWG.Add(2)
	go func() {
		defer dlWG.Done()
		stA, errA = mgr.DownloadAndStageWithProgress(context.Background(), res.LatestVersion, res.Asset, func(p DownloadProgress) {
			mu.Lock()
			eventsA = append(eventsA, p)
			mu.Unlock()
		})
	}()
	go func() {
		defer dlWG.Done()
		time.Sleep(10 * time.Millisecond)
		stB, errB = mgr.DownloadAndStageWithProgress(context.Background(), res.LatestVersion, res.Asset, func(p DownloadProgress) {
			mu.Lock()
			eventsB = append(eventsB, p)
			mu.Unlock()
		})
	}()
	dlWG.Wait()

	if errA != nil || errB != nil {
		t.Fatalf("concurrent downloads failed: errA=%v errB=%v", errA, errB)
	}
	if stB == nil || stB.SHA256 != stA.SHA256 || stB.FilePath != stA.FilePath {
		t.Fatalf("expected second subscriber to receive identical staged metadata: stA=%+v stB=%+v", stA, stB)
	}
	if got := assetHits.Load(); got != 1 {
		t.Fatalf("expected concurrent downloads to coalesce into 1 HTTP asset fetch, got %d", got)
	}
	if len(eventsA) < 3 || eventsA[len(eventsA)-1].Phase != "ready" || eventsA[len(eventsA)-1].Percent != 100 {
		t.Fatalf("expected multi-step progress ending at ready 100%%, got %+v", eventsA)
	}
	if len(eventsB) == 0 || eventsB[len(eventsB)-1].Phase != "ready" {
		t.Fatalf("expected secondary subscriber to also receive ready progress, got %+v", eventsB)
	}

	// 3. Subsequent download call is idempotent & reuses verified local cache with 0 network requests
	var cachedEvents []DownloadProgress
	stCached, err := mgr.DownloadAndStageWithProgress(context.Background(), res.LatestVersion, res.Asset, func(p DownloadProgress) {
		cachedEvents = append(cachedEvents, p)
	})
	if err != nil {
		t.Fatalf("cached download failed: %v", err)
	}
	if !stCached.ReusedCache || !stCached.VerifiedSHA256 || stCached.FilePath != stA.FilePath {
		t.Fatalf("expected ReusedCache=true & VerifiedSHA256=true, got %+v", stCached)
	}
	if got := assetHits.Load(); got != 1 {
		t.Fatalf("expected cache hit with 0 extra HTTP requests, got %d", got)
	}
	if len(cachedEvents) != 1 || !cachedEvents[0].ReusedCache || cachedEvents[0].Percent != 100 {
		t.Fatalf("expected immediate cached ready event, got %+v", cachedEvents)
	}

	// 4. Tampered cache is detected & cleanly re-downloaded
	if err := os.WriteFile(stA.FilePath, bytes.Repeat([]byte("X"), len(payload)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyAndRestart(*stA, "", ""); err == nil {
		t.Fatal("expected ApplyAndRestart to reject tampered staged file")
	}
	if found := mgr.FindStaged(res.LatestVersion, res.Asset); found != nil {
		t.Fatalf("expected FindStaged to reject tampered cache, got %+v", found)
	}
	stRedownloaded, err := mgr.DownloadAndStageWithProgress(context.Background(), res.LatestVersion, res.Asset, nil)
	if err != nil || stRedownloaded.ReusedCache {
		t.Fatalf("expected fresh re-download after tampering, got staged=%+v err=%v", stRedownloaded, err)
	}
	if got := assetHits.Load(); got != 2 {
		t.Fatalf("expected 1 fresh HTTP fetch after tampering, got total %d", got)
	}
}

func TestBuildRestartScriptQuotesSpacesAndSpecialChars(t *testing.T) {
	script := buildRestartScript(
		1234,
		StagedUpdate{FilePath: `C:\Temp\go-md-reader.exe`},
		`C:\Program Files\GoMarkdownReader\go-md-reader.exe`,
		`C:\Users\Admin\Desktop\jev 'api' key.txt`,
	)
	wantArg := `-ArgumentList '"C:\Users\Admin\Desktop\jev ''api'' key.txt"'`
	if !strings.Contains(script, wantArg) {
		t.Fatalf("expected double-quoted ArgumentList %s inside restart script, got:\n%s", wantArg, script)
	}
}
