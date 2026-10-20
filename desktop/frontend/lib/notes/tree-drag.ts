import type { NoteEntry } from "@/lib/notes/api";

/** Only a live, same-vault drag may change parent; ancestors also rule out subtree cycles. */
export function canDropNote(entry: NoteEntry | null, vaultId: string, parentId: string, ancestors: readonly string[], disabled: boolean) {
  return !disabled && !!entry && entry.vaultId === vaultId && !entry.deleted && entry.parentId !== parentId && entry.id !== parentId && !ancestors.includes(entry.id);
}
