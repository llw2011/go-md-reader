package tray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

type Config struct {
	MainHWND      uintptr
	Tooltip       string
	OnOpenFile    func()
	OnCheckUpdate func()
	OnQuit        func()
}

type prefs struct {
	MinimizeToTray bool `json:"minimizeToTray"`
}

// Manager controls the Windows notification area icon and window close-to-tray lifecycle.
type Manager struct {
	cfg            Config
	trayHWND       uintptr
	origWndProc    uintptr
	hInstance      uintptr
	hIcon          uintptr
	minimizeToTray atomic.Bool
	forceQuit      atomic.Bool
	stopped        atomic.Bool
	mu             sync.Mutex
}

var (
	activeMu      sync.RWMutex
	activeManager *Manager
)

func prefsPath() string {
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp == "" {
		localApp = os.TempDir()
	}
	return filepath.Join(localApp, "GoMarkdownReader", "tray.json")
}

func loadPrefs() prefs {
	p := prefs{MinimizeToTray: true}
	data, err := os.ReadFile(prefsPath())
	if err == nil {
		_ = json.Unmarshal(data, &p)
	}
	return p
}

func savePrefs(p prefs) {
	path := prefsPath()
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	if data, err := json.MarshalIndent(p, "", "  "); err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}

// Start initializes the system tray icon thread and attaches close-to-tray handling to MainHWND.
func Start(cfg Config) *Manager {
	p := loadPrefs()
	m := &Manager{cfg: cfg}
	m.minimizeToTray.Store(p.MinimizeToTray)

	activeMu.Lock()
	activeManager = m
	activeMu.Unlock()

	if cfg.MainHWND != 0 {
		hookPtr := syscall.NewCallback(mainWindowHookProc)
		idx := int32(gwlWndProc)
		orig, _, _ := setWindowLongPtrW.Call(cfg.MainHWND, uintptr(idx), hookPtr)
		m.origWndProc = orig
	}

	ready := make(chan struct{})
	go m.runMessageLoop(ready)
	<-ready

	return m
}

// MinimizeToTrayEnabled returns whether closing the main window hides it to tray instead of exiting.
func (m *Manager) MinimizeToTrayEnabled() bool {
	if m == nil {
		return false
	}
	return m.minimizeToTray.Load()
}

// SetMinimizeToTray updates and persists the close-to-tray preference.
func (m *Manager) SetMinimizeToTray(enabled bool) {
	if m == nil {
		return
	}
	m.minimizeToTray.Store(enabled)
	savePrefs(prefs{MinimizeToTray: enabled})
}

// ShowMainWindow restores and focuses the main reader window.
func (m *Manager) ShowMainWindow() {
	if m == nil || m.cfg.MainHWND == 0 {
		return
	}
	iconic, _, _ := isIconic.Call(m.cfg.MainHWND)
	if iconic != 0 {
		_, _, _ = showWindow.Call(m.cfg.MainHWND, swRestore)
	} else {
		_, _, _ = showWindow.Call(m.cfg.MainHWND, swShow)
	}
	_, _, _ = setForegroundWindow.Call(m.cfg.MainHWND)
}

// HideMainWindow hides the main window cleanly to the system tray.
func (m *Manager) HideMainWindow() {
	if m == nil || m.cfg.MainHWND == 0 {
		return
	}
	_, _, _ = showWindow.Call(m.cfg.MainHWND, swHide)
}

func shouldShowOnToggle(visible, iconic bool) bool {
	return !visible || iconic
}

// IsMainWindowShown reports whether the main window is currently visible and not minimized.
func (m *Manager) IsMainWindowShown() bool {
	if m == nil || m.cfg.MainHWND == 0 {
		return false
	}
	vis, _, _ := isWindowVisible.Call(m.cfg.MainHWND)
	ico, _, _ := isIconic.Call(m.cfg.MainHWND)
	return !shouldShowOnToggle(vis != 0, ico != 0)
}

// ToggleMainWindow shows/restores the main window when hidden or minimized, and hides it to tray when shown.
func (m *Manager) ToggleMainWindow() {
	if m == nil || m.cfg.MainHWND == 0 {
		return
	}
	if m.IsMainWindowShown() {
		m.HideMainWindow()
	} else {
		m.ShowMainWindow()
	}
}

// RequestQuit forces a full application shutdown (bypassing close-to-tray).
func (m *Manager) RequestQuit() {
	if m == nil {
		return
	}
	m.forceQuit.Store(true)
	m.Stop()
	if m.cfg.OnQuit != nil {
		m.cfg.OnQuit()
	}
}

// Stop removes the system tray icon and shuts down the tray message loop.
func (m *Manager) Stop() {
	if m == nil || !m.stopped.CompareAndSwap(false, true) {
		return
	}
	m.mu.Lock()
	hwnd := m.trayHWND
	m.mu.Unlock()

	if hwnd != 0 {
		var nid notifyIconDataW
		nid.CbSize = uint32(unsafe.Sizeof(nid))
		nid.HWnd = hwnd
		nid.UID = 1
		_, _, _ = shellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
		_, _, _ = postMessageW.Call(hwnd, wmClose, 0, 0)
	}
}
