# Go Markdown Reader

A lightweight, offline-first Windows desktop reader for Markdown, code, and Office documents, built with Go and native WebView2.

## Download

Grab the latest build from [Releases](https://github.com/llw2011/go-md-reader/releases):

- **`GoMarkdownReader-Setup.exe`** — GUI installer/uninstaller (per-user, no admin required) with customizable file associations and Explorer context menu integration.
- **`go-md-reader.exe`** — Standalone portable executable with built-in OTA self-update.

## Features

- **Markdown & Code Rendering**: GFM tables, task lists, Chroma syntax highlighting, GitHub-style alerts (`NOTE`, `TIP`, `IMPORTANT`, `WARNING`, `CAUTION`), searchable outline (TOC), dark/light theme, and word/reading-time stats.
- **Document Conversion**: Opens `.docx`, `.xlsx`, `.pptx`, and `.csv` directly as structured Markdown; extracts readable text from legacy `.doc` / `.xls` files.
- **Drag & Drop + Live Reload**: Open files via Explorer drag-and-drop or file associations, with automatic live reload on disk changes.
- **Search, Export & Print**: In-page search (`Ctrl+F`), one-click self-contained HTML export, and print/PDF layout optimization (`Ctrl+P`).
- **System Tray & GitHub OTA**: Optional minimize-to-tray via native Win32 tray icon; manual GitHub Releases update check with SHA-256 verification.

## Shortcuts

| Shortcut | Action |
| :--- | :--- |
| `Ctrl + O` | Open file |
| `Ctrl + F` | Find in document (`Enter` / `Shift+Enter`, `Esc` to close) |
| `Ctrl + B` | Toggle table of contents |
| `Ctrl + P` | Print / Save as PDF |
| `Ctrl + +` / `Ctrl + -` / `Ctrl + 0` | Zoom in / out / reset |
| `F5` / `Ctrl + R` | Reload document |
| `Esc` | Close search, dialogs, or menus |

## Build from Source

Requires Windows and **Go 1.25+**:

```powershell
.\build.ps1
```

Outputs `go-md-reader.exe` and `GoMarkdownReader-Setup.exe` in the repository root.

Optional CLI file-association registration:

```powershell
.\register-default.ps1             # Register
.\register-default.ps1 -Unregister # Restore previous associations
```

## Project Structure

```text
go-md-reader/
├── main.go               # Entry point, WebView2 host, and tray wiring
├── server/               # Local HTTP/SSE server, token auth, file watcher, API
├── parser/               # Goldmark GFM + Chroma renderer and TOC extraction
├── converter/            # Office/table/text converters (.docx, .xlsx, .pptx, .csv)
├── dialog/               # Win32 native file/folder dialogs
├── assoc/                # Windows Registry file associations and shortcuts
├── tray/                 # Native Win32 Shell_NotifyIconW system tray
├── updater/              # GitHub Releases OTA checker and SHA-256 updater
├── cmd/
│   ├── genicon/          # App icon and Windows .syso generator
│   └── installer/        # GUI Setup / Uninstall wizard
├── assets/               # Offline frontend assets
├── build.ps1             # Build script for app and installer
└── register-default.ps1  # CLI file-association helper
```

## License

[MIT](LICENSE)
