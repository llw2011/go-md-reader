// Go Markdown Reader - All-Round Enhanced Client Engine
let currentDocData = null;
let currentTOC = [];
let lastScrollPos = 0;
let searchMatches = [];
let currentSearchIndex = -1;
let editorDebounceTimer = null;
let isEditorOpen = false;
let isEditorDirty = false;
let lightboxZoom = 1;
let loadAbortController = null;
let loadGeneration = 0;
let previewAbortController = null;
let previewGeneration = 0;
let autoWideModeApplied = false;

const SESSION_TOKEN = new URLSearchParams(window.location.search).get("token") || "";

function apiFetch(url, options = {}) {
  const headers = new Headers(options.headers || {});
  if (SESSION_TOKEN) {
    headers.set("X-Auth-Token", SESSION_TOKEN);
  }
  return fetch(url, { ...options, headers });
}

const THEMES = [
  { id: "light", label: "☀️ 浅色" },
  { id: "dark", label: "🌙 深色" },
  { id: "sepia", label: "📜 羊皮纸" },
  { id: "forest", label: "🌲 护眼绿" }
];

document.addEventListener("DOMContentLoaded", () => {
  initTheme();
  initDragDrop();
  initShortcuts();
  initScrollProgress();
  initBackToTop();
  initLightboxWheel();
  initDelegatedLists();
  loadFile();
  initWatcher();
  renderRecentMenu();

  window.addEventListener("click", (e) => {
    if (!e.target.closest(".dropdown")) {
      closeAllDropdowns();
    }
  });
});

function initDelegatedLists() {
  const fileListEl = document.getElementById("file-list");
  if (fileListEl) {
    fileListEl.addEventListener("click", (e) => {
      const item = e.target.closest(".file-item[data-path]");
      if (item) {
        openRecentPath(item.getAttribute("data-path"));
      }
    });
  }

  const recentMenuEl = document.getElementById("recent-menu");
  if (recentMenuEl) {
    recentMenuEl.addEventListener("click", (e) => {
      const item = e.target.closest(".dropdown-item[data-path]");
      if (item) {
        openRecentPath(item.getAttribute("data-path"));
      }
    });
  }

  const tocListEl = document.getElementById("toc-list");
  if (tocListEl) {
    tocListEl.addEventListener("click", (e) => {
      const link = e.target.closest(".toc-item[data-id]");
      if (link) {
        scrollToHeading(e, link.getAttribute("data-id"));
      }
    });
  }
}

// Toast Notification Helper
function showToast(message) {
  const container = document.getElementById("toast-container");
  if (!container) return;
  const el = document.createElement("div");
  el.className = "toast-msg";
  el.textContent = message;
  container.appendChild(el);
  setTimeout(() => {
    if (el.parentNode) el.parentNode.removeChild(el);
  }, 2600);
}

// Load current document from backend with race-condition & scroll-position protection
async function loadFile(preserveScroll = false) {
  const contentEl = document.getElementById("content");
  const tocList = document.getElementById("toc-list");
  const titleEl = document.getElementById("doc-title");
  const metaEl = document.getElementById("doc-meta");
  const tocCountEl = document.getElementById("toc-count");
  const scrollContainer = document.getElementById("content-area");

  const prevPath = currentDocData ? (currentDocData.filepath || "") : "";
  const prevIsMemory = currentDocData ? !!currentDocData.isMemory : false;

  if (preserveScroll) {
    lastScrollPos = scrollContainer ? scrollContainer.scrollTop : 0;
  } else {
    lastScrollPos = 0;
  }

  if (loadAbortController) {
    loadAbortController.abort();
  }
  loadAbortController = new AbortController();
  const myGen = ++loadGeneration;

  try {
    const res = await apiFetch("/api/file", { signal: loadAbortController.signal });
    const data = await res.json();
    if (myGen !== loadGeneration) return;

    currentDocData = data;

    if (!data.hasFile) {
      isEditorDirty = false;
      titleEl.textContent = "Go Markdown Reader";
      metaEl.textContent = "未打开文档";
      contentEl.innerHTML = `
        <div class="empty-state">
          <div class="empty-icon">📖</div>
          <div class="empty-title">欢迎使用 Go 全能文档与 Markdown 阅读器</div>
          <div class="empty-desc">
            100% 本地离线运行 · 零外联 · 零遥测 · 毫秒级启动<br>
            支持直接拖入或打开 <strong>Markdown (.md)</strong>、<strong>Word (.docx)</strong>、<strong>Excel (.xlsx)</strong>、<strong>PPT (.pptx)</strong>、<strong>CSV / JSON / 代码文件</strong>
          </div>
          <div style="display:flex; gap:10px;">
            <button class="btn btn-primary" onclick="openFileDialog()" style="padding: 8px 18px; font-size: 14px;">
              📂 打开本地文档 (Ctrl+O)
            </button>
            <button class="btn" onclick="toggleEditor()" style="padding: 8px 16px; font-size: 14px;">
              ✏️ 新建 / 编辑 Markdown (Ctrl+E)
            </button>
          </div>
        </div>
      `;
      tocList.innerHTML = `<li style="padding:15px; color:var(--text-muted); font-size:12px; text-align:center;">暂无目录</li>`;
      if (tocCountEl) tocCountEl.textContent = "0";
      return;
    }

    let displayTitle = data.filename;
    if (data.isConverted && data.formatNote) {
      displayTitle = `[${data.formatNote}] ${data.filename}`;
    }
    titleEl.textContent = displayTitle;
    titleEl.title = data.filepath;
    document.title = `${data.filename} - Go Markdown Reader`;

    const words = data.wordCount || 0;
    const estMinutes = Math.max(1, Math.round(words / 350));
    let metaText = `${words.toLocaleString()} 字 · 约 ${estMinutes} 分钟`;
    if (data.isConverted) {
      metaText = `✨ 已自动转换 · ` + metaText;
    }
    metaEl.textContent = metaText;

    const isSameDiskFile = !prevIsMemory && !data.isMemory && prevPath !== "" && prevPath === data.filepath;
    if (!isSameDiskFile) {
      clearTimeout(editorDebounceTimer);
      if (previewAbortController) {
        previewAbortController.abort();
      }
      isEditorDirty = false;
    }

    // Populate editor textarea without clobbering unsaved edits
    const editorEl = document.getElementById("editor-textarea");
    if (editorEl) {
      if (!isSameDiskFile || !isEditorDirty) {
        editorEl.value = data.rawContent || "";
      } else {
        showToast("⚠️ 磁盘文件已更新，已保留您当前未保存的编辑内容");
      }
    }

    renderDocumentHTML(data.html, data.toc);

    // Add to recent files if opened from disk path
    if (data.filepath && !data.isMemory) {
      addRecentFile(data.filename, data.filepath);
    }

    // Load sibling files in the same directory
    loadSiblingFiles();

    // Auto-enable wide mode for Excel spreadsheets and restore when leaving them
    const isSpreadsheet = data.filename && /\.(xlsx|xls|csv|tsv)$/i.test(data.filename);
    if (isSpreadsheet) {
      if (!document.body.classList.contains("wide-mode")) {
        applyWideMode(true, true);
      }
    } else if (autoWideModeApplied) {
      applyWideMode(false, false);
    }

    if (scrollContainer) {
      scrollContainer.scrollTop = preserveScroll ? lastScrollPos : 0;
    }

  } catch (err) {
    if (err && err.name === "AbortError") return;
    console.error("加载文件失败:", err);
  }
}

