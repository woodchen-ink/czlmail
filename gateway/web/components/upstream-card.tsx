"use client"

import * as React from "react"
import { CircleAlert, Gauge, Network, RefreshCw } from "lucide-react"

import { Panel } from "@/components/panel"
import { StatusBadge } from "@/components/status-badge"
import { Progress } from "@/components/ui/progress"
import { formatNumber, formatRelative } from "@/lib/format"
import { useT } from "@/lib/i18n"
import type { UpstreamCard as Card } from "@/lib/types"
import { cn } from "@/lib/utils"

/** Text for an upstream error: our message for known codes, else the identifier with its code. */
export function useUpstreamErrorText() {
  const t = useT()
  return (code: number, msg: string) =>
    t.errors.withCode(t.upstreamCodes[code] ?? msg, code)
}

function Row({
  icon: Icon,
  label,
  children,
  tone,
}: {
  icon: typeof Gauge
  label: string
  children: React.ReactNode
  tone?: "danger"
}) {
  return (
    <div className="flex items-start gap-2 text-sm">
      <Icon
        className={cn(
          "mt-0.5 size-3.5 shrink-0 text-foreground-subtle",
          tone === "danger" && "text-destructive"
        )}
      />
      <span className="w-16 shrink-0 text-foreground-muted">{label}</span>
      <span
        className={cn(
          "min-w-0 flex-1",
          // Errors wrap so the whole message is readable; other values are short.
          tone === "danger" ? "text-destructive" : "truncate"
        )}
      >
        {children}
      </span>
    </div>
  )
}

/** One mailbox: name, status and how it is connected. Actions go in the header slot. */
export function UpstreamCard({
  card,
  now,
  showOwner,
  actions,
  badges,
}: {
  card: Card
  now: number
  showOwner?: boolean
  actions?: React.ReactNode
  badges?: React.ReactNode
}) {
  const t = useT()
  const errText = useUpstreamErrorText()
  const proxy = !card.proxy
    ? t.upstream.proxyDirect
    : card.proxy.mode === "default"
      ? card.proxy.host
        ? `${t.upstream.proxyDefault} · ${card.proxy.host}`
        : t.upstream.proxyDefault
      : `${t.upstream.proxyCustom} · ${card.proxy.host}`
  const pct =
    card.initialSync && card.initialSync.total > 0
      ? Math.min(100, (card.initialSync.done / card.initialSync.total) * 100)
      : 0

  return (
    <Panel className="flex flex-col">
      <div className="flex items-start gap-3 border-b px-4 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="truncate text-sm font-semibold">
              {card.name || card.email}
            </h3>
            {badges}
          </div>
          <p className="truncate text-xs text-foreground-muted">
            {t.kinds[card.kind] ?? card.kind} · {card.email}
            {showOwner && card.ownerEmail ? ` · ${card.ownerEmail}` : ""}
          </p>
        </div>
        <StatusBadge status={card.status} />
        {actions}
      </div>
      <div className="grid grid-cols-1 gap-1.5 px-4 py-3">
        <Row icon={Gauge} label={t.upstream.watchMode}>
          {t.watchModes[card.watchMode] ?? card.watchMode}
        </Row>
        {card.lastSyncAt !== undefined && (
          <Row icon={RefreshCw} label={t.upstream.lastSync}>
            {now ? formatRelative(card.lastSyncAt, now) : t.common.emptyValue}
          </Row>
        )}
        <Row icon={Network} label={t.upstream.proxy}>
          {proxy}
        </Row>
        {card.lastError && (
          <Row icon={CircleAlert} label={t.upstream.lastError} tone="danger">
            <span title={card.lastError.detail || card.lastError.at}>
              {errText(card.lastError.code, card.lastError.msg)}
            </span>
          </Row>
        )}
        {card.initialSync && (
          <div className="mt-1 grid gap-1">
            <div className="flex justify-between text-xs text-foreground-muted">
              <span>{t.upstream.initialSync}</span>
              <span className="tabular-nums">
                {t.upstream.initialSyncProgress(
                  formatNumber(card.initialSync.done),
                  formatNumber(card.initialSync.total)
                )}
              </span>
            </div>
            <Progress value={pct} className="h-1.5 *:bg-brand" />
          </div>
        )}
      </div>
    </Panel>
  )
}
