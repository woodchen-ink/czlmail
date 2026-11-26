"use client"

import * as React from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { FormField } from "@/components/auth-form"
import { Spinner } from "@/components/spinner"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { upstreamsApi, type Upstream } from "@/lib/api/upstreams"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

/** The account name and the mailbox's own address (From of sent mail). */
export function RenameDialog({
  upstream,
  onOpenChange,
}: {
  upstream: Upstream | null
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={!!upstream} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {upstream && (
          <RenameForm upstream={upstream} onClose={() => onOpenChange(false)} />
        )}
      </DialogContent>
    </Dialog>
  )
}

function RenameForm({
  upstream,
  onClose,
}: {
  upstream: Upstream
  onClose: () => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const [name, setName] = React.useState(upstream.name)
  const [email, setEmail] = React.useState(upstream.email)
  const login = upstream.view.imap.username
  const save = useMutation({
    mutationFn: () =>
      upstreamsApi.update(upstream.id, {
        name: name.trim() !== upstream.name ? name.trim() : undefined,
        // Saving the address as it is also confirms it.
        email:
          email.trim() !== upstream.email || upstream.addressPending
            ? email.trim()
            : undefined,
      }),
    onSuccess: () => {
      toast.success(t.mailboxes.renameSaved)
      qc.invalidateQueries({ queryKey: qk.upstreams })
      qc.invalidateQueries({ queryKey: ["overview"] })
      onClose()
    },
  })
  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {t.mailboxes.renameTitle(upstream.name || upstream.email)}
        </DialogTitle>
        <DialogDescription>{t.mailboxes.renameHint}</DialogDescription>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <FormField
          id="rename-name"
          label={t.mailboxes.accountName}
          required
          maxLength={64}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <FormField
          id="rename-email"
          label={t.mailboxes.mailboxAddress}
          type="email"
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          hint={
            upstream.addressPending
              ? t.mailboxes.addressPendingHint
              : upstream.kind !== "imap" && login && login !== email.trim()
                ? t.mailboxes.signInName(login)
                : undefined
          }
        />
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t.common.cancel}
          </Button>
          <Button
            type="submit"
            disabled={!name.trim() || !email.trim() || save.isPending}
          >
            {save.isPending && <Spinner />}
            {t.common.save}
          </Button>
        </DialogFooter>
      </form>
    </>
  )
}
