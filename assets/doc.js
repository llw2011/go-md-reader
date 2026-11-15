// Load current document from backend with race-condition & scroll-position protection
async function loadFile(preserveScroll = false) {
  const contentEl = document.getElementById("content");
  const tocList = document.getElementById("toc-list");
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

  const myGen = ++loadGeneration;

  try {
    const data = await rpcCall("doc.get");
    if (myGen !== loadGeneration) return;

    currentDocData = data;

    if (!data.hasFile) {
      isEditorDirty = false;
      metaEl.textContent = "未打开文档";
      const tbDoc = document.getElementById("titlebar-doc-name");
      if (tbDoc) tbDoc.textContent = "就绪";
      document.title = "Go Markdown Reader";
      contentEl.innerHTML = `
        <div class="empty-state">
          <div class="empty-icon">📖</div>
          <div class="empty-title">欢迎使用 Go Markdown Reader</div>
          <div class="empty-desc">
            打开或拖入文档即可阅读<br>
            支持 Markdown、Word、Excel、PPT、EPUB、网页与常见代码文件
          </div>
          <div class="empty-actions">
            <button type="button" class="btn btn-primary btn-lg" onclick="openFileDialog()">
              📂 打开文档 (Ctrl+O)
            </button>
            <button type="button" class="btn btn-lg" onclick="toggleEditor()">
              ✏️ 新建笔记 (Ctrl+E)
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
    document.title = `${data.filename} - Go Markdown Reader`;
    const tbDoc = document.getElementById("titlebar-doc-name");
    if (tbDoc) {
      tbDoc.textContent = displayTitle;
      tbDoc.title = data.filepath || "";
    }

    const words = data.wordCount || 0;
    const estMinutes = Math.max(1, Math.round(words / 350));
    let metaText = `${words.toLocaleString()} 字 · 约 ${estMinutes} 分钟`;
    if (data.isConverted) {
      metaText = `✨ 已转换 · ` + metaText;
    }
    metaEl.textContent = metaText;

    const isSameDiskFile = !prevIsMemory && !data.isMemory && prevPath !== "" && prevPath === data.filepath;
    if (!isSameDiskFile) {
      clearTimeout(editorDebounceTimer);
      previewGeneration++;
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
      if (preserveScroll) {
        scrollContainer.scrollTop = lastScrollPos;
      } else if (data.filepath && !data.isMemory) {
        const savedScroll = (uiPrefsCache.scrollPositions && uiPrefsCache.scrollPositions[data.filepath]) || 0;
        scrollContainer.scrollTop = savedScroll;
      } else {
        scrollContainer.scrollTop = 0;
      }
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

  if (typeof enhanceRichMedia === "function") {
    enhanceRichMedia();
  }
  enhanceCodeBlocks();
  enhanceAlertCallouts();
  enhanceTables();
  enhanceImages();
}

// Sidebar Tabs: TOC vs Folder Siblings
function switchSidebarTab(tab) {
  const tocBtn = document.getElementById("tab-btn-toc");
  const filesBtn = document.getElementById("tab-btn-files");
  tocBtn.classList.toggle("active", tab === "toc");
  filesBtn.classList.toggle("active", tab === "files");
  tocBtn.setAttribute("aria-selected", String(tab === "toc"));
  filesBtn.setAttribute("aria-selected", String(tab === "files"));
  document.getElementById("panel-toc").classList.toggle("active", tab === "toc");
  document.getElementById("panel-files").classList.toggle("active", tab === "files");
}

async function loadSiblingFiles() {
  const fileListEl = document.getElementById("file-list");
  const countEl = document.getElementById("files-count");
  if (!fileListEl) return;

  try {
    const data = await rpcCall("doc.siblings");
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
    case ".epub": return "📚";
    case ".html":
    case ".htm": return "🌐";
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

