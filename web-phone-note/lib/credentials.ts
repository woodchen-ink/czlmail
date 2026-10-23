import { JmapError, type Credentials } from "@/lib/jmap"

/**
 * The app password lives only in this browser: localStorage when "remember me" is on,
 * otherwise sessionStorage (gone when the tab closes). It is never sent anywhere but the JMAP server.
 */
const KEY = "czl-notes.credentials"
const LAST_SERVER = "czl-notes.server"
const VAULT = "czl-notes.vault"

function safe<T>(read: () => T, fallback: T): T {
  try { return read() } catch { return fallback }
}

export function loadCredentials(): Credentials | null {
  const raw = safe(() => sessionStorage.getItem(KEY) ?? localStorage.getItem(KEY), null)
  if (!raw) return null
  try {
    const value = JSON.parse(raw) as Partial<Credentials>
    return value.server && value.username && value.password ? (value as Credentials) : null
  } catch { return null }
}

export function saveCredentials(value: Credentials, remember: boolean) {
  safe(() => {
    (remember ? localStorage : sessionStorage).setItem(KEY, JSON.stringify(value))
    localStorage.setItem(LAST_SERVER, value.server)
  }, undefined)
}

export function forgetCredentials() {
  safe(() => { sessionStorage.removeItem(KEY); localStorage.removeItem(KEY) }, undefined)
}

export const lastServer = () => safe(() => localStorage.getItem(LAST_SERVER) ?? "", "")
export const savedVault = () => safe(() => localStorage.getItem(VAULT) ?? "", "")
export const saveVault = (id: string) => safe(() => localStorage.setItem(VAULT, id), undefined)

const MESSAGES: Record<number, string> = {
  3001: "邮箱或应用专用密码不对",
  3002: "这个地址不是 JMAP 邮件服务器",
  3003: "连不上服务器，检查地址和网络",
  3004: "这个账号没有开通文件存储，笔记存在那里",
  3005: "服务器请求失败，稍后再试",
}

/** Human text plus the numeric code, so a screenshot is enough to diagnose. */
export function errorText(error: unknown): string {
  if (error instanceof JmapError) return `${MESSAGES[error.code] ?? "请求失败"}（${error.code}）`
  return "出了点问题，刷新后再试（3000）"
}
