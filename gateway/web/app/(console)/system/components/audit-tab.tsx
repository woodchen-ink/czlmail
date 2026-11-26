"use client"

import * as React from "react"
import { useInfiniteQuery } from "@tanstack/react-query"
import { ScrollText } from "lucide-react"

import { Spinner } from "@/components/spinner"
import { Chip } from "@/components/status-badge"
import { EmptyState, ErrorState } from "@/components/state-views"
import { TablePanel } from "@/components/table-panel"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { auditApi } from "@/lib/api/admin"
import { formatDateTime } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { headRow, td, th } from "@/lib/table"
import type { AuditEntry } from "@/lib/types"

export function AuditTab() {
  const t = useT()
  const q = useInfiniteQuery({
    queryKey: qk.audit,
    queryFn: ({ pageParam }) => auditApi.list(pageParam, 50),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
  })
  const [detail, setDetail] = React.useState<AuditEntry | null>(null)
  const rows = q.data?.pages.flatMap((p) => p.items) ?? []

  return (
    <TablePanel
      footer={
        q.hasNextPage ? (
          <div className="flex justify-center">
            <Button
              variant="ghost"
              size="sm"
              disabled={q.isFetchingNextPage}
              onClick={() => q.fetchNextPage()}
            >
              {q.isFetchingNextPage && <Spinner />}
              {t.common.loadMore}
            </Button>
          </div>
        ) : null
      }
    >
      {q.isPending ? (
        <div className="grid gap-2 p-4">
          {Array.from({ length: 8 }).map((_, i) => (
            <Skeleton key={i} className="h-8" />
          ))}
        </div>
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState icon={ScrollText} title={t.system.audit.empty} />
      ) : (
        <Table>
          <TableHeader>
            <TableRow className={headRow}>
              <TableHead className={th}>{t.system.audit.time}</TableHead>
              <TableHead className={th}>{t.system.audit.actor}</TableHead>
              <TableHead className={th}>{t.system.audit.action}</TableHead>
              <TableHead className={th}>{t.system.audit.result}</TableHead>
              <TableHead className={th}>{t.system.audit.ip}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((a) => (
              <TableRow
                key={a.id}
                className="cursor-pointer"
                onClick={() => setDetail(a)}
              >
                <TableCell
                  className={`${td} text-foreground-muted tabular-nums`}
                >
                  {formatDateTime(a.createdAt)}
                </TableCell>
                <TableCell className={td}>
                  {a.actorEmail || (
                    <span className="text-foreground-subtle">
                      {t.system.audit.anonymous}
                    </span>
                  )}
                </TableCell>
                <TableCell className={`${td} font-mono text-xs`}>
                  <span className="text-foreground-muted">{a.method}</span>{" "}
                  {a.path.replace(/^\/api\/admin/, "")}
                </TableCell>
                <TableCell className={td}>
                  {a.code === 200 ? (
                    <span className="text-foreground-muted">200</span>
                  ) : (
                    <Chip tone="danger">
                      {a.code} {a.msg}
                    </Chip>
                  )}
                </TableCell>
                <TableCell
                  className={`${td} font-mono text-xs text-foreground-muted`}
                >
                  {a.ip}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Dialog open={!!detail} onOpenChange={(o) => !o && setDetail(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle className="font-mono text-sm">
              {detail?.method} {detail?.path}
            </DialogTitle>
          </DialogHeader>
          {detail && (
            <div className="grid gap-3 text-sm">
              <dl className="grid grid-cols-[5rem_1fr] gap-y-1.5">
                <dt className="text-foreground-muted">{t.system.audit.time}</dt>
                <dd>{formatDateTime(detail.createdAt)}</dd>
                <dt className="text-foreground-muted">
                  {t.system.audit.actor}
                </dt>
                <dd>{detail.actorEmail || t.system.audit.anonymous}</dd>
                <dt className="text-foreground-muted">
                  {t.system.audit.result}
                </dt>
                <dd className="font-mono text-xs leading-5">
                  {detail.status} · {detail.code} {detail.msg}
                </dd>
                <dt className="text-foreground-muted">IP</dt>
                <dd className="font-mono text-xs leading-5">{detail.ip}</dd>
                <dt className="text-foreground-muted">Trace</dt>
                <dd className="font-mono text-xs leading-5">
                  {detail.traceId}
                </dd>
              </dl>
              <div>
                <p className="mb-1 text-xs text-foreground-muted">
                  {t.system.audit.params}
                </p>
                <pre className="max-h-64 overflow-auto rounded-md bg-muted p-3 font-mono text-xs">
                  {JSON.stringify(detail.params, null, 2)}
                </pre>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </TablePanel>
  )
}
