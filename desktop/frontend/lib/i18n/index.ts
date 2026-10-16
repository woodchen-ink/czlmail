import english from "./en.json";

export type Locale = "zh-CN" | "en";
export const LOCALE_KEY = "czlmail.locale";
export const LOCALES = [["zh-CN", "简体中文"], ["en", "English"]] as const;

let currentLocale: Locale = "zh-CN";
const listeners = new Set<() => void>();
const messages: Readonly<Record<string, string>> = english;

/** Reject unsupported or damaged preferences rather than using them as locale identifiers. */
export function normalizeLocale(value: unknown): Locale {
  return value === "en" ? "en" : "zh-CN";
}

/** Current in-memory preference; initialized after hydration by LocaleProvider. */
export function getLocale(): Locale {
  return currentLocale;
}

/** Static export and hydration always start with the same Chinese snapshot. */
export function getServerLocale(): Locale {
  return "zh-CN";
}

/** Subscribe without remounting application views or losing unsaved drafts. */
export function subscribeLocale(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

/** Change language immediately; storage failure only affects the next launch. */
export function setLocale(value: Locale, persist = true): void {
  const next = normalizeLocale(value);
  if (typeof document !== "undefined") document.documentElement.lang = next;
  if (persist && typeof window !== "undefined") {
    try { window.localStorage.setItem(LOCALE_KEY, next); } catch { /* Session preference still works. */ }
  }
  if (next === currentLocale) return;
  currentLocale = next;
  for (const listener of listeners) listener();
}

/** Restore only the explicit app preference; Chinese remains the default. */
export function restoreLocale(): void {
  let saved: string | null = null;
  try { saved = window.localStorage.getItem(LOCALE_KEY); } catch { /* Use the default. */ }
  setLocale(normalizeLocale(saved), false);
}

/** Translate UI-owned messages only; substitutions are inserted once, never reinterpreted. */
export function translate(locale: Locale, source: string, ...values: readonly unknown[]): string {
  const template = locale === "en" ? messages[source] ?? source : source;
  return template.replace(/\{(\d+)\}/g, (match, index: string) => {
    const position = Number(index);
    return position < values.length ? String(values[position] ?? "") : match;
  });
}

/** Translate from non-React helpers using the current application language. */
export function t(source: string, ...values: readonly unknown[]): string {
  return translate(currentLocale, source, ...values);
}

/** Locale used for human-readable dates; storage and wire formats remain unchanged. */
export function dateLocale(): string {
  return currentLocale === "en" ? "en-US" : "zh-CN";
}
