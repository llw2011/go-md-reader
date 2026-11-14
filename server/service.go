package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-md-reader/assoc"
	"go-md-reader/converter"
	"go-md-reader/dialog"
	"go-md-reader/parser"
	"go-md-reader/updater"
)

const maxDecodedBinaryBytes = 64 << 20

func callNoArg[Resp any](fn func() (Resp, error)) (interface{}, error) {
	return fn()
}

func callTyped[Req any, Resp any](raw json.RawMessage, fn func(Req) (Resp, error)) (interface{}, error) {
	var req Req
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}
	return fn(req)
}

// HandleRPC dispatches typed application operations invoked from the WebView2 bridge or tests.
func (s *Server) HandleRPC(ctx context.Context, method string, rawParams json.RawMessage) (interface{}, error) {
	switch method {
	case "doc.get":
		return callNoArg(func() (DocumentResponse, error) { return s.GetFile(), nil })
	case "doc.siblings":
		return callNoArg(func() (SiblingsResponse, error) { return s.GetSiblings(), nil })
	case "doc.preview":
		return callTyped(rawParams, s.PreviewMarkdown)
	case "doc.format":
		return callTyped(rawParams, s.FormatMarkdown)
	case "doc.open":
		return callTyped(rawParams, s.OpenDocument)
	case "doc.save":
		return callTyped(rawParams, s.SaveDocument)
	case "prefs.get":
		return callNoArg(func() (UIPrefs, error) { return s.uiPrefs.get(), nil })
	case "prefs.update":
		return callTyped(rawParams, func(patch prefsPatch) (UIPrefsUpdateResponse, error) {
			return UIPrefsUpdateResponse{OK: true, Prefs: s.uiPrefs.applyPatch(patch)}, nil
		})
	case "tray.get":
		return callNoArg(func() (TrayPrefsState, error) { return s.GetTrayPrefs(), nil })
	case "tray.set":
		return callTyped(rawParams, func(req TrayPrefsState) (TrayPrefsState, error) {
			return s.SetTrayPrefs(req.MinimizeToTray), nil
		})
	case "assoc.get":
		return callNoArg(func() (assoc.Status, error) { return assoc.GetStatus(""), nil })
	case "assoc.apply":
		return callTyped(rawParams, s.ApplyAssoc)
	case "assoc.notepad.get":
		return callNoArg(func() (NotepadStatusResponse, error) {
			return NotepadStatusResponse{OK: true, Status: assoc.GetNotepadStatus()}, nil
		})
	case "assoc.notepad.set":
		return callTyped(rawParams, s.SetNotepadReplaced)
	case "ota.config.get":
		return callNoArg(func() (updater.ConfigView, error) { return s.updater.GetConfigView(), nil })
	case "ota.config.save":
		return callTyped(rawParams, s.SaveOTAConfig)
	case "ota.check":
		return callNoArg(func() (OTACheckResponse, error) { return s.CheckOTAUpdate(ctx) })
	case "ota.download":
		return callNoArg(func() (OTADownloadResponse, error) { return s.DownloadOTAUpdate(ctx) })
	case "ota.apply":
		return callNoArg(s.ApplyOTAUpdate)
	default:
		return nil, fmt.Errorf("未知操作: %s", method)
	}
}

