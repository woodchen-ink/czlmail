"use client";

import type { EmailSummary } from "@/lib/api";

/**
 * 最近看过的几个邮件列表 (按视图键: 统一视图|账号|文件夹), 本次运行、内存。
 * 切回去时先摆出上次的内容再静默重读, 不从骨架屏重来。只是显示用的快照:
 * 重读照样从本机库整段读, 期间的乐观更新由 mail-shell 的 overlay 叠上去。
 */

const MAX_VIEWS = 12;
/** 每个列表最多记这么多封; 更深的部分切回去后由重读补上。 */
const MAX_DEPTH = 500;

export interface CachedList {
  emails: EmailSummary[];
  hasMore: boolean;
}

const lists = new Map<string, CachedList>();

export function cachedList(view: string): CachedList | undefined {
  return lists.get(view);
}

export function rememberList(view: string, emails: EmailSummary[], hasMore: boolean) {
  lists.delete(view);
  lists.set(view, { emails: emails.slice(0, MAX_DEPTH), hasMore: hasMore || emails.length > MAX_DEPTH });
  while (lists.size > MAX_VIEWS) lists.delete(lists.keys().next().value!);
}

export function clearListCache() {
  lists.clear();
}
