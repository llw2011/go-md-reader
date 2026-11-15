// Curated On-Demand MIT Addons (KaTeX & Mermaid) - 100% local offline serving once installed
let addonsCache = null;
let addonsFetchPromise = null;
let katexLoadPromise = null;
let mermaidLoadPromise = null;
let mermaidRenderSeq = 0;

async function getAddonsState(force = false) {
  if (!force && addonsCache) return addonsCache;
  if (!force && addonsFetchPromise) return addonsFetchPromise;

  addonsFetchPromise = rpcCall("addons.list")
    .then(res => {
      addonsCache = (res && res.addons) || [];
      return addonsCache;
    })
    .catch(() => addonsCache || [])
    .finally(() => {
      addonsFetchPromise = null;
    });
  return addonsFetchPromise;
}

function loadStyleOnce(href) {
  if (document.querySelector(`link[data-addon-href="${href}"]`)) {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = href;
    link.setAttribute("data-addon-href", href);
    link.onload = () => resolve();
    link.onerror = () => reject(new Error(`加载样式失败: ${href}`));
    document.head.appendChild(link);
  });
}

function loadScriptOnce(src) {
  if (document.querySelector(`script[data-addon-src="${src}"]`)) {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    const s = document.createElement("script");
    s.src = src;
    s.setAttribute("data-addon-src", src);
    s.onload = () => resolve();
    s.onerror = () => reject(new Error(`加载脚本失败: ${src}`));
    document.body.appendChild(s);
  });
}

function ensureKatexLoaded() {
  if (window.renderMathInElement) return Promise.resolve();
  if (!katexLoadPromise) {
    katexLoadPromise = (async () => {
      await loadStyleOnce("addons/katex/katex.min.css");
      await loadScriptOnce("addons/katex/katex.min.js");
      await loadScriptOnce("addons/katex/contrib/auto-render.min.js");
    })().catch(err => {
      katexLoadPromise = null;
      throw err;
    });
  }
  return katexLoadPromise;
}

function ensureMermaidLoaded() {
  if (window.mermaid) return Promise.resolve();
  if (!mermaidLoadPromise) {
    mermaidLoadPromise = loadScriptOnce("addons/mermaid/mermaid.min.js").catch(err => {
      mermaidLoadPromise = null;
      throw err;
    });
  }
  return mermaidLoadPromise;
}

function getMermaidThemeForApp() {
  const t = document.documentElement.getAttribute("data-theme") || "light";
  if (t === "dark") return "dark";
  if (t === "forest") return "forest";
  return "default";
}

async function renderMermaidBlocks(contentEl) {
  const hosts = Array.from(contentEl.querySelectorAll(".mermaid-diagram[data-mermaid-src]"));
  if (hosts.length === 0) return;

  await ensureMermaidLoaded();
  window.mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    theme: getMermaidThemeForApp()
  });

  for (const host of hosts) {
    const src = host.getAttribute("data-mermaid-src") || "";
    const id = `mmd-svg-${++mermaidRenderSeq}`;
    try {
      const { svg } = await window.mermaid.render(id, src);
      host.innerHTML = svg;
    } catch (err) {
      host.innerHTML = `<pre class="mermaid-error">Mermaid 渲染失败: ${escapeHtml(err && err.message ? err.message : String(err))}\n\n${escapeHtml(src)}</pre>`;
    }
  }
}

async function rerenderMermaidForTheme() {
  const contentEl = document.getElementById("content");
  if (!contentEl) return;
  const list = await getAddonsState();
  const mmd = list.find(a => a.id === "mermaid");
  if (mmd && mmd.installed && mmd.enabled) {
    await renderMermaidBlocks(contentEl);
  }
}

