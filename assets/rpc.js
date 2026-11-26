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
let loadGeneration = 0;
let previewGeneration = 0;
let autoWideModeApplied = false;

let rpcSeq = 0;
const rpcPending = new Map();

window.__rpcResolve = function (id, payload) {
  const pending = rpcPending.get(id);
  if (!pending) return;
  rpcPending.delete(id);
  pending.resolve(payload);
};

window.__rpcReject = function (id, message) {
  const pending = rpcPending.get(id);
  if (!pending) return;
  rpcPending.delete(id);
  pending.reject(new Error(message || "操作失败"));
};

function rpcCall(method, params = {}) {
  if (typeof window.__appCall !== "function") {
    return Promise.reject(new Error("WebView2 IPC 桥尚未就绪"));
  }
  return new Promise((resolve, reject) => {
    const id = ++rpcSeq;
    rpcPending.set(id, { resolve, reject });
    try {
      window.__appCall(id, method, JSON.stringify(params));
    } catch (err) {
      rpcPending.delete(id);
      reject(err);
    }
  });
}

function arrayBufferToBase64(buf) {
  const bytes = new Uint8Array(buf);
  const chunkSize = 0x8000;
  const chunks = [];
  for (let i = 0; i < bytes.length; i += chunkSize) {
    chunks.push(String.fromCharCode.apply(null, bytes.subarray(i, i + chunkSize)));
  }
  return btoa(chunks.join(""));
}

const THEMES = [
  { id: "light", label: "☀️ 浅色" },
  { id: "sepia", label: "📜 羊皮纸" },
  { id: "forest", label: "🌲 护眼绿" },
  { id: "dark", label: "🌙 深色" },
  { id: "nord", label: "🌌 暗夜蓝灰" },
  { id: "system", label: "🖥️ 跟随系统" }
];

// Toast Notification Helper
function showToast(message) {
  const container = document.getElementById("toast-container");
  if (!container) return;
  const el = document.createElement("div");
  el.className = "toast-msg";
  el.textContent = message;
  container.appendChild(el);
  const statusEl = document.getElementById("a11y-status");
  if (statusEl) statusEl.textContent = message;
  setTimeout(() => {
    if (el.parentNode) el.parentNode.removeChild(el);
  }, 2600);
}

// Sidebar, Wide Mode, Theme & Font Size Controls (persisted via RPC)
let uiPrefsCache = {
  theme: "light",
  fontSize: 16,
  fontFamily: "sans",
  contentWidth: "standard",
  sidebarHidden: false,
  sidebarWidth: 292,
  recentFiles: [],
  scrollPositions: {}
};

async function persistUIPrefs(patch) {
  try {
    await rpcCall("prefs.update", patch);
  } catch (e) {}
}

function escapeHtml(str) {
  if (!str) return "";
  return String(str).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function escapeRegex(str) {
  return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function describeLoadError(kind) {
  switch (kind) {
    case "notfound":
      return {
        icon: "🔍",
        title: "找不到这个文件",
        desc: "它可能已被移动、改名或删除。"
      };
    case "denied":
      return {
        icon: "🔒",
        title: "没有权限打开这个文件",
        desc: "文件可能正被其他程序占用，或当前账户没有读取权限。"
      };
    case "parse":
      return {
        icon: "⚠️",
        title: "文件内容无法显示",
        desc: "文件可能已损坏，或格式暂不支持。"
      };
    default:
      return {
        icon: "⚠️",
        title: "无法打开这个文件",
        desc: "请稍后重试，或换一个文件。"
      };
  }
}

function calcSyncedScrollTop(srcTop, srcScrollHeight, srcClientHeight, dstScrollHeight, dstClientHeight) {
  const srcMax = srcScrollHeight - srcClientHeight;
  const dstMax = dstScrollHeight - dstClientHeight;
  if (srcMax <= 0 || dstMax <= 0) return 0;
  const ratio = Math.max(0, Math.min(1, srcTop / srcMax));
  return Math.round(ratio * dstMax);
}

function computeTOCParents(items) {
  const list = Array.isArray(items) ? items : [];
  const out = list.map((it, idx) => ({
    level: it.level,
    id: it.id,
    title: it.title,
    index: idx,
    parentIndex: -1,
    hasChildren: false
  }));
  for (let i = 0; i < out.length; i++) {
    for (let j = i - 1; j >= 0; j--) {
      if (out[j].level < out[i].level) {
        out[i].parentIndex = j;
        out[j].hasChildren = true;
        break;
      }
    }
  }
  return out;
}

function filterCommandItems(items, query) {
  const list = Array.isArray(items) ? items : [];
  const q = String(query || "").trim().toLowerCase();
  if (!q) return list;
  return list.filter(it => {
    const hay = `${it.title || ""} ${it.keywords || ""} ${it.hint || ""}`.toLowerCase();
    return hay.includes(q);
  });
}
