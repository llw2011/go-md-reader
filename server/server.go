package server

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go-md-reader/addons"
	"go-md-reader/parser"
	"go-md-reader/updater"
)

// Server hosts a minimal read-only HTTP server scoped to an unguessable per-launch URL nonce
// (for WebView2 HTML/CSS/JS assets, relative local Markdown images, and single-instance wakeup)
// alongside the strongly-typed application state driven via WebView2 native bindings.
type Server struct {
	assetsFS        embed.FS
	parser          *parser.MarkdownParser
	updater         *updater.Manager
	addons          *addons.Manager
	uiPrefs         *uiPrefsStore
	mu              sync.RWMutex
	currentFilePath string
	currentDir      string
	isMemoryOnly    bool
	memoryContent   []byte
	memoryFileName  string
	lastModTime     time.Time
	fileSize        int64
	docSeq          uint64
	getMinToTray    func() bool
	setMinToTray    func(bool)
	onQuit          func()
	onActivate      func()
	eventSink       func(string, interface{})
	lastCheckRes    *updater.CheckResult
	stagedUpdate    *updater.StagedUpdate
	applyStarted    bool
	nonce           string
	listener        net.Listener
	port            int
	watcher         fileWatcher
}

// New creates a new Server instance bound to an ephemeral loopback port.
func New(assets embed.FS, initialFile string) (*Server, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	nonce := hex.EncodeToString(b)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind local port: %w", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	s := &Server{
		assetsFS: assets,
		parser:   parser.NewMarkdownParserWithMediaPrefix("media/"),
		updater:  updater.NewManager(""),
		addons:   addons.NewManager("", "", nil),
		uiPrefs:  newUIPrefsStore(""),
		nonce:    nonce,
		listener: ln,
		port:     port,
	}

	s.initWatcher()
	if initialFile != "" {
		s.SetCurrentFile(initialFile)
	}

	go s.watchLoop()

	return s, nil
}

// URL returns the WebView2 entry URL scoped under the per-launch nonce prefix.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/%s/", s.port, s.nonce)
}

// Nonce returns the per-launch unguessable URL prefix isolating local assets and wakeup.
func (s *Server) Nonce() string {
	return s.nonce
}

// Port returns the bound local TCP port.
func (s *Server) Port() int {
	return s.port
}

// Start begins serving read-only assets, local media, and single-instance wakeup requests.
func (s *Server) Start() error {
	return http.Serve(s.listener, s.buildMux())
}

// SetTrayHandlers connects system tray callbacks to RPC handlers and OTA restart lifecycle.
func (s *Server) SetTrayHandlers(getMin func() bool, setMin func(bool), onQuit func()) {
	s.mu.Lock()
	s.getMinToTray = getMin
	s.setMinToTray = setMin
	s.onQuit = onQuit
	s.mu.Unlock()
}

// SetActivateHandler registers the callback invoked when another instance forwards a file/focus request.
func (s *Server) SetActivateHandler(onActivate func()) {
	s.mu.Lock()
	s.onActivate = onActivate
	s.mu.Unlock()
}

// SetEventSink registers the push callback used to notify WebView2 of events and optional JSON payloads.
func (s *Server) SetEventSink(fn func(event string, data interface{})) {
	s.mu.Lock()
	s.eventSink = fn
	s.mu.Unlock()
}

// TriggerEvent delivers a push notification (e.g., "reload", "open-doc", "open-ota", "ota-progress") to WebView2.
func (s *Server) TriggerEvent(event string, data ...interface{}) {
	s.mu.RLock()
	sink := s.eventSink
	s.mu.RUnlock()
	if sink == nil {
		return
	}
	var payload interface{}
	if len(data) > 0 {
		payload = data[0]
	}
	sink(event, payload)
}

// SetCurrentFile updates the active document file from disk and notifies the UI.
func (s *Server) SetCurrentFile(path string) {
	s.setCurrentFile(path, true)
}

