import type { FileNode } from "@/lib/jmap"

/** Desktop moves deleted notes here; the reader hides it like the desktop tree does. */
const TRASH = ".czl-trash"

/** One vault: a directory of the account's file storage, indexed for browsing and link resolution. */
export class Library {
  readonly root: FileNode
  private readonly byId = new Map<string, FileNode>()
  private readonly children = new Map<string, FileNode[]>()

  constructor(nodes: FileNode[], rootId: string) {
    for (const node of nodes) this.byId.set(node.id, node)
    const root = this.byId.get(rootId)
    if (!root?.directory) throw new Error("vault root missing")
    this.root = root
    for (const node of nodes) {
      if (node.name === TRASH || !this.inside(node.id)) continue
      const list = this.children.get(node.parentId) ?? []
      list.push(node)
      this.children.set(node.parentId, list)
    }
    const collator = new Intl.Collator("zh-CN", { numeric: true })
    for (const list of this.children.values()) list.sort((a, b) => Number(b.directory) - Number(a.directory) || collator.compare(a.name, b.name))
  }

  get(id: string) { return this.byId.get(id) }

  list(directoryId: string): FileNode[] { return this.children.get(directoryId) ?? [] }

  /** Every Markdown note in the vault, for search. */
  notes(): FileNode[] {
    const result: FileNode[] = []
    const walk = (id: string) => {
      for (const node of this.list(id)) {
        if (node.directory) walk(node.id)
        else if (isNote(node)) result.push(node)
      }
    }
    walk(this.root.id)
    return result
  }

  /** Directory chain from the vault root (exclusive) down to `id` (inclusive). */
  trail(id: string): FileNode[] {
    const chain: FileNode[] = []
    for (let node = this.byId.get(id); node && node.id !== this.root.id && chain.length < 128; node = this.byId.get(node.parentId)) chain.unshift(node)
    return chain
  }

  path(id: string) { return this.trail(id).map((node) => node.name).join("/") }

  /**
   * Same order as the desktop `resolveLink`: document-relative path, then a unique bare name
   * (Obsidian embeds, `.md` optional for notes), then a vault-root-relative path. Never leaves the vault.
   */
  resolve(fromNoteId: string, href: string): FileNode | null {
    const [relative] = href.split("#")
    let decoded: string
    try { decoded = decodeURIComponent(relative) } catch { return null }
    if (!decoded || /[\\\0:]/.test(decoded) || decoded.startsWith("/")) return null
    const note = this.byId.get(fromNoteId)
    if (!note) return null
    const walk = (start: string, parts: string[]) => {
      let parent = start
      let target: FileNode | null = null
      for (const part of parts) {
        if (part === "" || part === ".") continue
        if (part === "..") {
          if (parent === this.root.id) return null
          parent = this.byId.get(parent)?.parentId ?? ""
          target = null
          continue
        }
        target = this.list(parent).find((node) => node.name === part) ?? null
        if (!target) return null
        parent = target.id
      }
      return target
    }
    const parts = decoded.split("/")
    const relativeHit = walk(note.parentId, parts)
    if (relativeHit) return relativeHit
    if (!decoded.includes("/")) {
      const all: FileNode[] = []
      const collect = (id: string) => { for (const node of this.list(id)) { all.push(node); if (node.directory) collect(node.id) } }
      collect(this.root.id)
      const matches = all.filter((node) => node.name === decoded || (isNote(node) && node.name === `${decoded}.md`))
      if (matches.length === 1) return matches[0]
      if (matches.length > 1) return null
    }
    if (parts.some((part) => part === "..")) return null
    return walk(this.root.id, parts)
  }

  private inside(id: string): boolean {
    for (let node = this.byId.get(id), depth = 0; node && depth < 128; node = this.byId.get(node.parentId), depth++) {
      if (node.name === TRASH) return false
      if (node.parentId === this.root.id) return true
    }
    return false
  }
}

export const isNote = (node: FileNode) => !node.directory && /\.md$/i.test(node.name)

/** Directories at the account root are the vault candidates; desktop defaults to `Notes`. */
export function vaultCandidates(nodes: FileNode[]): FileNode[] {
  return nodes.filter((node) => node.directory && !node.parentId && node.name !== TRASH).sort((a, b) => Number(b.name === "Notes") - Number(a.name === "Notes") || a.name.localeCompare(b.name))
}

export const noteTitle = (node: FileNode) => node.name.replace(/\.md$/i, "")

const IMAGE_TYPES: Record<string, string> = { png: "image/png", jpg: "image/jpeg", jpeg: "image/jpeg", gif: "image/gif", webp: "image/webp", avif: "image/avif", bmp: "image/bmp", svg: "image/svg+xml" }

/** Image MIME type from the FileNode, or from the extension when the uploader left `type` empty; null for non-images. */
export function imageType(node: FileNode): string | null {
  if (node.directory) return null
  if (node.type.startsWith("image/")) return node.type
  return IMAGE_TYPES[node.name.split(".").pop()?.toLowerCase() ?? ""] ?? null
}
