package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jchv/go-webview2"
	"go-md-reader/assoc"
	"go-md-reader/dialog"
	"go-md-reader/server"
	"go-md-reader/tray"
	"go-md-reader/updater"
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

	// Isolated WebView2 runtime data path in LocalAppData
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp == "" {
		localApp = os.TempDir()
	}
	dataPath := filepath.Join(localApp, "GoMarkdownReader", "WebViewData")

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
