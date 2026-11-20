import czech from "./cs.json";
import english from "./en.json";

/**
 * Supported languages; everything else (type, validation, date locale, system matching, picker) derives from here.
 * Adding one: a `<id>.json` with the keys of en.json and one row here. The Go side embeds the same files for its
 * notifications, tray and file dialogs (desktop/api_locale.go) and picks up new ones automatically.
 * Chinese is the source language and needs no dictionary.
 */
const LOCALE_TABLE = [
  { id: "zh-CN", label: "简体中文", date: "zh-CN", messages: null },
  { id: "en", label: "English", date: "en-US", messages: english },
  { id: "cs", label: "Čeština", date: "cs-CZ", messages: czech },
] as const satisfies readonly {
  id: string; label: string; date: string; messages: Readonly<Record<string, string>> | null;
}[];

export type Locale = (typeof LOCALE_TABLE)[number]["id"];
export const LOCALE_KEY = "czlmail.locale";
export const DEFAULT_LOCALE: Locale = "zh-CN";
export const LOCALES: readonly (readonly [Locale, string])[] = LOCALE_TABLE.map(({ id, label }) => [id, label] as const);

const localeInfo = new Map<string, (typeof LOCALE_TABLE)[number]>(LOCALE_TABLE.map((row) => [row.id, row]));

let currentLocale: Locale = DEFAULT_LOCALE;
const listeners = new Set<() => void>();

/** Narrow a stored or received value to a supported locale id. */
export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && localeInfo.has(value);
}

/** Reject unsupported or damaged preferences rather than using them as locale identifiers. */
export function normalizeLocale(value: unknown): Locale {
  return isLocale(value) ? value : DEFAULT_LOCALE;
}

/** Current in-memory preference; initialized after hydration by LocaleProvider. */
export function getLocale(): Locale {
  return currentLocale;
}

/** Static export and hydration always start with the same Chinese snapshot. */
export function getServerLocale(): Locale {
  return DEFAULT_LOCALE;
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
  if (!languages?.length) return DEFAULT_LOCALE;
  for (const tag of languages) {
    const language = primaryLanguage(tag);
    const match = LOCALE_TABLE.find((row) => primaryLanguage(row.id) === language);
    if (match) return match.id;
  }
  return "en";
}

/** Primary language subtag: zh-TW and zh-CN both match the Chinese row. */
function primaryLanguage(tag: string): string {
  return tag.toLowerCase().split(/[-_]/)[0];
}

function systemLanguages(): readonly string[] | undefined {
  if (typeof navigator === "undefined") return undefined;
  return navigator.languages?.length ? navigator.languages : navigator.language ? [navigator.language] : undefined;
}

/** An explicit app preference wins; without one the system language is followed but not stored. */
export function preferredLocale(saved: string | null): Locale {
  return isLocale(saved) ? saved : systemLocale();
}

/** Restore after hydration; the static export always renders the Chinese snapshot first. */
export function restoreLocale(): void {
  let saved: string | null = null;
  try { saved = window.localStorage.getItem(LOCALE_KEY); } catch { /* Follow the system. */ }
  setLocale(preferredLocale(saved), false);
}

/** Translate UI-owned messages only; substitutions are inserted once, never reinterpreted. */
export function translate(locale: Locale, source: string, ...values: readonly unknown[]): string {
  const dictionary: Readonly<Record<string, string>> | null | undefined = localeInfo.get(locale)?.messages;
  const template = dictionary?.[source] ?? source;
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
  return localeInfo.get(currentLocale)?.date ?? DEFAULT_LOCALE;
}
