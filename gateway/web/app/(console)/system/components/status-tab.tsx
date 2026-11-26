"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { ConfirmDialog } from "@/components/confirm-dialog"
import { Panel, PanelHeader } from "@/components/panel"
import { Chip } from "@/components/status-badge"
import { ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { systemApi } from "@/lib/api/admin"
import {
  formatBytes,
  formatDateTime,
  formatDuration,
  formatNumber,
} from "@/lib/format"
import { useNow } from "@/lib/hooks/use-now"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

function KV({ items }: { items: [string, React.ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 px-4 py-3 text-sm">
      {items.map(([k, v]) => (
        <React.Fragment key={k}>
          <dt className="text-foreground-muted">{k}</dt>
          <dd className="min-w-0 truncate">{v}</dd>
        </React.Fragment>
      ))}
    </dl>
  )
}

export function StatusTab() {
  const t = useT()
  const qc = useQueryClient()
  const now = useNow()
  const info = useQuery({
    queryKey: qk.system,
    queryFn: systemApi.info,
    refetchInterval: 30_000,
  })
  const [confirming, setConfirming] = React.useState(false)
  const confirm = useMutation({
    mutationFn: systemApi.confirmMasterKeyBackup,
    onSuccess: () => {
      toast.success(t.common.saved)
      setConfirming(false)
      qc.invalidateQueries({ queryKey: qk.system })
      qc.invalidateQueries({ queryKey: ["overview"] })
    },
  })

  if (info.isPending)
    return (
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Skeleton className="h-48 rounded-xl" />
        <Skeleton className="h-48 rounded-xl" />
      </div>
    )
  if (info.isError)
    return (
      <Panel>
        <ErrorState error={info.error} onRetry={() => info.refetch()} />
      </Panel>
    )
  const d = info.data
  const s = t.system
  return (
    <>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Panel>
          <PanelHeader title={s.tabs.status} />
          <KV
            items={[
              [
                s.version,
                <span key="v" className="font-mono">
                  {d.version}
                </span>,
              ],
              [s.startedAt, formatDateTime(d.startedAt)],
              [
                s.uptime,
                now
                  ? formatDuration(now - new Date(d.startedAt).getTime())
                  : t.common.emptyValue,
              ],
              [
                s.baseUrl,
                <span key="b" className="font-mono">
                  {d.baseUrl}
                </span>,
              ],
              [s.timeZone, d.timeZone],
              [
                s.goVersion,
                <span key="g" className="font-mono">
                  {d.goVersion}
                </span>,
              ],
              [s.onlineClients, formatNumber(d.onlineClients)],
            ]}
          />
        </Panel>
        <div className="grid gap-4">
          <Panel>
            <PanelHeader
              title={s.database}
              actions={
                d.database.ok ? (
                  <Chip tone="success">{s.databaseOk}</Chip>
                ) : (
                  <Chip tone="danger">{s.databaseDown}</Chip>
                )
              }
            />
            <KV
              items={[
                [s.mainDb, formatBytes(d.database.mainBytes)],
                [s.upstreamDb, formatBytes(d.database.upstreamBytes)],
              ]}
            />
          </Panel>
          <Panel>
            <PanelHeader
              title={s.masterKey}
              description={s.masterKeyVersion(d.masterKey.version)}
              actions={
                d.masterKey.backupConfirmedAt ? null : (
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => setConfirming(true)}
                  >
                    {s.confirmBackup}
                  </Button>
                )
              }
            />
            <div className="px-4 py-3 text-sm">
              {d.masterKey.backupConfirmedAt ? (
                <span className="text-foreground-muted">
                  {s.backupConfirmed(
                    formatDateTime(d.masterKey.backupConfirmedAt)
                  )}
                </span>
              ) : (
                <span className="text-warning">{s.backupNotConfirmed}</span>
              )}
            </div>
          </Panel>
        </div>
      </div>
      <Panel>
        <PanelHeader title={s.env} />
        <dl className="grid grid-cols-1 gap-x-6 gap-y-2 px-4 py-3 text-sm sm:grid-cols-[auto_1fr]">
          {d.env.map((e) => (
            <React.Fragment key={e.name}>
              <dt className="font-mono text-xs leading-5 text-foreground-muted">
                {e.name}
              </dt>
              <dd className="min-w-0 truncate font-mono text-xs leading-5 max-sm:mb-1">
                {e.secret ? (
                  <Chip tone="neutral">{s.envSet}</Chip>
                ) : (
                  e.value || (
                    <span className="text-foreground-subtle">{s.envEmpty}</span>
                  )
                )}
              </dd>
            </React.Fragment>
          ))}
        </dl>
      </Panel>
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={s.confirmBackupTitle}
        body={s.confirmBackupBody}
        confirmLabel={s.confirmBackup}
        destructive={false}
        pending={confirm.isPending}
        onConfirm={() => confirm.mutate()}
      />
    </>
  )
}
