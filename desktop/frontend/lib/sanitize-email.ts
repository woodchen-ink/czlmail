import DOMPurify from "dompurify";

/**
 * 邮件正文的消毒与改写。
 *
 * 邮件 HTML 是攻击面最大的一类输入：内容完全由发件人控制，而用户只是「打开一封信」。
 * 这里做三件事，缺一不可：
 *
 *   1. DOMPurify 按允许列表清洗，去掉脚本、事件处理器、危险协议；
 *   2. 远程图片默认拆掉——它们是追踪像素的主要载体，一次加载就等于向发件人确认
 *      「这封信被谁、在什么时候、用什么 IP 打开了」；
 *   3. 链接改写成由宿主拦截的形式，避免在应用内导航。
 *
 * 清洗之后仍然要放进沙箱 iframe，并配 CSP。任何单层防护都可能有洞，
 * 这三层里每一层被绕过时，另外两层还在。
 */

export interface SanitizeResult {
  html: string;
  /** 被拦下的远程图片数量，供界面提示「图片已屏蔽」。 */
  blockedImages: number;
}

/** 允许直接渲染的 URL 协议。data: 只允许图片，且在 CSP 里另有约束。 */
const SAFE_PROTOCOL = /^(https?:|mailto:|cid:|data:image\/)/i;

/**
 * @param inlineImages cid → 本地预览 URL。邮件自带的内嵌图片（签名 logo、截图）
 *   以 cid: 引用，它们随邮件一起下载、不向外发请求，因此不受远程图片屏蔽约束。
 */
export function sanitizeEmailHtml(
  html: string,
  allowRemoteImages: boolean,
  inlineImages: Record<string, string> = {},
): SanitizeResult {
  let blockedImages = 0;

  const purify = DOMPurify(window);

  // 必须在属性校验之前改写：DOMPurify 默认不认 cid: 协议，放到 afterSanitize
  // 阶段时 src 已经被当作非法 URL 删掉了。
  purify.addHook("uponSanitizeAttribute", (node, data) => {
    if (data.attrName !== "src" || !(node instanceof Element) || node.tagName !== "IMG") return;
    const m = /^cid:(.+)$/i.exec(data.attrValue.trim());
    if (!m) return;
    const url = inlineImages[m[1].replace(/^<|>$/g, "").toLowerCase()];
    if (url) {
      data.attrValue = url;
      node.setAttribute("data-inline-image", "");
    }
  });

  purify.addHook("afterSanitizeAttributes", (node) => {
    if (!(node instanceof Element)) return;

    if (node.tagName === "A") {
      rewriteLink(node);
      return;
    }

    if (node.tagName === "IMG") {
      if (!allowRemoteImages) {
        if (stripRemoteImage(node)) blockedImages += 1;
      } else {
        proxyRemoteImage(node);
      }
      return;
    }

    // 背景图同样会发出远程请求，是被最常忽略的追踪通道。
    if (!allowRemoteImages) {
      stripRemoteBackground(node);
    }
  });

  const clean = purify.sanitize(hoistDocument(html), {
    // 明确禁掉的结构：表单可以诱导用户提交凭据，iframe 可以嵌任意站点，
    // base 能改变整篇文档里所有相对链接的指向。
    FORBID_TAGS: ["script", "iframe", "object", "embed", "form", "input", "button", "base", "meta"],
    FORBID_ATTR: ["srcset", "ping", "formaction"],
    ALLOW_DATA_ATTR: false,
    // 邮件排版重度依赖 table 与内联样式，保留它们，
    // 隔离交给沙箱 iframe，而不是靠剥掉样式。
    // 自己加的标记属性要显式放行：ALLOW_DATA_ATTR 为 false 时它们会在同一轮里被剥掉。
    ADD_ATTR: ["target", "rel", "data-external-href", "data-inline-image", "data-blocked-src", "data-email-body", "data-czl-tr"],
    WHOLE_DOCUMENT: false,
    // 开头的 <style> 不加这个会被解析进 <head>，随后和 head 一起被丢掉。
    FORCE_BODY: true,
    RETURN_DOM: false,
  });

  purify.removeAllHooks();

  return { html: String(clean), blockedImages };
}

