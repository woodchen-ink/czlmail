// Query keys, three parts: [domain, resource, params].
import type { TrendRange } from "@/lib/types"

export const qk = {
  me: ["auth", "me", null] as const,
  setup: ["auth", "setup", null] as const,
  overview: (range: TrendRange, scope?: string) =>
    ["overview", "dashboard", { range, scope }] as const,
  upstreams: ["mail", "upstreams", null] as const,
  appPasswords: ["mail", "app-passwords", null] as const,
  users: ["admin", "users", null] as const,
  oauthApps: ["admin", "oauth-apps", null] as const,
  settings: ["admin", "settings", null] as const,
  system: ["system", "info", null] as const,
  tasks: ["system", "tasks", null] as const,
  audit: ["system", "audit", null] as const,
}
