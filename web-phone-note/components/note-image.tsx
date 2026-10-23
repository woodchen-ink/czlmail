"use client"

import { useEffect, useState } from "react"
import { download, type FileNode, type Session } from "@/lib/jmap"
import { imageType, type Library } from "@/lib/library"

/**
 * Vault images need the Authorization header, so they load through fetch and render from an object URL.
 * The blob is retyped from the FileNode type and only image types are shown: an `<img>` never runs scripts.
 */
export function NoteImage({ session, library, note, src, alt, onOpen }: {
  session: Session; library: Library; note: FileNode; src: string; alt: string; onOpen: (url: string) => void
}) {
  const remote = /^https?:\/\//i.test(src)
  const resolved = remote ? null : library.resolve(note.id, src)
  const type = resolved && imageType(resolved)
  const target = type ? resolved : null
  const [url, setUrl] = useState<string | null>(remote ? src : null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (!target) return
    let objectUrl = ""
    let alive = true
    download(session, target)
      .then((blob) => { if (!alive) return; objectUrl = URL.createObjectURL(new Blob([blob], { type: type! })); setUrl(objectUrl) })
      .catch(() => alive && setFailed(true))
    return () => { alive = false; if (objectUrl) URL.revokeObjectURL(objectUrl) }
  }, [session, target, type])

  if (failed || (!remote && !target)) return <span className="bg-muted text-muted-foreground my-2 block rounded-md px-4 py-3 text-sm">图片加载失败 · {alt || src}</span>
  if (!url) return <span className="bg-muted my-3 block h-40 animate-pulse rounded-md" />
  // eslint-disable-next-line @next/next/no-img-element
  return <img src={url} alt={alt} referrerPolicy="no-referrer" loading="lazy" onClick={() => onOpen(url)} onError={() => setFailed(true)} className="my-3 max-h-[70vh] max-w-full cursor-zoom-in rounded-md object-contain" />
}
