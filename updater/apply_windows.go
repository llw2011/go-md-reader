package updater

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func CleanupOldBinary(exePath string) {
	if exePath == "" {
		var err error
		exePath, err = os.Executable()
		if err != nil || exePath == "" {
			return
		}
	}
	bakPath := exePath + ".old"
	if _, err := os.Stat(bakPath); err == nil {
		_ = os.Remove(bakPath)
	}
}

// ApplyAndRestart schedules replacement/installer execution after current PID exits.
func ApplyAndRestart(staged StagedUpdate, currentExe, openDocPath string) error {
	if staged.FilePath == "" {
		return fmt.Errorf("更新文件路径为空")
	}
	if _, err := os.Stat(staged.FilePath); err != nil {
		return fmt.Errorf("未找到已下载的更新文件: %w", err)
	}
	if staged.SHA256 != "" {
		actualSHA, _, err := computeFileDigest(staged.FilePath)
		if err != nil {
			return fmt.Errorf("读取更新包校验失败: %w", err)
		}
		if !strings.EqualFold(actualSHA, staged.SHA256) {
			return fmt.Errorf("本地暂存更新包完整性异常，请重新下载")
		}
	}

	pid := os.Getpid()
	if !staged.IsInstaller && currentExe == "" {
		var err error
		currentExe, err = os.Executable()
		if err != nil {
			return err
		}
	}

	script := buildRestartScript(pid, staged, currentExe, openDocPath)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

func buildRestartScript(pid int, staged StagedUpdate, currentExe, openDocPath string) string {
	escPS := func(s string) string {
		return strings.ReplaceAll(s, `'`, `''`)
	}

	if staged.IsInstaller {
		return fmt.Sprintf(
			`$p = Get-Process -Id %d -ErrorAction SilentlyContinue; `+
				`if ($p) { try { $p.WaitForExit(15000) | Out-Null } catch {} }; `+
				`Start-Sleep -Milliseconds 300; `+
				`Start-Process -FilePath '%s'`,
			pid,
			escPS(staged.FilePath),
		)
	}

	argPart := ""
	if openDocPath != "" {
		// Quote openDocPath for CreateProcessW so spaces (e.g. "jev api key.txt") are never split across os.Args.
		quoted := `"` + strings.ReplaceAll(openDocPath, `"`, `\"`) + `"`
		argPart = fmt.Sprintf(` -ArgumentList '%s'`, escPS(quoted))
	}
	return fmt.Sprintf(
		`$p = Get-Process -Id %d -ErrorAction SilentlyContinue; `+
			`if ($p) { try { $p.WaitForExit(15000) | Out-Null } catch {} }; `+
			`$src = '%s'; $dst = '%s'; $bak = $dst + '.old'; `+
			`for ($i = 0; $i -lt 30; $i++) { `+
			`  try { `+
			`    if (Test-Path $bak) { Remove-Item -Force $bak -ErrorAction SilentlyContinue }; `+
			`    if (Test-Path $dst) { Move-Item -Force $dst $bak }; `+
			`    Copy-Item -Force $src $dst; `+
			`    Remove-Item -Force $bak -ErrorAction SilentlyContinue; `+
			`    break `+
			`  } catch { `+
			`    if ((-not (Test-Path $dst)) -and (Test-Path $bak)) { try { Move-Item -Force $bak $dst -ErrorAction SilentlyContinue } catch {} }; `+
			`    Start-Sleep -Milliseconds 300 `+
			`  } `+
			`}; `+
			`if (Test-Path $dst) { Start-Process -FilePath $dst%s }`,
		pid,
		escPS(staged.FilePath),
		escPS(currentExe),
		argPart,
	)
}
