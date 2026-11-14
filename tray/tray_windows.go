package tray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

const (
	wmNull           = 0x0000
	wmClose          = 0x0010
	wmQuit           = 0x0012
	wmCommand        = 0x0111
	wmApp            = 0x8000
	wmTrayCallback   = wmApp + 100
	wmLButtonUp      = 0x0202
	wmLButtonDblClk  = 0x0203
	wmRButtonUp      = 0x0205
	wmContextMenu    = 0x007B
	swHide           = 0
	swShow           = 5
	swRestore        = 9
	gwlWndProc       = -4 // GWLP_WNDPROC (passed as int32 cast to uintptr on Win64)
	nimAdd           = 0x00000000
	nimModify        = 0x00000001
	nimDelete        = 0x00000002
	nifMessage       = 0x00000001
	nifIcon          = 0x00000002
	nifTip           = 0x00000004
	mfString         = 0x00000000
	mfChecked        = 0x00000008
	mfUnchecked      = 0x00000000
	mfSeparator      = 0x00000800
	tpmRightButton   = 0x0002
	tpmBottomAlign   = 0x0020
	tpmLeftAlign     = 0x0000
	idiApplication   = 32512
	menuShowWindow   = 1001
	menuOpenFile     = 1002
	menuCheckUpdate  = 1003
	menuToggleMinTry = 1004
	menuQuitApp      = 1005
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	shell32              = syscall.NewLazyDLL("shell32.dll")
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	getModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	registerClassExW     = user32.NewProc("RegisterClassExW")
	createWindowExW      = user32.NewProc("CreateWindowExW")
	destroyWindow        = user32.NewProc("DestroyWindow")
	defWindowProcW       = user32.NewProc("DefWindowProcW")
	getMessageW          = user32.NewProc("GetMessageW")
	translateMessage     = user32.NewProc("TranslateMessage")
	dispatchMessageW     = user32.NewProc("DispatchMessageW")
	postMessageW         = user32.NewProc("PostMessageW")
	postQuitMessage      = user32.NewProc("PostQuitMessage")
	loadIconW            = user32.NewProc("LoadIconW")
	createPopupMenu      = user32.NewProc("CreatePopupMenu")
	appendMenuW          = user32.NewProc("AppendMenuW")
	setMenuDefaultItem   = user32.NewProc("SetMenuDefaultItem")
	trackPopupMenu       = user32.NewProc("TrackPopupMenu")
	destroyMenu          = user32.NewProc("DestroyMenu")
	getCursorPos         = user32.NewProc("GetCursorPos")
	setForegroundWindow  = user32.NewProc("SetForegroundWindow")
	showWindow           = user32.NewProc("ShowWindow")
	isIconic             = user32.NewProc("IsIconic")
	setWindowLongPtrW    = user32.NewProc("SetWindowLongPtrW")
	callWindowProcW      = user32.NewProc("CallWindowProcW")
	shellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	wmTaskbarCreatedMsg  uint32
	registerWindowMsgW   = user32.NewProc("RegisterWindowMessageW")
)

type point struct {
	X int32
	Y int32
}

type msgStruct struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type wndClassExW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type notifyIconDataW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

// Config configures the native Windows system tray manager.
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

