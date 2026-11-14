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

// File Associations & Notepad Replacement Settings Modal
let currentAssocCatalog = [];
let currentNotepadReplaced = false;

function renderNotepadStatus(st) {
  const txtEl = document.getElementById("notepad-status-text");
  const btn = document.getElementById("btn-notepad-toggle");
  currentNotepadReplaced = !!(st && st.replaced);
  if (!txtEl || !btn) return;

  if (currentNotepadReplaced) {
    const scope = st.systemWide ? "系统级 + 当前用户已接管" : "当前用户已接管";
    txtEl.textContent = `✓ 已替换 (${scope}) — 可随时一键恢复原生记事本`;
    txtEl.style.color = "#10b981";
    btn.textContent = "🔄 恢复系统记事本";
    btn.className = "btn";
  } else {
    txtEl.textContent = "未替换（当前使用 Windows 原生记事本）";
    txtEl.style.color = "var(--text-muted)";
    btn.textContent = "📝 替换记事本";
    btn.className = "btn btn-primary";
  }
}

async function toggleNotepadReplacement() {
  const btn = document.getElementById("btn-notepad-toggle");
  const wantReplace = !currentNotepadReplaced;
  if (wantReplace) {
    const confirmed = window.confirm(
      "替换 Windows 记事本将写入系统级 IFEO 注册表项（需 UAC 管理员授权）并接管 notepad.exe 启动。\n卸载或再次点击可随时恢复默认记事本。确定继续吗？"
    );
    if (!confirmed) return;
  }
  if (btn) btn.disabled = true;
  try {
    const data = await rpcCall("assoc.notepad.set", { replace: wantReplace });
    renderNotepadStatus(data.status);
    showToast(wantReplace ? "✓ 已将系统记事本替换为 Go Markdown Reader" : "✓ 已恢复 Windows 系统默认记事本");
  } catch (err) {
    showToast("❌ 操作记事本设置异常: " + err.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function showAssocModal() {
  const modal = document.getElementById("assoc-modal");
  const container = document.getElementById("assoc-list-container");
  const ctxCheckbox = document.getElementById("assoc-ctx-menu");
  if (!modal || !container) return;

  try {
    const [data, trayData, npData] = await Promise.all([
      rpcCall("assoc.get"),
      rpcCall("tray.get"),
      rpcCall("assoc.notepad.get")
    ]);
    currentAssocCatalog = data.extensions || [];
    ctxCheckbox.checked = !!data.contextMenu;

    const minTrayCb = document.getElementById("pref-minimize-tray");
    if (minTrayCb && trayData) {
      minTrayCb.checked = !!trayData.minimizeToTray;
    }

    if (npData && npData.status) {
      renderNotepadStatus(npData.status);
    }

    container.innerHTML = currentAssocCatalog.map(item => `
      <label class="stats-item choice-card">
        <input type="checkbox" class="assoc-ext-cb" value="${escapeHtml(item.ext)}" data-cat="${escapeHtml(item.category)}" ${item.isAssociated ? "checked" : ""}>
        <div style="overflow:hidden;">
          <div class="choice-title">${escapeHtml(item.ext)}</div>
          <div class="choice-desc">${escapeHtml(item.name)}</div>
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
    await Promise.all([
      rpcCall("assoc.apply", { extensions: selectedExts, contextMenu }),
      rpcCall("tray.set", { minimizeToTray })
    ]);
    closeAssocModal();
    showToast(`✓ 已保存设置并应用 ${selectedExts.length} 项文件格式关联！`);
  } catch (err) {
    showToast("❌ 关联设置异常: " + err.message);
  }
}

// Online Update Management (with real percentage progress & idempotent guards)
let otaCheckInFlight = false;
let otaDownloadInFlight = false;
let otaApplyInFlight = false;
let otaKnownTotalBytes = 0;

function formatBytesMB(bytes) {
  if (!bytes || bytes <= 0) return "0 MB";
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function resetOTAProgress() {
  otaKnownTotalBytes = 0;
  const wrap = document.getElementById("ota-progress-wrap");
  const fill = document.getElementById("ota-progress-fill");
  const track = document.getElementById("ota-progress-track");
  const phaseEl = document.getElementById("ota-progress-phase");
  const statsEl = document.getElementById("ota-progress-stats");
  if (wrap) wrap.style.display = "none";
  if (fill) {
    fill.classList.remove("done", "indeterminate");
    fill.style.width = "0%";
  }
  if (track) track.setAttribute("aria-valuenow", "0");
  if (phaseEl) phaseEl.textContent = "正在准备下载...";
  if (statsEl) statsEl.textContent = "0%";
}

function renderOTAProgress(progress) {
  if (!progress) return;
  const wrap = document.getElementById("ota-progress-wrap");
  const fill = document.getElementById("ota-progress-fill");
  const track = document.getElementById("ota-progress-track");
  const phaseEl = document.getElementById("ota-progress-phase");
  const statsEl = document.getElementById("ota-progress-stats");
  if (!wrap || !fill || !phaseEl || !statsEl) return;

  wrap.style.display = "block";
  const pct = typeof progress.percent === "number" ? progress.percent : -1;
  const downloaded = progress.bytesDownloaded || 0;
  if (progress.totalBytes && progress.totalBytes > 0) {
    otaKnownTotalBytes = progress.totalBytes;
  }
  const total = (progress.totalBytes && progress.totalBytes > 0) ? progress.totalBytes : otaKnownTotalBytes;

  fill.classList.remove("done", "indeterminate");

  if (progress.phase === "ready") {
    fill.classList.add("done");
    fill.style.width = "100%";
    if (track) track.setAttribute("aria-valuenow", "100");
    phaseEl.textContent = progress.reusedCache ? "已命中本地校验缓存 ✓" : "下载与校验完成 ✓";
    statsEl.textContent = total > 0 ? `100% (${formatBytesMB(total)})` : "100%";
    return;
  }

  if (progress.phase === "verifying") {
    fill.style.width = "100%";
    if (track) track.setAttribute("aria-valuenow", "100");
    phaseEl.textContent = "正在校验 SHA-256 完整性...";
    statsEl.textContent = total > 0 ? `100% (${formatBytesMB(downloaded)})` : "100%";
    return;
  }

  if (progress.phase === "error") {
    phaseEl.textContent = "下载中断，可重试（支持断点重下）";
    statsEl.textContent = pct >= 0 ? `${pct}%` : formatBytesMB(downloaded);
    return;
  }

  phaseEl.textContent = "正在下载更新包...";
  if (pct >= 0 && total > 0) {
    const clamped = Math.max(0, Math.min(100, pct));
    fill.style.width = `${clamped}%`;
    if (track) track.setAttribute("aria-valuenow", String(clamped));
    statsEl.textContent = `${clamped}% (${formatBytesMB(downloaded)} / ${formatBytesMB(total)})`;
  } else {
    fill.classList.add("indeterminate");
    if (track) track.removeAttribute("aria-valuenow");
    statsEl.textContent = formatBytesMB(downloaded);
  }
}

function showStagedReadyUI(st) {
  const applyBtn = document.getElementById("btn-ota-apply");
  const dlBtn = document.getElementById("btn-ota-download");
  const assetInfoEl = document.getElementById("ota-asset-info");
  const verifyText = st.verifiedSha256 ? "SHA-256 校验通过 ✓" : "已完成校验 ✓";
  const cacheTag = st.reusedCache ? " (本地缓存)" : "";
  if (assetInfoEl) {
    assetInfoEl.textContent = `就绪: ${st.assetName} · ${verifyText}${cacheTag}`;
  }
  if (dlBtn) dlBtn.style.display = "none";
  if (applyBtn) applyBtn.style.display = "inline-flex";
}

async function showOTAModal() {
  const modal = document.getElementById("ota-modal");
  if (!modal) return;

  try {
    const cfg = await rpcCall("ota.config.get");
    applyOTAConfigToUI(cfg);
  } catch (e) {}
  modal.classList.add("show");
}

function applyOTAConfigToUI(cfg) {
  const verEl = document.getElementById("ota-current-ver");
  if (verEl) verEl.textContent = (cfg && cfg.currentVersion) || "-";
  const preEl = document.getElementById("ota-prerelease");
  if (preEl) preEl.checked = !!(cfg && cfg.includePrerelease);
}

function closeOTAModal(e) {
  if (e && e.target !== e.currentTarget && !e.target.classList.contains("stats-card-close")) return;
  const modal = document.getElementById("ota-modal");
  if (modal) modal.classList.remove("show");
}

async function saveOTAPrerelease() {
  const preEl = document.getElementById("ota-prerelease");
  const includePrerelease = !!(preEl && preEl.checked);
  try {
    const data = await rpcCall("ota.config.save", { includePrerelease });
    applyOTAConfigToUI(data.config);
    return true;
  } catch (err) {
    showToast("❌ 保存更新偏好失败: " + err.message);
    return false;
  }
}

async function checkOTAUpdate() {
  if (otaCheckInFlight || otaDownloadInFlight) return;
  otaCheckInFlight = true;

  const btn = document.getElementById("btn-ota-check");
  const resultBox = document.getElementById("ota-result-box");
  const statusLine = document.getElementById("ota-status-line");
  const notesEl = document.getElementById("ota-release-notes");
  const assetInfoEl = document.getElementById("ota-asset-info");
  const dlBtn = document.getElementById("btn-ota-download");
  const applyBtn = document.getElementById("btn-ota-apply");

  btn.disabled = true;
  btn.textContent = "⏳ 正在检查最新版本...";
  dlBtn.style.display = "none";
  applyBtn.style.display = "none";
  resetOTAProgress();

  try {
    const saved = await saveOTAPrerelease();
    if (!saved) return;

    const data = await rpcCall("ota.check");
    resultBox.style.display = "block";

    const r = data.result;
    notesEl.style.display = r.releaseNotes ? "block" : "none";
    notesEl.textContent = r.releaseNotes || "";

    if (r.hasUpdate) {
      statusLine.textContent = `🎉 发现新版本: ${r.latestVersion} (${r.releaseName || ""})`;
      statusLine.style.color = "#10b981";
      if (data.staged) {
        renderOTAProgress({
          phase: "ready",
          percent: 100,
          bytesDownloaded: r.asset ? r.asset.size : 0,
          totalBytes: r.asset ? r.asset.size : 0,
          reusedCache: true
        });
        showStagedReadyUI(data.staged);
      } else if (r.asset) {
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
    statusLine.textContent = "❌ 检查失败: " + err.message;
    statusLine.style.color = "#ef4444";
    notesEl.style.display = "none";
    assetInfoEl.textContent = "";
  } finally {
    otaCheckInFlight = false;
    btn.disabled = false;
    btn.textContent = "🔍 检查最新版本";
  }
}

async function downloadOTAUpdate() {
  if (otaDownloadInFlight || otaApplyInFlight) return;
  otaDownloadInFlight = true;

  const dlBtn = document.getElementById("btn-ota-download");
  const checkBtn = document.getElementById("btn-ota-check");
  const applyBtn = document.getElementById("btn-ota-apply");

  dlBtn.disabled = true;
  if (checkBtn) checkBtn.disabled = true;
  dlBtn.textContent = "⏳ 正在下载与校验...";
  renderOTAProgress({ phase: "downloading", percent: 0, bytesDownloaded: 0, totalBytes: 0 });

  try {
    const data = await rpcCall("ota.download");
    const st = data.staged;
    renderOTAProgress({ phase: "ready", percent: 100, reusedCache: !!st.reusedCache });
    showStagedReadyUI(st);
    showToast(st.reusedCache ? "✓ 已复用本地校验缓存，可直接安装重启" : "✓ 更新包已下载并校验完成，可点击安装重启");
  } catch (err) {
    renderOTAProgress({ phase: "error", percent: 0, bytesDownloaded: 0, totalBytes: 0 });
    showToast("❌ 下载异常: " + err.message);
  } finally {
    otaDownloadInFlight = false;
    dlBtn.disabled = false;
    if (checkBtn) checkBtn.disabled = false;
    dlBtn.textContent = "⬇️ 下载并校验更新";
    if (applyBtn && applyBtn.style.display !== "none") {
      dlBtn.style.display = "none";
    }
  }
}

async function applyOTAUpdate() {
  if (otaApplyInFlight) return;
  otaApplyInFlight = true;

  const applyBtn = document.getElementById("btn-ota-apply");
  applyBtn.disabled = true;
  applyBtn.textContent = "⏳ 正在替换并重启...";
  try {
    await rpcCall("ota.apply");
  } catch (err) {
    otaApplyInFlight = false;
    showToast("❌ 应用更新异常: " + err.message);
    applyBtn.disabled = false;
    applyBtn.textContent = "🚀 立即安装并重启";
  }
}

async function initReadingPrefs() {
  try {
    const data = await rpcCall("prefs.get");
    if (data) uiPrefsCache = { ...uiPrefsCache, ...data };
  } catch (e) {}

  setTheme(uiPrefsCache.theme || "light", false);

  const savedFont = uiPrefsCache.fontSize || 16;
  if (savedFont >= 12 && savedFont <= 26) {
    currentFontSize = savedFont;
    document.documentElement.style.setProperty("--content-font-size", `${currentFontSize}px`);
  }

  const sidebar = document.getElementById("sidebar");
  const sidebarBtn = document.getElementById("sidebar-btn");
  if (sidebar && uiPrefsCache.sidebarHidden) {
    sidebar.classList.add("hidden");
  }
  if (sidebarBtn) {
    const isVisible = !uiPrefsCache.sidebarHidden;
    sidebarBtn.setAttribute("aria-pressed", String(isVisible));
    sidebarBtn.classList.toggle("active", isVisible);
  }
}

function toggleSidebar() {
  const sidebar = document.getElementById("sidebar");
  const sidebarBtn = document.getElementById("sidebar-btn");
  const isHidden = sidebar.classList.toggle("hidden");
  if (sidebarBtn) {
    sidebarBtn.setAttribute("aria-pressed", String(!isHidden));
    sidebarBtn.classList.toggle("active", !isHidden);
  }
  uiPrefsCache.sidebarHidden = isHidden;
  persistUIPrefs({ sidebarHidden: isHidden });
}

function applyWideMode(isWide, isAuto = false) {
  autoWideModeApplied = isAuto && isWide;
  document.body.classList.toggle("wide-mode", isWide);
  const btn = document.getElementById("wide-btn");
  if (btn) {
    btn.classList.toggle("active", isWide);
    btn.setAttribute("aria-pressed", String(isWide));
  }
}

function toggleWideMode(forceState) {
  const isWide = typeof forceState === "boolean" ? forceState : !document.body.classList.contains("wide-mode");
  applyWideMode(isWide, false);
}

function cycleTheme() {
  const current = document.documentElement.getAttribute("data-theme") || "light";
  const idx = THEMES.findIndex(t => t.id === current);
  const next = THEMES[(idx + 1) % THEMES.length];
  setTheme(next.id, true);
  showToast(`已切换至: ${next.label}模式`);
}

function setTheme(themeId, save = true) {
  const found = THEMES.find(t => t.id === themeId) || THEMES[0];
  document.documentElement.setAttribute("data-theme", found.id);
  uiPrefsCache.theme = found.id;
  if (typeof window.__winSetThemeDark === "function") {
    window.__winSetThemeDark(found.id === "dark");
  }
  if (save) {
    persistUIPrefs({ theme: found.id });
  }
  const icon = document.getElementById("theme-icon");
  if (icon) icon.textContent = found.label;
}

let currentFontSize = 16;
function adjustFontSize(delta) {
  if (delta === 0) currentFontSize = 16;
  else currentFontSize = Math.min(26, Math.max(12, currentFontSize + delta));
  document.documentElement.style.setProperty("--content-font-size", `${currentFontSize}px`);
  uiPrefsCache.fontSize = currentFontSize;
  persistUIPrefs({ fontSize: currentFontSize });
}
