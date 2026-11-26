"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Check, Mail, Wand2 } from "lucide-react"
import { toast } from "sonner"

import { ConnectionReport } from "./connection-report"
import { ServerFields, type ServerDraft } from "./server-fields"
import { FormField } from "@/components/auth-form"
import { Spinner } from "@/components/spinner"
import { NotReady } from "@/components/state-views"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { isNotReady } from "@/lib/api/client"
import { oauthAppsApi } from "@/lib/api/oauth-apps"
import {
  upstreamsApi,
  type ConnectionReport as Report,
  type NewImapUpstream,
} from "@/lib/api/upstreams"
import { errorText } from "@/lib/errors"
import { isAdminRole, useMe } from "@/lib/hooks/use-me"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { cn } from "@/lib/utils"

type Provider = "imap" | "google" | "microsoft"

const emptyServer = (port: string): ServerDraft => ({
  host: "",
  port,
  security: "tls",
})

function ProviderButton({
  title,
  hint,
  onClick,
  mark,
}: {
  title: string
  hint: string
  onClick: () => void
  mark: string
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center gap-3 rounded-lg border bg-card px-3 py-3 text-left transition-colors hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none"
    >
      <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-sm font-semibold text-foreground-muted">
        {mark}
      </span>
      <span className="min-w-0">
        <span className="block text-sm font-medium">{title}</span>
        <span className="block text-xs text-foreground-muted">{hint}</span>
      </span>
    </button>
  )
}

