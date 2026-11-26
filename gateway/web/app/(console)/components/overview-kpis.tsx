"use client"

import { ArrowDownRight, ArrowUpRight } from "lucide-react"

import { Panel } from "@/components/panel"
import { formatMs, formatNumber, percentChange } from "@/lib/format"
import { useT } from "@/lib/i18n"
import type { Overview } from "@/lib/types"
import { cn } from "@/lib/utils"

/** Change against the previous window; colour by meaning (more throttling is bad). */
function Delta({
  cur,
  prev,
  upIsBad,
}: {
  cur: number
  prev: number
  upIsBad?: boolean
}) {
  const t = useT()
  const pct = percentChange(cur, prev)
  if (pct === null)
    return (
      <span className="text-xs text-foreground-subtle">
        {t.overview.noPrevious}
      </span>
    )
  const up = pct > 0
  const bad = upIsBad ? up : false
  const Icon = up ? ArrowUpRight : ArrowDownRight
  return (
    <span
      className={cn(
        "inline-flex items-center gap-0.5 text-xs tabular-nums",
        bad ? "text-destructive" : "text-foreground-muted"
      )}
    >
      {pct !== 0 && <Icon className="size-3" />}
      {Math.abs(pct)}%{" "}
      <span className="text-foreground-subtle">{t.overview.vsPrevious}</span>
    </span>
  )
}

function Cell({
  label,
  value,
  sub,
  tone,
}: {
  label: string
  value: string
  sub: React.ReactNode
  tone?: "danger" | "warning"
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-4 py-3">
      <span className="text-xs text-foreground-muted">{label}</span>
      <span
        className={cn(
          "text-2xl font-semibold tabular-nums",
          tone === "danger" && "text-destructive",
          tone === "warning" && "text-warning"
        )}
      >
        {value}
      </span>
      <span className="min-h-4">{sub}</span>
    </div>
  )
}

/** One strip of headline numbers for the selected window. */
export function OverviewKpis({ data }: { data: Overview }) {
  const t = useT()
  const total = data.upstreams.length
  const ok = (data.statusCounts.ok ?? 0) + (data.statusCounts.syncing ?? 0)
  const bad = total - ok - (data.statusCounts.paused ?? 0)
  const s = data.trend.summary
  const p = data.trend.previous
  return (
    <Panel className="grid grid-cols-2 divide-border md:grid-cols-5 md:divide-x max-md:[&>*:last-child]:col-span-2 max-md:[&>*:nth-child(n+3)]:border-t">
      <Cell
        label={t.overview.kpiMailboxes}
        value={`${ok} / ${total}`}
        tone={bad > 0 ? "danger" : undefined}
        sub={
          <span
            className={cn(
              "text-xs",
              bad > 0 ? "text-destructive" : "text-foreground-subtle"
            )}
          >
            {t.overview.kpiMailboxesSub(bad)}
          </span>
        }
      />
      <Cell
        label={t.overview.kpiClients}
        value={formatNumber(data.onlineClients)}
        sub={
          <span className="text-xs text-foreground-subtle">
            {t.overview.kpiClientsSub}
          </span>
        }
      />
      <Cell
        label={t.overview.kpiRequests}
        value={formatNumber(s.requests)}
        sub={<Delta cur={s.requests} prev={p.requests} />}
      />
      <Cell
        label={t.overview.kpiRateLimited}
        value={formatNumber(s.rateLimited)}
        tone={s.rateLimited > 0 ? "warning" : undefined}
        sub={<Delta cur={s.rateLimited} prev={p.rateLimited} upIsBad />}
      />
      <Cell
        label={t.overview.kpiLatency}
        value={formatMs(s.pushP95Ms)}
        sub={
          s.pushP95Ms != null && p.pushP95Ms != null ? (
            <Delta cur={s.pushP95Ms} prev={p.pushP95Ms} upIsBad />
          ) : (
            <span className="text-xs text-foreground-subtle">
              {t.overview.noPrevious}
            </span>
          )
        }
      />
    </Panel>
  )
}
