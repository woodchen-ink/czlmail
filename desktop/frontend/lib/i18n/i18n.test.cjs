/* eslint-disable @typescript-eslint/no-require-imports -- Standalone CommonJS Node test runner. */
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { createRequire } = require("node:module");
const { test } = require("node:test");
const ts = require("typescript");

const root = path.resolve(__dirname, "../..");
const corePath = path.join(__dirname, "index.ts");
const english = require("./en.json");

/** Load an isolated locale store without building Next.js or generating Wails bindings. */
function loadLocale({ saved = null, blocked = false } = {}) {
  const storage = new Map(saved === null ? [] : [["czlmail.locale", saved]]);
  const document = { documentElement: { lang: "zh-CN" } };
  const window = { localStorage: {
    getItem(key) { if (blocked) throw new Error("storage blocked"); return storage.get(key) ?? null; },
    setItem(key, value) { if (blocked) throw new Error("storage blocked"); storage.set(key, value); },
  } };
  const source = fs.readFileSync(corePath, "utf8");
  const output = ts.transpileModule(source, { compilerOptions: {
    module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, esModuleInterop: true,
  } }).outputText;
  const compiledModule = { exports: {} };
  vm.runInNewContext(output, { module: compiledModule, exports: compiledModule.exports, require: createRequire(corePath), window, document });
  return { api: compiledModule.exports, storage, document };
}

/** Recursively list source files, excluding dictionaries and generated dependencies. */
function sourceFiles(directory) {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const file = path.join(directory, entry.name);
    return entry.isDirectory() ? sourceFiles(file) : /\.tsx?$/.test(file) ? [file] : [];
  });
}

const files = ["app", "components", "lib"].flatMap((dir) => sourceFiles(path.join(root, dir)));

test("Chinese is the default and unsupported preferences are rejected", () => {
  const { api } = loadLocale();
  assert.equal(api.getLocale(), "zh-CN");
  assert.equal(api.t("邮件"), "邮件");
  for (const value of [undefined, null, "", "en-US", "fr", {}, 1]) assert.equal(api.normalizeLocale(value), "zh-CN");
});

test("English translates UI text and reorders substitutions", () => {
  const { api } = loadLocale();
  api.setLocale("en");
  assert.equal(api.t("邮件"), "Mail");
  assert.equal(api.t("由{0}检查", "mail.example.net"), "Checked by mail.example.net");
  assert.equal(api.t("已选{0}封", 5), "5 emails selected");
});

test("Inserted values are not interpreted as translation placeholders", () => {
  const { api } = loadLocale();
  api.setLocale("en");
  assert.equal(api.t("已保存到 {0}", "folder/{1}"), "Saved to folder/{1}");
  assert.equal(api.t("邮件"), "Mail");
  assert.equal(api.t("custom folder name"), "custom folder name");
  assert.equal(api.t("已保存到 {0}"), "Saved to {0}");
});

test("Both selectors use one persisted preference and update the document language", () => {
  const { api, storage, document } = loadLocale();
  api.setLocale("en");
  assert.equal(storage.get("czlmail.locale"), "en");
  assert.equal(document.documentElement.lang, "en");
  api.setLocale("zh-CN");
  assert.equal(storage.get("czlmail.locale"), "zh-CN");
  assert.equal(document.documentElement.lang, "zh-CN");
});

test("Language changes notify subscribers once per actual change", () => {
  const { api } = loadLocale();
  let calls = 0;
  const unsubscribe = api.subscribeLocale(() => { calls += 1; });
  api.setLocale("en");
  api.setLocale("en");
  assert.equal(calls, 1);
  unsubscribe();
  api.setLocale("zh-CN");
  assert.equal(calls, 1);
});

test("A saved preference is restored only after hydration", () => {
  const { api, document } = loadLocale({ saved: "en" });
  assert.equal(api.getLocale(), "zh-CN");
  api.restoreLocale();
  assert.equal(api.getLocale(), "en");
  assert.equal(document.documentElement.lang, "en");
  assert.equal(api.getServerLocale(), "zh-CN");
});

test("Corrupt or unavailable storage falls back safely", () => {
  const corrupt = loadLocale({ saved: '{"locale":"en"}' });
  corrupt.api.restoreLocale();
  assert.equal(corrupt.api.getLocale(), "zh-CN");
  const blocked = loadLocale({ blocked: true });
  blocked.api.restoreLocale();
  blocked.api.setLocale("en");
  assert.equal(blocked.api.getLocale(), "en");
  assert.equal(blocked.document.documentElement.lang, "en");
});

