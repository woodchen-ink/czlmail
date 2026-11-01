/**
 * 邮件列表里一封邮件的标识「账号/id」。
 *
 * 统一视图把各账号的邮件混在一个列表里, 而 JMAP 的邮件 id 只在账号内唯一(不同共享邮箱
 * 完全可能出现同一个 id), 所以选中、多选、乐观更新一律按这个键区分, 不能只用 id。
 * 账号 id 是 JMAP Id(字母数字与 -_), 不含 "/"。
 */
export function emailKey(e: { accountId: string; id: string }): string {
  return `${e.accountId}/${e.id}`;
}

export function parseEmailKey(key: string): { accountId: string; id: string } {
  const i = key.indexOf("/");
  return i < 0 ? { accountId: "", id: key } : { accountId: key.slice(0, i), id: key.slice(i + 1) };
}

/** 按账号分组, 写操作要逐个账号提交。 */
export function groupByAccount(keys: Iterable<string>): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const key of keys) {
    const { accountId, id } = parseEmailKey(key);
    const list = out.get(accountId);
    if (list) list.push(id);
    else out.set(accountId, [id]);
  }
  return out;
}
