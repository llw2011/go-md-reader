package server

import (
	"go-md-reader/addons"
	"go-md-reader/assoc"
	"go-md-reader/parser"
	"go-md-reader/updater"
)

// DocumentResponse is the strongly-typed payload returned for active document reads.
type DocumentResponse struct {
	HasFile     bool             `json:"hasFile"`
	DocSeq      uint64           `json:"docSeq"`
	Filename    string           `json:"filename,omitempty"`
	Filepath    string           `json:"filepath,omitempty"`
	HTML        string           `json:"html,omitempty"`
	TOC         []parser.TOCItem `json:"toc,omitempty"`
	WordCount   int              `json:"wordCount,omitempty"`
	LineCount   int              `json:"lineCount,omitempty"`
	FileSize    int64            `json:"fileSize,omitempty"`
	ModTime     string           `json:"modTime,omitempty"`
	IsMemory    bool             `json:"isMemory,omitempty"`
	IsConverted bool             `json:"isConverted,omitempty"`
	FormatNote  string           `json:"formatNote,omitempty"`
	RawContent  string           `json:"rawContent,omitempty"`
}

// SiblingItem describes a supported document file in the active folder.
type SiblingItem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Ext       string `json:"ext"`
	Size      int64  `json:"size"`
	IsCurrent bool   `json:"isCurrent"`
}

// SiblingsResponse lists supported sibling files alongside the active directory.
type SiblingsResponse struct {
	Dir   string        `json:"dir"`
	Files []SiblingItem `json:"files"`
}

// ContentRequest passes raw Markdown text for preview or formatting.
type ContentRequest struct {
	Content string `json:"content"`
}

// PreviewResponse holds rendered HTML, TOC, and word count for live preview.
type PreviewResponse struct {
	HTML      string           `json:"html"`
	TOC       []parser.TOCItem `json:"toc"`
	WordCount int              `json:"wordCount"`
}

// FormatResponse holds formatted Markdown text and its refreshed HTML preview.
type FormatResponse struct {
	OK        bool             `json:"ok"`
	Content   string           `json:"content"`
	Changed   bool             `json:"changed"`
	HTML      string           `json:"html"`
	TOC       []parser.TOCItem `json:"toc"`
	WordCount int              `json:"wordCount"`
}

// OpenRequest unifies dialog, path, text-content, and binary document opening modes.
type OpenRequest struct {
	Mode       string `json:"mode"` // "dialog" | "path" | "content" | "binary"
	Path       string `json:"path,omitempty"`
	Name       string `json:"name,omitempty"`
	Content    string `json:"content,omitempty"`
	DataBase64 string `json:"dataBase64,omitempty"`
}

// OpenResponse reports the outcome of an open-document operation.
type OpenResponse struct {
	OK     bool   `json:"ok"`
	Opened bool   `json:"opened"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

// SaveRequest unifies save, save-as (.md), and save-html (.html) operations.
type SaveRequest struct {
	Mode     string `json:"mode"` // "save" | "saveAs" | "saveHtml"
	Content  string `json:"content,omitempty"`
	HTML     string `json:"html,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// SaveResponse reports the saved disk path or cancellation state.
type SaveResponse struct {
	Saved bool   `json:"saved"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

// UIPrefsUpdateResponse returns the persisted UI preferences after a patch.
type UIPrefsUpdateResponse struct {
	OK    bool    `json:"ok"`
	Prefs UIPrefs `json:"prefs"`
}

// TrayPrefsState gets or updates the minimize-to-tray preference.
type TrayPrefsState struct {
	OK             bool `json:"ok,omitempty"`
	MinimizeToTray bool `json:"minimizeToTray"`
}

// AssocApplyRequest updates Windows file associations and explorer context menu entry.
type AssocApplyRequest struct {
	Extensions  []string `json:"extensions"`
	ContextMenu bool     `json:"contextMenu"`
}

// NotepadSetRequest enables or restores Windows Notepad IFEO replacement.
type NotepadSetRequest struct {
	Replace bool `json:"replace"`
}

// NotepadStatusResponse returns the current Notepad IFEO status.
type NotepadStatusResponse struct {
	OK     bool                `json:"ok"`
	Status assoc.NotepadStatus `json:"status"`
}

// OTAConfigRequest updates OTA update configuration.
type OTAConfigRequest struct {
	IncludePrerelease bool `json:"includePrerelease"`
}

// OTAConfigSaveResponse returns the sanitized persisted OTA config view.
type OTAConfigSaveResponse struct {
	OK     bool               `json:"ok"`
	Config updater.ConfigView `json:"config"`
}

// OTACheckResponse wraps the latest release check output.
type OTACheckResponse struct {
	OK     bool                  `json:"ok"`
	Result updater.CheckResult   `json:"result"`
	Staged *updater.StagedUpdate `json:"staged,omitempty"`
}

// OTADownloadResponse wraps the staged update artifact metadata.
type OTADownloadResponse struct {
	OK     bool                 `json:"ok"`
	Staged updater.StagedUpdate `json:"staged"`
}

// OKResponse is a minimal typed acknowledgment payload.
type OKResponse struct {
	OK bool `json:"ok"`
}

// AddonActionRequest identifies a curated optional addon for install or removal.
type AddonActionRequest struct {
	ID string `json:"id"`
}

// AddonToggleRequest enables or disables an installed optional addon.
type AddonToggleRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// AddonsListResponse returns status for all curated optional addons.
type AddonsListResponse struct {
	OK     bool                 `json:"ok"`
	Addons []addons.AddonStatus `json:"addons"`
}

// AddonInstallResponse returns the installed addon and refreshed catalog state.
type AddonInstallResponse struct {
	OK     bool                 `json:"ok"`
	Addon  addons.AddonStatus   `json:"addon"`
	Addons []addons.AddonStatus `json:"addons"`
}
