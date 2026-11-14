package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jchv/go-webview2"
	"go-md-reader/assoc"
	"go-md-reader/dialog"
	"go-md-reader/updater"
	"golang.org/x/sys/windows"
)

//go:embed setup.html
var setupHTML []byte

func requestWindowExit(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		if wvWindow == nil {
			return
		}
		wvWindow.Dispatch(func() {
			wvWindow.Terminate()
		})
	}()
}

//go:embed payload/go-md-reader.exe
var payloadExe []byte

//go:embed payload/sample.md
var payloadSample []byte

var (
	installedDir      string
	wvWindow          webview2.WebView
	cleanupMu         sync.Mutex
	pendingCleanupDir string
)

func main() {
	selfExe, _ := os.Executable()
	selfName := strings.ToLower(filepath.Base(selfExe))

	mode := "install"
	quiet := false
	for _, arg := range os.Args[1:] {
		if arg == "--uninstall" || arg == "/uninstall" {
			mode = "uninstall"
		} else if arg == "--quiet" || arg == "/S" || arg == "/quiet" {
			quiet = true
		}
	}
	if strings.HasPrefix(selfName, "uninstall") {
		mode = "uninstall"
	}

	localApp := os.Getenv("LOCALAPPDATA")
	if localApp == "" {
		localApp = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	defaultDir := filepath.Join(localApp, "Programs", "GoMarkdownReader")
	if mode == "uninstall" && strings.HasPrefix(selfName, "uninstall") {
		defaultDir = filepath.Dir(selfExe)
	}
	installedDir = defaultDir

	if mode == "uninstall" && quiet {
		_, err := performUninstall(defaultDir)
		cleanupPendingDir()
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(setupHTML)
	})

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"mode":       mode,
			"version":    updater.CurrentVersion,
			"defaultDir": defaultDir,
			"extensions": assoc.GetSupportedExtensions(),
		})
	})

	mux.HandleFunc("/api/browse-folder", func(w http.ResponseWriter, r *http.Request) {
		chosen, _ := dialog.SelectFolder("选择 Go Markdown Reader 安装目录")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"path": chosen,
		})
	})

	mux.HandleFunc("/api/install", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		var req struct {
			InstallDir        string   `json:"installDir"`
			Extensions        []string `json:"extensions"`
			DesktopShortcut   bool     `json:"desktopShortcut"`
			StartMenuShortcut bool     `json:"startMenuShortcut"`
			ContextMenu       bool     `json:"contextMenu"`
			RegisterUninstall bool     `json:"registerUninstall"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}

		targetDir := strings.TrimSpace(req.InstallDir)
		if targetDir == "" {
			targetDir = defaultDir
		}
		installedDir = targetDir

		logs, err := performInstall(selfExe, targetDir, req.Extensions, req.DesktopShortcut, req.StartMenuShortcut, req.ContextMenu, req.RegisterUninstall)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "logs": logs})
	})

	mux.HandleFunc("/api/uninstall", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		logs, err := performUninstall(defaultDir)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true, "logs": logs})
	})

	mux.HandleFunc("/api/launch", func(w http.ResponseWriter, r *http.Request) {
		targetExe := filepath.Join(installedDir, "go-md-reader.exe")
		sampleDoc := filepath.Join(installedDir, "sample.md")
		cmd := exec.Command(targetExe, sampleDoc)
		_ = cmd.Start()
		_, _ = w.Write([]byte(`{"ok":true}`))
		requestWindowExit(150 * time.Millisecond)
	})

	mux.HandleFunc("/api/exit", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
		requestWindowExit(80 * time.Millisecond)
	})

	go func() {
		_ = http.Serve(ln, mux)
	}()

	title := fmt.Sprintf("Go Markdown Reader %s 安装向导", updater.CurrentVersion)
	if mode == "uninstall" {
		title = "Go Markdown Reader 卸载向导"
	}

	wvData := filepath.Join(os.TempDir(), "GoMDSetup_WebView")
	wvWindow = webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  wvData,
		WindowOptions: webview2.WindowOptions{
			Title:  title,
			Width:  690,
			Height: 650,
			IconId: 1,
			Center: true,
		},
	})
	if wvWindow == nil {
		return
	}

	wvWindow.Navigate(fmt.Sprintf("http://127.0.0.1:%d/", port))
	wvWindow.Run()
	wvWindow.Destroy()
	cleanupPendingDir()
}

func isProcessRunning(targetExeName string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)

	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	if err := windows.Process32First(snap, &pe); err != nil {
		return false
	}
	for {
		exe := windows.UTF16ToString(pe.ExeFile[:])
		if strings.EqualFold(exe, targetExeName) {
			return true
		}
		if err := windows.Process32Next(snap, &pe); err != nil {
			break
		}
	}
	return false
}

func cleanupPendingDir() {
	cleanupMu.Lock()
	dir := pendingCleanupDir
	pendingCleanupDir = ""
	cleanupMu.Unlock()

	if dir == "" {
		return
	}
	// Run cmd cleanup from SystemRoot/Temp only after WebView2 & main loop have terminated
	cleanCmd := exec.Command("cmd.exe", "/C", fmt.Sprintf(`ping 127.0.0.1 -n 3 > nul & rmdir /S /Q "%s"`, dir))
	cleanCmd.Dir = os.TempDir()
	cleanCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cleanCmd.Start()
}

func performInstall(selfExe, targetDir string, exts []string, desktopLnk, startMenuLnk, ctxMenu, regUninst bool) ([]string, error) {
	if isProcessRunning("go-md-reader.exe") {
		return nil, fmt.Errorf("检测到 Go Markdown Reader 正在运行，请先关闭阅读器窗口后再继续安装或更新")
	}

	var logs []string

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("创建安装目录失败: %w", err)
	}

	targetExe := filepath.Join(targetDir, "go-md-reader.exe")
	if err := os.WriteFile(targetExe, payloadExe, 0755); err != nil {
		return nil, fmt.Errorf("写入主程序失败 (请确认程序未在运行): %w", err)
	}
	logs = append(logs, fmt.Sprintf("主程序已安装至: %s", targetExe))

	samplePath := filepath.Join(targetDir, "sample.md")
	_ = os.WriteFile(samplePath, payloadSample, 0644)

	uninstExe := filepath.Join(targetDir, "Uninstall.exe")
	if selfBytes, err := os.ReadFile(selfExe); err == nil {
		if !strings.EqualFold(selfExe, uninstExe) {
			_ = os.WriteFile(uninstExe, selfBytes, 0755)
		}
	}

	if err := assoc.ApplyAssociations(targetExe, exts, ctxMenu); err != nil {
		return nil, fmt.Errorf("配置系统文件关联失败: %w", err)
	}
	if len(exts) > 0 {
		logs = append(logs, fmt.Sprintf("已成功关联格式: %s", strings.Join(exts, ", ")))
	} else {
		logs = append(logs, "未修改任何默认文件格式关联（保持系统现状）")
	}

	if ctxMenu {
		logs = append(logs, "已添加 Windows 系统右键菜单快捷打开项")
	}

	userProfile := os.Getenv("USERPROFILE")
	appData := os.Getenv("APPDATA")

	if desktopLnk && userProfile != "" {
		lnkPath := filepath.Join(userProfile, "Desktop", "Go Markdown Reader.lnk")
		if err := assoc.CreateShortcut(lnkPath, targetExe, "纯净本地 Markdown 与 Office 文档阅读器"); err == nil {
			logs = append(logs, "已创建桌面快捷方式")
		}
	}

	if startMenuLnk && appData != "" {
		smDir := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs")
		lnkPath := filepath.Join(smDir, "Go Markdown Reader.lnk")
		if err := assoc.CreateShortcut(lnkPath, targetExe, "纯净本地 Markdown 与 Office 文档阅读器"); err == nil {
			logs = append(logs, "已创建开始菜单程序快捷方式")
		}
	}

	if regUninst {
		sizeKB := uint32(len(payloadExe) / 1024)
		ver := strings.TrimPrefix(updater.CurrentVersion, "v")
		_ = assoc.RegisterUninstallEntry(targetDir, targetExe, uninstExe, ver, sizeKB)
		logs = append(logs, "已注册独立卸载程序 (Uninstall.exe)")
	}

	return logs, nil
}

func performUninstall(installDir string) ([]string, error) {
	if isProcessRunning("go-md-reader.exe") {
		return nil, fmt.Errorf("检测到 Go Markdown Reader 正在运行，请先关闭阅读器窗口后再执行卸载")
	}

	var logs []string

	// 1. Restore system Notepad if replaced
	if assoc.GetNotepadStatus().Replaced {
		if err := assoc.RestoreNotepad(); err == nil {
			logs = append(logs, "已恢复系统默认记事本 (Notepad)")
		}
	}

	// 2. Remove all registry file associations & restore backups
	assoc.RemoveAllAssociations()
	logs = append(logs, "已清除所有文件关联并还原系统原有默认打开方式")

	// 2. Remove shortcuts
	userProfile := os.Getenv("USERPROFILE")
	appData := os.Getenv("APPDATA")
	if userProfile != "" {
		_ = os.Remove(filepath.Join(userProfile, "Desktop", "Go Markdown Reader.lnk"))
	}
	if appData != "" {
		_ = os.Remove(filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Go Markdown Reader.lnk"))
	}
	logs = append(logs, "已清理桌面与开始菜单快捷方式")

	// 3. Remove Uninstall registry entry
	assoc.UnregisterUninstallEntry()
	logs = append(logs, "已移除系统已安装应用注册表项")

	// 4. Clean up WebView2 cache folder in LocalAppData
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp != "" {
		_ = os.RemoveAll(filepath.Join(localApp, "GoMarkdownReader"))
	}

	// 5. Remove payload files now; schedule self-folder removal after GUI exits so Uninstall.exe lock is released
	if installDir != "" {
		_ = os.Remove(filepath.Join(installDir, "go-md-reader.exe"))
		_ = os.Remove(filepath.Join(installDir, "sample.md"))
		cleanupMu.Lock()
		pendingCleanupDir = installDir
		cleanupMu.Unlock()
		logs = append(logs, fmt.Sprintf("将在退出向导后彻底清除安装目录: %s", installDir))
	}

	return logs, nil
}