function renderDocumentHTML(htmlStr, tocItems) {
  const contentEl = document.getElementById("content");
  const tocCountEl = document.getElementById("toc-count");

  const fname = currentDocData ? currentDocData.filename : "未命名文档.md";
  const fpath = currentDocData ? currentDocData.filepath : "-";
  const words = currentDocData ? (currentDocData.wordCount || 0) : 0;

  const printHeaderHtml = `
    <div class="print-doc-header">
      <div class="print-title">${escapeHtml(fname)}</div>
      <div class="print-meta">
        路径: ${escapeHtml(fpath)} &nbsp;|&nbsp;
        字数: ${words.toLocaleString()} 字 &nbsp;|&nbsp;
        打印时间: ${new Date().toLocaleString()}
      </div>
    </div>
  `;

  contentEl.innerHTML = printHeaderHtml + htmlStr;

  currentTOC = tocItems || [];
  renderTOC(currentTOC);
  if (tocCountEl) tocCountEl.textContent = `${currentTOC.length}`;

  enhanceCodeBlocks();
  enhanceAlertCallouts();
  enhanceTables();
  enhanceImages();
}

// Sidebar Tabs: TOC vs Folder Siblings
function switchSidebarTab(tab) {
  document.getElementById("tab-btn-toc").classList.toggle("active", tab === "toc");
  document.getElementById("tab-btn-files").classList.toggle("active", tab === "files");
  document.getElementById("panel-toc").classList.toggle("active", tab === "toc");
  document.getElementById("panel-files").classList.toggle("active", tab === "files");
}

async function loadSiblingFiles() {
  const fileListEl = document.getElementById("file-list");
  const countEl = document.getElementById("files-count");
  if (!fileListEl) return;

  try {
    const res = await apiFetch("/api/siblings");
    const data = await res.json();
    const files = data.files || [];
    if (countEl) countEl.textContent = `${files.length}`;

    if (files.length === 0) {
      fileListEl.innerHTML = `<li style="padding:15px; color:var(--text-muted); font-size:12px; text-align:center;">当前目录暂无其他支持的文档</li>`;
      return;
    }

    fileListEl.innerHTML = files.map(f => {
      const icon = getFileIcon(f.ext);
      const extLabel = (f.ext || "").replace(".", "").toUpperCase();
      return `
        <li>
          <a class="file-item ${f.isCurrent ? 'active' : ''}" data-path="${escapeHtml(f.path)}" title="${escapeHtml(f.path)}">
            <span class="file-item-left">
              <span>${icon}</span>
              <span style="overflow:hidden; text-overflow:ellipsis;">${escapeHtml(f.name)}</span>
            </span>
            <span class="file-ext-badge">${escapeHtml(extLabel)}</span>
          </a>
        </li>
      `;
    }).join("");
  } catch (e) {}
}

function getFileIcon(ext) {
  switch (ext) {
    case ".md":
    case ".markdown": return "📝";
    case ".docx":
    case ".doc": return "📘";
    case ".xlsx":
    case ".xls":
    case ".csv":
    case ".tsv": return "📊";
    case ".pptx": return "🖥️";
    case ".json":
    case ".yaml":
    case ".yml": return "⚙️";
    default: return "📄";
  }
}

function filterSiblingFiles(query) {
  const filter = query.trim().toLowerCase();
  document.querySelectorAll("#file-list .file-item").forEach(el => {
    const text = el.textContent.toLowerCase();
    el.parentElement.style.display = text.includes(filter) ? "block" : "none";
  });
}

// Render Table of Contents
function renderTOC(items) {
  const tocList = document.getElementById("toc-list");
  if (!items || items.length === 0) {
    tocList.innerHTML = `<li style="padding:15px; color:var(--text-muted); font-size:12px; text-align:center;">本文暂无标题大纲</li>`;
    return;
  }

  tocList.innerHTML = items.map(item => `
    <li>
      <a href="#${escapeHtml(item.id)}" class="toc-item toc-level-${item.level}" data-id="${escapeHtml(item.id)}">
        ${escapeHtml(item.title)}
      </a>
    </li>
  `).join("");
}

function filterTOC(query) {
  const filter = query.trim().toLowerCase();
  document.querySelectorAll(".toc-item").forEach(el => {
    const text = el.textContent.toLowerCase();
    el.style.display = text.includes(filter) ? "block" : "none";
  });
}

function scrollToHeading(e, id) {
  e.preventDefault();
  const target = document.getElementById(id);
  if (target) {
    target.scrollIntoView({ behavior: "smooth", block: "start" });
    history.replaceState(null, null, `#${id}`);
    updateActiveTOC(id);
  }
}

function updateActiveTOC(activeId) {
  document.querySelectorAll(".toc-item").forEach(item => {
    item.classList.toggle("active", item.getAttribute("data-id") === activeId);
  });
}

// Split-Pane Live Editor
function toggleEditor() {
  const pane = document.getElementById("editor-pane");
  const btn = document.getElementById("edit-btn");
  const textarea = document.getElementById("editor-textarea");

  isEditorOpen = !isEditorOpen;
  pane.classList.toggle("active", isEditorOpen);
  btn.classList.toggle("active", isEditorOpen);

  if (isEditorOpen) {
    if (currentDocData && currentDocData.rawContent && !textarea.value) {
      textarea.value = currentDocData.rawContent;
    }
    textarea.focus();
  }
}

