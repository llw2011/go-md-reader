package server

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go-md-reader/assoc"
	"go-md-reader/converter"
	"go-md-reader/dialog"
	"go-md-reader/parser"
	"go-md-reader/updater"
)

// Server handles local HTTP requests and markdown state
type Server struct {
	assetsFS        embed.FS
	parser          *parser.MarkdownParser
	updater         *updater.Manager
	uiPrefs         *uiPrefsStore
	mu              sync.RWMutex
	currentFilePath string
	currentDir      string
	lastModTime     time.Time
	fileSize        int64
	isMemoryOnly    bool
	memoryFileName  string
	memoryContent   []byte
	docSeq          uint64
	lastCheckRes    *updater.CheckResult
	stagedUpdate    *updater.StagedUpdate
	getMinToTray    func() bool
	setMinToTray    func(bool)
	onQuit          func()
	clientsMu       sync.Mutex
	clients         map[chan string]struct{}
	token           string
	listener        net.Listener
	port            int
}

// New creates a new Server instance
func New(assets embed.FS, initialFile string) (*Server, error) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind local port: %w", err)
	}

	port := ln.Addr().(*net.TCPAddr).Port

	s := &Server{
		assetsFS: assets,
		parser:   parser.NewMarkdownParser(),
		updater:  updater.NewManager(""),
		uiPrefs:  newUIPrefsStore(""),
		clients:  make(map[chan string]struct{}),
		token:    token,
		listener: ln,
		port:     port,
	}

	if initialFile != "" {
		s.SetCurrentFile(initialFile)
	}

	go s.watchLoop()

	return s, nil
}

// URL returns the local webview entry URL
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d/?token=%s", s.port, s.token)
}

// Start begins serving HTTP requests
func (s *Server) Start() error {
	return http.Serve(s.listener, s.buildMux())
}

// Token returns the session authentication token
func (s *Server) Token() string {
	return s.token
}

func (s *Server) isValidToken(candidate string) bool {
	if s.token == "" || len(candidate) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(s.token)) == 1
}

func (s *Server) cookieName() string {
	return fmt.Sprintf("gomd_token_%d", s.port)
}

func (s *Server) hasValidToken(r *http.Request) bool {
	if h := r.Header.Get("X-Auth-Token"); h != "" {
		return s.isValidToken(h)
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return s.isValidToken(q)
	}
	if c, err := r.Cookie(s.cookieName()); err == nil && c.Value != "" {
		return s.isValidToken(c.Value)
	}
	return false
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hasValidToken(r) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	subFS, err := fs.Sub(s.assetsFS, "assets")
	if err == nil {
		fileSrv := http.StripPrefix("/assets/", http.FileServer(http.FS(subFS)))
		mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			fileSrv.ServeHTTP(w, r)
		})
	}

	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/file", s.requireAuth(s.handleGetFile))
	mux.HandleFunc("/api/siblings", s.requireAuth(s.handleGetSiblings))
	mux.HandleFunc("/api/assoc", s.requireAuth(s.handleAssoc))
	mux.HandleFunc("/api/assoc/notepad", s.requireAuth(s.handleAssocNotepad))
	mux.HandleFunc("/api/preview", s.requireAuth(s.handlePreview))
	mux.HandleFunc("/api/save", s.requireAuth(s.handleSave))
	mux.HandleFunc("/api/save-as", s.requireAuth(s.handleSaveAs))
	mux.HandleFunc("/api/save-html", s.requireAuth(s.handleSaveHTML))
	mux.HandleFunc("/api/open-dialog", s.requireAuth(s.handleOpenDialog))
	mux.HandleFunc("/api/open-path", s.requireAuth(s.handleOpenPath))
	mux.HandleFunc("/api/open-content", s.requireAuth(s.handleOpenContent))
	mux.HandleFunc("/api/open-binary", s.requireAuth(s.handleOpenBinary))
	mux.HandleFunc("/api/tray/prefs", s.requireAuth(s.handleTrayPrefs))
	mux.HandleFunc("/api/prefs", s.requireAuth(s.handleUIPrefs))
	mux.HandleFunc("/api/ota/config", s.requireAuth(s.handleOTAConfig))
	mux.HandleFunc("/api/ota/check", s.requireAuth(s.handleOTACheck))
	mux.HandleFunc("/api/ota/download", s.requireAuth(s.handleOTADownload))
	mux.HandleFunc("/api/ota/apply", s.requireAuth(s.handleOTAApply))
	mux.HandleFunc("/api/watch", s.requireAuth(s.handleWatchSSE))
	mux.HandleFunc("/media/", s.requireAuth(s.handleMedia))

	return mux
}

