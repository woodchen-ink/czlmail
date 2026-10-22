import * as App from "@/wailsjs/go/main/App";
import { main, notes } from "@/wailsjs/go/models";

export type NoteEntry = notes.Entry;
export type NoteVault = notes.Vault;
export type NoteConfig = notes.Config;
export type NoteDiff = notes.Diff;
export type JournalConfig = main.JournalConfig;

/** Every caller uses generated Wails bindings; no second note transport or browser database. */
export const noteApi = {
  config: App.GetNoteConfig,
  configure: (value: NoteConfig) => App.SetNoteConfig(notes.Config.createFrom(value)),
  vaults: App.ListNoteVaults,
  bind: App.BindNoteVault,
  list: App.ListNotes,
  read: App.ReadNote,
  create: (value: Partial<notes.Edit>, directory = false) => App.CreateNote(notes.Edit.createFrom(value), directory),
  update: (value: Partial<notes.Edit>) => App.UpdateNote(notes.Edit.createFrom(value)),
  move: (value: Partial<notes.Edit>) => App.MoveNote(notes.Edit.createFrom(value)),
  trash: App.TrashNote,
  diff: App.CompareNote,
  resolve: App.ResolveNote,
  sync: App.SyncNotes,
  attach: App.AddNoteAttachment,
  attachPaths: App.AttachNotePaths,
  openLink: App.OpenNoteLink,
  previewLink: App.PreviewNoteLink,
  importFolder: App.ImportNoteFolder,
  exportVault: App.ExportNoteVault,
  openJournal: App.OpenJournal,
  journalConfig: App.GetJournalConfig,
  setJournalConfig: (value: JournalConfig) => App.SetJournalConfig(main.JournalConfig.createFrom(value)),
};

/** The local calendar day as YYYY-MM-DD; toISOString would give the UTC day. */
export function localDay(date = new Date()) {
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

export const noteEvents = { changed: "notes:changed", config: "notes:config-changed" };
const flushers = new Set<() => Promise<void>>();

/** Flush handlers are awaited before disabling the module or navigating away. */
export function registerNoteFlush(flush: () => Promise<void>) {
  flushers.add(flush);
  return () => { flushers.delete(flush); };
}

/** A failed local save aborts the operation instead of discarding the editor buffer. */
export async function flushNotes() {
  for (const flush of flushers) await flush();
}

/** Note resources resolve relative to a document; arbitrary filesystem URLs are never exposed. */
export function noteResource(vaultId: string, noteId: string, path: string) {
  return `/czl-note-resource?${new URLSearchParams({ vault: vaultId, note: noteId, path })}`;
}

/** Clipboard files are encoded only after size validation by the caller. */
export function fileBase64(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",", 2)[1] ?? "");
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}
