"use client"

import { CircleCheck, CircleX } from "lucide-react"

import { useUpstreamErrorText } from "@/components/upstream-card"
import type { ConnectionReport as Report } from "@/lib/api/upstreams"
import { useT } from "@/lib/i18n"

/** Result of "test connection": IMAP and SMTP, the detected watch mode and server capabilities. */
export function ConnectionReport({ report }: { report: Report }) {
  const t = useT()
  const errText = useUpstreamErrorText()
  const line = (
    label: string,
    ok: boolean,
    code: number,
    msg: string,
    extra?: string
  ) => (
    <div className="flex items-start gap-2 text-sm">
      {ok ? (
        <CircleCheck className="mt-0.5 size-4 text-success" />
      ) : (
        <CircleX className="mt-0.5 size-4 text-destructive" />
      )}
      <span className="w-12 shrink-0 font-medium">{label}</span>
      <span className={ok ? "text-foreground-muted" : "text-destructive"}>
        {ok ? (extra ?? t.mailboxes.testOk) : errText(code, msg)}
      </span>
    </div>
  )
  return (
    <div className="grid gap-2 rounded-lg border bg-muted p-3">
      {line(
        "IMAP",
        report.imap.ok,
        report.imap.code,
        report.imap.msg,
        report.imap.watchMode
          ? `${t.mailboxes.testOk} · ${t.watchModes[report.imap.watchMode as keyof typeof t.watchModes] ?? report.imap.watchMode}`
          : undefined
      )}
      {line("SMTP", report.smtp.ok, report.smtp.code, report.smtp.msg)}
      {report.imap.capabilities.length > 0 && (
        <div className="mt-1">
          <p className="mb-1 text-xs text-foreground-muted">
            {t.mailboxes.capabilities}
          </p>
          <div className="flex flex-wrap gap-1">
            {report.imap.capabilities.map((c) => (
              <span
                key={c}
                className="rounded-sm bg-card px-1.5 py-0.5 font-mono text-xs text-foreground-muted"
              >
                {c}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}