// SetTrayHandlers connects system tray callbacks to HTTP API and OTA restart lifecycle.
func (s *Server) SetTrayHandlers(getMin func() bool, setMin func(bool), onQuit func()) {
	s.mu.Lock()
	s.getMinToTray = getMin
	s.setMinToTray = setMin
	s.onQuit = onQuit
	s.mu.Unlock()
}

// SetCurrentFile updates the active document file from disk and notifies SSE listeners
func (s *Server) SetCurrentFile(path string) {
	s.setCurrentFile(path, true)
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

	info, err := os.Stat(absPath)
	if err == nil {
		s.lastModTime = info.ModTime()
		s.fileSize = info.Size()
	}
	s.mu.Unlock()

	if notify {
		s.broadcastReload()
	}
}

// SetMemoryContent loads document content directly from drag & drop or clipboard and notifies SSE listeners
func (s *Server) SetMemoryContent(name string, content []byte) {
	s.setMemoryContent(name, content, true)
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

	if notify {
		s.broadcastReload()
	}
}

// handleIndex serves the embedded index.html after validating session token
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.hasValidToken(r) {
		http.Error(w, "Forbidden: invalid session token", http.StatusForbidden)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	data, err := s.assetsFS.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// handleGetFile returns parsed markdown HTML + TOC + detailed metadata
func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	isMemory := s.isMemoryOnly
	filePath := s.currentFilePath
	memName := s.memoryFileName
	memContent := s.memoryContent
	modTime := s.lastModTime
	fileSize := s.fileSize
	docSeq := s.docSeq
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if !isMemory && filePath == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"hasFile": false,
			"docSeq":  docSeq,
		})
		return
	}

	var rawContent []byte
	var displayName string
	var displayPath string

	if isMemory {
		rawContent = memContent
		displayName = memName
		displayPath = "(拖拽导入内容)"
	} else {
		displayName = filepath.Base(filePath)
		displayPath = filePath
		var err error
		rawContent, err = os.ReadFile(filePath)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"hasFile":  true,
				"docSeq":   docSeq,
				"filename": displayName,
				"filepath": displayPath,
				"html":     fmt.Sprintf("<p style='color:red;'>读取文件失败: %s</p>", html.EscapeString(err.Error())),
				"toc":      []interface{}{},
			})
			return
		}
	}

	// Automatic Conversion: Word (.docx), Excel (.xlsx), PPT (.pptx), CSV (.csv), JSON/Code -> Markdown
	convResult := converter.ConvertToMarkdown(displayName, rawContent)
	finalMarkdown := []byte(convResult.Markdown)

	result, err := s.parser.Parse(finalMarkdown)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"hasFile":  true,
			"docSeq":   docSeq,
			"filename": displayName,
			"filepath": displayPath,
			"html":     fmt.Sprintf("<p style='color:red;'>解析文档内容失败: %s</p>", html.EscapeString(err.Error())),
			"toc":      []interface{}{},
		})
		return
	}

	lineCount := len(strings.Split(string(finalMarkdown), "\n"))

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"hasFile":     true,
		"docSeq":      docSeq,
		"filename":    displayName,
		"filepath":    displayPath,
		"html":        result.HTML,
		"toc":         result.TOC,
		"wordCount":   result.WordCount,
		"lineCount":   lineCount,
		"fileSize":    fileSize,
		"modTime":     modTime.Format("2006-01-02 15:04:05"),
		"isMemory":    isMemory,
		"isConverted": convResult.IsConverted,
		"formatNote":  convResult.FormatNote,
		"rawContent":  string(finalMarkdown),
	})
}

// handleGetSiblings returns supported readable files in the same directory
func (s *Server) handleGetSiblings(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	dir := s.currentDir
	curPath := s.currentFilePath
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if dir == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"dir":   "",
			"files": []interface{}{},
		})
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"dir":   dir,
			"files": []interface{}{},
		})
		return
	}

	supportedExts := map[string]bool{
		".md": true, ".markdown": true, ".txt": true,
		".docx": true, ".doc": true,
		".xlsx": true, ".xls": true, ".csv": true, ".tsv": true,
		".pptx": true, ".json": true, ".yaml": true, ".yml": true,
	}

	type siblingItem struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Ext       string `json:"ext"`
		Size      int64  `json:"size"`
		IsCurrent bool   `json:"isCurrent"`
	}

	var list []siblingItem
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !supportedExts[ext] {
			continue
		}
		full := filepath.Join(dir, e.Name())
		var sz int64
		if info, err := e.Info(); err == nil {
			sz = info.Size()
		}
		list = append(list, siblingItem{
			Name:      e.Name(),
			Path:      full,
			Ext:       ext,
			Size:      sz,
			IsCurrent: strings.EqualFold(full, curPath),
		})
	}

	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"dir":   dir,
		"files": list,
	})
}

