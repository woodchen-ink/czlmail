"use client";

import { useState } from "react";
import { ExternalLink, Loader2, LogIn, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
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
  // 外部身份提供商不是秘密, 记在本机方便下次登录。
  const [idp, setIdp] = useState(() => readPref("idp"));
  const [clientId, setClientId] = useState(() => readPref("clientId"));
  const [sso, setSso] = useState(() => readPref("idp") !== "");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const canSubmit =
    mode === "password"
      ? server.trim() !== "" && email.trim() !== "" && password !== ""
      : server.trim() !== "" && (!sso || (idp.trim() !== "" && clientId.trim() !== ""));

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (busy || !canSubmit) return;

    setBusy(true);
    setError("");
    try {
      if (mode === "password") {
        await api.signInWithPassword(server.trim(), email.trim(), password);
      } else {
        const useSso = sso && idp.trim() !== "";
        writePref("idp", useSso ? idp.trim() : "");
        writePref("clientId", useSso ? clientId.trim() : "");
        await api.signIn(server.trim(), useSso ? idp.trim() : "", useSso ? clientId.trim() : "");
      }
      onSignedIn();
    } catch (err) {
      setError(signInError(errorMessage(err)));
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

          {mode === "oauth" && (
            <div className="flex flex-col gap-3">
              <label className="flex items-center gap-2 text-sm">
                <Checkbox checked={sso} onCheckedChange={(v) => setSso(v === true)} disabled={busy} />
                通过单点登录（外部身份提供商）
              </label>
              {sso && (
                <>
                  <Field
                    label="身份提供商地址"
                    htmlFor="idp"
                    hint="邮件服务器把登录交给外部身份提供商（如 Stalwart 配置了 OIDC 目录）时填写，例如 connect.example.net。"
                  >
                    <Input id="idp" value={idp} onChange={(e) => setIdp(e.target.value)} placeholder="connect.example.net" disabled={busy} />
                  </Field>
                  <Field
                    label="客户端 ID"
                    htmlFor="client-id"
                    hint="由管理员在身份提供商上创建的公开客户端（无密钥、PKCE），回调地址需包含 http://127.0.0.1:47821/callback。"
                  >
                    <Input id="client-id" value={clientId} onChange={(e) => setClientId(e.target.value)} disabled={busy} />
                  </Field>
                </>
              )}
            </div>
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

/** 把登录流程的错误码转成用户能照着处理的说明; 未收录的原样显示, 方便反馈时对照日志。 */
function signInError(msg: string): string {
  const code = msg.slice(0, 4);
  const map: Record<string, string> = {
    "2060": "认证失败：请检查邮箱地址与应用专用密码（单点登录的密码在这里不能用）。",
    "2061": "这个地址上没有找到 JMAP 服务，请确认服务器地址。",
    "2062": "连不上邮件服务器，请检查网络与服务器地址。",
    "2063": "邮件服务器没有正常响应，请稍后重试。",
    "2064": "邮件服务器不接受这次浏览器授权得到的令牌（服务器可能把认证交给了外部身份提供商）。请改用应用专用密码登录。",
    "2052": "单点登录需要同时填写身份提供商地址和客户端 ID。",
    "3001": "这个服务器没有提供浏览器授权登录，请改用应用专用密码。",
    "3021": "浏览器授权超时或已取消。",
    "3022": "换取令牌失败，请重试；多次失败请改用应用专用密码登录。",
    "3023": "授权被拒绝。",
    "3024": "授权状态校验失败，请重新登录。",
  };
  return map[code] ? `${map[code]}（${code}）` : msg;
}

const PREF_PREFIX = "czlmail.signin.";

function readPref(key: string): string {
  try {
    return localStorage.getItem(PREF_PREFIX + key) ?? "";
  } catch {
    return "";
  }
}

function writePref(key: string, value: string) {
  try {
    if (value) localStorage.setItem(PREF_PREFIX + key, value);
    else localStorage.removeItem(PREF_PREFIX + key);
  } catch {
    // 忽略
  }
}