function handleEditorInput() {
  isEditorDirty = true;
  clearTimeout(editorDebounceTimer);

  const expectedDocSeq = currentDocData ? currentDocData.docSeq : undefined;
  const expectedPath = currentDocData ? currentDocData.filepath : "";
  const myGen = ++previewGeneration;

  editorDebounceTimer = setTimeout(async () => {
    const content = document.getElementById("editor-textarea").value;
    if (previewAbortController) {
      previewAbortController.abort();
    }
    previewAbortController = new AbortController();

    try {
      const res = await apiFetch("/api/preview", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ content }),
        signal: previewAbortController.signal
      });
      if (myGen !== previewGeneration) return;
      if (currentDocData && (currentDocData.docSeq !== expectedDocSeq || currentDocData.filepath !== expectedPath)) {
        return;
      }

      if (res.ok) {
        const data = await res.json();
        if (myGen !== previewGeneration) return;
        if (!currentDocData) {
          currentDocData = { hasFile: true, filename: "未命名草稿.md", filepath: "(本地草稿)" };
        }
        currentDocData.rawContent = content;
        currentDocData.html = data.html;
        currentDocData.wordCount = data.wordCount;
        renderDocumentHTML(data.html, data.toc);

        const words = data.wordCount || 0;
        const estMinutes = Math.max(1, Math.round(words / 350));
        document.getElementById("doc-meta").textContent = `✏️ 编辑中 · ${words.toLocaleString()} 字 · 约 ${estMinutes} 分钟`;
      }
    } catch (e) {
      if (e && e.name === "AbortError") return;
    }
  }, 160);
}

function insertSnippet(prefix, suffix) {
  const textarea = document.getElementById("editor-textarea");
  if (!textarea) return;
  const start = textarea.selectionStart;
  const end = textarea.selectionEnd;
  const selected = textarea.value.substring(start, end) || "文本";
  const replacement = prefix + selected + suffix;
  textarea.setRangeText(replacement, start, end, "select");
  textarea.focus();
  handleEditorInput();
}

async function saveCurrentContent() {
  const textarea = document.getElementById("editor-textarea");
  const content = (isEditorOpen && textarea) ? textarea.value : (currentDocData ? currentDocData.rawContent : "");
  if (!content) {
    showToast("⚠️ 当前没有可保存的内容");
    return;
  }

  try {
    const res = await apiFetch("/api/save", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content })
    });
    const data = await res.json();
    if (data.saved) {
      isEditorDirty = false;
      showToast(`✓ 已成功保存至: ${data.path}`);
      loadFile(true);
    } else if (data.error) {
      showToast("❌ 保存失败: " + data.error);
    }
  } catch (err) {
    showToast("❌ 保存失败: " + err.message);
  }
}

async function saveAsMarkdown() {
  closeAllDropdowns();
  const textarea = document.getElementById("editor-textarea");
  const content = (isEditorOpen && textarea && textarea.value) ? textarea.value : (currentDocData ? currentDocData.rawContent : "");
  if (!content) {
    showToast("⚠️ 当前没有可导出的文档内容");
    return;
  }

  const fname = currentDocData && currentDocData.filename ? currentDocData.filename : "document.md";
  try {
    const res = await apiFetch("/api/save-as", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content, filename: fname })
    });
    const data = await res.json();
    if (data.saved) {
      showToast(`✓ 已成功另存为: ${data.path}`);
    } else if (data.error) {
      showToast("❌ 另存为失败: " + data.error);
    }
  } catch (err) {
    showToast("❌ 另存为失败: " + err.message);
  }
}

// Interactive Tables: Sorting & Copy as TSV for Excel
function enhanceTables() {
  document.querySelectorAll(".markdown-body table").forEach(table => {
    if (table.parentElement && table.parentElement.classList.contains("table-wrapper")) return;

    const wrapper = document.createElement("div");
    wrapper.className = "table-wrapper";
    table.parentNode.insertBefore(wrapper, table);
    wrapper.appendChild(table);

    // Copy Table as TSV button
    const copyBtn = document.createElement("button");
    copyBtn.className = "code-btn table-copy-btn";
    copyBtn.textContent = "📋 复制表格 (可粘贴至 Excel)";
    copyBtn.addEventListener("click", () => {
      const rows = Array.from(table.querySelectorAll("tr"));
      const tsv = rows.map(r => {
        return Array.from(r.querySelectorAll("th, td"))
          .map(c => c.innerText.replace(/\t/g, " ").replace(/\n/g, " ").trim())
          .join("\t");
      }).join("\n");

      navigator.clipboard.writeText(tsv).then(() => {
        copyBtn.textContent = "✓ 已复制表格数据";
        copyBtn.classList.add("copied");
        showToast("✓ 表格数据已复制，可直接粘贴到 Excel 中");
        setTimeout(() => {
          copyBtn.textContent = "📋 复制表格 (可粘贴至 Excel)";
          copyBtn.classList.remove("copied");
        }, 1800);
      });
    });
    wrapper.appendChild(copyBtn);

    // Sortable Table Headers
    const headers = table.querySelectorAll("th");
    headers.forEach((th, colIdx) => {
      th.title = "点击按此列排序";
      let asc = true;
      th.addEventListener("click", () => {
        const tbody = table.querySelector("tbody") || table;
        const rows = Array.from(tbody.querySelectorAll("tr")).filter(r => !r.querySelector("th"));
        if (rows.length <= 1) return;

        rows.sort((a, b) => {
          const cellA = a.children[colIdx] ? a.children[colIdx].innerText.trim() : "";
          const cellB = b.children[colIdx] ? b.children[colIdx].innerText.trim() : "";
          const numA = parseFloat(cellA.replace(/,/g, ""));
          const numB = parseFloat(cellB.replace(/,/g, ""));
          if (!isNaN(numA) && !isNaN(numB)) {
            return asc ? numA - numB : numB - numA;
          }
          return asc
            ? cellA.localeCompare(cellB, "zh-CN", { numeric: true })
            : cellB.localeCompare(cellA, "zh-CN", { numeric: true });
        });

        rows.forEach(r => tbody.appendChild(r));
        asc = !asc;
      });
    });
  });
}

// Image Lightbox Viewer
function enhanceImages() {
  document.querySelectorAll(".markdown-body img").forEach(img => {
    img.addEventListener("click", () => {
      openLightbox(img.src);
    });
  });
}

function openLightbox(src) {
  const modal = document.getElementById("lightbox-modal");
  const img = document.getElementById("lightbox-img");
  lightboxZoom = 1;
  img.style.transform = `scale(1)`;
  img.src = src;
  modal.classList.add("show");
}

function closeLightbox() {
  const modal = document.getElementById("lightbox-modal");
  if (modal) modal.classList.remove("show");
}

function initLightboxWheel() {
  const modal = document.getElementById("lightbox-modal");
  const img = document.getElementById("lightbox-img");
  if (!modal || !img) return;

  modal.addEventListener("wheel", (e) => {
    e.preventDefault();
    const delta = e.deltaY < 0 ? 0.15 : -0.15;
    lightboxZoom = Math.min(4, Math.max(0.4, lightboxZoom + delta));
    img.style.transform = `scale(${lightboxZoom})`;
  });
}

