// Upstream mailbox management (#39, PR #56). The routes answer 404 until that work
// package is deployed; pages then show "not ready". The view is mapped to the
// overview's UpstreamCard here, so both pages render mailboxes the same way.
import { api } from "./client"
import type { UpstreamCard, UpstreamStatus, WatchMode } from "@/lib/types"

/** tls (implicit TLS) or starttls; the gateway also accepts plain, the admin does not offer it. */
export type TlsMode = "tls" | "starttls" | "plain"

export interface EndpointInput {
  host: string
  port: number
  tls: TlsMode
  username?: string
  /** Omitted or "" on update: keep the stored password. */
  password?: string
}

/** follow: the gateway's default proxy (setting proxy.default); none: direct. */
export type ProxyMode = "follow" | "none" | "custom"

export interface ProxyInput {
  mode: ProxyMode
  /** socks5://[user@]host:port for custom. */
  url?: string
  /** Omitted or "" on update: keep the stored proxy password. */
  password?: string
}

export interface UpstreamInput {
  name: string
  email: string
  imap: EndpointInput
  smtp?: EndpointInput
  proxy?: ProxyInput
}

export type Capabilities = Partial<
  Record<
    | "condstore"
    | "qresync"
    | "idle"
    | "notify"
    | "move"
    | "uidplus"
    | "objectid"
    | "xguid"
    | "specialUse"
    | "listStatus"
    | "sort",
    boolean
  >
>

interface EndpointView {
  host: string
  port: number
  tls: TlsMode
  username: string
  hasPassword: boolean
}

/** GET /upstreams item as the gateway sends it. */
export interface UpstreamView {
  id: string
  userId: string
  kind: UpstreamCard["kind"]
  name: string
  email: string
  imap: EndpointView
  smtp: EndpointView
  proxy: { mode: ProxyMode; url: string; hasPassword: boolean }
  paused: boolean
  isDefault: boolean
  status: {
    state: UpstreamStatus
    code: number | null
    since: string | null
    /** The mail server's own reply, for the mailbox owner. */
    detail: string
    initialSync: { done: number; total: number } | null
  }
  capabilities: Capabilities
  /** How sent mail reaches the Sent folder (#41). */
  sentCopy: SentCopy
  /** Folders (Gmail: labels) in the index; null until the first sync is done. */
  mailboxes: number | null
  createdAt: string | null
}

/**
 * auto: the first send (or a test send) finds out whether the server keeps its own
 * copy; gateway: the gateway files the copy; server: the server does.
 */
export type SentCopy = "auto" | "gateway" | "server"

export interface SentCopyTest {
  ok: boolean
  /** The strategy now stored. */
  sentCopy: SentCopy
  serverSaves: boolean
  waitedMs: number
  code?: number
  msg?: string
  detail?: string
}

export interface IdentityAddress {
  name: string | null
  email: string
}

export interface Identity {
  id: string
  /** primary: the mailbox address; alias: added here (may be *@domain); provider: Gmail send-as. */
  source: "primary" | "alias" | "provider"
  email: string
  name: string
  replyTo: IdentityAddress[]
  bcc: IdentityAddress[]
  textSignature: string
  htmlSignature: string
  mayDelete: boolean
  updatedAt: string | null
}

export interface IdentityInput {
  /** Only for new aliases and alias edits. */
  email?: string
  name?: string
  replyTo?: IdentityAddress[]
  bcc?: IdentityAddress[]
  textSignature?: string
  htmlSignature?: string
}

/**
 * POST /upstreams/oauth/{provider}/start. The callback sends the browser back to
 * /admin/mailboxes/ with ?added=<id>, ?reauthorized=<id> or ?oauthError=<msg>&code=<n>.
 */
export interface OAuthStartInput {
  /** Login hint: preselects the account on the consent page. */
  email?: string
  /** Account name; defaults to the address. */
  name?: string
  proxy?: { mode: ProxyMode; url?: string; password?: string }
}

/** A mailbox for the pages: the overview card plus what the actions need. */
export interface Upstream extends UpstreamCard {
  view: UpstreamView
  isDefault: boolean
  paused: boolean
}

export interface ProbePart {
  ok: boolean
  code?: number
  msg?: string
  detail?: string
  capabilities?: Capabilities
  auth?: string[]
}

export interface ProbeReport {
  imap: ProbePart
  /** null when no SMTP server is configured. */
  smtp: ProbePart | null
}

