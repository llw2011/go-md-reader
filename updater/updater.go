package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// CurrentVersion is the running application version.
var CurrentVersion = "v2.3.1"

// DefaultRepo is the public GitHub repository used for OTA updates when unconfigured.
const DefaultRepo = "llw2011/go-md-reader"

var (
	apiBaseURL = "https://api.github.com"
	repoRegex  = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
)

// Config persists local GitHub OTA update settings.
type Config struct {
	Repo              string `json:"repo"`
	Token             string `json:"token,omitempty"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// ConfigView is returned to the UI without exposing the raw token.
type ConfigView struct {
	CurrentVersion    string `json:"currentVersion"`
	Repo              string `json:"repo"`
	HasToken          bool   `json:"hasToken"`
	TokenPreview      string `json:"tokenPreview"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// AssetInfo describes a downloadable Windows release asset.
type AssetInfo struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	APIURL      string `json:"apiUrl"`
	DownloadURL string `json:"downloadUrl"`
	SHA256      string `json:"sha256,omitempty"`
	IsInstaller bool   `json:"isInstaller"`
}

// CheckResult holds the result of checking GitHub Releases.
type CheckResult struct {
	CurrentVersion string     `json:"currentVersion"`
	LatestVersion  string     `json:"latestVersion"`
	HasUpdate      bool       `json:"hasUpdate"`
	ReleaseName    string     `json:"releaseName"`
	ReleaseNotes   string     `json:"releaseNotes"`
	HTMLURL        string     `json:"htmlUrl"`
	PublishedAt    string     `json:"publishedAt"`
	Asset          *AssetInfo `json:"asset,omitempty"`
}

// StagedUpdate represents a verified downloaded update binary ready to install.
type StagedUpdate struct {
	Version        string `json:"version"`
	AssetName      string `json:"assetName"`
	FilePath       string `json:"filePath"`
	VerifiedSHA256 bool   `json:"verifiedSha256"`
	SHA256         string `json:"sha256"`
	IsInstaller    bool   `json:"isInstaller"`
	ReusedCache    bool   `json:"reusedCache,omitempty"`
}

// DownloadProgress reports real-time byte and percentage progress during OTA staging.
type DownloadProgress struct {
	Version         string `json:"version"`
	AssetName       string `json:"assetName"`
	Phase           string `json:"phase"` // "downloading" | "verifying" | "ready" | "error"
	BytesDownloaded int64  `json:"bytesDownloaded"`
	TotalBytes      int64  `json:"totalBytes"`
	Percent         int    `json:"percent"` // 0..100, or -1 if total size is unknown
	ReusedCache     bool   `json:"reusedCache,omitempty"`
}

// ProgressFunc receives live OTA download progress notifications.
type ProgressFunc func(DownloadProgress)

type dlCall struct {
	done   chan struct{}
	staged *StagedUpdate
	err    error
	subMu  sync.Mutex
	subs   []ProgressFunc
}

type checkCall struct {
	done chan struct{}
	res  *CheckResult
	err  error
}

// Manager handles GitHub OTA configuration, checking, verification, and staging.
type Manager struct {
	mu          sync.RWMutex
	configPath  string
	stageRoot   string
	httpClient  *http.Client
	checkMu     sync.Mutex
	inflightChk *checkCall
	dlMu        sync.Mutex
	inflightDL  map[string]*dlCall
}

// NewManager creates an OTA manager backed by %LOCALAPPDATA%\GoMarkdownReader\ota.json.
func NewManager(configPath string) *Manager {
	if configPath == "" {
		localApp := os.Getenv("LOCALAPPDATA")
		if localApp == "" {
			localApp = os.TempDir()
		}
		configPath = filepath.Join(localApp, "GoMarkdownReader", "ota.json")
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return errors.New("too many redirects")
			}
			// Never leak GitHub PAT to external CDN/S3 hosts during asset download redirects
			if len(via) > 0 && req.URL.Host != via[0].URL.Host {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}

	return &Manager{
		configPath: configPath,
		stageRoot:  filepath.Join(os.TempDir(), "GoMD_OTA"),
		httpClient: client,
		inflightDL: make(map[string]*dlCall),
	}
}

