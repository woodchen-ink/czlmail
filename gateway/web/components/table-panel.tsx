import * as React from "react"

import { Panel } from "@/components/panel"
import { cn } from "@/lib/utils"

/**
 * A list page's table: fills the remaining height of the main area and scrolls inside,
 * so the page header and the panel's own toolbar / footer stay in view.
 */
export function TablePanel({
  toolbar,
  footer,
  children,
  className,
}: {
  toolbar?: React.ReactNode
  footer?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <Panel
      className={cn(
        "flex min-h-64 flex-1 flex-col overflow-hidden md:min-h-0",
        className
      )}
    >
      {toolbar}
      <div className="min-h-0 flex-1 overflow-auto">{children}</div>
      {footer && <div className="shrink-0 border-t px-4 py-2">{footer}</div>}
    </Panel>
  )
}