// handleAssoc gets or updates Windows file associations for the current executable
func (s *Server) handleAssoc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method == http.MethodGet {
		status := assoc.GetStatus("")
		_ = json.NewEncoder(w).Encode(status)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Extensions  []string `json:"extensions"`
			ContextMenu bool     `json:"contextMenu"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		exePath, _ := os.Executable()
		if err := assoc.ApplyAssociations(exePath, req.Extensions, req.ContextMenu); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

// handleAssocNotepad inspects, activates, or restores Windows Notepad replacement
func (s *Server) handleAssocNotepad(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     true,
			"status": assoc.GetNotepadStatus(),
		})
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Replace bool `json:"replace"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "无效的请求体")
			return
		}
		if req.Replace {
			exePath, _ := os.Executable()
			if err := assoc.ReplaceNotepad(exePath); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
		} else {
			if err := assoc.RestoreNotepad(); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     true,
			"status": assoc.GetNotepadStatus(),
		})
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

// handlePreview renders raw markdown in real-time for the split-pane editor
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	res, err := s.parser.Parse([]byte(req.Content))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"html":      res.HTML,
		"toc":       res.TOC,
		"wordCount": res.WordCount,
	})
}

func normalizeSaveBaseName(name, defaultName, targetExt string) string {
	baseName := strings.TrimSpace(name)
	if baseName == "" {
		return defaultName
	}
	extOld := filepath.Ext(baseName)
	if extOld != "" && !strings.EqualFold(extOld, targetExt) {
		baseName = strings.TrimSuffix(baseName, extOld) + targetExt
	} else if extOld == "" {
		baseName += targetExt
	}
	return baseName
}

// handleSave writes markdown content back to disk (or opens SaveAs dialog if converted/memory)
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	curPath := s.currentFilePath
	isMem := s.isMemoryOnly
	memName := s.memoryFileName
	s.mu.RUnlock()

	ext := strings.ToLower(filepath.Ext(curPath))
	canDirectWrite := !isMem && curPath != "" && (ext == ".md" || ext == ".markdown" || ext == ".txt")

	targetPath := curPath
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !canDirectWrite {
		baseName := memName
		if baseName == "" && curPath != "" {
			baseName = filepath.Base(curPath)
		}
		baseName = normalizeSaveBaseName(baseName, "document.md", ".md")

		chosen, err := dialog.SaveMarkdownFile(baseName)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"saved": false, "error": err.Error()})
			return
		}
		if chosen == "" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"saved": false})
			return
		}
		targetPath = chosen
	}

	if err := os.WriteFile(targetPath, []byte(req.Content), 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Avoid redundant SSE broadcast since UI awaits /api/save response directly
	s.setCurrentFile(targetPath, false)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"saved": true,
		"path":  targetPath,
	})
}

// handleSaveAs always triggers the Windows Save File Dialog to save as .md
func (s *Server) handleSaveAs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Content  string `json:"content"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	baseName := normalizeSaveBaseName(req.Filename, "document.md", ".md")

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	chosen, err := dialog.SaveMarkdownFile(baseName)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"saved": false, "error": err.Error()})
		return
	}
	if chosen == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"saved": false})
		return
	}

	if err := os.WriteFile(chosen, []byte(req.Content), 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"saved": true,
		"path":  chosen,
	})
}

// handleSaveHTML triggers native Save dialog to write standalone offline .html files cleanly
func (s *Server) handleSaveHTML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		HTML     string `json:"html"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&req); err != nil || req.HTML == "" {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	baseName := normalizeSaveBaseName(req.Filename, "document.html", ".html")

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	chosen, err := dialog.SaveHTMLFile(baseName)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"saved": false, "error": err.Error()})
		return
	}
	if chosen == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"saved": false})
		return
	}

	if err := os.WriteFile(chosen, []byte(req.HTML), 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"saved": true,
		"path":  chosen,
	})
}

// handleOpenDialog triggers native Windows file open dialog
func (s *Server) handleOpenDialog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	selectedPath, err := dialog.SelectMarkdownFile()
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"opened": false, "error": err.Error()})
		return
	}
	if selectedPath == "" {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"opened": false})
		return
	}

	// UI calls loadFile(false) directly upon receiving opened=true
	s.setCurrentFile(selectedPath, false)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"opened": true,
		"path":   selectedPath,
	})
}

// handleOpenPath receives a file path from Drag & Drop or Recent Files
func (s *Server) handleOpenPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if _, err := os.Stat(req.Path); err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	// UI calls loadFile(false) directly upon receiving res.ok
	s.setCurrentFile(req.Path, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// handleOpenContent receives raw file content from in-browser Drag & Drop
func (s *Server) handleOpenContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&req); err != nil || req.Content == "" {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		req.Name = "未命名文档.md"
	}

	s.setMemoryContent(req.Name, []byte(req.Content), false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// handleOpenBinary receives raw binary data (e.g. .docx, .xlsx, .pptx) from in-browser Drag & Drop
func (s *Server) handleOpenBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "未命名文档"
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil || len(data) == 0 {
		http.Error(w, "Empty or invalid body", http.StatusBadRequest)
		return
	}

	s.setMemoryContent(name, data, false)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
}

