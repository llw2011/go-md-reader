package server

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, initialFile string) *Server {
	t.Helper()
	srv, err := New(embed.FS{}, initialFile)
	if err != nil {
		t.Fatalf("New server failed: %v", err)
	}
	srv.listener.Close()
	return srv
}

func TestAuthTokenEnforcementAndCookie(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(mdPath, []byte("# Hello"), 0644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, mdPath)
	mux := srv.buildMux()

	// Unauthenticated API call must fail with 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	recUnauth := httptest.NewRecorder()
	mux.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/file, got %d", recUnauth.Code)
	}

	// Authenticated via header succeeds
	reqHdr := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	reqHdr.Header.Set("X-Auth-Token", srv.Token())
	recHdr := httptest.NewRecorder()
	mux.ServeHTTP(recHdr, reqHdr)
	if recHdr.Code != http.StatusOK {
		t.Fatalf("expected 200 with X-Auth-Token, got %d", recHdr.Code)
	}

	// Index rejects missing token with 403 and sets HttpOnly cookie on valid token
	reqBadIdx := httptest.NewRequest(http.MethodGet, "/", nil)
	recBadIdx := httptest.NewRecorder()
	mux.ServeHTTP(recBadIdx, reqBadIdx)
	if recBadIdx.Code != http.StatusForbidden {
		t.Fatalf("expected 403 on index without token, got %d", recBadIdx.Code)
	}

	reqGoodIdx := httptest.NewRequest(http.MethodGet, "/?token="+srv.Token(), nil)
	recGoodIdx := httptest.NewRecorder()
	mux.ServeHTTP(recGoodIdx, reqGoodIdx)
	cookies := recGoodIdx.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == srv.cookieName() && c.Value == srv.Token() && c.HttpOnly {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected HttpOnly %s cookie to be set on valid index request", srv.cookieName())
	}

	// Stale cookie from previous session/port must NOT block valid query token navigation
	reqStaleCookie := httptest.NewRequest(http.MethodGet, "/?token="+srv.Token(), nil)
	reqStaleCookie.AddCookie(&http.Cookie{Name: "gomd_token", Value: "stale-old-token"})
	reqStaleCookie.AddCookie(&http.Cookie{Name: srv.cookieName(), Value: "stale-port-token"})
	recStaleCookie := httptest.NewRecorder()
	mux.ServeHTTP(recStaleCookie, reqStaleCookie)
	if recStaleCookie.Code == http.StatusForbidden {
		t.Fatalf("stale cookie unexpectedly caused 403 on valid ?token= navigation")
	}
}

func TestMediaTraversalAndSubdir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "assets")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	imgFile := filepath.Join(sub, "pic.png")
	if err := os.WriteFile(imgFile, []byte("PNGDATA"), 0644); err != nil {
		t.Fatal(err)
	}
	mdPath := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(mdPath, []byte("# Img"), 0644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, mdPath)
	mux := srv.buildMux()

	// Valid subdir asset
	reqOK := httptest.NewRequest(http.MethodGet, "/media/assets/pic.png", nil)
	reqOK.Header.Set("X-Auth-Token", srv.Token())
	recOK := httptest.NewRecorder()
	mux.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK || recOK.Body.String() != "PNGDATA" {
		t.Fatalf("expected 200 PNGDATA, got %d (%q)", recOK.Code, recOK.Body.String())
	}

	// Path traversal & drive-letter paths blocked cleanly in handleMedia
	for _, badURL := range []string{"/media/../secret.txt", "/media/assets/../../secret.txt", "/media/C:/Windows/win.ini", `/media/..\secret.txt`} {
		reqBad := httptest.NewRequest(http.MethodGet, badURL, nil)
		recBad := httptest.NewRecorder()
		srv.handleMedia(recBad, reqBad)
		if recBad.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for %s, got %d", badURL, recBad.Code)
		}
	}
}

func TestOpenPathAndSaveDoNotEchoSSEAndIncrementDocSeq(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "one.md")
	f2 := filepath.Join(dir, "two.md")
	_ = os.WriteFile(f1, []byte("# One"), 0644)
	_ = os.WriteFile(f2, []byte("# Two"), 0644)

	srv := newTestServer(t, f1)
	mux := srv.buildMux()

	sseCh := make(chan string, 5)
	srv.clientsMu.Lock()
	srv.clients[sseCh] = struct{}{}
	srv.clientsMu.Unlock()

	// Open second file via /api/open-path
	body, _ := json.Marshal(map[string]string{"path": f2})
	reqOpen := httptest.NewRequest(http.MethodPost, "/api/open-path", bytes.NewReader(body))
	reqOpen.Header.Set("X-Auth-Token", srv.Token())
	recOpen := httptest.NewRecorder()
	mux.ServeHTTP(recOpen, reqOpen)
	if recOpen.Code != http.StatusOK {
		t.Fatalf("open-path failed: %d", recOpen.Code)
	}

	// Save via /api/save
	saveBody, _ := json.Marshal(map[string]string{"content": "# Two Updated"})
	reqSave := httptest.NewRequest(http.MethodPost, "/api/save", bytes.NewReader(saveBody))
	reqSave.Header.Set("X-Auth-Token", srv.Token())
	recSave := httptest.NewRecorder()
	mux.ServeHTTP(recSave, reqSave)
	if recSave.Code != http.StatusOK {
		t.Fatalf("save failed: %d", recSave.Code)
	}

	select {
	case msg := <-sseCh:
		t.Fatalf("expected UI-triggered open/save not to echo SSE reload, got %q", msg)
	case <-time.After(50 * time.Millisecond):
	}

	reqFile := httptest.NewRequest(http.MethodGet, "/api/file", nil)
	reqFile.Header.Set("X-Auth-Token", srv.Token())
	recFile := httptest.NewRecorder()
	mux.ServeHTTP(recFile, reqFile)
	if !strings.Contains(recFile.Body.String(), `"docSeq":3`) {
		t.Fatalf("expected docSeq=3 after initial+open+save, got %s", recFile.Body.String())
	}
}

