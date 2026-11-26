"use client"

import { BookOpen, CircleHelp } from "lucide-react"

import { docUrl, type DocTopic } from "@/lib/docs"
import { useLocale, useT } from "@/lib/i18n"
import { cn } from "@/lib/utils"

/**
 * A link to a section of the guide on GitHub, in the UI's language, opened in a
 * new window. "text" is a small "View guide" link; "icon" a help icon beside a
 * heading.
 */
export function DocLink({
  topic,
  variant = "text",
  className,
}: {
  topic: DocTopic
  variant?: "text" | "icon"
  className?: string
}) {
  const t = useT()
  const href = docUrl(topic, useLocale())
  if (variant === "icon") {
    return (
      <a
        href={href}
        target="_blank"
        rel="noreferrer"
        aria-label={t.common.guideTitle}
        title={t.common.guideTitle}
        className={cn(
          "inline-flex size-5 shrink-0 items-center justify-center rounded-md text-foreground-subtle transition-colors hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 focus-visible:outline-none",
          className
        )}
      >
        <CircleHelp className="size-3.5" />
      </a>
    )
  }
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      title={t.common.guideTitle}
      className={cn(
        "inline-flex w-fit items-center gap-1 text-xs text-link hover:underline",
        className
      )}
    >
      <BookOpen className="size-3.5" />
      {t.common.guide}
    </a>
  )
}
