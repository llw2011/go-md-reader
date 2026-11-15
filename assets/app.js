document.addEventListener("DOMContentLoaded", async () => {
  await initReadingPrefs();
  initWindowChrome();
  initDragDrop();
  initShortcuts();
  initToolbarKeyboard();
  initScrollProgress();
  initBackToTop();
  initLightboxWheel();
  initDelegatedLists();
  loadFile();
  renderRecentMenu();

  window.addEventListener("click", (e) => {
    if (!e.target.closest(".dropdown")) {
      closeAllDropdowns();
    }
  });
});

// Frameless Custom In-App Window Controls & Native Drag/Resize Bridge
let isWindowMaximized = false;

window.__syncWindowMaximized = function (max) {
  isWindowMaximized = !!max;
  document.body.classList.toggle("window-maximized", isWindowMaximized);
  const maxIcon = document.getElementById("win-max-icon");
  const restoreIcon = document.getElementById("win-restore-icon");
  const maxBtn = document.getElementById("win-max-btn");
  if (maxIcon && restoreIcon) {
    maxIcon.style.display = isWindowMaximized ? "none" : "block";
    restoreIcon.style.display = isWindowMaximized ? "block" : "none";
  }
  if (maxBtn) {
    maxBtn.setAttribute("aria-label", isWindowMaximized ? "还原窗口" : "最大化");
    maxBtn.title = isWindowMaximized ? "向下还原" : "最大化";
  }
};

function winMinimize() {
  if (typeof window.__winMinimize === "function") {
    window.__winMinimize();
  }
}

async function winToggleMaximize() {
  if (typeof window.__winToggleMaximize === "function") {
    const nextMax = await window.__winToggleMaximize();
    window.__syncWindowMaximized(!!nextMax);
  }
}

function winClose() {
  if (typeof window.__winClose === "function") {
    window.__winClose();
  } else {
    window.close();
  }
}

function getResizeHitDirection(e) {
  if (isWindowMaximized) return 0;
  const edge = 6;
  const w = window.innerWidth;
  const h = window.innerHeight;
  const left = e.clientX <= edge;
  const right = e.clientX >= w - edge;
  const top = e.clientY <= edge;
  const bottom = e.clientY >= h - edge;

  if (top && left) return 13;     // HTTOPLEFT
  if (top && right) return 14;    // HTTOPRIGHT
  if (bottom && left) return 16;  // HTBOTTOMLEFT
  if (bottom && right) return 17; // HTBOTTOMRIGHT
  if (left) return 10;            // HTLEFT
  if (right) return 11;           // HTRIGHT
  if (top) return 12;             // HTTOP
  if (bottom) return 15;          // HTBOTTOM
  return 0;
}

const RESIZE_CURSORS = {
  10: "ew-resize",
  11: "ew-resize",
  12: "ns-resize",
  13: "nwse-resize",
  14: "nesw-resize",
  15: "ns-resize",
  16: "nesw-resize",
  17: "nwse-resize",
};

function initWindowChrome() {
  if (typeof window.__winIsMaximized === "function") {
    window.__winIsMaximized().then(max => window.__syncWindowMaximized(!!max)).catch(() => {});
  }

  let activeEdgeCursor = "";
  document.addEventListener("pointermove", (e) => {
    const dir = getResizeHitDirection(e);
    const nextCursor = RESIZE_CURSORS[dir] || "";
    if (nextCursor !== activeEdgeCursor) {
      activeEdgeCursor = nextCursor;
      document.documentElement.style.cursor = nextCursor;
    }
  });

  document.addEventListener("mousedown", (e) => {
    if (e.button !== 0) return;
    const dir = getResizeHitDirection(e);
    if (dir && typeof window.__winStartResize === "function") {
      e.preventDefault();
      window.__winStartResize(dir);
    }
  }, true);

  const titlebar = document.getElementById("app-titlebar");
  if (!titlebar) return;

  let lastClickTime = 0;
  let lastClickX = 0;
  let lastClickY = 0;

  titlebar.addEventListener("mousedown", (e) => {
    if (e.button !== 0) return;
    if (getResizeHitDirection(e)) return;
    if (e.target.closest(".no-drag, button, a, input, select, textarea")) return;

    const now = Date.now();
    const isDouble = now - lastClickTime < 420 && Math.hypot(e.screenX - lastClickX, e.screenY - lastClickY) <= 6;
    if (isDouble) {
      lastClickTime = 0;
      e.preventDefault();
      winToggleMaximize();
      return;
    }
    lastClickTime = now;
    lastClickX = e.screenX;
    lastClickY = e.screenY;

    if (typeof window.__winStartDrag === "function") {
      e.preventDefault();
      window.__winStartDrag();
    }
  });
}

