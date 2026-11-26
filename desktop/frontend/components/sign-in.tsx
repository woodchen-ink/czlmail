"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useEffect, useState } from "react";
import { ExternalLink, Loader2, LogIn, X } from "lucide-react";

import { LanguageSelect } from "@/components/language-select";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { api, errorMessage, type SessionStatus, type SignInHint } from "@/lib/api";
import { openExternal } from "@/lib/external";

/** 网关的部署说明: Gmail、Outlook 与普通 IMAP 邮箱要先部署 czlmail 网关才能用。 */
const GATEWAY_README_URL = "https://github.com/woodchen-ink/czlmail/blob/main/gateway/README.md";

interface Props {
  onSignedIn: () => void;
  /** 登录失效后重新登录: 预填原来的服务器、邮箱与登录方式。 */
  reauth?: SessionStatus;
  /** 重新登录时可以不登录、退回去看已缓存的邮件。 */
  onBack?: () => void;
  /**
   * 在设置 → 账户里用: 添加一个登录, 或重新登录某个登录 (id 为登录前缀, 主登录为空串)。
   * 这时只渲染表单本身, 不占满整页, 也不显示语言选择与标题。
   */
  target?: { kind: "add" } | { kind: "again"; id: string };
}

/** 按 target 选登录调用: 不给时是主登录 (首次登录、登录失效后重新登录)。 */
function signInCalls(target: Props["target"]) {
  if (target?.kind === "add")
    return {
      password: (server: string, email: string, pass: string) => api.addSignInWithPassword(server, email, pass),
      oauth: (server: string, idp: string, clientId: string) => api.addSignInOAuth(server, idp, clientId),
    };
  if (target?.kind === "again") {
    const id = target.id;
    return {
      password: (server: string, email: string, pass: string) => api.signInAgainWithPassword(id, server, email, pass),
      oauth: (server: string, idp: string, clientId: string) => api.signInAgainOAuth(id, server, idp, clientId),
    };
  }
  return {
    password: (server: string, email: string, pass: string) => api.signInWithPassword(server, email, pass),
    oauth: (server: string, idp: string, clientId: string) => api.signIn(server, idp, clientId),
  };
}

type Mode = "password" | "oauth";

