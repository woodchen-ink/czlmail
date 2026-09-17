"use client";

import { useEffect, useState } from "react";
import { CalendarDays, Check, Loader2, MailX, MapPin } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
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
import { api, errorMessage, type EmailDetail, type Invite, type Unsubscribe } from "@/lib/api";
import { send } from "@/lib/app-bus";
import { parseLocalDateTime, shortDate, hm } from "@/lib/date";

/** 邮件顶部的横幅: 日历邀请与退订。 */
export function EmailBanners({ accountId, email }: { accountId: string; email: EmailDetail }) {
  return (
    <>
      <InviteBanner accountId={accountId} email={email} />
      <UnsubscribeBanner accountId={accountId} email={email} />
    </>
  );
}

function hasInvite(email: EmailDetail) {
  return (email.attachments ?? []).some((a) => {
    const t = (a.type || "").toLowerCase();
    return t === "text/calendar" || t === "application/ics" || (a.name || "").toLowerCase().endsWith(".ics");
  });
}

function InviteBanner({ accountId, email }: { accountId: string; email: EmailDetail }) {
  const [invite, setInvite] = useState<Invite | null>(null);
  const [busy, setBusy] = useState("");

  useEffect(() => {
    if (!email.bodyFetched || !hasInvite(email)) return;
    let cancelled = false;
    api
      .getInvite(accountId, email.id)
      .then((inv) => !cancelled && setInvite(inv ?? null))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [accountId, email]);

  if (!invite) return null;

  const start = invite.start ? parseLocalDateTime(invite.start) : null;
  const end = invite.end ? parseLocalDateTime(invite.end) : null;
  const when = start
    ? invite.allDay
      ? `${shortDate(start)}${end && end.getTime() - start.getTime() > 86400000 ? ` – ${shortDate(new Date(end.getTime() - 86400000))}` : ""} 全天`
      : `${shortDate(start)} ${hm(start)}${end ? ` – ${hm(end)}` : ""}`
    : "";

  async function act(status: "" | "accepted" | "tentative" | "declined") {
    if (!invite) return;
    setBusy(status || "add");
    try {
      const eventId = await api.addInviteToCalendar(accountId, email.id, status);
      toast.success(
        status === "accepted" ? "已接受邀请" : status === "tentative" ? "已回复暂定" : status === "declined" ? "已拒绝邀请" : "已加入日历",
      );
      setInvite({ ...invite, eventId, myStatus: status || invite.myStatus } as Invite);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
    }
  }

  return (
    <div className="border-border bg-secondary flex flex-wrap items-center gap-3 rounded-md border px-4 py-3">
      <CalendarDays className="text-accent size-5 shrink-0" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm font-medium">
          {invite.cancelled ? "日程已取消：" : "日历邀请："}
          {invite.title || "(无标题)"}
        </p>
        <p className="text-muted-foreground flex flex-wrap items-center gap-x-3 text-xs">
          {when && <span className="tabular-nums">{when}</span>}
          {invite.location && (
            <span className="flex items-center gap-1">
              <MapPin className="size-3" />
              {invite.location}
            </span>
          )}
          {invite.organizer && <span>组织者 {invite.organizer}</span>}
          {invite.recurring && <span>重复日程</span>}
        </p>
      </div>
      {!invite.cancelled && (
        <div className="flex flex-wrap gap-1.5">
          {(["accepted", "tentative", "declined"] as const).map((s) => (
            <Button
              key={s}
              size="sm"
              variant={invite.myStatus === s ? "default" : "outline"}
              className="h-7"
              disabled={!!busy}
              onClick={() => act(s)}
            >
              {busy === s ? <Loader2 className="size-3.5 animate-spin" /> : invite.myStatus === s ? <Check className="size-3.5" /> : null}
              {{ accepted: "接受", tentative: "暂定", declined: "拒绝" }[s]}
            </Button>
          ))}
          {invite.eventId ? (
            <Button size="sm" variant="ghost" className="h-7" onClick={() => send("navigate", { module: "calendar" })}>
              在日历中查看
            </Button>
          ) : (
            <Button size="sm" variant="ghost" className="h-7" disabled={!!busy} onClick={() => act("")}>
              {busy === "add" && <Loader2 className="size-3.5 animate-spin" />}
              仅加入日历
            </Button>
          )}
        </div>
      )}
    </div>
  );
}

function UnsubscribeBanner({ accountId, email }: { accountId: string; email: EmailDetail }) {
  const [info, setInfo] = useState<Unsubscribe | null>(email.unsubscribe ?? null);
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  useEffect(() => {
    setInfo(email.unsubscribe ?? null);
    // 正文在这个功能加入前就已缓存的邮件没有退订信息, 后台补查一次。
    if (email.bodyFetched && !email.unsubscribe) {
      let cancelled = false;
      api
        .loadListHeaders(accountId, email.id)
        .then((u) => !cancelled && setInfo(u))
        .catch(() => {});
      return () => {
        cancelled = true;
      };
    }
  }, [accountId, email]);

  if (!info || (!info.http && !info.mailto)) return null;

  const how = info.oneClick
    ? "会直接向发件方发送退订请求，无需打开网页。"
    : info.mailto
      ? `会从你的邮箱向 ${info.mailto.replace(/^mailto:/i, "").split("?")[0]} 发送一封退订邮件。`
      : "会在浏览器中打开发件方的退订页面。";

  async function run() {
    setBusy(true);
    try {
      const mode = await api.unsubscribe(accountId, email.id);
      setDone(true);
      toast.success(mode === "opened" ? "已打开退订页面，请在网页上完成退订" : "已发送退订请求");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="text-muted-foreground flex items-center gap-2 text-xs">
        <MailX className="size-3.5 shrink-0" />
        <span className="flex-1">这是一封订阅邮件</span>
        {done ? (
          <span className="flex items-center gap-1">
            <Check className="size-3.5" />
            已退订
          </span>
        ) : (
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => setConfirm(true)} disabled={busy}>
            {busy && <Loader2 className="size-3 animate-spin" />}
            退订
          </Button>
        )}
      </div>
      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>退订这类邮件</AlertDialogTitle>
            <AlertDialogDescription>{how} 只对可信的发件方使用退订；垃圾邮件请标记为垃圾邮件。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction onClick={run}>退订</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
