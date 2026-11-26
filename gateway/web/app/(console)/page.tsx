"use client"

import * as React from "react"
import Link from "next/link"
import { useQuery } from "@tanstack/react-query"
import { Mails, Plus } from "lucide-react"

import { OverviewKpis } from "./components/overview-kpis"
import { OverviewTodos } from "./components/overview-todos"
import { TrendChart } from "./components/trend-chart"
import { PageHeader } from "@/components/page-header"
import { Panel } from "@/components/panel"
import { Segmented } from "@/components/segmented"
import { EmptyState, ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { UpstreamCard } from "@/components/upstream-card"
import { overviewApi } from "@/lib/api/admin"
import { formatDateTime } from "@/lib/format"
import { isAdminRole, useMe } from "@/lib/hooks/use-me"
import { useNow } from "@/lib/hooks/use-now"
import { useQueryParam } from "@/lib/hooks/use-query-param"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import type { TrendRange } from "@/lib/types"

const RANGES = ["24h", "7d", "30d"] as const satisfies readonly TrendRange[]
const SCOPES = ["all", "mine"] as const

function OverviewSkeleton() {
  return (
    <>
      <Skeleton className="h-[92px] w-full rounded-xl" />
      <Skeleton className="h-[330px] w-full rounded-xl" />
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-44 rounded-xl" />
        ))}
      </div>
    </>
  )
}

function OverviewPage() {
  const t = useT()
  const me = useMe()
  const admin = isAdminRole(me.data?.role)
  const now = useNow()
  const [range, setRange] = useQueryParam<TrendRange>("range", RANGES, "24h")
  const [scope, setScope] = useQueryParam<(typeof SCOPES)[number]>(
    "scope",
    SCOPES,
    "all"
  )
  const effScope = admin ? scope : "mine"
  const q = useQuery({
    queryKey: qk.overview(range, effScope),
    queryFn: () => overviewApi.get(range, effScope),
    refetchInterval: 60_000,
    placeholderData: (prev) => prev,
  })

  const names = React.useMemo(
    () =>
      Object.fromEntries(
        (q.data?.upstreams ?? []).map((u) => [u.id, u.name || u.email])
      ),
    [q.data]
  )

  return (
    <>
      <PageHeader
        title={t.overview.title}
        subtitle={
          q.data
            ? t.overview.updatedAt(formatDateTime(q.data.generatedAt))
            : t.overview.subtitle
        }
        actions={
          <>
            {admin && (
              <Segmented
                value={scope}
                onChange={setScope}
                options={[
                  { value: "all", label: t.overview.scopeAll },
                  { value: "mine", label: t.overview.scopeMine },
                ]}
              />
            )}
            <Segmented
              value={range}
              onChange={setRange}
              options={RANGES.map((r) => ({
                value: r,
                label: t.overview.range[r],
              }))}
            />
          </>
        }
      />
      {q.isPending ? (
        <OverviewSkeleton />
      ) : q.isError && !q.data ? (
        <Panel>
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        </Panel>
      ) : (
        <>
          <OverviewTodos todos={q.data.todos} />
          <OverviewKpis data={q.data} />
          <TrendChart trend={q.data.trend} names={names} />
          <section className="grid grid-cols-1 gap-3">
            <div className="flex items-center justify-between">
              <h2 className="text-sm font-semibold">
                {t.overview.mailboxesTitle}
              </h2>
              <Button asChild variant="ghost" size="sm">
                <Link href="/mailboxes/">{t.common.viewAll}</Link>
              </Button>
            </div>
            {q.data.upstreams.length === 0 ? (
              <Panel>
                <EmptyState
                  icon={Mails}
                  title={t.overview.noMailboxes}
                  hint={t.overview.noMailboxesHint}
                  action={
                    <Button asChild size="sm">
                      <Link href="/mailboxes/?add=1">
                        <Plus /> {t.overview.addMailbox}
                      </Link>
                    </Button>
                  }
                />
              </Panel>
            ) : (
              <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                {q.data.upstreams.map((u) => (
                  <UpstreamCard
                    key={u.id}
                    card={u}
                    now={now}
                    showOwner={effScope === "all"}
                  />
                ))}
              </div>
            )}
          </section>
        </>
      )}
    </>
  )
}

export default function Page() {
  return (
    <React.Suspense>
      <OverviewPage />
    </React.Suspense>
  )
}
