"use client"

import { useState, type FormEvent } from "react"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { errorText, lastServer, saveCredentials } from "@/lib/credentials"
import { connect, type Credentials, type Session } from "@/lib/jmap"

/** Same main entry as the desktop: server + mailbox + app password (Basic auth). */
export function LoginForm({ onSession }: { onSession: (session: Session, credentials: Credentials) => void }) {
  const [server, setServer] = useState(lastServer)
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [remember, setRemember] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")

  async function submit(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError("")
    const credentials = { server: server.trim(), username: username.trim(), password }
    try {
      const session = await connect(credentials)
      saveCredentials(credentials, remember)
      onSession(session, credentials)
    } catch (err) {
      setError(errorText(err))
    } finally {
      setBusy(false)
    }
  }

  return <main className="mx-auto flex min-h-svh w-full max-w-sm flex-col justify-center px-4 py-10">
    <div className="mb-8 flex items-center gap-3">
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src="/images/icon-192.png" alt="" className="size-10" />
      <div><h1 className="text-xl font-semibold">CZL 笔记</h1><p className="text-muted-foreground text-sm">只读查看 CZL Mail 里的笔记</p></div>
    </div>
    <form onSubmit={submit} className="flex flex-col gap-4">
      <div className="grid gap-2"><Label htmlFor="server">服务器</Label><Input id="server" required inputMode="url" autoCapitalize="none" autoCorrect="off" placeholder="mail.example.com" value={server} onChange={(e) => setServer(e.target.value)} /></div>
      <div className="grid gap-2"><Label htmlFor="username">邮箱</Label><Input id="username" required type="email" autoComplete="username" autoCapitalize="none" value={username} onChange={(e) => setUsername(e.target.value)} /></div>
      <div className="grid gap-2"><Label htmlFor="password">应用专用密码</Label><Input id="password" required type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} /></div>
      <label className="flex items-center gap-2 text-sm"><Checkbox checked={remember} onCheckedChange={(value) => setRemember(value === true)} />在这台设备上记住</label>
      {error && <p className="text-destructive text-sm">{error}</p>}
      <Button type="submit" size="lg" disabled={busy}>{busy ? "连接中…" : "登录"}</Button>
      <p className="text-muted-foreground text-xs leading-relaxed">密码只保存在本机浏览器，只发给你填写的邮件服务器。建议在服务器上单独签发一个应用专用密码给它用。</p>
    </form>
  </main>
}
