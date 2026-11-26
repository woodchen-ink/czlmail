"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  Ellipsis,
  Pencil,
  Plus,
  Trash2,
  UserCheck,
  UserX,
  Users,
} from "lucide-react"
import { toast } from "sonner"

import { FormField } from "@/components/auth-form"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { PageHeader } from "@/components/page-header"
import { Spinner } from "@/components/spinner"
import { Chip } from "@/components/status-badge"
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
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { usersApi, type UserInput } from "@/lib/api/admin"
import { formatDateTime, formatRelative } from "@/lib/format"
import { useMe } from "@/lib/hooks/use-me"
import { useNow } from "@/lib/hooks/use-now"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { headRow, td, th } from "@/lib/table"
import type { Role, User } from "@/lib/types"

/** Roles the signed-in user may give: the owner makes admins, admins only plain users. */
function assignable(me: User | undefined): Role[] {
  return me?.role === "owner" ? ["admin", "user"] : ["user"]
}

/** The owner manages everyone else; an admin manages plain users. Nobody manages themselves here. */
function canManage(me: User | undefined, u: User) {
  if (!me || me.id === u.id) return false
  if (me.role === "owner") return true
  return me.role === "admin" && u.role === "user"
}

function UserDialog({
  open,
  user,
  onOpenChange,
}: {
  open: boolean
  user: User | null
  onOpenChange: (o: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {open && <UserForm user={user} onClose={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  )
}

function UserForm({
  user,
  onClose,
}: {
  user: User | null
  onClose: () => void
}) {
  const t = useT()
  const me = useMe()
  const qc = useQueryClient()
  const roles = assignable(me.data)
  const [email, setEmail] = React.useState(user?.email ?? "")
  const [name, setName] = React.useState(user?.name ?? "")
  const [password, setPassword] = React.useState("")
  const [role, setRole] = React.useState<Role>(user?.role ?? "user")

  const save = useMutation({
    mutationFn: () => {
      if (!user)
        return usersApi.create({
          email: email.trim(),
          name: name.trim(),
          password,
          role,
        })
      const patch: UserInput = {}
      if (email.trim() !== user.email) patch.email = email.trim()
      if (name.trim() !== user.name) patch.name = name.trim()
      if (password) patch.password = password
      if (role !== user.role) patch.role = role
      return usersApi.update(user.id, patch)
    },
    onSuccess: () => {
      toast.success(user ? t.users.updated : t.users.created)
      qc.invalidateQueries({ queryKey: qk.users })
      onClose()
    },
  })
  const editingSelf = user?.id === me.data?.id

  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {user ? t.users.editTitle : t.users.createTitle}
        </DialogTitle>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <FormField
          id="u-email"
          label={t.users.email}
          type="email"
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <FormField
          id="u-name"
          label={t.users.name}
          placeholder={t.auth.namePlaceholder}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <FormField
          id="u-pass"
          label={user ? t.users.newPassword : t.users.initialPassword}
          type="password"
          autoComplete="new-password"
          required={!user}
          minLength={10}
          hint={user ? t.users.newPasswordHint : t.auth.passwordHint}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        {!editingSelf && user?.role !== "owner" && (
          <div className="grid gap-1.5">
            <Label>{t.users.role}</Label>
            <Select value={role} onValueChange={(v) => setRole(v as Role)}>
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {roles.map((r) => (
                  <SelectItem key={r} value={r}>
                    {t.roles[r]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        )}
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t.common.cancel}
          </Button>
          <Button type="submit" disabled={save.isPending}>
            {save.isPending && <Spinner />}
            {t.common.save}
          </Button>
        </DialogFooter>
      </form>
    </>
  )
}

export default function UsersPage() {
  const t = useT()
  const qc = useQueryClient()
  const me = useMe()
  const now = useNow()
  const list = useQuery({ queryKey: qk.users, queryFn: usersApi.list })
  const [editing, setEditing] = React.useState<{ user: User | null } | null>(
    null
  )
  const [disabling, setDisabling] = React.useState<User | null>(null)
  const [deleting, setDeleting] = React.useState<User | null>(null)

  const setDisabled = useMutation({
    mutationFn: ({ u, disabled }: { u: User; disabled: boolean }) =>
      usersApi.update(u.id, { disabled }),
    onSuccess: () => {
      toast.success(t.users.updated)
      setDisabling(null)
      qc.invalidateQueries({ queryKey: qk.users })
    },
  })
  const remove = useMutation({
    mutationFn: (u: User) => usersApi.remove(u.id),
    onSuccess: () => {
      toast.success(t.users.deleted)
      setDeleting(null)
      qc.invalidateQueries({ queryKey: qk.users })
    },
  })

  return (
    <>
      <PageHeader
        title={t.users.title}
        subtitle={t.users.subtitle}
        actions={
          <Button onClick={() => setEditing({ user: null })}>
            <Plus /> {t.users.create}
          </Button>
        }
      />
      <TablePanel>
        {list.isPending ? (
          <div className="grid gap-2 p-4">
            {Array.from({ length: 5 }).map((_, i) => (
              <Skeleton key={i} className="h-9" />
            ))}
          </div>
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => list.refetch()} />
        ) : list.data.length === 0 ? (
          <EmptyState icon={Users} title={t.users.title} />
        ) : (
          <Table>
            <TableHeader>
              <TableRow className={headRow}>
                <TableHead className={th}>{t.users.email}</TableHead>
                <TableHead className={th}>{t.users.name}</TableHead>
                <TableHead className={th}>{t.users.role}</TableHead>
                <TableHead className={th}>{t.users.status}</TableHead>
                <TableHead className={th}>{t.users.lastLogin}</TableHead>
                <TableHead className={th}>{t.users.createdAt}</TableHead>
                <TableHead className={`${th} w-12`} />
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data.map((u) => {
                const manage = canManage(me.data, u)
                const self = u.id === me.data?.id
                return (
                  <TableRow key={u.id}>
                    <TableCell className={`${td} font-medium`}>
                      {u.email}
                      {self && (
                        <span className="ml-1.5 text-xs font-normal text-foreground-subtle">
                          ({t.users.you})
                        </span>
                      )}
                    </TableCell>
                    <TableCell className={`${td} text-foreground-muted`}>
                      {u.name}
                    </TableCell>
                    <TableCell className={td}>
                      <Chip tone={u.role === "user" ? "neutral" : "info"}>
                        {t.roles[u.role]}
                      </Chip>
                    </TableCell>
                    <TableCell className={td}>
                      {u.disabled ? (
                        <Chip tone="danger">{t.users.disabledLabel}</Chip>
                      ) : (
                        <span className="text-foreground-muted">
                          {t.users.active}
                        </span>
                      )}
                    </TableCell>
                    <TableCell
                      className={`${td} text-foreground-muted`}
                      title={formatDateTime(u.lastLoginAt)}
                    >
                      {now ? formatRelative(u.lastLoginAt, now) : ""}
                    </TableCell>
                    <TableCell className={`${td} text-foreground-muted`}>
                      {formatDateTime(u.createdAt)}
                    </TableCell>
                    <TableCell className={`${td} text-right`}>
                      {(manage || self) && (
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button
                              variant="ghost"
                              size="icon-sm"
                              aria-label={t.common.actions}
                            >
                              <Ellipsis />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem
                              onClick={() => setEditing({ user: u })}
                            >
                              <Pencil /> {t.common.edit}
                            </DropdownMenuItem>
                            {manage && (
                              <>
                                {u.disabled ? (
                                  <DropdownMenuItem
                                    onClick={() =>
                                      setDisabled.mutate({ u, disabled: false })
                                    }
                                  >
                                    <UserCheck /> {t.users.enable}
                                  </DropdownMenuItem>
                                ) : (
                                  <DropdownMenuItem
                                    onClick={() => setDisabling(u)}
                                  >
                                    <UserX /> {t.users.disable}
                                  </DropdownMenuItem>
                                )}
                                <DropdownMenuSeparator />
                                <DropdownMenuItem
                                  variant="destructive"
                                  onClick={() => setDeleting(u)}
                                >
                                  <Trash2 /> {t.common.delete}
                                </DropdownMenuItem>
                              </>
                            )}
                          </DropdownMenuContent>
                        </DropdownMenu>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        )}
      </TablePanel>
      <UserDialog
        open={!!editing}
        user={editing?.user ?? null}
        onOpenChange={(o) => !o && setEditing(null)}
      />
      <ConfirmDialog
        open={!!disabling}
        onOpenChange={(o) => !o && setDisabling(null)}
        title={t.users.disableTitle(disabling?.email ?? "")}
        body={t.users.disableBody}
        confirmLabel={t.users.disable}
        pending={setDisabled.isPending}
        onConfirm={() =>
          disabling && setDisabled.mutate({ u: disabling, disabled: true })
        }
      />
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t.users.deleteTitle(deleting?.email ?? "")}
        body={t.users.deleteBody}
        confirmLabel={t.common.delete}
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </>
  )
}
