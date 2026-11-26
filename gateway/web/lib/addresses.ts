import type { IdentityAddress } from "@/lib/api/upstreams"

/** "Name <a@b>, c@d" for an address list input. */
export function formatAddresses(list: IdentityAddress[]): string {
  return list
    .map((a) => (a.name ? `${a.name} <${a.email}>` : a.email))
    .join(", ")
}

/**
 * Parses "Name <a@b>, c@d" (comma or semicolon separated). Returns null when an
 * entry has no plausible address, so the form can say which field is wrong.
 */
export function parseAddresses(text: string): IdentityAddress[] | null {
  const out: IdentityAddress[] = []
  for (const raw of text.split(/[,;]/)) {
    const part = raw.trim()
    if (!part) continue
    const m = part.match(/^(.*?)\s*<([^<>\s]+)>$/)
    const email = (m ? m[2] : part).trim()
    if (!/^[^@\s]+@[^@\s]+$/.test(email)) return null
    const name = m ? m[1].trim().replace(/^"(.*)"$/, "$1") : ""
    out.push({ name: name || null, email })
  }
  return out
}
