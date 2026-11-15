package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

// FindStaged checks whether the requested release asset is already staged and SHA-256 verified on disk.
func (m *Manager) FindStaged(version string, asset *AssetInfo) *StagedUpdate {
	if asset == nil || asset.SHA256 == "" || m.stageRoot == "" {
		return nil
	}
	targetFile := filepath.Join(m.stageRoot, sanitizeStageVersion(version), filepath.Base(asset.Name))
	info, err := os.Stat(targetFile)
	if err != nil || info.IsDir() || info.Size() <= 0 || (asset.Size > 0 && info.Size() != asset.Size) {
		return nil
	}
	actualSHA, actualSize, err := computeFileDigest(targetFile)
	if err != nil || actualSize <= 0 || !strings.EqualFold(actualSHA, asset.SHA256) {
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
		pct = min(99, max(0, int((pw.downloaded*100)/pw.total)))
	}
	now := time.Now()
	if pct != pw.lastPercent || pw.lastEmit.IsZero() || now.Sub(pw.lastEmit) >= 100*time.Millisecond {
		pw.lastPercent = pct
		pw.lastEmit = now
		pw.onChunk(pw.downloaded, pw.total, pct)
	}
	return n, nil
}

// DownloadAndStage downloads (or reuses SHA-256 verified cache) with live progress callbacks,
// coalescing concurrent requests for the same version and asset.
func (m *Manager) DownloadAndStage(ctx context.Context, version string, asset *AssetInfo, onProgress ProgressFunc) (*StagedUpdate, error) {
	if asset == nil {
		return nil, fmt.Errorf("未找到可下载的 Windows 更新资产 (.exe)")
	}
	key := sanitizeStageVersion(version) + "/" + filepath.Base(asset.Name) + "/" + strings.ToLower(asset.SHA256)
	staged, err := runCoalesced(&m.flightMu, m.dlFlights, key, ctx, onProgress, func(emit func(DownloadProgress)) (*StagedUpdate, error) {
		return m.downloadAndStageOnce(context.WithoutCancel(ctx), version, asset, emit)
	})
	if staged == nil {
		return nil, err
	}
	cp := *staged
	return &cp, err
}

func (m *Manager) downloadAndStageOnce(ctx context.Context, version string, asset *AssetInfo, emit func(DownloadProgress)) (*StagedUpdate, error) {
	report := func(phase string, down, total int64, pct int, reused bool) {
		emit(DownloadProgress{
			Version:         version,
			AssetName:       asset.Name,
			Phase:           phase,
			BytesDownloaded: down,
			TotalBytes:      total,
			Percent:         pct,
			ReusedCache:     reused,
		})
	}

	if cached := m.FindStaged(version, asset); cached != nil {
		var sz int64
		if st, err := os.Stat(cached.FilePath); err == nil {
			sz = st.Size()
		}
		report("ready", sz, sz, 100, true)
		return cached, nil
	}

	if asset.DownloadURL == "" {
		return nil, fmt.Errorf("Release 资产缺少下载地址")
	}

	dlClient := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoMarkdownReader/"+CurrentVersion)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := dlClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载更新包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("下载更新包返回 HTTP %d", resp.StatusCode)
	}

	totalBytes := resp.ContentLength
	if totalBytes <= 0 {
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
	report("downloading", 0, totalBytes, initPct, false)

	pw := &progressWriter{
		total:       totalBytes,
		lastPercent: -2,
		onChunk: func(down, total int64, pct int) {
			report("downloading", down, total, pct, false)
		},
	}

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(out, hasher, pw), io.LimitReader(resp.Body, 200<<20))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || written == 0 || (totalBytes > 0 && written != totalBytes) {
		_ = os.Remove(partFile)
		report("error", written, totalBytes, initPct, false)
		if copyErr != nil {
			return nil, fmt.Errorf("写入下载文件失败: %w", copyErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return nil, fmt.Errorf("下载文件不完整（已写入 %d 字节，期望 %d 字节）", written, totalBytes)
	}

	report("verifying", written, written, 100, false)

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	verified := false
	if asset.SHA256 != "" {
		if !strings.EqualFold(actualSHA, asset.SHA256) {
			_ = os.Remove(partFile)
			report("error", written, written, 100, false)
			return nil, fmt.Errorf("SHA-256 校验失败（期望 %s，实际 %s），已中止安装", asset.SHA256, actualSHA)
		}
		verified = true
	}

	_ = os.Remove(targetFile)
	if err := os.Rename(partFile, targetFile); err != nil {
		_ = os.Remove(partFile)
		return nil, fmt.Errorf("落盘更新包失败: %w", err)
	}

	m.cleanupOldStageDirs(safeVer)
	report("ready", written, written, 100, false)

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
