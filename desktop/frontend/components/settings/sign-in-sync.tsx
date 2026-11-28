"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useState } from "react";
import { KeyRound, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Events, api, errorMessage, onEvent, type SignInSyncStatus } from "@/lib/api";

type Mode = "create" | "unlock" | "change" | "reset";

const MIN_LENGTH = 8;

/**
 * 设置 → 账户登录列表的最后一行: 附加登录的跨设备同步。
 * 密码用同步口令加密后存进主登录的个人网盘, 每台设备输入一次口令; 主登录没有网盘时不显示。
 */
export function SignInSyncRow({
  active,
  onChanged,
  onStatus,
}: {
  active: boolean;
  /** 开启或输入口令后附加登录可能已加上, 让登录列表重新读。 */
  onChanged: () => void;
  onStatus: (state: string) => void;
}) {
  useI18n();
  const [status, setStatus] = useState<SignInSyncStatus | null>(null);
  const [mode, setMode] = useState<Mode | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    api
      .getSignInSync()
      .then((s) => {
        setStatus(s);
        onStatus(s.state);
      })
      .catch(() => {});
  }, [onStatus]);

  useEffect(() => {
    if (!active) return;
    load();
    return onEvent(Events.accountsChanged, load);
  }, [active, load]);

  // 刚登录时网盘还在同步: 等网盘有变化再读一次 (读状态要下载同步文件, 平时不跟着网盘变化刷新)。
  const loading = status?.state === "loading";
  useEffect(() => {
    if (!active || !loading) return;
    return onEvent(Events.pimChanged, load);
  }, [active, loading, load]);

  if (!status || status.state === "unavailable") return null;

  async function disable() {
    setBusy(true);
    try {
      await api.disableSignInSync();
      toast.success(t("已在这台设备上关闭附加登录同步"));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
      load();
    }
  }

  const pending = status.pending ?? [];
  let desc: string;
  switch (status.state) {
    case "locked":
      desc =
        pending.length > 0
          ? t("网盘里有 {0} 个附加登录：{1}。输入同步口令后自动添加。", pending.length, pending.join(", "))
          : t("其它设备已开启同步，输入同步口令后这里会自动添加那边的附加登录。");
      break;
    case "stale":
      desc = t("同步口令已在其它设备上更改，输入新口令后继续同步。");
      break;
    case "loading":
      desc = t("正在读取个人网盘…");
      break;
    case "damaged":
      desc = t("网盘里的附加登录同步文件已损坏。重设同步口令后重新开始同步，其它设备需要输入新口令。");
      break;
    case "on":
      desc = t(
        "已开启。添加、移除附加登录和更改密码会同步到你的其它设备；浏览器授权的登录在其它设备上需要再授权一次。",
      );
      break;
    default:
      desc = t(
        "开启后，这里的附加登录会自动加到你的其它设备上。密码用同步口令加密后存进个人网盘，每台设备输入一次口令。",
      );
  }

  return (
    <>
      <div className="border-border flex flex-wrap items-center gap-x-4 gap-y-2 border-t px-4 py-3">
        <div className="min-w-0 flex-1 basis-48">
          <p className="flex items-center gap-1.5 text-sm font-medium">
            <KeyRound className="text-muted-foreground size-4 shrink-0" />
            {t("同步附加登录")}
          </p>
          <p className="text-muted-foreground mt-0.5 text-xs leading-relaxed [overflow-wrap:anywhere]">{desc}</p>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          {status.state === "off" && (
            <Button size="sm" variant="outline" onClick={() => setMode("create")}>
              {t("开启")}
            </Button>
          )}
          {(status.state === "locked" || status.state === "stale") && (
            <Button size="sm" onClick={() => setMode("unlock")}>
              {t("输入口令")}
            </Button>
          )}
          {status.state === "damaged" && (
            <Button size="sm" variant="outline" onClick={() => setMode("reset")}>
              {t("重设")}
            </Button>
          )}
          {status.state === "on" && (
            <>
              <Button size="sm" variant="outline" onClick={() => setMode("change")} disabled={busy}>
                {t("更改口令")}
              </Button>
              <Button size="sm" variant="outline" onClick={disable} disabled={busy}>
                {busy && <Loader2 className="size-4 animate-spin" />}
                {t("关闭")}
              </Button>
            </>
          )}
        </div>
      </div>
      <PassphraseDialog
        mode={mode}
        onMode={setMode}
        onDone={() => {
          setMode(null);
          load();
          onChanged();
        }}
      />
    </>
  );
}

function PassphraseDialog({
  mode,
  onMode,
  onDone,
}: {
  mode: Mode | null;
  onMode: (mode: Mode | null) => void;
  onDone: () => void;
}) {
  useI18n();
  const [passphrase, setPassphrase] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const needsConfirm = mode !== null && mode !== "unlock";

  /** 换模式 (含关闭) 时清空输入: 对话框下次打开或改成重设时从空白开始。 */
  function switchMode(next: Mode | null) {
    setPassphrase("");
    setConfirm("");
    setError("");
    onMode(next);
  }

  async function submit() {
    if (!mode) return;
    if ([...passphrase].length < MIN_LENGTH) {
      setError(t("同步口令至少要 8 个字符。"));
      return;
    }
    if (needsConfirm && passphrase !== confirm) {
      setError(t("两次输入的口令不一致。"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (mode === "create" || mode === "unlock") {
        await api.enableSignInSync(passphrase);
        toast.success(mode === "create" ? t("已开启附加登录同步") : t("已开始同步附加登录"));
      } else {
        await api.resetSignInSync(passphrase);
        toast.success(t("同步口令已更新，其它设备需要输入一次新口令"));
      }
      switchMode(null);
      onDone();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const title = {
    create: t("设置同步口令"),
    unlock: t("输入同步口令"),
    change: t("更改同步口令"),
    reset: t("重设同步口令"),
  };
  const description = {
    create: t("其它设备用这个口令解密同步的密码。口令只存在你的设备上，不上传，忘了只能重设。"),
    unlock: t("输入在其它设备上设置的同步口令。"),
    change: t("设置新口令后，其它设备需要输入一次新口令。"),
    reset: t(
      "重设后其它设备需要输入新口令。只存在其它设备上的密码无法恢复，那些附加登录要在那台设备上输入新口令后才会同步过来。",
    ),
  };

  return (
    <Dialog open={mode !== null} onOpenChange={(open) => !open && !busy && switchMode(null)}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{mode && title[mode]}</DialogTitle>
          <DialogDescription>{mode && description[mode]}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <Input
            type="password"
            autoFocus
            autoComplete={mode === "unlock" ? "current-password" : "new-password"}
            value={passphrase}
            onChange={(e) => setPassphrase(e.target.value)}
            placeholder={mode === "change" || mode === "reset" ? t("新口令") : t("同步口令")}
            aria-label={t("同步口令")}
          />
          {needsConfirm && (
            <Input
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder={t("再输入一次")}
              aria-label={t("再输入一次")}
            />
          )}
          {error && <p className="text-destructive text-xs leading-relaxed">{error}</p>}
          <DialogFooter>
            {mode === "unlock" && (
              <Button type="button" variant="ghost" className="sm:mr-auto" disabled={busy} onClick={() => switchMode("reset")}>
                {t("忘记口令？")}
              </Button>
            )}
            <Button type="button" variant="outline" disabled={busy} onClick={() => switchMode(null)}>
              {t("取消")}
            </Button>
            <Button type="submit" disabled={busy || passphrase === ""}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              {mode === "create" ? t("开启") : t("确定")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
