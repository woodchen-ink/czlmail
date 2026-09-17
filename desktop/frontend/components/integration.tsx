"use client";

import { useCallback, useEffect, useState } from "react";
import { CalendarPlus, Loader2, Mail, Power } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { Events, api, errorMessage, onEvent, type Calendar, type IntegrationStatus, type OpenRequest } from "@/lib/api";
import { send } from "@/lib/app-bus";
import { onFileDrop } from "@/lib/file-drop";

const ONBOARD_KEY = "czlmail.onboarded.integration";

/**
 * 与操作系统的衔接: 处理 mailto / .ics / webcal 打开请求、拖入的 .ics 文件,
 * 以及首次使用时引导开启开机自启和设为默认应用。
 */
export function Integration() {
  const [icsSource, setIcsSource] = useState<string | null>(null);
  const [onboard, setOnboard] = useState<IntegrationStatus | null>(null);

  const drain = useCallback(async () => {
    try {
      const reqs: OpenRequest[] = (await api.takeOpenRequests()) ?? [];
      for (const r of reqs) {
        if (r.kind === "compose") {
          send("navigate", { module: "mail" });
          send("compose", { to: r.to, cc: r.cc, bcc: r.bcc, subject: r.subject, body: r.body });
        } else if (r.kind === "ics" && r.path) {
          setIcsSource(r.path);
        } else if (r.kind === "email" || r.kind === "thread") {
          // czlmail:// 链接。会话链接打开其中最新的一封; 本地没有缓存时提示而不是静默忽略。
          let emailId = r.id;
          if (r.kind === "thread") {
            const list = (await api.threadEmails(r.accountId, r.id).catch(() => [])) ?? [];
            emailId = list[list.length - 1]?.id ?? "";
          }
          if (!emailId) {
            toast.error("本地找不到链接指向的邮件");
            continue;
          }
          send("navigate", { module: "mail" });
          send("openEmail", { accountId: r.accountId, emailId });
        }
      }
    } catch {
      // 未登录等情况下忽略, 请求会留在后端队列里。
    }
  }, []);

  useEffect(() => {
    drain();
    const off = onEvent(Events.openRequest, drain);
    const offDrop = onFileDrop((paths) => {
      const ics = paths.find((p) => /\.(ics|ical|ifb)$/i.test(p));
      if (!ics) return false;
      setIcsSource(ics);
      return true;
    });
    return () => {
      off();
      offDrop();
    };
  }, [drain]);

  useEffect(() => {
    try {
      if (localStorage.getItem(ONBOARD_KEY)) return;
    } catch {
      return;
    }
    // 登录后稍等再弹, 不和首轮同步的界面变化挤在一起。
    const t = setTimeout(async () => {
      try {
        const s = await api.getIntegrationStatus();
        if (s.supported && (!s.autostart || !s.defaultMail || !s.defaultCalendar)) setOnboard(s);
      } catch {
        // 忽略
      }
    }, 4000);
    return () => clearTimeout(t);
  }, []);

  function finishOnboard() {
    try {
      localStorage.setItem(ONBOARD_KEY, "1");
    } catch {
      // 忽略
    }
    setOnboard(null);
  }

  return (
    <>
      <IcsImportDialog source={icsSource} onClose={() => setIcsSource(null)} />
      {onboard && <OnboardDialog status={onboard} onChange={setOnboard} onClose={finishOnboard} />}
    </>
  );
}

function IcsImportDialog({ source, onClose }: { source: string | null; onClose: () => void }) {
  const [calendars, setCalendars] = useState<Calendar[]>([]);
  const [target, setTarget] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!source) return;
    setBusy(false);
    api
      .listCalendars()
      .then((list) => {
        const writable = (list ?? []).filter((c) => !c.myRights || c.myRights.mayWriteAll || c.myRights.mayWriteOwn);
        setCalendars(writable);
        const def = writable.find((c) => c.isDefault) ?? writable[0];
        setTarget(def ? `${def.accountId}/${def.id}` : "");
      })
      .catch((err) => toast.error(errorMessage(err)));
  }, [source]);

  if (!source) return null;
  const name = source.startsWith("https://") ? source : source.split(/[\\/]/).pop();

  async function run() {
    if (!target || busy || !source) return;
    const [acc, cal] = target.split("/");
    setBusy(true);
    try {
      const n = await api.importCalendarSource(acc, cal, source);
      toast.success(`已导入 ${n} 个日程`);
      send("navigate", { module: "calendar" });
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <CalendarPlus className="size-5" />
            导入日历
          </DialogTitle>
          <DialogDescription className="break-all">{name}</DialogDescription>
        </DialogHeader>
        <label className="flex flex-col gap-2 text-sm">
          导入到
          <select
            value={target}
            onChange={(e) => setTarget(e.target.value)}
            className="border-input h-9 rounded-sm border bg-transparent px-2"
          >
            {calendars.map((c) => (
              <option key={`${c.accountId}/${c.id}`} value={`${c.accountId}/${c.id}`} className="bg-popover">
                {c.name}
              </option>
            ))}
          </select>
        </label>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={run} disabled={!target || busy}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            导入
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function OnboardDialog({
  status,
  onChange,
  onClose,
}: {
  status: IntegrationStatus;
  onChange: (s: IntegrationStatus) => void;
  onClose: () => void;
}) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>让 CZL Mail 更顺手</DialogTitle>
          <DialogDescription>这些设置随时可以在「设置 → 默认应用」里修改。</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4 text-sm">
          <div className="flex items-start gap-3">
            <Power className="text-muted-foreground mt-0.5 size-5 shrink-0" />
            <div className="min-w-0 flex-1">
              <p className="font-medium">开机自动启动</p>
              <p className="text-muted-foreground text-xs">在后台启动并驻留托盘，新邮件与日程提醒能及时通知。</p>
            </div>
            <Switch
              checked={status.autostart}
              onCheckedChange={async (on) => {
                try {
                  onChange(await api.setAutostart(on));
                } catch (err) {
                  toast.error(errorMessage(err));
                }
              }}
            />
          </div>

          <div className="flex items-start gap-3">
            <Mail className="text-muted-foreground mt-0.5 size-5 shrink-0" />
            <div className="min-w-0 flex-1">
              <p className="font-medium">设为默认邮件与日历应用</p>
              <p className="text-muted-foreground text-xs">
                点击网页上的邮箱链接、双击 .ics 文件时用 CZL Mail 打开。Windows 需要在系统设置里确认。
              </p>
              <p className="text-muted-foreground mt-1 text-xs">
                当前：邮件 {status.defaultMail ? "已是默认" : "未设置"} · 日历 {status.defaultCalendar ? "已是默认" : "未设置"}
              </p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={async () => {
                try {
                  await api.openDefaultAppsSettings();
                } catch (err) {
                  toast.error(errorMessage(err));
                }
              }}
            >
              去设置
            </Button>
          </div>
        </div>

        <DialogFooter>
          <Button onClick={onClose}>完成</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
