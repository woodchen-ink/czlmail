"use client";

import { useEffect, useState } from "react";

import { t } from "@/lib/i18n";
import { Events, api, onEvent, type Account, type AppSettings } from "@/lib/api";

/** 账号 (或登录) 在本机的显示名与颜色, 设置 → 账户里改; 存在设置 accountLooks 里。 */
export type AccountLooks = AppSettings["accountLooks"];

/** accountLooks 里登录一节标题的键: "signin:" + 登录前缀。 */
export const signInLookKey = (signIn: string) => `signin:${signIn}`;

/**
 * 可选的账号颜色: 键名存设置 (与后端 accountColors 一致), 色值在浅色、深色主题下都看得清。
 * 只做一条细竖线和小圆点, 不铺底色, 所以一个色值两种主题共用。
 */
export const ACCOUNT_COLORS: { key: string; value: string; label: () => string }[] = [
  { key: "red", value: "#e03131", label: () => t("红色") },
  { key: "orange", value: "#e8590c", label: () => t("橙色") },
  { key: "amber", value: "#f59f00", label: () => t("黄色") },
  { key: "green", value: "#2f9e44", label: () => t("绿色") },
  { key: "teal", value: "#0c8599", label: () => t("青色") },
  { key: "blue", value: "#1c7ed6", label: () => t("蓝色") },
  { key: "violet", value: "#7048e8", label: () => t("紫色") },
  { key: "pink", value: "#d6336c", label: () => t("粉色") },
  { key: "gray", value: "#868e96", label: () => t("灰色") },
];

/** 颜色键名对应的色值, 没设或不认识时为 undefined。 */
export function accountColorValue(key: string | undefined): string | undefined {
  return key ? ACCOUNT_COLORS.find((c) => c.key === key)?.value : undefined;
}

/** 账号的显示名: 用户起的名字, 没起就用服务器给的。 */
export function accountName(acc: Pick<Account, "id" | "name">, looks: AccountLooks | undefined): string {
  return looks?.[acc.id]?.name || acc.name;
}

/** 账号颜色的色值。 */
export function accountColor(id: string, looks: AccountLooks | undefined): string | undefined {
  return accountColorValue(looks?.[id]?.color);
}

/** 读设置里的账号名与颜色, 设置变了跟着更新。 */
export function useAccountLooks(): AccountLooks {
  const [looks, setLooks] = useState<AccountLooks>({});
  useEffect(() => {
    const load = () =>
      api
        .getSettings()
        .then((s) => setLooks(s.accountLooks ?? {}))
        .catch(() => {});
    load();
    return onEvent(Events.settingsChanged, load);
  }, []);
  return looks;
}
