"use client"

import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { TlsMode } from "@/lib/api/upstreams"
import { useT } from "@/lib/i18n"

export interface ServerDraft {
  host: string
  port: string
  tls: TlsMode
}

/** Host, port and TLS mode of one server, on one row. Only encrypted connections are offered. */
export function ServerFields({
  id,
  title,
  value,
  onChange,
}: {
  id: string
  title: string
  value: ServerDraft
  onChange: (v: ServerDraft) => void
}) {
  const t = useT()
  return (
    <fieldset className="grid gap-1.5">
      <legend className="mb-1.5 text-sm font-medium">{title}</legend>
      <div className="grid grid-cols-[1fr_5.5rem_8rem] gap-2">
        <div className="grid gap-1">
          <Label
            htmlFor={`${id}-host`}
            className="text-xs text-foreground-muted"
          >
            {t.mailboxes.host}
          </Label>
          <Input
            id={`${id}-host`}
            value={value.host}
            onChange={(e) => onChange({ ...value, host: e.target.value })}
            placeholder={`${id}.example.com`}
            required
          />
        </div>
        <div className="grid gap-1">
          <Label
            htmlFor={`${id}-port`}
            className="text-xs text-foreground-muted"
          >
            {t.mailboxes.port}
          </Label>
          <Input
            id={`${id}-port`}
            inputMode="numeric"
            value={value.port}
            onChange={(e) =>
              onChange({ ...value, port: e.target.value.replace(/\D/g, "") })
            }
            required
          />
        </div>
        <div className="grid gap-1">
          <Label className="text-xs text-foreground-muted">
            {t.mailboxes.security}
          </Label>
          <Select
            value={value.tls}
            onValueChange={(v) => onChange({ ...value, tls: v as TlsMode })}
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="tls">{t.mailboxes.securityTls}</SelectItem>
              <SelectItem value="starttls">
                {t.mailboxes.securityStarttls}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
    </fieldset>
  )
}
