import { api } from "./client"
import type { User } from "@/lib/types"

export const authApi = {
  setupStatus: () =>
    api.get<{ required: boolean; registrationOpen: boolean }>("/setup"),
  setup: (body: {
    token: string
    email: string
    name: string
    password: string
  }) => api.post<User>("/setup", body),
  login: (body: { email: string; password: string }) =>
    api.post<User>("/login", body),
  register: (body: { email: string; name: string; password: string }) =>
    api.post<User>("/register", body),
  logout: () => api.post<object>("/logout"),
  me: () => api.get<User>("/me"),
  changePassword: (body: { currentPassword: string; newPassword: string }) =>
    api.put<object>("/me/password", body),
}
