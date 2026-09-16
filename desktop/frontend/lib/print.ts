import { sanitizeEmailHtml } from "@/lib/sanitize-email";
import { displayAddressList, fullDate } from "@/lib/format";
import type { EmailDetail } from "@/lib/api";

/**
 * 打印一封邮件。
 *
 * 打印用一个临时 iframe，sandbox 给 allow-same-origin 与 allow-modals、但**不给**
 * allow-scripts：父页面需要同源才能调用它的 print()，而不允许脚本意味着即使
 * 正文里混进了脚本也不会执行。远程图片保持屏蔽 —— 打印不该成为绕过追踪防护的后门。
 */
export function printEmail(email: EmailDetail, allowRemoteImages: boolean) {
  const body = email.bodyHtml
    ? sanitizeEmailHtml(email.bodyHtml, allowRemoteImages).html
    : `<pre style="white-space:pre-wrap;font:13px/1.6 ui-monospace,monospace">${escapeHtml(email.bodyText ?? "")}</pre>`;

  const header = `
    <h1 style="font-size:18px;margin:0 0 8px">${escapeHtml(email.subject || "(无主题)")}</h1>
    <p style="margin:2px 0;color:#687280;font-size:12px">发件人：${escapeHtml(displayAddressList(email.from))}</p>
    <p style="margin:2px 0;color:#687280;font-size:12px">收件人：${escapeHtml(displayAddressList(email.to))}</p>
    <p style="margin:2px 0 12px;color:#687280;font-size:12px">时间：${escapeHtml(fullDate(email.receivedAt))}</p>
    <hr style="border:0;border-top:1px solid #D5DCE5;margin:0 0 16px">`;

  const img = allowRemoteImages ? "img-src https: data:;" : "img-src data:;";
  const doc = `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; ${img} style-src 'unsafe-inline'; script-src 'none';">
<title>${escapeHtml(email.subject || "邮件")}</title>
<style>body{font:14px/1.6 system-ui,sans-serif;color:#10131A;margin:24px}img{max-width:100%}</style>
</head><body>${header}${body}</body></html>`;

  const frame = document.createElement("iframe");
  frame.setAttribute("sandbox", "allow-same-origin allow-modals");
  frame.style.cssText = "position:fixed;width:0;height:0;border:0;visibility:hidden";
  frame.srcdoc = doc;
  frame.onload = () => {
    frame.contentWindow?.focus();
    frame.contentWindow?.print();
    // 打印对话框是同步阻塞的，返回即表示用户已处理完毕。
    setTimeout(() => frame.remove(), 1000);
  };
  document.body.appendChild(frame);
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
