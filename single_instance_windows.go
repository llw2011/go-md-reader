package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const singleInstanceMutexName = `Local\GoMarkdownReader_SingleInstance_v1`

type instanceRecord struct {
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Nonce string `json:"nonce"`
}

func instanceRecordPath() string {
	localApp := os.Getenv("LOCALAPPDATA")
	if localApp == "" {
		localApp = os.TempDir()
	}
	return filepath.Join(localApp, "GoMarkdownReader", "instance.json")
}

var (
	kernel32DLL                = windows.NewLazySystemDLL("kernel32.dll")
	user32DLL                  = windows.NewLazySystemDLL("user32.dll")
	procCreateMutexW           = kernel32DLL.NewProc("CreateMutexW")
	procAllowSetForegroundWin  = user32DLL.NewProc("AllowSetForegroundWindow")
)

// acquireSingleInstanceMutex attempts to claim the per-session primary instance mutex.
// Returns (handle, true) if this process is the primary instance, or (handle, false) if
// another primary instance already holds it.
func acquireSingleInstanceMutex() (windows.Handle, bool) {
	return acquireNamedMutex(singleInstanceMutexName)
}

func acquireNamedMutex(name string) (windows.Handle, bool) {
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, true
	}
	// Call CreateMutexW directly so we inspect GetLastError() even when handle != 0;
	// golang.org/x/sys/windows.CreateMutex discards ERROR_ALREADY_EXISTS on non-zero handles.
	r0, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	h := windows.Handle(r0)
	if h == 0 {
		return 0, true
	}
	if errors.Is(lastErr, windows.ERROR_ALREADY_EXISTS) {
		return h, false
	}
	return h, true
}

func writeInstanceRecord(port int, nonce string) {
	p := instanceRecordPath()
	_ = os.MkdirAll(filepath.Dir(p), 0755)
	rec := instanceRecord{
		PID:   os.Getpid(),
		Port:  port,
		Nonce: nonce,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err == nil {
		_ = os.Rename(tmp, p)
	}
}

func removeInstanceRecord(pid int) {
	p := instanceRecordPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var rec instanceRecord
	if err := json.Unmarshal(data, &rec); err == nil && rec.PID == pid {
		_ = os.Remove(p)
	}
}

func activateExistingInstance(docPath string, timeout time.Duration) bool {
	p := instanceRecordPath()
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 800 * time.Millisecond}

	payloadBytes, _ := json.Marshal(map[string]string{"path": docPath})

	for {
		data, err := os.ReadFile(p)
		if err == nil {
			var rec instanceRecord
			if json.Unmarshal(data, &rec) == nil && rec.PID != os.Getpid() && rec.Port > 0 && rec.Nonce != "" {
				if isPIDAlive(rec.PID) {
					// Transfer Windows foreground lock privilege from this newly launched
					// Explorer/desktop process to the existing primary window process.
					if ok, _, _ := procAllowSetForegroundWin.Call(uintptr(rec.PID)); ok == 0 {
						const asfwAny = ^uintptr(0)
						_, _, _ = procAllowSetForegroundWin.Call(asfwAny)
					}
					url := fmt.Sprintf("http://127.0.0.1:%d/%s/activate", rec.Port, rec.Nonce)
					req, reqErr := http.NewRequest(http.MethodPost, url, bytes.NewReader(payloadBytes))
					if reqErr == nil {
						req.Header.Set("Content-Type", "application/json")
						resp, doErr := client.Do(req)
						if doErr == nil {
							_ = resp.Body.Close()
							if resp.StatusCode == http.StatusOK {
								return true
							}
						}
					}
				}
			}
		}

		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(80 * time.Millisecond)
	}
}

func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
