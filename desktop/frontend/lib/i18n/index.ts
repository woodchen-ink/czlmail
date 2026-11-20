import czech from "./cs.json";
import english from "./en.json";

export type Locale = "zh-CN" | "en" | "cs";
export const LOCALE_KEY = "czlmail.locale";
export const LOCALES = [["zh-CN", "简体中文"], ["en", "English"], ["cs", "Čeština"]] as const;

let currentLocale: Locale = "zh-CN";
const listeners = new Set<() => void>();
const messages: Readonly<Record<string, Readonly<Record<string, string>>>> = { en: english, cs: czech };

/** Reject unsupported or damaged preferences rather than using them as locale identifiers. */
export function normalizeLocale(value: unknown): Locale {
  return value === "en" || value === "cs" ? value : "zh-CN";
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

/** System UI languages in preference order; Chinese stays the default when none is supported. */
export function systemLocale(languages: readonly string[] | undefined = systemLanguages()): Locale {
  if (!languages?.length) return "zh-CN";
  for (const tag of languages) {
    const language = tag.toLowerCase().split(/[-_]/)[0];
    if (language === "zh") return "zh-CN";
    if (language === "en" || language === "cs") return language;
  }
  return "en";
}

function systemLanguages(): readonly string[] | undefined {
  if (typeof navigator === "undefined") return undefined;
  return navigator.languages?.length ? navigator.languages : navigator.language ? [navigator.language] : undefined;
}

/** An explicit app preference wins; without one the system language is followed but not stored. */
export function preferredLocale(saved: string | null): Locale {
  return saved === "zh-CN" || saved === "en" || saved === "cs" ? saved : systemLocale();
}

/** Restore after hydration; the static export always renders the Chinese snapshot first. */
export function restoreLocale(): void {
  let saved: string | null = null;
  try { saved = window.localStorage.getItem(LOCALE_KEY); } catch { /* Follow the system. */ }
  setLocale(preferredLocale(saved), false);
}

/** Translate UI-owned messages only; substitutions are inserted once, never reinterpreted. */
export function translate(locale: Locale, source: string, ...values: readonly unknown[]): string {
  const template = messages[locale]?.[source] ?? source;
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
  return currentLocale === "en" ? "en-US" : currentLocale === "cs" ? "cs-CZ" : "zh-CN";
}
