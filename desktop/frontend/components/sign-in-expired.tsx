"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Events, api, errorMessage, onEvent, type SessionStatus } from "@/lib/api";
import { send } from "@/lib/app-bus";

const TOAST_ID = "sign-in-required";

// 用户点过「稍后」或去过登录页又返回: 外壳重新挂载时不再弹窗打断, 只留常驻提示。
// 重新连上后清掉。
let snoozed = false;

/**
 * 登录失效提示。后端确认服务器拒绝了保存的凭据后（断网不算）同步会停下，
 * 这里弹窗说明并给出重新登录 / 重试；选「稍后」则留一条常驻提示，缓存的邮件照常可看。
 */
export function SignInExpired({ onReauth }: { onReauth: (status: SessionStatus) => void }) {
  useI18n();
  const [status, setStatus] = useState<SessionStatus | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [retryError, setRetryError] = useState("");

  const remind = useCallback(() => {
    toast.warning(t("登录已失效，同步已暂停"), {
      id: TOAST_ID,
      duration: Infinity,
      action: { label: t("查看"), onClick: () => setOpen(true) },
    });
  }, []);

  useEffect(() => {
    let cancelled = false;
    const check = async (fresh: boolean) => {
      try {
        const s = await api.getSessionStatus();
        if (cancelled || !s.signInRequired) return;
        setStatus(s);
        setRetryError("");
        // 新发生的失效总要弹窗; 挂载时发现的旧状态尊重用户之前的「稍后」。
        if (fresh) snoozed = false;
        if (snoozed) remind();
        else setOpen(true);
      } catch {
        // 取不到状态时不打扰。
      }
    };
    check(false);
    const offRequired = onEvent(Events.signInRequired, () => check(true));
    const offConnected = onEvent(Events.connected, async () => {
      // 连上的可能是别的登录 (#15): 主登录仍失效时提示留着。
      const s = await api.getSessionStatus().catch(() => null);
      if (cancelled || s?.signInRequired) return;
      snoozed = false;
      setOpen(false);
      setStatus(null);
      toast.dismiss(TOAST_ID);
    });
    return () => {
      cancelled = true;
      offRequired();
      offConnected();
      toast.dismiss(TOAST_ID);
    };
  }, [remind]);

  // 其余登录 (#15) 失效时不弹窗打断: 主登录的邮件照常收发, 留一条常驻提示, 点了去设置 → 账户重新登录。
  useEffect(() => {
    let cancelled = false;
    const shown = new Set<string>();
    const check = async () => {
      try {
        const list = (await api.listSignIns()) ?? [];
        if (cancelled) return;
        const now = new Set<string>();
        for (const s of list) {
          if (s.primary || !s.signInRequired) continue;
          now.add(s.id);
          toast.warning(t("{0} 的登录已失效，同步已暂停", s.username), {
            id: TOAST_ID + ":" + s.id,
            duration: Infinity,
            action: {
              label: t("处理"),
              onClick: () => {
                send("navigate", { module: "settings" });
                // 设置模块可能刚挂载, 等它订阅后再发。
                setTimeout(() => send("openSettings", { section: "account" }), 0);
              },
            },
          });
        }
        for (const id of shown) if (!now.has(id)) toast.dismiss(TOAST_ID + ":" + id);
        shown.clear();
        now.forEach((id) => shown.add(id));
      } catch {
        // 取不到状态时不打扰。
      }
    };
    check();
    const offs = [Events.signInRequired, Events.connected, Events.accountsChanged].map((e) => onEvent(e, check));
    return () => {
      cancelled = true;
      offs.forEach((off) => off());
      shown.forEach((id) => toast.dismiss(TOAST_ID + ":" + id));
    };
  }, []);

  function later() {
    snoozed = true;
    setOpen(false);
    remind();
  }

  async function retry() {
    setBusy(true);
    setRetryError("");
    try {
      // 成功后由 connected 事件收起弹窗。
      await api.retrySession();
      toast.success(t("已重新连接"));
    } catch (err) {
      setRetryError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!status) return null;

  return (
    <AlertDialog open={open} onOpenChange={(next) => (next ? setOpen(true) : later())}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("登录已失效")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t("邮件服务器不再接受本机保存的登录凭据，同步已暂停。已缓存的邮件仍可查看，重新登录后继续同步，无需重新下载。")}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {status.username && <p className="truncate text-sm font-medium">{status.username}</p>}
        <p className="text-muted-foreground text-xs leading-relaxed">
          {t("原因：{0}", errorMessage(status.signInReason))}
        </p>
        {retryError && (
          <p className="text-destructive text-xs leading-relaxed" role="alert">
            {t("重试失败：{0}", retryError)}
          </p>
        )}
        <AlertDialogFooter>
          <Button variant="ghost" onClick={later} disabled={busy}>
            {t("稍后")}
          </Button>
          <Button variant="outline" onClick={retry} disabled={busy}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            {t("重试连接")}
          </Button>
          <Button
            disabled={busy}
            onClick={() => {
              snoozed = true;
              onReauth(status);
            }}
          >
            {t("重新登录")}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
