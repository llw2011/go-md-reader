package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CurrentVersion is the running application version (overridden at release build via -ldflags).
var CurrentVersion = "v2.4.2"

// DefaultRepo is the public GitHub repository queried for OTA releases.
const DefaultRepo = "llw2011/go-md-reader"

var apiBaseURL = "https://api.github.com"

// Config persists user OTA preferences locally.
type Config struct {
	Repo              string `json:"repo,omitempty"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// ConfigView exposes safe OTA status and preferences to the frontend.
type ConfigView struct {
	CurrentVersion    string `json:"currentVersion"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// AssetInfo describes a downloadable Windows release asset.
type AssetInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"downloadUrl"`
	SHA256      string `json:"sha256,omitempty"`
	IsInstaller bool   `json:"isInstaller"`
}

// CheckResult holds the result of querying GitHub Releases.
type CheckResult struct {
	CurrentVersion string     `json:"currentVersion"`
	LatestVersion  string     `json:"latestVersion"`
	HasUpdate      bool       `json:"hasUpdate"`
	ReleaseName    string     `json:"releaseName"`
	ReleaseNotes   string     `json:"releaseNotes"`
	Asset          *AssetInfo `json:"asset,omitempty"`
}

// StagedUpdate describes a locally verified update binary ready to install.
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
	Percent         int    `json:"percent"`
	ReusedCache     bool   `json:"reusedCache,omitempty"`
}

// ProgressFunc receives live OTA download progress notifications.
type ProgressFunc func(DownloadProgress)

type flight[T any, E any] struct {
	done chan struct{}
	val  T
	err  error
	mu   sync.Mutex
	subs []func(E)
}

func runCoalesced[T any, E any](
	mu *sync.Mutex,
	inflight map[string]*flight[T, E],
	key string,
	ctx context.Context,
	onEvent func(E),
	work func(emit func(E)) (T, error),
) (T, error) {
	mu.Lock()
	if call, ok := inflight[key]; ok {
		if onEvent != nil {
			call.mu.Lock()
			call.subs = append(call.subs, onEvent)
			call.mu.Unlock()
		}
		mu.Unlock()
		select {
		case <-call.done:
			return call.val, call.err
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}

	call := &flight[T, E]{done: make(chan struct{})}
	if onEvent != nil {
		call.subs = []func(E){onEvent}
	}
	inflight[key] = call
	mu.Unlock()

	emit := func(ev E) {
		call.mu.Lock()
		subs := append(([]func(E))(nil), call.subs...)
		call.mu.Unlock()
		for _, fn := range subs {
			fn(ev)
		}
	}

	go func() {
		call.val, call.err = work(emit)
		mu.Lock()
		if inflight[key] == call {
			delete(inflight, key)
		}
		mu.Unlock()
		close(call.done)
	}()

	select {
	case <-call.done:
		return call.val, call.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

// Manager coordinates OTA configuration, GitHub release checks, staging, and installation.
type Manager struct {
	mu           sync.RWMutex
	configPath   string
	stageRoot    string
	httpClient   *http.Client
	flightMu     sync.Mutex
	checkFlights map[string]*flight[*CheckResult, struct{}]
	dlFlights    map[string]*flight[*StagedUpdate, DownloadProgress]
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
	return &Manager{
		configPath:   configPath,
		stageRoot:    filepath.Join(os.TempDir(), "GoMD_OTA"),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		checkFlights: make(map[string]*flight[*CheckResult, struct{}]),
		dlFlights:    make(map[string]*flight[*StagedUpdate, DownloadProgress]),
	}
}

func (m *Manager) loadConfig() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var cfg Config
	if data, err := os.ReadFile(m.configPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if strings.TrimSpace(cfg.Repo) == "" {
		cfg.Repo = DefaultRepo
	}
	return cfg
}

// GetConfigView returns sanitized OTA settings for UI display.
func (m *Manager) GetConfigView() ConfigView {
	cfg := m.loadConfig()
	return ConfigView{CurrentVersion: CurrentVersion, IncludePrerelease: cfg.IncludePrerelease}
}

// SaveConfig persists OTA preferences locally.
func (m *Manager) SaveConfig(includePrerelease bool) (ConfigView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cfg Config
	if data, err := os.ReadFile(m.configPath); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	cfg.IncludePrerelease = includePrerelease

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
	return ConfigView{CurrentVersion: CurrentVersion, IncludePrerelease: cfg.IncludePrerelease}, nil
}

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

// CheckUpdate queries GitHub Releases API for a newer version, coalescing concurrent calls.
func (m *Manager) CheckUpdate(ctx context.Context) (*CheckResult, error) {
	cfg := m.loadConfig()
	key := fmt.Sprintf("%s|pre=%v", cfg.Repo, cfg.IncludePrerelease)
	res, err := runCoalesced(&m.flightMu, m.checkFlights, key, ctx, nil, func(func(struct{})) (*CheckResult, error) {
		return m.checkUpdateOnce(context.WithoutCancel(ctx), cfg)
	})
	if res == nil {
		return nil, err
	}
	cp := *res
	if cp.Asset != nil {
		a := *cp.Asset
		cp.Asset = &a
	}
	return &cp, err
}

func (m *Manager) checkUpdateOnce(ctx context.Context, cfg Config) (*CheckResult, error) {
	rel, err := m.fetchTargetRelease(ctx, cfg)
	if err != nil {
		return nil, err
	}
	asset := selectWindowsAsset(rel.Assets)
	if asset != nil && asset.SHA256 == "" {
		asset.SHA256 = m.findChecksumInAssets(ctx, asset.Name, rel.Assets)
	}
	return &CheckResult{
		CurrentVersion: CurrentVersion,
		LatestVersion:  rel.TagName,
		HasUpdate:      CompareSemver(rel.TagName, CurrentVersion) > 0,
		ReleaseName:    rel.Name,
		ReleaseNotes:   rel.Body,
		Asset:          asset,
	}, nil
}

func (m *Manager) doGitHubGet(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoMarkdownReader/"+CurrentVersion)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接 GitHub 失败: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusForbidden, http.StatusTooManyRequests:
			return nil, fmt.Errorf("GitHub API 请求受限 (%d)，请稍后再试", resp.StatusCode)
		case http.StatusNotFound:
			return nil, fmt.Errorf("未找到发布信息 (404)")
		default:
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return nil, fmt.Errorf("GitHub API 返回异常 (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
		}
	}
	return resp, nil
}

func (m *Manager) fetchTargetRelease(ctx context.Context, cfg Config) (*ghRelease, error) {
	base := strings.TrimRight(apiBaseURL, "/")
	if !cfg.IncludePrerelease {
		resp, err := m.doGitHubGet(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", base, cfg.Repo), "application/vnd.github+json")
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

	resp, err := m.doGitHubGet(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=10", base, cfg.Repo), "application/vnd.github+json")
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
	return nil, fmt.Errorf("暂无已发布的正式版或预发布版 Release")
}

func assetDownloadURL(a ghAsset) string {
	if a.BrowserDownloadURL != "" {
		return a.BrowserDownloadURL
	}
	return a.URL
}

func selectWindowsAsset(assets []ghAsset) *AssetInfo {
	var exeCandidate, setupCandidate *ghAsset
	for i := range assets {
		lower := strings.ToLower(assets[i].Name)
		if !strings.HasSuffix(lower, ".exe") || strings.Contains(lower, "uninstall") {
			continue
		}
		if strings.Contains(lower, "setup") {
			if setupCandidate == nil {
				setupCandidate = &assets[i]
			}
		} else if exeCandidate == nil || lower == "go-md-reader.exe" {
			exeCandidate = &assets[i]
		}
	}

	chosen, isInstaller := exeCandidate, false
	if chosen == nil {
		chosen, isInstaller = setupCandidate, true
	}
	if chosen == nil {
		return nil
	}
	sha := ""
	if strings.HasPrefix(strings.ToLower(chosen.Digest), "sha256:") {
		sha = strings.ToLower(chosen.Digest[len("sha256:"):])
	}
	return &AssetInfo{
		Name:        chosen.Name,
		Size:        chosen.Size,
		DownloadURL: assetDownloadURL(*chosen),
		SHA256:      sha,
		IsInstaller: isInstaller,
	}
}

func (m *Manager) findChecksumInAssets(ctx context.Context, targetName string, assets []ghAsset) string {
	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if lower != strings.ToLower(targetName)+".sha256" && lower != "checksums.txt" && lower != "sha256sums.txt" {
			continue
		}
		resp, err := m.doGitHubGet(ctx, assetDownloadURL(a), "application/octet-stream")
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if h := parseSHA256FromChecksumFile(string(body), targetName); h != "" {
			return h
		}
	}
	return ""
}

func parseSHA256FromChecksumFile(content, targetName string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 0 || len(fields[0]) != 64 {
			continue
		}
		hexSum := strings.ToLower(fields[0])
		if len(fields) == 1 && len(lines) <= 2 {
			return hexSum
		}
		if len(fields) >= 2 && strings.EqualFold(filepath.Base(strings.TrimPrefix(fields[len(fields)-1], "*")), targetName) {
			return hexSum
		}
	}
	return ""
}

// CompareSemver compares two semantic version strings (e.g. "v2.3.1" vs "2.3.0").
func CompareSemver(a, b string) int {
	pa, pb := parseSemver(a), parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa.nums[i] != pb.nums[i] {
			if pa.nums[i] > pb.nums[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	case pa.pre > pb.pre:
		return 1
	default:
		return -1
	}
}

type semverParts struct {
	nums [3]int
	pre  string
}

func parseSemver(raw string) semverParts {
	s := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(raw), "v"), "V")
	if idx := strings.IndexByte(s, '+'); idx >= 0 {
		s = s[:idx]
	}
	var out semverParts
	if idx := strings.IndexByte(s, '-'); idx >= 0 {
		out.pre = s[idx+1:]
		s = s[:idx]
	}
	for i, part := range strings.Split(s, ".") {
		if i >= 3 {
			break
		}
		out.nums[i], _ = strconv.Atoi(part)
	}
	return out
}
