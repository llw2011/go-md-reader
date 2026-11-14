package main

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2"
)

const (
	winGwlWndProc                = -4
	winWmSize                    = 0x0005
	winSizeRestored              = 0
	winSizeMaximized             = 2
	winWmGetMinMaxInfo           = 0x0024
	winWmClose                   = 0x0010
	winWmNcCalcSize              = 0x0083
	winWmNcActivate              = 0x0086
	winWmNclButtonDown           = 0x00A1
	winHtCaption                 = 2
	winHtLeft                    = 10
	winHtBottomRight             = 17
	winSwMaximize                = 3
	winSwMinimize                = 6
	winSwRestore                 = 9
	winSwpNoSize                 = 0x0001
	winSwpNoMove                 = 0x0002
	winSwpNoZOrder               = 0x0004
	winSwpFrameChanged           = 0x0020
	winMonitorDefaultToNearest   = 0x00000002
	dwmwaUseImmersiveDarkMode    = 20
	dwmwaWindowCornerPreference  = 33
	dwmwcpRound                  = 2
	minWindowTrackWidth          = 720
	minWindowTrackHeight         = 460
)

var (
	chromeUser32                 = syscall.NewLazyDLL("user32.dll")
	chromeDwmapi                 = syscall.NewLazyDLL("dwmapi.dll")
	winSetWindowLongPtrW         = chromeUser32.NewProc("SetWindowLongPtrW")
	winCallWindowProcW           = chromeUser32.NewProc("CallWindowProcW")
	winDefWindowProcW            = chromeUser32.NewProc("DefWindowProcW")
	winSetWindowPos              = chromeUser32.NewProc("SetWindowPos")
	winShowWindow                = chromeUser32.NewProc("ShowWindow")
	winIsZoomed                  = chromeUser32.NewProc("IsZoomed")
	winMonitorFromWindow         = chromeUser32.NewProc("MonitorFromWindow")
	winGetMonitorInfoW           = chromeUser32.NewProc("GetMonitorInfoW")
	winReleaseCapture            = chromeUser32.NewProc("ReleaseCapture")
	winSendMessageW              = chromeUser32.NewProc("SendMessageW")
	winPostMessageW              = chromeUser32.NewProc("PostMessageW")
	dwmSetWindowAttribute        = chromeDwmapi.NewProc("DwmSetWindowAttribute")
	dwmExtendFrameIntoClientArea = chromeDwmapi.NewProc("DwmExtendFrameIntoClientArea")
)

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type winPoint struct {
	X int32
	Y int32
}

type winMinMaxInfo struct {
	PtReserved     winPoint
	PtMaxSize      winPoint
	PtMaxPosition  winPoint
	PtMinTrackSize winPoint
	PtMaxTrackSize winPoint
}

type winMonitorInfo struct {
	CbSize    uint32
	RcMonitor winRect
	RcWork    winRect
	DwFlags   uint32
}

type winNcCalcSizeParams struct {
	Rgrc  [3]winRect
	Lppos uintptr
}

type dwmMargins struct {
	CxLeftWidth    int32
	CxRightWidth   int32
	CyTopHeight    int32
	CyBottomHeight int32
}

type chromeState struct {
	origWndProc   uintptr
	lastMaximized bool
	onMaxChanged  func(bool)
}

var (
	chromeMu      sync.RWMutex
	chromeStates  = map[uintptr]*chromeState{}
	chromeHookPtr = syscall.NewCallback(chromeWndProc)
)

func clampMaximizedRect(work, monitor winRect) winRect {
	if work == monitor && work.Bottom > work.Top+1 {
		work.Bottom--
	}
	return work
}

func isValidResizeHitTest(dir int) bool {
	return dir >= winHtLeft && dir <= winHtBottomRight
}

func installWindowChrome(hwnd uintptr, w webview2.WebView) {
	if hwnd == 0 || w == nil {
		return
	}

	idx := int32(winGwlWndProc)
	orig, _, _ := winSetWindowLongPtrW.Call(hwnd, uintptr(idx), chromeHookPtr)

	zoomed, _, _ := winIsZoomed.Call(hwnd)
	state := &chromeState{
		origWndProc:   orig,
		lastMaximized: zoomed != 0,
		onMaxChanged: func(max bool) {
			js := "if (window.__syncWindowMaximized) window.__syncWindowMaximized(false);"
			if max {
				js = "if (window.__syncWindowMaximized) window.__syncWindowMaximized(true);"
			}
			w.Dispatch(func() {
				w.Eval(js)
			})
		},
	}

	chromeMu.Lock()
	chromeStates[hwnd] = state
	chromeMu.Unlock()

	corner := uint32(dwmwcpRound)
	_, _, _ = dwmSetWindowAttribute.Call(hwnd, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), 4)

	margins := dwmMargins{CyTopHeight: 1}
	_, _, _ = dwmExtendFrameIntoClientArea.Call(hwnd, uintptr(unsafe.Pointer(&margins)))

	_, _, _ = winSetWindowPos.Call(
		hwnd, 0, 0, 0, 0, 0,
		winSwpNoMove|winSwpNoSize|winSwpNoZOrder|winSwpFrameChanged,
	)

	bindWindowControls(w, hwnd)
}

