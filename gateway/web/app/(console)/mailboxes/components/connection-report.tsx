"use client"

import { CircleCheck, CircleX } from "lucide-react"

import { useUpstreamErrorText } from "@/components/upstream-card"
import {
  capabilityNames,
  watchModeOf,
  type ProbePart,
  type ProbeReport,
} from "@/lib/api/upstreams"
import { useT } from "@/lib/i18n"

/** Result of "test connection": IMAP and SMTP, the watch mode the capabilities allow, and the capabilities. */
export function ConnectionReport({ report }: { report: ProbeReport }) {
  const t = useT()
  const errText = useUpstreamErrorText()
  const watch = watchModeOf(report.imap.capabilities)
  const caps = capabilityNames(report.imap.capabilities)

  const line = (label: string, part: ProbePart, okText: string) => (
    <div className="flex items-start gap-2 text-sm">
      {part.ok ? (
        <CircleCheck className="mt-0.5 size-4 shrink-0 text-success" />
      ) : (
        <CircleX className="mt-0.5 size-4 shrink-0 text-destructive" />
      )}
      <span className="w-12 shrink-0 font-medium">{label}</span>
      <div className="min-w-0">
        <p className={part.ok ? "text-foreground-muted" : "text-destructive"}>
          {part.ok ? okText : errText(part.code ?? 0, part.msg ?? "")}
        </p>
        {/* The mail server's own reply: helps the owner fix their settings. */}
        {!part.ok && part.detail && (
          <p className="mt-0.5 font-mono text-xs break-all text-foreground-subtle">
            {part.detail}
          </p>
        )}
      </div>
    </div>
  )

  return (
    <div className="grid gap-2 rounded-lg border bg-muted p-3">
      {line(
        "IMAP",
        report.imap,
        watch
          ? `${t.mailboxes.testOk} · ${t.watchModes[watch]}`
          : t.mailboxes.testOk
      )}
      {report.smtp && line("SMTP", report.smtp, t.mailboxes.testOk)}
      {caps.length > 0 && (
        <div className="mt-1">
          <p className="mb-1 text-xs text-foreground-muted">
            {t.mailboxes.capabilities}
          </p>
          <div className="flex flex-wrap gap-1">
            {caps.map((c) => (
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
