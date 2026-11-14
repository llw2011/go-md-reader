# Go Markdown Reader (自研纯净 Markdown 文本阅读器)

基于 **Go 语言 + Windows 原生 WebView2** 构建的完全本地化、纯净可信、高扩展性的独立 Markdown 文本阅读工具。

## ✨ 补强与特性全览

### 1. 智能文档转换引擎 (Word / Excel / CSV 零乱码即读)
- **Word 文档自动转换 (`.docx`)**：直接打开或拖入 Word 文档，自动提取其段落、多级标题、粗体斜体、列表与表格，无缝转为结构化 Markdown 呈现。
- **Excel 表格自动转换 (`.xlsx`)**：智能解析各 Sheet 工作表与单元格数据，自动转换为清晰的 Markdown 表格，告别 ZIP 乱码。
- **CSV 表格支持 (`.csv`)**：自动将逗号分隔符数据转为带表头的表格视图。
- **旧版格式防护 (`.doc` / `.xls`)**：针对旧版二进制 Office 文件，自动过滤乱码并提取有效纯文本。

### 2. 拖放即读 (Drag & Drop)
- 支持从 Windows 资源管理器直接将任何 `.md` / `.markdown` / `.txt` 文件拖拽至窗口。
- **双通道自适应**：自动优先识别宿主文件真实路径（挂载实时文件变动监听）；若在沙箱或受限环境中，自动无缝降级为浏览器流式读取内容，确保 100% 拖拽成功率。

### 2. 深度优化的打印与 PDF 另存为 (`Ctrl + P`)
- **A4 纸张排版美化**：专为打印与 PDF 导出打造的 `@media print` 样式。
- **防生硬断页**：标题与后续正文自动保持关联（`break-after: avoid`）；代码块、表格、引用块与 Callout 自动避免跨页折断（`break-inside: avoid`）。
- **打印页眉信息**：纸张首部自动输出文档标题、源路径与打印时间戳。
- **代码打印防截断**：打印状态下代码块强制启用自动换行，防止宽行代码在纸张右边缘被裁剪。
- **一键导出独立 HTML**：点击工具栏“📤 导出”，即可生成单文件离线 HTML（内置所有 CSS 与样式），任何人双击浏览器即可查阅，方便分享。

### 3. 页面内即时全文检索 (`Ctrl + F`)
- 支持页面内关键字检索与黄色高亮匹配。
- 实时统计匹配数量（例如 `2 / 8`）。
- 支持 `Enter`（下一个）、`Shift + Enter`（上一个）、`Esc`（关闭搜索并清除高亮）。
- 聚焦的当前匹配项自动平滑居中显示。

### 4. GitHub 风格 Alert / Callout 提示框
- 原生支持 GitHub 风格的标注语法：
  - `> [!NOTE]`（说明提示）
  - `> [!TIP]`（操作建议）
  - `> [!IMPORTANT]`（重要通知）
  - `> [!WARNING]`（预警信息）
  - `> [!CAUTION]`（危险告警）
- 自动渲染对应的配色、边框与 SVG 提示图标。

### 5. 代码块增强体验
- **语言标识徽章**：代码块右下角显示对应编程语言（如 `GO`、`PYTHON`、`SQL`、`JSON` 等）。
- **折行切换**：右上角提供“折行 / 单行”切换按钮，自由掌控长代码展示。
- **一键复制代码**：点击复制并伴随动态成功反馈。

### 6. 最近打开历史 (Recent Files)
- 工具栏“🕒 最近”下拉菜单自动记录最近查看的 10 个文档。
- 点击任意历史记录可瞬间切换。

### 7. 多维统计属性弹窗
- 点击工具栏字数徽章，即可弹出文档详细属性统计：
  - 总字数 / 字符数
  - 预计阅读时长
  - 总行数
  - 文件体积
  - 章节标题数
  - 最后修改时间与本地物理路径

