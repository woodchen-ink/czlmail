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
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Label } from "@/components/ui/label"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import {
  upstreamsApi,
  type ProxyMode,
  type Upstream,
} from "@/lib/api/upstreams"
import { useT } from "@/lib/i18n"
import { qk } from "@/lib/queries"

/** Per-mailbox SOCKS5 proxy: the gateway default, direct, or a custom one. */
export function ProxyDialog({
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
          <ProxyForm upstream={upstream} onClose={() => onOpenChange(false)} />
        )}
      </DialogContent>
    </Dialog>
  )
}

function ProxyForm({
  upstream,
  onClose,
}: {
  upstream: Upstream
  onClose: () => void
}) {
  const t = useT()
  const qc = useQueryClient()
  const stored = upstream.view.proxy
  const [mode, setMode] = React.useState<ProxyMode>(stored.mode)
  const [url, setUrl] = React.useState(
    stored.mode === "custom" ? stored.url : ""
  )
  const [password, setPassword] = React.useState("")

  const save = useMutation({
    mutationFn: () =>
      upstreamsApi.update(upstream.id, {
        proxy:
          mode === "custom"
            ? { mode, url: url.trim(), password: password || undefined }
            : { mode },
      }),
    onSuccess: () => {
      toast.success(t.mailboxes.proxySaved)
      qc.invalidateQueries({ queryKey: qk.upstreams })
      onClose()
    },
  })

  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {t.mailboxes.proxyTitle(upstream.name || upstream.email)}
        </DialogTitle>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
      >
        <RadioGroup
          value={mode}
          onValueChange={(v) => setMode(v as ProxyMode)}
          className="grid gap-2.5"
        >
          {(
            [
              ["follow", t.mailboxes.proxyModeDefault],
              ["none", t.mailboxes.proxyModeNone],
              ["custom", t.mailboxes.proxyModeCustom],
            ] as const
          ).map(([v, label]) => (
            <div key={v} className="flex items-center gap-2">
              <RadioGroupItem value={v} id={`proxy-${v}`} />
              <Label htmlFor={`proxy-${v}`} className="font-normal">
                {label}
              </Label>
            </div>
          ))}
        </RadioGroup>
        {mode === "custom" && (
          <>
            <FormField
              id="proxy-url"
              label={t.mailboxes.proxyUrl}
              placeholder="socks5://user@127.0.0.1:1080"
              hint={t.mailboxes.proxyUrlHint}
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              required
            />
            <FormField
              id="proxy-pass"
              label={t.mailboxes.proxyPassword}
              type="password"
              autoComplete="new-password"
              placeholder={stored.hasPassword ? t.mailboxes.keepStored : ""}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </>
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