// Single tab-stop roving focus fallback for toolbars when native focusgroup is unsupported
function initToolbarKeyboard() {
  if ("focusGroup" in HTMLElement.prototype) return;
  const bar = document.querySelector('.toolbar[role="toolbar"]');
  if (!bar) return;
  const getItems = () => Array.from(bar.querySelectorAll('button:not([disabled])')).filter(el => el.offsetParent !== null && !el.closest('#search-container') && !el.closest('.dropdown-menu'));
  const items = getItems();
  items.forEach((b, i) => { b.tabIndex = i === 0 ? 0 : -1; });

  bar.addEventListener("focusin", ({ target }) => {
    const cur = getItems();
    if (cur.includes(target)) {
      cur.forEach(b => { b.tabIndex = b === target ? 0 : -1; });
    }
  });

  bar.addEventListener("keydown", (e) => {
    const cur = getItems();
    const idx = cur.indexOf(document.activeElement);
    if (idx === -1) return;
    const rtl = getComputedStyle(bar).direction === "rtl";
    const steps = { ArrowRight: rtl ? -1 : 1, ArrowLeft: rtl ? 1 : -1 };
    let next = -1;
    if (e.key in steps) {
      next = (idx + steps[e.key] + cur.length) % cur.length;
    } else if (e.key === "Home") {
      next = 0;
    } else if (e.key === "End") {
      next = cur.length - 1;
    }
    if (next !== -1) {
      e.preventDefault();
      cur[next].focus();
    }
  });
}

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

  document.querySelectorAll('[aria-haspopup="menu"]').forEach(trigger => {
    trigger.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown") {
        e.preventDefault();
        const dropdown = trigger.closest(".dropdown");
        const menu = dropdown && dropdown.querySelector(".dropdown-menu");
        if (!menu) return;
        if (!menu.classList.contains("show")) {
          toggleDropdown(menu.id, e);
        }
        const first = menu.querySelector(".dropdown-item");
        if (first) first.focus();
      }
    });
  });

  document.querySelectorAll(".dropdown-menu").forEach(menu => {
    menu.addEventListener("keydown", (e) => {
      const items = Array.from(menu.querySelectorAll(".dropdown-item"));
      if (items.length === 0) return;
      const idx = items.indexOf(document.activeElement);
      if (e.key === "ArrowDown") {
        e.preventDefault();
        items[(idx + 1) % items.length].focus();
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        items[(idx - 1 + items.length) % items.length].focus();
      } else if (e.key === "Home") {
        e.preventDefault();
        items[0].focus();
      } else if (e.key === "End") {
        e.preventDefault();
        items[items.length - 1].focus();
      } else if ((e.key === "Enter" || e.key === " ") && idx !== -1) {
        e.preventDefault();
        items[idx].click();
      } else if (e.key === "Escape") {
        e.preventDefault();
        closeAllDropdowns();
        const trigger = menu.closest(".dropdown") && menu.closest(".dropdown").querySelector('[aria-haspopup="menu"]');
        if (trigger) trigger.focus();
      }
    });
  });

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

// Backend push event receiver (delivered via WebView2 Eval instead of HTTP SSE)
window.__onBackendEvent = function (event, payload) {
  if (event === "reload") {
    loadFile(true);
  } else if (event === "open-doc") {
    loadFile(false);
  } else if (event === "open-ota") {
    showOTAModal();
  } else if (event === "ota-progress") {
    renderOTAProgress(payload);
  } else if (event === "addon-progress") {
    renderAddonProgress(payload);
  }
};

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
    } else if (e.ctrlKey && e.shiftKey && e.key.toLowerCase() === "f") {
      e.preventDefault();
      autoFormatDocument();
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
      closeAddonsModal();
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
