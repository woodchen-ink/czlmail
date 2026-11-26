"use client";

import { useSyncExternalStore } from "react";

import { Events, api, onEvent } from "@/lib/api";
import { t } from "@/lib/i18n";
import type { capability, store } from "@/wailsjs/go/models";

/**
 * 账号所在服务器公布的功能 (后端 `capability.Features`, 随 `ListAccounts` 返回)。
 * 离线时是上次连上时存下的值, 从没连上过的账号全部为真, 所以离线不会把功能藏掉。
 */
export type Feature = Exclude<keyof capability.Features, "maxDelayedSend">;

/** 账号是否有某功能。旧后端没有 features 字段时按支持处理。 */
export function supports(account: Pick<store.Account, "features"> | undefined, feature: Feature): boolean {
  return account?.features?.[feature] ?? true;
}

/** 只留下有某功能的账号。 */
export function withFeature<T extends Pick<store.Account, "features">>(accounts: T[], feature: Feature): T[] {
  return accounts.filter((a) => supports(a, feature));
}

/** 主登录的个人账号: 笔记、模板同步、信任通讯录、AI 设置同步都放在它的网盘与通讯录里。 */
export function primaryPersonal(accounts: store.Account[]): store.Account | undefined {
  return accounts.find((a) => a.isPersonal && !a.signIn);
}

/** 设置 → 账户卡片上列出的不支持功能, 按界面上的叫法。 */
const FEATURE_LABELS: [Feature, () => string][] = [
  ["calendars", () => t("日历")],
  ["contacts", () => t("通讯录")],
  ["files", () => t("文件")],
  ["sieve", () => t("过滤规则")],
  ["vacation", () => t("自动回复")],
  ["submission", () => t("发送邮件")],
];

/** 账号不支持的功能名, 都支持时为空。 */
export function missingFeatures(account: Pick<store.Account, "features">): string[] {
  return FEATURE_LABELS.filter(([f]) => !supports(account, f)).map(([, label]) => label());
}

// 全部账号的共享缓存: 各处 (导航、设置、阅读栏横幅) 共用一份, 不各自去读。
let cached: store.Account[] | null = null;
const listeners = new Set<() => void>();
let stopEvents: (() => void) | null = null;

function reload() {
  api
    .listAccounts()
    .then((list) => {
      cached = list ?? [];
      listeners.forEach((l) => l());
    })
    .catch(() => {});
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  if (!stopEvents) {
    const offs = [Events.accountsChanged, Events.connected].map((e) => onEvent(e, reload));
    stopEvents = () => offs.forEach((off) => off());
    reload();
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && stopEvents) {
      stopEvents();
      stopEvents = null;
    }
  };
}

/**
 * 全部账号, 随账号变化 (登录增删、共享变化、服务器能力变化) 与重新连上时刷新。
 * 还没读到时为 null: 调用方此时应按「都支持」显示, 不要先藏再出现。
 */
export function useAccounts(): store.Account[] | null {
  return useSyncExternalStore(subscribe, () => cached, () => null);
}

/** 是否至少有一个账号有某功能; 账号还没读到或一个账号都没有时为真 (不藏)。 */
export function anyAccountSupports(accounts: store.Account[] | null, feature: Feature): boolean {
  return !accounts || accounts.length === 0 || accounts.some((a) => supports(a, feature));
}

/**
 * 主登录的个人账号有没有某功能: 笔记、模板与 AI 设置同步要它的网盘, 信任名单要它的通讯录。
 * 账号还没读到时为真。
 */
export function usePrimarySupports(feature: Feature): boolean {
  const accounts = useAccounts();
  if (!accounts) return true;
  return supports(primaryPersonal(accounts), feature);
}