async function enhanceRichMedia() {
  const contentEl = document.getElementById("content");
  if (!contentEl) return;

  const hasMath = !!contentEl.querySelector(".math");
  const mermaidCodes = Array.from(
    contentEl.querySelectorAll("pre > code.language-mermaid, pre > code.chroma-mermaid")
  );
  const hasMermaid = mermaidCodes.length > 0;
  if (!hasMath && !hasMermaid) return;

  const list = await getAddonsState();
  const findAddon = id => list.find(a => a.id === id);
  const katexState = findAddon("katex");
  const mermaidState = findAddon("mermaid");

  const missingPrompts = [];
  if (hasMath && (!katexState || !katexState.installed)) {
    missingPrompts.push("📐 数学公式 (KaTeX · 约 0.5 MB)");
  }
  if (hasMermaid && (!mermaidState || !mermaidState.installed)) {
    missingPrompts.push("📊 Mermaid 图表 (约 5.5 MB)");
  }
  if (missingPrompts.length > 0) {
    const existingBanner = contentEl.querySelector(".addon-hint-banner");
    if (!existingBanner) {
      const banner = document.createElement("div");
      banner.className = "addon-hint-banner";
      banner.innerHTML = `
        <span>检测到文档包含 ${escapeHtml(missingPrompts.join("、"))}，当前处于零外联模式。</span>
        <button type="button" class="btn btn-primary btn-compact" onclick="showAddonsModal()">🧩 按需启用离线渲染</button>
      `;
      const header = contentEl.querySelector(".print-doc-header");
      if (header && header.nextSibling) {
        contentEl.insertBefore(banner, header.nextSibling);
      } else {
        contentEl.prepend(banner);
      }
    }
  }

  if (hasMath && katexState && katexState.installed && katexState.enabled) {
    try {
      await ensureKatexLoaded();
      window.renderMathInElement(contentEl, {
        delimiters: [
          { left: "\\[", right: "\\]", display: true },
          { left: "\\(", right: "\\)", display: false }
        ],
        throwOnError: false
      });
    } catch (err) {
      showToast("⚠️ KaTeX 渲染异常: " + err.message);
    }
  }

  if (hasMermaid && mermaidState && mermaidState.installed && mermaidState.enabled) {
    for (const codeEl of mermaidCodes) {
      const pre = codeEl.parentElement;
      if (!pre || !pre.parentNode) continue;
      const host = document.createElement("div");
      host.className = "mermaid-diagram";
      host.setAttribute("data-mermaid-src", codeEl.textContent || "");
      pre.parentNode.replaceChild(host, pre);
    }
    try {
      await renderMermaidBlocks(contentEl);
    } catch (err) {
      showToast("⚠️ Mermaid 渲染异常: " + err.message);
    }
  }
}

async function showAddonsModal() {
  const modal = document.getElementById("addons-modal");
  if (!modal) return;
  const list = await getAddonsState(true);
  renderAddonsModalList(list);
  modal.classList.add("show");
}

function closeAddonsModal(e) {
  if (e && e.target !== e.currentTarget && !e.target.classList.contains("stats-card-close")) return;
  const modal = document.getElementById("addons-modal");
  if (modal) modal.classList.remove("show");
}

