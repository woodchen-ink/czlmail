import { t } from "@/lib/i18n";

/** The fields of a note entry or problem that describe its last synchronization attempt. */
type SyncFields = { status?: string; syncError?: string; syncAttempts?: number };

/** A temporary failure is only worth showing once it keeps failing; mirrors `visibleAfter` in the backend. */
const visibleAfter = 3;

/** The failure kind to show for an entry, or "" when synchronization is fine or merely queued. */
export function noteFailure(entry: SyncFields): string {
  const kind = entry.syncError ?? "";
  if (!kind) return "";
  if (kind === "temporary" && (entry.syncAttempts ?? 0) < visibleAfter) return "";
  return kind;
}

/** Whether automatic retries continue for this kind. */
export function failureIsTemporary(kind: string): boolean {
  return kind === "temporary";
}

/** Short state word for the title row. */
export function failureLabel(kind: string): string {
  return kind === "conflict" ? t("存在版本冲突") : failureIsTemporary(kind) ? t("暂时无法同步") : t("同步失败");
}

/** One sentence a user can act on; the server's own words stay in the details. */
export function failureText(kind: string): string {
  switch (kind) {
    case "conflict": return t("本机与服务器上的内容不一致，请对比后选择。");
    case "temporary": return t("暂时连不上服务器，稍后会自动重试。内容仍保存在本机。");
    case "forbidden": return t("没有写入权限，无法上传。内容仍保存在本机。");
    case "quota": return t("网盘空间不足，无法上传。内容仍保存在本机。");
    case "too_large": return t("文件超过服务器的大小上限，无法同步。");
    case "missing": return t("本机的附件文件已丢失，无法上传。");
    default: return t("服务器不接受这次写入。内容仍保存在本机。");
  }
}
