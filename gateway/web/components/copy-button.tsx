"use client"

import * as React from "react"
import { Check, Copy } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { useT } from "@/lib/i18n"

async function writeClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // Plain http (not localhost) has no clipboard API.
    const ta = document.createElement("textarea")
    ta.value = text
    ta.style.position = "fixed"
    ta.style.opacity = "0"
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand("copy")
    ta.remove()
    return ok
  }
}

export function CopyButton({
  value,
  label,
  size = "icon-sm",
  variant = "ghost",
}: {
  value: string
  label?: string
  size?: "icon-sm" | "sm" | "default"
  variant?: "ghost" | "outline" | "default"
}) {
  const t = useT()
  const [done, setDone] = React.useState(false)
  const onClick = async () => {
    if (await writeClipboard(value)) {
      setDone(true)
      toast.success(t.common.copied)
      setTimeout(() => setDone(false), 1500)
    }
  }
  const Icon = done ? Check : Copy
  return (
    <Button
      type="button"
      variant={variant}
      size={label ? (size === "icon-sm" ? "sm" : size) : size}
      onClick={onClick}
      aria-label={label ?? t.common.copy}
    >
      <Icon />
      {label}
    </Button>
  )
}

/** A monospace value with a copy button, for redirect URIs, scopes and passwords. */
export function CopyField({ value, label }: { value: string; label?: string }) {
  return (
    <div className="flex items-center gap-1 rounded-md border bg-muted py-1 pr-1 pl-3">
      <code className="min-w-0 flex-1 truncate font-mono text-sm" title={value}>
        {value}
      </code>
      <CopyButton value={value} />
      {label && <span className="sr-only">{label}</span>}
    </div>
  )
}
