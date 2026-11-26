"use client"

import * as React from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ExternalLink,
  TriangleAlert,
} from "lucide-react"
import { toast } from "sonner"

import { FormField } from "@/components/auth-form"
import { CopyButton, CopyField } from "@/components/copy-button"
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
  OAUTH_DEFAULTS,
  oauthAppsApi,
  type OAuthApp,
  type OAuthProvider,
} from "@/lib/api/oauth-apps"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"
import { cn } from "@/lib/utils"

const CONSOLE = {
  google: {
    gmailApi:
      "https://console.cloud.google.com/apis/library/gmail.googleapis.com",
    consent: "https://console.cloud.google.com/auth/overview",
    clients: "https://console.cloud.google.com/auth/clients",
  },
  microsoft: {
    register:
      "https://entra.microsoft.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
  },
}

function ExtLink({
  href,
  children,
}: {
  href: string
  children: React.ReactNode
}) {
  return (
    <Button asChild variant="outline" size="sm">
      <a href={href} target="_blank" rel="noreferrer noopener">
        {children} <ExternalLink />
      </a>
    </Button>
  )
}

function Steps({ steps, current }: { steps: string[]; current: number }) {
  return (
    <ol className="flex items-center gap-2 text-xs">
      {steps.map((s, i) => (
        <li key={s} className="flex items-center gap-2">
          <span
            className={cn(
              "flex size-5 items-center justify-center rounded-full border text-[11px] font-semibold tabular-nums",
              i === current &&
                "border-primary bg-primary text-primary-foreground",
              i < current && "border-transparent bg-secondary",
              i > current && "text-foreground-subtle"
            )}
          >
            {i < current ? <Check className="size-3" /> : i + 1}
          </span>
          <span
            className={cn(
              i === current ? "font-medium" : "text-foreground-muted",
              "max-sm:hidden"
            )}
          >
            {s}
          </span>
          {i < steps.length - 1 && <span className="h-px w-4 bg-border" />}
        </li>
      ))}
    </ol>
  )
}

function Bullets({ items }: { items: string[] }) {
  return (
    <ol className="grid list-decimal gap-1.5 pl-5 text-sm text-foreground-muted marker:text-foreground-subtle">
      {items.map((s) => (
        <li key={s}>{s}</li>
      ))}
    </ol>
  )
}

function Warn({ title, body }: { title: string; body: string }) {
  return (
    <div className="flex gap-2 rounded-lg border border-warning-border bg-warning-soft px-3 py-2.5">
      <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warning" />
      <div>
        <p className="text-sm font-medium text-warning">{title}</p>
        <p className="mt-0.5 text-xs text-foreground-muted">{body}</p>
      </div>
    </div>
  )
}

function Scopes({ scopes }: { scopes: string[] }) {
  const t = useT()
  return (
    <div className="grid gap-1.5">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">{t.oauth.scopes}</span>
        <CopyButton
          value={scopes.join(" ")}
          label={t.oauth.copyAll}
          size="sm"
        />
      </div>
      <div className="grid gap-1">
        {scopes.map((s) => (
          <CopyField key={s} value={s} />
        ))}
      </div>
    </div>
  )
}