function renderAddonsModalList(list) {
  const container = document.getElementById("addons-list");
  if (!container) return;

  container.innerHTML = (list || []).map(a => {
    const sizeMB = (a.totalBytes / (1024 * 1024)).toFixed(2);
    let badgeText = "未安装";
    let badgeClass = "addon-badge";
    if (a.installed && a.enabled) {
      badgeText = "已离线启用";
      badgeClass = "addon-badge addon-badge-ok";
    } else if (a.installed) {
      badgeText = "已暂停";
      badgeClass = "addon-badge addon-badge-muted";
    }

    const actions = a.installed
      ? `
        <button type="button" class="btn btn-compact" onclick="toggleAddon('${escapeHtml(a.id)}', ${!a.enabled})">${a.enabled ? "⏸ 停用" : "▶️ 启用"}</button>
        <button type="button" class="btn btn-compact" onclick="removeAddon('${escapeHtml(a.id)}')">🗑️ 清理缓存</button>
      `
      : `
        <button type="button" class="btn btn-primary btn-compact" onclick="installAddon('${escapeHtml(a.id)}')">⬇️ 下载并校验安装 (${sizeMB} MB)</button>
      `;

    return `
      <div class="addon-card" data-addon-id="${escapeHtml(a.id)}">
        <div class="addon-card-head">
          <div>
            <strong>${escapeHtml(a.name)}</strong>
            <code class="addon-ver">v${escapeHtml(a.version)} · ${escapeHtml(a.license)}</code>
          </div>
          <span class="${badgeClass}">${badgeText}</span>
        </div>
        <div class="addon-desc">${escapeHtml(a.description)}</div>
        <div class="addon-foot">
          <span class="addon-copyright">${escapeHtml(a.copyright)}</span>
          <div class="addon-actions">${actions}</div>
        </div>
        <div class="ota-progress-wrap addon-progress-wrap" id="addon-progress-${escapeHtml(a.id)}" style="display:none; margin-top:8px; margin-bottom:0;">
          <div class="ota-progress-head">
            <span class="addon-progress-phase">正在下载并校验 SHA-256...</span>
            <span class="addon-progress-stats">0%</span>
          </div>
          <div class="ota-progress-track">
            <div class="ota-progress-fill"></div>
          </div>
        </div>
      </div>
    `;
  }).join("");
}

function renderAddonProgress(p) {
  if (!p || !p.id) return;
  const wrap = document.getElementById(`addon-progress-${p.id}`);
  if (!wrap) return;
  const phaseEl = wrap.querySelector(".addon-progress-phase");
  const statsEl = wrap.querySelector(".addon-progress-stats");
  const fillEl = wrap.querySelector(".ota-progress-fill");
  wrap.style.display = "block";

  if (p.phase === "error") {
    phaseEl.textContent = "❌ 安装或校验失败";
    statsEl.textContent = "";
    return;
  }
  const doneMB = (p.bytesDone / (1024 * 1024)).toFixed(2);
  const totalMB = (p.bytesTotal / (1024 * 1024)).toFixed(2);
  if (p.phase === "ready") {
    phaseEl.textContent = "✓ SHA-256 校验通过并已离线就绪";
    statsEl.textContent = "100%";
    fillEl.classList.add("done");
    fillEl.style.width = "100%";
    return;
  }
  fillEl.classList.remove("done");
  phaseEl.textContent = `正在拉取组件 (${p.completedFiles}/${p.totalFiles})`;
  statsEl.textContent = `${p.percent}% (${doneMB} / ${totalMB} MB)`;
  fillEl.style.width = `${Math.max(2, Math.min(100, p.percent || 0))}%`;
}

async function installAddon(id) {
  const card = document.querySelector(`.addon-card[data-addon-id="${id}"]`);
  const btn = card && card.querySelector("button");
  if (btn) btn.disabled = true;

  try {
    const res = await rpcCall("addons.install", { id });
    addonsCache = (res && res.addons) || await getAddonsState(true);
    renderAddonsModalList(addonsCache);
    showToast("✓ 扩展已校验安装并在当前文档生效");
    if (currentDocData && currentDocData.hasFile) {
      loadFile(true);
    }
  } catch (err) {
    showToast("❌ 扩展安装失败: " + err.message);
    if (btn) btn.disabled = false;
  }
}

async function toggleAddon(id, enabled) {
  try {
    const res = await rpcCall("addons.toggle", { id, enabled });
    addonsCache = (res && res.addons) || [];
    renderAddonsModalList(addonsCache);
    if (currentDocData && currentDocData.hasFile) {
      loadFile(true);
    }
  } catch (err) {
    showToast("❌ 切换扩展状态失败: " + err.message);
  }
}

async function removeAddon(id) {
  try {
    const res = await rpcCall("addons.remove", { id });
    addonsCache = (res && res.addons) || [];
    renderAddonsModalList(addonsCache);
    showToast("✓ 已清理本地扩展缓存");
    if (currentDocData && currentDocData.hasFile) {
      loadFile(true);
    }
  } catch (err) {
    showToast("❌ 卸载扩展失败: " + err.message);
  }
}