func (m *Manager) runMessageLoop(ready chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := getModuleHandleW.Call(0)
	m.hInstance = hInst

	taskbarStr, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	msgID, _, _ := registerWindowMsgW.Call(uintptr(unsafe.Pointer(taskbarStr)))
	wmTaskbarCreatedMsg = uint32(msgID)

	// Load embedded app icon ID 1 from rsrc syso; fall back to default application icon
	icon, _, _ := loadIconW.Call(hInst, uintptr(1))
	if icon == 0 {
		icon, _, _ = loadIconW.Call(0, uintptr(idiApplication))
	}
	m.hIcon = icon

	className, _ := syscall.UTF16PtrFromString("GoMarkdownReaderTrayClass")
	winTitle, _ := syscall.UTF16PtrFromString("GoMarkdownReaderTrayWindow")

	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		LpfnWndProc:   syscall.NewCallback(trayWndProc),
		HInstance:     hInst,
		LpszClassName: className,
	}
	_, _, _ = registerClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := createWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(winTitle)),
		0, 0, 0, 0, 0, 0, 0, hInst, 0,
	)

	m.mu.Lock()
	m.trayHWND = hwnd
	m.mu.Unlock()

	if hwnd != 0 {
		m.addOrRefreshTrayIcon(nimAdd)
	}

	close(ready)

	var msg msgStruct
	for {
		ret, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = dispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (m *Manager) addOrRefreshTrayIcon(action uintptr) {
	m.mu.Lock()
	hwnd := m.trayHWND
	m.mu.Unlock()
	if hwnd == 0 {
		return
	}

	var nid notifyIconDataW
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmTrayCallback
	nid.HIcon = m.hIcon

	tip := m.cfg.Tooltip
	if tip == "" {
		tip = "Go Markdown Reader"
	}
	if tipUTF16, err := syscall.UTF16FromString(tip); err == nil {
		copy(nid.SzTip[:len(nid.SzTip)-1], tipUTF16)
	}

	_, _, _ = shellNotifyIconW.Call(action, uintptr(unsafe.Pointer(&nid)))
}

func (m *Manager) showContextMenu() {
	m.mu.Lock()
	hwnd := m.trayHWND
	m.mu.Unlock()
	if hwnd == 0 {
		return
	}

	hMenu, _, _ := createPopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer destroyMenu.Call(hMenu)

	appendText := func(id uintptr, flags uintptr, text string) {
		p, _ := syscall.UTF16PtrFromString(text)
		_, _, _ = appendMenuW.Call(hMenu, flags, id, uintptr(unsafe.Pointer(p)))
	}

	appendText(menuShowWindow, mfString, "显示主窗口 (Go Markdown Reader)")
	_, _, _ = setMenuDefaultItem.Call(hMenu, menuShowWindow, 0)
	appendText(menuOpenFile, mfString, "打开本地文档...")
	appendText(menuCheckUpdate, mfString, "检查更新 / OTA 设置...")
	_, _, _ = appendMenuW.Call(hMenu, mfSeparator, 0, 0)

	minFlag := uintptr(mfUnchecked)
	if m.minimizeToTray.Load() {
		minFlag = mfChecked
	}
	appendText(menuToggleMinTry, mfString|minFlag, "关闭窗口时最小化到托盘")
	_, _, _ = appendMenuW.Call(hMenu, mfSeparator, 0, 0)
	appendText(menuQuitApp, mfString, "完全退出")

	var pt point
	_, _, _ = getCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = setForegroundWindow.Call(hwnd)
	_, _, _ = trackPopupMenu.Call(
		hMenu,
		tpmRightButton|tpmBottomAlign|tpmLeftAlign,
		uintptr(pt.X),
		uintptr(pt.Y),
		0,
		hwnd,
		0,
	)
	_, _, _ = postMessageW.Call(hwnd, wmNull, 0, 0)
}

func mainWindowHookProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	activeMu.RLock()
	m := activeManager
	activeMu.RUnlock()

	if m == nil {
		return 0
	}

	if msg == wmClose && !m.forceQuit.Load() && m.minimizeToTray.Load() {
		m.HideMainWindow()
		return 0
	}

	if msg == wmClose {
		m.Stop()
	}

	if m.origWndProc != 0 {
		ret, _, _ := callWindowProcW.Call(m.origWndProc, hwnd, uintptr(msg), wParam, lParam)
		return ret
	}
	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	activeMu.RLock()
	m := activeManager
	activeMu.RUnlock()

	if m != nil && wmTaskbarCreatedMsg != 0 && msg == wmTaskbarCreatedMsg {
		m.addOrRefreshTrayIcon(nimAdd)
		return 0
	}

	switch msg {
	case wmTrayCallback:
		if m != nil {
			switch uint32(lParam) {
			case wmLButtonUp, wmLButtonDblClk:
				m.ShowMainWindow()
			case wmRButtonUp, wmContextMenu:
				m.showContextMenu()
			}
		}
		return 0

	case wmCommand:
		if m != nil {
			cmdID := uint32(wParam & 0xFFFF)
			switch cmdID {
			case menuShowWindow:
				m.ShowMainWindow()
			case menuOpenFile:
				m.ShowMainWindow()
				if m.cfg.OnOpenFile != nil {
					go m.cfg.OnOpenFile()
				}
			case menuCheckUpdate:
				m.ShowMainWindow()
				if m.cfg.OnCheckUpdate != nil {
					go m.cfg.OnCheckUpdate()
				}
			case menuToggleMinTry:
				m.SetMinimizeToTray(!m.minimizeToTray.Load())
			case menuQuitApp:
				m.RequestQuit()
			}
		}
		return 0

	case wmClose:
		_, _, _ = destroyWindow.Call(hwnd)
		_, _, _ = postQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := defWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}
