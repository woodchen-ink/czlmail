"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { CalendarDays, FilePlus2, FolderPlus, RefreshCw, Pencil, Trash2, FolderOpen, NotebookPen } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useI18n } from "@/components/locale-provider";
import { usePanelRef } from "react-resizable-panels";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { NoteDocumentTools } from "@/components/notes/note-document-tools";
import { NoteEditor } from "@/components/notes/note-editor";
import { NoteHeader, type NoteMode } from "@/components/notes/note-header";
import { NotePreview } from "@/components/notes/note-preview";
import { NoteSearchResults } from "@/components/notes/note-search-results";
import { NoteTree } from "@/components/notes/note-tree";
import { NoteAttachmentPane, type NoteAttachmentPreview } from "@/components/notes/note-attachment-pane";
import { NoteDiffDialog } from "@/components/notes/note-diff-dialog";
import { api, errorMessage, onEvent } from "@/lib/api";
import { onFileDrop } from "@/lib/file-drop";
import { noteApi, noteEvents, fileBase64, localDay, takePendingNote, type NoteEntry, type NoteVault, type NoteDiff } from "@/lib/notes/api";
import { listen } from "@/lib/app-bus";
import { toggleTask } from "@/lib/notes/live-editing";
import { useNoteDocument } from "@/lib/notes/use-note-document";
import { useNoteSync } from "@/lib/notes/use-note-sync";
import { BrowserOpenURL } from "@/wailsjs/runtime/runtime";
import { getLocale, t } from "@/lib/i18n";

type DialogAction = "folder" | "rename" | "trash";

/** Last opened note per library, restored when the module opens. */
function lastNoteKey(vaultId: string) {
  return `czlmail.notes.last.${vaultId}`;
}

