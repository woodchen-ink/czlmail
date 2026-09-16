"use client";

import { useState } from "react";
import { ExternalLink, Loader2, LogIn, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, errorMessage } from "@/lib/api";

interface Props {
  onSignedIn: () => void;
}

type Mode = "password" | "oauth";

export function SignIn({ onSignedIn }: Props) {
  const [mode, setMode] = useState<Mode>("password");
  const [server, setServer] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const canSubmit =
    mode === "password"
      ? server.trim() !== "" && email.trim() !== "" && password !== ""
      : server.trim() !== "";

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy || !canSubmit) return;

    setBusy(true);
    setError("");
    try {
      if (mode === "password") {
        await api.signInWithPassword(server.trim(), email.trim(), password);
      } else {
        await api.signIn(server.trim());
      }
      onSignedIn();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  // 只有浏览器授权会长时间挂起等用户操作，才需要取消出口。
  async function cancel() {
    await api.cancelSignIn();
  }

  function switchMode(next: Mode) {
    if (busy) return;
    setMode(next);
    setError("");
  }

  return (
    <div className="flex h-full items-center justify-center overflow-y-auto p-6">
      <div className="w-full max-w-sm py-8">
        <h1 className="from-brand-from to-brand-to bg-gradient-to-r bg-clip-text text-center text-2xl font-semibold text-transparent">
          CZL Mail
        </h1>
        <p className="text-muted-foreground mt-2 text-center text-sm">
          连接你的 JMAP 邮箱服务器
        </p>

        <form onSubmit={submit} className="mt-8 flex flex-col gap-4">
          <Field label="服务器" htmlFor="server">
            <Input
              id="server"
              value={server}
              onChange={(e) => setServer(e.target.value)}
              placeholder="mail.example.net"
              autoComplete="url"
              autoFocus
              disabled={busy}
            />
          </Field>

          {mode === "password" && (
            <>
              <Field label="邮箱" htmlFor="email">
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="you@example.net"
                  autoComplete="username"
                  disabled={busy}
                />
              </Field>

              <Field
                label="应用专用密码"
                htmlFor="password"
                hint="在邮箱网页端的设置里创建。账号使用单点登录（SSO）时，这里不能填 SSO 的密码。"
              >
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  autoComplete="current-password"
                  disabled={busy}
                />
              </Field>
            </>
          )}

          {busy && mode === "oauth" ? (
            <div className="flex flex-col gap-2">
              <div className="border-border bg-secondary flex items-center gap-2 rounded-md border px-3 py-2.5 text-sm">
                <Loader2 className="size-4 shrink-0 animate-spin" />
                <span className="flex-1">请在浏览器中完成授权</span>
              </div>
              <Button type="button" variant="ghost" size="sm" onClick={cancel}>
                <X className="size-4" />
                取消
              </Button>
            </div>
          ) : (
            <Button type="submit" disabled={busy || !canSubmit}>
              {busy ? (
                <Loader2 className="size-4 animate-spin" />
              ) : mode === "password" ? (
                <LogIn className="size-4" />
              ) : (
                <ExternalLink className="size-4" />
              )}
              {mode === "password" ? "登录" : "在浏览器中登录"}
            </Button>
          )}

          {error && (
            <p className="text-destructive text-sm leading-relaxed" role="alert">
              {error}
            </p>
          )}
        </form>

        <div className="mt-6 text-center">
          <button
            type="button"
            onClick={() => switchMode(mode === "password" ? "oauth" : "password")}
            disabled={busy}
            className="text-muted-foreground hover:text-foreground text-xs underline-offset-4 hover:underline"
          >
            {mode === "password" ? "改用浏览器授权登录（OAuth）" : "改用应用专用密码登录"}
          </button>
        </div>
      </div>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
      </label>
      {children}
      {hint && <p className="text-muted-foreground text-xs leading-relaxed">{hint}</p>}
    </div>
  );
}
