"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { errorMessage, onEvent } from "@/lib/api";
import { t } from "@/lib/i18n";
import { noteApi, noteEvents, registerNoteFlush, type NoteEntry } from "@/lib/notes/api";

/** Serializes local saves and preserves dirty input when MCP or another device changes the revision. */
export function useNoteDocument() {
  const [note, setNote] = useState<NoteEntry | null>(null);
  const [text, setText] = useState("");
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const current = useRef<NoteEntry | null>(null);
  const draft = useRef("");
  const changed = useRef(false);
  const edits = useRef(0);
  const queue = useRef<Promise<void>>(Promise.resolve());

  /** Flush reads the latest draft at execution time, not when a stale timer was scheduled. */
  const flush = useCallback(() => {
    const task = queue.current.catch(() => {}).then(async () => {
      const doc = current.current;
      if (!doc || !changed.current) return;
      const snapshot = draft.current;
      let saved: NoteEntry;
      try {
        saved = await noteApi.update({ vaultId: doc.vaultId, id: doc.id, expectedRevision: doc.revision, content: snapshot });
      } catch (err) {
        // A newer version arrived while this text was being typed (2201). Keep both: the note
        // becomes a conflict with the typed text as the local side. When that is not possible
        // the original error stands and the recovery button is offered.
        if (!/\b2201\b/.test(err instanceof Error ? err.message : String(err))) throw err;
        saved = await noteApi.stash({ vaultId: doc.vaultId, id: doc.id, content: snapshot }).catch(() => { throw err; });
        if (saved.status === "conflict") toast.warning(t("这篇笔记刚从其他设备更新，你的输入已保留，请对比两个版本后选择。"));
      }
      if (current.current?.id !== saved.id) return;
      current.current = saved;
      setNote(saved);
      changed.current = draft.current !== snapshot;
      setDirty(changed.current);
      setError("");
    });
    queue.current = task;
    return task.catch((err) => { setError(errorMessage(err)); throw err; });
  }, []);

  /** Loading another document cannot discard an unsaved previous document. */
  const open = useCallback(async (vaultId: string, id: string) => {
    const leaving = current.current;
    await flush();
    // Uploads wait for typing to pause; leaving a note is the clearest pause there is.
    if (leaving && leaving.id !== id) void noteApi.nudge().catch(() => {});
    const value = await noteApi.read(vaultId, id);
    current.current = value;
    draft.current = value.content ?? "";
    changed.current = false;
    setNote(value); setText(draft.current); setDirty(false); setError("");
  }, [flush]);

  /** Network refresh never swaps over a locally dirty or in-flight editor buffer. */
  const refresh = useCallback(async () => {
    if (!current.current || changed.current) return;
    await queue.current.catch(() => {});
    const target = current.current;
    // Any keystroke during the read makes the result stale, even if a save has since cleared the dirty flag.
    const seen = edits.current;
    if (!target || changed.current) return;
    const value = await noteApi.read(target.vaultId, target.id);
    if (current.current?.id !== value.id || changed.current || edits.current !== seen) return;
    const previous = current.current;
    // A failed or retried upload changes neither revision nor status, but the title row shows it.
    const failure = (entry: NoteEntry) => `${entry.syncError ?? ""}:${entry.syncAttempts ?? 0}`;
    if (value.revision !== previous.revision || value.status !== previous.status || failure(value) !== failure(previous)) {
      current.current = value; setNote(value);
      // Status-only changes (pending -> synced) leave the editor text alone.
      if (value.revision !== previous.revision && (value.content ?? "") !== draft.current) {
        draft.current = value.content ?? ""; setText(draft.current);
      }
    }
  }, []);

  /** Recovery creates a new local note before clearing a stale buffer conflict. */
  const recover = useCallback(async () => {
    const doc = current.current;
    if (!doc) return;
    const saved = await noteApi.create({ vaultId: doc.vaultId, parentId: doc.parentId, name: `recovery-${crypto.randomUUID().slice(0, 8)}-${doc.name}`, content: draft.current, idempotencyKey: crypto.randomUUID() });
    current.current = saved; changed.current = false; setNote(saved); setDirty(false); setError("");
  }, []);

  /** Input never changes revision; only a confirmed backend save advances the revision token. */
  const edit = useCallback((value: string) => {
    edits.current++; draft.current = value; changed.current = value !== (current.current?.content ?? "");
    setText(value); setDirty(changed.current);
  }, []);

  useEffect(() => registerNoteFlush(flush), [flush]);
  // 切到后台 (手机上程序随即被挂起, 桌面上是窗口隐藏): 输入先落本机, 再让排队的上传立刻走,
  // 不等「停止输入 3 秒」——挂起后计时器不走, 等下去就是下次打开才上传。
  useEffect(() => {
    const leave = () => {
      if (document.visibilityState !== "hidden") return;
      void flush().catch(() => {}).then(() => noteApi.nudge()).catch(() => {});
    };
    document.addEventListener("visibilitychange", leave);
    return () => document.removeEventListener("visibilitychange", leave);
  }, [flush]);
  useEffect(() => {
    if (!dirty || error) return;
    const timer = setTimeout(() => { void flush().catch(() => {}); }, 500);
    return () => clearTimeout(timer);
  }, [text, dirty, error, flush]);
  useEffect(() => onEvent(noteEvents.changed, () => { void refresh().catch((err) => toast.error(errorMessage(err))); }), [refresh]);
  useEffect(() => {
    const onSave = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); void flush().catch((err) => toast.error(errorMessage(err))); }
    };
    window.addEventListener("keydown", onSave);
    return () => window.removeEventListener("keydown", onSave);
  }, [flush]);
  return { note, text, dirty, error, open, refresh, edit, flush, recover, getNote: () => current.current };
}