/** A bounded notes workspace: native Markdown, lazy folders and one durable editor. */
export function NotesShell({ active }: { active: boolean }) {
  useI18n();
  const doc = useNoteDocument();
  const { syncing, sync } = useNoteSync(doc.flush);
  const [vaults, setVaults] = useState<NoteVault[]>([]);
  const [vaultId, setVaultId] = useState("");
  const [parent, setParent] = useState("");
  const [selected, setSelected] = useState<NoteEntry | null>(null);
  const [tick, setTick] = useState(0);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<NoteEntry[]>([]);
  const [trash, setTrash] = useState(false);
  const [mode, setMode] = useState<NoteMode>("live");
  const [busy, setBusy] = useState(false);
  const [action, setAction] = useState<DialogAction | null>(null);
  const [name, setName] = useState("");
  const [created, setCreated] = useState("");
  const [attachmentPreview, setAttachmentPreview] = useState<NoteAttachmentPreview | null>(null);
  const [diff, setDiff] = useState<NoteDiff | null>(null);
  const insert = useRef<((text: string) => void) | null>(null);
  const picker = useRef<HTMLInputElement>(null);
  const treePanel = usePanelRef();
  useEffect(() => {
    try {
      const value = Number(localStorage.getItem("czlmail.notes.treeWidth"));
      if (Number.isFinite(value) && value >= 240 && value <= 560) treePanel.current?.resize(value);
    } catch { /* Ignore unavailable preferences. */ }
  }, [treePanel]);
  const vault = vaults.find((item) => item.id === vaultId);

  const actionLock = useRef(false);
  /** One action lock prevents switching selection mid-attachment or resolution. */
  const run = useCallback(async (work: () => Promise<void>) => {
    if (actionLock.current) return;
    actionLock.current = true; setBusy(true);
    try { await work(); } catch (err) { toast.error(errorMessage(err)); }
    finally { actionLock.current = false; setBusy(false); }
  }, []);

  /** Local events contain no document text; each view reads the newest revision when safe. */
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    const load = () => noteApi.vaults().then((items) => {
      if (!live) return;
      setVaults(items ?? []); setVaultId((id) => id || items?.[0]?.id || ""); setTick((v) => v + 1);
    }).catch((err) => { if (live) toast.error(errorMessage(err)); });
    void load();
    const off = onEvent(noteEvents.changed, () => { clearTimeout(timer); timer = setTimeout(() => void load(), 300); });
    return () => { live = false; clearTimeout(timer); off(); };
  }, []);

  useEffect(() => {
    if (!vaultId) return;
    let live = true;
    const timer = setTimeout(() => {
      if (query || trash) void noteApi.list(vaultId, "", query, trash).then((items) => { if (live) setResults(items ?? []); }).catch((err) => toast.error(errorMessage(err)));
    }, 180);
    return () => { live = false; clearTimeout(timer); };
  }, [vaultId, query, trash, tick]);

  const restored = useRef("");
  /** Notes opened from other modules (calendar markers); a request made before mounting is picked up here. */
  useEffect(() => {
    const show = (ref: { vaultId: string; id: string } | null) => {
      if (!ref) return;
      takePendingNote();
      restored.current = ref.vaultId;
      void run(async () => {
        const entry = await noteApi.read(ref.vaultId, ref.id);
        setVaultId(entry.vaultId); setQuery(""); setTrash(false);
        await open(entry);
      });
    };
    show(takePendingNote());
    return listen("openNote", show);
  // eslint-disable-next-line react-hooks/exhaustive-deps -- open is recreated every render; run is stable.
  }, [run]);

  /** Reopen the note that was open last time; a deleted or missing note is simply forgotten. */
  useEffect(() => {
    if (!vaultId || restored.current === vaultId || doc.getNote()) return;
    restored.current = vaultId;
    let id = "";
    try { id = localStorage.getItem(lastNoteKey(vaultId)) ?? ""; } catch { /* Ignore unavailable preferences. */ }
    if (!id) return;
    void noteApi.read(vaultId, id).then((entry) => {
      if (entry.deleted || entry.directory || doc.getNote()) return;
      return run(() => open(entry));
    }).catch(() => { try { localStorage.removeItem(lastNoteKey(vaultId)); } catch { /* Ignore unavailable preferences. */ } });
  // eslint-disable-next-line react-hooks/exhaustive-deps -- Runs once per library; open is recreated every render.
  }, [vaultId]);

  /** Folder dialogs: root entries never inherit an unrelated selected folder. */
  function folderAt(parentId: string) {
    setParent(parentId); setName(""); setAction("folder");
  }

  /** A new note is created and opened at once as "Untitled"; its title is focused for naming. */
  async function createNote(parentId: string) {
    await run(async () => {
      await doc.flush();
      const entry = await noteApi.create({ vaultId, parentId, name: t("未命名"), unique: true, idempotencyKey: crypto.randomUUID() });
      await open(entry);
      if (mode === "read") setMode("live");
      setCreated(entry.id); setTick((value) => value + 1);
    });
  }

  /** Today's diary opens in one click; the backend creates folders and fills the template once. */
  async function openJournal() {
    await run(async () => {
      await doc.flush();
      const entry = await noteApi.openJournal(vaultId, localDay(), getLocale());
      await open(entry);
      if (mode === "read") setMode("live");
      setTick((value) => value + 1);
    });
  }

  /** Moves flush the editor and compare against the latest backend revision. */
  async function move(entry: NoteEntry, parentId: string) {
    await run(async () => {
      await doc.flush();
      const latest = await noteApi.read(entry.vaultId, entry.id);
      const saved = await noteApi.move({ vaultId: entry.vaultId, id: latest.id, expectedRevision: latest.revision, parentId, name: latest.name });
      if (selected?.id === saved.id) setSelected(saved);
      await doc.refresh(); setTick((value) => value + 1);
    });
  }

  /** Renames keep the real extension; the title field shows note names without ".md". */
  async function rename(entry: NoteEntry, value: string) {
    await run(async () => {
      await doc.flush();
      const latest = await noteApi.read(entry.vaultId, entry.id);
      const next = latest.directory || latest.attachment || /\.md$/i.test(value) ? value : `${value}.md`;
      const saved = await noteApi.move({ vaultId: entry.vaultId, id: latest.id, expectedRevision: latest.revision, parentId: latest.parentId, name: next });
      if (selected?.id === saved.id) setSelected(saved);
      await doc.refresh(); setTick((v) => v + 1);
    });
  }

  /** Open saves first, then remembers the last note per library. */
  async function open(entry: NoteEntry) {
    if (entry.attachment) {
      await doc.flush();
      const result = await noteApi.previewLink(entry.vaultId, entry.id, encodeURIComponent(entry.name));
      if (result.previewType.startsWith("image/") || result.previewType === "application/pdf") {
        setSelected(entry);
        setAttachmentPreview({ vaultId: entry.vaultId, noteId: entry.id, path: encodeURIComponent(entry.name), name: entry.name, type: result.previewType });
      } else await noteApi.openLink(entry.vaultId, entry.id, encodeURIComponent(entry.name));
      return;
    }
    await doc.open(entry.vaultId, entry.id); setAttachmentPreview(null); setSelected(entry); setParent(entry.parentId); setCreated("");
    try { localStorage.setItem(lastNoteKey(entry.vaultId), entry.id); } catch { /* Ignore unavailable preferences. */ }
  }

  /** A saved document revision is used for each attachment; insertion occurs only after persistence. */
  const attachFiles = async (files: File[], at: (text: string) => void) => {
    await run(async () => {
      await doc.flush(); const note = doc.getNote(); if (!note || note.deleted) return;
      const fragments: string[] = [];
      for (const file of files) {
        if (file.size > 25 * 1024 * 1024) throw new Error(t("附件不能超过 25 MiB"));
        const item = await noteApi.attach(note.vaultId, note.id, note.revision, file.name || "截图.png", await fileBase64(file));
        fragments.push(item.markdown);
      }
      if (doc.getNote()?.id === note.id) { at(`\n${fragments.join("\n")}\n`); }
    });
  };

  useEffect(() => {
    if (!active || attachmentPreview || !doc.note || doc.note.deleted) return;
    return onFileDrop((paths) => {
      void run(async () => {
        await doc.flush(); const note = doc.getNote(); if (!note) return;
        const items = await noteApi.attachPaths(note.vaultId, note.id, note.revision, paths);
        if (doc.getNote()?.id === note.id) insert.current?.(`\n${items.map((item) => item.markdown).join("\n")}\n`);
      });
      return true;
    });
  }, [active, attachmentPreview, doc, run]);

  useEffect(() => {
    if (!active || !vaultId) return;
    const sync = () => { void noteApi.sync().catch(() => {}); };
    window.addEventListener("online", sync); window.addEventListener("focus", sync);
    return () => { window.removeEventListener("online", sync); window.removeEventListener("focus", sync); };
  }, [active, vaultId]);

  /** Dialog actions share the same version-checked backend as MCP. */
  async function submit() {
    await run(async () => {
      await doc.flush();
      if (action === "folder") {
        await noteApi.create({ vaultId, parentId: parent, name, idempotencyKey: crypto.randomUUID() }, true);
      } else if (selected) {
        const latest = await noteApi.read(vaultId, selected.id);
        if (action === "rename") await noteApi.move({ vaultId, id: latest.id, expectedRevision: latest.revision, parentId: latest.parentId, name });
        if (action === "trash") await noteApi.trash(vaultId, latest.id, latest.revision, false);
        await doc.refresh();
      }
      setAction(null); setTick((v) => v + 1);
    });
  }

  /** Links are either explicit external navigation or vault-scoped local resolution. */
  async function follow(href: string) {
    await run(async () => {
      if (/^https?:\/\//i.test(href)) { BrowserOpenURL(href); return; }
      const note = doc.getNote(); if (!note) return;
      const result = await noteApi.previewLink(note.vaultId, note.id, href);
      const entry = result.entry;
      if (!entry.directory && !entry.attachment) await open(entry);
      else if (result.previewType.startsWith("image/") || result.previewType === "application/pdf") setAttachmentPreview({ vaultId: note.vaultId, noteId: note.id, path: href, name: entry.name, type: result.previewType });
      else if (entry.attachment) await noteApi.openLink(note.vaultId, note.id, href);
    });
  }

  /** A single delegated image click works in both CodeMirror widgets and the reading view. */
  async function previewImage(image: HTMLImageElement) {
    await run(async () => {
      await doc.flush(); const note = doc.getNote(); if (!note) return;
      const path = image.dataset.notePath;
      if (path) {
        const result = await noteApi.previewLink(note.vaultId, note.id, path);
        if (result.previewType.startsWith("image/")) setAttachmentPreview({ vaultId: note.vaultId, noteId: note.id, path, name: result.entry.name, type: result.previewType });
      } else if (/^https?:\/\//i.test(image.dataset.noteRemote ?? "")) {
        setAttachmentPreview({ vaultId: note.vaultId, noteId: note.id, path: "", name: image.alt || t("附件预览"), type: "image/remote", src: image.dataset.noteRemote });
      }
    });
  }

  /** Return from a preview without re-reading or replacing the saved editor buffer. */
  function closePreview() {
    setAttachmentPreview(null); if (doc.note) setSelected(doc.note);
  }

  /** Reading mode toggles tasks through the same buffer and autosave as typing. */
  function toggleReadingTask(from: number) {
    const change = toggleTask(doc.text, from);
    if (change && doc.note && !doc.note.deleted) doc.edit(doc.text.slice(0, change.from) + change.insert + doc.text.slice(change.to));
  }

  const compare = () => void run(async () => { await doc.flush(); const n = doc.getNote(); if (n) setDiff(await noteApi.diff(n.vaultId, n.id)); });
  const openDocument = (entry: NoteEntry) => { if (!busy && !entry.directory) void run(() => open(entry)); };

  const tree = <NoteTree vaultId={vaultId} tick={tick} selectedId={selected?.id ?? ""} disabled={busy}
    onRootAction={(kind) => { if (kind === "note") void createNote(""); else folderAt(""); }}
    onMove={(entry, parentId) => void move(entry, parentId)}
    onOpen={openDocument}
    onFolder={(entry) => { setSelected(entry); setParent(entry.id); }}
    onAction={(entry, kind) => {
      if (busy) return;
      if (kind === "note") { void createNote(entry.id); return; }
      setSelected(entry); setParent(entry.directory ? entry.id : entry.parentId); setName(kind === "rename" ? entry.name : ""); setAction(kind);
    }} />;

  const sidebar = <aside className="bg-sidebar flex h-full min-w-0 flex-col">
    <div className="flex flex-col gap-2 p-3">
      <div className="flex items-center gap-1">
        <NotebookPen className="size-4" /><h1 className="ml-1 font-semibold">{t("笔记")}</h1><span className="flex-1" />
        {iconButton(t("今日日记"), !vaultId || busy || trash, () => void openJournal(), <CalendarDays />)}
        {iconButton(t("新建笔记"), !vaultId || busy || trash, () => void createNote(""), <FilePlus2 />)}
        {iconButton(t("新建文件夹"), !vaultId || busy || trash, () => folderAt(""), <FolderPlus />)}
        {iconButton(t("重命名"), !selected || busy || trash, () => { setName(selected?.name ?? ""); setAction("rename"); }, <Pencil />)}
        {iconButton(t("立即同步"), busy || syncing || !vaultId, () => void sync(), <RefreshCw className={syncing ? "animate-spin" : undefined} />)}
      </div>
      <Input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("搜索笔记正文和标题")} aria-label={t("搜索笔记正文和标题")} />
    </div>
    <div className="czl-scroll min-h-0 flex-1 overflow-y-auto px-2">
      {vaultId && !query && !trash && tree}
      {(query || trash) && <NoteSearchResults entries={results} query={query} trash={trash} selectedId={selected?.id ?? ""} disabled={busy} onOpen={openDocument}
        onRestore={(entry) => void run(async () => { await noteApi.trash(vaultId, entry.id, entry.revision, true); setTick((v) => v + 1); })} />}
    </div>
    <div className="border-border flex items-center gap-2 border-t p-2">
      <Button variant={trash ? "secondary" : "ghost"} size="sm" onClick={() => setTrash((value) => !value)}>{trash ? <FolderOpen /> : <Trash2 />}{trash ? t("回到笔记区") : t("回收站")}</Button>
    </div>
  </aside>;

  const body = doc.note
    ? mode === "read"
      ? <NotePreview vaultId={doc.note.vaultId} noteId={doc.note.id} text={doc.text} onLink={(href) => void follow(href)} onTask={doc.note.deleted ? undefined : toggleReadingTask} />
      : <NoteEditor vaultId={doc.note.vaultId} noteId={doc.note.id} value={doc.text} source={mode === "source"} readOnly={doc.note.deleted} onChange={doc.edit} onFiles={(files, at) => void attachFiles(files, at)} onReady={(at) => { insert.current = at; }} />
    : <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-3 text-sm">
      <p>{t("选择或新建一篇笔记")}</p>
      <Button variant="outline" size="sm" disabled={busy || trash} onClick={() => void createNote("")}><FilePlus2 />{t("新建笔记")}</Button>
    </div>;

  return <div className="flex h-full min-h-0" style={{ "--wails-drop-target": "drop" } as React.CSSProperties}>
    <ResizablePanelGroup orientation="horizontal">
    <ResizablePanel id="notes-tree" panelRef={treePanel} defaultSize={320} minSize={240} maxSize={560} onResize={(size, _id, previous) => { if (!previous) return; try { localStorage.setItem("czlmail.notes.treeWidth", String(size.inPixels)); } catch { /* Ignore unavailable preferences. */ } }}>
      {sidebar}
    </ResizablePanel>
    <ResizableHandle aria-label={t("调整目录宽度")} />
    <ResizablePanel id="notes-body" minSize={400}>
    <section className="flex h-full min-w-0 flex-col">
      {!vaultId ? <div className="m-auto flex max-w-md flex-col gap-4 p-8"><h2 className="text-xl font-semibold">{t("笔记")}</h2><p className="text-muted-foreground text-sm">{t("笔记保存在个人账号的 Notes 目录中。")}</p><Button disabled={busy} onClick={() => void run(async () => { const accounts = await api.listAccounts(); const account = accounts.find((item) => item.isPersonal); if (!account) return; const v = await noteApi.bind(account.id, ""); setVaults([v]); setVaultId(v.id); void sync(); })}>{t("开始使用笔记")}</Button></div> : <>
        {attachmentPreview
          ? <header className="border-border flex min-h-12 items-center gap-3 border-b px-4"><span className="min-w-0 flex-1 truncate text-sm">{attachmentPreview.name}</span><Button variant="ghost" size="sm" onClick={closePreview}>{doc.note ? t("返回笔记") : t("关闭")}</Button></header>
          : doc.note && <NoteHeader note={doc.note} dirty={doc.dirty} mode={mode} busy={busy} focusTitle={created === doc.note.id} onMode={setMode}
            onRename={(value) => { const note = doc.getNote(); if (note) void rename(note, value); }}
            onAttach={() => picker.current?.click()} onDiff={compare}
            onTrash={() => { setSelected(doc.note); setAction("trash"); }} />}
        {vault?.error && <p className="text-destructive px-4 py-2 text-sm">{t("同步未完成，本地内容仍保留")}: {vault.error}</p>}
        {doc.error && <div className="text-destructive flex items-center gap-3 px-4 py-2 text-sm"><span>{doc.error}</span><Button variant="outline" size="sm" onClick={() => void run(doc.recover)}>{t("将当前编辑另存为恢复笔记")}</Button></div>}
        {attachmentPreview && <div className="min-h-0 flex-1"><NoteAttachmentPane item={attachmentPreview} onExternal={() => void run(async () => { if (attachmentPreview.src) BrowserOpenURL(attachmentPreview.src); else await noteApi.openLink(attachmentPreview.vaultId, attachmentPreview.noteId, attachmentPreview.path); })} /></div>}
        <div hidden={!!attachmentPreview} onClickCapture={(event) => {
          const target = event.target as HTMLElement;
          const image = target.closest<HTMLImageElement>("img[data-note-path], img[data-note-remote]");
          if (image) { event.preventDefault(); event.stopPropagation(); if (!busy) void previewImage(image); return; }
          const link = target.closest<HTMLAnchorElement>("a[data-note-link]");
          if (link) { event.preventDefault(); event.stopPropagation(); void follow(link.dataset.noteLink ?? ""); }
        }} className={attachmentPreview ? "hidden" : "min-h-0 flex-1"}><NoteDocumentTools text={doc.text} note={doc.note}>{body}</NoteDocumentTools></div>
      </>}
    </section>
    </ResizablePanel>
    </ResizablePanelGroup>
    <input ref={picker} type="file" multiple className="hidden" onChange={(event) => { const files = Array.from(event.target.files ?? []); event.target.value = ""; if (insert.current) void attachFiles(files, insert.current); }} />
    <Dialog open={!!action} onOpenChange={(open) => { if (!open && !busy) setAction(null); }}><DialogContent><DialogHeader><DialogTitle>{action === "trash" ? t("移入回收站") : action === "rename" ? t("重命名") : t("新建文件夹")}</DialogTitle><DialogDescription>{action === "trash" ? t("保留附件，可以从回收站恢复。") : t("使用普通文件名保存，不会改写正文标题。")}</DialogDescription></DialogHeader>{action !== "trash" && <Input autoFocus value={name} onChange={(event) => setName(event.target.value)} aria-label={t("名称")} onKeyDown={(event) => { if (event.key === "Enter" && name.trim() && !busy) void submit(); }} />}<DialogFooter><Button variant="outline" disabled={busy} onClick={() => setAction(null)}>{t("取消")}</Button><Button disabled={busy || action !== "trash" && !name.trim()} onClick={() => void submit()}>{t("确定")}</Button></DialogFooter></DialogContent></Dialog>
    <NoteDiffDialog diff={diff} busy={busy} onClose={() => setDiff(null)} onResolve={(strategy) => void run(async () => { if (!diff) return; const saved = await noteApi.resolve(diff.local.vaultId, diff.local.id, diff.local.revision, diff.remoteVersion, strategy, ""); setDiff(null); await doc.open(saved.vaultId, saved.id); })} />
  </div>;
}

/** Icon-only toolbar button with a hover/focus label; the span keeps tooltips working while disabled. */
function iconButton(label: string, disabled: boolean, onClick: () => void, icon: React.ReactNode) {
  return <Tooltip><TooltipTrigger asChild><span className="inline-flex"><Button variant="ghost" size="icon-sm" aria-label={label} disabled={disabled} onClick={onClick}>{icon}</Button></span></TooltipTrigger><TooltipContent>{label}</TooltipContent></Tooltip>;
}
