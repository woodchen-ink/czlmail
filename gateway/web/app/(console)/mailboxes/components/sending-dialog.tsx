"use client"

import * as React from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeft,
  CircleCheck,
  CircleX,
  Pencil,
  Plus,
  Send,
  Trash2,
  UserRound,
} from "lucide-react"
import { toast } from "sonner"

import { FormField } from "@/components/auth-form"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { Segmented } from "@/components/segmented"
import { Spinner } from "@/components/spinner"
import { Chip } from "@/components/status-badge"
import { EmptyState, ErrorState } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { useUpstreamErrorText } from "@/components/upstream-card"
import { formatAddresses, parseAddresses } from "@/lib/addresses"
import {
  upstreamsApi,
  type Identity,
  type IdentityInput,
  type SentCopy,
  type SentCopyTest,
  type Upstream,
} from "@/lib/api/upstreams"
import { errorText } from "@/lib/errors"
import { formatMs } from "@/lib/format"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { cn } from "@/lib/utils"

const SENT_COPY: SentCopy[] = ["auto", "gateway", "server"]

/** Sending settings of one mailbox: where the Sent copy comes from, and its identities. */
export function SendingDialog({
  upstream,
  onOpenChange,
}: {
  upstream: Upstream | null
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={!!upstream} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        {upstream && <SendingBody key={upstream.id} upstream={upstream} />}
      </DialogContent>
    </Dialog>
  )
}

function SendingBody({ upstream }: { upstream: Upstream }) {
  const t = useT()
  // null: the identity list; an identity or "new": its editor.
  const [editing, setEditing] = React.useState<Identity | "new" | null>(null)
  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {t.mailboxes.sendingTitle(upstream.name || upstream.email)}
        </DialogTitle>
      </DialogHeader>
      {editing ? (
        <IdentityForm
          upstreamId={upstream.id}
          identity={editing === "new" ? null : editing}
          onDone={() => setEditing(null)}
        />
      ) : (
        <div className="grid max-h-[70dvh] gap-5 overflow-y-auto pr-1">
          {upstream.kind === "gmail" ? (
            // Gmail sends through its API and files the Sent copy itself.
            <section>
              <h3 className="text-sm font-semibold">
                {t.mailboxes.sentCopyTitle}
              </h3>
              <p className="mt-0.5 text-xs text-foreground-muted">
                {t.mailboxes.sentCopyByProvider}
              </p>
            </section>
          ) : (
            <SentCopySection upstream={upstream} />
          )}
          <div className="border-t" />
          <IdentityList upstreamId={upstream.id} onEdit={setEditing} />
        </div>
      )}
    </>
  )
}