// GetFile reads, converts, and parses the currently active disk or memory document.
func (s *Server) GetFile() DocumentResponse {
	s.mu.RLock()
	isMemory := s.isMemoryOnly
	filePath := s.currentFilePath
	memName := s.memoryFileName
	memContent := s.memoryContent
	modTime := s.lastModTime
	fileSize := s.fileSize
	docSeq := s.docSeq
	s.mu.RUnlock()

	if !isMemory && filePath == "" {
		return DocumentResponse{HasFile: false, DocSeq: docSeq}
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
			return DocumentResponse{
				HasFile:  true,
				DocSeq:   docSeq,
				Filename: displayName,
				Filepath: displayPath,
				HTML:     fmt.Sprintf("<p style='color:red;'>读取文件失败: %s</p>", html.EscapeString(err.Error())),
			}
		}
	}

	convResult := converter.ConvertToMarkdown(displayName, rawContent)
	finalMarkdown := []byte(convResult.Markdown)

	result, err := s.parser.Parse(finalMarkdown)
	if err != nil {
		return DocumentResponse{
			HasFile:  true,
			DocSeq:   docSeq,
			Filename: displayName,
			Filepath: displayPath,
			HTML:     fmt.Sprintf("<p style='color:red;'>解析文档内容失败: %s</p>", html.EscapeString(err.Error())),
		}
	}

	return DocumentResponse{
		HasFile:     true,
		DocSeq:      docSeq,
		Filename:    displayName,
		Filepath:    displayPath,
		HTML:        result.HTML,
		TOC:         result.TOC,
		WordCount:   result.WordCount,
		LineCount:   len(strings.Split(string(finalMarkdown), "\n")),
		FileSize:    fileSize,
		ModTime:     modTime.Format("2006-01-02 15:04:05"),
		IsMemory:    isMemory,
		IsConverted: convResult.IsConverted,
		FormatNote:  convResult.FormatNote,
		RawContent:  string(finalMarkdown),
	}
}

