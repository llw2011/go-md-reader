package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AddonFile pins an individual asset belonging to a curated MIT addon by path and SHA-256.
type AddonFile struct {
	RelPath string `json:"relPath"` // slash-separated path relative to addon dir
	URLPath string `json:"urlPath"` // path relative to CDN base URL
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

// AddonSpec defines an official, vetted MIT open-source optional frontend enhancement.
type AddonSpec struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	License     string      `json:"license"`
	Copyright   string      `json:"copyright"`
	Description string      `json:"description"`
	Files       []AddonFile `json:"-"`
}

// AddonStatus describes local installation & activation state for UI display.
type AddonStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	License     string `json:"license"`
	Copyright   string `json:"copyright"`
	Description string `json:"description"`
	TotalBytes  int64  `json:"totalBytes"`
	Installed   bool   `json:"installed"`
	Enabled     bool   `json:"enabled"`
}

// InstallProgress reports byte progress during on-demand SHA-256 verified addon installation.
type InstallProgress struct {
	ID             string `json:"id"`
	Phase          string `json:"phase"` // "downloading" | "ready" | "error"
	CompletedFiles int    `json:"completedFiles"`
	TotalFiles     int    `json:"totalFiles"`
	BytesDone      int64  `json:"bytesDone"`
	BytesTotal     int64  `json:"bytesTotal"`
	Percent        int    `json:"percent"`
}

type addonState struct {
	Disabled map[string]bool `json:"disabled,omitempty"`
}

// Manager manages on-demand, SHA-256-verified local caching of curated MIT frontend addons.
type Manager struct {
	mu          sync.Mutex
	installing  map[string]bool
	rootDir     string
	statePath   string
	cdnBaseURL  string
	httpClient  *http.Client
	catalog     []AddonSpec
	catalogByID map[string]AddonSpec
}

