import { cn } from "@/lib/utils"
import { useT } from "@/lib/i18n"
import type { UpstreamStatus } from "@/lib/types"

// Normal is quiet; only states that need attention get colour.
const tone: Record<UpstreamStatus, string> = {
  ok: "bg-secondary text-foreground-muted",
  syncing: "bg-info-soft text-info border-info-border",
  authRequired:
    "bg-destructive-soft text-destructive border-destructive-border",
  unreachable: "bg-warning-soft text-warning border-warning-border",
  paused: "bg-secondary text-foreground-subtle",
}

const dot: Record<UpstreamStatus, string> = {
  ok: "bg-success",
  syncing: "bg-info",
  authRequired: "bg-destructive",
  unreachable: "bg-warning",
  paused: "bg-foreground-subtle",
}

export function StatusBadge({
  status,
  className,
}: {
  status: UpstreamStatus
  className?: string
}) {
  const t = useT()
  return (
    <span
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1.5 rounded-full border border-transparent px-2 text-xs font-medium whitespace-nowrap",
        tone[status],
        className
      )}
    >
      <span className={cn("size-1.5 rounded-full", dot[status])} />
      {t.status[status] ?? status}
    </span>
  )
}

/** Small semantic chip for roles, task results and the like. */
export function Chip({
  tone: chipTone = "neutral",
  children,
  className,
}: {
  tone?: "neutral" | "success" | "danger" | "warning" | "info"
  children: React.ReactNode
  className?: string
}) {
  const tones = {
    neutral: "bg-secondary text-foreground-muted",
    success: "bg-success-soft text-success border-success-border",
    danger: "bg-destructive-soft text-destructive border-destructive-border",
    warning: "bg-warning-soft text-warning border-warning-border",
    info: "bg-info-soft text-info border-info-border",
  }
  return (
    <span
      className={cn(
        "inline-flex h-5 shrink-0 items-center rounded-sm border border-transparent px-1.5 text-xs font-medium whitespace-nowrap",
        tones[chipTone],
        className
      )}
    >
      {children}
    </span>
  )
}