// GetSiblings lists supported readable files in the active directory.
func (s *Server) GetSiblings() SiblingsResponse {
	s.mu.RLock()
	dir := s.currentDir
	curPath := s.currentFilePath
	s.mu.RUnlock()

	if dir == "" {
		return SiblingsResponse{Dir: "", Files: []SiblingItem{}}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return SiblingsResponse{Dir: dir, Files: []SiblingItem{}}
	}

	supportedExts := map[string]bool{
		".md": true, ".markdown": true, ".txt": true,
		".docx": true, ".doc": true,
		".xlsx": true, ".xls": true, ".csv": true, ".tsv": true,
		".pptx": true, ".json": true, ".yaml": true, ".yml": true,
	}

	list := make([]SiblingItem, 0, len(entries))
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
		list = append(list, SiblingItem{
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

	return SiblingsResponse{Dir: dir, Files: list}
}

// PreviewMarkdown renders raw Markdown for live editor preview.
func (s *Server) PreviewMarkdown(req ContentRequest) (PreviewResponse, error) {
	res, err := s.parser.Parse([]byte(req.Content))
	if err != nil {
		return PreviewResponse{}, err
	}
	return PreviewResponse{
		HTML:      res.HTML,
		TOC:       res.TOC,
		WordCount: res.WordCount,
	}, nil
}

// FormatMarkdown normalizes Markdown structure and bilingual spacing and returns refreshed HTML.
func (s *Server) FormatMarkdown(req ContentRequest) (FormatResponse, error) {
	formatted := parser.FormatMarkdown(req.Content)
	res, err := s.parser.Parse([]byte(formatted))
	if err != nil {
		return FormatResponse{}, err
	}
	return FormatResponse{
		OK:        true,
		Content:   formatted,
		Changed:   formatted != req.Content,
		HTML:      res.HTML,
		TOC:       res.TOC,
		WordCount: res.WordCount,
	}, nil
}

// OpenDocument handles dialog, disk-path, text-content, and base64-binary document opening.
func (s *Server) OpenDocument(req OpenRequest) (OpenResponse, error) {
	switch req.Mode {
	case "dialog":
		selectedPath, err := dialog.SelectMarkdownFile()
		if err != nil {
			return OpenResponse{Opened: false, Error: err.Error()}, nil
		}
		if selectedPath == "" {
			return OpenResponse{Opened: false}, nil
		}
		s.setCurrentFile(selectedPath, false)
		return OpenResponse{OK: true, Opened: true, Path: selectedPath}, nil

	case "path":
		if strings.TrimSpace(req.Path) == "" {
			return OpenResponse{}, errors.New("文件路径不能为空")
		}
		info, err := os.Stat(req.Path)
		if err != nil || info.IsDir() {
			return OpenResponse{}, errors.New("文件不存在或无法访问")
		}
		s.setCurrentFile(req.Path, false)
		return OpenResponse{OK: true, Opened: true, Path: req.Path}, nil

	case "content":
		if req.Content == "" {
			return OpenResponse{}, errors.New("文档内容不能为空")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = "未命名文档.md"
		}
		s.setMemoryContent(name, []byte(req.Content), false)
		return OpenResponse{OK: true, Opened: true}, nil

	case "binary":
		if req.DataBase64 == "" {
			return OpenResponse{}, errors.New("上传文件内容为空")
		}
		data, err := base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil || len(data) == 0 || len(data) > maxDecodedBinaryBytes {
			return OpenResponse{}, errors.New("文件内容解码失败或超出大小限制")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = "未命名文档"
		}
		s.setMemoryContent(name, data, false)
		return OpenResponse{OK: true, Opened: true}, nil

	default:
		return OpenResponse{}, fmt.Errorf("不支持的打开模式: %s", req.Mode)
	}
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

// SaveDocument unifies save, save-as (.md), and save-html (.html) disk persistence.
func (s *Server) SaveDocument(req SaveRequest) (SaveResponse, error) {
	switch req.Mode {
	case "save", "":
		s.mu.RLock()
		curPath := s.currentFilePath
		isMem := s.isMemoryOnly
		memName := s.memoryFileName
		s.mu.RUnlock()

		ext := strings.ToLower(filepath.Ext(curPath))
		canDirectWrite := !isMem && curPath != "" && (ext == ".md" || ext == ".markdown" || ext == ".txt")
		targetPath := curPath
		if !canDirectWrite {
			baseName := memName
			if baseName == "" && curPath != "" {
				baseName = filepath.Base(curPath)
			}
			chosen, err := dialog.SaveMarkdownFile(normalizeSaveBaseName(baseName, "document.md", ".md"))
			if err != nil {
				return SaveResponse{Saved: false, Error: err.Error()}, nil
			}
			if chosen == "" {
				return SaveResponse{Saved: false}, nil
			}
			targetPath = chosen
		}
		if err := os.WriteFile(targetPath, []byte(req.Content), 0644); err != nil {
			return SaveResponse{}, err
		}
		s.setCurrentFile(targetPath, false)
		return SaveResponse{Saved: true, Path: targetPath}, nil

	case "saveAs":
		chosen, err := dialog.SaveMarkdownFile(normalizeSaveBaseName(req.Filename, "document.md", ".md"))
		if err != nil {
			return SaveResponse{Saved: false, Error: err.Error()}, nil
		}
		if chosen == "" {
			return SaveResponse{Saved: false}, nil
		}
		if err := os.WriteFile(chosen, []byte(req.Content), 0644); err != nil {
			return SaveResponse{}, err
		}
		return SaveResponse{Saved: true, Path: chosen}, nil

	case "saveHtml":
		if req.HTML == "" {
			return SaveResponse{}, errors.New("导出的 HTML 内容不能为空")
		}
		chosen, err := dialog.SaveHTMLFile(normalizeSaveBaseName(req.Filename, "document.html", ".html"))
		if err != nil {
			return SaveResponse{Saved: false, Error: err.Error()}, nil
		}
		if chosen == "" {
			return SaveResponse{Saved: false}, nil
		}
		if err := os.WriteFile(chosen, []byte(req.HTML), 0644); err != nil {
			return SaveResponse{}, err
		}
		return SaveResponse{Saved: true, Path: chosen}, nil

	default:
		return SaveResponse{}, fmt.Errorf("不支持的保存模式: %s", req.Mode)
	}
}

// GetTrayPrefs reads the tray minimize preference.
func (s *Server) GetTrayPrefs() TrayPrefsState {
	s.mu.RLock()
	fn := s.getMinToTray
	s.mu.RUnlock()
	minVal := true
	if fn != nil {
		minVal = fn()
	}
	return TrayPrefsState{OK: true, MinimizeToTray: minVal}
}

// SetTrayPrefs updates the tray minimize preference.
func (s *Server) SetTrayPrefs(minToTray bool) TrayPrefsState {
	s.mu.RLock()
	fn := s.setMinToTray
	s.mu.RUnlock()
	if fn != nil {
		fn(minToTray)
	}
	return TrayPrefsState{OK: true, MinimizeToTray: minToTray}
}

// ApplyAssoc updates Windows file associations and Explorer context menu integration.
func (s *Server) ApplyAssoc(req AssocApplyRequest) (OKResponse, error) {
	exePath, _ := os.Executable()
	if err := assoc.ApplyAssociations(exePath, req.Extensions, req.ContextMenu); err != nil {
		return OKResponse{}, err
	}
	return OKResponse{OK: true}, nil
}

// SetNotepadReplaced activates or restores Windows Notepad IFEO replacement.
func (s *Server) SetNotepadReplaced(req NotepadSetRequest) (NotepadStatusResponse, error) {
	if req.Replace {
		exePath, _ := os.Executable()
		if err := assoc.ReplaceNotepad(exePath); err != nil {
			return NotepadStatusResponse{}, err
		}
	} else {
		if err := assoc.RestoreNotepad(); err != nil {
			return NotepadStatusResponse{}, err
		}
	}
	return NotepadStatusResponse{OK: true, Status: assoc.GetNotepadStatus()}, nil
}

// SaveOTAConfig validates and persists OTA update preferences.
func (s *Server) SaveOTAConfig(req OTAConfigRequest) (OTAConfigSaveResponse, error) {
	view, err := s.updater.SaveConfig(req.Repo, req.Token, req.ClearToken, req.IncludePrerelease)
	if err != nil {
		return OTAConfigSaveResponse{}, err
	}
	return OTAConfigSaveResponse{OK: true, Config: view}, nil
}

// CheckOTAUpdate queries the configured release feed for newer versions.
func (s *Server) CheckOTAUpdate(ctx context.Context) (OTACheckResponse, error) {
	res, err := s.updater.CheckUpdate(ctx)
	if err != nil {
		return OTACheckResponse{}, err
	}
	s.mu.Lock()
	s.lastCheckRes = res
	s.stagedUpdate = nil
	s.mu.Unlock()
	return OTACheckResponse{OK: true, Result: *res}, nil
}

// DownloadOTAUpdate downloads and SHA-256-verifies the pending release asset.
func (s *Server) DownloadOTAUpdate(ctx context.Context) (OTADownloadResponse, error) {
	s.mu.RLock()
	checkRes := s.lastCheckRes
	s.mu.RUnlock()
	if checkRes == nil || checkRes.Asset == nil {
		return OTADownloadResponse{}, errors.New("请先检查更新并确认存在可下载的 Windows 资产")
	}

	staged, err := s.updater.DownloadAndStage(ctx, checkRes.LatestVersion, checkRes.Asset)
	if err != nil {
		return OTADownloadResponse{}, err
	}

	s.mu.Lock()
	s.stagedUpdate = staged
	s.mu.Unlock()
	return OTADownloadResponse{OK: true, Staged: *staged}, nil
}

// ApplyOTAUpdate starts the staged installer and triggers graceful app shutdown.
func (s *Server) ApplyOTAUpdate() (OKResponse, error) {
	s.mu.RLock()
	staged := s.stagedUpdate
	curPath := ""
	if !s.isMemoryOnly {
		curPath = s.currentFilePath
	}
	onQuit := s.onQuit
	s.mu.RUnlock()

	if staged == nil {
		return OKResponse{}, errors.New("尚无已就绪的更新包，请先点击下载")
	}

	if err := updater.ApplyAndRestart(*staged, "", curPath); err != nil {
		return OKResponse{}, fmt.Errorf("启动更新安装程序失败: %w", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		if onQuit != nil {
			onQuit()
		} else {
			os.Exit(0)
		}
	}()

	return OKResponse{OK: true}, nil
}

// watchLoop polls the current disk file for modifications every 400ms.
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
				s.TriggerEvent("reload")
			}
		}
	}
}