// DefaultCatalog returns the pinned curated MIT addons (KaTeX + Mermaid).
func DefaultCatalog() []AddonSpec {
	return []AddonSpec{
		{
			ID:          "katex",
			Name:        "KaTeX 数学公式渲染",
			Version:     "0.19.1",
			License:     "MIT",
			Copyright:   "Copyright (c) Khan Academy and other contributors",
			Description: "支持离线高质量渲染行内 ($...$) 与块级 ($$...$$) LaTeX 数学公式",
			Files: []AddonFile{
				{RelPath: "katex.min.css", URLPath: "/katex@0.19.1/dist/katex.min.css", Size: 24793, SHA256: "00e0b094171b07f58422075f469b4feac2b2f7bce1aeec943c08da4d697f4dcf"},
				{RelPath: "katex.min.js", URLPath: "/katex@0.19.1/dist/katex.min.js", Size: 273081, SHA256: "fa140498b8cfc8302cbe5e7cb1bdbc1f952bc38b3fa258ede09e73edbf9a74bc"},
				{RelPath: "contrib/auto-render.min.js", URLPath: "/katex@0.19.1/dist/contrib/auto-render.min.js", Size: 3486, SHA256: "e5372d199bcdae8b4de71d0f7ceba72a4ba12774a27c60a6f1f77d03b3228ee4"},
				{RelPath: "fonts/KaTeX_AMS-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_AMS-Regular.woff2", Size: 28076, SHA256: "0cdd387c9590a1a9f9794560022dbb59654a7d86f187aa0c81495ad42d3a7308"},
				{RelPath: "fonts/KaTeX_Caligraphic-Bold.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Caligraphic-Bold.woff2", Size: 6912, SHA256: "de7701e42cf1f4cf0b766c03fb27977207eee2f4fd5d76fa82188406da43ea4c"},
				{RelPath: "fonts/KaTeX_Caligraphic-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Caligraphic-Regular.woff2", Size: 6908, SHA256: "5d53e70ad607c2352162dec9e0923fb54ecdafaccbf604cd8dcf7d00facb989b"},
				{RelPath: "fonts/KaTeX_Fraktur-Bold.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Fraktur-Bold.woff2", Size: 11348, SHA256: "74444efd593c005e3f4573b44524704c0af0a937fe911cca9e94068d0d140d3f"},
				{RelPath: "fonts/KaTeX_Fraktur-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Fraktur-Regular.woff2", Size: 11316, SHA256: "51814d270d06ff0255dba0799994fa4d8c84d11f09951d47595f4abb1f3602dc"},
				{RelPath: "fonts/KaTeX_Main-Bold.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Main-Bold.woff2", Size: 25324, SHA256: "0f60d1b897938ec918c8ce073092411baf9438f6739465693ff18b0f9d20b021"},
				{RelPath: "fonts/KaTeX_Main-BoldItalic.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Main-BoldItalic.woff2", Size: 16780, SHA256: "99cd42a3c072d918f2f44984a807cf7aa16e13545fd0875fc07c6c65f99e715b"},
				{RelPath: "fonts/KaTeX_Main-Italic.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Main-Italic.woff2", Size: 16988, SHA256: "97479ca6cce906abc961ecac96faa5f9ca2e61b8e7670d475826bcdee9a7c267"},
				{RelPath: "fonts/KaTeX_Main-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Main-Regular.woff2", Size: 26272, SHA256: "c2342cd8b869e01752a9321dc17213fc40d4d04c79688c1d43f2cf316abd7866"},
				{RelPath: "fonts/KaTeX_Math-BoldItalic.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Math-BoldItalic.woff2", Size: 16400, SHA256: "dc47344dbb6cb5b655c8460d561f4df5f501b90c804ad3c6cec65fe322351ab1"},
				{RelPath: "fonts/KaTeX_Math-Italic.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Math-Italic.woff2", Size: 16440, SHA256: "7af58c5ec8f132a2ddde9027c6d7814decce4d3b822a11192a42a20e2e973264"},
				{RelPath: "fonts/KaTeX_SansSerif-Bold.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_SansSerif-Bold.woff2", Size: 12216, SHA256: "e99ae51144bf1232efcc1bfe5add36262c6866b0faab24fa75740e1b98577a62"},
				{RelPath: "fonts/KaTeX_SansSerif-Italic.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_SansSerif-Italic.woff2", Size: 12028, SHA256: "00b26ac825e2095056396e0553b8ac26d3f8ad158c3826e28b4c45b385c4714a"},
				{RelPath: "fonts/KaTeX_SansSerif-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_SansSerif-Regular.woff2", Size: 10344, SHA256: "68e8c73ef42afd3ccec58bf0fba302cce448938e7fc020a5e31f8a952eee1342"},
				{RelPath: "fonts/KaTeX_Script-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Script-Regular.woff2", Size: 9644, SHA256: "036d4e95149b69ff9bcc0cd55771efeb25ffa3947293e69acd78d5ac328c684b"},
				{RelPath: "fonts/KaTeX_Size1-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Size1-Regular.woff2", Size: 5468, SHA256: "6b47c40166b6dbe21a5dfca7718413f2147fd2399be1ba605d8ad39cedf25dfe"},
				{RelPath: "fonts/KaTeX_Size2-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Size2-Regular.woff2", Size: 5208, SHA256: "d04c54219f9eaec6d4d4fd42dfb28785975a4794d6b2fc71e566b9cd6db842dd"},
				{RelPath: "fonts/KaTeX_Size3-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Size3-Regular.woff2", Size: 3624, SHA256: "73d591271b1604960cb10bb90fee021670af7297017e0e98480b332d11f51995"},
				{RelPath: "fonts/KaTeX_Size4-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Size4-Regular.woff2", Size: 4928, SHA256: "a4af7d414440a1c1790825cfb700cf9cf43b0f2c4b04f0ebc523011ad9853ec0"},
				{RelPath: "fonts/KaTeX_Typewriter-Regular.woff2", URLPath: "/katex@0.19.1/dist/fonts/KaTeX_Typewriter-Regular.woff2", Size: 13568, SHA256: "71d517d67827787cfabdf186914cc3358eda539e37931941f2b2fd4a21f68c0b"},
			},
		},
		{
			ID:          "mermaid",
			Name:        "Mermaid 流程图与时序图",
			Version:     "12.3.0",
			License:     "MIT",
			Copyright:   "Copyright (c) Knut Sveidqvist",
			Description: "将 mermaid 代码块离线渲染为矢量图表，并自动随 4 套护眼/深色主题切换配色",
			Files: []AddonFile{
				{RelPath: "mermaid.min.js", URLPath: "/mermaid@12.3.0/dist/mermaid.min.js", Size: 5537838, SHA256: "336df803d87a38b70a01e736e666c8f7601d2ce42f5e47c8fafe5d721074b9a3"},
			},
		},
	}
}