/** Three-step setup of the Google or Microsoft OAuth app, with copyable redirect URI and scopes. */
export function OAuthWizard({
  provider,
  app,
  ready,
  onOpenChange,
}: {
  provider: OAuthProvider | null
  app?: OAuthApp
  /** The OAuth apps API exists; saving is disabled until it does. */
  ready: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={!!provider} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-xl">
        {provider && (
          <WizardBody
            key={provider}
            provider={provider}
            app={app}
            ready={ready}
            onClose={() => onOpenChange(false)}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function WizardBody({
  provider,
  app,
  ready,
  onClose,
}: {
  provider: OAuthProvider
  app?: OAuthApp
  ready: boolean
  onClose: () => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const [step, setStep] = React.useState(0)
  const [clientId, setClientId] = React.useState(app?.clientId ?? "")
  const [secret, setSecret] = React.useState("")
  const [tenant, setTenant] = React.useState(app?.tenant || "common")
  const [expires, setExpires] = React.useState(
    app?.secretExpiresAt ? app.secretExpiresAt.slice(0, 10) : ""
  )

  const save = useMutation({
    mutationFn: () =>
      oauthAppsApi.save(provider, {
        clientId: clientId.trim(),
        clientSecret: secret || undefined,
        ...(provider === "microsoft"
          ? {
              tenant: tenant.trim() || "common",
              secretExpiresAt: expires
                ? new Date(expires + "T00:00:00Z").toISOString()
                : undefined,
            }
          : {}),
      }),
    onSuccess: () => {
      toast.success(t.oauth.saved)
      qc.invalidateQueries({ queryKey: qk.oauthApps })
      qc.invalidateQueries({ queryKey: ["overview"] })
      onClose()
    },
  })

  const copy = provider === "google" ? t.oauth.google : t.oauth.microsoft
  const defaults = OAUTH_DEFAULTS[provider]
  const origin = typeof window === "undefined" ? "" : window.location.origin
  const redirectUri = app?.redirectUri || origin + defaults.callbackPath
  const scopes = app?.scopes?.length ? app.scopes : defaults.scopes
  const last = step === 2
  const canSave =
    ready &&
    clientId.trim() &&
    (secret || app?.configured) &&
    (provider === "google" || expires)

  return (
    <>
      <DialogHeader>
        <DialogTitle>{t.oauth.wizardTitle(copy.name)}</DialogTitle>
        <DialogDescription asChild>
          <div className="pt-1">
            <Steps steps={copy.steps} current={step} />
          </div>
        </DialogDescription>
      </DialogHeader>

      <div className="grid max-h-[60dvh] gap-4 overflow-y-auto pr-1">
        {provider === "google" && step === 0 && (
          <>
            <Bullets items={t.oauth.google.s1} />
            <div className="flex flex-wrap gap-2">
              <ExtLink href={CONSOLE.google.gmailApi}>Gmail API</ExtLink>
              <ExtLink href={CONSOLE.google.consent}>
                {t.oauth.google.consoleProject}
              </ExtLink>
            </div>
            <Scopes scopes={scopes} />
            <Warn
              title={t.oauth.google.publishWarnTitle}
              body={t.oauth.google.publishWarnBody}
            />
          </>
        )}
        {provider === "google" && step === 1 && (
          <>
            <Bullets items={t.oauth.google.s2} />
            <div className="grid gap-1.5">
              <span className="text-sm font-medium">{t.oauth.redirectUri}</span>
              <CopyField value={redirectUri} />
            </div>
            <div>
              <ExtLink href={CONSOLE.google.clients}>
                {t.oauth.google.consoleClients}
              </ExtLink>
            </div>
          </>
        )}
        {provider === "microsoft" && step === 0 && (
          <>
            <Bullets items={t.oauth.microsoft.s1} />
            <div className="grid gap-1.5">
              <span className="text-sm font-medium">{t.oauth.redirectUri}</span>
              <CopyField value={redirectUri} />
            </div>
            <div>
              <ExtLink href={CONSOLE.microsoft.register}>
                {t.oauth.microsoft.consoleRegister}
              </ExtLink>
            </div>
          </>
        )}
        {provider === "microsoft" && step === 1 && (
          <>
            <Bullets items={t.oauth.microsoft.s2} />
            <Scopes scopes={scopes} />
            <Warn
              title={t.oauth.microsoft.secretWarnTitle}
              body={t.oauth.microsoft.secretWarnBody}
            />
          </>
        )}
        {last && (
          <form
            id="oauth-form"
            className="grid gap-3"
            onSubmit={(e) => {
              e.preventDefault()
              if (canSave) save.mutate()
            }}
          >
            <p className="text-sm text-foreground-muted">{copy.s3}</p>
            <FormField
              id="oa-id"
              label={t.oauth.clientId}
              required
              value={clientId}
              onChange={(e) => setClientId(e.target.value)}
              autoComplete="off"
            />
            <FormField
              id="oa-secret"
              label={t.oauth.clientSecret}
              type="password"
              autoComplete="new-password"
              required={!app?.configured}
              hint={app?.configured ? t.oauth.clientSecretKeep : undefined}
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
            />
            {provider === "microsoft" && (
              <div className="grid gap-3 sm:grid-cols-2">
                <FormField
                  id="oa-exp"
                  label={t.oauth.secretExpires}
                  type="date"
                  required
                  value={expires}
                  onChange={(e) => setExpires(e.target.value)}
                  hint={t.oauth.secretExpiresHint}
                />
                <FormField
                  id="oa-tenant"
                  label={t.oauth.tenant}
                  value={tenant}
                  onChange={(e) => setTenant(e.target.value)}
                  hint={t.oauth.tenantHint}
                />
              </div>
            )}
            {!ready && (
              <p className="text-sm text-warning">{t.common.notReadyTitle}</p>
            )}
          </form>
        )}
      </div>

      <DialogFooter>
        {step > 0 ? (
          <Button variant="ghost" onClick={() => setStep(step - 1)}>
            <ArrowLeft /> {t.common.prev}
          </Button>
        ) : (
          <Button variant="ghost" onClick={onClose}>
            {t.common.cancel}
          </Button>
        )}
        {last ? (
          <Button
            type="submit"
            form="oauth-form"
            disabled={!canSave || save.isPending}
          >
            {save.isPending && <Spinner />}
            {t.common.save}
          </Button>
        ) : (
          <Button onClick={() => setStep(step + 1)}>
            {t.common.next} <ArrowRight />
          </Button>
        )}
      </DialogFooter>
    </>
  )
}
