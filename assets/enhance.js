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
let scrollSaveTimer = null;
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

    if (currentDocData && currentDocData.filepath && !currentDocData.isMemory) {
      const fpath = currentDocData.filepath;
      const top = Math.round(container.scrollTop);
      if (!uiPrefsCache.scrollPositions) uiPrefsCache.scrollPositions = {};
      uiPrefsCache.scrollPositions[fpath] = top;
      clearTimeout(scrollSaveTimer);
      scrollSaveTimer = setTimeout(() => {
        persistUIPrefs({ scrollPath: fpath, scrollTop: top });
      }, 250);
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
    if (!confirmDiscardEditorChanges()) return;

    const file = files[0];
    const filePath = file.path || "";

    if (filePath) {
      try {
        await rpcCall("doc.open", { mode: "path", path: filePath });
        showToast(`✓ 已加载: ${file.name}`);
        loadFile(false);
        return;
      } catch (err) {}
    }

    try {
      const buf = await file.arrayBuffer();
      await rpcCall("doc.open", {
        mode: "binary",
        name: file.name,
        dataBase64: arrayBufferToBase64(buf)
      });
      showToast(`✓ 已打开: ${file.name}`);
      loadFile(false);
    } catch (err) {
      showToast("❌ 打开文件失败: " + err.message);
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
    showToast("⚠️ 没有可复制的内容");
    return;
  }
  navigator.clipboard.writeText(md).then(() => {
    showToast("✓ 已复制 Markdown 源码");
  });
}

function copyPlainText() {
  closeAllDropdowns();
  const contentEl = document.getElementById("content");
  if (!contentEl || !currentDocData || !currentDocData.hasFile) {
    showToast("⚠️ 没有可复制的文字");
    return;
  }
  navigator.clipboard.writeText(contentEl.innerText).then(() => {
    showToast("✓ 已复制纯文本");
  });
}

async function exportStandaloneHTML() {
  closeAllDropdowns();
  if (!currentDocData || !currentDocData.html) {
    showToast("⚠️ 没有可导出的文档");
    return;
  }

  try {
    const cssParts = await Promise.all(
      ["assets/theme.css", "assets/style.css"].map(url => fetch(url).then(r => r.text()))
    );
    const cssText = cssParts.join("\n");
    const currentTheme = document.documentElement.getAttribute("data-theme") || "light";
    const currentFont = document.documentElement.getAttribute("data-font") || "sans";
    const currentWidth = document.documentElement.getAttribute("data-width") || "standard";

    const standaloneHTML = `<!DOCTYPE html>
<html lang="zh-CN" data-theme="${currentTheme}" data-font="${currentFont}" data-width="${currentWidth}">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <meta name="color-scheme" content="light dark">
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

    const saveData = await rpcCall("doc.save", {
      mode: "saveHtml",
      html: standaloneHTML,
      filename: currentDocData.filename || "document"
    });
    if (saveData.saved) {
      showToast(`✓ 已导出网页: ${saveData.path}`);
    } else if (saveData.error) {
      showToast("❌ 导出失败: " + saveData.error);
    }
    return;
  } catch (err) {
    showToast("❌ 导出失败: " + err.message);
  }
}
