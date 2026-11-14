package assoc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	notepadIFEOKey     = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Image File Execution Options\notepad.exe`
	notepadAppPathsKey = `Software\Microsoft\Windows\CurrentVersion\App Paths\notepad.exe`
	notepadAppsKey     = `Software\Classes\Applications\notepad.exe`
	backupIFEOVal      = "GoMD_BackupDebugger"
	backupAppPathVal   = "GoMD_BackupAppPath"
	backupAppsCmdVal   = "GoMD_BackupCommand"
	notepadProxyFlag   = "--notepad-proxy"
)

// NotepadStatus describes whether Windows Notepad is currently redirected to Go Markdown Reader.
type NotepadStatus struct {
	Replaced    bool   `json:"replaced"`
	SystemWide  bool   `json:"systemWide"`
	PerUser     bool   `json:"perUser"`
	ExePath     string `json:"exePath,omitempty"`
	Debugger    string `json:"debugger,omitempty"`
}

// GetNotepadStatus inspects HKLM IFEO and HKCU App Paths / Applications keys.
func GetNotepadStatus() NotepadStatus {
	var st NotepadStatus
	if exe, err := os.Executable(); err == nil {
		st.ExePath = exe
	}

	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, notepadIFEOKey, registry.QUERY_VALUE); err == nil {
		dbg, _, dErr := k.GetStringValue("Debugger")
		k.Close()
		if dErr == nil && dbg != "" {
			st.Debugger = dbg
			if isOurCommand(dbg) {
				st.SystemWide = true
			}
		}
	}

	if k, err := registry.OpenKey(registry.CURRENT_USER, notepadAppPathsKey, registry.QUERY_VALUE); err == nil {
		val, _, vErr := k.GetStringValue("")
		k.Close()
		if vErr == nil && isOurCommand(val) {
			st.PerUser = true
		}
	}

	if !st.PerUser {
		if k, err := registry.OpenKey(registry.CURRENT_USER, notepadAppsKey+`\shell\open\command`, registry.QUERY_VALUE); err == nil {
			val, _, vErr := k.GetStringValue("")
			k.Close()
			if vErr == nil && isOurCommand(val) {
				st.PerUser = true
			}
		}
	}

	st.Replaced = st.SystemWide || st.PerUser
	return st
}

func isOurCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, notepadProxyFlag) || strings.Contains(lower, "go-md-reader.exe")
}

// ReplaceNotepad configures Windows Notepad calls to launch Go Markdown Reader instead.
// Writes HKLM IFEO (triggering a standard UAC prompt if not elevated) and HKCU App Paths.
func ReplaceNotepad(exePath string) error {
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil {
			return err
		}
	}
	absExe, err := filepath.Abs(exePath)
	if err != nil {
		absExe = exePath
	}

	// Ensure IFEO + HKCU changes succeed together without leaving partial state if UAC is cancelled.
	if err := applyNotepadHKLM(absExe); err != nil {
		if isAccessDenied(err) {
			if elevErr := runElevatedHelper(absExe, fmt.Sprintf(`--apply-notepad "%s"`, absExe)); elevErr != nil {
				return elevErr
			}
		} else {
			return err
		}
	}

	applyNotepadHKCU(absExe)
	RefreshShell()
	return nil
}

// RestoreNotepad removes Go Markdown Reader's Notepad redirection and restores previous registry entries.
func RestoreNotepad() error {
	exePath, _ := os.Executable()
	absExe, err := filepath.Abs(exePath)
	if err != nil {
		absExe = exePath
	}

	// Check HKLM IFEO first so cancelling UAC leaves status coherent and reports the cancellation cleanly.
	if st := GetNotepadStatus(); st.SystemWide {
		if err := restoreNotepadHKLM(); err != nil {
			if isAccessDenied(err) {
				if elevErr := runElevatedHelper(absExe, "--restore-notepad"); elevErr != nil {
					return elevErr
				}
			} else {
				return err
			}
		}
	}

	restoreNotepadHKCU()
	RefreshShell()
	return nil
}

// ApplyNotepadIFEO writes HKLM IFEO Debugger directly (intended for elevated helper execution).
func ApplyNotepadIFEO(exePath string) error {
	if exePath == "" {
		return fmt.Errorf("executable path is required")
	}
	absExe, err := filepath.Abs(exePath)
	if err != nil {
		absExe = exePath
	}
	if err := applyNotepadHKLM(absExe); err != nil {
		return err
	}
	RefreshShell()
	return nil
}

// RestoreNotepadIFEO removes or restores HKLM IFEO Debugger directly (intended for elevated helper execution).
func RestoreNotepadIFEO() error {
	if err := restoreNotepadHKLM(); err != nil {
		return err
	}
	RefreshShell()
	return nil
}

func applyNotepadHKLM(absExe string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, notepadIFEOKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	prev, _, prevErr := k.GetStringValue("Debugger")
	if prevErr == nil && prev != "" && !isOurCommand(prev) {
		_ = k.SetStringValue(backupIFEOVal, prev)
	}

	dbgCmd := fmt.Sprintf(`"%s" %s`, absExe, notepadProxyFlag)
	return k.SetStringValue("Debugger", dbgCmd)
}

func restoreNotepadHKLM() error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, notepadIFEOKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}

	cur, _, curErr := k.GetStringValue("Debugger")
	backup, _, bErr := k.GetStringValue(backupIFEOVal)

	if curErr == nil && isOurCommand(cur) {
		if bErr == nil && backup != "" {
			if err := k.SetStringValue("Debugger", backup); err != nil {
				k.Close()
				return err
			}
		} else {
			if err := k.DeleteValue("Debugger"); err != nil && !errors.Is(err, registry.ErrNotExist) {
				k.Close()
				return err
			}
		}
	}
	_ = k.DeleteValue(backupIFEOVal)

	valNames, _ := k.ReadValueNames(8)
	subNames, _ := k.ReadSubKeyNames(8)
	k.Close()

	if len(valNames) == 0 && len(subNames) == 0 {
		_ = deleteRegistryTree(registry.LOCAL_MACHINE, notepadIFEOKey)
	}
	return nil
}

func applyNotepadHKCU(absExe string) {
	if k, _, err := registry.CreateKey(registry.CURRENT_USER, notepadAppPathsKey, registry.ALL_ACCESS); err == nil {
		prev, _, pErr := k.GetStringValue("")
		if pErr == nil && prev != "" && !isOurCommand(prev) {
			_ = k.SetStringValue(backupAppPathVal, prev)
		}
		_ = k.SetStringValue("", absExe)
		_ = k.SetStringValue("Path", filepath.Dir(absExe))
		k.Close()
	}

	if k, _, err := registry.CreateKey(registry.CURRENT_USER, notepadAppsKey, registry.ALL_ACCESS); err == nil {
		_ = k.SetStringValue("FriendlyAppName", AppName)
		k.Close()
	}
	if cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, notepadAppsKey+`\shell\open\command`, registry.ALL_ACCESS); err == nil {
		prev, _, pErr := cmdKey.GetStringValue("")
		if pErr == nil && prev != "" && !isOurCommand(prev) {
			_ = cmdKey.SetStringValue(backupAppsCmdVal, prev)
		}
		_ = cmdKey.SetStringValue("", fmt.Sprintf(`"%s" %s "%%1"`, absExe, notepadProxyFlag))
		cmdKey.Close()
	}
}

func restoreNotepadHKCU() {
	if k, err := registry.OpenKey(registry.CURRENT_USER, notepadAppPathsKey, registry.ALL_ACCESS); err == nil {
		cur, _, _ := k.GetStringValue("")
		backup, _, bErr := k.GetStringValue(backupAppPathVal)
		keepKey := false
		if isOurCommand(cur) {
			if bErr == nil && backup != "" {
				_ = k.SetStringValue("", backup)
				keepKey = true
			} else {
				_ = k.DeleteValue("")
				_ = k.DeleteValue("Path")
			}
		}
		_ = k.DeleteValue(backupAppPathVal)
		k.Close()
		if !keepKey {
			_ = deleteRegistryTree(registry.CURRENT_USER, notepadAppPathsKey)
		}
	}

	if cmdKey, err := registry.OpenKey(registry.CURRENT_USER, notepadAppsKey+`\shell\open\command`, registry.ALL_ACCESS); err == nil {
		cur, _, _ := cmdKey.GetStringValue("")
		backup, _, bErr := cmdKey.GetStringValue(backupAppsCmdVal)
		restored := false
		if isOurCommand(cur) && bErr == nil && backup != "" {
			_ = cmdKey.SetStringValue("", backup)
			restored = true
		}
		_ = cmdKey.DeleteValue(backupAppsCmdVal)
		cmdKey.Close()
		if !restored {
			_ = deleteRegistryTree(registry.CURRENT_USER, notepadAppsKey)
		}
	}
}

func isAccessDenied(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) ||
		strings.Contains(strings.ToLower(err.Error()), "access is denied")
}

type shellExecuteInfoW struct {
	CbSize       uint32
	FMask        uint32
	Hwnd         uintptr
	LpVerb       *uint16
	LpFile       *uint16
	LpParameters *uint16
	LpDirectory  *uint16
	NShow        int32
	HInstApp     uintptr
	LpIDList     uintptr
	LpClass      *uint16
	HkeyClass    uintptr
	DwHotKey     uint32
	HIcon        uintptr
	HProcess     uintptr
}

var shellExecuteExW = shell32.NewProc("ShellExecuteExW")

func runElevatedHelper(exePath, params string) error {
	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	filePtr, err := syscall.UTF16PtrFromString(exePath)
	if err != nil {
		return err
	}
	paramPtr, err := syscall.UTF16PtrFromString(params)
	if err != nil {
		return err
	}
	dirPtr, _ := syscall.UTF16PtrFromString(filepath.Dir(exePath))

	const (
		seeMaskNoCloseProcess = 0x00000040
		seeMaskNoUI           = 0x00000400
		swHide                = 0
	)

	sei := shellExecuteInfoW{
		CbSize:       uint32(unsafe.Sizeof(shellExecuteInfoW{})),
		FMask:        seeMaskNoCloseProcess | seeMaskNoUI,
		LpVerb:       verbPtr,
		LpFile:       filePtr,
		LpParameters: paramPtr,
		LpDirectory:  dirPtr,
		NShow:        swHide,
	}

	r1, _, callErr := shellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if r1 == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == windows.ERROR_CANCELLED {
			return fmt.Errorf("已取消管理员授权，系统记事本设置未变更")
		}
		return fmt.Errorf("请求管理员授权失败: %w", callErr)
	}
	if sei.HProcess != 0 {
		hProc := windows.Handle(sei.HProcess)
		defer windows.CloseHandle(hProc)
		_, _ = windows.WaitForSingleObject(hProc, 30000)
		var exitCode uint32
		if err := windows.GetExitCodeProcess(hProc, &exitCode); err == nil && exitCode != 0 {
			return fmt.Errorf("提权写入注册表返回异常退出码 (%d)", exitCode)
		}
	}
	return nil
}

// NormalizeNotepadArgs extracts the target document path from CLI args, filtering out
// Windows IFEO's prepended notepad.exe executable path and legacy Notepad flags.
func NormalizeNotepadArgs(args []string) (isProxy bool, docPath string) {
	if len(args) <= 1 {
		return false, ""
	}

	var rest []string
	for _, a := range args[1:] {
		if a == notepadProxyFlag {
			isProxy = true
			continue
		}
		rest = append(rest, a)
	}
	if !isProxy {
		return false, args[1]
	}

	var candidates []string
	for _, a := range rest {
		trim := strings.TrimSpace(a)
		if trim == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(strings.Trim(trim, `"`)))
		if base == "notepad.exe" || base == "notepad" {
			continue
		}
		upper := strings.ToUpper(trim)
		if upper == "/A" || upper == "/W" || upper == "/P" || upper == "/PT" || strings.HasPrefix(upper, "/.") {
			continue
		}
		candidates = append(candidates, trim)
	}

	if len(candidates) == 0 {
		return true, ""
	}
	if len(candidates) == 1 {
		return true, candidates[0]
	}
	// If Windows IFEO passed an unquoted path containing spaces, join and check stat first.
	joined := strings.Join(candidates, " ")
	if _, err := os.Stat(joined); err == nil {
		return true, joined
	}
	return true, candidates[0]
}
