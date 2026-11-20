"use client";

import { useCallback, useEffect, useSyncExternalStore } from "react";

import { api } from "@/lib/api";
import {
  getLocale, getServerLocale, LOCALE_KEY, preferredLocale, restoreLocale,
  setLocale, subscribeLocale, translate,
} from "@/lib/i18n";

/** Subscribe each translated view without recreating its state on language changes. */
export function useI18n() {
  const locale = useSyncExternalStore(subscribeLocale, getLocale, getServerLocale);
  const t = useCallback((source: string, ...values: readonly unknown[]) => translate(locale, source, ...values), [locale]);
  return { locale, setLocale, t };
}

/** Restore after hydration and synchronize preferences changed by another window. */
export function LocaleProvider({ children }: { children: React.ReactNode }) {
  useEffect(() => {
    // 后端自己发的系统通知 (如新版本) 按界面语言出文案; 浏览器里开发时没有运行时, 忽略即可。
    const report = () => { api.setUILocale(getLocale()).catch(() => {}); };
    const unsubscribe = subscribeLocale(report);
    restoreLocale();
    report();
    const handleStorage = (event: StorageEvent) => {
      if (event.key === LOCALE_KEY || event.key === null) setLocale(preferredLocale(event.newValue), false);
    };
    window.addEventListener("storage", handleStorage);
    return () => {
      unsubscribe();
      window.removeEventListener("storage", handleStorage);
    };
  }, []);
  return children;
}
