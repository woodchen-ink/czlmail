import { parseDocument } from "yaml";

export interface Frontmatter { from: number; to: number; raw: string; rows: [string, string][]; error: boolean }

/** Only a complete leading YAML block is metadata; invalid YAML remains recoverable verbatim. */
export function frontmatter(text: string): Frontmatter | null {
  const match = /^(?:\uFEFF)?---[ \t]*\r?\n([\s\S]*?)\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(text);
  if (!match) return null;
  const doc = parseDocument(match[1], { schema: "core", uniqueKeys: true });
  try {
    const value: unknown = doc.toJS({ maxAliasCount: 50 });
    const valid = !doc.errors.length && value !== null && typeof value === "object" && !Array.isArray(value);
    return { from: 0, to: match[0].length, raw: match[1], error: !valid, rows: valid ? Object.entries(value as Record<string, unknown>).map(([key, value]) => [key, display(value)]) : [] };
  } catch { return { from: 0, to: match[0].length, raw: match[1], rows: [], error: true }; }
}

/** Nested YAML stays text, never HTML or an executable object. */
function display(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (Array.isArray(value)) return value.map(display).join(", ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

/** Resolve only HTTP(S) explicitly or a vault-scoped local resource, never active URL schemes. */
export function imageTarget(path: string, vault: string, note: string): { src: string; remote: boolean } | null {
  if (/^https?:\/\//i.test(path)) return { src: path, remote: true };
  if (/^[a-z][a-z0-9+.-]*:|^\//i.test(path)) return null;
  return { src: `/czl-note-resource?${new URLSearchParams({ vault, note, path })}`, remote: false };
}
