/**
 * Browser-side JMAP client: only what the read-only reader needs (session, FileNode/query+get, blob download).
 * The server must allow CORS for the page origin; Stalwart answers `Access-Control-Allow-Origin: *`.
 */

const CORE = "urn:ietf:params:jmap:core"
export const FILENODE = "urn:ietf:params:jmap:filenode"

/** Numeric codes reach the UI next to the human message (see `errorText`). */
export class JmapError extends Error {
  constructor(readonly code: number, message: string) {
    super(`${code} ${message}`)
  }
}
export const errBadCredentials = () => new JmapError(3001, "bad_credentials")
export const errNotJmap = () => new JmapError(3002, "not_a_jmap_server")
export const errUnreachable = () => new JmapError(3003, "server_unreachable")
export const errNoFiles = () => new JmapError(3004, "file_storage_unavailable")
export const errRequest = () => new JmapError(3005, "request_failed")

export interface Credentials { server: string; username: string; password: string }

export interface Session {
  apiUrl: string
  downloadUrl: string
  accountId: string
  maxObjectsInGet: number
  authorization: string
}

export interface FileNode {
  id: string
  parentId: string
  name: string
  directory: boolean
  blobId: string
  type: string
  size: number
  /** Unix ms; 0 when the server does not report it. */
  modified: number
}

/** `mail.example.com`, `https://mail.example.com/` and full URLs all normalise to an origin. */
export function serverOrigin(server: string): string {
  const raw = server.trim()
  const url = new URL(/^https?:\/\//i.test(raw) ? raw : `https://${raw}`)
  return url.origin
}

function basic({ username, password }: Credentials) {
  const bytes = new TextEncoder().encode(`${username}:${password}`)
  return `Basic ${btoa(String.fromCharCode(...bytes))}`
}

async function send(url: string, authorization: string, init: RequestInit = {}): Promise<Response> {
  let response: Response
  try {
    response = await fetch(url, { ...init, headers: { ...init.headers, Authorization: authorization }, cache: "no-store" })
  } catch {
    throw errUnreachable()
  }
  if (response.status === 401 || response.status === 403) throw errBadCredentials()
  return response
}

export async function connect(credentials: Credentials): Promise<Session> {
  const origin = serverOrigin(credentials.server)
  const authorization = basic(credentials)
  const response = await send(`${origin}/.well-known/jmap`, authorization)
  if (!response.ok) throw response.status === 404 ? errNotJmap() : errRequest()
  let session: {
    apiUrl?: string; downloadUrl?: string; capabilities?: Record<string, { maxObjectsInGet?: number }>
    primaryAccounts?: Record<string, string>; accounts?: Record<string, { isPersonal?: boolean; accountCapabilities?: Record<string, unknown> }>
  }
  try { session = await response.json() } catch { throw errNotJmap() }
  if (!session.apiUrl || !session.downloadUrl) throw errNotJmap()
  const personal = Object.entries(session.accounts ?? {}).find(([, a]) => a.isPersonal && a.accountCapabilities?.[FILENODE])?.[0]
  const accountId = session.primaryAccounts?.[FILENODE] ?? personal
  if (!accountId) throw errNoFiles()
  return {
    // Some deployments publish relative URLs; resolve them against the server.
    apiUrl: new URL(session.apiUrl, origin).href,
    downloadUrl: new URL(session.downloadUrl, origin).href.replace(/%7B/g, "{").replace(/%7D/g, "}"),
    accountId,
    maxObjectsInGet: session.capabilities?.[CORE]?.maxObjectsInGet ?? 500,
    authorization,
  }
}

type Invocation = [string, Record<string, unknown>, string]

async function call(session: Session, methodCalls: Invocation[]): Promise<Invocation[]> {
  const response = await send(session.apiUrl, session.authorization, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ using: [CORE, FILENODE], methodCalls }),
  })
  if (!response.ok) throw errRequest()
  const body = (await response.json()) as { methodResponses?: Invocation[] }
  const results = body.methodResponses ?? []
  if (results.some(([name]) => name === "error")) throw errRequest()
  return results
}

/** All FileNodes of the account. Pages of `maxObjectsInGet` use a back-reference so each page is one round trip. */
export async function listFileNodes(session: Session): Promise<FileNode[]> {
  const limit = Math.min(500, session.maxObjectsInGet)
  const nodes: FileNode[] = []
  for (let position = 0; position < 100_000; position += limit) {
    const [query, get] = await call(session, [
      ["FileNode/query", { accountId: session.accountId, position, limit, calculateTotal: true }, "q"],
      ["FileNode/get", {
        accountId: session.accountId,
        "#ids": { resultOf: "q", name: "FileNode/query", path: "/ids" },
        properties: ["id", "parentId", "name", "nodeType", "blobId", "type", "size", "modified"],
      }, "g"],
    ])
    const ids = (query?.[1].ids as string[] | undefined) ?? []
    for (const raw of (get?.[1].list as Record<string, unknown>[] | undefined) ?? []) nodes.push(fileNode(raw))
    const total = Number(query?.[1].total ?? 0)
    if (ids.length < limit || (total > 0 && position + ids.length >= total)) break
  }
  return nodes
}

function fileNode(raw: Record<string, unknown>): FileNode {
  const text = (value: unknown) => (typeof value === "string" ? value : "")
  const blobId = text(raw.blobId)
  const nodeType = text(raw.nodeType)
  const modified = Date.parse(text(raw.modified))
  return {
    id: text(raw.id),
    parentId: text(raw.parentId),
    name: text(raw.name),
    // Same rule as the desktop syncer: without nodeType, a node without a blob is a directory.
    directory: nodeType === "directory" || nodeType === "folder" || (!nodeType && !blobId),
    blobId,
    type: text(raw.type),
    size: Number(raw.size ?? 0),
    modified: Number.isFinite(modified) ? modified : 0,
  }
}

export async function download(session: Session, node: FileNode): Promise<Blob> {
  const url = session.downloadUrl
    .replace("{accountId}", encodeURIComponent(session.accountId))
    .replace("{blobId}", encodeURIComponent(node.blobId))
    .replace("{name}", encodeURIComponent(node.name))
    .replace("{type}", encodeURIComponent(node.type || "application/octet-stream"))
  const response = await send(url, session.authorization)
  if (!response.ok) throw errRequest()
  return response.blob()
}
