"use client"

import * as React from "react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { useQueryClient } from "@tanstack/react-query"
import {
  AppWindow,
  ChevronsUpDown,
  KeyRound,
  Languages,
  LayoutDashboard,
  LogOut,
  Mails,
  Monitor,
  Moon,
  Server,
  Settings,
  Sun,
  Users,
  type LucideIcon,
} from "lucide-react"
import { useTheme } from "next-themes"

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import { authApi } from "@/lib/api/auth"
import { isAdminRole } from "@/lib/hooks/use-me"
import { useMounted } from "@/lib/hooks/use-mounted"
import { LOCALES, setLocale, useLocale, useT, type Locale } from "@/lib/i18n"
import type { User } from "@/lib/types"

interface NavItem {
  href: string
  label: string
  icon: LucideIcon
  admin?: boolean
}

function useNav(): { label: string; items: NavItem[] }[] {
  const t = useT()
  return [
    {
      label: t.nav.groupOverview,
      items: [{ href: "/", label: t.nav.overview, icon: LayoutDashboard }],
    },
    {
      label: t.nav.groupMail,
      items: [
        { href: "/mailboxes/", label: t.nav.mailboxes, icon: Mails },
        { href: "/app-passwords/", label: t.nav.appPasswords, icon: KeyRound },
      ],
    },
    {
      label: t.nav.groupAdmin,
      items: [
        { href: "/users/", label: t.nav.users, icon: Users, admin: true },
        {
          href: "/oauth-apps/",
          label: t.nav.oauthApps,
          icon: AppWindow,
          admin: true,
        },
      ],
    },
    {
      label: t.nav.groupSystem,
      items: [
        { href: "/settings/", label: t.nav.settings, icon: Settings },
        { href: "/system/", label: t.nav.system, icon: Server, admin: true },
      ],
    },
  ]
}

function isCurrent(pathname: string, href: string) {
  const p = pathname.endsWith("/") ? pathname : pathname + "/"
  return href === "/" ? p === "/" : p.startsWith(href)
}

// Current item: brand-soft background with brand-strong text (not the hover grey).
const activeClass =
  "data-active:bg-brand-soft data-active:text-brand-strong data-active:font-medium data-active:hover:bg-brand-soft data-active:hover:text-brand-strong"

export function AppSidebar({ me }: { me: User }) {
  const t = useT()
  const pathname = usePathname()
  const nav = useNav()
  const admin = isAdminRole(me.role)
  const { isMobile, setOpenMobile } = useSidebar()

  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <div className="flex h-10 items-center gap-2 px-1.5">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <Mails className="size-4" />
          </div>
          <span className="truncate text-sm font-semibold group-data-[collapsible=icon]:hidden">
            {t.app.name}
          </span>
        </div>
      </SidebarHeader>
      <SidebarContent>
        {nav.map((group) => {
          const items = group.items.filter((i) => !i.admin || admin)
          if (items.length === 0) return null
          return (
            <SidebarGroup key={group.label} className="py-1">
              <SidebarGroupLabel className="text-xs font-medium text-foreground-subtle">
                {group.label}
              </SidebarGroupLabel>
              <SidebarMenu>
                {items.map((item) => (
                  <SidebarMenuItem key={item.href}>
                    <SidebarMenuButton
                      asChild
                      isActive={isCurrent(pathname, item.href)}
                      tooltip={item.label}
                      className={activeClass}
                    >
                      <Link
                        href={item.href}
                        onClick={() => isMobile && setOpenMobile(false)}
                      >
                        <item.icon />
                        <span>{item.label}</span>
                      </Link>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroup>
          )
        })}
      </SidebarContent>
      <SidebarFooter>
        <UserMenu me={me} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

function UserMenu({ me }: { me: User }) {
  const t = useT()
  const qc = useQueryClient()
  const { theme, setTheme } = useTheme()
  const mounted = useMounted()
  const locale = useLocale()

  const signOut = async () => {
    try {
      await authApi.logout()
    } finally {
      qc.clear()
      window.location.assign("/admin/login/")
    }
  }
  const initial = (me.name || me.email).slice(0, 1).toUpperCase()

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent"
            >
              <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-secondary text-sm font-semibold">
                {initial}
              </span>
              <span className="grid min-w-0 flex-1 text-left leading-tight">
                <span className="truncate text-sm font-medium">
                  {me.name || me.email}
                </span>
                <span className="truncate text-xs text-foreground-muted">
                  {t.roles[me.role]}
                </span>
              </span>
              <ChevronsUpDown className="ml-auto size-4 text-foreground-subtle" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            side="top"
            align="start"
            className="w-(--radix-dropdown-menu-trigger-width) min-w-56"
          >
            <DropdownMenuLabel className="font-normal">
              <div className="truncate text-sm font-medium">
                {me.name || me.email}
              </div>
              <div className="truncate text-xs text-foreground-muted">
                {me.email}
              </div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs text-foreground-subtle">
              {t.nav.theme}
            </DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={mounted ? (theme ?? "system") : "system"}
              onValueChange={setTheme}
            >
              <DropdownMenuRadioItem value="light">
                <Sun /> {t.nav.themeLight}
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="dark">
                <Moon /> {t.nav.themeDark}
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="system">
                <Monitor /> {t.nav.themeSystem}
              </DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs text-foreground-subtle">
              {t.nav.language}
            </DropdownMenuLabel>
            <DropdownMenuRadioGroup
              value={locale}
              onValueChange={(v) => setLocale(v as Locale)}
            >
              {LOCALES.map((l) => (
                <DropdownMenuRadioItem
                  key={l.value}
                  value={l.value}
                  lang={l.value}
                >
                  <Languages /> {l.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={signOut}>
              <LogOut /> {t.nav.signOut}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>
    </SidebarMenu>
  )
}
