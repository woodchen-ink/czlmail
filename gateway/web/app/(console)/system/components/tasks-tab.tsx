"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Play } from "lucide-react"
import { toast } from "sonner"

import { Spinner } from "@/components/spinner"
import { Chip } from "@/components/status-badge"
import { ErrorState } from "@/components/state-views"
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
import { systemApi } from "@/lib/api/admin"
import {
  formatDateTime,
  formatMs,
  formatInterval,
  formatRelative,
} from "@/lib/format"
import { useNow } from "@/lib/hooks/use-now"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { headRow, td, th } from "@/lib/table"
import type { TaskRun, TaskStatus } from "@/lib/types"

function RunResult({ run }: { run: TaskRun | null }) {
  const t = useT()
  // Summaries are English from the server; known shapes get our wording.
  const deleted = run?.result.match(/^deleted (\d+)$/)
  const result = deleted ? t.system.tasks.deleted(deleted[1]) : run?.result
  if (!run)
    return <span className="text-foreground-subtle">{t.common.emptyValue}</span>
  return (
    <span className="flex min-w-0 items-center gap-2">
      {run.ok ? (
        <Chip tone="neutral">{t.system.tasks.ok}</Chip>
      ) : (
        <Chip tone="danger">{t.system.tasks.failed}</Chip>
      )}
      <span
        className={
          run.ok
            ? "truncate text-foreground-muted"
            : "truncate text-destructive"
        }
      >
        {run.ok ? result : run.error}
      </span>
    </span>
  )
}

export function TasksTab() {
  const t = useT()
  const qc = useQueryClient()
  const now = useNow(10_000)
  const tasks = useQuery({
    queryKey: qk.tasks,
    queryFn: systemApi.tasks,
    refetchInterval: 15_000,
  })
  const [history, setHistory] = React.useState<TaskStatus | null>(null)
  const run = useMutation({
    mutationFn: (name: string) => systemApi.runTask(name),
    onSuccess: () => {
      toast.success(t.system.tasks.ran)
      qc.invalidateQueries({ queryKey: qk.tasks })
    },
  })
  const name = (n: string) => t.system.tasks.names[n] ?? n

  return (
    <TablePanel>
      {tasks.isPending ? (
        <div className="grid gap-2 p-4">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="h-9" />
          ))}
        </div>
      ) : tasks.isError ? (
        <ErrorState error={tasks.error} onRetry={() => tasks.refetch()} />
      ) : (
        <Table>
          <TableHeader>
            <TableRow className={headRow}>
              <TableHead className={th}>{t.system.tasks.name}</TableHead>
              <TableHead className={th}>{t.system.tasks.interval}</TableHead>
              <TableHead className={th}>{t.system.tasks.lastRun}</TableHead>
              <TableHead className={th}>{t.system.tasks.result}</TableHead>
              <TableHead className={th}>{t.system.tasks.nextRun}</TableHead>
              <TableHead className={`${th} text-right`}>
                {t.common.actions}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tasks.data.map((task) => (
              <TableRow key={task.name}>
                <TableCell className={td}>
                  <div className="font-medium">{name(task.name)}</div>
                  <div className="font-mono text-xs text-foreground-subtle">
                    {task.name}
                  </div>
                </TableCell>
                <TableCell className={`${td} text-foreground-muted`}>
                  {formatInterval(task.intervalSeconds)}
                </TableCell>
                <TableCell
                  className={`${td} text-foreground-muted`}
                  title={formatDateTime(task.lastRun?.startedAt)}
                >
                  {task.running ? (
                    <Chip tone="info">{t.system.tasks.running}</Chip>
                  ) : now ? (
                    <>
                      {formatRelative(task.lastRun?.startedAt, now)}
                      {task.lastRun?.manual && (
                        <span className="ml-1 text-xs text-foreground-subtle">
                          ({t.system.tasks.manual})
                        </span>
                      )}
                    </>
                  ) : null}
                </TableCell>
                <TableCell className={`${td} max-w-72`}>
                  <RunResult run={task.lastRun} />
                </TableCell>
                <TableCell className={`${td} text-foreground-muted`}>
                  {formatDateTime(task.nextRunAt)}
                </TableCell>
                <TableCell className={`${td} text-right whitespace-nowrap`}>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setHistory(task)}
                    disabled={task.history.length === 0}
                  >
                    {t.system.tasks.history}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={
                      task.running ||
                      (run.isPending && run.variables === task.name)
                    }
                    onClick={() => run.mutate(task.name)}
                  >
                    {run.isPending && run.variables === task.name ? (
                      <Spinner />
                    ) : (
                      <Play />
                    )}
                    {t.system.tasks.run}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Dialog open={!!history} onOpenChange={(o) => !o && setHistory(null)}>
        <DialogContent className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>
              {t.system.tasks.history} · {history ? name(history.name) : ""}
            </DialogTitle>
          </DialogHeader>
          <ul className="max-h-[60dvh] divide-y overflow-y-auto rounded-lg border">
            {history?.history.map((r, i) => (
              <li key={i} className="flex items-center gap-3 px-3 py-2 text-sm">
                <span className="w-36 shrink-0 text-foreground-muted tabular-nums">
                  {formatDateTime(r.startedAt)}
                </span>
                <span className="w-14 shrink-0 text-xs text-foreground-subtle tabular-nums">
                  {formatMs(
                    Math.max(
                      0,
                      new Date(r.finishedAt).getTime() -
                        new Date(r.startedAt).getTime()
                    )
                  )}
                </span>
                <span className="min-w-0 flex-1">
                  <RunResult run={r} />
                </span>
                {r.manual && (
                  <span className="text-xs text-foreground-subtle">
                    {t.system.tasks.manual}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </DialogContent>
      </Dialog>
    </TablePanel>
  )
}
