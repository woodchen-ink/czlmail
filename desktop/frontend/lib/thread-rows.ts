import type { EmailSummary } from "@/lib/api";

/** 会话分组的键「账号/会话 id」(统一视图里不同账号的会话 id 可能相同)。没有会话 id 的邮件自成一组。 */
export function threadKey(e: { accountId: string; threadId?: string; id: string }): string {
  return `${e.accountId}/${e.threadId || e.id}`;
}

export interface ThreadRow {
  /** 代表会话的那封: 会话里最新的一封。 */
  head: EmailSummary;
  /** 会话里有未读。 */
  unread: boolean;
}

/**
 * 邮件列表的可见行: 同一会话只占一行, 位置取它在 emails 里第一次出现处。
 * 列表渲染与键盘上下移动共用, 否则方向键会选中折叠在会话里、屏幕上没有的那封。
 */
export function threadRows(emails: EmailSummary[]): ThreadRow[] {
  const byThread = new Map<string, ThreadRow>();
  const out: ThreadRow[] = [];
  for (const e of emails) {
    const key = threadKey(e);
    const existing = byThread.get(key);
    if (existing) {
      existing.unread ||= e.isUnread;
      if (new Date(e.receivedAt).getTime() > new Date(existing.head.receivedAt).getTime()) existing.head = e;
      continue;
    }
    const row = { head: e, unread: e.isUnread };
    byThread.set(key, row);
    out.push(row);
  }
  return out;
}