function SentCopySection({ upstream }: { upstream: Upstream }) {
  const t = useT()
  const qc = useQueryClient()
  const errText = useUpstreamErrorText()
  const [mode, setMode] = React.useState<SentCopy>(
    upstream.view.sentCopy || "auto"
  )
  const [result, setResult] = React.useState<SentCopyTest | null>(null)

  const save = useMutation({
    mutationFn: (m: SentCopy) => upstreamsApi.setSentCopy(upstream.id, m),
    onMutate: (m) => setMode(m),
    onSuccess: (v) => {
      setMode(v.sentCopy)
      toast.success(t.mailboxes.sentCopySaved)
      qc.invalidateQueries({ queryKey: qk.upstreams })
    },
    onError: () => setMode(upstream.view.sentCopy || "auto"),
  })
  const test = useMutation({
    mutationFn: () => upstreamsApi.sendTest(upstream.id),
    onMutate: () => setResult(null),
    onSuccess: (r) => {
      setResult(r)
      if (r.ok) {
        setMode(r.sentCopy)
        qc.invalidateQueries({ queryKey: qk.upstreams })
      }
    },
  })

  return (
    <section className="grid gap-3">
      <div>
        <h3 className="text-sm font-semibold">{t.mailboxes.sentCopyTitle}</h3>
        <p className="mt-0.5 text-xs text-foreground-muted">
          {t.mailboxes.sentCopyHint}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Segmented
          value={mode}
          onChange={(m) => m !== mode && save.mutate(m)}
          options={SENT_COPY.map((m) => ({
            value: m,
            label: t.mailboxes.sentCopy[m],
          }))}
          aria-label={t.mailboxes.sentCopyTitle}
        />
        <Button
          variant="outline"
          size="sm"
          disabled={test.isPending || save.isPending}
          onClick={() => test.mutate()}
          title={t.mailboxes.sendTestHint}
        >
          {test.isPending ? <Spinner /> : <Send />}
          {test.isPending ? t.mailboxes.sendTesting : t.mailboxes.sendTest}
        </Button>
      </div>
      {mode === "auto" && !result && (
        <p className="text-xs text-foreground-subtle">
          {t.mailboxes.sentCopyAutoHint}
        </p>
      )}
      {mode === "server" && upstream.kind === "microsoft" && !result && (
        <p className="text-xs text-foreground-subtle">
          {t.mailboxes.sentCopyServerPreset}
        </p>
      )}
      {result && (
        <div
          className={cn(
            "flex items-start gap-2 rounded-lg border bg-muted p-3 text-sm"
          )}
        >
          {result.ok ? (
            <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" />
          ) : (
            <CircleX className="mt-0.5 size-4 shrink-0 text-destructive" />
          )}
          <div className="min-w-0">
            {result.ok ? (
              <>
                <p>
                  {result.serverSaves
                    ? t.mailboxes.sendTestServerSaves
                    : t.mailboxes.sendTestGatewaySaves}
                </p>
                <p className="text-xs text-foreground-muted">
                  {t.mailboxes.sendTestResult(
                    t.mailboxes.sentCopy[result.sentCopy] ?? result.sentCopy,
                    formatMs(result.waitedMs)
                  )}
                </p>
              </>
            ) : (
              <>
                <p className="text-destructive">
                  {t.mailboxes.sendTestFailed}:{" "}
                  {errText(result.code ?? 0, result.msg ?? "")}
                </p>
                {result.detail && (
                  <p className="mt-0.5 font-mono text-xs break-all text-foreground-subtle">
                    {result.detail}
                  </p>
                )}
              </>
            )}
          </div>
        </div>
      )}
    </section>
  )
}

