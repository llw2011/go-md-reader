package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go-md-reader/assoc"
	"go-md-reader/updater"
)

// GetTrayPrefs reads the tray minimize preference.
func (s *Server) GetTrayPrefs() TrayPrefsState {
	s.mu.RLock()
	fn := s.getMinToTray
	s.mu.RUnlock()
	minVal := true
	if fn != nil {
		minVal = fn()
	}
	return TrayPrefsState{OK: true, MinimizeToTray: minVal}
}

// SetTrayPrefs updates the tray minimize preference.
func (s *Server) SetTrayPrefs(minToTray bool) TrayPrefsState {
	s.mu.RLock()
	fn := s.setMinToTray
	s.mu.RUnlock()
	if fn != nil {
		fn(minToTray)
	}
	return TrayPrefsState{OK: true, MinimizeToTray: minToTray}
}

// ApplyAssoc updates Windows file associations and Explorer context menu integration.
func (s *Server) ApplyAssoc(req AssocApplyRequest) (OKResponse, error) {
	exePath, _ := os.Executable()
	if err := assoc.ApplyAssociations(exePath, req.Extensions, req.ContextMenu); err != nil {
		return OKResponse{}, err
	}
	return OKResponse{OK: true}, nil
}

// SetNotepadReplaced activates or restores Windows Notepad IFEO replacement.
func (s *Server) SetNotepadReplaced(req NotepadSetRequest) (NotepadStatusResponse, error) {
	if req.Replace {
		exePath, _ := os.Executable()
		if err := assoc.ReplaceNotepad(exePath); err != nil {
			return NotepadStatusResponse{}, err
		}
	} else {
		if err := assoc.RestoreNotepad(); err != nil {
			return NotepadStatusResponse{}, err
		}
	}
	return NotepadStatusResponse{OK: true, Status: assoc.GetNotepadStatus()}, nil
}

// SaveOTAConfig validates and persists OTA update preferences.
func (s *Server) SaveOTAConfig(req OTAConfigRequest) (OTAConfigSaveResponse, error) {
	view, err := s.updater.SaveConfig(req.Repo, req.Token, req.ClearToken, req.IncludePrerelease)
	if err != nil {
		return OTAConfigSaveResponse{}, err
	}
	return OTAConfigSaveResponse{OK: true, Config: view}, nil
}

// CheckOTAUpdate queries the configured release feed for newer versions.
func (s *Server) CheckOTAUpdate(ctx context.Context) (OTACheckResponse, error) {
	res, err := s.updater.CheckUpdate(ctx)
	if err != nil {
		return OTACheckResponse{}, err
	}

	var diskStaged *updater.StagedUpdate
	if res.HasUpdate && res.Asset != nil {
		diskStaged = s.updater.FindStaged(res.LatestVersion, res.Asset)
	}

	s.mu.Lock()
	s.lastCheckRes = res
	if diskStaged != nil {
		s.stagedUpdate = diskStaged
	} else if s.stagedUpdate != nil {
		sameVer := s.stagedUpdate.Version == res.LatestVersion
		sameAsset := res.Asset != nil && s.stagedUpdate.AssetName == res.Asset.Name
		if !res.HasUpdate || !sameVer || !sameAsset {
			s.stagedUpdate = nil
		} else if _, statErr := os.Stat(s.stagedUpdate.FilePath); statErr != nil {
			s.stagedUpdate = nil
		}
	}
	stagedCopy := s.stagedUpdate
	s.mu.Unlock()

	return OTACheckResponse{OK: true, Result: *res, Staged: stagedCopy}, nil
}

// DownloadOTAUpdate downloads and SHA-256-verifies the pending release asset, streaming real progress events.
func (s *Server) DownloadOTAUpdate(ctx context.Context) (OTADownloadResponse, error) {
	s.mu.RLock()
	checkRes := s.lastCheckRes
	s.mu.RUnlock()
	if checkRes == nil || checkRes.Asset == nil {
		return OTADownloadResponse{}, errors.New("请先检查更新并确认存在可下载的 Windows 资产")
	}

	staged, err := s.updater.DownloadAndStage(
		ctx,
		checkRes.LatestVersion,
		checkRes.Asset,
		func(p updater.DownloadProgress) {
			s.TriggerEvent("ota-progress", p)
		},
	)
	if err != nil {
		return OTADownloadResponse{}, err
	}

	s.mu.Lock()
	s.stagedUpdate = staged
	s.mu.Unlock()
	return OTADownloadResponse{OK: true, Staged: *staged}, nil
}

// ApplyOTAUpdate starts the staged installer and triggers graceful app shutdown idempotently.
func (s *Server) ApplyOTAUpdate() (OKResponse, error) {
	s.mu.Lock()
	if s.applyStarted {
		s.mu.Unlock()
		return OKResponse{OK: true}, nil
	}
	staged := s.stagedUpdate
	if staged == nil && s.lastCheckRes != nil && s.lastCheckRes.Asset != nil {
		staged = s.updater.FindStaged(s.lastCheckRes.LatestVersion, s.lastCheckRes.Asset)
		if staged != nil {
			s.stagedUpdate = staged
		}
	}
	if staged == nil {
		s.mu.Unlock()
		return OKResponse{}, errors.New("尚无已就绪的更新包，请先点击下载")
	}

	curPath := ""
	if !s.isMemoryOnly {
		curPath = s.currentFilePath
	}
	onQuit := s.onQuit
	s.applyStarted = true
	s.mu.Unlock()

	if err := updater.ApplyAndRestart(*staged, "", curPath); err != nil {
		s.mu.Lock()
		s.applyStarted = false
		s.mu.Unlock()
		return OKResponse{}, fmt.Errorf("启动更新安装程序失败: %w", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		if onQuit != nil {
			onQuit()
		} else {
			os.Exit(0)
		}
	}()

	return OKResponse{OK: true}, nil
}
