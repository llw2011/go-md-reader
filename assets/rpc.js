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
  { id: "dark", label: "🌙 深色" },
  { id: "sepia", label: "📜 羊皮纸" },
  { id: "forest", label: "🌲 护眼绿" }
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
  sidebarHidden: false,
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

function escapeQuotes(str) {
  if (!str) return "";
  return String(str).replace(/\\/g, "\\\\").replace(/'/g, "\\'").replace(/"/g, '\\"');
}

function escapeRegex(str) {
  return str.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