test("Cross-window updates need not rewrite storage", () => {
  const { api, storage } = loadLocale({ saved: "en" });
  api.setLocale("zh-CN", false);
  assert.equal(storage.get("czlmail.locale"), "en");
  api.setLocale("en", false);
  assert.equal(api.dateLocale(), "en-US");
  api.setLocale("zh-CN", false);
  assert.equal(api.dateLocale(), "zh-CN");
});

test("Every literal UI translation key has an English entry", () => {
  const missing = new Set();
  let count = 0;
  for (const file of files) {
    const source = ts.createSourceFile(file, fs.readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
    function visit(node) {
      if (ts.isCallExpression(node) && node.expression.getText(source) === "t" && node.arguments[0] && ts.isStringLiteral(node.arguments[0])) {
        count += 1;
        if (!Object.hasOwn(english, node.arguments[0].text)) missing.add(node.arguments[0].text);
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
  assert.ok(count > 1000, "scan must cover the complete app, not just the login page");
  assert.deepEqual([...missing], []);
});

test("Translations preserve all numbered placeholders", () => {
  const placeholders = (value) => [...new Set(value.match(/\{\d+\}/g) ?? [])].sort();
  for (const [source, translated] of Object.entries(english)) {
    assert.ok(translated.trim(), `empty translation: ${source}`);
    assert.deepEqual(placeholders(translated), placeholders(source), source);
  }
});

test("Every TypeScript and JSX source parses", () => {
  for (const file of files) {
    const source = ts.createSourceFile(file, fs.readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
    assert.deepEqual(source.parseDiagnostics.map((d) => ts.flattenDiagnosticMessageText(d.messageText, " ")), [], file);
  }
});

test("Locale core passes strict TypeScript checking", () => {
  const program = ts.createProgram([corePath], {
    strict: true, noEmit: true, skipLibCheck: true, types: [],
    target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS,
    moduleResolution: ts.ModuleResolutionKind.Node10,
    esModuleInterop: true, resolveJsonModule: true,
  });
  assert.deepEqual(ts.getPreEmitDiagnostics(program).map((d) => ts.flattenDiagnosticMessageText(d.messageText, " ")), []);
});

test("Translation calls cannot be shadowed by a task, template, or timer variable", () => {
  const program = ts.createProgram(files, {
    noEmit: true, skipLibCheck: true, target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler,
    jsx: ts.JsxEmit.ReactJSX, baseUrl: root, paths: { "@/*": ["./*"] },
  });
  const checker = program.getTypeChecker();
  const conflicts = [];
  for (const source of program.getSourceFiles()) {
    if (!files.includes(source.fileName)) continue;
    function visit(node) {
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "t") {
        const declarations = checker.getSymbolAtLocation(node.expression)?.declarations ?? [];
        if (!declarations.some((entry) => ts.isImportSpecifier(entry) || ts.isFunctionDeclaration(entry) || ts.isBindingElement(entry))) {
          conflicts.push(`${source.fileName}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1}`);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
  assert.deepEqual(conflicts, []);
});

test("Date labels change language but date input and protocol values do not", () => {
  const { api } = loadLocale();
  const file = path.join(root, "lib/date.ts");
  const output = ts.transpileModule(fs.readFileSync(file, "utf8"), { compilerOptions: {
    module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
  } }).outputText;
  const compiledModule = { exports: {} };
  vm.runInNewContext(output, {
    module: compiledModule, exports: compiledModule.exports,
    require: (specifier) => { assert.equal(specifier, "@/lib/i18n"); return api; },
  });
  const dates = compiledModule.exports;
  const date = new Date(2026, 9, 16, 15, 30);
  const input = dates.toDateInput(date);
  const protocol = dates.toLocalDateTime(date);
  assert.equal(dates.getWeekdays()[0], "周一");
  api.setLocale("en");
  assert.equal(dates.getWeekdays()[0], "Mon");
  assert.equal(dates.monthTitle(date), "October 2026");
  assert.equal(dates.toDateInput(date), input);
  assert.equal(dates.toLocalDateTime(date), protocol);
});

test("Language switching never translates attachment detection keywords or request values", () => {
  const compose = fs.readFileSync(path.join(root, "components/compose/compose-pane.tsx"), "utf8");
  assert.match(compose, /const ATTACHMENT_WORDS = \["附件", "附上", "见附", "attached", "attachment", "enclosed"\]/);
  assert.match(compose, /language: lang/);
  const login = fs.readFileSync(path.join(root, "components/sign-in.tsx"), "utf8");
  const appearance = fs.readFileSync(path.join(root, "components/settings/general-sections.tsx"), "utf8");
  assert.match(login, /<LanguageSelect\s*\/>/);
  assert.match(appearance, /<LanguageSelect\s*\/>/);
});
