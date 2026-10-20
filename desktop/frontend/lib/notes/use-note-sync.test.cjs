/* eslint-disable @typescript-eslint/no-require-imports -- Matches the existing Node CJS regression harness. */
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const ts = require("typescript");

/** A controllable network response keeps synchronization pending while local actions are exercised. */
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

/** Render the workspace with persistent hook slots and mocked Wails calls, without a browser. */
function workspace() {
  const slots = [];
  let cursor = 0;
  const errors = [];
  let network = deferred();
  const calls = { flush: 0, sync: 0, open: 0 };
  let flushError = null;
  const doc = {
    note: null, text: "", dirty: false, error: "",
    flush: async () => { calls.flush++; if (flushError) throw flushError; },
    open: async () => { calls.open++; },
  };
  const react = {
    useState: (initial) => {
      const index = cursor++;
      if (!(index in slots)) slots[index] = initial;
      return [slots[index], (value) => { slots[index] = typeof value === "function" ? value(slots[index]) : value; }];
    },
    useRef: (initial) => {
      const index = cursor++;
      if (!(index in slots)) slots[index] = { current: initial };
      return slots[index];
    },
    useCallback: (fn) => fn,
    useEffect: () => {},
  };
  const noteApi = {
    bind: async () => ({ id: "vault" }),
    sync: async () => { calls.sync++; return network.promise; },
  };
  const ui = new Proxy({}, { get: (_target, key) => key });
  const modules = {
    react,
    "react/jsx-runtime": { jsx: (type, props) => ({ type, props }), jsxs: (type, props) => ({ type, props }), Fragment: "Fragment" },
    sonner: { toast: { error: (value) => errors.push(value) } },
    "react-resizable-panels": { usePanelRef: () => ({ current: null }) },
    "@/lib/api": { api: { listAccounts: async () => [{ id: "account", isPersonal: true }] }, errorMessage: (err) => err.message },
    "@/lib/notes/api": { noteApi, noteEvents: {} },
    "@/lib/notes/use-note-document": { useNoteDocument: () => doc },
    "@/lib/i18n": { t: (value) => value },
    "@/components/locale-provider": { useI18n: () => {} },
  };
  /** Transpile production sources and resolve only explicit dependencies against this harness. */
  function load(relative) {
    const file = path.resolve(__dirname, relative);
    const output = ts.transpileModule(fs.readFileSync(file, "utf8"), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX },
    }).outputText;
    const mod = { exports: {} };
    vm.runInNewContext(output, {
      module: mod, exports: mod.exports,
      require: (name) => modules[name] ?? ui,
      localStorage: { setItem: () => {} },
    });
    return mod.exports;
  }
  modules["@/lib/notes/use-note-sync"] = load("use-note-sync.ts");
  const { NotesShell } = load("../../components/notes/notes-shell.tsx");
  return {
    calls, errors, get network() { return network; },
    nextSync: () => { network = deferred(); },
    failFlush: (err) => { flushError = err; },
    render: () => { cursor = 0; return NotesShell({ active: true }); },
  };
}

/** Find a rendered element without executing child components or mounting an editor. */
function find(node, predicate) {
  if (!node || typeof node !== "object") return null;
  if (Array.isArray(node)) {
    for (const child of node) { const match = find(child, predicate); if (match) return match; }
    return null;
  }
  return predicate(node) ? node : find(node.props?.children, predicate);
}

/** Drain async action callbacks while deliberately leaving the network response unsettled. */
async function settle() {
  await new Promise((resolve) => setImmediate(resolve));
}

/** Open the initial Notes root using the real workspace button callback. */
async function start(app) {
  const button = find(app.render(), (node) => node.type === "Button" && node.props.children === "开始使用笔记");
  assert.ok(button);
  button.props.onClick();
  await settle();
}

/** Select the sync control from the actual rendered workspace. */
function syncButton(app) {
  return find(app.render(), (node) => node.type === "Button" && node.props["aria-label"] === "立即同步");
}

/** Click a note through the real directory callback and wait for the local open action. */
async function openNote(app) {
  const tree = find(app.render(), (node) => node.type === "NoteTree");
  assert.ok(tree);
  tree.props.onOpen({ vaultId: "vault", id: "note", parentId: "", name: "example.md" });
  await settle();
}

test("first initialization opens downloaded notes before the full synchronization finishes", async () => {
  const app = workspace();
  await start(app);
  assert.equal(app.calls.sync, 1);
  assert.equal(syncButton(app).props.disabled, true);
  await openNote(app);
  assert.equal(app.calls.open, 1);
  app.network.resolve();
  await settle();
  assert.equal(syncButton(app).props.disabled, false);
});

test("manual synchronization flushes first but does not lock note selection", async () => {
  const app = workspace();
  await start(app);
  app.network.resolve();
  await settle();
  const before = app.calls.flush;
  app.nextSync();
  syncButton(app).props.onClick();
  syncButton(app).props.onClick();
  await openNote(app);
  assert.equal(syncButton(app).props.disabled, true);
  assert.equal(app.calls.sync, 2, "duplicate sync calls share one pending request");
  assert.equal(app.calls.flush, before + 1);
  assert.equal(app.calls.open, 1);
  app.network.resolve();
  await settle();
});

test("failed synchronization reports an error and leaves local notes usable", async () => {
  const app = workspace();
  await start(app);
  app.network.reject(new Error("offline"));
  await settle();
  assert.deepEqual(app.errors, ["offline"]);
  assert.equal(syncButton(app).props.disabled, false);
  await openNote(app);
  assert.equal(app.calls.open, 1);
});

test("failed local flush prevents synchronization and allows retry after recovery", async () => {
  const app = workspace();
  app.failFlush(new Error("save failed"));
  await start(app);
  assert.equal(app.calls.sync, 0);
  assert.deepEqual(app.errors, ["save failed"]);
  assert.equal(syncButton(app).props.disabled, false);
  app.failFlush(null);
  syncButton(app).props.onClick();
  await settle();
  assert.equal(app.calls.sync, 1);
  app.network.resolve();
  await settle();
  assert.equal(syncButton(app).props.disabled, false);
});