// Reading progress bar and active TOC tracking on scroll
function initScrollProgress() {
  const container = document.getElementById("content-area");
  const bar = document.getElementById("progress-bar");

  container.addEventListener("scroll", () => {
    const scrollTop = container.scrollTop;
    const scrollHeight = container.scrollHeight - container.clientHeight;
    const progress = scrollHeight > 0 ? (scrollTop / scrollHeight) * 100 : 0;
    bar.style.width = `${progress}%`;

    const headings = document.querySelectorAll(".markdown-body h1, .markdown-body h2, .markdown-body h3, .markdown-body h4");
    let currentId = "";
    headings.forEach(h => {
      const top = h.offsetTop - container.offsetTop;
      if (top <= scrollTop + 60) {
        currentId = h.id;
      }
    });
    if (currentId) {
      updateActiveTOC(currentId);
    }

    const backToTopBtn = document.getElementById("back-to-top");
    if (backToTopBtn) {
      backToTopBtn.style.display = scrollTop > 300 ? "flex" : "none";
    }
  });
}

function initBackToTop() {
  const btn = document.getElementById("back-to-top");
  if (btn) btn.style.display = "none";
}

function scrollToTop() {
  const container = document.getElementById("content-area");
  if (container) {
    container.scrollTo({ top: 0, behavior: "smooth" });
  }
}

// Enhance code blocks with copy, word-wrap toggle, and language label
function enhanceCodeBlocks() {
  document.querySelectorAll(".markdown-body pre").forEach(pre => {
    if (pre.querySelector(".code-actions")) return;

    const code = pre.querySelector("code");
    let lang = "";
    if (code) {
      const classes = (code.className || "").split(" ");
      for (const cls of classes) {
        if (cls.startsWith("language-") || cls.startsWith("chroma-")) {
          lang = cls.replace("language-", "").replace("chroma-", "");
          break;
        }
      }
    }

    if (lang) {
      const badge = document.createElement("span");
      badge.className = "code-lang-badge";
      badge.textContent = lang;
      pre.appendChild(badge);
    }

    const actions = document.createElement("div");
    actions.className = "code-actions";

    const wrapBtn = document.createElement("button");
    wrapBtn.className = "code-btn";
    wrapBtn.textContent = "折行";
    wrapBtn.title = "切换自动换行";
    wrapBtn.addEventListener("click", () => {
      pre.classList.toggle("wrap-code");
      wrapBtn.textContent = pre.classList.contains("wrap-code") ? "单行" : "折行";
    });

    const copyBtn = document.createElement("button");
    copyBtn.className = "code-btn";
    copyBtn.textContent = "复制";
    copyBtn.addEventListener("click", () => {
      const textToCopy = code ? code.innerText : pre.innerText;
      navigator.clipboard.writeText(textToCopy).then(() => {
        copyBtn.textContent = "已复制 ✓";
        copyBtn.classList.add("copied");
        showToast("✓ 代码已复制到剪贴板");
        setTimeout(() => {
          copyBtn.textContent = "复制";
          copyBtn.classList.remove("copied");
        }, 1800);
      });
    });

    actions.appendChild(wrapBtn);
    actions.appendChild(copyBtn);
    pre.appendChild(actions);
  });
}

// Transform GitHub Alert Callouts
function enhanceAlertCallouts() {
  const alertIcons = {
    note: `<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M0 8a8 8 0 1 1 16 0A8 8 0 0 1 0 8Zm8-6.5a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13ZM6.5 7.75A.75.75 0 0 1 7.25 7h1a.75.75 0 0 1 .75.75v2.75h.25a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1 0-1.5h.25v-2h-.25a.75.75 0 0 1-.75-.75ZM8 6a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z"/></svg>`,
    tip: `<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M8 1.5c-2.363 0-4 1.69-4 3.75 0 .984.424 1.625.984 2.304l.214.253c.223.264.47.556.673.913.307.541.469 1.18.529 1.98H9.6c.06-.8.222-1.439.529-1.98.203-.357.45-.649.673-.913l.214-.253c.56-.679.984-1.32.984-2.304 0-2.06-1.637-3.75-4-3.75ZM5.75 12h4.5a.75.75 0 0 1 0 1.5h-4.5a.75.75 0 0 1 0-1.5Zm1 2.5h2.5a.75.75 0 0 1 0 1.5h-2.5a.75.75 0 0 1 0-1.5Z"/></svg>`,
    important: `<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M0 1.75C0 .784.784 0 1.75 0h12.5C15.216 0 16 .784 16 1.75v9.5A1.75 1.75 0 0 1 14.25 13H9.06l-2.573 2.573A1.458 1.458 0 0 1 4 14.543V13H1.75A1.75 1.75 0 0 1 0 11.25Zm1.75-.25a.25.25 0 0 0-.25.25v9.5c0 .138.112.25.25.25h3a.75.75 0 0 1 .75.75v2.19l2.72-2.72a.749.749 0 0 1 .53-.22h5.5a.25.25 0 0 0 .25-.25v-9.5a.25.25 0 0 0-.25-.25Zm6.25 3.25a.75.75 0 0 1 .75.75v2a.75.75 0 0 1-1.5 0v-2a.75.75 0 0 1 .75-.75Zm0 5.5a.75.75 0 1 1 0-1.5.75.75 0 0 1 0 1.5Z"/></svg>`,
    warning: `<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M6.457 1.047c.659-1.234 2.427-1.234 3.086 0l6.082 11.378A1.75 1.75 0 0 1 14.082 15H1.918a1.75 1.75 0 0 1-1.543-2.575Zm1.763.707a.25.25 0 0 0-.44 0L1.698 13.132a.25.25 0 0 0 .22.368h12.164a.25.25 0 0 0 .22-.368Zm.53 3.996v2.5a.75.75 0 0 1-1.5 0v-2.5a.75.75 0 0 1 1.5 0ZM9 11a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z"/></svg>`,
    caution: `<svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M4.47.047A1.75 1.75 0 0 1 5.707 0h4.586c.464 0 .909.184 1.237.513l3.966 3.966c.329.328.513.773.513 1.237v4.586a1.75 1.75 0 0 1-.513 1.237l-3.966 3.966a1.75 1.75 0 0 1-1.237.513H5.707a1.75 1.75 0 0 1-1.237-.513L.504 11.539A1.75 1.75 0 0 1 0 10.302V5.716c0-.464.184-.909.513-1.237ZM5.707 1.5a.25.25 0 0 0-.177.073L1.573 5.539a.25.25 0 0 0-.073.177v4.586c0 .066.026.13.073.177l3.957 3.957a.25.25 0 0 0 .177.073h4.586a.25.25 0 0 0 .177-.073l3.957-3.957a.25.25 0 0 0 .073-.177V5.716a.25.25 0 0 0-.073-.177L9.47 1.573A.25.25 0 0 0 9.293 1.5ZM8 3.75a.75.75 0 0 1 .75.75v3.5a.75.75 0 0 1-1.5 0v-3.5A.75.75 0 0 1 8 3.75Zm0 7a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z"/></svg>`
  };

  document.querySelectorAll(".markdown-body blockquote").forEach(bq => {
    const firstP = bq.querySelector("p");
    if (!firstP) return;

    const text = firstP.innerHTML.trim();
    const match = text.match(/^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]/i);
    if (!match) return;

    const type = match[1].toLowerCase();
    const cleanText = text.replace(/^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*(<br\s*\/?>)?/i, "");
    firstP.innerHTML = cleanText;

    bq.className = `alert alert-${type}`;
    const header = document.createElement("div");
    header.className = "alert-title";
    header.innerHTML = `${alertIcons[type] || ""} <span>${type.toUpperCase()}</span>`;
    bq.insertBefore(header, firstP);
  });
}