### 8. 体验微细节
- **回到顶部**：滚动离开首屏后自动浮现快捷按钮 `↑`。
- **智能目录大纲 (TOC)**：支持按关键字实时过滤、滚动段落实时跟踪高亮。
- **明暗主题**：自适应系统夜间模式，支持一键手动切换。

### 9. Windows 系统托盘与 GitHub OTA 在线更新 (`v2.0.0`)
- **原生系统托盘常驻**：支持右键菜单快速显示窗口、打开文档、检查更新、切换“关闭窗口时最小化到托盘”以及彻底退出；点击右上角关闭按钮默认隐藏到托盘，瞬间唤醒。
- **可控 GitHub OTA 更新**：点击工具栏 `☁️ 更新`（默认指向本开源仓库 `llw2011/go-md-reader`），仅在用户主动触发时检查 GitHub Releases；支持 SHA-256 哈希自动校验、便携 EXE 原子自替换升级或启动新版安装包。
- **图形化绿色安装器**：提供 `GoMarkdownReader-Setup.exe`（免管理员提权），自主选择要关联的 `.md` / `.txt` 等扩展名与系统右键菜单，支持卸载时自动清理与备份还原。

---

## ⌨️ 完整快捷键清单

| 快捷键 | 功能 |
| :--- | :--- |
| `Ctrl + O` | 打开本地文件选择框 |
| `Ctrl + F` | 开启全文检索（`Enter` 下一个，`Shift+Enter` 上一个，`Esc` 退出） |
| `Ctrl + B` | 展开 / 折叠左侧目录大纲 |
| `Ctrl + P` | 打印当前文档或保存为 PDF |
| `Ctrl + +` / `Ctrl + -` | 放大 / 缩小阅读字号 |
| `Ctrl + 0` | 恢复默认阅读字号 |
| `F5` / `Ctrl + R` | 重新加载当前文档 |
| `Esc` | 关闭搜索框 / 统计弹窗 / 下拉菜单 |

---

## 🛠️ 项目结构

```
go-md-reader/
├── main.go               # 主入口：启动本地鉴权服务、WebView2 窗口与系统托盘
├── server/
│   └── server.go         # 本地 HTTP + SSE 服务、Token 鉴权、文件监听、OTA/关联 API
├── parser/
│   └── markdown.go       # Goldmark GFM 引擎、Chroma 语法高亮、TOC 与媒体路径解析
├── converter/            # Office/表格/代码转换引擎 (.docx / .xlsx / .pptx / .csv)
├── dialog/               # Windows 原生打开/保存文件与目录选择对话框 Win32 封装
├── assoc/                # Windows 注册表文件格式关联、备份还原与快捷方式管理
├── tray/                 # Windows 原生 Shell_NotifyIconW 系统托盘与最小化拦截
├── updater/              # GitHub Releases OTA 检查、SHA-256 校验与自更新引擎
├── cmd/
│   ├── genicon/          # 自动生成高分 ICO 与 Windows .syso 资源文件
│   └── installer/        # 独立图形化安装 / 卸载向导源码
├── assets/               # 纯离线前端资源 (无任何外部 CDN 引用)
├── build.ps1             # 一键编译主程序 + 安装包打包脚本
├── register-default.ps1  # 命令行关联 / 解除 Windows 默认打开方式
└── sample.md             # 演示样例文件
```

## 🚀 下载安装与源码编译

### 直接使用（推荐）
前往 [Releases](https://github.com/llw2011/go-md-reader/releases) 下载：
- `GoMarkdownReader-Setup.exe`：图形化安装与卸载向导（按需勾选要关联的文件格式）
- `go-md-reader.exe`：单文件便携免安装版（支持内置 OTA 自动升级）

### 从源码编译（Windows, Go 1.25+）
```powershell
.\build.ps1
```
构建完成后会在当前目录生成 `go-md-reader.exe` 与 `GoMarkdownReader-Setup.exe`。

### 命令行关联默认程序（可选）
```powershell
.\register-default.ps1
```
*(若日后需要解除关联，执行：`.\register-default.ps1 -Unregister`)*

## 📄 开源协议
本项目基于 [MIT License](LICENSE) 开源。
