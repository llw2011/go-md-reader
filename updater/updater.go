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
var CurrentVersion = "v2.1.1"

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
}

// Manager handles GitHub OTA configuration, checking, verification, and staging.
type Manager struct {
	mu         sync.RWMutex
	configPath string
	httpClient *http.Client
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
		httpClient: client,
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

// CheckUpdate queries GitHub Releases API for a newer version.
func (m *Manager) CheckUpdate(ctx context.Context) (*CheckResult, error) {
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

// DownloadAndStage downloads the selected asset to TEMP and verifies SHA-256 if available.
func (m *Manager) DownloadAndStage(ctx context.Context, version string, asset *AssetInfo) (*StagedUpdate, error) {
	if asset == nil {
		return nil, fmt.Errorf("未找到可下载的 Windows 更新资产 (.exe)")
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

	safeVer := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, version)
	if safeVer == "" {
		safeVer = "latest"
	}

	stageDir := filepath.Join(os.TempDir(), "GoMD_OTA", safeVer)
	if err := os.MkdirAll(stageDir, 0755); err != nil {
		return nil, err
	}
	targetFile := filepath.Join(stageDir, filepath.Base(asset.Name))
	out, err := os.OpenFile(targetFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return nil, err
	}

	hasher := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(out, hasher), io.LimitReader(resp.Body, 200<<20))
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(targetFile)
		return nil, fmt.Errorf("写入下载文件失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(targetFile)
		return nil, closeErr
	}

	actualSHA := hex.EncodeToString(hasher.Sum(nil))
	verified := false
	if asset.SHA256 != "" {
		if !strings.EqualFold(actualSHA, asset.SHA256) {
			_ = os.Remove(targetFile)
			return nil, fmt.Errorf("SHA-256 校验失败（期望 %s，实际 %s），已中止安装", asset.SHA256, actualSHA)
		}
		verified = true
	}

	return &StagedUpdate{
		Version:        version,
		AssetName:      asset.Name,
		FilePath:       targetFile,
		VerifiedSHA256: verified,
		SHA256:         actualSHA,
		IsInstaller:    asset.IsInstaller,
	}, nil
}

// ApplyAndRestart schedules replacement/installer execution after current PID exits.
func ApplyAndRestart(staged StagedUpdate, currentExe, openDocPath string) error {
	if staged.FilePath == "" {
		return fmt.Errorf("更新文件路径为空")
	}
	if _, err := os.Stat(staged.FilePath); err != nil {
		return fmt.Errorf("未找到已下载的更新文件: %w", err)
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
				`$src = '%s'; $dst = '%s'; $bak = $dst + '.old'; `+
				`for ($i = 0; $i -lt 25; $i++) { `+
				`  try { `+
				`    if (Test-Path $bak) { Remove-Item -Force $bak -ErrorAction SilentlyContinue }; `+
				`    if (Test-Path $dst) { Move-Item -Force $dst $bak }; `+
				`    Copy-Item -Force $src $dst; `+
				`    Remove-Item -Force $bak -ErrorAction SilentlyContinue; `+
				`    break `+
				`  } catch { Start-Sleep -Milliseconds 300 } `+
				`}; `+
				`Start-Process -FilePath $dst%s`,
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
