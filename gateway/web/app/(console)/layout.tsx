"use client"

import * as React from "react"

import { AppSidebar } from "@/components/app-sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { useMe } from "@/lib/hooks/use-me"
import { useT } from "@/lib/i18n"

const noop = () => () => {}

// Sidebar collapse state: shadcn writes the sidebar_state cookie, read back after hydration.
function useSidebarPref(): [boolean, (v: boolean) => void] {
  const saved = React.useSyncExternalStore(
    noop,
    () => !/(?:^|;\s*)sidebar_state=false/.test(document.cookie),
    () => true
  )
  const [open, setOpen] = React.useState<boolean | null>(null)
  return [open ?? saved, setOpen]
}

export default function ConsoleLayout({
  children,
}: {
  children: React.ReactNode
}) {
  const t = useT()
  const me = useMe()
  const [open, setOpen] = useSidebarPref()

  if (!me.data) {
    // Until /me answers (or redirects to sign-in): the shell's outline.
    return (
      <div className="flex h-svh">
        <div className="hidden w-64 border-r bg-background-subtle p-3 md:block">
          <Skeleton className="mb-6 h-8 w-40" />
          {Array.from({ length: 7 }).map((_, i) => (
            <Skeleton key={i} className="mb-2 h-7 w-full" />
          ))}
        </div>
        <div className="flex-1 p-6">
          <Skeleton className="mb-6 h-7 w-48" />
          <Skeleton className="h-64 w-full" />
        </div>
      </div>
    )
  }

  return (
    <SidebarProvider
      open={open}
      onOpenChange={setOpen}
      className="h-svh overflow-hidden"
    >
      <AppSidebar me={me.data} />
      <SidebarInset className="min-h-0 min-w-0 overflow-hidden">
        <header className="flex h-12 shrink-0 items-center gap-2 border-b bg-card px-3 md:hidden">
          <SidebarTrigger aria-label={t.nav.toggleSidebar} />
          <span className="text-sm font-semibold">{t.app.name}</span>
        </header>
        {/* Main scrolls on its own; list pages give their table the remaining height (flex-1 min-h-0). */}
        <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-5 md:px-6 md:py-6">
          <div className="mx-auto flex h-full w-full max-w-[1400px] flex-col gap-5">
            {children}
          </div>
        </div>
      </SidebarInset>
    </SidebarProvider>
  )
}
