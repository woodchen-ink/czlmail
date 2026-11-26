"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { OAuthWizard } from "./components/oauth-wizard"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { Panel } from "@/components/panel"
import { Chip } from "@/components/status-badge"
import { ErrorState, NotReady } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { isNotReady } from "@/lib/api/client"
import {
  oauthAppsApi,
  type OAuthApp,
  type OAuthProvider,
} from "@/lib/api/oauth-apps"
import { formatDate } from "@/lib/format"
import { useNow } from "@/lib/hooks/use-now"
import { useQueryParam } from "@/lib/hooks/use-query-param"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

const PROVIDERS = ["google", "microsoft"] as const
const WIZARD = ["", ...PROVIDERS] as const

const DAY = 86_400_000

function ProviderPanel({
  provider,
  app,
  now,
  onConfigure,
  onRemove,
}: {
  provider: OAuthProvider
  app?: OAuthApp
  now: number
  onConfigure: () => void
  onRemove?: () => void
}) {
  const t = useT()
  const copy = provider === "google" ? t.oauth.google : t.oauth.microsoft
  const expires = app?.secretExpiresAt
    ? new Date(app.secretExpiresAt).getTime()
    : null
  const tone =
    expires && now
      ? expires <= now
        ? "danger"
        : expires - now < 30 * DAY
          ? "warning"
          : null
      : null
  return (
    <Panel className="flex flex-col">
      <div className="flex items-center gap-3 border-b px-4 py-3">
        <span className="flex size-8 items-center justify-center rounded-md bg-muted text-sm font-semibold text-foreground-muted">
          {copy.name[0]}
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold">{copy.name}</h2>
          <p className="text-xs text-foreground-muted">
            {provider === "google" ? "Gmail" : "Outlook.com · Microsoft 365"}
          </p>
        </div>
        {app?.configured ? (
          <Chip tone="success">{t.oauth.configured}</Chip>
        ) : (
          <Chip>{t.oauth.notConfigured}</Chip>
        )}
      </div>
      <dl className="grid flex-1 grid-cols-[6rem_1fr] gap-x-3 gap-y-1.5 px-4 py-3 text-sm">
        <dt className="text-foreground-muted">{t.oauth.clientId}</dt>
        <dd className="truncate font-mono text-xs leading-5">
          {app?.clientId || t.common.emptyValue}
        </dd>
        {provider === "microsoft" && (
          <>
            <dt className="text-foreground-muted">{t.oauth.secretExpires}</dt>
            <dd
              className={
                tone === "danger"
                  ? "text-destructive"
                  : tone === "warning"
                    ? "text-warning"
                    : undefined
              }
            >
              {formatDate(app?.secretExpiresAt)}
            </dd>
          </>
        )}
      </dl>
      <div className="flex justify-end gap-2 border-t px-4 py-2.5">
        {app?.configured && onRemove && (
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive hover:text-destructive"
            onClick={onRemove}
          >
            {t.oauth.remove}
          </Button>
        )}
        <Button
          size="sm"
          variant={app?.configured ? "outline" : "default"}
          onClick={onConfigure}
        >
          {app?.configured ? t.oauth.reconfigure : t.oauth.configure}
        </Button>
      </div>
    </Panel>
  )
}

function OAuthAppsPage() {
  const t = useT()
  const qc = useQueryClient()
  const now = useNow()
  const list = useQuery({ queryKey: qk.oauthApps, queryFn: oauthAppsApi.list })
  // ?provider=google|microsoft opens the wizard (the overview's to-do links here).
  const [wizard, setWizard] = useQueryParam<(typeof WIZARD)[number]>(
    "provider",
    WIZARD,
    ""
  )
  const [removing, setRemoving] = React.useState<OAuthProvider | null>(null)
  const notReady = isNotReady(list.error)
  const apps = list.data ?? []
  const find = (p: OAuthProvider) => apps.find((a) => a.provider === p)

  const remove = useMutation({
    mutationFn: (p: OAuthProvider) => oauthAppsApi.remove(p),
    onSuccess: () => {
      toast.success(t.oauth.removed)
      setRemoving(null)
      qc.invalidateQueries({ queryKey: qk.oauthApps })
    },
  })

  return (
    <>
      <PageHeader title={t.oauth.title} subtitle={t.oauth.subtitle} />
      {notReady && (
        <Panel>
          <NotReady className="py-8" />
        </Panel>
      )}
      {list.isError && !notReady ? (
        <Panel>
          <ErrorState error={list.error} onRetry={() => list.refetch()} />
        </Panel>
      ) : list.isPending ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          <Skeleton className="h-44 rounded-xl" />
          <Skeleton className="h-44 rounded-xl" />
        </div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {PROVIDERS.map((p) => (
            <ProviderPanel
              key={p}
              provider={p}
              app={find(p)}
              now={now}
              onConfigure={() => setWizard(p)}
              onRemove={notReady ? undefined : () => setRemoving(p)}
            />
          ))}
        </div>
      )}
      <OAuthWizard
        provider={wizard || null}
        app={wizard ? find(wizard) : undefined}
        ready={!notReady}
        onOpenChange={(o) => !o && setWizard("")}
      />
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={t.oauth.removeTitle(
          removing === "google" ? t.oauth.google.name : t.oauth.microsoft.name
        )}
        body={t.oauth.removeBody}
        confirmLabel={t.oauth.remove}
        pending={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing)}
      />
    </>
  )
}

export default function Page() {
  return (
    <React.Suspense>
      <OAuthAppsPage />
    </React.Suspense>
  )
}
