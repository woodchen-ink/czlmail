"use client";

import { useEffect, useState } from "react";
import { Bot, CalendarDays, FolderOpen, Mail, Settings, UsersRound } from "lucide-react";

import { AssistantShell } from "@/components/assistant/assistant-shell";
import { CalendarShell } from "@/components/calendar/calendar-shell";
import { ContactsShell } from "@/components/contacts/contacts-shell";
import { FilesShell } from "@/components/files/files-shell";
import { Integration } from "@/components/integration";
import { MailShell } from "@/components/mail-shell";
import { SettingsShell } from "@/components/settings/settings-shell";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

import { Events, api, errorMessage, onEvent, type UpdateInfo } from "@/lib/api";
import { listen, send, type Module } from "@/lib/app-bus";

const MODULE_KEY = "czlmail.module";

const NAV: { id: Module; label: string; icon: typeof Mail; shortcut: string }[] = [
  { id: "mail", label: "邮件", icon: Mail, shortcut: "Ctrl+1" },
  { id: "calendar", label: "日历", icon: CalendarDays, shortcut: "Ctrl+2" },
  { id: "contacts", label: "通讯录", icon: UsersRound, shortcut: "Ctrl+3" },
  { id: "files", label: "文件", icon: FolderOpen, shortcut: "Ctrl+4" },
  { id: "assistant", label: "AI 助手", icon: Bot, shortcut: "Ctrl+5" },
];

/**
 * 应用外壳：最左侧的模块导航栏加各模块。
 *
 * 模块常驻挂载、切换时只是隐藏：邮件列表的滚动位置、日历的视图与日期这些状态
 * 在切来切去时不该丢。
 */
export function AppShell({ onSignedOut }: { onSignedOut: () => void }) {
  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  const [module, setModule] = useState<Module>("mail");
  // 未打开过的模块不挂载，避免启动时一次性拉取全部数据。
  const [visited, setVisited] = useState<Set<Module>>(() => new Set(["mail"]));

  function go(m: Module) {
    setModule(m);
    setVisited((v) => (v.has(m) ? v : new Set(v).add(m)));
    try {
      localStorage.setItem(MODULE_KEY, m);
    } catch {
      // 忽略
    }
  }

  useEffect(() => {
    try {
      const saved = localStorage.getItem(MODULE_KEY) as Module | null;
      if (saved && saved !== "settings" && NAV.some((n) => n.id === saved)) go(saved);
    } catch {
      // 忽略
    }
  }, []);

  useEffect(() => {
    const offNav = listen("navigate", ({ module }) => go(module));
    // 点系统通知时切到对应模块; 具体定位由模块自己监听同一事件完成。
    const offMail = onEvent(Events.openEmail, () => go("mail"));
    const offEvent = onEvent(Events.openEvent, () => go("calendar"));
    const openAbout = () => {
      go("settings");
      // 设置模块可能刚挂载, 等它订阅后再发。
      setTimeout(() => send("openSettings", { section: "about" }), 0);
    };
    const announce = (info: UpdateInfo) => {
      setUpdate(info);
      toast.info(`CZL Mail ${info.release?.version ?? ""} 已发布`, {
        id: "update-available",
        duration: Infinity,
        description: "更新会自动下载、校验并重新打开程序。",
        action: {
          label: "立即更新",
          onClick: () => {
            toast.loading("正在下载更新…", { id: "update-install" });
            api.installUpdate().catch((err) => toast.error(errorMessage(err), { id: "update-install" }));
          },
        },
        cancel: { label: "稍后", onClick: () => {} },
      });
    };
    api
      .getPendingUpdate()
      .then((info) => info?.available && announce(info))
      .catch(() => {});
    const offUpdate = onEvent(Events.updateAvailable, announce);
    const offOpenUpdate = onEvent(Events.openUpdate, openAbout);

    function onKey(e: KeyboardEvent) {
      if (!e.ctrlKey || e.altKey || e.shiftKey) return;
      const index = Number(e.key) - 1;
      if (index >= 0 && index < NAV.length) {
        e.preventDefault();
        go(NAV[index].id);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => {
      offNav();
      offMail();
      offEvent();
      offUpdate();
      offOpenUpdate();
      window.removeEventListener("keydown", onKey);
    };
  }, []);

  return (
    <div className="flex h-full">
      <nav className="border-border bg-sidebar flex w-14 shrink-0 flex-col items-center gap-1 border-r py-3" aria-label="模块">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src="/logo.png" alt="CZL Mail" className="mb-3 size-8" />
        {NAV.map((item) => (
          <RailButton key={item.id} item={item} active={module === item.id} onClick={() => go(item.id)} />
        ))}
        <div className="flex-1" />
        <RailButton
          badge={!!update?.available}
          item={{ label: update?.available ? "设置（有新版本）" : "设置", icon: Settings, shortcut: "" }}
          active={module === "settings"}
          onClick={() => go("settings")}
        />
      </nav>

      <main className="relative min-w-0 flex-1">
        <Pane show={module === "mail"}>
          <MailShell onSignedOut={onSignedOut} />
        </Pane>
        {visited.has("calendar") && (
          <Pane show={module === "calendar"}>
            <CalendarShell active={module === "calendar"} />
          </Pane>
        )}
        {visited.has("contacts") && (
          <Pane show={module === "contacts"}>
            <ContactsShell active={module === "contacts"} />
          </Pane>
        )}
        {visited.has("files") && (
          <Pane show={module === "files"}>
            <FilesShell active={module === "files"} />
          </Pane>
        )}
        {visited.has("assistant") && (
          <Pane show={module === "assistant"}>
            <AssistantShell active={module === "assistant"} />
          </Pane>
        )}
        {visited.has("settings") && (
          <Pane show={module === "settings"}>
            <SettingsShell active={module === "settings"} onSignedOut={onSignedOut} />
          </Pane>
        )}
      </main>
      <Integration />
    </div>
  );
}

function Pane({ show, children }: { show: boolean; children: React.ReactNode }) {
  return <div className={cn("absolute inset-0", !show && "hidden")}>{children}</div>;
}

function RailButton({
  item,
  active,
  onClick,
  badge,
}: {
  item: { label: string; icon: typeof Mail; shortcut: string };
  active: boolean;
  onClick: () => void;
  badge?: boolean;
}) {
  const Icon = item.icon;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={onClick}
          aria-label={item.label}
          aria-current={active ? "page" : undefined}
          className={cn(
            "text-muted-foreground relative flex size-10 items-center justify-center rounded-md transition-colors",
            "hover:bg-secondary hover:text-foreground focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
            active && "bg-muted text-foreground",
          )}
        >
          {/* 激活指示条用品牌渐变，这是规范允许的少数品牌点缀之一。 */}
          {active && (
            <span className="from-brand-from to-brand-to absolute top-2 bottom-2 -left-2 w-0.5 rounded-full bg-linear-to-b" />
          )}
          <Icon className="size-5" />
          {badge && <span className="bg-destructive ring-sidebar absolute top-1.5 right-1.5 size-2 rounded-full ring-2" />}
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">
        {item.label}
        {item.shortcut && <span className="text-muted-foreground ml-2 text-xs">{item.shortcut}</span>}
      </TooltipContent>
    </Tooltip>
  );
}
