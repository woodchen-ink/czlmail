import type { DragEvent } from "react";

import { t } from "@/lib/i18n";
import type { EmailSummary, Mailbox } from "@/lib/api";
import { emailKey } from "@/lib/email-key";

/**
 * 把邮件从列表拖到侧栏文件夹上移动。
 *
 * 拖动中的邮件记在模块里而不是 dataTransfer: dragover 时浏览器不给读数据, 只给类型,
 * 而判断能不能放下(属于哪个账号、是不是已经在那个文件夹)正要用到它们。
 * dataTransfer 里只放一个自有类型作为标记, 别的窗口拖进来的东西不会被当成邮件。
 */
export const EMAIL_DRAG_TYPE = "application/x-czlmail-emails";

export interface DraggedEmail {
  key: string;
  accountId: string;
  mailboxIds: string[];
}

let current: DraggedEmail[] | null = null;
const listeners = new Set<(items: DraggedEmail[] | null) => void>();

function publish(items: DraggedEmail[] | null) {
  current = items;
  for (const fn of listeners) fn(items);
}

/** 拖动开始、结束时回调, 供列表把拖走的行变淡、侧栏展开收起的分组。 */
export function onEmailDrag(fn: (items: DraggedEmail[] | null) => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** 这次拖动是不是本程序列表里的邮件; 是就返回它们。 */
export function draggedEmails(e: DragEvent): DraggedEmail[] | null {
  return current && e.dataTransfer.types.includes(EMAIL_DRAG_TYPE) ? current : null;
}

/**
 * 能否放到 accountId 账号的 box 文件夹: 文件夹属于各自的账号, 跨账号不能移;
 * 草稿、已计划不是移进去的地方; 全部都已经在这个文件夹里时也没有可移的。
 */
export function canDropEmails(items: DraggedEmail[], accountId: string, box: Mailbox): boolean {
  if (box.kind === "drafts" || box.kind === "scheduled") return false;
  if (box.myRights?.mayAddItems === false) return false;
  if (!items.every((i) => i.accountId === accountId)) return false;
  return items.some((i) => !i.mailboxIds.includes(box.id));
}

/** 放下后真正要移动的: 已经在目标文件夹里的不再移。 */
export function keysToMove(items: DraggedEmail[], boxId: string): string[] {
  return items.filter((i) => !i.mailboxIds.includes(boxId)).map((i) => i.key);
}

export function startEmailDrag(e: DragEvent, emails: EmailSummary[]) {
  const items = emails.map((m) => ({ key: emailKey(m), accountId: m.accountId, mailboxIds: m.mailboxIds ?? [] }));
  e.dataTransfer.effectAllowed = "move";
  // 只放自有类型: 带上 text/plain 的话, 松手在写信的正文里会插进一段文字。
  e.dataTransfer.setData(EMAIL_DRAG_TYPE, items.map((i) => i.key).join("\n"));
  const ghost = dragGhost(emails);
  document.body.appendChild(ghost);
  // 指针落在卡片左上角外侧, 卡片在指针右下方, 不挡住指针下面要放的文件夹。
  e.dataTransfer.setDragImage(ghost, 6, 6);
  // 浏览器在 dragstart 里就把元素拍成图片, 之后可以移除。
  setTimeout(() => ghost.remove(), 0);
  publish(items);
}

export function endEmailDrag() {
  if (current) publish(null);
}

const MAIL_ICON =
  '<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="20" height="16" x="2" y="4" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/></svg>';

/**
 * 拖动时跟着指针的卡片: 不透明、带强调色边框和阴影, 比半透明的整行(Outlook)醒目;
 * 多封时叠成一摞并在角上标出数量。
 * 用行内样式与主题变量, 不依赖 Tailwind 扫描这个文件里的类名。
 */
function dragGhost(emails: EmailSummary[]): HTMLElement {
  const first = emails[0];
  const many = emails.length > 1;
  const wrap = document.createElement("div");
  wrap.setAttribute("aria-hidden", "true");
  Object.assign(wrap.style, {
    position: "fixed",
    top: "-1000px",
    left: "-1000px",
    padding: "18px 18px 14px 18px",
    pointerEvents: "none",
    zIndex: "2147483647",
  } satisfies Partial<CSSStyleDeclaration>);

  const card = (offset: number) => {
    const el = document.createElement("div");
    Object.assign(el.style, {
      position: offset ? "absolute" : "relative",
      inset: offset ? `${18 + offset}px ${18 - offset}px ${14 - offset}px ${18 + offset}px` : "",
      width: "260px",
      boxSizing: "border-box",
      background: "var(--card)",
      color: "var(--card-foreground)",
      border: "2px solid var(--accent)",
      borderRadius: "10px",
      boxShadow: offset ? "" : "0 6px 18px rgba(0,0,0,.28)",
    } satisfies Partial<CSSStyleDeclaration>);
    return el;
  };

  if (many) {
    wrap.appendChild(card(8));
    wrap.appendChild(card(4));
  }

  const front = card(0);
  Object.assign(front.style, {
    display: "flex",
    alignItems: "center",
    gap: "10px",
    padding: "8px 12px",
    font: "500 13px/1.3 system-ui, 'Segoe UI', sans-serif",
  } satisfies Partial<CSSStyleDeclaration>);

  const icon = document.createElement("span");
  icon.innerHTML = MAIL_ICON;
  Object.assign(icon.style, { color: "var(--accent)", display: "flex", flexShrink: "0" });
  front.appendChild(icon);

  const text = document.createElement("div");
  Object.assign(text.style, { minWidth: "0", flex: "1" });
  const line = (value: string, muted: boolean) => {
    const el = document.createElement("div");
    el.textContent = value;
    Object.assign(el.style, {
      overflow: "hidden",
      whiteSpace: "nowrap",
      textOverflow: "ellipsis",
      ...(muted ? { color: "var(--muted-foreground)", fontWeight: "400", fontSize: "12px" } : {}),
    });
    return el;
  };
  text.appendChild(line(first?.subject || t("(无主题)"), false));
  const sender = first?.from?.[0];
  text.appendChild(
    line(many ? t("{0}封邮件", emails.length) : sender?.name || sender?.email || t("(无发件人)"), true),
  );
  front.appendChild(text);
  wrap.appendChild(front);

  if (many) {
    const badge = document.createElement("div");
    badge.textContent = String(emails.length);
    Object.assign(badge.style, {
      position: "absolute",
      top: "6px",
      right: "4px",
      minWidth: "24px",
      height: "24px",
      padding: "0 6px",
      boxSizing: "border-box",
      borderRadius: "12px",
      background: "var(--accent)",
      color: "var(--accent-foreground)",
      font: "600 12px/24px system-ui, 'Segoe UI', sans-serif",
      textAlign: "center",
      boxShadow: "0 0 0 2px var(--card)",
    } satisfies Partial<CSSStyleDeclaration>);
    wrap.appendChild(badge);
  }
  return wrap;
}
