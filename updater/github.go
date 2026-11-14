package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

type ghRelease struct {
	TagName    string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Assets     []ghAsset `json:"assets"`
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
