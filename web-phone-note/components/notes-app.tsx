"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import { ArrowLeft, ChevronRight, FileText, Folder, LogOut, Paperclip, RefreshCw, Search, X } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { LoginForm } from "@/components/login-form"
import { NoteView } from "@/components/note-view"
import { NoteSearch } from "@/components/note-search"
import { connect, download, JmapError, listFileNodes, type FileNode, type Session } from "@/lib/jmap"
import { imageType, isNote, Library, noteTitle, vaultCandidates } from "@/lib/library"
import { errorText, forgetCredentials, loadCredentials, savedVault, saveVault } from "@/lib/credentials"
import { clearTextCache } from "@/lib/text-cache"

/** Browsing position lives in the hash so the phone's back gesture walks back through folders and notes. */
type Place = { kind: "dir" | "note"; id: string }

function readPlace(): Place | null {
  const match = /^#([dn])=(.+)$/.exec(window.location.hash)
  return match ? { kind: match[1] === "n" ? "note" : "dir", id: decodeURIComponent(match[2]) } : null
}

const placeHash = (place: Place) => `#${place.kind === "note" ? "n" : "d"}=${encodeURIComponent(place.id)}`

/** Reload the tree when the page comes back to the foreground after this long. */
const STALE_MS = 60_000

export function NotesApp() {
  const [boot, setBoot] = useState(true)
  const [session, setSession] = useState<Session | null>(null)
  const [nodes, setNodes] = useState<FileNode[] | null>(null)
  const [loadedAt, setLoadedAt] = useState(0)
  const [loadError, setLoadError] = useState("")
  const [query, setQuery] = useState("")
  const [searching, setSearching] = useState(false)
  const [image, setImage] = useState<string | null>(null)

  const load = useCallback(async (current: Session) => {
    try {
      setNodes(await listFileNodes(current))
      setLoadedAt(Date.now())
      setLoadError("")
    } catch (error) {
      setLoadError(errorText(error))
    }
  }, [])

  // Credentials and the hash exist only in the browser, so the static HTML is just the loading state.
  const [place, setPlace] = useState<Place | null>(() => (typeof window === "undefined" ? null : readPlace()))
  const [vaultId, setVaultId] = useState(() => (typeof window === "undefined" ? "" : savedVault()))
  useEffect(() => {
    void Promise.resolve(loadCredentials()).then(async (credentials) => {
      if (!credentials) return
      try {
        const value = await connect(credentials)
        setSession(value)
        await load(value)
      } catch (error) {
        if (error instanceof JmapError && error.code === 3001) forgetCredentials()
        toast.error(errorText(error))
      }
    }).finally(() => setBoot(false))
  }, [load])

  useEffect(() => {
    const onHash = () => { setPlace(readPlace()); setImage(null) }
    window.addEventListener("hashchange", onHash)
    return () => window.removeEventListener("hashchange", onHash)
  }, [])

  useEffect(() => {
    if (!session) return
    const onVisible = () => { if (document.visibilityState === "visible" && Date.now() - loadedAt > STALE_MS) void load(session) }
    document.addEventListener("visibilitychange", onVisible)
    return () => document.removeEventListener("visibilitychange", onVisible)
  }, [session, loadedAt, load])

  const vaults = useMemo(() => (nodes ? vaultCandidates(nodes) : []), [nodes])
  const vault = vaults.find((node) => node.id === vaultId) ?? vaults[0]
  const library = useMemo(() => (nodes && vault ? new Library(nodes, vault.id) : null), [nodes, vault])

  const go = (next: Place | null) => { window.location.hash = next ? placeHash(next) : "" }

  function chooseVault(id: string) {
    setVaultId(id)
    saveVault(id)
    window.history.replaceState(null, "", window.location.pathname)
    setPlace(null)
  }

  async function logout() {
    forgetCredentials()
    await clearTextCache()
    window.history.replaceState(null, "", window.location.pathname)
    setSession(null); setNodes(null); setPlace(null); setQuery(""); setSearching(false)
  }

  async function openAttachment(node: FileNode) {
    try {
      const blob = await download(session!, node)
      const type = imageType(node)
      if (type) { setImage(URL.createObjectURL(new Blob([blob], { type }))); return }
      // Other attachments are saved, never navigated to: a blob: URL runs with this page's origin.
      const url = URL.createObjectURL(blob)
      const link = Object.assign(document.createElement("a"), { href: url, download: node.name })
      link.click()
      setTimeout(() => URL.revokeObjectURL(url), 30_000)
    } catch (error) {
      toast.error(errorText(error))
    }
  }

  function open(node: FileNode) {
    setSearching(false)
    if (node.directory) go({ kind: "dir", id: node.id })
    else if (isNote(node)) go({ kind: "note", id: node.id })
    else void openAttachment(node)
  }

  function followLink(from: FileNode, href: string) {
    if (/^(https?|mailto):/i.test(href)) { window.open(href, "_blank", "noopener,noreferrer"); return }
    const target = library?.resolve(from.id, href)
    if (target) open(target)
    else toast.error("找不到链接指向的笔记或附件")
  }

  if (boot) return <div className="mx-auto max-w-2xl space-y-3 p-4"><Skeleton className="h-10 w-full" /><Skeleton className="h-6 w-2/3" /><Skeleton className="h-6 w-1/2" /></div>
  if (!session) return <LoginForm onSession={(value) => { setSession(value); void load(value) }} />

  const current = place && library?.get(place.id)
  const note = current && place?.kind === "note" && isNote(current) ? current : null
  const directory = current && place?.kind === "dir" && current.directory ? current : library?.root
  const trail = library && directory ? library.trail(directory.id) : []
  const parentPlace = (node: FileNode): Place | null => node.parentId && node.parentId !== library?.root.id ? { kind: "dir", id: node.parentId } : null

  return <div className="mx-auto flex min-h-svh w-full max-w-3xl flex-col">
    <header className="bg-background/95 border-border sticky top-0 z-10 flex items-center gap-2 border-b px-4 py-2 backdrop-blur pt-[max(0.5rem,env(safe-area-inset-top))]">
      {searching ? <>
        <Search className="text-muted-foreground size-4 shrink-0" />
        <Input autoFocus value={query} onChange={(e) => setQuery(e.target.value)} placeholder="搜索标题与正文" className="h-9 flex-1 border-0 px-1 shadow-none focus-visible:ring-0" />
        <Button variant="ghost" size="icon" aria-label="关闭搜索" onClick={() => { setSearching(false); setQuery("") }}><X /></Button>
      </> : <>
        {(note || trail.length > 0) && <Button variant="ghost" size="icon" aria-label="返回" onClick={() => window.history.length > 1 ? window.history.back() : go(note ? parentPlace(note) : directory ? parentPlace(directory) : null)}><ArrowLeft /></Button>}
        <div className="min-w-0 flex-1">
          {vaults.length > 1 && !note && !trail.length
            ? <select aria-label="笔记库" value={vault?.id} onChange={(e) => chooseVault(e.target.value)} className="bg-transparent max-w-full truncate text-base font-semibold outline-none">{vaults.map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}</select>
            : <h1 className="truncate text-base font-semibold">{note ? noteTitle(note) : directory?.name ?? "笔记"}</h1>}
        </div>
        <Button variant="ghost" size="icon" aria-label="搜索" disabled={!library} onClick={() => setSearching(true)}><Search /></Button>
        <Button variant="ghost" size="icon" aria-label="刷新" onClick={() => void load(session).then(() => toast.success("已刷新"))}><RefreshCw /></Button>
        <Button variant="ghost" size="icon" aria-label="退出登录" onClick={() => void logout()}><LogOut /></Button>
      </>}
    </header>

    <main className="flex-1 px-4 pt-4 pb-[max(2.5rem,env(safe-area-inset-bottom))]">
      {loadError && <p className="text-destructive mb-4 text-sm">{loadError}</p>}
      {!nodes && !loadError && <div className="space-y-3"><Skeleton className="h-6 w-full" /><Skeleton className="h-6 w-5/6" /><Skeleton className="h-6 w-4/6" /></div>}
      {nodes && !library && <p className="text-muted-foreground py-10 text-center text-sm">网盘里还没有笔记库。先在 CZL Mail 桌面端打开「笔记」模块，它会在网盘根目录建好 Notes。</p>}
      {library && (searching && query.trim()
        ? <NoteSearch session={session} library={library} query={query} onOpen={open} />
        : searching ? <p className="text-muted-foreground py-10 text-center text-sm">输入关键字搜索 {library.notes().length} 篇笔记</p>
        : note ? <>
          {trail.length > 0 || note.parentId !== library.root.id ? <p className="text-muted-foreground mb-2 truncate text-xs">{library.path(note.parentId) || library.root.name}</p> : null}
          <NoteView session={session} library={library} note={note} onLink={(href) => followLink(note, href)} onImage={setImage} />
        </>
        : directory && <DirectoryList library={library} directory={directory} trail={trail} onOpen={open} onCrumb={(id) => go(id ? { kind: "dir", id } : null)} />)}
    </main>

    {image && <button type="button" aria-label="关闭图片" onClick={() => setImage(null)} className="fixed inset-0 z-20 flex items-center justify-center bg-black/90 p-4">
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={image} alt="" className="max-h-full max-w-full object-contain" />
    </button>}
  </div>
}

