package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
