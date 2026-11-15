package server

import (
	"context"
	"encoding/json"
	"fmt"

	"go-md-reader/assoc"
	"go-md-reader/updater"
)

func callNoArg[Resp any](fn func() (Resp, error)) (interface{}, error) {
	return fn()
}

func callTyped[Req any, Resp any](raw json.RawMessage, fn func(Req) (Resp, error)) (interface{}, error) {
	var req Req
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}
	return fn(req)
}

// HandleRPC dispatches typed application operations invoked from the WebView2 bridge or tests.
func (s *Server) HandleRPC(ctx context.Context, method string, rawParams json.RawMessage) (interface{}, error) {
	switch method {
	case "doc.get":
		return callNoArg(func() (DocumentResponse, error) { return s.GetFile(), nil })
	case "doc.siblings":
		return callNoArg(func() (SiblingsResponse, error) { return s.GetSiblings(), nil })
	case "doc.preview":
		return callTyped(rawParams, s.PreviewMarkdown)
	case "doc.format":
		return callTyped(rawParams, s.FormatMarkdown)
	case "doc.open":
		return callTyped(rawParams, s.OpenDocument)
	case "doc.save":
		return callTyped(rawParams, s.SaveDocument)
	case "prefs.get":
		return callNoArg(func() (UIPrefs, error) { return s.uiPrefs.get(), nil })
	case "prefs.update":
		return callTyped(rawParams, func(patch prefsPatch) (UIPrefsUpdateResponse, error) {
			return UIPrefsUpdateResponse{OK: true, Prefs: s.uiPrefs.applyPatch(patch)}, nil
		})
	case "tray.get":
		return callNoArg(func() (TrayPrefsState, error) { return s.GetTrayPrefs(), nil })
	case "tray.set":
		return callTyped(rawParams, func(req TrayPrefsState) (TrayPrefsState, error) {
			return s.SetTrayPrefs(req.MinimizeToTray), nil
		})
	case "assoc.get":
		return callNoArg(func() (assoc.Status, error) { return assoc.GetStatus(""), nil })
	case "assoc.apply":
		return callTyped(rawParams, s.ApplyAssoc)
	case "assoc.notepad.get":
		return callNoArg(func() (NotepadStatusResponse, error) {
			return NotepadStatusResponse{OK: true, Status: assoc.GetNotepadStatus()}, nil
		})
	case "assoc.notepad.set":
		return callTyped(rawParams, s.SetNotepadReplaced)
	case "ota.config.get":
		return callNoArg(func() (updater.ConfigView, error) { return s.updater.GetConfigView(), nil })
	case "ota.config.save":
		return callTyped(rawParams, s.SaveOTAConfig)
	case "ota.check":
		return callNoArg(func() (OTACheckResponse, error) { return s.CheckOTAUpdate(ctx) })
	case "ota.download":
		return callNoArg(func() (OTADownloadResponse, error) { return s.DownloadOTAUpdate(ctx) })
	case "ota.apply":
		return callNoArg(s.ApplyOTAUpdate)
	case "addons.list":
		return callNoArg(func() (AddonsListResponse, error) {
			return AddonsListResponse{OK: true, Addons: s.addons.List()}, nil
		})
	case "addons.install":
		return callTyped(rawParams, func(req AddonActionRequest) (AddonInstallResponse, error) {
			return s.InstallAddon(ctx, req)
		})
	case "addons.toggle":
		return callTyped(rawParams, s.ToggleAddon)
	case "addons.remove":
		return callTyped(rawParams, s.RemoveAddon)
	default:
		return nil, fmt.Errorf("未知操作: %s", method)
	}
}
