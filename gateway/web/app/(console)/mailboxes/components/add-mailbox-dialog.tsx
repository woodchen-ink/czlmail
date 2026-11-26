"use client"

import * as React from "react"
import Link from "next/link"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Check, Mail, Wand2 } from "lucide-react"
import { toast } from "sonner"

import { ConnectionReport } from "./connection-report"
import { ServerFields, type ServerDraft } from "./server-fields"
import { FormField } from "@/components/auth-form"
import { DocLink } from "@/components/doc-link"
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
import {
  oauthAppsApi,
  type OAuthApp,
  type OAuthProvider,
} from "@/lib/api/oauth-apps"
import {
  upstreamsApi,
  probeOk,
  type ProbeReport as Report,
  type UpstreamInput,
} from "@/lib/api/upstreams"
import { errorText } from "@/lib/errors"
import { isAdminRole, useMe } from "@/lib/hooks/use-me"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { cn } from "@/lib/utils"

type Provider = "imap" | "google" | "microsoft"

/** Server detection is shown once the gateway offers GET /upstreams/autoconfig. */
const AUTOCONFIG_READY = false

const emptyServer = (port: string): ServerDraft => ({
  host: "",
  port,
  tls: "tls",
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
  // GET /oauth-apps lists the providers this gateway build supports (site
  // admins only); without that answer every provider is offered and the start
  // call reports what is missing.
  const apps = useQuery({
    queryKey: qk.oauthApps,
    queryFn: oauthAppsApi.list,
    retry: false,
  })
  const offers = (p: OAuthProvider) =>
    !apps.data || apps.data.some((a) => a.provider === p)
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
          {offers("google") && (
            <ProviderButton
              mark="G"
              title={t.mailboxes.providerGoogle}
              hint={t.mailboxes.providerGoogleHint}
              onClick={() => setProvider("google")}
            />
          )}
          {offers("microsoft") && (
            <ProviderButton
              mark="M"
              title={t.mailboxes.providerMicrosoft}
              hint={t.mailboxes.providerMicrosoftHint}
              onClick={() => setProvider("microsoft")}
            />
          )}
        </div>
      )}
      {provider === "imap" && (
        <ImapForm onBack={() => setProvider(null)} onDone={onClose} />
      )}
      {(provider === "google" || provider === "microsoft") && (
        <OAuthStart
          provider={provider}
          app={apps.data?.find((a) => a.provider === provider)}
          appsError={apps.error}
          onBack={() => setProvider(null)}
        />
      )}
    </>
  )
}

function OAuthStart({
  provider,
  app,
  appsError,
  onBack,
}: {
  provider: OAuthProvider
  /** undefined while GET /oauth-apps has not answered or is not allowed. */
  app: OAuthApp | undefined
  appsError: unknown
  onBack: () => void
}) {
  const t = useT()
  const me = useMe()
  const [email, setEmail] = React.useState("")
  const [error, setError] = React.useState("")
  const start = useMutation({
    mutationFn: () =>
      upstreamsApi.startOAuth(
        provider,
        email.trim() ? { email: email.trim() } : {}
      ),
    meta: { silent: true },
    onSuccess: ({ url }) => window.location.assign(url),
    onError: (e) => setError(errorText(e)),
  })
  const name =
    provider === "google" ? t.oauth.google.name : t.oauth.microsoft.name
  // Known to be missing only when the admin could read the app settings.
  const missing = app ? !app.configured : false

  return (
    <form
      className="grid gap-4"
      onSubmit={(e) => {
        e.preventDefault()
        setError("")
        start.mutate()
      }}
    >
      {isNotReady(appsError) ? (
        <NotReady className="py-6" />
      ) : missing ? (
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
        <>
          <p className="text-sm text-foreground-muted">
            {provider === "google"
              ? t.mailboxes.providerGoogleHint
              : t.mailboxes.providerMicrosoftHint}
          </p>
          <FormField
            id="oauth-email"
            label={t.mailboxes.oauthLoginHint}
            type="email"
            autoComplete="off"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            hint={t.mailboxes.oauthLoginHintHint}
          />
        </>
      )}
      {error && <p className="text-sm text-destructive">{error}</p>}
      <DialogFooter>
        <DocLink
          topic={provider === "google" ? "gmail" : "microsoft"}
          className="self-center sm:mr-auto"
        />
        <Button type="button" variant="ghost" onClick={onBack}>
          <ArrowLeft /> {t.common.back}
        </Button>
        <Button type="submit" disabled={missing || start.isPending}>
          {start.isPending && <Spinner />}
          {t.mailboxes.oauthGo} · {name}
        </Button>
      </DialogFooter>
    </form>
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

  const body = (): UpstreamInput => {
    const user = username.trim() || email.trim()
    return {
      name: name.trim(),
      email: email.trim(),
      imap: {
        host: imap.host.trim(),
        port: Number(imap.port),
        tls: imap.tls,
        username: user,
        password,
      },
      smtp: {
        host: smtp.host.trim(),
        port: Number(smtp.port),
        tls: smtp.tls,
        username: sameCreds ? user : smtpUser.trim(),
        password: sameCreds ? password : smtpPass,
      },
      proxy: { mode: "follow" },
    }
  }

  // A test result counts only for the exact settings it was run with.
  const fingerprint = JSON.stringify(body())
  const report = tested?.fingerprint === fingerprint ? tested.report : null
  const reportOk = report ? probeOk(report) : false

  const detect = useMutation({
    mutationFn: () => upstreamsApi.autoconfig(email.trim()),
    meta: { silent: true },
    onSuccess: (r) => {
      if (r.imap)
        setImap({
          host: r.imap.host,
          port: String(r.imap.port),
          tls: r.imap.tls,
        })
      if (r.smtp)
        setSmtp({
          host: r.smtp.host,
          port: String(r.smtp.port),
          tls: r.smtp.tls,
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
        if (reportOk) save.mutate()
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

      {/* TODO(#39 follow-up): server detection (presets → ISPDB → autoconfig) has no API yet. */}
      <div
        className={cn("flex items-center gap-2", !AUTOCONFIG_READY && "hidden")}
      >
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
        <DocLink topic="imap" className="self-center sm:mr-auto" />
        <Button type="button" variant="ghost" onClick={onBack}>
          <ArrowLeft /> {t.common.back}
        </Button>
        {reportOk ? (
          <Button type="submit" disabled={save.isPending}>
            {save.isPending ? <Spinner /> : <Check />}
            {t.mailboxes.saveMailbox}
          </Button>
        ) : (
          <Button
            type="submit"
            variant={report && !reportOk ? "outline" : "default"}
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
