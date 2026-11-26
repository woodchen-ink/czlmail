"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { KeyRound, Plus } from "lucide-react"
import { toast } from "sonner"

import { CreatedDialog } from "./components/created-dialog"
import { FormField } from "@/components/auth-form"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { Spinner } from "@/components/spinner"
import { EmptyState, ErrorState } from "@/components/state-views"
import { TablePanel } from "@/components/table-panel"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
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
import { appPasswordsApi } from "@/lib/api/admin"
import { formatDateTime, formatRelative } from "@/lib/format"
import { useNow } from "@/lib/hooks/use-now"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { headRow, td, th } from "@/lib/table"
import type { AppPassword, CreatedAppPassword } from "@/lib/types"

export default function AppPasswordsPage() {
  const t = useT()
  const qc = useQueryClient()
  const now = useNow()
  const list = useQuery({
    queryKey: qk.appPasswords,
    queryFn: appPasswordsApi.list,
  })
  const [creating, setCreating] = React.useState(false)
  const [name, setName] = React.useState("")
  const [created, setCreated] = React.useState<CreatedAppPassword | null>(null)
  const [revoking, setRevoking] = React.useState<AppPassword | null>(null)

  const create = useMutation({
    mutationFn: () => appPasswordsApi.create(name.trim()),
    onSuccess: (p) => {
      setCreating(false)
      setName("")
      setCreated(p)
      qc.invalidateQueries({ queryKey: qk.appPasswords })
    },
  })
  const revoke = useMutation({
    mutationFn: (id: string) => appPasswordsApi.revoke(id),
    onSuccess: () => {
      toast.success(t.appPasswords.revoked)
      setRevoking(null)
      qc.invalidateQueries({ queryKey: qk.appPasswords })
    },
  })

  return (
    <>
      <PageHeader
        title={t.appPasswords.title}
        subtitle={t.appPasswords.subtitle}
        actions={
          <Button onClick={() => setCreating(true)}>
            <Plus /> {t.appPasswords.create}
          </Button>
        }
      />
      <TablePanel>
        {list.isPending ? (
          <div className="grid gap-2 p-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => list.refetch()} />
        ) : list.data.length === 0 ? (
          <EmptyState
            icon={KeyRound}
            title={t.appPasswords.empty}
            hint={t.appPasswords.emptyHint}
            action={
              <Button size="sm" onClick={() => setCreating(true)}>
                <Plus /> {t.appPasswords.create}
              </Button>
            }
          />
        ) : (
          <Table>
            <TableHeader>
              <TableRow className={headRow}>
                <TableHead className={th}>{t.appPasswords.name}</TableHead>
                <TableHead className={th}>{t.appPasswords.prefix}</TableHead>
                <TableHead className={th}>{t.appPasswords.lastUsed}</TableHead>
                <TableHead className={th}>
                  {t.appPasswords.lastUsedIp}
                </TableHead>
                <TableHead className={th}>{t.appPasswords.createdAt}</TableHead>
                <TableHead className={`${th} text-right`}>
                  {t.common.actions}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.map((p) => (
                <TableRow key={p.id}>
                  <TableCell className={`${td} font-medium`}>
                    {p.name}
                  </TableCell>
                  <TableCell
                    className={`${td} font-mono text-xs text-foreground-muted`}
                  >
                    {p.prefix}…
                  </TableCell>
                  <TableCell
                    className={`${td} text-foreground-muted`}
                    title={formatDateTime(p.lastUsedAt)}
                  >
                    {now ? formatRelative(p.lastUsedAt, now) : ""}
                  </TableCell>
                  <TableCell
                    className={`${td} font-mono text-xs text-foreground-muted`}
                  >
                    {p.lastUsedIp || t.common.emptyValue}
                  </TableCell>
                  <TableCell className={`${td} text-foreground-muted`}>
                    {formatDateTime(p.createdAt)}
                  </TableCell>
                  <TableCell className={`${td} text-right`}>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive hover:text-destructive"
                      onClick={() => setRevoking(p)}
                    >
                      {t.appPasswords.revoke}
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </TablePanel>

      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t.appPasswords.create}</DialogTitle>
          </DialogHeader>
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault()
              create.mutate()
            }}
          >
            <FormField
              id="ap-name"
              label={t.appPasswords.name}
              placeholder={t.appPasswords.namePlaceholder}
              required
              maxLength={64}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
            />
            <DialogFooter>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setCreating(false)}
              >
                {t.common.cancel}
              </Button>
              <Button type="submit" disabled={!name.trim() || create.isPending}>
                {create.isPending && <Spinner />}
                {t.common.create}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <CreatedDialog created={created} onClose={() => setCreated(null)} />
      <ConfirmDialog
        open={!!revoking}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={t.appPasswords.revokeTitle(revoking?.name ?? "")}
        body={t.appPasswords.revokeBody}
        confirmLabel={t.appPasswords.revoke}
        pending={revoke.isPending}
        onConfirm={() => revoking && revoke.mutate(revoking.id)}
      />
    </>
  )
}
