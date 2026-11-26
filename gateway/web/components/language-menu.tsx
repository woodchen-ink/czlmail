"use client"

import { Languages } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { LOCALES, setLocale, useLocale, useT, type Locale } from "@/lib/i18n"

/** Language switch for pages without the sidebar (sign-in, setup, sign-up). */
export function LanguageMenu() {
  const t = useT()
  const locale = useLocale()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" aria-label={t.nav.language}>
          <Languages />
          {LOCALES.find((l) => l.value === locale)?.label}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup
          value={locale}
          onValueChange={(v) => setLocale(v as Locale)}
        >
          {LOCALES.map((l) => (
            <DropdownMenuRadioItem key={l.value} value={l.value} lang={l.value}>
              {l.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