// NewManager creates an Addon Manager rooted at %LOCALAPPDATA%\GoMarkdownReader\addons (when rootDir is empty).
func NewManager(rootDir, cdnBaseURL string, catalog []AddonSpec) *Manager {
	if rootDir == "" {
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" {
			localApp = os.TempDir()
		}
		rootDir = filepath.Join(localApp, "GoMarkdownReader", "addons")
	}
	if cdnBaseURL == "" {
		cdnBaseURL = "https://cdn.jsdelivr.net/npm"
	}
	if len(catalog) == 0 {
		catalog = DefaultCatalog()
	}
	byID := make(map[string]AddonSpec, len(catalog))
	for _, spec := range catalog {
		byID[spec.ID] = spec
	}
	return &Manager{
		installing:  make(map[string]bool),
		rootDir:     rootDir,
		statePath:   filepath.Join(rootDir, "state.json"),
		cdnBaseURL:  strings.TrimRight(cdnBaseURL, "/"),
		httpClient:  &http.Client{Timeout: 3 * time.Minute},
		catalog:     catalog,
		catalogByID: byID,
	}
}

func (m *Manager) loadState() addonState {
	var st addonState
	if data, err := os.ReadFile(m.statePath); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Disabled == nil {
		st.Disabled = make(map[string]bool)
	}
	return st
}

func (m *Manager) saveState(st addonState) error {
	if err := os.MkdirAll(m.rootDir, 0755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.statePath, out, 0600)
}

func (m *Manager) isInstalled(spec AddonSpec) bool {
	for _, f := range spec.Files {
		p := filepath.Join(m.rootDir, spec.ID, filepath.FromSlash(f.RelPath))
		info, err := os.Stat(p)
		if err != nil || info.IsDir() || info.Size() <= 0 || (f.Size > 0 && info.Size() != f.Size) {
			return false
		}
	}
	return true
}

// List returns current status of all curated MIT addons.
func (m *Manager) List() []AddonStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	st := m.loadState()
	out := make([]AddonStatus, 0, len(m.catalog))
	for _, spec := range m.catalog {
		var total int64
		for _, f := range spec.Files {
			total += f.Size
		}
		installed := m.isInstalled(spec)
		out = append(out, AddonStatus{
			ID:          spec.ID,
			Name:        spec.Name,
			Version:     spec.Version,
			License:     spec.License,
			Copyright:   spec.Copyright,
			Description: spec.Description,
			TotalBytes:  total,
			Installed:   installed,
			Enabled:     installed && !st.Disabled[spec.ID],
		})
	}
	return out
}