function DirectoryList({ library, directory, trail, onOpen, onCrumb }: {
  library: Library; directory: FileNode; trail: FileNode[]; onOpen: (node: FileNode) => void; onCrumb: (id: string | null) => void
}) {
  const items = library.list(directory.id)
  return <>
    {trail.length > 0 && <nav className="text-muted-foreground mb-3 flex flex-wrap items-center gap-1 text-xs">
      <button type="button" onClick={() => onCrumb(null)} className="underline-offset-2 hover:underline">{library.root.name}</button>
      {trail.map((node, index) => <span key={node.id} className="flex items-center gap-1"><ChevronRight className="size-3" />
        {index === trail.length - 1 ? <span className="text-foreground">{node.name}</span> : <button type="button" onClick={() => onCrumb(node.id)} className="underline-offset-2 hover:underline">{node.name}</button>}
      </span>)}
    </nav>}
    {!items.length && <p className="text-muted-foreground py-10 text-center text-sm">这个文件夹是空的</p>}
    <ul className="divide-border divide-y">
      {items.map((node) => {
        const Icon = node.directory ? Folder : isNote(node) ? FileText : Paperclip
        return <li key={node.id}>
          <button type="button" onClick={() => onOpen(node)} className="active:bg-muted flex w-full items-center gap-3 px-1 py-3 text-left">
            <Icon className="text-muted-foreground size-4 shrink-0" />
            <span className={`min-w-0 flex-1 truncate ${isNote(node) || node.directory ? "" : "text-muted-foreground text-sm"}`}>{isNote(node) ? noteTitle(node) : node.name}</span>
            {node.modified > 0 && !node.directory && <span className="text-muted-foreground shrink-0 text-xs">{new Date(node.modified).toLocaleDateString("zh-CN")}</span>}
            {node.directory && <ChevronRight className="text-muted-foreground size-4 shrink-0" />}
          </button>
        </li>
      })}
    </ul>
  </>
}