export function probeOk(r: ProbeReport): boolean {
  return r.imap.ok && (!r.smtp || r.smtp.ok)
}

/** How the gateway can watch the mailbox, from the server's capabilities. */
export function watchModeOf(caps: Capabilities | undefined): WatchMode {
  if (!caps || Object.keys(caps).length === 0) return ""
  if (caps.notify) return "notify"
  if (caps.idle) return "idle"
  return "poll"
}

/** Capability names the server announced, for the connection report. */
export function capabilityNames(caps: Capabilities | undefined): string[] {
  return Object.entries(caps ?? {})
    .filter(([, on]) => on)
    .map(([k]) => k.toUpperCase())
}

function proxyHost(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

export function toUpstream(v: UpstreamView): Upstream {
  const failing =
    v.status.state === "authRequired" || v.status.state === "unreachable"
  return {
    id: v.id,
    kind: v.kind,
    name: v.name,
    email: v.email,
    ownerEmail: "",
    status: v.status.state,
    statusSince: v.status.since,
    code: v.status.code ?? 0,
    watchMode: watchModeOf(v.capabilities),
    lastSyncAt: undefined, // not in this view
    initialSync: v.status.initialSync,
    lastError:
      failing && v.status.code
        ? {
            code: v.status.code,
            msg: "",
            at: v.status.since ?? "",
            detail: v.status.detail,
          }
        : null,
    proxy:
      v.proxy.mode === "none"
        ? null
        : v.proxy.mode === "follow"
          ? { mode: "default", host: "" }
          : { mode: "custom", host: proxyHost(v.proxy.url) },
    mailboxes: v.mailboxes ?? null,
    view: v,
    isDefault: v.isDefault,
    paused: v.paused,
  }
}

const path = (id: string) => `/upstreams/${encodeURIComponent(id)}`

export const upstreamsApi = {
  list: async () =>
    (await api.get<UpstreamView[]>("/upstreams")).map(toUpstream),
  test: (body: UpstreamInput) => api.post<ProbeReport>("/upstreams/test", body),
  create: (body: UpstreamInput) => api.post<UpstreamView>("/upstreams", body),
  update: (
    id: string,
    body: Partial<UpstreamInput> & { sentCopy?: SentCopy }
  ) => api.patch<UpstreamView>(path(id), body),
  testExisting: (id: string) => api.post<ProbeReport>(`${path(id)}/test`),
  pause: (id: string) => api.post<UpstreamView>(`${path(id)}/pause`),
  resume: (id: string) => api.post<UpstreamView>(`${path(id)}/resume`),
  setDefault: (id: string) => api.post<UpstreamView>(`${path(id)}/default`),
  remove: (id: string) => api.del<object>(path(id)),
  /** TODO(#39 follow-up): server detection (presets → ISPDB → autoconfig) is not built yet; answers 404. */
  autoconfig: (email: string) =>
    api.get<{
      source: "preset" | "ispdb" | "autoconfig" | "none"
      imap?: { host: string; port: number; tls: TlsMode }
      smtp?: { host: string; port: number; tls: TlsMode }
    }>(`/upstreams/autoconfig?email=${encodeURIComponent(email)}`),
  /** Consent URL to re-authorise a Gmail / Microsoft mailbox. */
  reauth: (id: string) => api.post<{ url: string }>(`${path(id)}/reauth`),
  setSentCopy: (id: string, sentCopy: SentCopy) =>
    api.patch<UpstreamView>(path(id), { sentCopy }),
  /** Sends a message to the mailbox itself and stores the result; takes up to ~15 s. */
  sendTest: (id: string) => api.post<SentCopyTest>(`${path(id)}/send-test`),
  identities: (id: string) => api.get<Identity[]>(`${path(id)}/identities`),
  createIdentity: (id: string, body: IdentityInput) =>
    api.post<Identity>(`${path(id)}/identities`, body),
  updateIdentity: (id: string, identityId: string, body: IdentityInput) =>
    api.patch<Identity>(
      `${path(id)}/identities/${encodeURIComponent(identityId)}`,
      body
    ),
  deleteIdentity: (id: string, identityId: string) =>
    api.del<object>(`${path(id)}/identities/${encodeURIComponent(identityId)}`),
  /** Starts adding a Gmail / Microsoft mailbox; returns the consent URL. */
  startOAuth: (provider: "google" | "microsoft", body: OAuthStartInput = {}) =>
    api.post<{ url: string }>(`/upstreams/oauth/${provider}/start`, body),
}
