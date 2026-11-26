package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// RecentFileEntry represents a recently opened document in persistent UI preferences.
type RecentFileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// UIPrefs stores reader preferences across random-port WebView2 restarts.
type UIPrefs struct {
	Theme           string            `json:"theme"`
	FontSize        int               `json:"fontSize"`
	FontFamily      string            `json:"fontFamily"`
	ContentWidth    string            `json:"contentWidth"`
	SidebarHidden   bool              `json:"sidebarHidden"`
	SidebarWidth    int               `json:"sidebarWidth"`
	RecentFiles     []RecentFileEntry `json:"recentFiles"`
	ScrollPositions map[string]int    `json:"scrollPositions"`
}

type prefsPatch struct {
	Theme           *string            `json:"theme,omitempty"`
	FontSize        *int               `json:"fontSize,omitempty"`
	FontFamily      *string            `json:"fontFamily,omitempty"`
	ContentWidth    *string            `json:"contentWidth,omitempty"`
	SidebarHidden   *bool              `json:"sidebarHidden,omitempty"`
	SidebarWidth    *int               `json:"sidebarWidth,omitempty"`
	RecentFiles     *[]RecentFileEntry `json:"recentFiles,omitempty"`
	ScrollPath      *string            `json:"scrollPath,omitempty"`
	ScrollTop       *int               `json:"scrollTop,omitempty"`
	ScrollPositions *map[string]int    `json:"scrollPositions,omitempty"`
}

type uiPrefsStore struct {
	mu       sync.Mutex
	filePath string
	prefs    UIPrefs
}

func defaultUIPrefsPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		} else {
			appData = os.TempDir()
		}
	}
	return filepath.Join(appData, "GoMarkdownReader", "ui_prefs.json")
}

func newUIPrefsStore(filePath string) *uiPrefsStore {
	if filePath == "" {
		filePath = defaultUIPrefsPath()
	}
	st := &uiPrefsStore{
		filePath: filePath,
		prefs: UIPrefs{
			Theme:           "light",
			FontSize:        16,
			FontFamily:      "sans",
			ContentWidth:    "standard",
			SidebarWidth:    292,
			ScrollPositions: map[string]int{},
		},
	}
	st.loadLocked()
	return st
}

func normalizeFontFamily(v string) string {
	if v == "serif" {
		return "serif"
	}
	return "sans"
}

func normalizeContentWidth(v string) string {
	switch v {
	case "narrow", "standard", "wide":
		return v
	default:
		return "standard"
	}
}

func (st *uiPrefsStore) loadLocked() {
	data, err := os.ReadFile(st.filePath)
	if err != nil {
		return
	}
	var loaded UIPrefs
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}
	if loaded.Theme != "" {
		st.prefs.Theme = loaded.Theme
	}
	if loaded.FontSize >= 12 && loaded.FontSize <= 26 {
		st.prefs.FontSize = loaded.FontSize
	}
	st.prefs.FontFamily = normalizeFontFamily(loaded.FontFamily)
	st.prefs.ContentWidth = normalizeContentWidth(loaded.ContentWidth)
	st.prefs.SidebarHidden = loaded.SidebarHidden
	if loaded.SidebarWidth >= 200 && loaded.SidebarWidth <= 520 {
		st.prefs.SidebarWidth = loaded.SidebarWidth
	}
	if len(loaded.RecentFiles) > 10 {
		loaded.RecentFiles = loaded.RecentFiles[:10]
	}
	st.prefs.RecentFiles = loaded.RecentFiles
	if loaded.ScrollPositions != nil {
		st.prefs.ScrollPositions = loaded.ScrollPositions
	}
}

func (st *uiPrefsStore) get() UIPrefs {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := st.prefs
	if st.prefs.RecentFiles != nil {
		out.RecentFiles = append([]RecentFileEntry(nil), st.prefs.RecentFiles...)
	}
	out.ScrollPositions = make(map[string]int, len(st.prefs.ScrollPositions))
	for k, v := range st.prefs.ScrollPositions {
		out.ScrollPositions[k] = v
	}
	return out
}

func (st *uiPrefsStore) applyPatch(p prefsPatch) UIPrefs {
	st.mu.Lock()
	defer st.mu.Unlock()

	if p.Theme != nil && *p.Theme != "" {
		st.prefs.Theme = *p.Theme
	}
	if p.FontSize != nil && *p.FontSize >= 12 && *p.FontSize <= 26 {
		st.prefs.FontSize = *p.FontSize
	}
	if p.FontFamily != nil {
		st.prefs.FontFamily = normalizeFontFamily(*p.FontFamily)
	}
	if p.ContentWidth != nil {
		st.prefs.ContentWidth = normalizeContentWidth(*p.ContentWidth)
	}
	if p.SidebarHidden != nil {
		st.prefs.SidebarHidden = *p.SidebarHidden
	}
	if p.SidebarWidth != nil && *p.SidebarWidth >= 200 && *p.SidebarWidth <= 520 {
		st.prefs.SidebarWidth = *p.SidebarWidth
	}
	if p.RecentFiles != nil {
		list := *p.RecentFiles
		if len(list) > 10 {
			list = list[:10]
		}
		st.prefs.RecentFiles = append([]RecentFileEntry(nil), list...)
	}
	if p.ScrollPositions != nil {
		st.prefs.ScrollPositions = *p.ScrollPositions
	}
	if p.ScrollPath != nil && *p.ScrollPath != "" && p.ScrollTop != nil {
		if st.prefs.ScrollPositions == nil {
			st.prefs.ScrollPositions = map[string]int{}
		}
		// Keep map bounded cleanly
		if len(st.prefs.ScrollPositions) >= 60 {
			for k := range st.prefs.ScrollPositions {
				if k != *p.ScrollPath {
					delete(st.prefs.ScrollPositions, k)
					break
				}
			}
		}
		st.prefs.ScrollPositions[*p.ScrollPath] = *p.ScrollTop
	}

	_ = st.saveLocked()
	return st.prefs
}

func (st *uiPrefsStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(st.filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st.prefs, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, st.filePath)
}
