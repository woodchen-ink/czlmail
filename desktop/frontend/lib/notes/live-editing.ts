/** Source change that flips the task box of the list item starting at `from`; null when it is not a task. */
export function toggleTask(text: string, from: number): { from: number; to: number; insert: string } | null {
  const match = /^([ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+\[)([ xX])\]/.exec(text.slice(from, from + 80));
  if (!match) return null;
  const at = from + match[1].length;
  return { from: at, to: at + 1, insert: match[2] === " " ? "x" : " " };
}

/**
 * Offset in `source` after the rendered `prefix`: characters are matched in order and Markdown
 * syntax between them (`**`, `#`, link targets…) is skipped. Whitespace may collapse when rendered.
 */
export function caretOffset(source: string, prefix: string): number {
  let at = 0;
  for (const char of prefix) {
    if (/\s/.test(char)) continue;
    const found = source.indexOf(char, at);
    // A rendered-only character (list numbers, footnote labels) must not drag the caret far ahead.
    if (found < 0 || found - at > 200) continue;
    at = found + char.length;
  }
  return at;
}