func bindWindowControls(w webview2.WebView, hwnd uintptr) {
	_ = w.Bind("__winMinimize", func() {
		_, _, _ = winShowWindow.Call(hwnd, winSwMinimize)
	})

	_ = w.Bind("__winToggleMaximize", func() bool {
		zoomed, _, _ := winIsZoomed.Call(hwnd)
		if zoomed != 0 {
			_, _, _ = winShowWindow.Call(hwnd, winSwRestore)
			return false
		}
		_, _, _ = winShowWindow.Call(hwnd, winSwMaximize)
		return true
	})

	_ = w.Bind("__winIsMaximized", func() bool {
		zoomed, _, _ := winIsZoomed.Call(hwnd)
		return zoomed != 0
	})

	_ = w.Bind("__winClose", func() {
		_, _, _ = winPostMessageW.Call(hwnd, winWmClose, 0, 0)
	})

	_ = w.Bind("__winStartDrag", func() {
		_, _, _ = winReleaseCapture.Call()
		_, _, _ = winSendMessageW.Call(hwnd, winWmNclButtonDown, winHtCaption, 0)
	})

	_ = w.Bind("__winStartResize", func(dir int) {
		if !isValidResizeHitTest(dir) {
			return
		}
		_, _, _ = winReleaseCapture.Call()
		_, _, _ = winSendMessageW.Call(hwnd, winWmNclButtonDown, uintptr(dir), 0)
	})

	_ = w.Bind("__winSetThemeDark", func(dark bool) {
		var val uint32
		if dark {
			val = 1
		}
		_, _, _ = dwmSetWindowAttribute.Call(hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&val)), 4)
	})
}

func chromeWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	chromeMu.RLock()
	st := chromeStates[hwnd]
	chromeMu.RUnlock()

	if st == nil {
		ret, _, _ := winDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return ret
	}

	switch msg {
	case winWmNcCalcSize:
		if wParam != 0 && lParam != 0 {
			zoomed, _, _ := winIsZoomed.Call(hwnd)
			if zoomed != 0 {
				hMon, _, _ := winMonitorFromWindow.Call(hwnd, winMonitorDefaultToNearest)
				if hMon != 0 {
					var mi winMonitorInfo
					mi.CbSize = uint32(unsafe.Sizeof(mi))
					ok, _, _ := winGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi)))
					if ok != 0 {
						p := (*winNcCalcSizeParams)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
						p.Rgrc[0] = clampMaximizedRect(mi.RcWork, mi.RcMonitor)
					}
				}
			}
			return 0
		}

	case winWmNcActivate:
		return 1

	case winWmGetMinMaxInfo:
		var ret uintptr
		if st.origWndProc != 0 {
			ret, _, _ = winCallWindowProcW.Call(st.origWndProc, hwnd, uintptr(msg), wParam, lParam)
		}
		if lParam != 0 {
			mmi := (*winMinMaxInfo)(*(*unsafe.Pointer)(unsafe.Pointer(&lParam)))
			if mmi.PtMinTrackSize.X < minWindowTrackWidth {
				mmi.PtMinTrackSize.X = minWindowTrackWidth
			}
			if mmi.PtMinTrackSize.Y < minWindowTrackHeight {
				mmi.PtMinTrackSize.Y = minWindowTrackHeight
			}
		}
		return ret

	case winWmSize:
		if wParam == winSizeMaximized || wParam == winSizeRestored {
			isMax := wParam == winSizeMaximized
			if isMax != st.lastMaximized {
				st.lastMaximized = isMax
				if st.onMaxChanged != nil {
					st.onMaxChanged(isMax)
				}
			}
		}
	}

	if st.origWndProc != 0 {
		ret, _, _ := winCallWindowProcW.Call(st.origWndProc, hwnd, uintptr(msg), wParam, lParam)
		return ret
	}
	ret, _, _ := winDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}
