package assoc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	ProgIDPrefix = "GoMarkdownReader"
	AppKeyName   = "GoMarkdownReader"
	AppName      = "Go Markdown Reader"
)

var (
	shell32        = syscall.NewLazyDLL("shell32.dll")
	shChangeNotify = shell32.NewProc("SHChangeNotify")
	advapi32       = syscall.NewLazyDLL("advapi32.dll")
	regDeleteTreeW = advapi32.NewProc("RegDeleteTreeW")
	regDeleteKeyW  = advapi32.NewProc("RegDeleteKeyW")
)

// ExtOption defines a supported file extension for association
type ExtOption struct {
	Ext            string `json:"ext"`
	Name           string `json:"name"`
	Category       string `json:"category"` // "markdown", "text", "data", "office"
	DefaultChecked bool   `json:"defaultChecked"`
	IsAssociated   bool   `json:"isAssociated"`
}

// Status represents current association state on the system
type Status struct {
	ExePath     string      `json:"exePath"`
	ContextMenu bool        `json:"contextMenu"`
	Extensions  []ExtOption `json:"extensions"`
}

// GetSupportedExtensions returns the catalog of configurable file formats
func GetSupportedExtensions() []ExtOption {
	return []ExtOption{
		{Ext: ".md", Name: "Markdown 文档 (.md)", Category: "markdown", DefaultChecked: true},
		{Ext: ".markdown", Name: "Markdown 扩展文档 (.markdown)", Category: "markdown", DefaultChecked: true},
		{Ext: ".txt", Name: "纯文本文档 (.txt)", Category: "text", DefaultChecked: true},
		{Ext: ".log", Name: "运行日志文件 (.log)", Category: "text", DefaultChecked: false},
		{Ext: ".ini", Name: "INI 配置文件 (.ini)", Category: "text", DefaultChecked: false},
		{Ext: ".conf", Name: "CONF 配置文件 (.conf)", Category: "text", DefaultChecked: false},
		{Ext: ".json", Name: "JSON 数据文件 (.json)", Category: "data", DefaultChecked: false},
		{Ext: ".yaml", Name: "YAML 配置文件 (.yaml)", Category: "data", DefaultChecked: false},
		{Ext: ".yml", Name: "YML 配置文件 (.yml)", Category: "data", DefaultChecked: false},
		{Ext: ".toml", Name: "TOML 配置文件 (.toml)", Category: "data", DefaultChecked: false},
		{Ext: ".csv", Name: "CSV 逗号分隔表格 (.csv)", Category: "data", DefaultChecked: false},
		{Ext: ".tsv", Name: "TSV 制表符表格 (.tsv)", Category: "data", DefaultChecked: false},
		{Ext: ".xml", Name: "XML 结构化文档 (.xml)", Category: "data", DefaultChecked: false},
		{Ext: ".docx", Name: "Word 文档 (.docx 转换阅读)", Category: "office", DefaultChecked: false},
		{Ext: ".xlsx", Name: "Excel 表格 (.xlsx 转换阅读)", Category: "office", DefaultChecked: false},
		{Ext: ".pptx", Name: "PPT 演示文稿 (.pptx 转换阅读)", Category: "office", DefaultChecked: false},
	}
}

func progIDForExt(ext string) string {
	clean := strings.TrimPrefix(strings.ToLower(ext), ".")
	return fmt.Sprintf("%s.%s", ProgIDPrefix, clean)
}

// GetStatus inspects the Windows Registry and returns current association states
func GetStatus(exePath string) Status {
	if exePath == "" {
		exePath, _ = os.Executable()
	}
	exts := GetSupportedExtensions()
	for i := range exts {
		exts[i].IsAssociated = isExtAssociated(exts[i].Ext)
	}

	ctxMenu := false
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\*\shell\`+AppKeyName, registry.QUERY_VALUE)
	if err == nil {
		ctxMenu = true
		k.Close()
	}

	return Status{
		ExePath:     exePath,
		ContextMenu: ctxMenu,
		Extensions:  exts,
	}
}

func isExtAssociated(ext string) bool {
	expectedProgID := progIDForExt(ext)

	// Check UserChoice first
	ucKey, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\`+ext+`\UserChoice`, registry.QUERY_VALUE)
	if err == nil {
		progID, _, err := ucKey.GetStringValue("ProgId")
		ucKey.Close()
		if err == nil && progID != "" {
			return strings.EqualFold(progID, expectedProgID)
		}
	}

	// Check HKCU\Software\Classes\<ext>
	clsKey, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+ext, registry.QUERY_VALUE)
	if err == nil {
		val, _, err := clsKey.GetStringValue("")
		clsKey.Close()
		if err == nil {
			return strings.EqualFold(val, expectedProgID)
		}
	}

	return false
}

// ApplyAssociations registers selected extensions and unregisters unselected ones cleanly
func ApplyAssociations(exePath string, selectedExts []string, enableContextMenu bool) error {
	absExe, err := filepath.Abs(exePath)
	if err != nil {
		absExe = exePath
	}

	selectedMap := make(map[string]bool)
	for _, e := range selectedExts {
		selectedMap[strings.ToLower(e)] = true
	}

	// 1. Register Application Capabilities in HKCU\Software\Classes\Applications\go-md-reader.exe
	registerAppCapabilities(absExe)

	// 2. Process each supported extension
	allExts := GetSupportedExtensions()
	for _, item := range allExts {
		ext := strings.ToLower(item.Ext)
		if selectedMap[ext] {
			if err := associateSingleExt(absExe, item.Ext, item.Name); err != nil {
				return err
			}
		} else {
			unassociateSingleExt(item.Ext)
		}
	}

	// 3. Context Menu ("使用 Go Markdown Reader 打开")
	if enableContextMenu {
		registerContextMenu(absExe)
	} else {
		unregisterContextMenu()
	}

	// 4. Notify Windows Explorer to refresh icons & file associations immediately
	RefreshShell()
	return nil
}

// RemoveAllAssociations cleans up all registry entries created by GoMarkdownReader
func RemoveAllAssociations() {
	allExts := GetSupportedExtensions()
	for _, item := range allExts {
		unassociateSingleExt(item.Ext)
		progID := progIDForExt(item.Ext)
		deleteRegistryTree(registry.CURRENT_USER, `Software\Classes\`+progID)
	}
	unregisterContextMenu()
	deleteRegistryTree(registry.CURRENT_USER, `Software\Classes\Applications\go-md-reader.exe`)
	deleteRegistryTree(registry.CURRENT_USER, `Software\GoMarkdownReader\Capabilities`)

	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\RegisteredApplications`, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(AppKeyName)
		k.Close()
	}

	RefreshShell()
}

