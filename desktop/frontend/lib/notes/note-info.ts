import { frontmatter } from "@/lib/notes/markdown";

export interface NoteInfo { created: number; modified: number; words: number }

/** Front matter keys Obsidian users commonly keep the original creation time in. */
const createdKeys = ["created", "date", "created_at", "createdAt", "创建时间"];

/**
 * Creation time prefers the note's own front matter (it survives exports, imports and migration),
 * then the stored creation time; modification time is the last local or synced edit.
 */
export function noteInfo(text: string, created: number | undefined, modified: number): NoteInfo {
  const metadata = frontmatter(text);
  let fromText = 0;
  for (const [key, value] of metadata?.rows ?? []) {
    if (!createdKeys.includes(key)) continue;
    const parsed = Date.parse(value.replace(" ", "T"));
    if (Number.isFinite(parsed)) { fromText = parsed; break; }
  }
  const body = text.slice(metadata?.to ?? 0);
  // Chinese and Japanese are counted per character, other scripts per word, like Obsidian.
  const cjk = body.match(/[぀-ヿ㐀-鿿豈-﫿]/g)?.length ?? 0;
  const words = body.replace(/[぀-ヿ㐀-鿿豈-﫿]/g, " ").match(/[\p{L}\p{N}][\p{L}\p{N}'’_-]*/gu)?.length ?? 0;
  return { created: fromText || created || 0, modified, words: cjk + words };
}
