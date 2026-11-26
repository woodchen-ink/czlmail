"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ChevronLeft,
  ChevronRight,
  Network,
  Palette,
  UserPlus,
  UserRound,
  type LucideIcon,
} from "lucide-react"
import { useTheme } from "next-themes"
import { toast } from "sonner"

import { FormField, SubmitButton } from "@/components/auth-form"
import { PageHeader } from "@/components/page-header"
import { Panel } from "@/components/panel"
import { Segmented } from "@/components/segmented"
import { ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { settingsApi } from "@/lib/api/admin"
import { authApi } from "@/lib/api/auth"
import { errorText } from "@/lib/errors"
import { isAdminRole, useMe } from "@/lib/hooks/use-me"
import { useMounted } from "@/lib/hooks/use-mounted"
import { useQueryParam } from "@/lib/hooks/use-query-param"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import type { Setting } from "@/lib/types"
import { cn } from "@/lib/utils"

const SECTIONS = ["account", "appearance", "registration", "network"] as const
type Section = (typeof SECTIONS)[number]

const ADMIN_SECTIONS: Section[] = ["registration", "network"]
const ICONS: Record<Section, LucideIcon> = {
  account: UserRound,
  appearance: Palette,
  registration: UserPlus,
  network: Network,
}

/** Which category a registered setting key belongs to, by prefix. */
function sectionOf(key: string): Section {
  if (key.startsWith("site.")) return "registration"
  return "network" // security., proxy., sync., limits.
}

function Group({
  title,
  children,
}: {
  title: string
  children: React.ReactNode
}) {
  return (
    <div className="grid gap-3">
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </div>
  )
}

/** One setting on one row: title and the control, the hint in secondary text below the title. */
function SettingRow({
  title,
  hint,
  control,
}: {
  title: string
  hint?: string
  control: React.ReactNode
}) {
  return (
    <div className="flex items-start gap-4 py-3 first:pt-0 last:pb-0">
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{title}</p>
        {hint && <p className="mt-0.5 text-xs text-foreground-muted">{hint}</p>}
      </div>
      <div className="shrink-0">{control}</div>
    </div>
  )
}

function AccountSection() {
  const t = useT()
  const me = useMe()
  const [cur, setCur] = React.useState("")
  const [next, setNext] = React.useState("")
  const [again, setAgain] = React.useState("")
  const [error, setError] = React.useState("")
  const change = useMutation({
    mutationFn: () =>
      authApi.changePassword({ currentPassword: cur, newPassword: next }),
    meta: { silent: true },
    onSuccess: () => {
      toast.success(t.settings.passwordChanged)
      setCur("")
      setNext("")
      setAgain("")
    },
    onError: (e) => setError(errorText(e)),
  })
  return (
    <div className="grid gap-6">
      <Group title={t.settings.accountName}>
        <dl className="grid grid-cols-[5rem_1fr] gap-y-1.5 text-sm">
          <dt className="text-foreground-muted">{t.users.email}</dt>
          <dd>{me.data?.email}</dd>
          <dt className="text-foreground-muted">{t.users.name}</dt>
          <dd>{me.data?.name || t.common.emptyValue}</dd>
          <dt className="text-foreground-muted">{t.users.role}</dt>
          <dd>{me.data ? t.roles[me.data.role] : ""}</dd>
        </dl>
      </Group>
      <div className="border-t" />
      <Group title={t.settings.changePassword}>
        <form
          className="grid max-w-sm gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            setError("")
            if (next !== again) return setError(t.settings.passwordMismatch)
            if (next.length < 10) return setError(t.errors.codes[40002])
            change.mutate()
          }}
        >
          <FormField
            id="cur"
            label={t.settings.currentPassword}
            type="password"
            autoComplete="current-password"
            required
            value={cur}
            onChange={(e) => setCur(e.target.value)}
          />
          <FormField
            id="new"
            label={t.settings.newPassword}
            type="password"
            autoComplete="new-password"
            required
            hint={t.auth.passwordHint}
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
          <FormField
            id="again"
            label={t.settings.confirmPassword}
            type="password"
            autoComplete="new-password"
            required
            value={again}
            onChange={(e) => setAgain(e.target.value)}
          />
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div>
            <SubmitButton pending={change.isPending}>
              {t.common.save}
            </SubmitButton>
          </div>
        </form>
      </Group>
    </div>
  )
}

function AppearanceSection() {
  const t = useT()
  const { theme, setTheme } = useTheme()
  const mounted = useMounted()
  return (
    <SettingRow
      title={t.settings.theme}
      hint={t.settings.themeHint}
      control={
        <Segmented
          value={
            mounted
              ? ((theme as "light" | "dark" | "system") ?? "system")
              : "system"
          }
          onChange={setTheme}
          options={[
            { value: "light", label: t.nav.themeLight },
            { value: "dark", label: t.nav.themeDark },
            { value: "system", label: t.nav.themeSystem },
          ]}
        />
      }
    />
  )
}