func (m *Manager) loadConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var cfg Config
	data, err := os.ReadFile(m.configPath)
	if err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.Repo == "" {
		cfg.Repo = DefaultRepo
	}
	return cfg
}

// GetConfigView returns sanitized OTA settings safe for frontend display.
func (m *Manager) GetConfigView() ConfigView {
	cfg := m.loadConfig()
	return ConfigView{
		CurrentVersion:    CurrentVersion,
		Repo:              cfg.Repo,
		HasToken:          cfg.Token != "",
		TokenPreview:      maskToken(cfg.Token),
		IncludePrerelease: cfg.IncludePrerelease,
	}
}

// SaveConfig validates and stores GitHub OTA settings locally.
func (m *Manager) SaveConfig(repo, token string, clearToken, includePrerelease bool) (ConfigView, error) {
	normRepo, err := NormalizeRepo(repo)
	if err != nil {
		return ConfigView{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var cfg Config
	if data, rErr := os.ReadFile(m.configPath); rErr == nil {
		_ = json.Unmarshal(data, &cfg)
	}

	cfg.Repo = normRepo
	cfg.IncludePrerelease = includePrerelease
	if clearToken {
		cfg.Token = ""
	} else if t := strings.TrimSpace(token); t != "" {
		cfg.Token = t
	}

	if err := os.MkdirAll(filepath.Dir(m.configPath), 0755); err != nil {
		return ConfigView{}, err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return ConfigView{}, err
	}
	if err := os.WriteFile(m.configPath, out, 0600); err != nil {
		return ConfigView{}, err
	}

	return ConfigView{
		CurrentVersion:    CurrentVersion,
		Repo:              cfg.Repo,
		HasToken:          cfg.Token != "",
		TokenPreview:      maskToken(cfg.Token),
		IncludePrerelease: cfg.IncludePrerelease,
	}, nil
}

// NormalizeRepo converts "https://github.com/owner/repo(.git)" or "owner/repo" into strict "owner/repo".
func NormalizeRepo(input string) (string, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", nil
	}
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	if !repoRegex.MatchString(s) || strings.Contains(s, "..") {
		return "", fmt.Errorf("无效的 GitHub 仓库格式，请填写 owner/repo（例如 user/go-md-reader）")
	}
	return s, nil
}

func maskToken(tok string) string {
	if tok == "" {
		return ""
	}
	if len(tok) <= 8 {
		return "****"
	}
	return tok[:4] + "****" + tok[len(tok)-4:]
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt string    `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

// CheckUpdate queries GitHub Releases API for a newer version, coalescing concurrent calls.
func (m *Manager) CheckUpdate(ctx context.Context) (*CheckResult, error) {
	m.checkMu.Lock()
	if call := m.inflightChk; call != nil {
		m.checkMu.Unlock()
		select {
		case <-call.done:
			if call.res == nil {
				return nil, call.err
			}
			cp := *call.res
			if cp.Asset != nil {
				a := *cp.Asset
				cp.Asset = &a
			}
			return &cp, call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &checkCall{done: make(chan struct{})}
	m.inflightChk = call
	m.checkMu.Unlock()

	go func() {
		call.res, call.err = m.checkUpdateOnce(context.WithoutCancel(ctx))
		m.checkMu.Lock()
		if m.inflightChk == call {
			m.inflightChk = nil
		}
		m.checkMu.Unlock()
		close(call.done)
	}()

	select {
	case <-call.done:
		return call.res, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Manager) checkUpdateOnce(ctx context.Context) (*CheckResult, error) {
	cfg := m.loadConfig()
	if cfg.Repo == "" {
		return nil, fmt.Errorf("请先配置 GitHub 仓库（格式：owner/repo）")
	}

	var rel *ghRelease
	var err error
	if cfg.IncludePrerelease {
		rel, err = m.fetchLatestIncludingPrerelease(ctx, cfg)
	} else {
		rel, err = m.fetchLatestStable(ctx, cfg)
	}
	if err != nil {
		return nil, err
	}

	asset := selectWindowsAsset(rel.Assets)
	if asset != nil && asset.SHA256 == "" {
		asset.SHA256 = m.findChecksumInAssets(ctx, cfg, asset.Name, rel.Assets)
	}

	return &CheckResult{
		CurrentVersion: CurrentVersion,
		LatestVersion:  rel.TagName,
		HasUpdate:      CompareSemver(rel.TagName, CurrentVersion) > 0,
		ReleaseName:    rel.Name,
		ReleaseNotes:   rel.Body,
		HTMLURL:        rel.HTMLURL,
		PublishedAt:    rel.PublishedAt,
		Asset:          asset,
	}, nil
}

func (m *Manager) doGitHubGet(ctx context.Context, rawURL, token, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoMarkdownReader/"+CurrentVersion)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 失败: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("GitHub 鉴权失败 (401)：Token 无效或已过期")
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, fmt.Errorf("GitHub API 请求受限 (%d)：请配置有效的 GitHub Token 后重试", resp.StatusCode)
		case http.StatusNotFound:
			return nil, fmt.Errorf("未找到仓库或 Release (404)：如果是私有仓库请先填写有 Contents 只读权限的 Token")
		default:
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("GitHub API 返回异常 (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
		}
	}
	return resp, nil
}

func (m *Manager) fetchLatestStable(ctx context.Context, cfg Config) (*ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(apiBaseURL, "/"), cfg.Repo)
	resp, err := m.doGitHubGet(ctx, url, cfg.Token, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 Release 数据失败: %w", err)
	}
	return &rel, nil
}

func (m *Manager) fetchLatestIncludingPrerelease(ctx context.Context, cfg Config) (*ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=10", strings.TrimRight(apiBaseURL, "/"), cfg.Repo)
	resp, err := m.doGitHubGet(ctx, url, cfg.Token, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("解析 Release 列表失败: %w", err)
	}
	for i := range list {
		if !list[i].Draft {
			return &list[i], nil
		}
	}
	return nil, fmt.Errorf("该仓库暂无已发布的正式版或预发布版 Release")
}

func selectWindowsAsset(assets []ghAsset) *AssetInfo {
	var setupCandidate *ghAsset
	var exeCandidate *ghAsset

	for i := range assets {
		nameLower := strings.ToLower(assets[i].Name)
		if !strings.HasSuffix(nameLower, ".exe") {
			continue
		}
		if strings.Contains(nameLower, "uninstall") {
			continue
		}
		if strings.Contains(nameLower, "setup") {
			if setupCandidate == nil {
				setupCandidate = &assets[i]
			}
			continue
		}
		if exeCandidate == nil || nameLower == "go-md-reader.exe" {
			exeCandidate = &assets[i]
		}
	}

	chosen := exeCandidate
	isInstaller := false
	if chosen == nil {
		chosen = setupCandidate
		isInstaller = true
	}
	if chosen == nil {
		return nil
	}

	sha := ""
	if strings.HasPrefix(strings.ToLower(chosen.Digest), "sha256:") {
		sha = strings.ToLower(strings.TrimPrefix(strings.ToLower(chosen.Digest), "sha256:"))
	}

	return &AssetInfo{
		ID:          chosen.ID,
		Name:        chosen.Name,
		Size:        chosen.Size,
		APIURL:      chosen.URL,
		DownloadURL: chosen.BrowserDownloadURL,
		SHA256:      sha,
		IsInstaller: isInstaller,
	}
}

func (m *Manager) findChecksumInAssets(ctx context.Context, cfg Config, targetName string, assets []ghAsset) string {
	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if lower == strings.ToLower(targetName)+".sha256" || lower == "checksums.txt" || lower == "sha256sums.txt" {
			dlURL := a.URL
			if dlURL == "" {
				dlURL = a.BrowserDownloadURL
			}
			resp, err := m.doGitHubGet(ctx, dlURL, cfg.Token, "application/octet-stream")
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
			resp.Body.Close()
			if h := parseSHA256FromChecksumFile(string(body), targetName); h != "" {
				return h
			}
		}
	}
	return ""
}

func parseSHA256FromChecksumFile(content, targetName string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 {
			continue
		}
		candidateHex := strings.ToLower(fields[0])
		if len(candidateHex) != 64 {
			continue
		}
		if len(fields) == 1 && len(lines) <= 2 {
			return candidateHex
		}
		if len(fields) >= 2 {
			fname := strings.TrimPrefix(fields[len(fields)-1], "*")
			fname = filepath.Base(fname)
			if strings.EqualFold(fname, targetName) {
				return candidateHex
			}
		}
	}
	return ""
}

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

// DownloadAndStage downloads the selected asset to TEMP and verifies SHA-256 if available.
func (m *Manager) DownloadAndStage(ctx context.Context, version string, asset *AssetInfo) (*StagedUpdate, error) {
	return m.DownloadAndStageWithProgress(ctx, version, asset, nil)
}

// DownloadAndStageWithProgress downloads (or idempotently reuses staged cache) with live progress callbacks,
// coalescing concurrent requests for the same version and asset.
func (m *Manager) DownloadAndStageWithProgress(ctx context.Context, version string, asset *AssetInfo, onProgress ProgressFunc) (*StagedUpdate, error) {
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
	escPS := func(s string) string {
		return strings.ReplaceAll(s, `'`, `''`)
	}

	var script string
	if staged.IsInstaller {
		script = fmt.Sprintf(
			`$p = Get-Process -Id %d -ErrorAction SilentlyContinue; `+
				`if ($p) { try { $p.WaitForExit(15000) | Out-Null } catch {} }; `+
				`Start-Sleep -Milliseconds 300; `+
				`Start-Process -FilePath '%s'`,
			pid,
			escPS(staged.FilePath),
		)
	} else {
		if currentExe == "" {
			var err error
			currentExe, err = os.Executable()
			if err != nil {
				return err
			}
		}
		argPart := ""
		if openDocPath != "" {
			argPart = fmt.Sprintf(` -ArgumentList '%s'`, escPS(openDocPath))
		}
		script = fmt.Sprintf(
			`$p = Get-Process -Id %d -ErrorAction SilentlyContinue; `+
				`if ($p) { try { $p.WaitForExit(15000) | Out-Null } catch {} }; `+
				`$src = '%s'; $dst = '%s'; $bak = $dst + '.old'; $replaced = $false; `+
				`for ($i = 0; $i -lt 30; $i++) { `+
				`  try { `+
				`    if (Test-Path $bak) { Remove-Item -Force $bak -ErrorAction SilentlyContinue }; `+
				`    if (Test-Path $dst) { Move-Item -Force $dst $bak }; `+
				`    Copy-Item -Force $src $dst; `+
				`    Remove-Item -Force $bak -ErrorAction SilentlyContinue; `+
				`    $replaced = $true; break `+
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

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

// CompareSemver compares two semantic versions (e.g. "v2.0.1" vs "v2.0.0").
// Returns 1 if a > b, -1 if a < b, 0 if equal.
func CompareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa.nums[i] > pb.nums[i] {
			return 1
		}
		if pa.nums[i] < pb.nums[i] {
			return -1
		}
	}
	if pa.pre == "" && pb.pre != "" {
		return 1
	}
	if pa.pre != "" && pb.pre == "" {
		return -1
	}
	if pa.pre > pb.pre {
		return 1
	}
	if pa.pre < pb.pre {
		return -1
	}
	return 0
}

type semverParts struct {
	nums [3]int
	pre  string
}

func parseSemver(raw string) semverParts {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}
	pre := ""
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		pre = s[idx+1:]
		s = s[:idx]
	}
	parts := strings.Split(s, ".")
	var out semverParts
	out.pre = pre
	for i := 0; i < 3 && i < len(parts); i++ {
		n, _ := strconv.Atoi(parts[i])
		out.nums[i] = n
	}
	return out
}