func associateSingleExt(exePath, ext, friendlyName string) error {
	progID := progIDForExt(ext)
	progIDPath := `Software\Classes\` + progID

	// Create ProgID key
	k, _, err := registry.CreateKey(registry.CURRENT_USER, progIDPath, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	_ = k.SetStringValue("", friendlyName)
	_ = k.SetStringValue("FriendlyTypeName", friendlyName)
	k.Close()

	// DefaultIcon
	iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, progIDPath+`\DefaultIcon`, registry.ALL_ACCESS)
	if err == nil {
		_ = iconKey.SetStringValue("", fmt.Sprintf(`"%s",0`, exePath))
		iconKey.Close()
	}

	// shell\open\command
	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, progIDPath+`\shell\open\command`, registry.ALL_ACCESS)
	if err == nil {
		_ = cmdKey.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, exePath))
		cmdKey.Close()
	}

	// Set HKCU\Software\Classes\<ext> and backup previous default if not ours
	extKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+ext, registry.ALL_ACCESS)
	if err == nil {
		prev, _, err := extKey.GetStringValue("")
		if err == nil && prev != "" && !strings.HasPrefix(prev, ProgIDPrefix) {
			_ = extKey.SetStringValue("GoMD_BackupDefault", prev)
		}
		_ = extKey.SetStringValue("", progID)
		extKey.Close()
	}

	// Add to OpenWithProgids
	owKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+ext+`\OpenWithProgids`, registry.ALL_ACCESS)
	if err == nil {
		_ = owKey.SetBinaryValue(progID, []byte{})
		owKey.Close()
	}

	feKey, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\`+ext+`\OpenWithProgids`, registry.ALL_ACCESS)
	if err == nil {
		_ = feKey.SetBinaryValue(progID, []byte{})
		feKey.Close()
	}

	// Backup existing UserChoice ProgId (for transparency) and clear UserChoice override so HKCU\Software\Classes\<ext> takes effect
	ucPath := `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\` + ext + `\UserChoice`
	if ucKey, err := registry.OpenKey(registry.CURRENT_USER, ucPath, registry.QUERY_VALUE); err == nil {
		ucProg, _, pErr := ucKey.GetStringValue("ProgId")
		ucKey.Close()
		if pErr == nil && ucProg != "" && !strings.HasPrefix(ucProg, ProgIDPrefix) {
			if bKey, _, kErr := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\`+ext, registry.ALL_ACCESS); kErr == nil {
				_ = bKey.SetStringValue("GoMD_BackupUserChoice", ucProg)
				bKey.Close()
			}
		}
	}
	_ = deleteRegistryTree(registry.CURRENT_USER, ucPath)

	return nil
}

func unassociateSingleExt(ext string) {
	progID := progIDForExt(ext)

	extKey, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+ext, registry.ALL_ACCESS)
	if err == nil {
		cur, _, _ := extKey.GetStringValue("")
		if strings.EqualFold(cur, progID) {
			backup, _, bErr := extKey.GetStringValue("GoMD_BackupDefault")
			if bErr == nil && backup != "" {
				_ = extKey.SetStringValue("", backup)
				_ = extKey.DeleteValue("GoMD_BackupDefault")
			} else {
				_ = extKey.DeleteValue("")
			}
		}
		_ = extKey.DeleteValue("GoMD_BackupUserChoice")
		extKey.Close()
	}

	if owKey, err := registry.OpenKey(registry.CURRENT_USER, `Software\Classes\`+ext+`\OpenWithProgids`, registry.SET_VALUE); err == nil {
		_ = owKey.DeleteValue(progID)
		owKey.Close()
	}

	if feKey, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\`+ext+`\OpenWithProgids`, registry.SET_VALUE); err == nil {
		_ = feKey.DeleteValue(progID)
		feKey.Close()
	}

	// If UserChoice was pointing to our progID, remove it
	ucPath := `Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\` + ext + `\UserChoice`
	ucKey, err := registry.OpenKey(registry.CURRENT_USER, ucPath, registry.QUERY_VALUE)
	if err == nil {
		ucProg, _, _ := ucKey.GetStringValue("ProgId")
		ucKey.Close()
		if strings.EqualFold(ucProg, progID) {
			_ = deleteRegistryTree(registry.CURRENT_USER, ucPath)
		}
	}
}

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

