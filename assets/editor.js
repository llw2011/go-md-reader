let scrollSyncEnabled = true;
let activeScrollPane = "";
let activeScrollTimer = null;
let scrollSyncRaf = 0;

function setActiveScrollDriver(pane) {
  activeScrollPane = pane;
  clearTimeout(activeScrollTimer);
  activeScrollTimer = setTimeout(() => {
    activeScrollPane = "";
  }, 450);
}

function toggleScrollSync() {
  scrollSyncEnabled = !scrollSyncEnabled;
  const btn = document.getElementById("sync-scroll-btn");
  if (btn) {
    btn.classList.toggle("active", scrollSyncEnabled);
    btn.setAttribute("aria-pressed", String(scrollSyncEnabled));
  }
  if (scrollSyncEnabled) {
    syncScrollRatio("editor", true);
  }
}

function syncScrollRatio(sourceId, force = false) {
  if (!isEditorOpen || !scrollSyncEnabled) return;
  if (!force && activeScrollPane && activeScrollPane !== sourceId) return;

  if (scrollSyncRaf) {
    cancelAnimationFrame(scrollSyncRaf);
  }
  scrollSyncRaf = requestAnimationFrame(() => {
    scrollSyncRaf = 0;
    const editorEl = document.getElementById("editor-textarea");
    const previewEl = document.getElementById("content-area");
    if (!editorEl || !previewEl) return;

    if (sourceId === "editor") {
      const nextTop = calcSyncedScrollTop(
        editorEl.scrollTop, editorEl.scrollHeight, editorEl.clientHeight,
        previewEl.scrollHeight, previewEl.clientHeight
      );
      if (Math.abs(previewEl.scrollTop - nextTop) >= 1) {
        previewEl.scrollTop = nextTop;
      }
    } else {
      const nextTop = calcSyncedScrollTop(
        previewEl.scrollTop, previewEl.scrollHeight, previewEl.clientHeight,
        editorEl.scrollHeight, editorEl.clientHeight
      );
      if (Math.abs(editorEl.scrollTop - nextTop) >= 1) {
        editorEl.scrollTop = nextTop;
      }
    }
  });
}

function initEditorScrollSync() {
  const editorEl = document.getElementById("editor-textarea");
  const previewEl = document.getElementById("content-area");
  if (editorEl && !editorEl.dataset.syncBound) {
    editorEl.dataset.syncBound = "1";
    const markEditor = () => setActiveScrollDriver("editor");
    editorEl.addEventListener("wheel", markEditor, { passive: true });
    editorEl.addEventListener("touchstart", markEditor, { passive: true });
    editorEl.addEventListener("mousedown", markEditor);
    editorEl.addEventListener("keydown", markEditor);
    editorEl.addEventListener("scroll", () => {
      syncScrollRatio("editor");
    }, { passive: true });
  }
  if (previewEl && !previewEl.dataset.syncDriverBound) {
    previewEl.dataset.syncDriverBound = "1";
    const markPreview = () => setActiveScrollDriver("preview");
    previewEl.addEventListener("wheel", markPreview, { passive: true });
    previewEl.addEventListener("touchstart", markPreview, { passive: true });
    previewEl.addEventListener("mousedown", markPreview);
    previewEl.addEventListener("keydown", markPreview);
  }
}

function toggleEditor() {
  const pane = document.getElementById("editor-pane");
  const btn = document.getElementById("edit-btn");
  const textarea = document.getElementById("editor-textarea");

  isEditorOpen = !isEditorOpen;
  pane.classList.toggle("active", isEditorOpen);
  btn.classList.toggle("active", isEditorOpen);
  btn.setAttribute("aria-pressed", String(isEditorOpen));

  if (isEditorOpen) {
    if (currentDocData && currentDocData.rawContent && !textarea.value) {
      textarea.value = currentDocData.rawContent;
    }
    textarea.focus();
    syncScrollRatio("editor", true);
  }
}

function markActiveTabDirty(dirty, content) {
  if (typeof openTabs === "undefined" || !activeTabId) return;
  const tab = openTabs.find(t => t.id === activeTabId);
  if (!tab) return;
  const changed = tab.dirty !== !!dirty;
  tab.dirty = !!dirty;
  if (typeof content === "string") {
    tab.draftContent = dirty ? content : "";
    if (!dirty) tab.rawContent = content;
  }
  if (changed && typeof renderTabBar === "function") {
    renderTabBar();
  }
}

