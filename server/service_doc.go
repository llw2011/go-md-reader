package server

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-md-reader/converter"
	"go-md-reader/dialog"
	"go-md-reader/parser"
)

const maxDecodedBinaryBytes = 64 << 20

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
			kind := "other"
			if errors.Is(err, fs.ErrNotExist) {
				kind = "notfound"
			} else if errors.Is(err, fs.ErrPermission) {
				kind = "denied"
			}
			return DocumentResponse{
				HasFile:       true,
				DocSeq:        docSeq,
				Filename:      displayName,
				Filepath:      displayPath,
				LoadErrorKind: kind,
				LoadError:     err.Error(),
			}
		}
	}

	convResult := converter.ConvertToMarkdown(displayName, rawContent)
	finalMarkdown := []byte(convResult.Markdown)

	result, err := s.parser.Parse(finalMarkdown)
	if err != nil {
		return DocumentResponse{
			HasFile:       true,
			DocSeq:        docSeq,
			Filename:      displayName,
			Filepath:      displayPath,
			LoadErrorKind: "parse",
			LoadError:     err.Error(),
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
		".pptx": true, ".epub": true, ".html": true, ".htm": true,
		".json": true, ".yaml": true, ".yml": true,
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
		s.beginSelfWrite()
		writeErr := os.WriteFile(targetPath, []byte(req.Content), 0644)
		if writeErr == nil {
			s.setCurrentFile(targetPath, false)
		}
		s.endSelfWrite()
		if writeErr != nil {
			return SaveResponse{}, writeErr
		}
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
