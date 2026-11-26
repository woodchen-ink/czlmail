"use client"

import { BrandLogo } from "@/components/brand-logo"
import { LanguageMenu } from "@/components/language-menu"
import { useT } from "@/lib/i18n"

// Sign-in, sign-up and setup: one centred form under the product name; the language switch top right.
export default function AuthLayout({
  children,
}: {
  children: React.ReactNode
}) {
  const t = useT()
  return (
    <main className="relative flex min-h-svh flex-col items-center justify-center px-4 py-10">
      <div className="absolute top-3 right-3">
        <LanguageMenu />
      </div>
      <div className="mb-6 flex items-center gap-2">
        <BrandLogo className="size-9" />
        <span className="text-base font-semibold">{t.app.name}</span>
      </div>
      <div className="w-full max-w-sm rounded-2xl border bg-card p-6 shadow-card">
        {children}
      </div>
    </main>
  )
}