// handleMedia securely serves local images/media relative to document directory
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	baseDir := s.currentDir
	s.mu.RUnlock()

	if baseDir == "" {
		http.NotFound(w, r)
		return
	}

	relPath := strings.TrimPrefix(r.URL.Path, "/media/")
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

// handleWatchSSE provides live hot-reload SSE events
func (s *Server) handleWatchSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 10)
	s.clientsMu.Lock()
	s.clients[ch] = struct{}{}
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		s.clientsMu.Unlock()
	}()

	notify := r.Context().Done()
	for {
		select {
		case <-notify:
			return
		case msg := <-ch:
			_, _ = io.WriteString(w, fmt.Sprintf("data: %s\n\n", msg))
			flusher.Flush()
		}
	}
}

// TriggerEvent pushes an instruction event (e.g., "reload", "open-ota") to SSE clients.
func (s *Server) TriggerEvent(event string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- event:
		default:
		}
	}
}

// broadcastReload sends reload signal to all SSE clients
func (s *Server) broadcastReload() {
	s.TriggerEvent("reload")
}

func (s *Server) handleTrayPrefs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodGet {
		s.mu.RLock()
		fn := s.getMinToTray
		s.mu.RUnlock()
		minVal := true
		if fn != nil {
			minVal = fn()
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"minimizeToTray": minVal})
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			MinimizeToTray bool `json:"minimizeToTray"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid payload", http.StatusBadRequest)
			return
		}
		s.mu.RLock()
		fn := s.setMinToTray
		s.mu.RUnlock()
		if fn != nil {
			fn(req.MinimizeToTray)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "minimizeToTray": req.MinimizeToTray})
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": msg})
}

func (s *Server) handleOTAConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(s.updater.GetConfigView())
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Repo              string `json:"repo"`
			Token             string `json:"token"`
			ClearToken        bool   `json:"clearToken"`
			IncludePrerelease bool   `json:"includePrerelease"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "无效的请求体")
			return
		}
		view, err := s.updater.SaveConfig(req.Repo, req.Token, req.ClearToken, req.IncludePrerelease)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "config": view})
		return
	}
	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleOTACheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	res, err := s.updater.CheckUpdate(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.mu.Lock()
	s.lastCheckRes = res
	s.stagedUpdate = nil
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "result": res})
}

func (s *Server) handleOTADownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	checkRes := s.lastCheckRes
	s.mu.RUnlock()
	if checkRes == nil || checkRes.Asset == nil {
		writeJSONError(w, http.StatusBadRequest, "请先检查更新并确认存在可下载的 Windows 资产")
		return
	}

	staged, err := s.updater.DownloadAndStage(r.Context(), checkRes.LatestVersion, checkRes.Asset)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	s.mu.Lock()
	s.stagedUpdate = staged
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "staged": staged})
}

func (s *Server) handleOTAApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	staged := s.stagedUpdate
	curPath := ""
	if !s.isMemoryOnly {
		curPath = s.currentFilePath
	}
	onQuit := s.onQuit
	s.mu.RUnlock()

	if staged == nil {
		writeJSONError(w, http.StatusBadRequest, "尚无已就绪的更新包，请先点击下载")
		return
	}

	if err := updater.ApplyAndRestart(*staged, "", curPath); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "启动更新安装程序失败: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})

	go func() {
		time.Sleep(150 * time.Millisecond)
		if onQuit != nil {
			onQuit()
		} else {
			os.Exit(0)
		}
	}()
}

// watchLoop polls the current disk file for modifications every 400ms
func (s *Server) watchLoop() {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.RLock()
		isMem := s.isMemoryOnly
		filePath := s.currentFilePath
		lastMod := s.lastModTime
		lastSize := s.fileSize
		seq := s.docSeq
		s.mu.RUnlock()

		if isMem || filePath == "" {
			continue
		}

		info, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		if info.ModTime().After(lastMod) || info.Size() != lastSize {
			s.mu.Lock()
			// Guard against TOCTOU if active document switched while os.Stat was running
			changed := !s.isMemoryOnly &&
				s.currentFilePath == filePath &&
				s.docSeq == seq &&
				(info.ModTime().After(s.lastModTime) || info.Size() != s.fileSize)
			if changed {
				s.lastModTime = info.ModTime()
				s.fileSize = info.Size()
				s.docSeq++
			}
			s.mu.Unlock()

			if changed {
				s.broadcastReload()
			}
		}
	}
}
