import { api } from "./client"
import type {
  AppPassword,
  AuditPage,
  CreatedAppPassword,
  Overview,
  Role,
  Setting,
  SystemInfo,
  TaskRun,
  TaskStatus,
  TrendRange,
  User,
} from "@/lib/types"

export const appPasswordsApi = {
  list: () => api.get<AppPassword[]>("/app-passwords"),
  create: (name: string) =>
    api.post<CreatedAppPassword>("/app-passwords", { name }),
  revoke: (id: string) =>
    api.del<object>(`/app-passwords/${encodeURIComponent(id)}`),
}

export interface UserInput {
  email?: string
  name?: string
  password?: string
  role?: Role
  disabled?: boolean
}

export const usersApi = {
  list: () => api.get<User[]>("/users"),
  create: (body: UserInput) => api.post<User>("/users", body),
  update: (id: string, body: UserInput) =>
    api.patch<User>(`/users/${encodeURIComponent(id)}`, body),
  remove: (id: string) => api.del<object>(`/users/${encodeURIComponent(id)}`),
}

export const settingsApi = {
  list: () => api.get<Setting[]>("/settings"),
  update: (changes: Record<string, unknown>) =>
    api.patch<Setting[]>("/settings", changes),
}

export const auditApi = {
  list: (cursor = "", limit = 50) =>
    api.get<AuditPage>(
      `/audit-logs?limit=${limit}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`
    ),
}

export const overviewApi = {
  get: (range: TrendRange, scope?: "mine" | "all") =>
    api.get<Overview>(
      `/overview?range=${range}${scope ? `&scope=${scope}` : ""}`
    ),
}

export const systemApi = {
  info: () => api.get<SystemInfo>("/system"),
  confirmMasterKeyBackup: () =>
    api.post<{ backupConfirmedAt: string }>("/system/master-key-backup"),
  tasks: () => api.get<TaskStatus[]>("/system/tasks"),
  runTask: (name: string) =>
    api.post<TaskRun>(`/system/tasks/${encodeURIComponent(name)}/run`),
}
