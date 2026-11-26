import { Cloud, Server, type LucideIcon } from "lucide-react";

import { t } from "@/lib/i18n";
import type { store, upstream } from "@/wailsjs/go/models";

/**
 * czlmail 网关账号背后上游邮箱的状态 (后端 `upstream.Info`, 随 `ListAccounts` 的 `upstream` 返回)。
 * 普通 JMAP 服务器的账号没有这一项。状态变化时网关改会话, 客户端重读后经 accounts-changed 刷新。
 */
export type UpstreamStatus = "ok" | "syncing" | "authRequired" | "unreachable" | "paused";

/** 账号是不是网关账号。 */
export function isGatewayAccount(account: Pick<store.Account, "upstream"> | undefined): boolean {
  return !!account?.upstream;
}

/** 上游是否正常 (不是网关账号也算正常)。 */
export function upstreamOK(info: upstream.Info | undefined): boolean {
  return !info || !info.status || info.status === "ok";
}

/** 首次导入的百分比, 没有进度时为 null。 */
export function initialSyncPercent(info: upstream.Info | undefined): number | null {
  const p = info?.initialSync;
  if (!p || p.total <= 0) return null;
  return Math.min(100, Math.floor((p.done / p.total) * 100));
}

/**
 * 非正常状态的短标签与是否算问题 (要用户处理的标红, 等着就会好的灰色)。正常时为 null。
 * 不认识的新状态按「暂时不可用」显示, 不藏起来。
 */
export function upstreamLabel(info: upstream.Info | undefined): { text: string; problem: boolean } | null {
  if (upstreamOK(info) || !info) return null;
  switch (info.status as UpstreamStatus) {
    case "authRequired":
      return { text: t("需要重新授权"), problem: true };
    case "unreachable":
      return info.code === 42910 ? { text: t("被邮箱服务商限流"), problem: false } : { text: t("连不上"), problem: true };
    case "syncing": {
      const pct = initialSyncPercent(info);
      return { text: pct === null ? t("同步中") : t("同步中 {0}%", pct), problem: false };
    }
    case "paused":
      return { text: t("已暂停"), problem: false };
  }
  return { text: t("暂时不可用"), problem: true };
}

/** 状态的一句说明, 显示在空列表与设置里。 */
export function upstreamDescription(info: upstream.Info | undefined): string {
  if (upstreamOK(info) || !info) return "";
  switch (info.status as UpstreamStatus) {
    case "authRequired":
      return t("网关里这个邮箱的授权已失效，请到网关后台重新授权。其它邮箱照常同步。");
    case "unreachable":
      return info.code === 42910
        ? t("邮箱服务商暂时限制了网关的访问，稍后会自动恢复。")
        : t("网关连不上这个邮箱的服务器，恢复后会自动同步。");
    case "syncing":
      return t("网关正在首次导入这个邮箱，完成后邮件会自动出现。");
    case "paused":
      return t("这个邮箱在网关里已暂停，恢复后会自动同步。");
  }
  return t("这个邮箱暂时不可用。");
}

/** 上游类型的名称与图标; 不认识的类型按通用邮箱。 */
export function upstreamKind(info: upstream.Info | undefined): { label: string; icon: LucideIcon } | null {
  if (!info) return null;
  switch (info.kind) {
    case "imap":
      return { label: "IMAP", icon: Server };
    case "gmail":
      return { label: "Gmail", icon: Cloud };
    case "microsoft":
      return { label: "Microsoft", icon: Cloud };
  }
  return { label: info.kind || t("邮箱"), icon: Server };
}
