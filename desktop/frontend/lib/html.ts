import DOMPurify from "dompurify";

export function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

/** 纯文本转成段落 HTML: 空行分段, 单个换行变 <br>。 */
export function textToHtml(text: string): string {
  const paras = text.replace(/\r\n/g, "\n").split(/\n{2,}/);
  return paras.map((p) => `<p>${escapeHtml(p).replace(/\n/g, "<br>")}</p>`).join("");
}

/** HTML 转纯文本, 用于生成邮件的 text/plain 部分与交给 AI。解析在惰性文档里进行。 */
export function htmlToText(html: string): string {
  if (!html) return "";
  const doc = new DOMParser().parseFromString(html, "text/html");
  doc.querySelectorAll("br").forEach((br) => br.replaceWith("\n"));
  doc.querySelectorAll("p, div, h1, h2, h3, li, tr, blockquote, pre").forEach((el) => el.append("\n"));
  doc.querySelectorAll("li").forEach((li) => li.prepend("• "));
  return (doc.body.textContent ?? "").replace(/\n{3,}/g, "\n\n").trim();
}

/**
 * 清洗签名与引用这类要内联显示在应用里的 HTML。
 * 与邮件正文不同, 它们来自用户自己或已经显示过的邮件, 仍然去掉脚本、表单与事件处理器。
 */
export function sanitizeInline(html: string): string {
  return DOMPurify.sanitize(html, {
    FORBID_TAGS: ["script", "iframe", "object", "embed", "form", "input", "button", "style", "link", "meta", "base"],
    FORBID_ATTR: ["srcset", "ping", "formaction"],
  });
}

export function isHtmlEmpty(html: string): boolean {
  return htmlToText(html).trim() === "" && !/<img/i.test(html);
}

/**
 * 把本应用保存的草稿 HTML 拆回「正文 / 引用」。
 * 发送与存草稿时签名包在 div.signature 里、引用包在 div.czl-quote 里, 重新打开草稿时
 * 去掉签名(写信面板会按当前身份重新附上)并取出引用, 避免签名与引用叠加进编辑器。
 */
export function splitDraftHtml(html: string): { body: string; quote: string } {
  const doc = new DOMParser().parseFromString(html, "text/html");
  const quoteEl = doc.body.querySelector("div.czl-quote");
  const quote = quoteEl?.outerHTML ?? "";
  quoteEl?.remove();
  const sig = doc.body.querySelector("div.signature");
  if (sig) {
    const prev = sig.previousElementSibling;
    if (prev?.tagName === "P" && prev.textContent?.trim() === "--") prev.remove();
    sig.remove();
  }
  return { body: doc.body.innerHTML, quote };
}