/**
 * 把完整 HTML 文档压平成可以放进 body 的片段。
 *
 * DOMPurify 在 WHOLE_DOCUMENT 为 false 时只返回 body 的内容，而营销与通知邮件几乎都把
 * CSS 写在 <head> 的 <style> 里、把底色写在 <body bgcolor> 上——直接清洗会把排版
 * 连同底色一起丢掉，表现为「版式全乱、白底变透明」。这里把 head 里的样式挪到正文开头，
 * 把 body 上的底色与文字色转成外层 div 的内联样式，之后照常走允许列表清洗。
 *
 * DOMParser 生成的是惰性文档：不执行脚本、不加载图片，解析本身不会发出任何请求。
 */
function hoistDocument(html: string): string {
  if (!/<(html|head|body)[\s>]/i.test(html)) return html;

  const doc = new DOMParser().parseFromString(html, "text/html");
  const styles = Array.from(doc.head.querySelectorAll("style"))
    .map((el) => `<style>${el.textContent ?? ""}</style>`)
    .join("");

  const body = doc.body;
  const css: string[] = [];
  const bg = body.getAttribute("bgcolor");
  if (bg && /^[#a-z0-9]+$/i.test(bg)) css.push(`background-color:${bg}`);
  const text = body.getAttribute("text");
  if (text && /^[#a-z0-9]+$/i.test(text)) css.push(`color:${text}`);
  const inline = body.getAttribute("style");
  if (inline) css.push(inline);
  const background = body.getAttribute("background");

  // 包装节点必须建在 DOMParser 的惰性文档里，并以移动节点而不是 innerHTML 赋值的方式填充：
  // 在应用自己的 document 上建元素再写 innerHTML，<img onerror> 会在应用的源里立即触发。
  const wrapper = doc.createElement("div");
  wrapper.setAttribute("data-email-body", "");
  if (css.length) wrapper.setAttribute("style", css.join(";"));
  if (background) wrapper.setAttribute("background", background);
  while (body.firstChild) wrapper.appendChild(body.firstChild);

  return styles + wrapper.outerHTML;
}

/**
 * 判断邮件是否自带配色。
 *
 * 自带底色或文字色的邮件是按亮色背景设计的，套上暗色主题的浅色字会出现
 * 「白底白字」或「深色表格里深色字」。这类邮件在暗色模式下仍按亮色纸面渲染，
 * 只有纯排版、不设颜色的邮件才跟随暗色。
 */
export function hasOwnColors(html: string): boolean {
  return /\bbgcolor\s*=|background(-color)?\s*:|(^|[\s;"'{])color\s*:/i.test(html);
}

/**
 * 链接改写：href 移到 data 属性上，由沙箱内的桥接脚本拦截并交给宿主打开。
 *
 * 保留原 href 会让点击在 iframe 内部导航，用户会在阅读窗格里看到一个
 * 完整的外部网站——那是钓鱼页面最理想的展示位。
 */
function rewriteLink(node: Element) {
  const href = node.getAttribute("href") ?? "";
  node.removeAttribute("href");

  if (!href || !SAFE_PROTOCOL.test(href)) return;

  node.setAttribute("data-external-href", href);
  node.setAttribute("rel", "noopener noreferrer");
}

/** 返回是否真的拦下了一张远程图片。 */
function stripRemoteImage(node: Element): boolean {
  if (node.hasAttribute("data-inline-image")) return false;
  const src = node.getAttribute("src") ?? "";
  // 内嵌图片（data: 与邮件自带的 cid:）不外发请求，不必拦。
  if (!src || /^(data:|cid:)/i.test(src)) return false;

  node.removeAttribute("src");
  node.setAttribute("data-blocked-src", src);
  return true;
}

/**
 * 允许加载的远程图片改走本地代理 /czl-remote: 由 Go 下载并存进磁盘缓存,
 * 再次打开同一封邮件不必重新下载。代理只连公网、只返回嗅探得出的图片。
 * 只接管 https —— http 图片 CSP 本来就不放行。背景图仍直连。
 */
function proxyRemoteImage(node: Element) {
  if (node.hasAttribute("data-inline-image")) return;
  const src = (node.getAttribute("src") ?? "").trim();
  if (!/^https:\/\//i.test(src)) return;
  node.setAttribute("src", `${window.location.origin}/czl-remote?url=${encodeURIComponent(src)}`);
}

function stripRemoteBackground(node: Element) {
  const style = node.getAttribute("style");
  if (style && /url\(/i.test(style)) {
    node.setAttribute("style", style.replace(/url\([^)]*\)/gi, "none"));
  }
  if (node.hasAttribute("background")) {
    node.removeAttribute("background");
  }
}
