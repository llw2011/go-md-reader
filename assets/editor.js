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
    return;
  }

  try {
    const data = await rpcCall("doc.save", { mode: "save", content });
    if (data.saved) {
      isEditorDirty = false;
      showToast(`✓ 已保存: ${data.path}`);
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
}

function confirmDiscardEditorChanges() {
  if (!isEditorDirty) return true;
  return window.confirm("当前源码编辑器有未保存的修改，确认放弃修改并切换文档吗？");
}

async function openRecentPath(path) {
  closeAllDropdowns();
  if (!confirmDiscardEditorChanges()) return;
  try {
    await rpcCall("doc.open", { mode: "path", path });
    loadFile(false);
  } catch (err) {
    showToast("❌ 无法打开该文件: " + err.message);
  }
}

async function openFileDialog() {
  if (!confirmDiscardEditorChanges()) return;
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
