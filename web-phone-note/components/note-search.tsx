"use client"

import { useEffect, useState } from "react"
import { FileText } from "lucide-react"
import type { FileNode, Session } from "@/lib/jmap"
import { noteTitle, type Library } from "@/lib/library"
import { noteText } from "@/lib/text-cache"

interface Hit { node: FileNode; snippet: string; titleMatch: boolean }

/** Full text needs every note downloaded once; blobs are cached, so later searches are local. */
export function NoteSearch({ session, library, query, onOpen }: { session: Session; library: Library; query: string; onOpen: (node: FileNode) => void }) {
  // Tagged with the library it was loaded for, so a refresh mid-search starts over instead of mixing counts.
  const [loaded, setLoaded] = useState<{ source: Library | null; texts: Map<string, string>; done: number }>({ source: null, texts: new Map(), done: 0 })
  const notes = library.notes()

  useEffect(() => {
    let alive = true
    const queue = library.notes()
    const texts = new Map<string, string>()
    let next = 0
    async function worker() {
      while (alive && next < queue.length) {
        const node = queue[next++]
        try { texts.set(node.id, await noteText(session, node)) } catch { /* Unreadable notes still match by title. */ }
        if (alive) setLoaded((value) => ({ source: library, texts, done: value.source === library ? value.done + 1 : 1 }))
      }
    }
    void Promise.all(Array.from({ length: 6 }, worker))
    return () => { alive = false }
  }, [session, library])

  const done = loaded.source === library ? loaded.done : 0
  const texts = loaded.source === library ? loaded.texts : new Map<string, string>()
  const needle = query.trim().toLowerCase()
  const hits: Hit[] = []
  for (const node of notes) {
    const titleMatch = noteTitle(node).toLowerCase().includes(needle)
    const text = texts.get(node.id) ?? ""
    const at = text.toLowerCase().indexOf(needle)
    if (!titleMatch && at < 0) continue
    const snippet = at < 0 ? text.slice(0, 80) : `${at > 30 ? "…" : ""}${text.slice(Math.max(0, at - 30), at + needle.length + 60)}`
    hits.push({ node, snippet: snippet.replace(/\s+/g, " ").trim(), titleMatch })
  }
  hits.sort((a, b) => Number(b.titleMatch) - Number(a.titleMatch) || b.node.modified - a.node.modified)

  const loading = done < notes.length
  return <div>
    {loading && <p className="text-muted-foreground px-1 pb-3 text-xs">正在载入全文 {done}/{notes.length}，已载入的部分可按正文匹配</p>}
    {!hits.length && !loading && <p className="text-muted-foreground py-10 text-center text-sm">没有找到「{query.trim()}」</p>}
    <ul className="divide-border divide-y">
      {hits.map(({ node, snippet }) => <li key={node.id}>
        <button type="button" onClick={() => onOpen(node)} className="active:bg-muted flex w-full gap-3 px-1 py-3 text-left">
          <FileText className="text-muted-foreground mt-0.5 size-4 shrink-0" />
          <span className="min-w-0 flex-1">
            <span className="block truncate font-medium">{noteTitle(node)}</span>
            <span className="text-muted-foreground block truncate text-xs">{library.path(node.parentId) || library.root.name}</span>
            {snippet && <span className="text-muted-foreground mt-1 line-clamp-2 block text-sm">{snippet}</span>}
          </span>
        </button>
      </li>)}
    </ul>
  </div>
}
