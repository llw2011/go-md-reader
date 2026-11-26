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
          <div class="welcome-recent" id="welcome-recent"></div>
        </div>
      `;
      tocList.innerHTML = `<li style="padding:15px; color:var(--text-muted); font-size:12px; text-align:center;">暂无目录</li>`;
      if (tocCountEl) tocCountEl.textContent = "0";
      renderWelcomeRecent();
      return;
    }

    if (data.loadErrorKind) {
      renderLoadErrorCard(data);
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

let currentTOCNodes = [];
let collapsedTOCIndices = new Set();
let tocAllFolded = false;

// Render Table of Contents
function renderTOC(items) {
  const tocList = document.getElementById("toc-list");
  const foldAllBtn = document.getElementById("toc-fold-all-btn");
  collapsedTOCIndices.clear();
  tocAllFolded = false;

  if (!items || items.length === 0) {
    currentTOCNodes = [];
    if (foldAllBtn) foldAllBtn.style.display = "none";
    tocList.innerHTML = `<li style="padding:15px; color:var(--text-muted); font-size:12px; text-align:center;">本文暂无标题大纲</li>`;
    return;
  }

  currentTOCNodes = computeTOCParents(items);
  const anyCollapsible = currentTOCNodes.some(n => n.hasChildren);
  if (foldAllBtn) {
    foldAllBtn.style.display = anyCollapsible ? "" : "none";
    foldAllBtn.textContent = "折叠";
  }

  tocList.innerHTML = currentTOCNodes.map(node => `
    <li class="toc-row" data-toc-idx="${node.index}" data-parent-idx="${node.parentIndex}">
      <div class="toc-entry toc-level-${node.level}">
        ${node.hasChildren
          ? `<button type="button" class="toc-fold-btn" data-fold-idx="${node.index}" aria-expanded="true" aria-label="折叠章节">▾</button>`
          : `<span class="toc-fold-spacer"></span>`}
        <a href="#${escapeHtml(node.id)}" class="toc-item" data-id="${escapeHtml(node.id)}">${escapeHtml(node.title)}</a>
      </div>
    </li>
  `).join("");
}

function applyTOCVisibility() {
  const filterInput = document.getElementById("toc-filter-input");
  const query = filterInput ? filterInput.value.trim().toLowerCase() : "";
  document.querySelectorAll("#toc-list .toc-row").forEach(row => {
    const idx = Number(row.getAttribute("data-toc-idx"));
    const node = currentTOCNodes[idx];
    const foldBtn = row.querySelector(".toc-fold-btn");
    if (foldBtn) {
      const isFolded = collapsedTOCIndices.has(idx);
      foldBtn.textContent = isFolded ? "▸" : "▾";
      foldBtn.setAttribute("aria-expanded", String(!isFolded));
    }
    if (query) {
      const text = row.textContent.toLowerCase();
      row.style.display = text.includes(query) ? "" : "none";
      return;
    }
    let p = node ? node.parentIndex : -1;
    let hidden = false;
    while (p !== -1) {
      if (collapsedTOCIndices.has(p)) {
        hidden = true;
        break;
      }
      p = currentTOCNodes[p] ? currentTOCNodes[p].parentIndex : -1;
    }
    row.style.display = hidden ? "none" : "";
  });
}

function toggleTOCFold(idx) {
  if (collapsedTOCIndices.has(idx)) {
    collapsedTOCIndices.delete(idx);
  } else {
    collapsedTOCIndices.add(idx);
  }
  applyTOCVisibility();
}

function toggleAllTOCFold() {
  if (!tocAllFolded) {
    currentTOCNodes.forEach(n => {
      if (n.hasChildren) collapsedTOCIndices.add(n.index);
    });
    tocAllFolded = true;
  } else {
    collapsedTOCIndices.clear();
    tocAllFolded = false;
  }
  const btn = document.getElementById("toc-fold-all-btn");
  if (btn) btn.textContent = tocAllFolded ? "展开" : "折叠";
  applyTOCVisibility();
}

function filterTOC(query) {
  applyTOCVisibility();
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

function renderLoadErrorCard(data) {
  const contentEl = document.getElementById("content");
  const tocList = document.getElementById("toc-list");
  const metaEl = document.getElementById("doc-meta");
  const tocCountEl = document.getElementById("toc-count");
  const tbDoc = document.getElementById("titlebar-doc-name");

  const info = describeLoadError(data.loadErrorKind);
  const fname = data.filename || "未知文件";
  const fpath = data.filepath || "";

  if (metaEl) metaEl.textContent = "无法打开";
  if (tbDoc) {
    tbDoc.textContent = fname;
    tbDoc.title = fpath;
  }
  document.title = `${fname} - Go Markdown Reader`;
  if (tocList) tocList.innerHTML = `<li style="padding:15px; color:var(--text-muted); font-size:12px; text-align:center;">暂无目录</li>`;
  if (tocCountEl) tocCountEl.textContent = "0";

  const showDetails = (data.loadErrorKind === "other" || data.loadErrorKind === "parse") && data.loadError;
  const detailsHtml = showDetails
    ? `<details class="load-error-details"><summary>详细信息</summary><code>${escapeHtml(data.loadError)}</code></details>`
    : "";
  const pathHtml = fpath ? `<code class="load-error-path">${escapeHtml(fpath)}</code>` : "";

  contentEl.innerHTML = `
    <div class="empty-state">
      <div class="empty-icon">${info.icon}</div>
      <div class="empty-title">${escapeHtml(info.title)}</div>
      <div class="empty-desc">${escapeHtml(info.desc)}</div>
      ${pathHtml}
      ${detailsHtml}
      <div class="empty-actions" id="load-error-actions"></div>
    </div>
  `;

  const actionsEl = document.getElementById("load-error-actions");
  if (actionsEl) {
    const reopenBtn = document.createElement("button");
    reopenBtn.type = "button";
    reopenBtn.className = "btn btn-primary btn-lg";
    reopenBtn.textContent = "📂 重新选择文件";
    reopenBtn.addEventListener("click", () => openFileDialog());
    actionsEl.appendChild(reopenBtn);

    if (data.loadErrorKind === "notfound" && fpath) {
      const removeBtn = document.createElement("button");
      removeBtn.type = "button";
      removeBtn.className = "btn btn-lg";
      removeBtn.textContent = "🗑️ 从最近列表移除";
      removeBtn.addEventListener("click", () => {
        removeRecentFile(fpath);
        showToast("✓ 已从最近列表移除");
        removeBtn.disabled = true;
      });
      actionsEl.appendChild(removeBtn);
    }
  }
}

function renderWelcomeRecent() {
  const container = document.getElementById("welcome-recent");
  if (!container) return;
  const recent = Array.isArray(uiPrefsCache.recentFiles) ? uiPrefsCache.recentFiles.slice(0, 5) : [];
  if (recent.length === 0) {
    container.innerHTML = "";
    return;
  }

  const itemsHtml = recent.map(item => {
    const extMatch = (item.name || "").match(/\.[^.]+$/);
    const ext = extMatch ? extMatch[0].toLowerCase() : "";
    const p = item.path || "";
    const sepIdx = Math.max(p.lastIndexOf("\\"), p.lastIndexOf("/"));
    const parentDir = sepIdx > 0 ? p.slice(0, sepIdx) : p;
    return `
      <button type="button" class="welcome-recent-item" data-path="${escapeHtml(p)}" title="${escapeHtml(p)}">
        <span class="welcome-recent-icon">${getFileIcon(ext)}</span>
        <span class="welcome-recent-text">
          <span class="welcome-recent-name">${escapeHtml(item.name)}</span>
          <span class="welcome-recent-path">${escapeHtml(parentDir)}</span>
        </span>
      </button>
    `;
  }).join("");

  container.innerHTML = `<div class="welcome-recent-title">最近打开</div>${itemsHtml}`;

  if (!container.dataset.clickBound) {
    container.dataset.clickBound = "1";
    container.addEventListener("click", (e) => {
      const btn = e.target.closest(".welcome-recent-item[data-path]");
      if (!btn) return;
      const p = btn.getAttribute("data-path");
      if (btn.classList.contains("missing")) {
        removeRecentFile(p);
        renderWelcomeRecent();
        showToast("✓ 已从最近列表移除");
        return;
      }
      openRecentPath(p);
    });
  }

  markMissingRecent(container);
}

