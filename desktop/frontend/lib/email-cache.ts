"use client";

import { api, type EmailDetail } from "@/lib/api";

/**
 * 阅读栏的邮件快照 (本次运行, 内存): 带正文的详情 + 发件人是否受信任。
 *
 * 打开一封邮件要依次问后端详情、信任状态, 正文没落库时再取正文。看过的或预热过的邮件
 * 先用快照渲染, 不出骨架屏; 阅读栏照常向后端重读, 已读、星标等变化随后替换上来。
 */

export interface CachedEmail {
  detail: EmailDetail;
  trusted: boolean;
}

const MAX_ENTRIES = 80;
const entries = new Map<string, CachedEmail>();

const keyOf = (accountId: string, id: string) => `${accountId}/${id}`;

export function cachedEmail(accountId: string, id: string): CachedEmail | undefined {
  return entries.get(keyOf(accountId, id));
}

/** 只记正文已取到的: 没有正文的快照照样要等网络, 记了也省不下什么。 */
export function rememberEmail(accountId: string, detail: EmailDetail, trusted: boolean) {
  if (!detail.bodyFetched) return;
  const k = keyOf(accountId, detail.id);
  entries.delete(k);
  entries.set(k, { detail, trusted });
  while (entries.size > MAX_ENTRIES) entries.delete(entries.keys().next().value!);
}

/** 信任名单变了: 快照里的信任状态都可能过时, 整个丢掉 (重读本机库很快)。 */
export function forgetCachedEmails() {
  entries.clear();
}

/**
 * 预热相邻邮件的快照。只读本机库: 正文还没落库的跳过 (由后端的 PrefetchBodies 去取),
 * 下一次选中变化时再来一遍就补上了。
 */
export async function warmEmails(accountId: string, ids: string[]) {
  for (const id of ids) {
    if (entries.has(keyOf(accountId, id))) continue;
    try {
      const detail = await api.getEmail(accountId, id);
      if (!detail?.bodyFetched) continue;
      const from = detail.from?.[0]?.email ?? "";
      const trusted = from ? await api.isSenderTrusted(accountId, from) : false;
      rememberEmail(accountId, detail, trusted);
    } catch {
      // 预热失败不影响什么, 打开时照常加载。
    }
  }
}
