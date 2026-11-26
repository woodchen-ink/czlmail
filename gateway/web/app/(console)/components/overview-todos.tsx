"use client"

import Link from "next/link"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { CircleAlert, KeyRound, TriangleAlert } from "lucide-react"
import { toast } from "sonner"

import { DocLink } from "@/components/doc-link"
import { Panel, PanelHeader } from "@/components/panel"
import { Spinner } from "@/components/spinner"
import { Button } from "@/components/ui/button"
import { systemApi } from "@/lib/api/admin"
import { formatDate } from "@/lib/format"
import { useT } from "@/lib/i18n"
import type { Todo } from "@/lib/types"
import { cn } from "@/lib/utils"

/** Things to act on; hidden when there are none, so the normal state stays quiet. */
export function OverviewTodos({ todos }: { todos: Todo[] }) {
  const t = useT()
  const qc = useQueryClient()
  const confirm = useMutation({
    mutationFn: systemApi.confirmMasterKeyBackup,
    onSuccess: () => {
      toast.success(t.common.saved)
      qc.invalidateQueries({ queryKey: ["overview"] })
      qc.invalidateQueries({ queryKey: ["system"] })
    },
  })
  if (todos.length === 0) return null

  const provider = (p?: string) =>
    p === "microsoft"
      ? t.oauth.microsoft.name
      : p === "google"
        ? t.oauth.google.name
        : (p ?? "")

  return (
    <Panel>
      <PanelHeader
        title={`${t.overview.todos} · ${todos.length}`}
        actions={<DocLink topic="statuses" />}
      />
      <ul className="divide-y">
        {todos.map((todo, i) => {
          const Icon =
            todo.kind === "masterKeyBackup"
              ? KeyRound
              : todo.severity === "danger"
                ? CircleAlert
                : TriangleAlert
          let text = ""
          let hint = ""
          let action: React.ReactNode = null
          if (todo.kind === "reauth") {
            text = t.overview.todoReauth(todo.name ?? "")
            action = (
              <Button asChild size="sm" variant="outline">
                <Link
                  href={`/mailboxes/?focus=${encodeURIComponent(todo.upstreamId ?? "")}`}
                >
                  {t.overview.todoGo}
                </Link>
              </Button>
            )
          } else if (todo.kind === "oauthSecretExpiring") {
            text =
              todo.severity === "danger"
                ? t.overview.todoSecretExpired(provider(todo.name))
                : t.overview.todoSecret(
                    provider(todo.name),
                    formatDate(todo.dueAt)
                  )
            action = (
              <Button asChild size="sm" variant="outline">
                <Link href={`/oauth-apps/?provider=${todo.name ?? ""}`}>
                  {t.overview.todoGo}
                </Link>
              </Button>
            )
          } else {
            text = t.overview.todoMasterKey
            hint = t.overview.todoMasterKeyHint
            action = (
              <Button
                size="sm"
                variant="outline"
                disabled={confirm.isPending}
                onClick={() => confirm.mutate()}
              >
                {confirm.isPending && <Spinner />}
                {t.overview.todoMasterKeyConfirm}
              </Button>
            )
          }
          return (
            <li key={i} className="flex items-center gap-3 px-4 py-2.5">
              <Icon
                className={cn(
                  "size-4 shrink-0",
                  todo.severity === "danger"
                    ? "text-destructive"
                    : "text-warning"
                )}
              />
              <div className="min-w-0 flex-1">
                <p className="text-sm">{text}</p>
                {hint && (
                  <p className="text-xs text-foreground-muted">{hint}</p>
                )}
              </div>
              {action}
            </li>
          )
        })}
      </ul>
    </Panel>
  )
}