func TestTrayPrefsAndOTAConfigEndpoints(t *testing.T) {
	srv := newTestServer(t, "")
	minFlag := true
	srv.SetTrayHandlers(func() bool { return minFlag }, func(v bool) { minFlag = v }, nil)
	mux := srv.buildMux()

	// Must require auth
	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/ota/config", nil)
	recNoAuth := httptest.NewRecorder()
	mux.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/ota/config, got %d", recNoAuth.Code)
	}

	// Toggle tray preference via POST
	postBody := bytes.NewBufferString(`{"minimizeToTray":false}`)
	reqPost := httptest.NewRequest(http.MethodPost, "/api/tray/prefs", postBody)
	reqPost.Header.Set("X-Auth-Token", srv.Token())
	recPost := httptest.NewRecorder()
	mux.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusOK || minFlag {
		t.Fatalf("expected minimizeToTray updated to false, code=%d minFlag=%v", recPost.Code, minFlag)
	}
}

func TestAssocNotepadEndpoint(t *testing.T) {
	srv := newTestServer(t, "")

	mux := srv.buildMux()

	reqNoAuth := httptest.NewRequest(http.MethodGet, "/api/assoc/notepad", nil)
	recNoAuth := httptest.NewRecorder()
	mux.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/assoc/notepad, got %d", recNoAuth.Code)
	}

	reqGet := httptest.NewRequest(http.MethodGet, "/api/assoc/notepad", nil)
	reqGet.Header.Set("X-Auth-Token", srv.Token())
	recGet := httptest.NewRecorder()
	mux.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for authenticated GET /api/assoc/notepad, got %d", recGet.Code)
	}

	var payload struct {
		OK     bool `json:"ok"`
		Status struct {
			Replaced bool `json:"replaced"`
		} `json:"status"`
	}
	if err := json.NewDecoder(recGet.Body).Decode(&payload); err != nil || !payload.OK {
		t.Fatalf("unexpected /api/assoc/notepad payload: ok=%v err=%v", payload.OK, err)
	}
}

func TestUIPrefsEndpoint(t *testing.T) {
	srv := newTestServer(t, "")
	prefsFile := filepath.Join(t.TempDir(), "ui_prefs.json")
	srv.uiPrefs = newUIPrefsStore(prefsFile)
	mux := srv.buildMux()

	postBody := bytes.NewBufferString(`{"theme":"dark","fontSize":18,"sidebarHidden":true,"scrollPath":"C:\\docs\\a.md","scrollTop":420}`)
	reqPost := httptest.NewRequest(http.MethodPost, "/api/prefs", postBody)
	reqPost.Header.Set("X-Auth-Token", srv.Token())
	recPost := httptest.NewRecorder()
	mux.ServeHTTP(recPost, reqPost)
	if recPost.Code != http.StatusOK {
		t.Fatalf("POST /api/prefs failed: %d", recPost.Code)
	}

	reloaded := newUIPrefsStore(prefsFile).get()
	if reloaded.Theme != "dark" || reloaded.FontSize != 18 || !reloaded.SidebarHidden || reloaded.ScrollPositions[`C:\docs\a.md`] != 420 {
		t.Fatalf("unexpected reloaded UIPrefs: %+v", reloaded)
	}
}

func TestActivateEndpoint(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "activated.md")
	_ = os.WriteFile(docPath, []byte("# Activated"), 0644)

	srv := newTestServer(t, "")
	activated := false
	srv.SetActivateHandler(func() { activated = true })
	mux := srv.buildMux()

	sseCh := make(chan string, 4)
	srv.clientsMu.Lock()
	srv.clients[sseCh] = struct{}{}
	srv.clientsMu.Unlock()

	body, _ := json.Marshal(map[string]string{"path": docPath})
	req := httptest.NewRequest(http.MethodPost, "/api/activate", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", srv.Token())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !activated {
		t.Fatalf("expected 200 and activated=true, got code=%d activated=%v", rec.Code, activated)
	}

	select {
	case ev := <-sseCh:
		if ev != "open-doc" {
			t.Fatalf("expected open-doc SSE event, got %q", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected open-doc SSE event")
	}
}

func TestFormatEndpoint(t *testing.T) {
	srv := newTestServer(t, "")
	mux := srv.buildMux()

	reqNoAuth := httptest.NewRequest(http.MethodPost, "/api/format", bytes.NewBufferString(`{"content":"#标题"}`))
	recNoAuth := httptest.NewRecorder()
	mux.ServeHTTP(recNoAuth, reqNoAuth)
	if recNoAuth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated /api/format, got %d", recNoAuth.Code)
	}

	body, _ := json.Marshal(map[string]string{"content": "#标题\n测试Go语言"})
	req := httptest.NewRequest(http.MethodPost, "/api/format", bytes.NewReader(body))
	req.Header.Set("X-Auth-Token", srv.Token())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for /api/format, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		OK      bool   `json:"ok"`
		Content string `json:"content"`
		Changed bool   `json:"changed"`
		HTML    string `json:"html"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode /api/format response: %v", err)
	}
	if !resp.OK || !resp.Changed || resp.Content != "# 标题\n\n测试 Go 语言\n" {
		t.Fatalf("unexpected /api/format result: %+v", resp)
	}
	if !strings.Contains(resp.HTML, "<h1") {
		t.Fatalf("expected rendered heading HTML, got %q", resp.HTML)
	}
}
