package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-md-reader/updater"
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

func TestNonceIsolationAndMediaTraversal(t *testing.T) {
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
	if err := os.WriteFile(mdPath, []byte("# Img\n\n![p](assets/pic.png)"), 0644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, mdPath)
	mux := srv.buildMux()

	// Rendered HTML must use relative "media/..." under /<nonce>/
	doc := srv.GetFile()
	if !strings.Contains(doc.HTML, `src="media/assets/pic.png"`) {
		t.Fatalf("expected relative media/ path in parsed HTML, got:\n%s", doc.HTML)
	}

	// Root or mismatched nonce paths must return 404 (no cookie ceremony needed)
	for _, badPath := range []string{"/", "/wrongnonce/", "/media/assets/pic.png", "/wrongnonce/media/assets/pic.png"} {
		req := httptest.NewRequest(http.MethodGet, badPath, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for %s, got %d", badPath, rec.Code)
		}
	}

	// Valid nonce-scoped subdir asset succeeds without cookies or auth headers
	reqOK := httptest.NewRequest(http.MethodGet, "/"+srv.Nonce()+"/media/assets/pic.png", nil)
	recOK := httptest.NewRecorder()
	mux.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK || recOK.Body.String() != "PNGDATA" {
		t.Fatalf("expected 200 PNGDATA, got %d (%q)", recOK.Code, recOK.Body.String())
	}

	// Path traversal & drive-letter paths blocked cleanly in handleMedia
	baseMedia := "/" + srv.Nonce() + "/media/"
	for _, badRel := range []string{"../secret.txt", "assets/../../secret.txt", "C:/Windows/win.ini", `..\secret.txt`} {
		reqBad := httptest.NewRequest(http.MethodGet, baseMedia+badRel, nil)
		recBad := httptest.NewRecorder()
		srv.handleMedia(recBad, reqBad)
		if recBad.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for %s, got %d", badRel, recBad.Code)
		}
	}
}

func TestUnifiedOpenSaveDoNotEchoEventsAndIncrementDocSeq(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "one.md")
	f2 := filepath.Join(dir, "two.md")
	_ = os.WriteFile(f1, []byte("# One"), 0644)
	_ = os.WriteFile(f2, []byte("# Two"), 0644)

	srv := newTestServer(t, f1)
	events := make(chan string, 5)
	srv.SetEventSink(func(ev string) {
		events <- ev
	})

	// Unified doc.open (mode: path)
	openRaw, _ := json.Marshal(OpenRequest{Mode: "path", Path: f2})
	if _, err := srv.HandleRPC(context.Background(), "doc.open", openRaw); err != nil {
		t.Fatalf("doc.open(path) failed: %v", err)
	}

	// Unified doc.save (mode: save)
	saveRaw, _ := json.Marshal(SaveRequest{Mode: "save", Content: "# Two Updated"})
	if _, err := srv.HandleRPC(context.Background(), "doc.save", saveRaw); err != nil {
		t.Fatalf("doc.save(save) failed: %v", err)
	}

	select {
	case ev := <-events:
		t.Fatalf("expected UI-triggered open/save not to echo reload event, got %q", ev)
	case <-time.After(50 * time.Millisecond):
	}

	doc := srv.GetFile()
	if doc.DocSeq != 3 || doc.RawContent != "# Two Updated" {
		t.Fatalf("expected docSeq=3 and updated content, got docSeq=%d raw=%q", doc.DocSeq, doc.RawContent)
	}

	// Unified doc.open (mode: content)
	contentRaw, _ := json.Marshal(OpenRequest{Mode: "content", Name: "mem.md", Content: "# Mem"})
	if _, err := srv.HandleRPC(context.Background(), "doc.open", contentRaw); err != nil {
		t.Fatalf("doc.open(content) failed: %v", err)
	}
	if got := srv.GetFile(); !got.IsMemory || got.Filename != "mem.md" {
		t.Fatalf("unexpected memory document after doc.open(content): %+v", got)
	}

	// Unified doc.open (mode: binary)
	binRaw, _ := json.Marshal(OpenRequest{
		Mode:       "binary",
		Name:       "raw.txt",
		DataBase64: base64.StdEncoding.EncodeToString([]byte("hello binary")),
	})
	if _, err := srv.HandleRPC(context.Background(), "doc.open", binRaw); err != nil {
		t.Fatalf("doc.open(binary) failed: %v", err)
	}
	if got := srv.GetFile(); !got.IsMemory || !strings.Contains(got.RawContent, "hello binary") {
		t.Fatalf("unexpected memory document after doc.open(binary): %+v", got)
	}
}

