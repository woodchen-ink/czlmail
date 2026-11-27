"use client";

import { useCallback, useEffect, useState } from "react";

import { t } from "@/lib/i18n";
import { Events, api, onEvent, type Account } from "@/lib/api";
import type { main } from "@/wailsjs/go/models";

/**
 * 账号 (或登录那一节) 的显示名与颜色, 设置 → 账户 → 名称与颜色里改。
 * 键是本机账号 id, 登录那一节是 "signin:" + 登录前缀; 后端换算成服务器上的键经个人网盘同步到别的设备。
 */
export type AccountLooks = Record<string, main.AccountLook>;

/** 登录那一节在 AccountLooks 里的键。 */
export const signInLookKey = (signIn: string) => `signin:${signIn}`;

/**
 * 可选的账号颜色: 键名存设置 (与后端 accountColors 一致), 色值是 globals.css 的 --account-* (取日历那套低饱和色),
 * 暗色主题自动切换。只画小圆点与细线, 不铺底色。
 */
export const ACCOUNT_COLORS: { key: string; value: string; label: () => string }[] = [
  { key: "blue", value: "var(--account-blue)", label: () => t("蓝色") },
  { key: "green", value: "var(--account-green)", label: () => t("绿色") },
  { key: "amber", value: "var(--account-amber)", label: () => t("黄色") },
  { key: "violet", value: "var(--account-violet)", label: () => t("紫色") },
  { key: "red", value: "var(--account-red)", label: () => t("红色") },
  { key: "gray", value: "var(--account-gray)", label: () => t("灰色") },
];

/** 颜色键名对应的色值, 没设或不认识时为 undefined。 */
export function accountColorValue(key: string | undefined): string | undefined {
  return key ? ACCOUNT_COLORS.find((c) => c.key === key)?.value : undefined;
}

/** 账号的显示名: 用户起的名字, 没起就用服务器给的。 */
export function accountName(acc: Pick<Account, "id" | "name">, looks: AccountLooks): string {
  return looks[acc.id]?.name || acc.name;
}

/** 账号颜色的色值。 */
export function accountColor(id: string, looks: AccountLooks): string | undefined {
  return accountColorValue(looks[id]?.color);
}

/** 读账号名与颜色; 本机修改与别的设备同步过来时 (都发 settings:changed) 跟着更新。 */
export function useAccountLooks(): AccountLooks {
  return useAccountLooksState().looks;
}

/** 同 useAccountLooks, 另给一个立即重读的 reload (设置页保存后用, 不等事件)。 */
export function useAccountLooksState(): { looks: AccountLooks; reload: () => Promise<void> } {
  const [looks, setLooks] = useState<AccountLooks>({});
  const reload = useCallback(
    () =>
      api
        .getAccountLooks()
        .then((l) => setLooks(l ?? {}))
        .catch(() => {}),
    [],
  );
  useEffect(() => {
    void reload();
    const offSettings = onEvent(Events.settingsChanged, reload);
    // 登录增减或会话里的账号变了, 本机 id 与服务器键的对应跟着变。
    const offAccounts = onEvent(Events.accountsChanged, reload);
    return () => {
      offSettings();
      offAccounts();
    };
  }, [reload]);
  return { looks, reload };
}
