import * as React from "react"
import { cn } from "@/lib/utils"

/** A card surface: white on the grey page, 1px border and the card shadow. */
export function Panel({
  className,
  ...props
}: React.ComponentProps<"section">) {
  return (
    <section
      className={cn(
        "rounded-xl border bg-card text-card-foreground shadow-card",
        className
      )}
      {...props}
    />
  )
}

/** Panel header row: title on the left, actions on the right, divider below. */
export function PanelHeader({
  title,
  description,
  actions,
  className,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex min-h-12 flex-wrap items-center gap-x-3 gap-y-2 border-b px-4 py-2.5",
        className
      )}
    >
      <div className="min-w-32 flex-1">
        <h2 className="truncate text-sm font-semibold">{title}</h2>
        {description && (
          <p className="truncate text-xs text-foreground-muted">
            {description}
          </p>
        )}
      </div>
      {actions && (
        <div className="flex flex-wrap items-center gap-2">{actions}</div>
      )}
    </div>
  )
}
