// Fails when the language files differ in shape: missing or extra keys, a text in
// one and a function in the other, functions with different parameters, lists of
// different length. Run with `npm run i18n:check` (Node strips the TypeScript types).
import { en } from "./en.ts"
import { zhCN } from "./zh-CN.ts"

const problems = []

function kind(v) {
  if (Array.isArray(v)) return "list"
  if (v === null) return "null"
  return typeof v
}

function compare(a, b, path) {
  const ka = kind(a)
  const kb = kind(b)
  if (ka !== kb) return problems.push(`${path}: ${ka} in zh-CN, ${kb} in en`)
  if (ka === "function" && a.length !== b.length)
    return problems.push(`${path}: ${a.length} parameter(s) in zh-CN, ${b.length} in en`)
  if (ka === "list") {
    if (a.length !== b.length) problems.push(`${path}: ${a.length} item(s) in zh-CN, ${b.length} in en`)
    return
  }
  if (ka !== "object") return
  for (const k of Object.keys(a)) {
    if (!(k in b)) problems.push(`${path}.${k}: missing in en`)
    else compare(a[k], b[k], `${path}.${k}`)
  }
  for (const k of Object.keys(b)) if (!(k in a)) problems.push(`${path}.${k}: missing in zh-CN`)
}

compare(zhCN, en, "messages")
if (problems.length > 0) {
  console.error(`i18n: the language files differ (${problems.length}):\n  ` + problems.join("\n  "))
  process.exit(1)
}
console.log("i18n: zh-CN and en have the same keys")
