"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Copy, ExternalLink, ImageOff, UserCheck } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { hasOwnColors, sanitizeEmailHtml } from "@/lib/sanitize-email";
import { openExternal } from "@/lib/external";

interface Props {
  html: string;
  text: string;
  /** 发件人地址，决定远程内容是否默认放行。 */
  senderEmail: string;
  /** 发件人是否已在信任名单（或通讯录）中。 */
  senderTrusted: boolean;
  /** 「始终信任此发件人」的回调。 */
  onTrustSender: (address: string) => void;
  /**
   * 正文用亮色还是暗色渲染，由应用明确给出。
   *
   * 不能让 iframe 自己读 prefers-color-scheme：那是操作系统的主题，而应用主题
   * 可以被用户手动设成与系统相反，结果就是白色应用里嵌一块暗色正文。
   */
  mode: "light" | "dark";
  /** cid → 本地预览 URL，用于显示邮件内嵌图片。 */
  inlineImages?: Record<string, string>;
  /**
   * 已翻译的文字片段 `[片段序号, 译文]`，全量（幂等，丢一次不影响下一次）。
   *
   * 走 postMessage 送进 iframe 原地替换 `[data-czl-tr]`，而不是重新生成 srcDoc：
   * srcDoc 一变整个文档就重载，图片要重新请求一遍，流式翻译时就是一直在闪。
   */
  translated?: [number, string][];
}

/**
 * 邮件正文渲染。
 *
 * 正文放进沙箱 iframe，而不是直接插进应用的 DOM：邮件排版重度依赖内联样式与
 * table，为了正确显示必须保留它们，而保留样式就意味着邮件的 CSS 能影响到应用
 * 自身的界面。iframe 是唯一能同时做到「样式生效」与「样式不外溢」的办法。
 *
 * sandbox 给了 allow-scripts 但**没有** allow-same-origin，这是关键的一对：
 * 没有 same-origin，iframe 处于一个独立的不透明源，拿不到父页面的 DOM、
 * localStorage 与 cookie。脚本能跑，但跑在一个与应用完全隔离的盒子里。
 */
