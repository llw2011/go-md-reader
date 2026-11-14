package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jchv/go-webview2"
	"go-md-reader/assoc"
	"go-md-reader/dialog"
	"go-md-reader/server"
	"go-md-reader/tray"
	"go-md-reader/updater"
	"golang.org/x/sys/windows"
)

//go:embed assets/*
var assetsFS embed.FS

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--apply-notepad":
			targetExe := ""
			if len(os.Args) > 2 {
				targetExe = os.Args[2]
			}
			if targetExe == "" {
				targetExe, _ = os.Executable()
			}
			if err := assoc.ApplyNotepadIFEO(targetExe); err != nil {
				os.Exit(1)
			}
			return
		case "--restore-notepad":
			if err := assoc.RestoreNotepadIFEO(); err != nil {
				os.Exit(1)
			}
			return
		}
	}

	_, initialFile := assoc.NormalizeNotepadArgs(os.Args)

	// Start internal server
	srv, err := server.New(assetsFS, initialFile)
	if err != nil {
		log.Fatalf("启动本地服务失败: %v", err)
	}

	go func() {
		if err := srv.Start(); err != nil {
			log.Printf("本地服务退出: %v", err)
		}
	}()

	// Per-process isolated WebView2 runtime data directory prevents lock/elevation conflicts
	// and stale cookie leaks across restarts; user preferences persist in AppData\ui_prefs.json.
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp == "" {
		localApp = os.TempDir()
	}
	sessionsRoot := filepath.Join(localApp, "GoMarkdownReader", "Sessions")
	sweepStaleSessionDirs(sessionsRoot)
	dataPath := filepath.Join(sessionsRoot, fmt.Sprintf("wv-%d", os.Getpid()))
	defer os.RemoveAll(dataPath)

	// Initialize Windows WebView2 Window
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  dataPath,
		WindowOptions: webview2.WindowOptions{
			Title:  "Go Markdown Reader",
			Width:  1280,
			Height: 820,
			IconId: 1,
			Center: true,
		},
	})

	if w == nil {
		fmt.Println("无法创建 WebView2 窗口。请确保已安装 Microsoft Edge WebView2 Runtime。")
		return
	}
	defer w.Destroy()

	trayMgr := tray.Start(tray.Config{
		MainHWND: uintptr(w.Window()),
		Tooltip:  "Go Markdown Reader " + updater.CurrentVersion,
		OnOpenFile: func() {
			if p, err := dialog.SelectMarkdownFile(); err == nil && p != "" {
				srv.SetCurrentFile(p)
			}
		},
		OnCheckUpdate: func() {
			srv.TriggerEvent("open-ota")
		},
		OnQuit: func() {
			w.Dispatch(func() {
				w.Terminate()
			})
		},
	})
	defer trayMgr.Stop()

	srv.SetTrayHandlers(trayMgr.MinimizeToTrayEnabled, trayMgr.SetMinimizeToTray, trayMgr.RequestQuit)

	w.Navigate(srv.URL())
	w.Run()
}

func sweepStaleSessionDirs(root string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	now := time.Now()
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "wv-") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "wv-"))
		if err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)); err == nil {
			var code uint32
			stillAlive := windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
			windows.CloseHandle(h)
			if stillAlive {
				continue
			}
		} else if info, sErr := e.Info(); sErr == nil && now.Sub(info.ModTime()) < 2*time.Minute {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, e.Name()))
	}
}
