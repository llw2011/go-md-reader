import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const rpcSource = fs.readFileSync(path.resolve(__dirname, "..", "assets", "rpc.js"), "utf8");
const addonsSource = fs.readFileSync(path.resolve(__dirname, "..", "assets", "addons.js"), "utf8");

function loadAddonsSandbox() {
  const attrs = { "data-theme": "dark" };
  const windowObj = {};
  const ctx = vm.createContext({
    window: windowObj,
    document: {
      documentElement: { getAttribute: (k) => attrs[k] || null },
      getElementById: () => null,
      querySelector: () => null,
      head: { appendChild: () => {} },
      body: { appendChild: () => {} },
      createElement: () => ({ style: {}, setAttribute: () => {} })
    },
    btoa: (bin) => Buffer.from(bin, "binary").toString("base64"),
    setTimeout,
    clearTimeout,
    Uint8Array,
    Map,
    Promise,
    Error,
    JSON,
    String,
    Array,
    Math
  });
  vm.runInContext(rpcSource, ctx, { filename: "assets/rpc.js" });
  vm.runInContext(addonsSource, ctx, { filename: "assets/addons.js" });
  return { ctx, attrs };
}

test("getMermaidThemeForApp maps app themes accurately", () => {
  const { ctx, attrs } = loadAddonsSandbox();
  const fn = vm.runInContext("getMermaidThemeForApp", ctx);

  attrs["data-theme"] = "dark";
  assert.equal(fn(), "dark");

  attrs["data-theme"] = "forest";
  assert.equal(fn(), "forest");

  attrs["data-theme"] = "sepia";
  assert.equal(fn(), "default");

  attrs["data-theme"] = "light";
  assert.equal(fn(), "default");

  attrs["data-theme"] = "nord";
  assert.equal(fn(), "dark");

  attrs["data-theme"] = "system";
  ctx.window.matchMedia = () => ({ matches: true });
  assert.equal(fn(), "dark");
  ctx.window.matchMedia = () => ({ matches: false });
  assert.equal(fn(), "default");
});

test("renderAddonProgress formats phase, byte MB stats, and percentage bar cleanly", () => {
  const { ctx } = loadAddonsSandbox();
  const phaseEl = { textContent: "" };
  const statsEl = { textContent: "" };
  const fillEl = { style: { width: "0%" }, classList: { classes: new Set(), add(c) { this.classes.add(c); }, remove(c) { this.classes.delete(c); } } };
  const wrap = {
    style: { display: "none" },
    querySelector(sel) {
      if (sel === ".addon-progress-phase") return phaseEl;
      if (sel === ".addon-progress-stats") return statsEl;
      if (sel === ".ota-progress-fill") return fillEl;
      return null;
    }
  };
  ctx.document.getElementById = (id) => (id === "addon-progress-katex" ? wrap : null);

  const renderProgress = vm.runInContext("renderAddonProgress", ctx);
  renderProgress({ id: "katex", phase: "downloading", completedFiles: 10, totalFiles: 23, bytesDone: 524288, bytesTotal: 1048576, percent: 50 });
  assert.equal(wrap.style.display, "block");
  assert.match(phaseEl.textContent, /10\/23/);
  assert.match(statsEl.textContent, /50%/);
  assert.equal(fillEl.style.width, "50%");

  renderProgress({ id: "katex", phase: "ready", completedFiles: 23, totalFiles: 23, bytesDone: 1048576, bytesTotal: 1048576, percent: 100 });
  assert.equal(statsEl.textContent, "100%");
  assert.equal(fillEl.classList.classes.has("done"), true);
});