func TestTypedTrayUIPrefsAndNotepadRPC(t *testing.T) {
	srv := newTestServer(t, "")
	prefsFile := filepath.Join(t.TempDir(), "ui_prefs.json")
	srv.uiPrefs = newUIPrefsStore(prefsFile)

	minFlag := true
	srv.SetTrayHandlers(func() bool { return minFlag }, func(v bool) { minFlag = v }, nil)

	ctx := context.Background()

	// Tray get & set
	trayRaw, _ := json.Marshal(TrayPrefsState{MinimizeToTray: false})
	if _, err := srv.HandleRPC(ctx, "tray.set", trayRaw); err != nil || minFlag {
		t.Fatalf("expected tray.set to update minFlag=false, err=%v minFlag=%v", err, minFlag)
	}

	// UI prefs patch + persistence check
	patchBody := json.RawMessage(`{"theme":"dark","fontSize":18,"sidebarHidden":true,"scrollPath":"C:\\docs\\a.md","scrollTop":420}`)
	if _, err := srv.HandleRPC(ctx, "prefs.update", patchBody); err != nil {
		t.Fatalf("prefs.update failed: %v", err)
	}
	reloaded := newUIPrefsStore(prefsFile).get()
	if reloaded.Theme != "dark" || reloaded.FontSize != 18 || !reloaded.SidebarHidden || reloaded.ScrollPositions[`C:\docs\a.md`] != 420 {
		t.Fatalf("unexpected reloaded UIPrefs: %+v", reloaded)
	}

	// Notepad status RPC
	resNP, err := srv.HandleRPC(ctx, "assoc.notepad.get", nil)
	if err != nil {
		t.Fatalf("assoc.notepad.get failed: %v", err)
	}
	npResp, ok := resNP.(NotepadStatusResponse)
	if !ok || !npResp.OK {
		t.Fatalf("unexpected NotepadStatusResponse: %+v", resNP)
	}
}

func TestActivateEndpointWithNonce(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, "activated.md")
	_ = os.WriteFile(docPath, []byte("# Activated"), 0644)

	srv := newTestServer(t, "")
	activated := false
	srv.SetActivateHandler(func() { activated = true })

	events := make(chan string, 4)
	srv.SetEventSink(func(ev string) {
		events <- ev
	})
	mux := srv.buildMux()

	body, _ := json.Marshal(map[string]string{"path": docPath})

	// Missing nonce rejected
	reqBad := httptest.NewRequest(http.MethodPost, "/activate", bytes.NewReader(body))
	recBad := httptest.NewRecorder()
	mux.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusNotFound {
		t.Fatalf("expected 404 without nonce prefix, got %d", recBad.Code)
	}

	// Valid nonce triggers activation + open-doc event
	req := httptest.NewRequest(http.MethodPost, "/"+srv.Nonce()+"/activate", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !activated {
		t.Fatalf("expected 200 and activated=true, got code=%d activated=%v", rec.Code, activated)
	}

	select {
	case ev := <-events:
		if ev != "open-doc" {
			t.Fatalf("expected open-doc event, got %q", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected open-doc event")
	}
}

func TestFormatRPC(t *testing.T) {
	srv := newTestServer(t, "")
	raw, _ := json.Marshal(ContentRequest{Content: "#标题\n测试Go语言"})
	res, err := srv.HandleRPC(context.Background(), "doc.format", raw)
	if err != nil {
		t.Fatalf("doc.format failed: %v", err)
	}
	resp, ok := res.(FormatResponse)
	if !ok {
		t.Fatalf("expected FormatResponse, got %T", res)
	}
	if !resp.OK || !resp.Changed || resp.Content != "# 标题\n\n测试 Go 语言\n" {
		t.Fatalf("unexpected FormatResponse: %+v", resp)
	}
	if !strings.Contains(resp.HTML, "<h1") {
		t.Fatalf("expected rendered heading HTML, got %q", resp.HTML)
	}
}

func TestOTAIdempotentApplyGuardAndProgressSink(t *testing.T) {
	srv := newTestServer(t, "")

	var gotEvent string
	var gotPayload interface{}
	srv.SetDataEventSink(func(ev string, data interface{}) {
		gotEvent = ev
		gotPayload = data
	})
	srv.TriggerEventWithData("ota-progress", updater.DownloadProgress{
		Version:         "v2.4.0",
		AssetName:       "go-md-reader.exe",
		Phase:           "downloading",
		BytesDownloaded: 50,
		TotalBytes:      100,
		Percent:         50,
	})
	if gotEvent != "ota-progress" {
		t.Fatalf("expected ota-progress event, got %q", gotEvent)
	}
	if p, ok := gotPayload.(updater.DownloadProgress); !ok || p.Percent != 50 {
		t.Fatalf("unexpected progress payload: %#v", gotPayload)
	}

	// Without staged update, ota.apply rejects cleanly
	if _, err := srv.HandleRPC(context.Background(), "ota.apply", nil); err == nil {
		t.Fatal("expected error when applying without staged update")
	}

	// Once applyStarted is active, repeat ota.apply is an idempotent no-op
	srv.mu.Lock()
	srv.applyStarted = true
	srv.mu.Unlock()
	res, err := srv.HandleRPC(context.Background(), "ota.apply", nil)
	if err != nil {
		t.Fatalf("expected idempotent OK when apply already started, got %v", err)
	}
	if okResp, ok := res.(OKResponse); !ok || !okResp.OK {
		t.Fatalf("expected OKResponse{OK:true}, got %#v", res)
	}
}