/** Steps: provider → (IMAP) account and servers, test, save; (OAuth) consent at the provider. */
export function AddMailboxDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        {/* The body unmounts on close, so the next open starts at the provider choice. */}
        <AddMailboxBody onClose={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

function AddMailboxBody({ onClose }: { onClose: () => void }) {
  const t = useT()
  const [provider, setProvider] = React.useState<Provider | null>(null)
  return (
    <>
      <DialogHeader>
        <DialogTitle>{t.mailboxes.addTitle}</DialogTitle>
        <DialogDescription>
          {provider ? null : t.mailboxes.chooseProvider}
        </DialogDescription>
      </DialogHeader>
      {provider === null && (
        <div className="grid gap-2">
          <ProviderButton
            mark="@"
            title={t.mailboxes.providerImap}
            hint={t.mailboxes.providerImapHint}
            onClick={() => setProvider("imap")}
          />
          <ProviderButton
            mark="G"
            title={t.mailboxes.providerGoogle}
            hint={t.mailboxes.providerGoogleHint}
            onClick={() => setProvider("google")}
          />
          <ProviderButton
            mark="M"
            title={t.mailboxes.providerMicrosoft}
            hint={t.mailboxes.providerMicrosoftHint}
            onClick={() => setProvider("microsoft")}
          />
        </div>
      )}
      {provider === "imap" && (
        <ImapForm onBack={() => setProvider(null)} onDone={onClose} />
      )}
      {(provider === "google" || provider === "microsoft") && (
        <OAuthStart provider={provider} onBack={() => setProvider(null)} />
      )}
    </>
  )
}

function OAuthStart({
  provider,
  onBack,
}: {
  provider: "google" | "microsoft"
  onBack: () => void
}) {
  const t = useT()
  const me = useMe()
  const apps = useQuery({ queryKey: qk.oauthApps, queryFn: oauthAppsApi.list })
  const start = useMutation({
    mutationFn: () => upstreamsApi.startOAuth(provider),
    onSuccess: ({ url }) => window.location.assign(url),
  })
  const app = apps.data?.find((a) => a.provider === provider)
  const name =
    provider === "google" ? t.oauth.google.name : t.oauth.microsoft.name

  return (
    <div className="grid gap-4">
      {apps.isPending ? (
        <Spinner className="mx-auto my-6" />
      ) : isNotReady(apps.error) ? (
        <NotReady className="py-6" />
      ) : !app?.configured ? (
        <div className="rounded-lg border border-warning-border bg-warning-soft px-3 py-2.5 text-sm text-warning">
          {t.mailboxes.oauthNotConfigured}
          {isAdminRole(me.data?.role) && (
            <Link
              href={`/oauth-apps/?provider=${provider}`}
              className="ml-2 text-link hover:underline"
            >
              {t.oauth.configure}
            </Link>
          )}
        </div>
      ) : (
        <p className="text-sm text-foreground-muted">
          {provider === "google"
            ? t.mailboxes.providerGoogleHint
            : t.mailboxes.providerMicrosoftHint}
        </p>
      )}
      <DialogFooter>
        <Button variant="ghost" onClick={onBack}>
          <ArrowLeft /> {t.common.back}
        </Button>
        <Button
          disabled={!app?.configured || start.isPending}
          onClick={() => start.mutate()}
        >
          {start.isPending && <Spinner />}
          {t.mailboxes.oauthGo} · {name}
        </Button>
      </DialogFooter>
    </div>
  )
}

function ImapForm({
  onBack,
  onDone,
}: {
  onBack: () => void
  onDone: () => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const [name, setName] = React.useState("")
  const [email, setEmail] = React.useState("")
  const [password, setPassword] = React.useState("")
  const [username, setUsername] = React.useState("")
  const [imap, setImap] = React.useState<ServerDraft>(emptyServer("993"))
  const [smtp, setSmtp] = React.useState<ServerDraft>(emptyServer("465"))
  const [sameCreds, setSameCreds] = React.useState(true)
  const [smtpUser, setSmtpUser] = React.useState("")
  const [smtpPass, setSmtpPass] = React.useState("")
  const [detected, setDetected] = React.useState<string>("")
  const [tested, setTested] = React.useState<{
    fingerprint: string
    report: Report
  } | null>(null)
  const [error, setError] = React.useState("")

  const body = (): NewImapUpstream => {
    const user = username.trim() || email.trim()
    return {
      kind: "imap",
      name: name.trim(),
      email: email.trim(),
      imap: {
        host: imap.host.trim(),
        port: Number(imap.port),
        security: imap.security,
        username: user,
        password,
      },
      smtp: {
        host: smtp.host.trim(),
        port: Number(smtp.port),
        security: smtp.security,
        username: sameCreds ? user : smtpUser.trim(),
        password: sameCreds ? password : smtpPass,
      },
      proxy: { mode: "default" },
    }
  }

  // A test result counts only for the exact settings it was run with.
  const fingerprint = JSON.stringify(body())
  const report = tested?.fingerprint === fingerprint ? tested.report : null

  const detect = useMutation({
    mutationFn: () => upstreamsApi.autoconfig(email.trim()),
    meta: { silent: true },
    onSuccess: (r) => {
      if (r.imap)
        setImap({
          host: r.imap.host,
          port: String(r.imap.port),
          security: r.imap.security,
        })
      if (r.smtp)
        setSmtp({
          host: r.smtp.host,
          port: String(r.smtp.port),
          security: r.smtp.security,
        })
      setDetected(t.mailboxes.detectedFrom[r.source])
    },
    onError: (e) => setDetected(errorText(e)),
  })
  const test = useMutation({
    mutationFn: async () => ({
      fingerprint,
      report: await upstreamsApi.test(body()),
    }),
    meta: { silent: true },
    onSuccess: setTested,
    onError: (e) => setError(errorText(e)),
  })
  const save = useMutation({
    mutationFn: () => upstreamsApi.create(body()),
    meta: { silent: true },
    onSuccess: () => {
      toast.success(t.mailboxes.added)
      qc.invalidateQueries({ queryKey: qk.upstreams })
      qc.invalidateQueries({ queryKey: ["overview"] })
      onDone()
    },
    onError: (e) => setError(errorText(e)),
  })

  const filled =
    email.trim() &&
    password &&
    imap.host.trim() &&
    imap.port &&
    smtp.host.trim() &&
    smtp.port
  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        setError("")
        if (report?.ok) save.mutate()
        else test.mutate()
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <FormField
          id="mb-email"
          label={t.mailboxes.emailAddress}
          type="email"
          required
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <FormField
          id="mb-name"
          label={t.mailboxes.displayName}
          placeholder={t.mailboxes.displayNamePlaceholder}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <FormField
          id="mb-user"
          label={t.mailboxes.username}
          placeholder={email || t.mailboxes.usernameHint}
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
        <FormField
          id="mb-pass"
          label={t.mailboxes.password}
          type="password"
          autoComplete="new-password"
          required
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
      </div>
      <p className="-mt-2 text-xs text-foreground-subtle">
        {t.mailboxes.passwordHint}
      </p>

      <div className="flex items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!email.includes("@") || detect.isPending}
          onClick={() => detect.mutate()}
        >
          {detect.isPending ? <Spinner /> : <Wand2 />}
          {detect.isPending ? t.mailboxes.detecting : t.mailboxes.detect}
        </Button>
        {detected && (
          <span className="text-xs text-foreground-muted">{detected}</span>
        )}
      </div>

      <div className="grid gap-3 rounded-lg border p-3">
        <ServerFields
          id="imap"
          title={t.mailboxes.imapServer}
          value={imap}
          onChange={setImap}
        />
        <ServerFields
          id="smtp"
          title={t.mailboxes.smtpServer}
          value={smtp}
          onChange={setSmtp}
        />
        <div className="flex items-center gap-2">
          <Switch
            id="same-creds"
            checked={sameCreds}
            onCheckedChange={setSameCreds}
          />
          <Label htmlFor="same-creds" className="text-sm font-normal">
            {t.mailboxes.smtpSameCredentials}
          </Label>
        </div>
        {!sameCreds && (
          <div className="grid gap-3 sm:grid-cols-2">
            <FormField
              id="smtp-user"
              label={t.mailboxes.username}
              value={smtpUser}
              onChange={(e) => setSmtpUser(e.target.value)}
            />
            <FormField
              id="smtp-pass"
              label={t.mailboxes.password}
              type="password"
              autoComplete="new-password"
              value={smtpPass}
              onChange={(e) => setSmtpPass(e.target.value)}
            />
          </div>
        )}
        <p className="text-xs text-foreground-subtle">{t.mailboxes.tlsOnly}</p>
      </div>

      {report ? (
        <ConnectionReport report={report} />
      ) : (
        <p className="text-xs text-foreground-muted">
          {t.mailboxes.testBeforeSave}
        </p>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}

      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onBack}>
          <ArrowLeft /> {t.common.back}
        </Button>
        {report?.ok ? (
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? <Spinner /> : <Check />}
            {t.mailboxes.saveMailbox}
          </Button>
        ) : (
          <Button
            type="submit"
            variant={report && !report.ok ? "outline" : "default"}
            disabled={!filled || test.isPending}
            className={cn(!filled && "opacity-50")}
          >
            {test.isPending ? <Spinner /> : <Mail />}
            {test.isPending ? t.mailboxes.testing : t.mailboxes.runTest}
          </Button>
        )}
      </DialogFooter>
    </form>
  )
}
