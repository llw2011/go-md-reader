import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const rpcSource = fs.readFileSync(path.resolve(__dirname, "..", "assets", "rpc.js"), "utf8");

function loadRpcSandbox(overrides = {}) {
  const windowObj = {};
  const sandbox = {
    window: windowObj,
    document: { getElementById: () => null },
    btoa: (bin) => Buffer.from(bin, "binary").toString("base64"),
    setTimeout,
    clearTimeout,
    Uint8Array,
    Map,
    Promise,
    Error,
    JSON,
    String,
    ...overrides
  };
  sandbox.window = windowObj;
  const ctx = vm.createContext(sandbox);
  vm.runInContext(rpcSource, ctx, { filename: "assets/rpc.js" });

  return {
    window: windowObj,
    rpcCall: (method, params) => vm.runInContext(`rpcCall`, ctx)(method, params),
    pendingSize: () => vm.runInContext(`rpcPending.size`, ctx),
    escapeHtml: vm.runInContext(`escapeHtml`, ctx),
    escapeRegex: vm.runInContext(`escapeRegex`, ctx),
    arrayBufferToBase64: vm.runInContext(`arrayBufferToBase64`, ctx)
  };
}

test("rpcCall rejects when WebView2 bridge is not ready", async () => {
  const env = loadRpcSandbox();
  await assert.rejects(() => env.rpcCall("doc.get"), /WebView2 IPC/);
  assert.equal(env.pendingSize(), 0);
});

test("rpcCall assigns monotonic ids and resolves out-of-order responses accurately", async () => {
  const env = loadRpcSandbox();
  const dispatched = [];
  env.window.__appCall = (id, method, paramsJSON) => {
    dispatched.push({ id, method, params: JSON.parse(paramsJSON) });
  };

  const p1 = env.rpcCall("doc.get", { a: 1 });
  const p2 = env.rpcCall("doc.preview", { content: "# hi" });
  const p3 = env.rpcCall("prefs.get");

  assert.equal(env.pendingSize(), 3);
  assert.deepEqual(dispatched, [
    { id: 1, method: "doc.get", params: { a: 1 } },
    { id: 2, method: "doc.preview", params: { content: "# hi" } },
    { id: 3, method: "prefs.get", params: {} }
  ]);

  // Out-of-order resolution + rejection
  env.window.__rpcResolve(2, { html: "<h1>hi</h1>" });
  env.window.__rpcReject(3, "权限不足");
  env.window.__rpcResolve(1, { filename: "a.md" });

  assert.deepEqual(await p2, { html: "<h1>hi</h1>" });
  await assert.rejects(() => p3, { message: "权限不足" });
  assert.deepEqual(await p1, { filename: "a.md" });
  assert.equal(env.pendingSize(), 0);
});

test("duplicate or unknown resolve/reject calls are safe no-ops and empty message falls back", async () => {
  const env = loadRpcSandbox();
  env.window.__appCall = () => {};

  const p1 = env.rpcCall("doc.open", { path: "a.md" });
  assert.equal(env.pendingSize(), 1);

  env.window.__rpcResolve(999, { ignored: true });
  env.window.__rpcReject(999, "ignored");
  assert.equal(env.pendingSize(), 1);

  env.window.__rpcReject(1, "");
  // Duplicate resolve after settle must be ignored
  env.window.__rpcResolve(1, { stale: true });

  await assert.rejects(() => p1, { message: "操作失败" });
  assert.equal(env.pendingSize(), 0);
});

test("rpcCall cleans rpcPending map when __appCall throws synchronously", async () => {
  const env = loadRpcSandbox();
  env.window.__appCall = () => {
    throw new Error("IPC disconnected");
  };

  await assert.rejects(() => env.rpcCall("doc.save", {}), /IPC disconnected/);
  assert.equal(env.pendingSize(), 0);
});

test("rpc.js pure helpers escape HTML/regex and encode large binary buffers", () => {
  const env = loadRpcSandbox();

  assert.equal(env.escapeHtml(`<a href="x&y">`), `&lt;a href=&quot;x&amp;y&quot;&gt;`);
  assert.equal(env.escapeHtml(null), "");
  assert.equal(env.escapeRegex("a.b+(c)?[0]$"), "a\\.b\\+\\(c\\)\\?\\[0\\]\\$");

  const buf = new Uint8Array(70000);
  for (let i = 0; i < buf.length; i++) buf[i] = i & 0xff;
  const b64 = env.arrayBufferToBase64(buf.buffer);
  assert.equal(Buffer.from(b64, "base64").compare(Buffer.from(buf)), 0);
});
