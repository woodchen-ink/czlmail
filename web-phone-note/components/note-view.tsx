"use client"

import { useEffect, useState } from "react"
import { Fragment, jsx, jsxs } from "react/jsx-runtime"
import { toJsxRuntime, type Components } from "hast-util-to-jsx-runtime"
import { parseNote } from "@/lib/notes/parse-note"
import { noteMarkdownClassName } from "@/lib/notes/markdown-style"
import type { FileNode, Session } from "@/lib/jmap"
import type { Library } from "@/lib/library"
import { noteText } from "@/lib/text-cache"
import { errorText } from "@/lib/credentials"
import { NoteImage } from "@/components/note-image"
import { Skeleton } from "@/components/ui/skeleton"

/** Read-only rendering through the desktop parser (`parseNote`): same Markdown, wiki links, sanitizing and typography. */
export function NoteView({ session, library, note, onLink, onImage }: {
  session: Session; library: Library; note: FileNode
  /** Vault-relative or external link clicked in the note. */
  onLink: (href: string) => void
  onImage: (url: string) => void
}) {
  const [state, setState] = useState<{ blobId: string; text?: string; error?: string }>({ blobId: "" })

  useEffect(() => {
    let alive = true
    noteText(session, note)
      .then((text) => alive && setState({ blobId: note.blobId, text }))
      .catch((error) => alive && setState({ blobId: note.blobId, error: errorText(error) }))
    return () => { alive = false }
  }, [session, note])

  if (state.blobId !== note.blobId) return <div className="space-y-3 py-2"><Skeleton className="h-8 w-2/3" /><Skeleton className="h-4 w-full" /><Skeleton className="h-4 w-5/6" /><Skeleton className="h-4 w-4/6" /></div>
  if (state.error !== undefined) return <p className="text-destructive py-6 text-sm">{state.error}</p>

  const parsed = parseNote(state.text ?? "")
  const components: Partial<Components> = {
    a: ({ href, children, id, className, "aria-label": label }) => <a href={href} id={id} className={className} aria-label={label} onClick={(event) => {
      const target = href ?? ""
      event.preventDefault()
      if (!target.startsWith("#")) { onLink(target); return }
      const anchor = target.slice(1)
      try { (document.getElementById(anchor) ?? document.getElementById(decodeURIComponent(anchor)))?.scrollIntoView({ block: "start", behavior: "smooth" }) } catch { /* Malformed fragment. */ }
    }}>{children}</a>,
    img: ({ src, alt }) => typeof src === "string" ? <NoteImage key={src} session={session} library={library} note={note} src={src} alt={alt ?? ""} onOpen={onImage} /> : null,
    input: ({ type, checked }) => type === "checkbox" ? <input type="checkbox" checked={!!checked} disabled readOnly className="size-4 accent-current" /> : null,
    table: ({ children }) => <div className="my-3 overflow-x-auto"><table>{children}</table></div>,
    pre: ({ children }) => <pre className="!leading-relaxed whitespace-pre">{children}</pre>,
  }
  const { metadata } = parsed

  return <article className={`${noteMarkdownClassName} break-words`}>
    {metadata && <details className="text-muted-foreground mb-6 text-sm"><summary className="cursor-pointer">属性 · {metadata.rows.length}</summary>{metadata.error ? <pre>{metadata.raw}</pre> : <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2">{metadata.rows.map(([key, value]) => <div key={key} className="contents"><dt>{key}</dt><dd className="min-w-0 break-words whitespace-pre-wrap">{value}</dd></div>)}</dl>}</details>}
    {toJsxRuntime(parsed.tree, { Fragment, jsx, jsxs, components, ignoreInvalidStyle: true, passKeys: true, passNode: true })}
  </article>
}