// ClearCurrentFile clears the active document back to the empty welcome state.
func (s *Server) ClearCurrentFile() {
	s.mu.Lock()
	s.isMemoryOnly = false
	s.currentFilePath = ""
	s.memoryContent = nil
	s.memoryFileName = ""
	s.docSeq++
	s.mu.Unlock()
	s.notifyWatcher()
}

func (s *Server) setCurrentFile(path string, notify bool) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	s.mu.Lock()
	s.isMemoryOnly = false
	s.currentFilePath = absPath
	s.currentDir = filepath.Dir(absPath)
	s.memoryContent = nil
	s.memoryFileName = ""
	s.docSeq++

	if info, err := os.Stat(absPath); err == nil {
		s.lastModTime = info.ModTime()
		s.fileSize = info.Size()
	}
	s.mu.Unlock()
	s.notifyWatcher()

	if notify {
		s.TriggerEvent("reload")
	}
}

func (s *Server) setMemoryContent(name string, content []byte, notify bool) {
	s.mu.Lock()
	s.isMemoryOnly = true
	s.memoryFileName = name
	s.memoryContent = content
	s.currentFilePath = ""
	s.currentDir = ""
	s.lastModTime = time.Now()
	s.fileSize = int64(len(content))
	s.docSeq++
	s.mu.Unlock()
	s.notifyWatcher()

	if notify {
		s.TriggerEvent("reload")
	}
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	base := "/" + s.nonce

	mux.HandleFunc(base+"/", s.handleIndex)

	if subFS, err := fs.Sub(s.assetsFS, "assets"); err == nil {
		assetSrv := http.StripPrefix(base+"/assets/", http.FileServer(http.FS(subFS)))
		mux.HandleFunc(base+"/assets/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			assetSrv.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc(base+"/media/", s.handleMedia)
	mux.HandleFunc(base+"/addons/", s.handleAddon)
	mux.HandleFunc(base+"/activate", s.handleActivate)

	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/"+s.nonce+"/" {
		http.NotFound(w, r)
		return
	}
	data, err := s.assetsFS.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	baseDir := s.currentDir
	s.mu.RUnlock()

	if baseDir == "" {
		http.NotFound(w, r)
		return
	}

	prefix := "/" + s.nonce + "/media/"
	relPath := strings.TrimPrefix(r.URL.Path, prefix)
	relPath = strings.TrimLeft(relPath, "/\\")
	cleanPath := filepath.Clean(filepath.FromSlash(relPath))
	if cleanPath == "." || filepath.IsAbs(cleanPath) || filepath.VolumeName(cleanPath) != "" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	fullPath := filepath.Join(baseDir, cleanPath)
	relToBase, err := filepath.Rel(baseDir, fullPath)
	if err != nil || relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(filepath.Separator)) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	http.ServeFile(w, r, fullPath)
}

func (s *Server) handleAddon(w http.ResponseWriter, r *http.Request) {
	prefix := "/" + s.nonce + "/addons/"
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	slashIdx := strings.IndexByte(rest, '/')
	if slashIdx <= 0 || slashIdx == len(rest)-1 {
		http.NotFound(w, r)
		return
	}
	id := rest[:slashIdx]
	relPath := rest[slashIdx+1:]
	fullPath, ok := s.addons.ResolveFile(id, relPath)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, fullPath)
}

func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req)

	docPath := strings.TrimSpace(req.Path)
	if docPath != "" {
		if info, err := os.Stat(docPath); err != nil || info.IsDir() {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(OpenResponse{OK: false, Error: "File not found"})
			return
		}
		s.setCurrentFile(docPath, false)
		s.TriggerEvent("open-doc")
	}

	s.mu.RLock()
	activateFn := s.onActivate
	s.mu.RUnlock()
	if activateFn != nil {
		activateFn()
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(OKResponse{OK: true})
}
