"use client"

import * as React from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Ellipsis,
  KeyRound,
  Mails,
  Network,
  Pause,
  Play,
  Plug,
  Plus,
  RefreshCw,
  Star,
  Trash2,
} from "lucide-react"
import { toast } from "sonner"

import { AddMailboxDialog } from "./components/add-mailbox-dialog"
import { ConnectionReport } from "./components/connection-report"
import { PasswordDialog } from "./components/password-dialog"
import { ProxyDialog } from "./components/proxy-dialog"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { Panel } from "@/components/panel"
import { Chip } from "@/components/status-badge"
import { EmptyState, ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { UpstreamCard } from "@/components/upstream-card"
import {
  upstreamsApi,
  type ProbeReport as Report,
  type Upstream,
} from "@/lib/api/upstreams"
import { useNow } from "@/lib/hooks/use-now"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { cn } from "@/lib/utils"

function MailboxesPage() {
  const t = useT()
  const qc = useQueryClient()
  const now = useNow()
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const focus = params.get("focus")

  // ?add=1 opens the add dialog, so the overview and links can deep-link to it.
  const adding = params.get("add") === "1"
  const setAdding = (open: boolean) => {
    const next = new URLSearchParams(params.toString())
    if (open) next.set("add", "1")
    else next.delete("add")
    const qs = next.toString()
    if (open) router.push(`${pathname}?${qs}`, { scroll: false })
    else router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
  }

  const list = useQuery({
    queryKey: qk.upstreams,
    queryFn: upstreamsApi.list,
    refetchInterval: 30_000,
  })
  const [proxyFor, setProxyFor] = React.useState<Upstream | null>(null)
  const [passwordFor, setPasswordFor] = React.useState<Upstream | null>(null)
  const [removing, setRemoving] = React.useState<Upstream | null>(null)
  const [report, setReport] = React.useState<{
    name: string
    report: Report
  } | null>(null)
  const [busy, setBusy] = React.useState<string | null>(null)

  const refresh = () => {
    qc.invalidateQueries({ queryKey: qk.upstreams })
    qc.invalidateQueries({ queryKey: ["overview"] })
  }
  const act = useMutation({
    mutationFn: async ({
      kind,
      u,
    }: {
      kind: "test" | "pause" | "resume" | "default" | "reauth"
      u: Upstream
    }) => {
      setBusy(u.id)
      switch (kind) {
        case "test":
          return { kind, u, report: await upstreamsApi.testExisting(u.id) }
        case "pause":
          await upstreamsApi.pause(u.id)
          return { kind, u }
        case "resume":
          await upstreamsApi.resume(u.id)
          return { kind, u }
        case "default":
          await upstreamsApi.setDefault(u.id)
          return { kind, u }
        case "reauth":
          return { kind, u, url: (await upstreamsApi.reauth(u.id)).url }
      }
    },
    onSettled: () => setBusy(null),
    onSuccess: (r) => {
      if (r.kind === "test" && r.report)
        return setReport({ name: r.u.name || r.u.email, report: r.report })
      if (r.kind === "reauth" && r.url) return window.location.assign(r.url)
      toast.success(
        r.kind === "pause"
          ? t.mailboxes.paused
          : r.kind === "resume"
            ? t.mailboxes.resumed
            : t.mailboxes.defaultSet
      )
      refresh()
    },
  })
  const remove = useMutation({
    mutationFn: (u: Upstream) => upstreamsApi.remove(u.id),
    onSuccess: () => {
      toast.success(t.mailboxes.removed)
      setRemoving(null)
      refresh()
    },
  })

  const menu = (u: Upstream) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label={t.common.actions}
          disabled={busy === u.id}
        >
          {busy === u.id ? (
            <RefreshCw className="animate-spin" />
          ) : (
            <Ellipsis />
          )}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onClick={() => act.mutate({ kind: "test", u })}>
          <Plug /> {t.mailboxes.test}
        </DropdownMenuItem>
        {u.status === "paused" || u.paused ? (
          <DropdownMenuItem onClick={() => act.mutate({ kind: "resume", u })}>
            <Play /> {t.mailboxes.resume}
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem onClick={() => act.mutate({ kind: "pause", u })}>
            <Pause /> {t.mailboxes.pause}
          </DropdownMenuItem>
        )}
        {u.kind === "imap" ? (
          <DropdownMenuItem onClick={() => setPasswordFor(u)}>
            <KeyRound /> {t.mailboxes.updatePassword}
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem onClick={() => act.mutate({ kind: "reauth", u })}>
            <KeyRound /> {t.mailboxes.reauth}
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onClick={() => setProxyFor(u)}>
          <Network /> {t.mailboxes.proxySettings}
        </DropdownMenuItem>
        {!u.isDefault && (
          <DropdownMenuItem onClick={() => act.mutate({ kind: "default", u })}>
            <Star /> {t.mailboxes.setDefault}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={() => setRemoving(u)}>
          <Trash2 /> {t.mailboxes.remove}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )

  return (
    <>
      <PageHeader
        title={t.mailboxes.title}
        subtitle={t.mailboxes.subtitle}
        actions={
          <Button onClick={() => setAdding(true)}>
            <Plus /> {t.mailboxes.add}
          </Button>
        }
      />
      {list.isPending ? (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-44 rounded-xl" />
          ))}
        </div>
      ) : list.isError ? (
        <Panel>
          <ErrorState error={list.error} onRetry={() => list.refetch()} />
        </Panel>
      ) : list.data.length === 0 ? (
        <Panel>
          <EmptyState
            icon={Mails}
            title={t.mailboxes.empty}
            hint={t.mailboxes.emptyHint}
            action={
              <Button size="sm" onClick={() => setAdding(true)}>
                <Plus /> {t.mailboxes.add}
              </Button>
            }
          />
        </Panel>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {list.data.map((u) => (
            <div
              key={u.id}
              className={cn("rounded-xl", focus === u.id && "ring-2 ring-ring")}
            >
              <UpstreamCard
                card={u}
                now={now}
                actions={menu(u)}
                badges={u.isDefault ? <Chip>{t.upstream.default}</Chip> : null}
              />
            </div>
          ))}
        </div>
      )}

      <AddMailboxDialog open={adding} onOpenChange={setAdding} />
      <ProxyDialog
        upstream={proxyFor}
        onOpenChange={(o) => !o && setProxyFor(null)}
      />
      <PasswordDialog
        upstream={passwordFor}
        onOpenChange={(o) => !o && setPasswordFor(null)}
      />
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={t.mailboxes.removeTitle(removing?.name || removing?.email || "")}
        body={t.mailboxes.removeBody}
        confirmLabel={t.common.delete}
        pending={remove.isPending}
        onConfirm={() => removing && remove.mutate(removing)}
      />
      <Dialog open={!!report} onOpenChange={(o) => !o && setReport(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {t.mailboxes.test} · {report?.name}
            </DialogTitle>
          </DialogHeader>
          {report && <ConnectionReport report={report.report} />}
        </DialogContent>
      </Dialog>
    </>
  )
}

export default function Page() {
  return (
    <React.Suspense>
      <MailboxesPage />
    </React.Suspense>
  )
}
