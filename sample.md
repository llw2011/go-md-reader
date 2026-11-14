# Go Markdown Reader 终极增强演示

> 这是一款使用 **Go 语言 + Windows 原生 WebView2** 构建的完全本地、纯净安全的 Markdown 阅读器。零外联、零遥测、毫秒级响应。

---

## 🌟 核心新功能亮点

### 1. 拖放即读 (Drag & Drop)
支持从 Windows 资源管理器直接将任何 `.md` 文件**拖入本窗口**，支持路径模式与浏览器内存流双通道自动切换，松开鼠标立即加载。

### 2. 完美的打印与 PDF 导出 (`Ctrl + P`)
针对 A4 纸张排版进行深度优化：
- 自动隐藏工具栏、侧边栏、按钮等 UI 杂质。
- 智能防断页：标题绝不孤立在页尾，代码块、表格、引用块绝不跨页生硬截断。
- 打印页眉自动附带文档名、源路径与打印时间戳。
- 支持一键导出为**独立离线 HTML 文件**（内置所有样式，任何人任何浏览器双击即看）。

### 3. 页面内即时搜索 (`Ctrl + F`)
支持全文关键词快速检索、黄色高亮匹配、`Enter` / `Shift + Enter` 上下切换、当前项橙色聚焦。

### 4. GitHub 风格 Alert / Callout 提示框

> [!NOTE]
> 这是一个 **NOTE 提示框**，适合放置普通背景说明或技术细节。

> [!TIP]
> 这是一个 **TIP 技巧框**：按 `Ctrl + B` 可以快速隐藏或展开左侧大纲目录，获得全屏沉浸式阅读体验！

> [!IMPORTANT]
> 这是一个 **IMPORTANT 重要通知**：本软件不包含任何后台进程与联网代码，完全绿色安全。

> [!WARNING]
> 这是一个 **WARNING 警告框**：某些商业阅读器会自动抢占 `.md` 打开方式并上传云端，请警惕此类行为。

> [!CAUTION]
> 这是一个 **CAUTION 危险警告**：请勿随意安装来路不明的带有提权脚本的软件安装包。

---

## 💻 多语言代码高亮与交互测试

### Go 语言示例 (纯本地微服务)

```go
package main

import (
	"fmt"
	"net/http"
	"time"
)

// SafeServer 演示一个纯本地绑定的 HTTP 监听服务
func SafeServer(port int) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","time":"%s"}`, time.Now().Format(time.RFC3339))
	})
	
	// 仅监听 127.0.0.1 回环地址，严禁暴露外部网络
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	fmt.Printf("服务启动于本地安全端口: %s\n", addr)
	_ = http.ListenAndServe(addr, mux)
}
```

### Python 示例 (超长单行，可测试右上角“折行”按钮)

```python
import json

def process_complex_data_records(input_filepath: str, output_filepath: str, filter_active_users_only: bool = True, maximum_retry_attempts: int = 5):
    """这是一行非常长非常长的函数定义与说明注释，用来测试本阅读器代码块右上角的『折行 / 单行』切换功能是否工作正常。"""
    print(f"Loading data from {input_filepath} with max retries = {maximum_retry_attempts}")
```

### SQL 查询示例

```sql
SELECT 
    u.id, 
    u.username, 
    COUNT(d.id) AS total_documents,
    MAX(d.updated_at) AS last_modified
FROM users u
LEFT JOIN documents d ON u.id = d.author_id
WHERE u.status = 'active'
GROUP BY u.id, u.username
ORDER BY total_documents DESC;
```

---

## 📊 详细对比与待办事项

| 考量维度 | 传统流氓/捆绑软件 | 本自建 Go 阅读器 |
| :--- | :--- | :--- |
| **体积与资源** | 动辄 1.3 GB+ 臃肿 Electron 包，常驻 400MB 内存 | **单文件独立 EXE (11MB)**，占用内存极小 |
| **网络隐私** | 静默上报设备指纹 (Qimei)、云端强制同步 | **100% 离线**，禁止任何外部网络通信 |
| **系统侵入** | 随意静默篡改关联、植入 MSIX 共享扩展 | **纯绿色软件**，支持一键关联或一键解除 |
| **核心功能** | 广告、AI 推荐、云端付费引导 | **纯粹专注阅读**，TOC、大纲筛选、打印、搜索 |

### 功能完成度确认
- [x] 清理本地残留
- [x] 纯 Go 构建原生 WebView2 独立 EXE
- [x] 全面支持 Windows Explorer 文件拖拽即读
- [x] 完善 A4 打印与 PDF 另存为排版
- [x] 页面内关键词全文搜索 (`Ctrl + F`)
- [x] GitHub 风格 Callout 提示框
- [x] 代码块一键复制与折行切换
- [x] 最近打开文件历史快速直达
- [x] 文档多维统计信息弹窗