// Robust Drag & Drop Handling
function initDragDrop() {
  const overlay = document.getElementById("drag-overlay");
  let dragCounter = 0;

  window.addEventListener("dragenter", (e) => {
    e.preventDefault();
    dragCounter++;
    overlay.classList.add("active");
  });

  window.addEventListener("dragleave", (e) => {
    e.preventDefault();
    dragCounter--;
    if (dragCounter <= 0) {
      dragCounter = 0;
      overlay.classList.remove("active");
    }
  });

  window.addEventListener("dragover", (e) => {
    e.preventDefault();
  });

  window.addEventListener("drop", async (e) => {
    e.preventDefault();
    dragCounter = 0;
    overlay.classList.remove("active");

    const files = e.dataTransfer.files;
    if (files.length === 0) return;

    const file = files[0];
    const filePath = file.path || "";

    if (filePath) {
      try {
        const res = await apiFetch("/api/open-path", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: filePath })
        });
        if (res.ok) {
          showToast(`✓ 已加载: ${file.name}`);
          loadFile(false);
          return;
        }
      } catch (err) {}
    }

    try {
      const buf = await file.arrayBuffer();
      const res = await apiFetch(`/api/open-binary?name=${encodeURIComponent(file.name)}`, {
        method: "POST",
        body: buf
      });
      if (res.ok) {
        showToast(`✓ 已导入并解析: ${file.name}`);
        loadFile(false);
      } else {
        showToast("❌ 解析文档失败，请检查文件格式");
      }
    } catch (err) {
      showToast("❌ 读取拖拽文件失败: " + err.message);
    }
  });
}

// Print & Export Suite
function triggerPrint() {
  closeAllDropdowns();
  window.print();
}

function copyMarkdownSource() {
  closeAllDropdowns();
  const textarea = document.getElementById("editor-textarea");
  const md = (isEditorOpen && textarea && textarea.value) ? textarea.value : (currentDocData ? currentDocData.rawContent : "");
  if (!md) {
    showToast("⚠️ 当前无 Markdown 源码可复制");
    return;
  }
  navigator.clipboard.writeText(md).then(() => {
    showToast("✓ 已复制全文 Markdown 源码到剪贴板");
  });
}

function copyPlainText() {
  closeAllDropdowns();
  const contentEl = document.getElementById("content");
  if (!contentEl || !currentDocData || !currentDocData.hasFile) {
    showToast("⚠️ 当前无文本内容可复制");
    return;
  }
  navigator.clipboard.writeText(contentEl.innerText).then(() => {
    showToast("✓ 已复制全文纯文本到剪贴板");
  });
}

async function exportStandaloneHTML() {
  closeAllDropdowns();
  if (!currentDocData || !currentDocData.html) {
    showToast("⚠️ 当前无已加载的文档可导出");
    return;
  }

  try {
    const cssRes = await fetch("/assets/style.css");
    const cssText = await cssRes.text();
    const currentTheme = document.documentElement.getAttribute("data-theme") || "light";

    const standaloneHTML = `<!DOCTYPE html>
<html lang="zh-CN" data-theme="${currentTheme}">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>${escapeHtml(currentDocData.filename)}</title>
  <style>
${cssText}
  body { overflow: auto; height: auto; padding: 40px 20px; }
  .content-area { padding: 0; display: flex; justify-content: center; }
  </style>
</head>
<body>
  <div class="content-area">
    <article class="markdown-body">
      ${currentDocData.html}
    </article>
  </div>
</body>
</html>`;

    const saveRes = await apiFetch("/api/save-html", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        html: standaloneHTML,
        filename: currentDocData.filename || "document"
      })
    });
    if (saveRes.ok) {
      const saveData = await saveRes.json();
      if (saveData.saved) {
        showToast(`✓ 已导出独立离线 HTML 文件: ${saveData.path}`);
        return;
      }
      if (saveData.error) {
        showToast("❌ 导出 HTML 失败: " + saveData.error);
      }
      return;
    }

    const blob = new Blob([standaloneHTML], { type: "text/html;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${currentDocData.filename || "document"}.html`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
    showToast("✓ 已生成独立离线 HTML 文件");
  } catch (err) {
    showToast("❌ 导出 HTML 失败: " + err.message);
  }
}

// In-Page Search Feature
function toggleSearch() {
  const container = document.getElementById("search-container");
  const input = document.getElementById("search-input");
  container.classList.toggle("active");
  if (container.classList.contains("active")) {
    input.focus();
    input.select();
  } else {
    clearSearchHighlights();
  }
}

function closeSearch() {
  const container = document.getElementById("search-container");
  if (container) container.classList.remove("active");
  clearSearchHighlights();
}

function handleSearch(query) {
  clearSearchHighlights();
  query = query.trim();
  const countEl = document.getElementById("search-count");

  if (!query) {
    countEl.textContent = "0 / 0";
    return;
  }

  const content = document.getElementById("content");
  searchMatches = [];
  currentSearchIndex = -1;

  highlightTextNodes(content, query);

  searchMatches = Array.from(content.querySelectorAll(".search-match"));
  if (searchMatches.length > 0) {
    currentSearchIndex = 0;
    updateSearchMatchDisplay();
  } else {
    countEl.textContent = "0 / 0";
  }
}

