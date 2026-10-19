"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { FilePlus2, FolderPlus, RefreshCw, Paperclip, GitCompareArrows, Pencil, Trash2, Upload, Download, FolderOpen, NotebookPen } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useI18n } from "@/components/locale-provider";
import { usePanelRef } from "react-resizable-panels";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable";
import { NoteDocumentTools } from "@/components/notes/note-document-tools";
import { NoteEditor } from "@/components/notes/note-editor";
import { NotePreview } from "@/components/notes/note-preview";
import { NoteTree } from "@/components/notes/note-tree";
import { NoteAttachmentDialog, type NoteAttachmentPreview } from "@/components/notes/note-attachment-dialog";
import { NoteDiffDialog } from "@/components/notes/note-diff-dialog";
import { api, errorMessage, onEvent } from "@/lib/api";
import { onFileDrop } from "@/lib/file-drop";
import { noteApi, noteEvents, fileBase64, type NoteEntry, type NoteVault, type NoteDiff } from "@/lib/notes/api";
import { useNoteDocument } from "@/lib/notes/use-note-document";
import { BrowserOpenURL } from "@/wailsjs/runtime/runtime";
import { t } from "@/lib/i18n";

/** A bounded notes workspace: native Markdown, lazy folders and one durable editor. */
export function NotesShell({ active }: { active: boolean }) {
  useI18n();
  const doc = useNoteDocument();
  const [vaults, setVaults] = useState<NoteVault[]>([]);
  const [vaultId, setVaultId] = useState("");
  const [parent, setParent] = useState("");
  const [selected, setSelected] = useState<NoteEntry | null>(null);
  const [tick, setTick] = useState(0);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<NoteEntry[]>([]);
  const [trash, setTrash] = useState(false);
  const [mode, setMode] = useState("live");
  const [busy, setBusy] = useState(false);
  const [action, setAction] = useState<"note" | "folder" | "rename" | "trash" | null>(null);
  const [name, setName] = useState("");
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

  /** One action lock prevents switching selection mid-attachment or resolution. */
  const run = useCallback(async (work: () => Promise<void>) => {
    setBusy(true);
    try { await work(); } catch (err) { toast.error(errorMessage(err)); }
    finally { setBusy(false); }
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

  /** Open saves first, then remembers the last note per library. */
  async function open(entry: NoteEntry) {
    if (entry.attachment) {
      await doc.flush();
      const result = await noteApi.previewLink(entry.vaultId, entry.id, encodeURIComponent(entry.name));
      if (result.previewType.startsWith("image/") || result.previewType === "application/pdf") {
        setAttachmentPreview({ vaultId: entry.vaultId, noteId: entry.id, path: encodeURIComponent(entry.name), name: entry.name, type: result.previewType });
      } else await noteApi.openLink(entry.vaultId, entry.id, encodeURIComponent(entry.name));
      return;
    }
    await doc.open(entry.vaultId, entry.id); setSelected(entry); setParent(entry.parentId);
    localStorage.setItem(`czlmail.notes.last.${entry.vaultId}`, entry.id);
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
    if (!active || !doc.note || doc.note.deleted) return;
    return onFileDrop((paths) => {
      void run(async () => {
        await doc.flush(); const note = doc.getNote(); if (!note) return;
        const items = await noteApi.attachPaths(note.vaultId, note.id, note.revision, paths);
        if (doc.getNote()?.id === note.id) insert.current?.(`\n${items.map((item) => item.markdown).join("\n")}\n`);
      });
      return true;
    });
  }, [active, doc, run]);

  useEffect(() => {
    if (!active || !vaultId) return;
    const sync = () => { void noteApi.sync().catch(() => {}); };
    window.addEventListener("online", sync); window.addEventListener("focus", sync);
    return () => { window.removeEventListener("online", sync); window.removeEventListener("focus", sync); };
  }, [active, vaultId]);

  /** Form actions share the same version-checked backend as MCP. */
  async function submit() {
    await run(async () => {
      await doc.flush();
      if (action === "note" || action === "folder") {
        const entry = await noteApi.create({ vaultId, parentId: parent, name, idempotencyKey: crypto.randomUUID() }, action === "folder");
        if (!entry.directory) await open(entry);
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

  const status = doc.dirty ? t("正在保存到本地…") : doc.note?.status === "synced" ? t("已同步") : doc.note?.status === "pending" ? t("本地已保存，等待同步") : doc.note?.status === "conflict" ? t("存在版本冲突") : doc.note?.status === "remote_deleted" ? t("远端文件已删除，本地副本仍保留") : doc.note?.status ?? "";

  return <div className="flex h-full min-h-0" style={{ "--wails-drop-target": "drop" } as React.CSSProperties}>
    <ResizablePanelGroup orientation="horizontal">
    <ResizablePanel id="notes-tree" panelRef={treePanel} defaultSize={320} minSize={240} maxSize={560} onResize={(size, _id, previous) => { if (!previous) return; try { localStorage.setItem("czlmail.notes.treeWidth", String(size.inPixels)); } catch { /* Ignore unavailable preferences. */ } }}>
    <aside className="bg-sidebar flex h-full min-w-0 flex-col">
      <div className="flex flex-col gap-2 p-3">
        <div className="flex items-center gap-2"><NotebookPen className="size-4" /><h1 className="font-semibold">{t("笔记")}</h1><span className="flex-1" /><Button variant="ghost" size="icon-sm" aria-label={t("立即同步")} disabled={busy} onClick={() => void run(async () => { await doc.flush(); await noteApi.sync(); })}><RefreshCw /></Button></div>
        <Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t("搜索笔记正文和标题")} aria-label={t("搜索笔记正文和标题")} />
        <div className="flex gap-1">
          <Button variant="ghost" size="icon-sm" aria-label={t("新建笔记")} disabled={!vaultId || busy || trash} onClick={() => { setName(""); setAction("note"); }}><FilePlus2 /></Button>
          <Button variant="ghost" size="icon-sm" aria-label={t("新建文件夹")} disabled={!vaultId || busy || trash} onClick={() => { setName(""); setAction("folder"); }}><FolderPlus /></Button>
          <Button variant="ghost" size="icon-sm" aria-label={t("重命名")} disabled={!selected || busy || trash} onClick={() => { setName(selected?.name ?? ""); setAction("rename"); }}><Pencil /></Button>
          <Button variant="ghost" size="icon-sm" aria-label={t("导入笔记")} disabled={!vaultId || busy} onClick={() => void run(async () => { const out = await noteApi.importFolder(vaultId); toast.success(t("已导入 {0} 个文件，跳过 {1} 个", out.imported, out.skipped)); })}><Upload /></Button>
          <Button variant="ghost" size="icon-sm" aria-label={t("导出笔记库")} disabled={!vaultId || busy} onClick={() => void run(async () => { await doc.flush(); const path = await noteApi.exportVault(vaultId); if (path) toast.success(t("已保存到 {0}", path)); })}><Download /></Button>
        </div>
      </div>
      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto px-2">
        {vaultId && !query && !trash && <><NoteTree vaultId={vaultId} tick={tick} selectedId={selected?.id ?? ""} onOpen={(entry) => { if (!busy) void run(() => open(entry)); }} onFolder={(entry) => { setSelected(entry); setParent(entry.id); }} onAction={(entry, kind) => { if (busy) return; setSelected(entry); setParent(entry.directory ? entry.id : entry.parentId); setName(kind === "rename" ? entry.name : ""); setAction(kind); }} /></>}
        {(query || trash) && results.map((entry) => <div key={entry.id} className="flex items-center gap-1"><button className="hover:bg-secondary min-w-0 flex-1 truncate rounded-md px-2 py-2 text-left text-sm" onClick={() => { if (!entry.directory) void run(() => open(entry)); }}>{entry.name}</button>{trash && <Button variant="ghost" size="sm" onClick={() => void run(async () => { await noteApi.trash(vaultId, entry.id, entry.revision, true); setTick((v) => v + 1); })}>{t("恢复")}</Button>}</div>)}
      </div>
      <div className="border-border flex items-center gap-2 border-t p-2"><Button variant={trash ? "secondary" : "ghost"} size="sm" onClick={() => setTrash((value) => !value)}>{trash ? <FolderOpen /> : <Trash2 />}{trash ? t("回到笔记区") : t("回收站")}</Button>{selected && !trash && <Button variant="ghost" size="icon-sm" aria-label={t("移入回收站")} disabled={busy} onClick={() => setAction("trash")}><Trash2 /></Button>}</div>
    </aside>
    </ResizablePanel>
    <ResizableHandle aria-label={t("调整目录宽度")} />
    <ResizablePanel id="notes-body" minSize={400}>
    <section className="flex h-full min-w-0 flex-col">
      {!vaultId ? <div className="m-auto flex max-w-md flex-col gap-4 p-8"><h2 className="text-xl font-semibold">{t("笔记")}</h2><p className="text-muted-foreground text-sm">{t("笔记保存在个人账号的 Notes 目录中。")}</p><Button disabled={busy} onClick={() => void run(async () => { const accounts = await api.listAccounts(); const account = accounts.find((item) => item.isPersonal); if (!account) return; const v = await noteApi.bind(account.id, ""); setVaults([v]); setVaultId(v.id); await noteApi.sync(); })}>{t("开始使用笔记")}</Button></div> : <>
        <header className="border-border flex min-h-12 items-center gap-3 border-b px-4"><span className="min-w-0 flex-1 truncate text-sm">{doc.note?.name ?? t("选择或新建一篇笔记")}</span><span className="text-muted-foreground text-xs">{status}</span>
          <Button variant="ghost" size="icon-sm" aria-label={t("本地与远端版本对比")} disabled={!doc.note || busy} onClick={() => void run(async () => { await doc.flush(); const n = doc.getNote(); if (n) setDiff(await noteApi.diff(n.vaultId, n.id)); })}><GitCompareArrows /></Button>
          <Button variant="ghost" size="icon-sm" aria-label={t("添加附件")} disabled={!doc.note || busy || mode === "read" || doc.note.deleted} onClick={() => picker.current?.click()}><Paperclip /></Button>
          <ToggleGroup type="single" value={mode} onValueChange={(value) => { if (value) setMode(value); }}><ToggleGroupItem value="live" aria-label={t("实时预览")}>{t("实时预览")}</ToggleGroupItem><ToggleGroupItem value="source" aria-label={t("源码")}>{t("源码")}</ToggleGroupItem><ToggleGroupItem value="read" aria-label={t("阅读")}>{t("阅读")}</ToggleGroupItem></ToggleGroup>
        </header>
        {vault?.error && <p className="text-destructive px-4 py-2 text-sm">{t("同步未完成，本地内容仍保留")}: {vault.error}</p>}
        {doc.error && <div className="text-destructive flex items-center gap-3 px-4 py-2 text-sm"><span>{doc.error}</span><Button variant="outline" size="sm" onClick={() => void run(doc.recover)}>{t("将当前编辑另存为恢复笔记")}</Button></div>}
        <div onClickCapture={(event) => { const target = event.target as HTMLElement; const link = target.closest<HTMLAnchorElement>("a[data-note-link]"); if (link) { event.preventDefault(); void follow(link.dataset.noteLink ?? ""); } }} onDoubleClick={(event) => { const image = event.target as HTMLImageElement; if (image.tagName === "IMG" && image.dataset.notePath) void follow(image.dataset.notePath); }} className="min-h-0 flex-1"><NoteDocumentTools text={doc.text}>{doc.note ? mode === "read" ? <NotePreview vaultId={doc.note.vaultId} noteId={doc.note.id} text={doc.text} onLink={(href) => void follow(href)} /> : <NoteEditor vaultId={doc.note.vaultId} noteId={doc.note.id} value={doc.text} source={mode === "source"} readOnly={doc.note.deleted} onChange={doc.edit} onFiles={(files, at) => void attachFiles(files, at)} onReady={(at) => { insert.current = at; }} /> : <div className="text-muted-foreground flex h-full items-center justify-center text-sm">{t("选择或新建一篇笔记")}</div>}</NoteDocumentTools></div>
      </>}
    </section>
    </ResizablePanel>
    </ResizablePanelGroup>
    <input ref={picker} type="file" multiple className="hidden" onChange={(event) => { const files = Array.from(event.target.files ?? []); event.target.value = ""; if (insert.current) void attachFiles(files, insert.current); }} />
    <Dialog open={!!action} onOpenChange={(open) => { if (!open && !busy) setAction(null); }}><DialogContent><DialogHeader><DialogTitle>{action === "trash" ? t("移入回收站") : action === "rename" ? t("重命名") : action === "folder" ? t("新建文件夹") : t("新建笔记")}</DialogTitle><DialogDescription>{action === "trash" ? t("保留附件，可以从回收站恢复。") : t("使用普通文件名保存，不会改写正文标题。")}</DialogDescription></DialogHeader>{action !== "trash" && <Input autoFocus value={name} onChange={(event) => setName(event.target.value)} aria-label={t("名称")} onKeyDown={(event) => { if (event.key === "Enter" && name.trim() && !busy) void submit(); }} />}<DialogFooter><Button variant="outline" disabled={busy} onClick={() => setAction(null)}>{t("取消")}</Button><Button disabled={busy || action !== "trash" && !name.trim()} onClick={() => void submit()}>{t("确定")}</Button></DialogFooter></DialogContent></Dialog>
    <NoteAttachmentDialog item={attachmentPreview} onClose={() => setAttachmentPreview(null)} onExternal={() => void run(async () => { if (attachmentPreview) await noteApi.openLink(attachmentPreview.vaultId, attachmentPreview.noteId, attachmentPreview.path); })} />
    <NoteDiffDialog diff={diff} busy={busy} onClose={() => setDiff(null)} onResolve={(strategy) => void run(async () => { if (!diff) return; const saved = await noteApi.resolve(diff.local.vaultId, diff.local.id, diff.local.revision, diff.remoteVersion, strategy, ""); setDiff(null); await doc.open(saved.vaultId, saved.id); })} />
  </div>;
}
