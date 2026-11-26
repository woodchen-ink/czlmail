"use client"

import * as React from "react"

import { I18nContextProvider, syncLocale, t, useLocaleState } from "@/lib/i18n"

/**
 * Supplies the interface language. A language change remounts the tree below, so
 * every text, date and number is rendered again in the new language; the query
 * cache lives above and is kept.
 */
export function I18nProvider({ children }: { children: React.ReactNode }) {
  const locale = useLocaleState()
  // Code outside React (error texts, formatters) reads the same locale while this tree renders.
  syncLocale(locale)
  React.useEffect(() => {
    document.documentElement.lang = locale
    document.title = t().app.name
  }, [locale])
  return (
    <I18nContextProvider value={locale}>
      <React.Fragment key={locale}>{children}</React.Fragment>
    </I18nContextProvider>
  )
}
