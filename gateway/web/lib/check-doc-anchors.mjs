// Checks that every anchor in lib/docs.ts is a heading of gateway/docs/guide.md
// (English) or guide.zh-CN.md (Chinese), with GitHub's heading slugs.
// Run by `npm run i18n:check`; skipped while the guide is not in the tree.
import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const docsDir = path.join(here, "..", "..", "docs")
const table = fs.readFileSync(path.join(here, "docs.ts"), "utf8")
const pairs = [...table.matchAll(/\[\s*"([^"]+)",\s*"([^"]+)",?\s*\]/g)]

function slugs(file) {
  const out = new Set()
  for (const line of fs.readFileSync(file, "utf8").split("\n")) {
    const m = line.match(/^#{1,6}\s+(.*?)\s*$/)
    if (!m) continue
    out.add(
      m[1]
        .toLowerCase()
        .replace(/[^\p{L}\p{M}\p{N}\p{Pc}\- ]/gu, "")
        .replace(/ /g, "-")
    )
  }
  return out
}

const en = path.join(docsDir, "guide.md")
const zh = path.join(docsDir, "guide.zh-CN.md")
if (!fs.existsSync(en) || !fs.existsSync(zh)) {
  console.log("doc anchors: gateway/docs/guide*.md not found, skipped")
  process.exit(0)
}
const enSlugs = slugs(en)
const zhSlugs = slugs(zh)
let bad = 0
for (const [, a, b] of pairs) {
  if (!enSlugs.has(a)) {
    bad++
    console.error(`guide.md has no heading #${a}`)
  }
  if (!zhSlugs.has(b)) {
    bad++
    console.error(`guide.zh-CN.md has no heading #${b}`)
  }
}
if (bad) process.exit(1)
console.log(`doc anchors: ${pairs.length} topics match the guide headings`)
