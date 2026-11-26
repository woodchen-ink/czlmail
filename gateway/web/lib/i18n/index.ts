"use client"

// Interface language: Chinese (source) and English, with the same keys.
// The language follows the browser (zh* → Chinese, anything else → English) until
// the user picks one, which is remembered in localStorage.
import * as React from "react"

import { en } from "./en"
import { zhCN, type Messages } from "./zh-CN"

export type { Messages }

export type Locale = "zh-CN" | "en"

export const LOCALES: { value: Locale; label: string }[] = [
  { value: "zh-CN", label: "中文" },
  { value: "en", label: "English" },
]

const catalogs: Record<Locale, Messages> = { "zh-CN": zhCN, en }

/** Rendered into the static HTML; the browser's language takes over after hydration. */
export const DEFAULT_LOCALE: Locale = "zh-CN"

const STORAGE_KEY = "czlmail-gateway:locale"

function isLocale(v: unknown): v is Locale {
  return v === "zh-CN" || v === "en"
}

/** zh, zh-CN, zh-TW … → Chinese; any other preferred language → English. */
export function browserLocale(): Locale {
  if (typeof navigator === "undefined") return DEFAULT_LOCALE
  const first = (
    navigator.languages?.[0] ??
    navigator.language ??
    ""
  ).toLowerCase()
  return first.startsWith("zh") ? "zh-CN" : "en"
}

function readLocale(): Locale {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (isLocale(saved)) return saved
  } catch {
    // Storage blocked (private mode, policy): fall back to the browser language.
  }
  return browserLocale()
}

const listeners = new Set<() => void>()

function subscribe(fn: () => void) {
  listeners.add(fn)
  const onStorage = (e: StorageEvent) => e.key === STORAGE_KEY && fn()
  window.addEventListener("storage", onStorage)
  return () => {
    listeners.delete(fn)
    window.removeEventListener("storage", onStorage)
  }
}

export function setLocale(locale: Locale) {
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // Not persisted; the choice still applies until the page is reloaded.
  }
  override = locale
  listeners.forEach((fn) => fn())
}

// Set when storage is unavailable, so a choice still applies in this page.
let override: Locale | null = null

/** The current locale; DEFAULT_LOCALE in the static HTML and during hydration. */
export function useLocaleState(): Locale {
  return React.useSyncExternalStore(
    subscribe,
    () => override ?? readLocale(),
    () => DEFAULT_LOCALE
  )
}

// The locale for code outside React (API error texts, formatters). I18nProvider keeps it
// in step and remounts the tree on a change, so everything renders with one locale.
let current: Locale = DEFAULT_LOCALE

export function syncLocale(locale: Locale) {
  current = locale
}

export function currentLocale(): Locale {
  return current
}

const I18nContext = React.createContext<Locale>(DEFAULT_LOCALE)
export const I18nContextProvider = I18nContext.Provider

/** The current messages. */
export function useT(): Messages {
  return catalogs[React.useContext(I18nContext)]
}

export function useLocale(): Locale {
  return React.useContext(I18nContext)
}

/** The current messages for code outside React. */
export function t(): Messages {
  return catalogs[current]
}
