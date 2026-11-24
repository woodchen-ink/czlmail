"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useState } from "react";
import {
  AlertTriangle,
  Loader2,
  LogIn,
  LogOut,
  Plus,
  RefreshCw,
  Server,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { SignIn } from "@/components/sign-in";
import { Card } from "@/components/settings/ui";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Account,
  type SessionStatus,
  type SignInInfo,
} from "@/lib/api";

/**
 * 设置 → 账户里的登录列表: 每个登录一张卡, 可重试连接、重新登录、退出、移除; 底下添加新的登录。
 * 退出一个登录只停它自己的同步、删它的凭据, 缓存留着, 其余登录照常; 移除才删缓存。
 */
export function SignInsCard({
  active,
  accounts,
  onSignOut,
}: {
  active: boolean;
  accounts: Account[];
  /** 只有主登录时的「退出登录」(回到登录页), 由外面弹确认框。 */
  onSignOut: () => void;
}) {
  useI18n();
  const [list, setList] = useState<SignInInfo[]>([]);
  const [form, setForm] = useState<
    null | { kind: "add" } | { kind: "again"; info: SignInInfo }
  >(null);
  const [removing, setRemoving] = useState<SignInInfo | null>(null);
  const [signingOut, setSigningOut] = useState<SignInInfo | null>(null);
  const [busy, setBusy] = useState("");

  const load = useCallback(() => {
    api
      .listSignIns()
      .then((l) => setList(l ?? []))
      .catch(() => {});
  }, []);

  useEffect(() => {
    if (!active) return;
    load();
    const offs = [
      Events.connected,
      Events.signInRequired,
      Events.accountsChanged,
      Events.mailChanged,
    ].map((e) => onEvent(e, load));
    // 恢复中的登录连上或放弃重试时没有单独的事件, 打开设置页时隔一会儿再刷一次状态。
    const id = setInterval(load, 5000);
    return () => {
      offs.forEach((off) => off());
      clearInterval(id);
    };
  }, [active, load]);

  const names = new Map(accounts.map((a) => [a.id, a.name]));
  const others = list.filter((s) => !s.primary).length;

  async function retry(s: SignInInfo) {
    setBusy(s.id);
    try {
      await api.retrySignIn(s.id);
      toast.success(t("已重新连接"));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
      load();
    }
  }

  async function signOut(s: SignInInfo) {
    setBusy(s.id);
    try {
      await api.signOutOf(s.id);
      toast.success(t("已退出 {0}", s.username));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
      setSigningOut(null);
      load();
    }
  }

  async function remove(s: SignInInfo) {
    setBusy(s.id);
    try {
      await api.removeSignIn(s.id);
      toast.success(t("已移除 {0}", s.username));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
      setRemoving(null);
      load();
    }
  }

  return (
    <>
      <Card className="gap-0 p-0">
        {list.map((s, i) => (
          <div
            key={s.id || "primary"}
            className={cn(
              "flex flex-col gap-2 px-4 py-3",
              i > 0 && "border-border border-t",
            )}
          >
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
              <div className="min-w-0 flex-1 basis-48">
                <p className="flex items-center gap-1.5 text-sm font-medium">
                  {s.signInRequired && (
                    <AlertTriangle className="text-destructive size-4 shrink-0" />
                  )}
                  <span className="truncate">{s.username}</span>
                  {s.primary && list.length > 1 && (
                    <span className="text-muted-foreground shrink-0 text-xs font-normal">
                      {t("主登录")}
                    </span>
                  )}
                </p>
                <p className="text-muted-foreground mt-0.5 flex items-start gap-1 text-xs leading-relaxed">
                  <Server className="mt-0.5 size-3 shrink-0" />
                  <span className="min-w-0 [overflow-wrap:anywhere]">
                    {t("服务器 {0} · {1}", s.server, statusText(s))}
                  </span>
                </p>
              </div>
              <div className="flex shrink-0 flex-wrap items-center gap-2">
                {!s.connected && !s.restoring && !s.signedOut && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => retry(s)}
                    disabled={busy !== ""}
                  >
                    {busy === s.id ? (
                      <Loader2 className="size-4 animate-spin" />
                    ) : (
                      <RefreshCw className="size-4" />
                    )}
                    {t("重试连接")}
                  </Button>
                )}
                {(s.signInRequired || s.signedOut) && (
                  <Button
                    size="sm"
                    onClick={() => setForm({ kind: "again", info: s })}
                    disabled={busy !== ""}
                  >
                    <LogIn className="size-4" />
                    {s.signedOut ? t("登录") : t("重新登录")}
                  </Button>
                )}
                {!s.signedOut && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      s.primary && others === 0 ? onSignOut() : setSigningOut(s)
                    }
                    disabled={busy !== ""}
                  >
                    <LogOut className="size-4" />
                    {t("退出登录")}
                  </Button>
                )}
                {!s.primary && (
                  <Button
                    variant="outline"
                    size="sm"
                    className="text-destructive hover:text-destructive"
                    onClick={() => setRemoving(s)}
                    disabled={busy !== ""}
                  >
                    <Trash2 className="size-4" />
                    {t("移除")}
                  </Button>
                )}
              </div>
            </div>
            {s.signInRequired && (
              <p className="text-destructive text-xs leading-relaxed">
                {t("登录已失效，同步已暂停：{0}", errorMessage(s.signInReason))}
              </p>
            )}
            {s.accounts.length > 1 && (
              <p className="text-muted-foreground text-xs leading-relaxed">
                {t(
                  "可访问的邮箱：{0}",
                  s.accounts.map((id) => names.get(id) ?? id).join(", "),
                )}
              </p>
            )}
          </div>
        ))}
        <div
          className={cn(
            "px-4 py-3",
            list.length > 0 && "border-border border-t",
          )}
        >
          <Button
            variant="outline"
            size="sm"
            onClick={() => setForm({ kind: "add" })}
          >
            <Plus className="size-4" />
            {t("添加登录")}
          </Button>
          <p className="text-muted-foreground mt-2 text-xs leading-relaxed">
            {t(
              "同时使用另一台 JMAP 服务器或同一台服务器上的另一个用户。各个登录的邮件都会出现在「全部账号」里。",
            )}
          </p>
        </div>
      </Card>

      <Dialog
        open={form !== null}
        onOpenChange={(open) => !open && setForm(null)}
      >
        <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {form?.kind === "again" ? t("重新登录") : t("添加登录")}
            </DialogTitle>
            <DialogDescription>
              {form?.kind !== "again"
                ? t("连接你的 JMAP 邮箱服务器")
                : form.info.signedOut
                  ? t(
                      "登录 {0} 后继续同步，已缓存的邮件无需重新下载。",
                      form.info.username,
                    )
                  : t(
                      "{0} 的登录已失效。重新登录后继续同步，已缓存的邮件无需重新下载。",
                      form.info.username,
                    )}
            </DialogDescription>
          </DialogHeader>
          {form && (
            <SignIn
              key={form.kind === "again" ? form.info.id : "add"}
              target={
                form.kind === "again"
                  ? { kind: "again", id: form.info.id }
                  : { kind: "add" }
              }
              reauth={form.kind === "again" ? reauthOf(form.info) : undefined}
              onSignedIn={() => {
                toast.success(
                  form.kind === "again" ? t("已重新连接") : t("已添加登录"),
                );
                setForm(null);
                load();
              }}
            />
          )}
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={signingOut !== null}
        onOpenChange={(open) => !open && busy === "" && setSigningOut(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("退出登录")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "{0} 会退出登录：停止同步并删除本机保存的登录凭据。这个登录的邮件缓存保留，其余登录照常同步，以后可以重新登录。",
                signingOut?.username ?? "",
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy !== ""}>
              {t("取消")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={busy !== ""}
              onClick={(e) => {
                e.preventDefault();
                if (signingOut) signOut(signingOut);
              }}
            >
              {busy !== "" && <Loader2 className="size-4 animate-spin" />}
              {t("退出登录")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog
        open={removing !== null}
        onOpenChange={(open) => !open && busy === "" && setRemoving(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("移除登录")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "{0} 会从本机移除：停止同步，删除本机保存的登录凭据和这个登录的邮件缓存。服务器上的邮件不受影响，以后可以重新添加。",
                removing?.username ?? "",
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy !== ""}>
              {t("取消")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={busy !== ""}
              onClick={(e) => {
                e.preventDefault();
                if (removing) remove(removing);
              }}
            >
              {busy !== "" && <Loader2 className="size-4 animate-spin" />}
              {t("移除")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function statusText(s: SignInInfo): string {
  if (s.signedOut) return t("已退出登录");
  if (s.signInRequired) return t("需要重新登录");
  if (s.connected) return t("已连接");
  if (s.restoring) return t("正在连接");
  return t("离线");
}

/** 重新登录时预填原来的服务器、邮箱与登录方式。 */
function reauthOf(s: SignInInfo): SessionStatus {
  return {
    configured: true,
    connected: s.connected,
    username: s.username,
    server: s.server,
    authMethod: s.authMethod,
    signInRequired: s.signInRequired,
    signInReason: s.signInReason,
    signedOut: s.signedOut,
  } as SessionStatus;
}