// Install downloads (or reuses verified files), strictly checks SHA-256 digests, and activates the addon.
func (m *Manager) Install(ctx context.Context, id string, onProgress func(InstallProgress)) (AddonStatus, error) {
	m.mu.Lock()
	spec, ok := m.catalogByID[id]
	if !ok {
		m.mu.Unlock()
		return AddonStatus{}, fmt.Errorf("未知扩展: %s", id)
	}
	if m.installing[id] {
		m.mu.Unlock()
		return AddonStatus{}, fmt.Errorf("扩展正在安装中，请稍候")
	}
	m.installing[id] = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.installing, id)
		m.mu.Unlock()
	}()

	var totalBytes int64
	for _, f := range spec.Files {
		totalBytes += f.Size
	}

	report := func(phase string, doneFiles int, bytesDone int64) {
		if onProgress == nil {
			return
		}
		pct := 0
		if totalBytes > 0 {
			pct = min(100, max(0, int((bytesDone*100)/totalBytes)))
		} else if phase == "ready" {
			pct = 100
		}
		onProgress(InstallProgress{
			ID:             id,
			Phase:          phase,
			CompletedFiles: doneFiles,
			TotalFiles:     len(spec.Files),
			BytesDone:      bytesDone,
			BytesTotal:     totalBytes,
			Percent:        pct,
		})
	}

	var doneBytes int64
	report("downloading", 0, 0)

	for idx, f := range spec.Files {
		targetPath := filepath.Join(m.rootDir, spec.ID, filepath.FromSlash(f.RelPath))
		if matchesDigest(targetPath, f.SHA256) {
			doneBytes += f.Size
			report("downloading", idx+1, doneBytes)
			continue
		}
		if err := m.downloadAndVerifyFile(ctx, m.cdnBaseURL+f.URLPath, targetPath, f.SHA256); err != nil {
			report("error", idx, doneBytes)
			return AddonStatus{}, fmt.Errorf("下载 %s 失败: %w", f.RelPath, err)
		}
		doneBytes += f.Size
		report("downloading", idx+1, doneBytes)
	}

	m.mu.Lock()
	st := m.loadState()
	delete(st.Disabled, id)
	_ = m.saveState(st)
	m.mu.Unlock()

	report("ready", len(spec.Files), totalBytes)

	for _, s := range m.List() {
		if s.ID == id {
			return s, nil
		}
	}
	return AddonStatus{}, nil
}

// SetEnabled persists enable/disable toggle for an installed addon.
func (m *Manager) SetEnabled(id string, enabled bool) ([]AddonStatus, error) {
	m.mu.Lock()
	if _, ok := m.catalogByID[id]; !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("未知扩展: %s", id)
	}
	st := m.loadState()
	if enabled {
		delete(st.Disabled, id)
	} else {
		st.Disabled[id] = true
	}
	if err := m.saveState(st); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Unlock()
	return m.List(), nil
}

// Remove deletes cached addon files from disk.
func (m *Manager) Remove(id string) ([]AddonStatus, error) {
	m.mu.Lock()
	if _, ok := m.catalogByID[id]; !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("未知扩展: %s", id)
	}
	if m.installing[id] {
		m.mu.Unlock()
		return nil, fmt.Errorf("扩展正在安装中，请稍候再卸载")
	}
	_ = os.RemoveAll(filepath.Join(m.rootDir, id))
	st := m.loadState()
	delete(st.Disabled, id)
	_ = m.saveState(st)
	m.mu.Unlock()
	return m.List(), nil
}

// ResolveFile validates and resolves an allowlisted local asset file for serving over WebView2 loopback.
func (m *Manager) ResolveFile(id, slashRelPath string) (string, bool) {
	m.mu.Lock()
	spec, ok := m.catalogByID[id]
	st := m.loadState()
	disabled := st.Disabled[id]
	m.mu.Unlock()

	if !ok || disabled {
		return "", false
	}
	cleanRel := strings.TrimPrefix(path.Clean("/"+slashRelPath), "/")
	for _, f := range spec.Files {
		if f.RelPath == cleanRel {
			full := filepath.Join(m.rootDir, id, filepath.FromSlash(f.RelPath))
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				return full, true
			}
			return "", false
		}
	}
	return "", false
}

func matchesDigest(filePath, wantSHA256 string) bool {
	if wantSHA256 == "" {
		return false
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), wantSHA256)
}

func (m *Manager) downloadAndVerifyFile(ctx context.Context, url, targetPath, wantSHA256 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "GoMarkdownReader")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	partPath := targetPath + ".part"
	_ = os.Remove(partPath)

	out, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, 32<<20))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(partPath)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(partPath)
		return closeErr
	}

	gotSHA := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(gotSHA, wantSHA256) {
		_ = os.Remove(partPath)
		return fmt.Errorf("SHA-256 校验失败 (期望 %s，实际 %s)", wantSHA256, gotSHA)
	}

	_ = os.Remove(targetPath)
	return os.Rename(partPath, targetPath)
}
