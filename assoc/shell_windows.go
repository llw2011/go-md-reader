package assoc

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

func registerAppCapabilities(exePath string) {
	appPath := `Software\Classes\Applications\go-md-reader.exe`
	k, _, err := registry.CreateKey(registry.CURRENT_USER, appPath, registry.ALL_ACCESS)
	if err == nil {
		_ = k.SetStringValue("FriendlyAppName", AppName)
		k.Close()
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, appPath+`\shell\open\command`, registry.ALL_ACCESS)
	if err == nil {
		_ = cmdKey.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, exePath))
		cmdKey.Close()
	}

	supKey, _, err := registry.CreateKey(registry.CURRENT_USER, appPath+`\SupportedTypes`, registry.ALL_ACCESS)
	if err == nil {
		for _, item := range GetSupportedExtensions() {
			_ = supKey.SetStringValue(item.Ext, "")
		}
		supKey.Close()
	}

	// Register in RegisteredApplications for Windows Default Apps panel
	capPath := `Software\GoMarkdownReader\Capabilities`
	capKey, _, err := registry.CreateKey(registry.CURRENT_USER, capPath, registry.ALL_ACCESS)
	if err == nil {
		_ = capKey.SetStringValue("ApplicationName", AppName)
		_ = capKey.SetStringValue("ApplicationDescription", "纯净本地 Markdown 与 Office 文档转换阅读器")
		capKey.Close()
	}
	faKey, _, err := registry.CreateKey(registry.CURRENT_USER, capPath+`\FileAssociations`, registry.ALL_ACCESS)
	if err == nil {
		for _, item := range GetSupportedExtensions() {
			_ = faKey.SetStringValue(item.Ext, progIDForExt(item.Ext))
		}
		faKey.Close()
	}
	regAppKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.ALL_ACCESS)
	if err == nil {
		_ = regAppKey.SetStringValue(AppKeyName, capPath)
		regAppKey.Close()
	}
}

func registerContextMenu(exePath string) {
	menuPath := `Software\Classes\*\shell\` + AppKeyName
	k, _, err := registry.CreateKey(registry.CURRENT_USER, menuPath, registry.ALL_ACCESS)
	if err == nil {
		_ = k.SetStringValue("", "使用 Go Markdown Reader 打开")
		_ = k.SetStringValue("Icon", fmt.Sprintf(`"%s",0`, exePath))
		k.Close()
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, menuPath+`\command`, registry.ALL_ACCESS)
	if err == nil {
		_ = cmdKey.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, exePath))
		cmdKey.Close()
	}
}

func unregisterContextMenu() {
	deleteRegistryTree(registry.CURRENT_USER, `Software\Classes\*\shell\`+AppKeyName)
}

func deleteRegistryTree(root registry.Key, subkey string) error {
	subkeyUTF16, err := syscall.UTF16PtrFromString(subkey)
	if err != nil {
		return err
	}
	// ERROR_SUCCESS = 0, ERROR_FILE_NOT_FOUND = 2
	r1, _, _ := regDeleteTreeW.Call(uintptr(root), uintptr(unsafe.Pointer(subkeyUTF16)))
	if r1 == 0 || r1 == 2 {
		return nil
	}
	r2, _, _ := regDeleteKeyW.Call(uintptr(root), uintptr(unsafe.Pointer(subkeyUTF16)))
	if r2 == 0 || r2 == 2 {
		return nil
	}
	return syscall.Errno(r1)
}

// RefreshShell notifies Windows Explorer that file associations have changed
func RefreshShell() {
	// SHCNE_ASSOCCHANGED = 0x08000000, SHCNF_IDLIST = 0x0000
	_, _, _ = shChangeNotify.Call(0x08000000, 0x0000, 0, 0)
}

// CreateShortcut creates a Windows .lnk shortcut using WScript.Shell
func CreateShortcut(lnkPath, targetExe, description string) error {
	workDir := filepath.Dir(targetExe)
	psScript := fmt.Sprintf(
		`$s=(New-Object -COM WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Description='%s';$s.IconLocation='%s,0';$s.Save()`,
		strings.ReplaceAll(lnkPath, `'`, `''`),
		strings.ReplaceAll(targetExe, `'`, `''`),
		strings.ReplaceAll(workDir, `'`, `''`),
		strings.ReplaceAll(description, `'`, `''`),
		strings.ReplaceAll(targetExe, `'`, `''`),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", psScript)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

// RegisterUninstallEntry registers the app in Windows "Installed Apps / Programs and Features"
func RegisterUninstallEntry(installDir, exePath, uninstallerPath, version string, sizeKB uint32) error {
	uninstPath := `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + AppKeyName
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstPath, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()

	_ = k.SetStringValue("DisplayName", AppName)
	_ = k.SetStringValue("DisplayVersion", version)
	_ = k.SetStringValue("Publisher", "Local Open Source (Go Markdown Reader)")
	_ = k.SetStringValue("DisplayIcon", fmt.Sprintf(`"%s",0`, exePath))
	_ = k.SetStringValue("InstallLocation", installDir)
	_ = k.SetStringValue("InstallDate", time.Now().Format("20060102"))
	_ = k.SetStringValue("UninstallString", fmt.Sprintf(`"%s" --uninstall`, uninstallerPath))
	_ = k.SetStringValue("QuietUninstallString", fmt.Sprintf(`"%s" --uninstall --quiet`, uninstallerPath))
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	_ = k.SetDWordValue("EstimatedSize", sizeKB)
	return nil
}

// UnregisterUninstallEntry removes the app from Windows "Installed Apps"
func UnregisterUninstallEntry() {
	deleteRegistryTree(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\`+AppKeyName)
}