function highlightTextNodes(element, query) {
  const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT, {
    acceptNode: (node) => {
      if (node.parentElement && (node.parentElement.tagName === "SCRIPT" || node.parentElement.classList.contains("search-match"))) {
        return NodeFilter.FILTER_REJECT;
      }
      return NodeFilter.FILTER_ACCEPT;
    }
  });

  const nodesToReplace = [];
  while (walker.nextNode()) {
    const textNode = walker.currentNode;
    if (textNode.nodeValue.toLowerCase().includes(query.toLowerCase())) {
      nodesToReplace.push(textNode);
    }
  }

  const regex = new RegExp(`(${escapeRegex(query)})`, "gi");
  nodesToReplace.forEach(textNode => {
    const parent = textNode.parentNode;
    if (!parent) return;

    const frag = document.createDocumentFragment();
    const parts = textNode.nodeValue.split(regex);
    parts.forEach(part => {
      if (part.toLowerCase() === query.toLowerCase()) {
        const mark = document.createElement("mark");
        mark.className = "search-match";
        mark.textContent = part;
        frag.appendChild(mark);
      } else if (part) {
        frag.appendChild(document.createTextNode(part));
      }
    });
    parent.replaceChild(frag, textNode);
  });
}

function clearSearchHighlights() {
  document.querySelectorAll(".search-match").forEach(mark => {
    const parent = mark.parentNode;
    if (parent) {
      parent.replaceChild(document.createTextNode(mark.textContent), mark);
      parent.normalize();
    }
  });
  searchMatches = [];
  currentSearchIndex = -1;
}

function navigateSearch(direction) {
  if (searchMatches.length === 0) return;
  currentSearchIndex += direction;
  if (currentSearchIndex >= searchMatches.length) currentSearchIndex = 0;
  if (currentSearchIndex < 0) currentSearchIndex = searchMatches.length - 1;
  updateSearchMatchDisplay();
}

function updateSearchMatchDisplay() {
  const countEl = document.getElementById("search-count");
  searchMatches.forEach((m, idx) => {
    if (idx === currentSearchIndex) {
      m.classList.add("current");
      m.scrollIntoView({ behavior: "smooth", block: "center" });
    } else {
      m.classList.remove("current");
    }
  });
  countEl.textContent = `${currentSearchIndex + 1} / ${searchMatches.length}`;
}

// Dropdown & Recent Files Menu
function toggleDropdown(menuId, e) {
  e.stopPropagation();
  const target = document.getElementById(menuId);
  const isShown = target.classList.contains("show");
  closeAllDropdowns();
  if (!isShown) target.classList.add("show");
}

function closeAllDropdowns() {
  document.querySelectorAll(".dropdown-menu").forEach(m => m.classList.remove("show"));
}

function addRecentFile(name, path) {
  try {
    let recent = JSON.parse(localStorage.getItem("go-md-recent-files") || "[]");
    recent = recent.filter(item => item.path !== path);
    recent.unshift({ name, path, time: Date.now() });
    if (recent.length > 12) recent.pop();
    localStorage.setItem("go-md-recent-files", JSON.stringify(recent));
    renderRecentMenu();
  } catch (e) {}
}

function renderRecentMenu() {
  const menu = document.getElementById("recent-menu");
  if (!menu) return;
  try {
    const recent = JSON.parse(localStorage.getItem("go-md-recent-files") || "[]");
    if (recent.length === 0) {
      menu.innerHTML = `<li style="padding:10px 14px; font-size:12px; color:var(--text-muted); text-align:center;">暂无打开历史</li>`;
      return;
    }
    menu.innerHTML = recent.map(item => `
      <li class="dropdown-item" data-path="${escapeHtml(item.path)}">
        <span class="dropdown-item-name">${escapeHtml(item.name)}</span>
        <span class="dropdown-item-path" title="${escapeHtml(item.path)}">${escapeHtml(item.path)}</span>
      </li>
    `).join("");
  } catch (e) {}
}

async function openRecentPath(path) {
  closeAllDropdowns();
  try {
    const res = await apiFetch("/api/open-path", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path })
    });
    if (res.ok) {
      loadFile(false);
    } else {
      showToast("❌ 文件不存在或无法访问");
    }
  } catch (err) {
    showToast("❌ 无法打开该文件: " + err.message);
  }
}

async function openFileDialog() {
  try {
    const res = await apiFetch("/api/open-dialog", { method: "POST" });
    const data = await res.json();
    if (data.opened) {
      loadFile(false);
    } else if (data.error) {
      showToast("❌ 打开文件失败: " + data.error);
    }
  } catch (err) {
    console.error("打开文件对话框失败:", err);
  }
}

// Document Stats Modal
function showStatsModal() {
  if (!currentDocData || !currentDocData.hasFile) return;

  const modal = document.getElementById("stats-modal");
  document.getElementById("stat-words").textContent = (currentDocData.wordCount || 0).toLocaleString();
  document.getElementById("stat-read-time").textContent = `${Math.max(1, Math.round((currentDocData.wordCount || 0) / 350))} 分钟`;
  document.getElementById("stat-lines").textContent = (currentDocData.lineCount || 0).toLocaleString();

  const sizeKB = currentDocData.fileSize ? (currentDocData.fileSize / 1024).toFixed(1) : "0";
  document.getElementById("stat-size").textContent = `${sizeKB} KB`;
  document.getElementById("stat-headings").textContent = (currentDocData.toc || []).length;
  document.getElementById("stat-mtime").textContent = currentDocData.modTime || "-";
  document.getElementById("stat-path").textContent = currentDocData.filepath || "-";

  modal.classList.add("show");
}

function closeStatsModal(e) {
  if (e && e.target !== e.currentTarget && !e.target.classList.contains("stats-card-close")) return;
  const modal = document.getElementById("stats-modal");
  if (modal) modal.classList.remove("show");
}

// File Associations Settings Modal
let currentAssocCatalog = [];

async function showAssocModal() {
  const modal = document.getElementById("assoc-modal");
  const container = document.getElementById("assoc-list-container");
  const ctxCheckbox = document.getElementById("assoc-ctx-menu");
  if (!modal || !container) return;

  try {
    const [res, trayRes] = await Promise.all([
      apiFetch("/api/assoc"),
      apiFetch("/api/tray/prefs")
    ]);
    const data = await res.json();
    currentAssocCatalog = data.extensions || [];
    ctxCheckbox.checked = !!data.contextMenu;

    const minTrayCb = document.getElementById("pref-minimize-tray");
    if (minTrayCb && trayRes.ok) {
      const trayData = await trayRes.json();
      minTrayCb.checked = !!trayData.minimizeToTray;
    }

    container.innerHTML = currentAssocCatalog.map(item => `
      <label class="stats-item" style="display:flex; align-items:center; gap:8px; cursor:pointer; user-select:none; padding: 8px 10px;">
        <input type="checkbox" class="assoc-ext-cb" value="${escapeHtml(item.ext)}" data-cat="${escapeHtml(item.category)}" ${item.isAssociated ? "checked" : ""}>
        <div style="overflow:hidden;">
          <div style="font-weight:600; font-size:13px; color:var(--text-main);">${escapeHtml(item.ext)}</div>
          <div style="font-size:11px; color:var(--text-muted); white-space:nowrap; overflow:hidden; text-overflow:ellipsis;">${escapeHtml(item.name)}</div>
        </div>
      </label>
    `).join("");

    modal.classList.add("show");
  } catch (err) {
    showToast("❌ 获取系统关联状态失败: " + err.message);
  }
}

