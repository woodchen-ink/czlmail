/* eslint-disable @typescript-eslint/no-require-imports -- Isolated browser regression using installed Next webpack and a system Chromium. */
const fs = require("node:fs");
const path = require("node:path");
const os = require("node:os");
const http = require("node:http");
const { spawn } = require("node:child_process");
const { createRequire } = require("node:module");
const root = path.resolve(__dirname, "../..");
const local = createRequire(path.join(root, "package.json"));
const { webpack } = local("next/dist/compiled/webpack/webpack");
const base = fs.mkdtempSync(path.join(process.env.CZL_TEMP || os.tmpdir(), "notes-browser-"));
const browserPath = process.env.CHROME_BIN || ["C:/Program Files/Google/Chrome/Application/chrome.exe", "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe"].find((file) => fs.existsSync(file));

/** Wait for an actual browser condition rather than assuming asynchronous React roots have mounted. */
async function waitFor(check, label) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) { if (await check()) return; await new Promise((resolve) => setTimeout(resolve, 100)); }
  throw new Error(`Timed out: ${label}`);
}

/** Bundle production editor code against synthetic text, using only existing project dependencies. */
async function bundle() {
  fs.writeFileSync(path.join(base, "stub.js"), "export const t=(value)=>value; export const useI18n=()=>({}); export const toast={success(){},error(){}};");
  fs.writeFileSync(path.join(base, "loader.cjs"), `const ts=require(${JSON.stringify(local.resolve("typescript"))}); module.exports=function(source){return ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext,jsx:ts.JsxEmit.ReactJSX,esModuleInterop:true}}).outputText};`);
  fs.writeFileSync(path.join(base, "entry.tsx"), `import React from "react"; import {createRoot} from "react-dom/client"; import {NoteEditor} from "@/components/notes/note-editor"; import {EditorView} from "@codemirror/view";
const text="# Test heading\\n\\n| Name | Value |\\n| --- | --- |\\n| Table | **bold**<br>next |\\n\\nFirst reference[^n].\\n\\n"+Array.from({length:180},(_,i)=>"Paragraph "+i+". Long document body for viewport testing.\\n\\n").join("")+"[^n]: Definition at the end.\\n\\nEnd without a newline";
window.errors=[]; window.addEventListener("error",e=>window.errors.push(e.message)); window.addEventListener("czl-note-anchor",e=>window.lastAnchor=e.detail,true); window.fixtureText=text;
createRoot(document.getElementById("root")).render(<main style={{height:460,width:880}}><NoteEditor vaultId="fixture" noteId="fixture" value={text} source={false} readOnly={false} onChange={v=>window.lastText=v} onFiles={()=>{}} onReady={()=>{}} /></main>); window.getView=()=>EditorView.findFromDOM(document.querySelector(".cm-editor"));`);
  await new Promise((resolve, reject) => webpack({ mode: "development", devtool: false, context: root, entry: path.join(base, "entry.tsx"), output: { path: base, filename: "bundle.js" }, resolve: { extensions: [".ts", ".tsx", ".js", ".mjs"], modules: [path.join(root, "node_modules"), "node_modules"], alias: { "@/components/locale-provider$": path.join(base, "stub.js"), "@/lib/i18n$": path.join(base, "stub.js"), "sonner$": path.join(base, "stub.js"), "@": root } }, module: { rules: [{ test: /\.tsx?$/, exclude: /node_modules/, use: path.join(base, "loader.cjs") }] } }, (error, stats) => error || stats.hasErrors() ? reject(error || new Error(stats.toString({ all: false, errors: true }))) : resolve()));
}

