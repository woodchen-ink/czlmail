"use client";

import { useCallback, useEffect, useSyncExternalStore } from "react";
import {
  getLocale, getServerLocale, LOCALE_KEY, normalizeLocale, restoreLocale,
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
    restoreLocale();
    const handleStorage = (event: StorageEvent) => {
      if (event.key === LOCALE_KEY || event.key === null) setLocale(normalizeLocale(event.newValue), false);
    };
    window.addEventListener("storage", handleStorage);
    return () => window.removeEventListener("storage", handleStorage);
  }, []);
  return children;
}
