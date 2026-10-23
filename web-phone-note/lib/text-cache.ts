import { download, type FileNode, type Session } from "@/lib/jmap"

/**
 * Note text keyed by blobId. Blobs are immutable, so a hit never goes stale and an edited note simply
 * gets a new key. IndexedDB keeps it across visits (search would otherwise re-download the whole vault);
 * private windows and blocked storage fall back to memory only.
 */
const memory = new Map<string, string>()
let database: Promise<IDBDatabase | null> | null = null

function open(): Promise<IDBDatabase | null> {
  database ??= new Promise((resolve) => {
    try {
      const request = indexedDB.open("czl-notes", 1)
      request.onupgradeneeded = () => request.result.createObjectStore("text")
      request.onsuccess = () => resolve(request.result)
      request.onerror = () => resolve(null)
    } catch { resolve(null) }
  })
  return database
}

async function stored(key: string): Promise<string | undefined> {
  const db = await open()
  if (!db) return undefined
  return new Promise((resolve) => {
    try {
      const request = db.transaction("text").objectStore("text").get(key)
      request.onsuccess = () => resolve(typeof request.result === "string" ? request.result : undefined)
      request.onerror = () => resolve(undefined)
    } catch { resolve(undefined) }
  })
}

async function store(key: string, value: string) {
  const db = await open()
  try { db?.transaction("text", "readwrite").objectStore("text").put(value, key) } catch { /* Memory copy still serves this visit. */ }
}

/** Matches the desktop limit; larger files are not notes it would have synced either. */
const MAX_TEXT = 4 << 20

export async function noteText(session: Session, node: FileNode): Promise<string> {
  const key = `${session.accountId}/${node.blobId}`
  const cached = memory.get(key) ?? (await stored(key))
  if (cached !== undefined) { memory.set(key, cached); return cached }
  if (node.size > MAX_TEXT) throw new Error("note too large")
  const text = await (await download(session, node)).text()
  memory.set(key, text)
  void store(key, text)
  return text
}

/** Logging out removes cached note text from this browser. */
export async function clearTextCache() {
  memory.clear()
  const db = await open()
  try { db?.transaction("text", "readwrite").objectStore("text").clear() } catch { /* Nothing stored. */ }
}