function closeAssocModal(e) {
  if (e && e.target !== e.currentTarget && !e.target.classList.contains("stats-card-close")) return;
  const modal = document.getElementById("assoc-modal");
  if (modal) modal.classList.remove("show");
}

function selectAssocPreset(mode) {
  document.querySelectorAll(".assoc-ext-cb").forEach(cb => {
    const ext = cb.value;
    const cat = cb.getAttribute("data-cat");
    if (mode === "none") {
      cb.checked = false;
    } else if (mode === "recommended") {
      cb.checked = (ext === ".md" || ext === ".markdown" || ext === ".txt");
    } else if (mode === "all") {
      cb.checked = (cat !== "office");
    }
  });
}

async function saveAssocSettings() {
  const selectedExts = Array.from(document.querySelectorAll(".assoc-ext-cb:checked")).map(cb => cb.value);
  const contextMenu = document.getElementById("assoc-ctx-menu").checked;
  const minTrayCb = document.getElementById("pref-minimize-tray");
  const minimizeToTray = minTrayCb ? minTrayCb.checked : true;

  try {
    const [res] = await Promise.all([
      apiFetch("/api/assoc", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ extensions: selectedExts, contextMenu })
      }),
      apiFetch("/api/tray/prefs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ minimizeToTray })
      })
    ]);
    if (res.ok) {
      closeAssocModal();
      showToast(`✓ 已保存设置并应用 ${selectedExts.length} 项文件格式关联！`);
    } else {
      showToast("❌ 保存关联设置失败");
    }
  } catch (err) {
    showToast("❌ 关联设置异常: " + err.message);
  }
}

// GitHub OTA Update Management
let clearTokenFlag = false;

async function showOTAModal() {
  const modal = document.getElementById("ota-modal");
  if (!modal) return;

  clearTokenFlag = false;
  document.getElementById("ota-token").value = "";
  try {
    const res = await apiFetch("/api/ota/config");
    if (res.ok) {
      const cfg = await res.json();
      applyOTAConfigToUI(cfg);
    }
  } catch (e) {}
  modal.classList.add("show");
}

function applyOTAConfigToUI(cfg) {
  document.getElementById("ota-current-ver").textContent = cfg.currentVersion || "v2.0.0";
  document.getElementById("ota-repo").value = cfg.repo || "";
  document.getElementById("ota-prerelease").checked = !!cfg.includePrerelease;
  const statusEl = document.getElementById("ota-token-status");
  const clearBtn = document.getElementById("ota-clear-token-btn");
  if (cfg.hasToken) {
    statusEl.textContent = `已在本机保存 GitHub Token (${cfg.tokenPreview})`;
    clearBtn.style.display = "inline-flex";
  } else {
    statusEl.textContent = "未配置 Token（将使用匿名公开仓库模式）";
    clearBtn.style.display = "none";
  }
}

function closeOTAModal(e) {
  if (e && e.target !== e.currentTarget && !e.target.classList.contains("stats-card-close")) return;
  const modal = document.getElementById("ota-modal");
  if (modal) modal.classList.remove("show");
}

function clearSavedOTAToken() {
  clearTokenFlag = true;
  document.getElementById("ota-token").value = "";
  document.getElementById("ota-token-status").textContent = "已标记清除 Token（点击保存或检查后生效）";
  document.getElementById("ota-clear-token-btn").style.display = "none";
}

async function saveOTASettings(silentToast = false) {
  const repo = document.getElementById("ota-repo").value.trim();
  const token = document.getElementById("ota-token").value.trim();
  const includePrerelease = document.getElementById("ota-prerelease").checked;

  const res = await apiFetch("/api/ota/config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      repo,
      token,
      clearToken: clearTokenFlag,
      includePrerelease
    })
  });
  const data = await res.json();
  if (!res.ok || !data.ok) {
    showToast("❌ " + (data.error || "保存 OTA 设置失败"));
    return false;
  }
  clearTokenFlag = false;
  document.getElementById("ota-token").value = "";
  applyOTAConfigToUI(data.config);
  if (!silentToast) {
    showToast("✓ GitHub OTA 设置已保存至本机");
  }
  return true;
}

async function checkOTAUpdate() {
  const saved = await saveOTASettings(true);
  if (!saved) return;

  const btn = document.getElementById("btn-ota-check");
  const resultBox = document.getElementById("ota-result-box");
  const statusLine = document.getElementById("ota-status-line");
  const notesEl = document.getElementById("ota-release-notes");
  const assetInfoEl = document.getElementById("ota-asset-info");
  const dlBtn = document.getElementById("btn-ota-download");
  const applyBtn = document.getElementById("btn-ota-apply");

  btn.disabled = true;
  btn.textContent = "⏳ 正在查询 GitHub...";
  dlBtn.style.display = "none";
  applyBtn.style.display = "none";

  try {
    const res = await apiFetch("/api/ota/check", { method: "POST" });
    const data = await res.json();
    resultBox.style.display = "block";

    if (!res.ok || !data.ok) {
      statusLine.textContent = "❌ 检查失败: " + (data.error || "请求异常");
      statusLine.style.color = "#ef4444";
      notesEl.style.display = "none";
      assetInfoEl.textContent = "";
      return;
    }

    const r = data.result;
    notesEl.style.display = r.releaseNotes ? "block" : "none";
    notesEl.textContent = r.releaseNotes || "";

    if (r.hasUpdate) {
      statusLine.textContent = `🎉 发现新版本: ${r.latestVersion} (${r.releaseName || ""})`;
      statusLine.style.color = "#10b981";
      if (r.asset) {
        const mb = (r.asset.size / (1024 * 1024)).toFixed(2);
        const shaNote = r.asset.sha256 ? " · 支持 SHA-256 校验" : "";
        assetInfoEl.textContent = `匹配更新包: ${r.asset.name} (${mb} MB${shaNote})`;
        dlBtn.style.display = "inline-flex";
      } else {
        assetInfoEl.textContent = "该 Release 未附带 .exe 更新包资产";
      }
    } else {
      statusLine.textContent = `✓ 当前已是最新版本 (${r.currentVersion}，远端 ${r.latestVersion})`;
      statusLine.style.color = "var(--text-main)";
      assetInfoEl.textContent = "";
    }
  } catch (err) {
    resultBox.style.display = "block";
    statusLine.textContent = "❌ 网络异常: " + err.message;
    statusLine.style.color = "#ef4444";
  } finally {
    btn.disabled = false;
    btn.textContent = "🔍 检查最新版本";
  }
}

