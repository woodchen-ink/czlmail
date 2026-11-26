"use client"

import Link from "next/link"
import { FileQuestion } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useT } from "@/lib/i18n"

export default function NotFound() {
  const t = useT()
  return (
    <main className="flex min-h-svh flex-col items-center justify-center gap-3 px-4 text-center">
      <FileQuestion className="size-8 text-foreground-subtle" />
      <p className="text-sm font-medium">{t.notFound.title}</p>
      <Button asChild variant="outline" size="sm">
        <Link href="/">{t.notFound.back}</Link>
      </Button>
    </main>
  )
}