function IdentityList({
  upstreamId,
  onEdit,
}: {
  upstreamId: string
  onEdit: (i: Identity | "new") => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const list = useQuery({
    queryKey: qk.identities(upstreamId),
    queryFn: () => upstreamsApi.identities(upstreamId),
  })
  const [deleting, setDeleting] = React.useState<Identity | null>(null)
  const remove = useMutation({
    mutationFn: (i: Identity) => upstreamsApi.deleteIdentity(upstreamId, i.id),
    onSuccess: () => {
      toast.success(t.mailboxes.identityDeleted)
      setDeleting(null)
      qc.invalidateQueries({ queryKey: qk.identities(upstreamId) })
    },
  })

  return (
    <section className="grid gap-3">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-semibold">{t.mailboxes.identities}</h3>
          <p className="mt-0.5 text-xs text-foreground-muted">
            {t.mailboxes.identitiesHint}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => onEdit("new")}>
          <Plus /> {t.mailboxes.identityAdd}
        </Button>
      </div>
      {list.isPending ? (
        <div className="grid gap-2">
          <Skeleton className="h-12" />
          <Skeleton className="h-12" />
        </div>
      ) : list.isError ? (
        <ErrorState
          error={list.error}
          onRetry={() => list.refetch()}
          className="py-6"
        />
      ) : list.data.length === 0 ? (
        <EmptyState
          icon={UserRound}
          title={t.mailboxes.identityEmpty}
          className="py-6"
        />
      ) : (
        <ul className="divide-y rounded-lg border">
          {list.data.map((i) => (
            <li key={i.id} className="flex items-center gap-3 px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="truncate text-sm font-medium">
                    {i.email}
                  </span>
                  <Chip tone={i.source === "alias" ? "info" : "neutral"}>
                    {t.mailboxes.identitySource[i.source] ?? i.source}
                  </Chip>
                </div>
                <p className="truncate text-xs text-foreground-muted">
                  {[
                    i.name,
                    i.replyTo.length
                      ? `${t.mailboxes.identityReplyTo}: ${formatAddresses(i.replyTo)}`
                      : "",
                  ]
                    .filter(Boolean)
                    .join(" · ") || t.common.emptyValue}
                </p>
              </div>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t.mailboxes.identityEdit}
                onClick={() => onEdit(i)}
              >
                <Pencil />
              </Button>
              {i.mayDelete && (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t.common.delete}
                  className="text-destructive hover:text-destructive"
                  onClick={() => setDeleting(i)}
                >
                  <Trash2 />
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={t.mailboxes.identityDeleteTitle(deleting?.email ?? "")}
        body={t.mailboxes.identityDeleteBody}
        confirmLabel={t.common.delete}
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </section>
  )
}

/** Add an alias, or edit any identity (the address only for aliases). */
function IdentityForm({
  upstreamId,
  identity,
  onDone,
}: {
  upstreamId: string
  identity: Identity | null
  onDone: () => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const emailEditable = !identity || identity.source === "alias"
  const [email, setEmail] = React.useState(identity?.email ?? "")
  const [name, setName] = React.useState(identity?.name ?? "")
  const [replyTo, setReplyTo] = React.useState(
    formatAddresses(identity?.replyTo ?? [])
  )
  const [bcc, setBcc] = React.useState(formatAddresses(identity?.bcc ?? []))
  const [textSig, setTextSig] = React.useState(identity?.textSignature ?? "")
  const [htmlSig, setHtmlSig] = React.useState(identity?.htmlSignature ?? "")
  const [error, setError] = React.useState("")

  const replyList = parseAddresses(replyTo)
  const bccList = parseAddresses(bcc)

  const save = useMutation({
    mutationFn: () => {
      const body: IdentityInput = {
        name: name.trim(),
        replyTo: replyList ?? [],
        bcc: bccList ?? [],
        textSignature: textSig,
        htmlSignature: htmlSig,
      }
      if (emailEditable) body.email = email.trim()
      return identity
        ? upstreamsApi.updateIdentity(upstreamId, identity.id, body)
        : upstreamsApi.createIdentity(upstreamId, body)
    },
    meta: { silent: true },
    onSuccess: () => {
      toast.success(t.mailboxes.identitySaved)
      qc.invalidateQueries({ queryKey: qk.identities(upstreamId) })
      onDone()
    },
    onError: (e) => setError(errorText(e)),
  })

  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        setError("")
        if (!replyList || !bccList)
          return setError(t.mailboxes.addressListInvalid)
        save.mutate()
      }}
    >
      <h3 className="text-sm font-semibold">
        {identity ? t.mailboxes.identityEdit : t.mailboxes.identityAdd}
      </h3>
      <div className="grid max-h-[60dvh] items-start gap-3 overflow-y-auto pr-1 sm:grid-cols-2">
        <FormField
          id="id-email"
          label={t.mailboxes.identityEmail}
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          disabled={!emailEditable}
          required={emailEditable}
          hint={emailEditable ? t.mailboxes.identityEmailHint : undefined}
          placeholder="me@example.com"
        />
        <FormField
          id="id-name"
          label={t.mailboxes.identityName}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <FormField
          id="id-reply"
          label={t.mailboxes.identityReplyTo}
          value={replyTo}
          onChange={(e) => setReplyTo(e.target.value)}
          hint={t.mailboxes.addressListHint}
          error={replyList ? undefined : t.mailboxes.addressListInvalid}
        />
        <FormField
          id="id-bcc"
          label={t.mailboxes.identityBcc}
          value={bcc}
          onChange={(e) => setBcc(e.target.value)}
          hint={t.mailboxes.addressListHint}
          error={bccList ? undefined : t.mailboxes.addressListInvalid}
        />
        <div className="grid gap-1.5 sm:col-span-2">
          <Label htmlFor="id-text-sig">
            {t.mailboxes.identityTextSignature}
          </Label>
          <Textarea
            id="id-text-sig"
            rows={3}
            value={textSig}
            onChange={(e) => setTextSig(e.target.value)}
            placeholder={t.mailboxes.noSignature}
          />
        </div>
        <div className="grid gap-1.5 sm:col-span-2">
          <Label htmlFor="id-html-sig">
            {t.mailboxes.identityHtmlSignature}
          </Label>
          <Textarea
            id="id-html-sig"
            rows={3}
            className="font-mono text-xs"
            value={htmlSig}
            onChange={(e) => setHtmlSig(e.target.value)}
            placeholder="<p>…</p>"
          />
        </div>
      </div>
      {error && <p className="text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone}>
          <ArrowLeft /> {t.common.back}
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending && <Spinner />}
          {t.common.save}
        </Button>
      </DialogFooter>
    </form>
  )
}
