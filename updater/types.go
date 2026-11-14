package updater

import (
	"net/http"
	"regexp"
	"sync"
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
