"use client"

import * as React from "react"
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  XAxis,
  YAxis,
} from "recharts"

import { Panel, PanelHeader } from "@/components/panel"
import { Segmented } from "@/components/segmented"
import { EmptyState } from "@/components/state-views"
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { formatHour, formatMs, formatNumber } from "@/lib/format"
import { useQueryParam } from "@/lib/hooks/use-query-param"
import { useT } from "@/lib/i18n"
import type { Trend, TrendPoint } from "@/lib/types"
import { Activity } from "lucide-react"

const METRICS = ["requests", "rateLimited", "latency"] as const
type Metric = (typeof METRICS)[number]

const ALL = "all"

/** Hourly upstream activity: stacked bars per mailbox (top 5 + other), or p50 / p95 latency lines. */
export function TrendChart({
  trend,
  names,
}: {
  trend: Trend
  names: Record<string, string>
}) {
  const t = useT()
  const [metric, setMetric] = useQueryParam<Metric>(
    "metric",
    METRICS,
    "requests"
  )
  const [account, setAccount] = React.useState<string>(ALL)
  const long = trend.range !== "24h"

  const series = trend.series
  const selected =
    account === ALL ? null : series.find((s) => s.accountId === account)
  const label = (id: string) =>
    id === "other" ? t.overview.trendOther : (names[id] ?? id)

  // Recharts keys must be CSS-safe for --color-<key>: s0, s1, …
  type Row = Record<string, string | number | null>
  const { data, config, keys } = React.useMemo((): {
    data: Row[]
    config: ChartConfig
    keys: string[]
  } => {
    if (metric === "latency") {
      const pts: TrendPoint[] = selected ? selected.points : trend.total
      return {
        data: pts.map((p) => ({ t: p.t, p50: p.pushP50Ms, p95: p.pushP95Ms })),
        config: {
          p50: { label: t.overview.p50, color: "var(--chart-1)" },
          p95: { label: t.overview.p95, color: "var(--chart-3)" },
        },
        keys: ["p50", "p95"],
      }
    }
    const field = metric === "requests" ? "requests" : "rateLimited"
    const list = selected ? [selected] : series
    const cfg: ChartConfig = {}
    list.forEach((s, i) => {
      cfg[`s${i}`] = {
        label: label(s.accountId),
        color: `var(--chart-${(i % 6) + 1})`,
      }
    })
    const rows = trend.total.map((p, h) => {
      const row: Row = { t: p.t }
      list.forEach((s, i) => {
        row[`s${i}`] = s.points[h]?.[field] ?? 0
      })
      return row
    })
    return { data: rows, config: cfg, keys: list.map((_, i) => `s${i}`) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [metric, selected, series, trend.total, t])

  const hasData =
    metric === "latency"
      ? data.some((d) => (d as { p95: number | null }).p95 != null)
      : data.some((d) =>
          keys.some((k) => Number((d as Record<string, number>)[k]) > 0)
        )

  const tick = (v: string) => formatHour(v, long)
  const axis = {
    xAxis: (
      <XAxis
        dataKey="t"
        tickLine={false}
        axisLine={false}
        tickMargin={8}
        minTickGap={32}
        tickFormatter={tick}
      />
    ),
    grid: <CartesianGrid vertical={false} />,
  }

  return (
    <Panel>
      <PanelHeader
        title={t.overview.trendTitle}
        description={t.overview.rangeLabel[trend.range]}
        actions={
          <>
            {series.length > 1 && (
              <Select value={account} onValueChange={setAccount}>
                <SelectTrigger size="sm" className="w-36">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ALL}>{t.overview.trendAll}</SelectItem>
                  {series
                    .filter((s) => s.accountId !== "other")
                    .map((s) => (
                      <SelectItem key={s.accountId} value={s.accountId}>
                        {label(s.accountId)}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            )}
            <Segmented
              value={metric}
              onChange={setMetric}
              options={[
                { value: "requests", label: t.overview.trendRequests },
                { value: "rateLimited", label: t.overview.trendRateLimited },
                { value: "latency", label: t.overview.trendLatency },
              ]}
            />
          </>
        }
      />
      <div className="px-2 py-3 sm:px-4">
        {!hasData ? (
          <EmptyState
            icon={Activity}
            title={t.overview.trendEmpty}
            className="h-64 py-0"
          />
        ) : (
          <ChartContainer config={config} className="aspect-auto h-64 w-full">
            {metric === "latency" ? (
              <LineChart data={data} margin={{ left: 4, right: 8 }}>
                {axis.grid}
                {axis.xAxis}
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={72}
                  tickFormatter={(v: number) => formatMs(v)}
                />
                <ChartTooltip
                  content={
                    <ChartTooltipContent
                      labelFormatter={(v) => tick(String(v))}
                      formatter={(v, n) => (
                        <TooltipRow
                          name={String(config[n as string]?.label ?? n)}
                          value={formatMs(Number(v))}
                        />
                      )}
                    />
                  }
                />
                {keys.map((k) => (
                  <Line
                    key={k}
                    dataKey={k}
                    type="monotone"
                    stroke={`var(--color-${k})`}
                    strokeWidth={2}
                    dot={{ r: 1.5, strokeWidth: 0, fill: `var(--color-${k})` }}
                    isAnimationActive={false}
                    connectNulls
                  />
                ))}
                <ChartLegend
                  content={
                    <ChartLegendContent className="flex-wrap gap-x-4 gap-y-1 [&>div]:whitespace-nowrap" />
                  }
                />
              </LineChart>
            ) : (
              <BarChart data={data} margin={{ left: 4, right: 8 }}>
                {axis.grid}
                {axis.xAxis}
                <YAxis
                  tickLine={false}
                  axisLine={false}
                  width={44}
                  allowDecimals={false}
                  tickFormatter={(v: number) => formatNumber(v)}
                />
                <ChartTooltip
                  content={
                    <ChartTooltipContent
                      labelFormatter={(v) => tick(String(v))}
                      formatter={(v, n) => (
                        <TooltipRow
                          name={String(config[n as string]?.label ?? n)}
                          value={formatNumber(Number(v))}
                          color={`var(--color-${n})`}
                        />
                      )}
                    />
                  }
                />
                {keys.map((k, i) => (
                  <Bar
                    key={k}
                    dataKey={k}
                    stackId="a"
                    fill={`var(--color-${k})`}
                    radius={i === keys.length - 1 ? [3, 3, 0, 0] : 0}
                    isAnimationActive={false}
                  />
                ))}
                {keys.length > 1 && (
                  <ChartLegend
                    content={
                      <ChartLegendContent className="flex-wrap gap-x-4 gap-y-1 [&>div]:whitespace-nowrap" />
                    }
                  />
                )}
              </BarChart>
            )}
          </ChartContainer>
        )}
      </div>
    </Panel>
  )
}

function TooltipRow({
  name,
  value,
  color,
}: {
  name: string
  value: string
  color?: string
}) {
  return (
    <div className="flex w-full min-w-36 items-center gap-2">
      {color && (
        <span
          className="size-2 shrink-0 rounded-[2px]"
          style={{ background: color }}
        />
      )}
      <span className="flex-1 text-foreground-muted">{name}</span>
      <span className="font-mono font-medium tabular-nums">{value}</span>
    </div>
  )
}