async function downloadOTAUpdate() {
  const dlBtn = document.getElementById("btn-ota-download");
  const applyBtn = document.getElementById("btn-ota-apply");
  const assetInfoEl = document.getElementById("ota-asset-info");

  dlBtn.disabled = true;
  dlBtn.textContent = "⏳ 正在下载与校验...";
  try {
    const res = await apiFetch("/api/ota/download", { method: "POST" });
    const data = await res.json();
    if (!res.ok || !data.ok) {
      showToast("❌ " + (data.error || "下载更新包失败"));
      return;
    }
    const st = data.staged;
    const verifyText = st.verifiedSha256 ? "SHA-256 校验通过 ✓" : "已完成下载 (远端未提供摘要)";
    assetInfoEl.textContent = `就绪: ${st.assetName} · ${verifyText}`;
    dlBtn.style.display = "none";
    applyBtn.style.display = "inline-flex";
    showToast("✓ 更新包已下载并校验完成，可点击安装重启");
  } catch (err) {
    showToast("❌ 下载异常: " + err.message);
  } finally {
    dlBtn.disabled = false;
    dlBtn.textContent = "⬇️ 下载并校验更新";
  }
}

async function applyOTAUpdate() {
  const applyBtn = document.getElementById("btn-ota-apply");
  applyBtn.disabled = true;
  applyBtn.textContent = "⏳ 正在替换并重启...";
  try {
    const res = await apiFetch("/api/ota/apply", { method: "POST" });
    const data = await res.json();
    if (!res.ok || !data.ok) {
      showToast("❌ " + (data.error || "应用更新失败"));
      applyBtn.disabled = false;
      applyBtn.textContent = "🚀 立即安装并重启";
    }
  } catch (err) {
    showToast("❌ 应用更新异常: " + err.message);
    applyBtn.disabled = false;
    applyBtn.textContent = "🚀 立即安装并重启";
  }
}

// Sidebar, Wide Mode, Theme & Font Size Controls
function toggleSidebar() {
  const sidebar = document.getElementById("sidebar");
  sidebar.classList.toggle("hidden");
}

function applyWideMode(isWide, isAuto = false) {
  autoWideModeApplied = isAuto && isWide;
  document.body.classList.toggle("wide-mode", isWide);
  const btn = document.getElementById("wide-btn");
  if (btn) btn.classList.toggle("active", isWide);
}

function toggleWideMode(forceState) {
  const isWide = typeof forceState === "boolean" ? forceState : !document.body.classList.contains("wide-mode");
  applyWideMode(isWide, false);
}

function initTheme() {
  const saved = localStorage.getItem("go-md-theme") || "light";
  setTheme(saved);
}

function cycleTheme() {
  const current = document.documentElement.getAttribute("data-theme") || "light";
  const idx = THEMES.findIndex(t => t.id === current);
  const next = THEMES[(idx + 1) % THEMES.length];
  setTheme(next.id);
  showToast(`已切换至: ${next.label}模式`);
}

function setTheme(themeId) {
  const found = THEMES.find(t => t.id === themeId) || THEMES[0];
  document.documentElement.setAttribute("data-theme", found.id);
  localStorage.setItem("go-md-theme", found.id);
  const icon = document.getElementById("theme-icon");
  if (icon) icon.textContent = found.label;
}

let currentFontSize = 16;
function adjustFontSize(delta) {
  if (delta === 0) currentFontSize = 16;
  else currentFontSize = Math.min(26, Math.max(12, currentFontSize + delta));
  document.documentElement.style.setProperty("--content-font-size", `${currentFontSize}px`);
}

// Live file watcher via SSE
function initWatcher() {
  try {
    const watchUrl = SESSION_TOKEN ? `/api/watch?token=${encodeURIComponent(SESSION_TOKEN)}` : "/api/watch";
    const evtSource = new EventSource(watchUrl);
    evtSource.onmessage = (event) => {
      if (event.data === "reload") {
        loadFile(true);
      } else if (event.data === "open-ota") {
        showOTAModal();
      }
    };
  } catch (e) {}
}

// Keyboard shortcuts
function initShortcuts() {
  window.addEventListener("keydown", (e) => {
    if (e.ctrlKey && e.key.toLowerCase() === "o") {
      e.preventDefault();
      openFileDialog();
    } else if (e.ctrlKey && e.key.toLowerCase() === "b") {
      e.preventDefault();
      toggleSidebar();
    } else if (e.ctrlKey && e.key.toLowerCase() === "e") {
      e.preventDefault();
      toggleEditor();
    } else if (e.ctrlKey && e.key.toLowerCase() === "s") {
      e.preventDefault();
      saveCurrentContent();
    } else if (e.ctrlKey && e.key.toLowerCase() === "f") {
      e.preventDefault();
      toggleSearch();
    } else if (e.ctrlKey && e.key.toLowerCase() === "p") {
      e.preventDefault();
      triggerPrint();
    } else if (e.ctrlKey && (e.key === "=" || e.key === "+")) {
      e.preventDefault();
      adjustFontSize(1);
    } else if (e.ctrlKey && e.key === "-") {
      e.preventDefault();
      adjustFontSize(-1);
    } else if (e.ctrlKey && e.key === "0") {
      e.preventDefault();
      adjustFontSize(0);
    } else if (e.key === "F5" || (e.ctrlKey && e.key.toLowerCase() === "r")) {
      e.preventDefault();
      loadFile(true);
    } else if (e.key === "Escape") {
      closeSearch();
      closeStatsModal();
      closeAssocModal();
      closeOTAModal();
      closeAllDropdowns();
      closeLightbox();
    } else if (e.key === "Enter") {
      const searchBox = document.getElementById("search-container");
      if (searchBox && searchBox.classList.contains("active") && document.activeElement === document.getElementById("search-input")) {
        e.preventDefault();
        navigateSearch(e.shiftKey ? -1 : 1);
      }
    }
  });
}

function escapeHtml(str) {
  if (!str) return "";
  return String(str).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function escapeQuotes(str) {
  if (!str) return "";
  return String(str).replace(/\\/g, "\\\\").replace(/'/g, "\\'").replace(/"/g, '\\"');
}

function escapeRegex(str) {
  return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