function StringSetting({
  s,
  onSave,
  pending,
}: {
  s: Setting
  onSave: (v: string) => void
  pending: boolean
}) {
  const t = useT()
  // Keyed by the stored value (see SettingsSection), so a save resets the draft.
  const [v, setV] = React.useState(String(s.value ?? ""))
  return (
    <form
      className="flex gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        onSave(v)
      }}
    >
      <Input
        value={v}
        onChange={(e) => setV(e.target.value)}
        className="w-64"
      />
      <Button
        type="submit"
        variant="outline"
        disabled={pending || v === String(s.value ?? "")}
      >
        {t.common.save}
      </Button>
    </form>
  )
}

function SettingsSection({ section }: { section: Section }) {
  const t = useT()
  const qc = useQueryClient()
  const list = useQuery({ queryKey: qk.settings, queryFn: settingsApi.list })
  const update = useMutation({
    mutationFn: (changes: Record<string, unknown>) =>
      settingsApi.update(changes),
    onSuccess: (rows) => {
      qc.setQueryData(qk.settings, rows)
      qc.invalidateQueries({ queryKey: qk.setup })
      toast.success(t.common.saved)
    },
  })
  if (list.isPending) return <Skeleton className="h-24" />
  if (list.isError)
    return <ErrorState error={list.error} onRetry={() => list.refetch()} />
  const rows = list.data.filter((s) => sectionOf(s.key) === section)
  return (
    <div className="divide-y">
      {rows.map((s) => {
        const copy = t.settings.keys[s.key] ?? { title: s.key, hint: "" }
        const control =
          s.kind === "bool" ? (
            <Switch
              checked={Boolean(s.value)}
              disabled={update.isPending}
              onCheckedChange={(v) => update.mutate({ [s.key]: v })}
              aria-label={copy.title}
            />
          ) : (
            <StringSetting
              key={String(s.value)}
              s={s}
              pending={update.isPending}
              onSave={(v) =>
                update.mutate({ [s.key]: s.kind === "int" ? Number(v) : v })
              }
            />
          )
        return (
          <SettingRow
            key={s.key}
            title={copy.title}
            hint={copy.hint}
            control={control}
          />
        )
      })}
    </div>
  )
}

function SettingsPage() {
  const t = useT()
  const me = useMe()
  const admin = isAdminRole(me.data?.role)
  const visible = SECTIONS.filter((s) => admin || !ADMIN_SECTIONS.includes(s))
  // On phones the category list is the first level; ?section= opens one.
  const [section, setSection] = useQueryParam<Section | "">(
    "section",
    ["", ...visible],
    ""
  )
  const current: Section = section || "account"

  const content = (s: Section) =>
    s === "account" ? (
      <AccountSection />
    ) : s === "appearance" ? (
      <AppearanceSection />
    ) : (
      <SettingsSection section={s} />
    )

  return (
    <>
      <PageHeader title={t.settings.title} subtitle={t.settings.subtitle} />
      <div className="flex flex-col gap-4 md:flex-row md:items-start">
        <nav
          className={cn("md:w-48 md:shrink-0", section && "max-md:hidden")}
          aria-label={t.settings.title}
        >
          <ul className="grid gap-0.5 max-md:overflow-hidden max-md:rounded-xl max-md:border max-md:bg-card max-md:shadow-card">
            {visible.map((s) => {
              const Icon = ICONS[s]
              const active = current === s
              return (
                <li key={s}>
                  <button
                    type="button"
                    onClick={() => setSection(s)}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm transition-colors hover:bg-accent max-md:rounded-none max-md:py-3",
                      active &&
                        "md:bg-brand-soft md:font-medium md:text-brand-strong md:hover:bg-brand-soft"
                    )}
                    aria-current={active ? "page" : undefined}
                  >
                    <Icon className="size-4 shrink-0" />
                    <span className="flex-1">{t.settings.sections[s]}</span>
                    <ChevronRight className="size-4 text-foreground-subtle md:hidden" />
                  </button>
                </li>
              )
            })}
          </ul>
        </nav>
        <Panel className={cn("min-w-0 flex-1", !section && "max-md:hidden")}>
          <div className="flex items-center gap-2 border-b px-4 py-3">
            <Button
              variant="ghost"
              size="icon-sm"
              className="md:hidden"
              onClick={() => setSection("")}
              aria-label={t.common.back}
            >
              <ChevronLeft />
            </Button>
            <h2 className="text-sm font-semibold">
              {t.settings.sections[current]}
            </h2>
          </div>
          <div className="px-4 py-4">{content(current)}</div>
        </Panel>
      </div>
    </>
  )
}

export default function Page() {
  return (
    <React.Suspense>
      <SettingsPage />
    </React.Suspense>
  )
}
