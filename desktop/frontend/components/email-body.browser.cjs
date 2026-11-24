/* eslint-disable @typescript-eslint/no-require-imports -- Standalone Chromium regression. */
const assert = require("node:assert/strict");
const { spawn } = require("node:child_process");
const http = require("node:http");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const ts = require("typescript");

const chrome = process.env.CHROME_BIN || ["C:/Program Files/Google/Chrome/Application/chrome.exe",
  "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe", "/usr/bin/chromium", "/usr/bin/google-chrome",
].find((file) => fs.existsSync(file));
assert.ok(chrome, "Set CHROME_BIN to a Chromium executable");

// Use the production document generator without starting the React/Wails app.
const source = fs.readFileSync(process.env.EMAIL_BODY_SOURCE || path.join(__dirname, "email-body.tsx"), "utf8");
const ast = ts.createSourceFile("email-body.tsx", source, ts.ScriptTarget.Latest, true);
const names = new Set(["htmlDocument", "bridgeScript", "contentSecurityPolicy", "palette"]);
const helpers = ast.statements.filter((node) => ts.isFunctionDeclaration(node) && names.has(node.name?.text));
const compile = (code) => ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const json = (value) => JSON.stringify(value).replaceAll("<", "\\u003c");

async function main() {
  const fixture = process.argv[2];
  const mail = fixture ? fs.readFileSync(fixture, "utf8") : `<style>html, body { height: 100%; }</style>
    <table style="border-spacing:0;width:100%"><tr><td style="padding:0">
      <div style="height:560px">Notification</div><div style="height:40px">Footer</div>
    </td></tr></table>`;
  const sanitizer = fixture ? compile(fs.readFileSync(path.join(__dirname, "../lib/sanitize-email.ts"), "utf8")
    .replace(/^import .*;$/gm, "").replace(/\bexport /g, "")) : "";
  let finish;
  const result = new Promise((resolve) => { finish = resolve; });
  const server = http.createServer(async (req, res) => {
    if (req.method === "POST" && req.url === "/result") {
      let data = "";
      for await (const part of req) data += part;
      res.end();
      finish(JSON.parse(data));
    } else if (req.url === "/purify.js") {
      res.setHeader("Content-Type", "text/javascript");
      res.end(fs.readFileSync(path.join(path.dirname(require.resolve("dompurify")), "purify.js")));
    } else {
      res.setHeader("Content-Type", "text/html; charset=utf-8");
      res.end(`<!doctype html><script src="/purify.js"></script><body><script>
        const localOrigin = () => location.origin;
        ${compile(helpers.map((node) => node.getText(ast)).join("\n")).replaceAll("</script>", "<\\/script>")}
        ${sanitizer}
        const raw = ${json(mail)};
        const frame = document.createElement('iframe');
        frame.width = '680';
        frame.height = '320';
        frame.setAttribute('sandbox', 'allow-scripts');
        let height;
        window.addEventListener('message', (event) => {
          if (event.source !== frame.contentWindow || event.data.type !== 'height') return;
          height = event.data.height;
          frame.height = Math.max(120, height + 16);
        });
        const clean = ${fixture ? "sanitizeEmailHtml(raw, false).html" : "raw"};
        frame.srcdoc = htmlDocument(clean, 'regression', false, 'light');
        document.body.append(frame);
        const settle = () => new Promise((resolve) => setTimeout(resolve, 300));
        (async () => {
          await settle();
          const normal = height;
          frame.contentWindow.postMessage({ type: 'czl-zoom', value: 1.25 }, '*');
          await settle();
          const zoomed = height;
          frame.contentWindow.postMessage({ type: 'czl-zoom', value: 1 }, '*');
          await settle();
          await fetch('/result', { method: 'POST', body: JSON.stringify([normal, zoomed, height]) });
        })();
      </script>`);
    }
  });
  const dir = fs.mkdtempSync(path.join(process.env.CZL_TEMP || os.tmpdir(), "mail-browser-"));
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const child = spawn(chrome, ["--headless", "--disable-gpu", "--disable-background-networking", "--no-first-run",
    `--user-data-dir=${dir}`, `http://127.0.0.1:${server.address().port}`], { stdio: "ignore" });
  let timer;
  try {
    const heights = await Promise.race([result, new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("Timed out waiting for Chromium")), 12000);
      child.once("error", reject);
    })]);
    if (fixture) {
      assert.ok(heights[0] > 336 && heights[1] > heights[0] && heights[2] === heights[0], JSON.stringify(heights));
    } else {
      assert.deepEqual(heights, [600, 750, 600]);
    }
    console.log("Email body heights (normal, enlarged, restored):", heights.join(", "));
  } finally {
    clearTimeout(timer);
    server.close();
    if (child.pid && child.exitCode === null) {
      const exited = new Promise((resolve) => child.once("exit", resolve));
      child.kill();
      await exited;
    }
    fs.rmSync(dir, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
