package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type stageMeta struct {
	Version     string `json:"version"`
	AssetName   string `json:"assetName"`
	AssetID     int64  `json:"assetId"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	IsInstaller bool   `json:"isInstaller"`
}

func sanitizeStageVersion(version string) string {
	safeVer := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, version)
	if safeVer == "" {
		return "latest"
	}
	return safeVer
}

func computeFileDigest(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// FindStaged checks whether the requested release asset is already staged and verified on disk.
func (m *Manager) FindStaged(version string, asset *AssetInfo) *StagedUpdate {
	if asset == nil || m.stageRoot == "" {
		return nil
	}
	safeVer := sanitizeStageVersion(version)
	targetFile := filepath.Join(m.stageRoot, safeVer, filepath.Base(asset.Name))
	info, err := os.Stat(targetFile)
	if err != nil || info.IsDir() || info.Size() <= 0 {
		return nil
	}
	if asset.Size > 0 && info.Size() != asset.Size {
		return nil
	}

	actualSHA, actualSize, err := computeFileDigest(targetFile)
	if err != nil || actualSize <= 0 {
		return nil
	}

	if asset.SHA256 != "" {
		if !strings.EqualFold(actualSHA, asset.SHA256) {
			return nil
		}
		return &StagedUpdate{
			Version:        version,
			AssetName:      asset.Name,
			FilePath:       targetFile,
			VerifiedSHA256: true,
			SHA256:         actualSHA,
			IsInstaller:    asset.IsInstaller,
			ReusedCache:    true,
		}
	}

	rawMeta, err := os.ReadFile(targetFile + ".meta.json")
	if err != nil {
		return nil
	}
	var meta stageMeta
	if err := json.Unmarshal(rawMeta, &meta); err != nil {
		return nil
	}
	if meta.Version != version ||
		meta.AssetName != asset.Name ||
		(asset.ID != 0 && meta.AssetID != asset.ID) ||
		meta.Size != actualSize ||
		!strings.EqualFold(meta.SHA256, actualSHA) {
		return nil
	}

	return &StagedUpdate{
		Version:        version,
		AssetName:      asset.Name,
		FilePath:       targetFile,
		VerifiedSHA256: false,
		SHA256:         actualSHA,
		IsInstaller:    asset.IsInstaller,
		ReusedCache:    true,
	}
}

type progressWriter struct {
	total       int64
	downloaded  int64
	lastPercent int
	lastEmit    time.Time
	onChunk     func(downloaded, total int64, pct int)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.downloaded += int64(n)
	if pw.onChunk == nil {
		return n, nil
	}

	pct := -1
	if pw.total > 0 {
		pct = int((pw.downloaded * 100) / pw.total)
		if pct > 99 {
			pct = 99
		}
		if pct < 0 {
			pct = 0
		}
	}

	now := time.Now()
	if pct != pw.lastPercent || pw.lastEmit.IsZero() || now.Sub(pw.lastEmit) >= 100*time.Millisecond {
		pw.lastPercent = pct
		pw.lastEmit = now
		pw.onChunk(pw.downloaded, pw.total, pct)
	}
	return n, nil
}

// DownloadAndStage downloads (or idempotently reuses staged cache) with optional live progress callbacks,
// coalescing concurrent requests for the same version and asset.
func (m *Manager) DownloadAndStage(ctx context.Context, version string, asset *AssetInfo, onProgress ProgressFunc) (*StagedUpdate, error) {
	if asset == nil {
		return nil, fmt.Errorf("未找到可下载的 Windows 更新资产 (.exe)")
	}

	key := sanitizeStageVersion(version) + "/" + filepath.Base(asset.Name) + "/" + strings.ToLower(asset.SHA256)

	m.dlMu.Lock()
	if m.inflightDL == nil {
		m.inflightDL = make(map[string]*dlCall)
	}
	if call, ok := m.inflightDL[key]; ok {
		if onProgress != nil {
			call.subMu.Lock()
			call.subs = append(call.subs, onProgress)
			call.subMu.Unlock()
		}
		m.dlMu.Unlock()
		select {
		case <-call.done:
			if call.staged != nil {
				cp := *call.staged
				return &cp, call.err
			}
			return nil, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	call := &dlCall{done: make(chan struct{})}
	if onProgress != nil {
		call.subs = []ProgressFunc{onProgress}
	}
	m.inflightDL[key] = call
	m.dlMu.Unlock()

	emit := func(p DownloadProgress) {
		call.subMu.Lock()
		subs := append([]ProgressFunc(nil), call.subs...)
		call.subMu.Unlock()
		for _, fn := range subs {
			fn(p)
		}
	}

	go func() {
		call.staged, call.err = m.downloadAndStageOnce(context.WithoutCancel(ctx), version, asset, emit)
		m.dlMu.Lock()
		if cur, ok := m.inflightDL[key]; ok && cur == call {
			delete(m.inflightDL, key)
		}
		m.dlMu.Unlock()
		close(call.done)
	}()

	select {
	case <-call.done:
		if call.staged != nil {
			cp := *call.staged
			return &cp, call.err
		}
		return nil, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) downloadAndStageOnce(ctx context.Context, version string, asset *AssetInfo, emit ProgressFunc) (*StagedUpdate, error) {
	if cached := m.FindStaged(version, asset); cached != nil {
		var sz int64
		if st, err := os.Stat(cached.FilePath); err == nil {
			sz = st.Size()
		}
		emit(DownloadProgress{
			Version:         version,
			AssetName:       asset.Name,
			Phase:           "ready",
			BytesDownloaded: sz,
			TotalBytes:      sz,
			Percent:         100,
			ReusedCache:     true,
		})
		return cached, nil
	}

	cfg := m.loadConfig()

	dlURL := asset.APIURL
	accept := "application/octet-stream"
	if dlURL == "" {
		dlURL = asset.DownloadURL
	}
	if dlURL == "" {
		return nil, fmt.Errorf("Release 资产缺少下载地址")
	}

	dlClient := &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: m.httpClient.CheckRedirect,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, dlURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoMarkdownReader/"+CurrentVersion)
	req.Header.Set("Accept", accept)
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}

	resp, err := dlClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载更新包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("下载更新包返回 HTTP %d", resp.StatusCode)
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 && asset.Size > 0 {
		totalBytes = asset.Size
	}
	if totalBytes <= 0 {
		totalBytes = -1
	}

	safeVer := sanitizeStageVersion(version)
	stageDir := filepath.Join(m.stageRoot, safeVer)
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		return nil, err
	}
	targetFile := filepath.Join(stageDir, filepath.Base(asset.Name))
	partFile := targetFile + ".part"
	_ = os.Remove(partFile)

	out, err := os.OpenFile(partFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return nil, err
	}

	initPct := 0
	if totalBytes < 0 {
		initPct = -1
	}
	emit(DownloadProgress{
		Version:         version,
		AssetName:       asset.Name,
		Phase:           "downloading",
		BytesDownloaded: 0,
		TotalBytes:      totalBytes,
		Percent:         initPct,
	})

	pw := &progressWriter{
		total:       totalBytes,
		lastPercent: -2,
		onChunk: func(downloaded, total int64, pct int) {
			emit(DownloadProgress{
				Version:         version,
				AssetName:       asset.Name,
				Phase:           "downloading",
				BytesDownloaded: downloaded,
				TotalBytes:      total,
				Percent:         pct,
			})
		},
	}

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher, pw), io.LimitReader(resp.Body, 200<<20))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(partFile)
		emit(DownloadProgress{Version: version, AssetName: asset.Name, Phase: "error", BytesDownloaded: written, TotalBytes: totalBytes, Percent: initPct})
		return nil, fmt.Errorf("写入下载文件失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(partFile)
		emit(DownloadProgress{Version: version, AssetName: asset.Name, Phase: "error", BytesDownloaded: written, TotalBytes: totalBytes, Percent: initPct})
		return nil, closeErr
	}
	if written == 0 || (totalBytes > 0 && written != totalBytes) {
		_ = os.Remove(partFile)
		emit(DownloadProgress{Version: version, AssetName: asset.Name, Phase: "error", BytesDownloaded: written, TotalBytes: totalBytes, Percent: initPct})
		return nil, fmt.Errorf("下载文件不完整（已写入 %d 字节，期望 %d 字节）", written, totalBytes)
	}

	emit(DownloadProgress{
		Version:         version,
		AssetName:       asset.Name,
		Phase:           "verifying",
		BytesDownloaded: written,
		TotalBytes:      written,
		Percent:         100,
	})

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	verified := false
	if asset.SHA256 != "" {
		if !strings.EqualFold(actualSHA, asset.SHA256) {
			_ = os.Remove(partFile)
			emit(DownloadProgress{Version: version, AssetName: asset.Name, Phase: "error", BytesDownloaded: written, TotalBytes: written, Percent: 100})
			return nil, fmt.Errorf("SHA-256 校验失败（期望 %s，实际 %s），已中止安装", asset.SHA256, actualSHA)
		}
		verified = true
	}

	_ = os.Remove(targetFile)
	if err := os.Rename(partFile, targetFile); err != nil {
		_ = os.Remove(partFile)
		return nil, fmt.Errorf("落盘更新包失败: %w", err)
	}

	meta := stageMeta{
		Version:     version,
		AssetName:   asset.Name,
		AssetID:     asset.ID,
		Size:        written,
		SHA256:      actualSHA,
		IsInstaller: asset.IsInstaller,
	}
	if metaBytes, err := json.Marshal(meta); err == nil {
		_ = os.WriteFile(targetFile+".meta.json", metaBytes, 0600)
	}
	m.cleanupOldStageDirs(safeVer)

	emit(DownloadProgress{
		Version:         version,
		AssetName:       asset.Name,
		Phase:           "ready",
		BytesDownloaded: written,
		TotalBytes:      written,
		Percent:         100,
	})

	return &StagedUpdate{
		Version:        version,
		AssetName:      asset.Name,
		FilePath:       targetFile,
		VerifiedSHA256: verified,
		SHA256:         actualSHA,
		IsInstaller:    asset.IsInstaller,
	}, nil
}

func (m *Manager) cleanupOldStageDirs(keepVer string) {
	if m.stageRoot == "" {
		return
	}
	entries, err := os.ReadDir(m.stageRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != keepVer {
			_ = os.RemoveAll(filepath.Join(m.stageRoot, entry.Name()))
		}
	}
}

// CleanupOldBinary removes any leftover .old executable backup from a prior OTA replacement.
