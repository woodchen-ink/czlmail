"use client"

import * as React from "react"
import { ThemeProvider as NextThemesProvider } from "next-themes"

/** Follows the system theme, same as the desktop default. */
export function ThemeProvider({ children }: { children: React.ReactNode }) {
  return (
    <NextThemesProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      {children}
    </NextThemesProvider>
  )
}
