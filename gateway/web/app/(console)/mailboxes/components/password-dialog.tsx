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
import {
  upstreamsApi,
  type EndpointInput,
  type Upstream,
  type UpstreamView,
} from "@/lib/api/upstreams"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

/** Re-authorise an IMAP mailbox: a new password (or authorisation code) for IMAP and SMTP. */
export function PasswordDialog({
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
          <PasswordForm
            upstream={upstream}
            onClose={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function endpoint(e: UpstreamView["imap"], password: string): EndpointInput {
  return {
    host: e.host,
    port: e.port,
    tls: e.tls,
    username: e.username,
    password,
  }
}

function PasswordForm({
  upstream,
  onClose,
}: {
  upstream: Upstream
  onClose: () => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const [password, setPassword] = React.useState("")
  const save = useMutation({
    mutationFn: () =>
      upstreamsApi.update(upstream.id, {
        imap: endpoint(upstream.view.imap, password),
        // SMTP signs in with the same account in the usual setup; a separate SMTP
        // login keeps its own password.
        smtp:
          upstream.view.smtp.host &&
          upstream.view.smtp.username === upstream.view.imap.username
            ? endpoint(upstream.view.smtp, password)
            : undefined,
      }),
    onSuccess: () => {
      toast.success(t.mailboxes.passwordSaved)
      qc.invalidateQueries({ queryKey: qk.upstreams })
      qc.invalidateQueries({ queryKey: ["overview"] })
      onClose()
    },
  })
  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {t.mailboxes.passwordTitle(upstream.name || upstream.email)}
        </DialogTitle>
        <DialogDescription>{t.mailboxes.passwordHint}</DialogDescription>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <FormField
          id="new-pass"
          label={t.mailboxes.password}
          type="password"
          autoComplete="new-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <DialogFooter>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t.common.cancel}
          </Button>
          <Button type="submit" disabled={!password || save.isPending}>
            {save.isPending && <Spinner />}
            {t.common.save}
          </Button>
        </DialogFooter>
      </form>
    </>
  )
}