export function EmailBody({
  html,
  text,
  senderEmail,
  senderTrusted,
  onTrustSender,
  mode: requestedMode,
  inlineImages,
  translated,
}: Props) {
  // 自带配色的邮件在暗色主题下仍用亮色纸面，见 hasOwnColors。
  const mode = requestedMode === "dark" && html && hasOwnColors(html) ? "light" : requestedMode;

  // 本次阅读临时放行。与「永久信任」分开：多数时候用户只是想看这一封,
  // 把这个动作等同于永久信任会让信任名单迅速失去意义。
  const [allowOnce, setAllowOnce] = useState(false);
  const allowImages = senderTrusted || allowOnce;

  const frameRef = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(320);
  // 正文里链接的右键菜单。坐标是相对应用视口的，由 iframe 上报的坐标加上 iframe 自身位置得到。
  const [linkMenu, setLinkMenu] = useState<{ url: string; x: number; y: number } | null>(null);

  // 切换邮件时重置临时放行，否则上一封的决定会带到下一封陌生发件人身上。
  useEffect(() => {
    setAllowOnce(false);
  }, [html, text, senderEmail]);

  // 每次渲染换一个 nonce：CSP 只放行带该 nonce 的脚本。
  // 这样即使消毒器漏掉了一个 <script>，浏览器仍会拒绝执行它。
  const nonce = useMemo(() => cryptoNonce(), [html, allowImages, mode]);

  const { doc, blockedImages } = useMemo(() => {
    if (!html) {
      return { doc: plainTextDocument(text, nonce, mode), blockedImages: 0 };
    }
    const result = sanitizeEmailHtml(html, allowImages, inlineImages);
    return {
      doc: htmlDocument(result.html, nonce, allowImages, mode),
      blockedImages: result.blockedImages,
    };
  }, [html, text, allowImages, nonce, mode, inlineImages]);

  // 桥接脚本就绪之前发的补丁会丢在半路，先攒着，等 iframe 报到再发。
  // 换了文档(新 srcDoc)要重新握手，这里用 ref 记状态：置成 state 会在 effect 里
  // 触发一轮额外渲染，而这件事本来就只是和 iframe 这个外部系统对齐。
  const readyRef = useRef(false);
  const pendingRef = useRef<[number, string][] | null>(null);

  const sendTranslated = (items: [number, string][]) => {
    frameRef.current?.contentWindow?.postMessage({ type: "czl-translate", items }, "*");
  };

  // 换了文档(新 srcDoc)就要重新握手。下面那个 effect 依赖里带上 doc，
  // 于是新文档挂载后会把当前译文重新排进队列，等 iframe 报到再发。
  useEffect(() => {
    readyRef.current = false;
    pendingRef.current = null;
  }, [doc]);

  useEffect(() => {
    if (!translated?.length) return;
    if (!readyRef.current) {
      pendingRef.current = translated;
      return;
    }
    sendTranslated(translated);
  }, [translated, doc]);

  // 沙箱内的桥接脚本通过 postMessage 上报链接点击与内容高度。
  // 消息一律当作不可信输入校验：源必须是本 iframe，类型与字段必须完全匹配。
  useEffect(() => {
    function onMessage(event: MessageEvent) {
      if (event.source !== frameRef.current?.contentWindow) return;

      const data = event.data as unknown;
      if (typeof data !== "object" || data === null) return;

      const msg = data as {
        type?: unknown;
        url?: unknown;
        height?: unknown;
        x?: unknown;
        y?: unknown;
      };

      if (msg.type === "ready") {
        readyRef.current = true;
        const queued = pendingRef.current;
        pendingRef.current = null;
        if (queued?.length) sendTranslated(queued);
        return;
      }
      if (msg.type === "link" && typeof msg.url === "string") {
        openExternal(msg.url);
        return;
      }
      if (
        msg.type === "link-menu" &&
        typeof msg.url === "string" &&
        typeof msg.x === "number" &&
        typeof msg.y === "number"
      ) {
        const rect = frameRef.current?.getBoundingClientRect();
        if (!rect) return;
        setLinkMenu({ url: msg.url, x: rect.left + msg.x, y: rect.top + msg.y });
        return;
      }
      if (msg.type === "height" && typeof msg.height === "number") {
        // 留一点余量，避免因为四舍五入出现一条内部滚动条。
        setHeight(Math.min(Math.max(msg.height + 16, 120), 20000));
      }
    }

    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  return (
    <div className="flex flex-col gap-3">
      {blockedImages > 0 && !allowImages && (
        <div className="border-border bg-secondary flex flex-wrap items-center gap-3 rounded-md border px-3 py-2.5">
          <ImageOff className="text-muted-foreground size-4 shrink-0" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">
              已屏蔽 {blockedImages} 张远程图片
            </p>
            <p className="text-muted-foreground text-xs">
              加载后发件人会得知你打开邮件的时间与网络位置
            </p>
          </div>
          <div className="flex shrink-0 gap-2">
            <Button size="sm" variant="secondary" onClick={() => setAllowOnce(true)}>
              加载图片
            </Button>
            {senderEmail && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => onTrustSender(senderEmail)}
              >
                <UserCheck className="size-3.5" />
                始终信任此发件人
              </Button>
            )}
          </div>
        </div>
      )}

      {/* 亮色正文放进白底圆角容器：应用是暗色时，它看起来是一张纸，而不是一块刺眼的洞。 */}
      <div
        className={
          mode === "light"
            ? "bg-paper overflow-hidden rounded-md p-4"
            : "overflow-hidden rounded-md"
        }
      >
        <iframe
          ref={frameRef}
          title="邮件正文"
          key={nonce}
          srcDoc={doc}
          sandbox="allow-scripts"
          className="block w-full border-0 bg-transparent"
          // iframe 里的 prefers-color-scheme 取自 iframe 元素自己的 color-scheme，
          // 不写就继承应用根上的 dark：邮件自带的暗色 @media 会在亮色纸面上生效。
          style={{ height, colorScheme: mode }}
        />
      </div>

      <DropdownMenu open={linkMenu !== null} onOpenChange={(open) => !open && setLinkMenu(null)}>
        {/* 菜单锚点：放在右键位置的零尺寸元素。 */}
        <DropdownMenuTrigger asChild>
          <span
            aria-hidden
            className="pointer-events-none fixed size-0"
            style={{ left: linkMenu?.x ?? 0, top: linkMenu?.y ?? 0 }}
          />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-auto max-w-80">
          {linkMenu && (
            <>
              <DropdownMenuItem onSelect={() => openExternal(linkMenu.url)}>
                <ExternalLink />
                在浏览器中打开
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => copyLink(linkMenu.url)}>
                <Copy />
                {isMailto(linkMenu.url) ? "复制邮箱地址" : "复制链接"}
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function isMailto(url: string): boolean {
  return /^mailto:/i.test(url);
}

/** 复制链接；mailto: 只复制地址部分，那才是用户想要的东西。 */
async function copyLink(url: string) {
  const text = isMailto(url) ? decodeURIComponent(url.slice(7).split("?")[0]) : url;
  try {
    await navigator.clipboard.writeText(text);
    toast.success(isMailto(url) ? "已复制邮箱地址" : "已复制链接");
  } catch {
    toast.error("复制失败");
  }
}

/** 沙箱内的桥接脚本：上报高度、拦截链接点击与右键、把父窗口送来的译文写进对应的片段。 */
function bridgeScript(nonce: string): string {
  return `<script nonce="${nonce}">
(function () {
  var lastH = 0, lastView = 0;
  function report() {
    var h = Math.max(
      document.documentElement.scrollHeight,
      document.body ? document.body.scrollHeight : 0
    );
    var view = window.innerHeight;
    // 版式高度跟随视口(height:100%、100vh)的邮件: iframe 变高后内容也等量变高,
    // 再上报就会无限增长。视口变了而"内容 - 视口"的差值没变, 说明是被视口撑开的, 不上报。
    if (lastView && view !== lastView && h - view === lastH - lastView) return;
    lastH = h;
    lastView = view;
    parent.postMessage({ type: "height", height: h }, "*");
  }
  document.addEventListener("click", function (e) {
    var a = e.target && e.target.closest ? e.target.closest("a[data-external-href]") : null;
    if (!a) return;
    e.preventDefault();
    parent.postMessage({ type: "link", url: a.getAttribute("data-external-href") }, "*");
  });
  // 链接上右键: 交给父窗口弹出应用自己的菜单(打开 / 复制链接)。
  document.addEventListener("contextmenu", function (e) {
    var a = e.target && e.target.closest ? e.target.closest("a[data-external-href]") : null;
    if (!a) return;
    e.preventDefault();
    parent.postMessage({
      type: "link-menu",
      url: a.getAttribute("data-external-href"),
      x: e.clientX,
      y: e.clientY
    }, "*");
  });
  // 父窗口送来的译文: 按 data-czl-tr 找到片段, 只写 textContent(不解析 HTML)。
  var marks = null;
  window.addEventListener("message", function (e) {
    if (e.source !== parent) return;
    var d = e.data;
    if (!d || d.type !== "czl-translate" || !d.items || !d.items.length) return;
    if (!marks) {
      marks = {};
      var all = document.querySelectorAll("[data-czl-tr]");
      for (var i = 0; i < all.length; i++) marks[all[i].getAttribute("data-czl-tr")] = all[i];
    }
    for (var k = 0; k < d.items.length; k++) {
      var it = d.items[k];
      if (!it || typeof it[1] !== "string") continue;
      var el = marks[String(it[0])];
      if (el && el.textContent !== it[1]) el.textContent = it[1];
    }
    report();
  });
  window.addEventListener("load", report);
  parent.postMessage({ type: "ready" }, "*");
  // 图片加载完成会改变高度，需要再报一次。
  new ResizeObserver(report).observe(document.documentElement);
  report();
})();
</script>`;
}

/**
 * CSP 是消毒之外的第二道闸。
 *
 * default-src 'none' 意味着除了显式放行的，任何子资源请求都不会发出——
 * 包括字体、媒体、以及 fetch。img-src 在屏蔽状态下只留 data:，
 * 使得任何漏网的远程图片 URL 也无法真正发起请求。
 */
function contentSecurityPolicy(nonce: string, allowImages: boolean): string {
  // 沙箱 iframe 是不透明源，'self' 匹配不到应用本身，内嵌图片的本地路径要写明源。
  const local = typeof window === "undefined" ? "" : ` ${window.location.origin}`;
  const img = allowImages ? `img-src https: data:${local};` : `img-src data:${local};`;
  return [
    "default-src 'none';",
    img,
    "style-src 'unsafe-inline';",
    `script-src 'nonce-${nonce}';`,
    "form-action 'none';",
    "frame-ancestors 'none';",
    "base-uri 'none';",
  ].join(" ");
}

/** 正文的前景与链接色。与 globals.css 的 token 取值保持一致。 */
function palette(mode: "light" | "dark") {
  return mode === "dark"
    ? { scheme: "dark", text: "#D5DCE5", link: "#7B90BF", border: "#253042" }
    : { scheme: "light", text: "#10131A", link: "#4B669A", border: "#D5DCE5" };
}

function htmlDocument(
  body: string,
  nonce: string,
  allowImages: boolean,
  mode: "light" | "dark",
): string {
  const c = palette(mode);
  return `<!doctype html><html><head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${contentSecurityPolicy(nonce, allowImages)}">
<style nonce="${nonce}">
  :root { color-scheme: ${c.scheme}; }
  html, body { margin: 0; padding: 0; background: transparent; }
  body {
    font: 14px/1.5 system-ui, -apple-system, "Segoe UI", "Microsoft YaHei", sans-serif;
    color: ${c.text};
    /* 只用 break-word：anywhere 与 word-break 会把表格单元格的最小宽度算成一个字符，
       多栏版式被挤成一列一个字。 */
    overflow-wrap: break-word;
  }
  img { max-width: 100%; }
  /* 仅对被 max-width 压缩的图片保持比例，不覆盖邮件自己写的 height。 */
  img[width] { height: auto; }
  pre { white-space: pre-wrap; }
  a { color: ${c.link}; }
  /* href 已被清洗移到 data 属性上，浏览器不再把它当链接，手形光标要自己补。 */
  a[data-external-href] { cursor: pointer; }
  /* 被拦下的图片留一个占位，否则排版会塌陷，用户也看不出这里原本有图。 */
  img[data-blocked-src] {
    display: inline-block; min-width: 24px; min-height: 24px;
    border: 1px dashed ${c.border}; border-radius: 4px;
  }
</style>
</head><body>${body}${bridgeScript(nonce)}</body></html>`;
}

/** 纯文本正文：保留换行与空格，但仍然走同一套沙箱。 */
function plainTextDocument(text: string, nonce: string, mode: "light" | "dark"): string {
  const c = palette(mode);

  return `<!doctype html><html><head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${contentSecurityPolicy(nonce, false)}">
<style nonce="${nonce}">
  :root { color-scheme: ${c.scheme}; }
  html, body { margin: 0; padding: 0; background: transparent; }
  pre {
    margin: 0;
    font: 14px/1.7 ui-monospace, "Cascadia Code", Menlo, monospace;
    white-space: pre-wrap; word-break: break-word;
    color: ${c.text};
  }
  a { color: ${c.link}; }
  a[data-external-href] { cursor: pointer; }
</style>
</head><body><pre>${linkifyText(text)}</pre>${bridgeScript(nonce)}</body></html>`;
}

/** 纯文本里的网址转成链接。href 只留给桥接脚本用的 data 属性，点击一律交给系统浏览器。 */
function linkifyText(text: string): string {
  const escape = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

  // 结尾的标点通常是句子的而不是地址的，回退掉；成对的右括号留给下面单独判断。
  const url = /\b(?:https?:\/\/|www\.)[^\s<>"'`]+/gi;

  let out = "";
  let last = 0;
  for (const m of text.matchAll(url)) {
    const at = m.index ?? 0;
    let raw = m[0];
    const trailing = raw.match(/[.,;:!?'"]+$/);
    if (trailing) raw = raw.slice(0, -trailing[0].length);
    while (raw.endsWith(")") && (raw.match(/\(/g)?.length ?? 0) < (raw.match(/\)/g)?.length ?? 0)) {
      raw = raw.slice(0, -1);
    }
    if (!raw) continue;

    const href = raw.startsWith("www.") ? `https://${raw}` : raw;
    out += escape(text.slice(last, at));
    out += `<a href="#" data-external-href="${escape(href)}">${escape(raw)}</a>`;
    last = at + raw.length;
  }
  out += escape(text.slice(last));
  return out;
}

function cryptoNonce(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}
