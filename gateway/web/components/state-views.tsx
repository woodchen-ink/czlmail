import * as React from "react"
import { CircleAlert, Construction, type LucideIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { isNotReady } from "@/lib/api/client"
import { errorText } from "@/lib/errors"
import { useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export function EmptyState({
  icon: Icon,
  title,
  hint,
  action,
  className,
}: {
  icon: LucideIcon
  title: string
  hint?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-2 px-6 py-12 text-center",
        className
      )}
    >
      <div className="mb-1 flex size-10 items-center justify-center rounded-full bg-muted text-foreground-muted">
        <Icon className="size-5" />
      </div>
      <p className="text-sm font-medium">{title}</p>
      {hint && <p className="max-w-sm text-sm text-foreground-muted">{hint}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

/** "This feature is not ready yet": shown where an API answers 404 because its work package has not landed. */
export function NotReady({ className }: { className?: string }) {
  const t = useT()
  return (
    <EmptyState
      icon={Construction}
      title={t.common.notReadyTitle}
      hint={t.common.notReadyBody}
      className={className}
    />
  )
}

/** Load error with retry; turns a missing route into NotReady. */
export function ErrorState({
  error,
  onRetry,
  className,
}: {
  error: unknown
  onRetry?: () => void
  className?: string
}) {
  const t = useT()
  if (isNotReady(error)) return <NotReady className={className} />
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-2 px-6 py-12 text-center",
        className
      )}
    >
      <CircleAlert className="size-6 text-destructive" />
      <p className="text-sm font-medium">{t.common.loadFailed}</p>
      <p className="text-sm text-foreground-muted">{errorText(error)}</p>
      {onRetry && (
        <Button variant="outline" size="sm" className="mt-2" onClick={onRetry}>
          {t.common.retry}
        </Button>
      )}
    </div>
  )
}
