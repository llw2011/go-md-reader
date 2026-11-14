package dialog

import (
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	getOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	getSaveFileNameW     = comdlg32.NewProc("GetSaveFileNameW")
	commDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	shell32              = syscall.NewLazyDLL("shell32.dll")
	shBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	shGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	ole32                = syscall.NewLazyDLL("ole32.dll")
	coTaskMemFree        = ole32.NewProc("CoTaskMemFree")
)

type browseInfoW struct {
	HwndOwner      uintptr
	PidlRoot       uintptr
	PszDisplayName *uint16
	LpszTitle      *uint16
	UlFlags        uint32
	Lpfn           uintptr
	LParam         uintptr
	IImage         int32
}

type openFileNameW struct {
	StructSize      uint32
	HwndOwner       uintptr
	HInstance       uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefExt          *uint16
	CustData        uintptr
	FnHook          uintptr
	TemplateName    *uint16
	PvReserved      uintptr
	DwReserved      uint32
	FlagsEx         uint32
}

const (
	ofnOverwritePrompt = 0x00000002
	ofnNoChangeDir     = 0x00000008
	ofnPathMustExist   = 0x00000800
	ofnFileMustExist   = 0x00001000
)

// encodeMultiSZ encodes a string (which may contain embedded NULs for Win32 filters) into UTF-16 with double-NUL termination.
func encodeMultiSZ(s string) (*uint16, []uint16) {
	encoded := utf16.Encode([]rune(s))
	// Ensure double trailing NUL for Win32 lpstrFilter buffers
	if len(encoded) < 2 || encoded[len(encoded)-1] != 0 || encoded[len(encoded)-2] != 0 {
		encoded = append(encoded, 0, 0)
	}
	return &encoded[0], encoded
}

// SelectMarkdownFile opens the standard Windows Open File Dialog with Word/Excel/PPT/Markdown/Code support
func SelectMarkdownFile() (string, error) {
	var ofn openFileNameW
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))

	filterStr := "支持的全部文档 (*.md;*.docx;*.xlsx;*.pptx;*.csv;*.txt;*.json)\x00*.md;*.markdown;*.docx;*.xlsx;*.pptx;*.csv;*.tsv;*.txt;*.json;*.yaml;*.yml;*.doc;*.xls\x00" +
		"Markdown 文档 (*.md;*.markdown)\x00*.md;*.markdown\x00" +
		"Word 文档 (*.docx;*.doc)\x00*.docx;*.doc\x00" +
		"Excel / 表格 (*.xlsx;*.xls;*.csv;*.tsv)\x00*.xlsx;*.xls;*.csv;*.tsv\x00" +
		"PPT 演示文稿 (*.pptx)\x00*.pptx\x00" +
		"所有文件 (*.*)\x00*.*\x00\x00"

	filterPtr, filterKeep := encodeMultiSZ(filterStr)
	ofn.Filter = filterPtr

	titleSlice, _ := syscall.UTF16FromString("选择要阅读或转换的本地文档")
	ofn.Title = &titleSlice[0]

	buf := make([]uint16, 32768)
	ofn.File = &buf[0]
	ofn.MaxFile = uint32(len(buf))
	ofn.Flags = ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir

	ret, _, _ := getOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filterKeep)
	runtime.KeepAlive(titleSlice)
	runtime.KeepAlive(buf)

	if ret == 0 {
		extErr, _, _ := commDlgExtendedError.Call()
		if extErr != 0 {
			return "", fmt.Errorf("打开文件对话框异常 (code=0x%x)", extErr)
		}
		return "", nil
	}

	return syscall.UTF16ToString(buf), nil
}

// SaveMarkdownFile opens the standard Windows Save File Dialog to save .md files
func SaveMarkdownFile(defaultName string) (string, error) {
	filterStr := "Markdown 文档 (*.md)\x00*.md\x00文本文档 (*.txt)\x00*.txt\x00所有文件 (*.*)\x00*.*\x00\x00"
	return runSaveFileDialog("另存为 Markdown 文档 (.md)", filterStr, "md", defaultName)
}

// SaveHTMLFile opens the standard Windows Save File Dialog to save standalone .html files
func SaveHTMLFile(defaultName string) (string, error) {
	filterStr := "独立离线网页 (*.html)\x00*.html;*.htm\x00所有文件 (*.*)\x00*.*\x00\x00"
	return runSaveFileDialog("导出独立离线网页 (.html)", filterStr, "html", defaultName)
}

func runSaveFileDialog(title, filterStr, defExt, defaultName string) (string, error) {
	var ofn openFileNameW
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))

	filterPtr, filterKeep := encodeMultiSZ(filterStr)
	ofn.Filter = filterPtr

	titleSlice, _ := syscall.UTF16FromString(title)
	ofn.Title = &titleSlice[0]

	defExtSlice, _ := syscall.UTF16FromString(defExt)
	ofn.DefExt = &defExtSlice[0]

	buf := make([]uint16, 32768)
	if defaultName != "" {
		if initSlice, err := syscall.UTF16FromString(defaultName); err == nil && len(initSlice) < len(buf) {
			copy(buf, initSlice)
		}
	}

	ofn.File = &buf[0]
	ofn.MaxFile = uint32(len(buf))
	ofn.Flags = ofnOverwritePrompt | ofnPathMustExist | ofnNoChangeDir

	ret, _, _ := getSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filterKeep)
	runtime.KeepAlive(titleSlice)
	runtime.KeepAlive(defExtSlice)
	runtime.KeepAlive(buf)

	if ret == 0 {
		extErr, _, _ := commDlgExtendedError.Call()
		if extErr != 0 {
			return "", fmt.Errorf("保存文件对话框异常 (code=0x%x)", extErr)
		}
		return "", nil
	}

	return syscall.UTF16ToString(buf), nil
}

// SelectFolder opens the Windows native folder browser dialog
func SelectFolder(title string) (string, error) {
	if title == "" {
		title = "选择安装目录"
	}
	titleUTF16, _ := syscall.UTF16PtrFromString(title)
	displayBuf := make([]uint16, 260)

	var bi browseInfoW
	bi.PszDisplayName = &displayBuf[0]
	bi.LpszTitle = titleUTF16
	bi.UlFlags = 0x00000001 | 0x00000040 // BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE

	pidl, _, _ := shBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", nil
	}
	defer coTaskMemFree.Call(pidl)

	pathBuf := make([]uint16, 260)
	ok, _, _ := shGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return "", nil
	}

	return syscall.UTF16ToString(pathBuf), nil
}