/** Exercise a real virtualized editor in an isolated headless profile, with no Wails or mailbox. */
async function main() {
  if (!browserPath) throw new Error("Set CHROME_BIN to a system Chromium executable");
  await bundle();
  const cssDirectory = path.join(root, "out/_next/static/chunks");
  const cssFile = fs.readdirSync(cssDirectory).find((name) => name.endsWith(".css"));
  const server = http.createServer((request, response) => {
    if (request.url === "/bundle.js") { response.setHeader("Content-Type", "text/javascript"); response.end(fs.readFileSync(path.join(base, "bundle.js"))); }
    else if (request.url === "/style.css") { response.setHeader("Content-Type", "text/css"); response.end(fs.readFileSync(path.join(cssDirectory, cssFile))); }
    else { response.setHeader("Content-Type", "text/html; charset=utf-8"); response.end('<!doctype html><link rel="stylesheet" href="/style.css"><div id="root"></div><script src="/bundle.js"></script>'); }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const profile = path.join(base, "profile");
  const child = spawn(browserPath, ["--headless=new", "--disable-gpu", "--no-first-run", "--disable-extensions", "--disable-background-networking", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: "ignore" });
  let socket;
  try {
    const portFile = path.join(profile, "DevToolsActivePort");
    await waitFor(() => fs.existsSync(portFile), "Chromium startup");
    const port = fs.readFileSync(portFile, "utf8").split("\n")[0];
    const tabs = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    socket = new WebSocket(tabs.find((tab) => tab.type === "page").webSocketDebuggerUrl);
    await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
    let sequence = 0; const pending = new Map();
    socket.onmessage = (message) => { const value = JSON.parse(message.data); if (value.id && pending.has(value.id)) { const { resolve, reject } = pending.get(value.id); pending.delete(value.id); if (value.error) reject(new Error(JSON.stringify(value.error))); else resolve(value.result); } };
    const call = (method, params = {}) => new Promise((resolve, reject) => { const id = ++sequence; pending.set(id, { resolve, reject }); socket.send(JSON.stringify({ id, method, params })); });
    const evaluate = async (expression) => { const result = await call("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }); if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text); return result.result.value; };
    await call("Runtime.enable"); await call("Page.enable");
    await call("Emulation.setDeviceMetricsOverride", { width: 1100, height: 720, deviceScaleFactor: 1, mobile: false });
    await call("Page.navigate", { url: `http://127.0.0.1:${server.address().port}` });
    await waitFor(() => evaluate('!!document.querySelector("a[data-footnote-ref]") && !!document.querySelector("table")'), "rendered table and first reference");
    await evaluate('document.querySelector("a[data-footnote-ref]").click()');
    try { await waitFor(() => evaluate('(()=>{const target=document.getElementById("user-content-fn-n"); if(!target)return false; const r=target.getBoundingClientRect();return r.top>=0&&r.top<480&&document.querySelector(".cm-scroller").scrollTop>1000})()'), "offscreen footnote navigation"); }
    catch (error) {
      console.error(await evaluate('JSON.stringify({errors:window.errors,lastAnchor:window.lastAnchor,scroll:document.querySelector(".cm-scroller").scrollTop,height:document.querySelector(".cm-scroller").scrollHeight,clientHeight:document.querySelector(".cm-scroller").clientHeight,bounds:document.querySelector(".cm-scroller").getBoundingClientRect().toJSON(),main:document.querySelector("main").getBoundingClientRect().toJSON(),viewport:window.getView().viewport,footnote:document.getElementById("user-content-fn-n")?.getBoundingClientRect().toJSON(),html:document.querySelector("section[data-footnotes]")?.outerHTML})'));
      fs.writeFileSync(path.join(base, "failure.png"), Buffer.from((await call("Page.captureScreenshot", { format: "png" })).data, "base64"));
      console.error(`Browser artifacts: ${base}`); throw error;
    }
    await evaluate('document.querySelector("a[data-footnote-backref]").click()');
    await waitFor(() => evaluate('(()=>{const target=document.getElementById("user-content-fnref-n"); if(!target)return false;const r=target.getBoundingClientRect();return r.top>=0&&r.top<480})()'), "footnote back-reference navigation");
    const result = await evaluate('({errors:window.errors,unchanged:window.getView().state.doc.toString()===window.fixtureText,table:!!document.querySelector("table")})');
    if (result.errors.length || !result.unchanged || !result.table) throw new Error(JSON.stringify(result));
    fs.writeFileSync(path.join(base, "preview.png"), Buffer.from((await call("Page.captureScreenshot", { format: "png" })).data, "base64"));
    const click = async ({ x, y }) => {
      for (const type of ["mousePressed", "mouseReleased"]) await call("Input.dispatchMouseEvent", { type, x, y, button: "left", clickCount: 1 });
    };
    // A long note without cross-block syntax: other blocks must be reused, not re-rendered, while typing.
    await evaluate(`(()=>{window.plainText="# Plain heading\\n\\n- [ ] task one\\n\\n"+Array.from({length:300},(_,i)=>"Paragraph "+i+" with **bold** word.\\n\\n").join("");
      const v=window.getView(); v.dispatch({changes:{from:0,to:v.state.doc.length,insert:window.plainText},selection:{anchor:window.plainText.length}}); v.scrollDOM.scrollTop=0;})()`);
    await waitFor(() => evaluate('!!document.querySelector(".cm-content input[type=checkbox]") && document.querySelectorAll(".cm-content strong").length > 3'), "plain document rendered");
    const typing = await evaluate(`(async()=>{const v=window.getView(); const marked=document.querySelectorAll(".cm-content strong")[3]; let at=v.state.doc.toString().indexOf("Paragraph 1 ");
      v.dispatch({selection:{anchor:at}}); await new Promise(r=>setTimeout(r,50)); const start=performance.now();
      for(let i=0;i<40;i++){v.dispatch(v.state.replaceSelection("x")); await new Promise(r=>setTimeout(r,0));}
      return {ms:performance.now()-start, reused:marked.isConnected};})()`);
    if (!typing.reused || typing.ms > 2000) throw new Error(`typing re-rendered other blocks: ${JSON.stringify(typing)}`);
    // Measured heights replace estimates over a few frames; click only once the word stops moving.
    const word = await evaluate(`(async()=>{const at=()=>{const p=[...document.querySelectorAll(".cm-content p")].find(e=>e.textContent.startsWith("Paragraph 3 ")); const s=p.querySelector("strong").firstChild; const r=document.createRange(); r.setStart(s,2); r.setEnd(s,3); const b=r.getBoundingClientRect(); return {x:b.left+1,y:b.top+b.height/2};};
      let last=at(), still=0; for(let i=0;i<120&&still<5;i++){await new Promise(r=>requestAnimationFrame(r)); const next=at(); still=next.x===last.x&&next.y===last.y?still+1:0; last=next;} return last;})()`);
    await click(word);
    const caret = await evaluate(`(()=>{const v=window.getView(); const text=v.state.doc.toString(); const at=text.indexOf("**bold**",text.indexOf("Paragraph 3 ")); return {head:v.state.selection.main.head, at};})()`);
    if (caret.head < caret.at + 3 || caret.head > caret.at + 5) throw new Error(`click caret ${JSON.stringify(caret)}`);
    await evaluate('window.getView().dispatch({selection:{anchor:window.getView().state.doc.length}})');
    await waitFor(() => evaluate('!!document.querySelector(".cm-content input[type=checkbox]")'), "task rendered");
    const box = await evaluate('(()=>{const b=document.querySelector(".cm-content input[type=checkbox]").getBoundingClientRect(); return {x:b.left+b.width/2,y:b.top+b.height/2};})()');
    await click(box);
    await waitFor(() => evaluate('window.getView().state.doc.toString().includes("- [x] task one")'), "task toggled in source");
    await evaluate('window.getView().dispatch({selection:{anchor:3}})');
    const heading = await evaluate('(()=>{const line=document.querySelector(".cm-line"); const span=line.querySelector("span:last-child")||line; return parseFloat(getComputedStyle(span).fontSize);})()');
    if (heading < 28) throw new Error(`source heading font ${heading}px does not match rendered h1`);
    const errors = await evaluate("window.errors");
    if (errors.length) throw new Error(JSON.stringify(errors));
    console.log(JSON.stringify({ status: "passed", checks: ["real editor GFM table", "long-document footnote jump", "back-reference jump", "source unchanged", `40 keystrokes in ${Math.round(typing.ms)}ms without re-rendering other blocks`, "click places caret in source", "task box toggles source", "source heading matches rendered size", "no browser exceptions"], artifacts: base }));
  } finally {
    socket?.close(); child.kill(); await new Promise((resolve) => server.close(resolve));
  }
}
main().catch((error) => { console.error(error.stack); process.exitCode = 1; });
