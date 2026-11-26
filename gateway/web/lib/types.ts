// Shapes of the gateway admin API. Field names follow the Go JSON tags.

export type Role = "owner" | "admin" | "user"

export interface User {
  id: string
  email: string
  name: string
  role: Role
  disabled: boolean
  lastLoginAt: string | null
  createdAt: string
}

export interface AppPassword {
  id: string
  name: string
  prefix: string
  createdAt: string
  lastUsedAt: string | null
  lastUsedIp: string
}

export interface CreatedAppPassword extends AppPassword {
  password: string
}

export interface Setting {
  key: string
  /** secret: the value is "********" when set; writing "" keeps it, null clears it. */
  kind: "bool" | "string" | "int" | "secret"
  value: unknown
  default: unknown
}

export interface AuditEntry {
  id: string
  actorId: string
  actorEmail: string
  method: string
  path: string
  params: unknown
  status: number
  code: number
  msg: string
  ip: string
  traceId: string
  createdAt: string
}

export interface AuditPage {
  items: AuditEntry[]
  nextCursor: string
}

export type UpstreamStatus =
  "ok" | "syncing" | "authRequired" | "unreachable" | "paused"
export type UpstreamKind = "imap" | "gmail" | "microsoft"
export type WatchMode = "notify" | "idle" | "push" | "poll" | ""

export interface UpstreamCard {
  id: string
  kind: UpstreamKind
  name: string
  email: string
  ownerEmail: string
  status: UpstreamStatus
  statusSince: string | null
  code: number
  watchMode: WatchMode
  /** undefined: not known by this API (the row is hidden); null: never synced. */
  lastSyncAt?: string | null
  initialSync: { done: number; total: number } | null
  /** detail is the mail server's own reply, shown to the mailbox owner on hover. */
  lastError: { code: number; msg: string; at: string; detail?: string } | null
  proxy: { mode: "default" | "custom"; host: string } | null
}

export interface TrendPoint {
  t: string
  requests: number
  rateLimited: number
  pushCount: number
  pushP50Ms: number | null
  pushP95Ms: number | null
}

export interface TrendTotals {
  requests: number
  rateLimited: number
  pushCount: number
  pushP50Ms: number | null
  pushP95Ms: number | null
}

export interface Trend {
  range: TrendRange
  from: string
  to: string
  total: TrendPoint[]
  series: { accountId: string; totals: TrendTotals; points: TrendPoint[] }[]
  summary: TrendTotals
  previous: TrendTotals
}

export type TrendRange = "24h" | "7d" | "30d"

export type TodoKind = "reauth" | "oauthSecretExpiring" | "masterKeyBackup"

export interface Todo {
  kind: TodoKind
  severity: "danger" | "warning"
  upstreamId?: string
  name?: string
  dueAt?: string
}

export interface Overview {
  generatedAt: string
  scope: "mine" | "all"
  upstreams: UpstreamCard[]
  statusCounts: Partial<Record<UpstreamStatus, number>>
  onlineClients: number
  todos: Todo[]
  trend: Trend
}

export interface SystemInfo {
  version: string
  goVersion: string
  startedAt: string
  baseUrl: string
  timeZone: string
  database: {
    ok: boolean
    mainBytes: number
    upstreamBytes: number
    error?: string
  }
  masterKey: { version: number; backupConfirmedAt: string | null }
  onlineClients: number
  adminWeb: boolean
  env: { name: string; value: string; secret: boolean }[]
}

export interface TaskRun {
  startedAt: string
  finishedAt: string
  ok: boolean
  result: string
  error: string
  manual: boolean
}

export interface TaskStatus {
  name: string
  intervalSeconds: number
  running: boolean
  nextRunAt: string | null
  lastRun: TaskRun | null
  history: TaskRun[]
}
