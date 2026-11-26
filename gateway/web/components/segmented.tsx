"use client"

import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { cn } from "@/lib/utils"

/** Segmented switch (shadcn Tabs without panels) for ranges and small view modes. */
export function Segmented<T extends string>({
  value,
  onChange,
  options,
  className,
  "aria-label": ariaLabel,
}: {
  value: T
  onChange: (v: T) => void
  options: { value: T; label: string }[]
  className?: string
  "aria-label"?: string
}) {
  return (
    <Tabs
      value={value}
      onValueChange={(v) => onChange(v as T)}
      className={className}
    >
      <TabsList aria-label={ariaLabel} className={cn("bg-background-subtle")}>
        {options.map((o) => (
          <TabsTrigger key={o.value} value={o.value} className="px-2.5 text-xs">
            {o.label}
          </TabsTrigger>
        ))}
      </TabsList>
    </Tabs>
  )
}