export function SignIn({ onSignedIn, reauth, onBack, target }: Props) {
  useI18n();
  const calls = signInCalls(target);
  const embedded = !!target;
  const [mode, setMode] = useState<Mode>(reauth?.authMethod === "oauth" ? "oauth" : "password");
  const [server, setServer] = useState(reauth?.server ?? "");
  const [email, setEmail] = useState(reauth?.username ?? "");
  const [password, setPassword] = useState("");
  // 外部身份提供商不是秘密, 记在本机方便下次登录。
  const [idp, setIdp] = useState(() => readPref("idp"));
  const [clientId, setClientId] = useState(() => readPref("clientId"));
  const [sso, setSso] = useState(() => readPref("idp") !== "");

  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // 域名下 /.well-known/czlmail.json 声明的单点登录方式; 有它时直接给出「使用 XX 登录」。
  // 服务器是 czlmail 网关时 hint.gateway 有值: 密码说明换成网关的应用专用密码, 并给出网关后台入口。
  const [hint, setHint] = useState<SignInHint | null>(null);
  const gateway = hint?.gateway;
  const oauthHint = hint && hint.clientId ? hint : null;
  const [hintBusy, setHintBusy] = useState(false);

  useEffect(() => {
    const host = server.trim();
    setHint(null);
    if (!host.includes(".")) return;
    let cancelled = false;
    const t = setTimeout(async () => {
      try {
        const h = await api.discoverSignIn(host);
        if (!cancelled) setHint(h ?? null);
      } catch {
        // 没有自动配置时照常手动登录。
      }
    }, 600);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [server]);

  async function signInWithHint() {
    if (!oauthHint || busy) return;
    setHintBusy(true);
    setBusy(true);
    setError("");
    try {
      await calls.oauth(server.trim(), oauthHint.issuer, oauthHint.clientId);
      onSignedIn();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
      setHintBusy(false);
    }
  }

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
        await calls.password(server.trim(), email.trim(), password);
      } else {
        const useSso = sso && idp.trim() !== "";
        writePref("idp", useSso ? idp.trim() : "");
        writePref("clientId", useSso ? clientId.trim() : "");
        await calls.oauth(server.trim(), useSso ? idp.trim() : "", useSso ? clientId.trim() : "");
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

  const form = (
    <>
      <form onSubmit={submit} className={embedded ? "flex flex-col gap-4" : "mt-8 flex flex-col gap-4"}>
        <Field label={t("服务器")} htmlFor="server">
          <Input
            id="server"
            value={server}
            onChange={(e) => setServer(e.target.value)}
            placeholder="mail.example.net"
            autoComplete="url"
            autoFocus={!reauth && target?.kind !== "again"}
            disabled={busy}
          />
        </Field>

        {oauthHint && (
          <div className="flex flex-col gap-2">
            {hintBusy ? (
              <div className="flex flex-col gap-2">
                <div className="border-border bg-secondary flex items-center gap-2 rounded-md border px-3 py-2.5 text-sm">
                  <Loader2 className="size-4 shrink-0 animate-spin" />
                  <span className="flex-1">{t("请在浏览器中完成 {0} 登录", oauthHint.name)}</span>
                </div>
                <Button type="button" variant="ghost" size="sm" onClick={cancel}>
                  <X className="size-4" />
                  {t("取消")}</Button>
              </div>
            ) : (
              <Button type="button" onClick={signInWithHint} disabled={busy}>
                <ExternalLink className="size-4" />
                {t("使用 {0} 登录", oauthHint.name)}</Button>
            )}
            <p className="text-muted-foreground text-center text-xs">{t("或者使用应用专用密码登录")}</p>
          </div>
        )}

        {mode === "password" && (
          <>
            <Field label={t("邮箱")} htmlFor="email">
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
              label={t("应用专用密码")}
              htmlFor="password"
              hint={
                gateway
                  ? t("网关后台生成的应用专用密码，不是 Gmail 等邮箱的密码。")
                  : t("在邮箱网页端的设置里创建。账号使用单点登录（SSO）时，这里不能填 SSO 的密码。")
              }
              action={
                gateway?.adminUrl ? (
                  <LinkButton onClick={() => openExternal(gateway.adminUrl)}>{t("打开网关后台")}</LinkButton>
                ) : undefined
              }
            >
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                autoFocus={!!reauth || target?.kind === "again"}
                disabled={busy}
              />
            </Field>
          </>
        )}

        {mode === "oauth" && (
          <div className="flex flex-col gap-3">
            <label className="flex items-center gap-2 text-sm">
              <Checkbox checked={sso} onCheckedChange={(v) => setSso(v === true)} disabled={busy} />
              {t("通过单点登录（外部身份提供商）")}</label>
            {sso && (
              <>
                <Field
                  label={t("身份提供商地址")}
                  htmlFor="idp"
                  hint={t("邮件服务器把登录交给外部身份提供商（如 Stalwart 配置了 OIDC 目录）时填写，例如 connect.example.net。")}
                >
                  <Input id="idp" value={idp} onChange={(e) => setIdp(e.target.value)} placeholder="connect.example.net" disabled={busy} />
                </Field>
                <Field
                  label={t("客户端 ID")}
                  htmlFor="client-id"
                  hint={t("由管理员在身份提供商上创建的公开客户端（无密钥、PKCE），回调地址需包含 http://127.0.0.1:47821/callback。")}
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
              <span className="flex-1">{t("请在浏览器中完成授权")}</span>
            </div>
            <Button type="button" variant="ghost" size="sm" onClick={cancel}>
              <X className="size-4" />
              {t("取消")}</Button>
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
            {mode === "password" ? t("登录") : t("在浏览器中登录")}
          </Button>
        )}

        {error && (
          <div className="flex flex-col items-start gap-1" role="alert">
            <p className="text-destructive text-sm leading-relaxed">{errorMessage(error)}</p>
            {/\b2061\b/.test(error) && (
              <LinkButton onClick={() => openExternal(GATEWAY_README_URL)}>{t("查看 czlmail 网关部署说明")}</LinkButton>
            )}
            {/\b2066\b/.test(error) && gateway?.adminUrl && (
              <LinkButton onClick={() => openExternal(gateway.adminUrl)}>{t("打开网关后台")}</LinkButton>
            )}
          </div>
        )}
      </form>

      <div className="mt-6 text-center">
        <button
          type="button"
          onClick={() => switchMode(mode === "password" ? "oauth" : "password")}
          disabled={busy}
          className="text-muted-foreground hover:text-foreground text-xs underline-offset-4 hover:underline"
        >
          {mode === "password" ? t("改用浏览器授权登录（OAuth）") : t("改用应用专用密码登录")}
        </button>
        {onBack && (
          <button
            type="button"
            onClick={onBack}
            disabled={busy}
            className="text-muted-foreground hover:text-foreground mt-3 block w-full text-xs underline-offset-4 hover:underline"
          >
            {t("暂不登录，返回查看已缓存的邮件")}
          </button>
        )}
      </div>
    </>
  );

  if (embedded) return form;

  return (
    <div className="flex h-full items-center justify-center overflow-y-auto p-6">
      <div className="w-full max-w-sm py-8">
        <div className="mb-6 flex justify-end">
          <LanguageSelect />
        </div>
        <h1 className="from-brand-from to-brand-to bg-gradient-to-r bg-clip-text text-center text-2xl font-semibold text-transparent">
          CZL Mail
        </h1>
        <p className="text-muted-foreground mt-2 text-center text-sm">
          {reauth ? t("登录已失效，请重新登录以继续同步") : t("连接你的 JMAP 邮箱服务器")}</p>
        {form}
      </div>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  action,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  /** 跟在说明后面的一个链接 (如「打开网关后台」)。 */
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  useI18n();
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-sm font-medium">
        {label}
      </label>
      {children}
      {hint && <p className="text-muted-foreground text-xs leading-relaxed">{hint}</p>}
      {action}
    </div>
  );
}

/** 表单里的文字链接: 交给系统浏览器打开外部页面。 */
function LinkButton({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="text-muted-foreground hover:text-foreground inline-flex items-center gap-1 self-start text-xs underline-offset-4 hover:underline"
    >
      <ExternalLink className="size-3" />
      {children}
    </button>
  );
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