function handleEditorInput() {
  isEditorDirty = true;
  clearTimeout(editorDebounceTimer);

  const editorEl = document.getElementById("editor-textarea");
  markActiveTabDirty(true, editorEl ? editorEl.value : undefined);

  const expectedDocSeq = currentDocData ? currentDocData.docSeq : undefined;
  const expectedPath = currentDocData ? currentDocData.filepath : "";
  const myGen = ++previewGeneration;

  editorDebounceTimer = setTimeout(async () => {
    const content = document.getElementById("editor-textarea").value;
    try {
      const data = await rpcCall("doc.preview", { content });
      if (myGen !== previewGeneration) return;
      if (currentDocData && (currentDocData.docSeq !== expectedDocSeq || currentDocData.filepath !== expectedPath)) {
        return;
      }

      if (!currentDocData) {
        currentDocData = { hasFile: true, filename: "未命名草稿.md", filepath: "(本地草稿)" };
      }
      currentDocData.rawContent = content;
      currentDocData.html = data.html;
      currentDocData.wordCount = data.wordCount;
      renderDocumentHTML(data.html, data.toc);
      syncScrollRatio("editor", true);

      const words = data.wordCount || 0;
      const estMinutes = Math.max(1, Math.round(words / 350));
      document.getElementById("doc-meta").textContent = `✏️ 编辑中 · ${words.toLocaleString()} 字 · 约 ${estMinutes} 分钟`;
    } catch (e) {}
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

async function autoFormatDocument() {
  const textarea = document.getElementById("editor-textarea");
  const rawSource = (isEditorOpen && textarea)
    ? textarea.value
    : ((currentDocData && currentDocData.rawContent) || (textarea && textarea.value) || "");

  if (!rawSource.trim()) {
    showToast("⚠️ 没有可排版的内容");
    return;
  }

  const fmtBtn = document.getElementById("format-btn");
  if (fmtBtn) fmtBtn.disabled = true;

  try {
    const data = await rpcCall("doc.format", { content: rawSource });
    if (!data.changed) {
      showToast("✓ 排版已很整齐");
      return;
    }

    if (!isEditorOpen) {
      toggleEditor();
    }

    if (textarea) {
      textarea.focus();
      textarea.setSelectionRange(0, textarea.value.length);
      let appliedViaExec = false;
      try {
        appliedViaExec = document.execCommand("insertText", false, data.content);
      } catch (e) {}
      if (!appliedViaExec || textarea.value !== data.content) {
        textarea.value = data.content;
      }
      textarea.setSelectionRange(0, 0);
      textarea.scrollTop = 0;
    }

    clearTimeout(editorDebounceTimer);
    isEditorDirty = true;
    if (!currentDocData) {
      currentDocData = { hasFile: true, filename: "未命名草稿.md", filepath: "(本地草稿)" };
    }
    currentDocData.rawContent = data.content;
    currentDocData.html = data.html;
    currentDocData.wordCount = data.wordCount;
    markActiveTabDirty(true, data.content);
    renderDocumentHTML(data.html, data.toc);

    const words = data.wordCount || 0;
    const estMinutes = Math.max(1, Math.round(words / 350));
    const metaEl = document.getElementById("doc-meta");
    if (metaEl) {
      metaEl.textContent = `✨ 已自动排版 · ${words.toLocaleString()} 字 · 约 ${estMinutes} 分钟`;
    }

    showToast("✓ 排版完成（Ctrl+Z 撤销，Ctrl+S 保存）");
  } catch (err) {
    showToast("❌ 排版失败: " + err.message);
  } finally {
    if (fmtBtn) fmtBtn.disabled = false;
  }
}

async function saveCurrentContent() {
  const textarea = document.getElementById("editor-textarea");
  const content = (isEditorOpen && textarea) ? textarea.value : (currentDocData ? currentDocData.rawContent : "");
  if (!content) {
    showToast("⚠️ 当前没有可保存的内容");
    return false;
  }

  try {
    const data = await rpcCall("doc.save", { mode: "save", content });
    if (data.saved) {
      isEditorDirty = false;
      if (typeof openTabs !== "undefined" && activeTabId) {
        const tab = openTabs.find(t => t.id === activeTabId);
        if (tab) {
          tab.dirty = false;
          tab.draftContent = "";
          tab.rawContent = content;
          if (data.path) {
            tab.isMemory = false;
            tab.path = data.path;
            tab.id = "path:" + data.path;
            activeTabId = tab.id;
          }
          if (typeof renderTabBar === "function") renderTabBar();
        }
      }
      showToast(`✓ 已保存: ${data.path}`);
      loadFile(true);
      return true;
    } else if (data.error) {
      showToast("❌ 保存失败: " + data.error);
    }
  } catch (err) {
    showToast("❌ 保存失败: " + err.message);
  }
  return false;
}

async function saveAsMarkdown() {
  closeAllDropdowns();
  const textarea = document.getElementById("editor-textarea");
  const content = (isEditorOpen && textarea && textarea.value) ? textarea.value : (currentDocData ? currentDocData.rawContent : "");
  if (!content) {
    showToast("⚠️ 没有可导出的内容");
    return;
  }

  const fname = currentDocData && currentDocData.filename ? currentDocData.filename : "document.md";
  try {
    const data = await rpcCall("doc.save", { mode: "saveAs", content, filename: fname });
    if (data.saved) {
      showToast(`✓ 已另存为: ${data.path}`);
    } else if (data.error) {
      showToast("❌ 另存为失败: " + data.error);
    }
  } catch (err) {
    showToast("❌ 另存为失败: " + err.message);
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
  if (e && e.stopPropagation) e.stopPropagation();
  const target = document.getElementById(menuId);
  if (!target) return;
  const trigger = (e && (e.currentTarget || (e.target && e.target.closest("button")))) ||
    (target.closest(".dropdown") && target.closest(".dropdown").querySelector('[aria-haspopup]'));
  const isShown = target.classList.contains("show");
  closeAllDropdowns();
  if (!isShown) {
    if (menuId === "recent-menu") {
      renderRecentMenu();
    }
    target.style.left = "";
    target.style.right = "";
    target.classList.add("show");
    const rect = target.getBoundingClientRect();
    if (rect.right > window.innerWidth - 8) {
      target.style.left = "auto";
      target.style.right = "0";
    } else if (rect.left < 8) {
      target.style.left = "0";
      target.style.right = "auto";
    }
    if (trigger) trigger.setAttribute("aria-expanded", "true");
    if (e && e.type === "click" && e.detail === 0) {
      const firstItem = target.querySelector(".dropdown-item, button:not([disabled])");
      if (firstItem) firstItem.focus();
    }
  }
}

function closeAllDropdowns() {
  document.querySelectorAll(".dropdown-menu").forEach(m => m.classList.remove("show"));
  document.querySelectorAll('[aria-haspopup]').forEach(b => b.setAttribute("aria-expanded", "false"));
}

function addRecentFile(name, path) {
  let recent = Array.isArray(uiPrefsCache.recentFiles) ? uiPrefsCache.recentFiles.slice() : [];
  recent = recent.filter(item => item.path !== path);
  recent.unshift({ name, path });
  if (recent.length > 10) recent = recent.slice(0, 10);
  uiPrefsCache.recentFiles = recent;
  renderRecentMenu();
  persistUIPrefs({ recentFiles: recent });
}

function renderRecentMenu() {
  const menu = document.getElementById("recent-menu");
  if (!menu) return;
  const recent = Array.isArray(uiPrefsCache.recentFiles) ? uiPrefsCache.recentFiles : [];
  if (recent.length === 0) {
    menu.innerHTML = `<li style="padding:10px 14px; font-size:12px; color:var(--text-muted); text-align:center;">暂无打开历史</li>`;
    return;
  }
  menu.innerHTML = recent.map(item => `
    <li class="dropdown-item" role="menuitem" tabindex="-1" data-path="${escapeHtml(item.path)}">
      <span class="dropdown-item-name">${escapeHtml(item.name)}</span>
      <span class="dropdown-item-path" title="${escapeHtml(item.path)}">${escapeHtml(item.path)}</span>
    </li>
  `).join("");
  markMissingRecent(menu);
}

function removeRecentFile(path) {
  if (!path) return;
  const recent = Array.isArray(uiPrefsCache.recentFiles) ? uiPrefsCache.recentFiles : [];
  const next = recent.filter(item => item.path !== path);
  if (next.length === recent.length) return;
  uiPrefsCache.recentFiles = next;
  renderRecentMenu();
  persistUIPrefs({ recentFiles: next });
}

async function markMissingRecent(container) {
  if (!container) return;
  const items = Array.from(container.querySelectorAll("[data-path]"));
  if (items.length === 0) return;
  const paths = items.map(el => el.getAttribute("data-path")).filter(Boolean);
  if (paths.length === 0) return;
  try {
    const res = await rpcCall("doc.exists", { paths });
    const existsMap = (res && res.exists) || {};
    items.forEach(el => {
      const p = el.getAttribute("data-path");
      if (p && existsMap[p] === false) {
        el.classList.add("missing");
        el.title = "文件已不存在，点击从列表移除";
        const pathEl = el.querySelector(".dropdown-item-path, .welcome-recent-path");
        if (pathEl) pathEl.textContent = "文件已不存在 · 点击移除";
      }
    });
  } catch (e) {}
}

let confirmResolver = null;

function resolveConfirmDialog(value) {
  const modal = document.getElementById("confirm-modal");
  if (modal) modal.classList.remove("show");
  const r = confirmResolver;
  confirmResolver = null;
  if (r) r(value);
}

function closeConfirmDialog(e) {
  if (e && e.target !== e.currentTarget) return;
  resolveConfirmDialog(null);
}

function showConfirmDialog({ title, message, buttons }) {
  if (confirmResolver) {
    resolveConfirmDialog(null);
  }
  const modal = document.getElementById("confirm-modal");
  const titleEl = document.getElementById("confirm-title");
  const msgEl = document.getElementById("confirm-message");
  const actionsEl = document.getElementById("confirm-actions");
  if (!modal || !titleEl || !msgEl || !actionsEl) {
    return Promise.resolve(null);
  }

  titleEl.textContent = title || "";
  msgEl.textContent = message || "";
  actionsEl.innerHTML = "";

  let primaryBtn = null;
  (buttons || []).forEach(b => {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = b.variant === "primary" ? "btn btn-primary" : "btn";
    btn.textContent = b.label;
    btn.addEventListener("click", () => resolveConfirmDialog(b.id));
    actionsEl.appendChild(btn);
    if (b.variant === "primary" && !primaryBtn) {
      primaryBtn = btn;
    }
  });

  if (!modal.dataset.escBound) {
    modal.dataset.escBound = "1";
    modal.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        resolveConfirmDialog(null);
      }
    });
  }

  modal.classList.add("show");
  const focusTarget = primaryBtn || actionsEl.querySelector("button");
  if (focusTarget) focusTarget.focus();

  return new Promise(resolve => {
    confirmResolver = resolve;
  });
}

async function confirmDiscardEditorChanges() {
  if (!isEditorDirty) return true;
  const choice = await showConfirmDialog({
    title: "有未保存的修改",
    message: "切换文档前要保存当前修改吗？",
    buttons: [
      { id: "save", label: "💾 保存并切换", variant: "primary" },
      { id: "discard", label: "不保存" },
      { id: "cancel", label: "取消" }
    ]
  });
  if (choice === "save") {
    return await saveCurrentContent();
  }
  if (choice === "discard") {
    isEditorDirty = false;
    return true;
  }
  return false;
}

async function openRecentPath(path) {
  closeAllDropdowns();
  if (typeof stashActiveTabState === "function") stashActiveTabState();
  try {
    await rpcCall("doc.open", { mode: "path", path });
    loadFile(false);
  } catch (err) {
    showToast("❌ 无法打开该文件: " + err.message);
  }
}

async function openFileDialog() {
  if (typeof stashActiveTabState === "function") stashActiveTabState();
  try {
    const data = await rpcCall("doc.open", { mode: "dialog" });
    if (data.opened) {
      loadFile(false);
    } else if (data.error) {
      showToast("❌ 打开文件失败: " + data.error);
    }
  } catch (err) {
    console.error("打开文件对话框失败:", err);
  }
}
