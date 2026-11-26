// Upstream mailbox management. TODO(#39): these routes land with the upstream
// framework; until then they answer 404 and the pages show "not ready". The contract
// is written down in the shared notes (gw-44-admin-web.md) and in README "Admin API".
import { api } from "./client"
import type { UpstreamCard } from "@/lib/types"

export type Security = "tls" | "starttls"

export interface ServerSettings {
  host: string
  port: number
  security: Security
  username: string
  /** Omitted on update: keep the stored password. */
  password?: string
}

export type ProxyMode = "default" | "none" | "custom"

export interface ProxySettings {
  mode: ProxyMode
  /** socks5://user:pass@host:port for "custom"; the server answers it masked. */
  url?: string
}

export interface Upstream extends UpstreamCard {
  isDefault: boolean
  paused: boolean
  imap?: Omit<ServerSettings, "password">
  smtp?: Omit<ServerSettings, "password">
  proxySettings?: ProxySettings
  createdAt: string
}

export interface NewImapUpstream {
  kind: "imap"
  name: string
  email: string
  imap: ServerSettings
  smtp: ServerSettings
  proxy: ProxySettings
}

export interface Autoconfig {
  source: "preset" | "ispdb" | "autoconfig" | "none"
  provider?: string
  imap?: { host: string; port: number; security: Security }
  smtp?: { host: string; port: number; security: Security }
}

export interface ConnectionReport {
  ok: boolean
  imap: {
    ok: boolean
    code: number
    msg: string
    capabilities: string[]
    watchMode: string
  }
  smtp: { ok: boolean; code: number; msg: string }
}

const path = (id: string) => `/upstreams/${encodeURIComponent(id)}`

export const upstreamsApi = {
  list: () => api.get<Upstream[]>("/upstreams"),
  autoconfig: (email: string) =>
    api.get<Autoconfig>(
      `/upstreams/autoconfig?email=${encodeURIComponent(email)}`
    ),
  test: (body: NewImapUpstream) =>
    api.post<ConnectionReport>("/upstreams/test", body),
  create: (body: NewImapUpstream) => api.post<Upstream>("/upstreams", body),
  update: (id: string, body: Partial<Omit<NewImapUpstream, "kind">>) =>
    api.patch<Upstream>(path(id), body),
  testExisting: (id: string) => api.post<ConnectionReport>(`${path(id)}/test`),
  pause: (id: string) => api.post<Upstream>(`${path(id)}/pause`),
  resume: (id: string) => api.post<Upstream>(`${path(id)}/resume`),
  setDefault: (id: string) => api.post<Upstream>(`${path(id)}/default`),
  /** OAuth mailboxes: returns the provider's consent URL to open. */
  reauth: (id: string) => api.post<{ url: string }>(`${path(id)}/reauth`),
  remove: (id: string) => api.del<object>(path(id)),
  /** TODO(#42/#43): start adding a Gmail / Microsoft mailbox; returns the consent URL. */
  startOAuth: (provider: "google" | "microsoft") =>
    api.post<{ url: string }>(`/upstreams/oauth/${provider}/start`),
}
