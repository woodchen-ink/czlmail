"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useRef, useState } from "react";
import { Bot, CalendarDays, FolderOpen, Mail, NotebookPen, Settings, UsersRound } from "lucide-react";
import { NotesShell } from "@/components/notes/notes-shell";
import { flushNotes, noteApi, noteEvents, type NoteConfig } from "@/lib/notes/api";

import { AssistantShell } from "@/components/assistant/assistant-shell";
import { CalendarShell } from "@/components/calendar/calendar-shell";
import { ContactsShell } from "@/components/contacts/contacts-shell";
import { FilesShell } from "@/components/files/files-shell";
import { Integration } from "@/components/integration";
import { MailShell } from "@/components/mail-shell";
import { SettingsShell } from "@/components/settings/settings-shell";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { UnreadBadge } from "@/components/unread-badge";
import { useCompact } from "@/lib/use-compact";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

import { Events, api, errorMessage, onEvent, type UpdateInfo } from "@/lib/api";
import { listen, send, type Module } from "@/lib/app-bus";
import { nativePlatform } from "@/lib/native-bridge";

const MODULE_KEY = "czlmail.module";

// macOS 习惯用 ⌘ 而不是 Ctrl 作组合键; 两个都认, 提示按平台显示。
const MOD = typeof navigator !== "undefined" && /Mac/i.test(navigator.userAgent) ? "⌘" : "Ctrl+";

const getNav: () => ({ id: Module; label: string; icon: typeof Mail; shortcut: string }[]) = () => ([
  { id: "mail", label: t("邮件"), icon: Mail, shortcut: `${MOD}1` },
  { id: "calendar", label: t("日历"), icon: CalendarDays, shortcut: `${MOD}2` },
  { id: "contacts", label: t("通讯录"), icon: UsersRound, shortcut: `${MOD}3` },
  { id: "files", label: t("文件"), icon: FolderOpen, shortcut: `${MOD}4` },
  { id: "notes", label: t("笔记"), icon: NotebookPen, shortcut: `${MOD}6` },
]);

/**
 * 应用外壳：最左侧的模块导航栏加各模块。
 *
 * 模块常驻挂载、切换时只是隐藏：邮件列表的滚动位置、日历的视图与日期这些状态
 * 在切来切去时不该丢。
 */
export function AppShell({ onSignedOut }: { onSignedOut: () => void }) {
  useI18n();
  const [update, setUpdate] = useState<UpdateInfo | null>(null);
  // 各账号收件箱未读总数(不含静音账号), 与托盘角标同源: 在别的模块里也要看得到有新信。
  const [unread, setUnread] = useState(0);
  const [module, setModule] = useState<Module>("mail");
  const currentModule = useRef<Module>("mail");
  const notesGate = useRef(false);
  const [notesEnabled, setNotesEnabled] = useState(false);
  // 未打开过的模块不挂载，避免启动时一次性拉取全部数据。
  const [visited, setVisited] = useState<Set<Module>>(() => new Set(["mail"]));
  const compact = useCompact();

  // Navigation must not unmount or hide a failed save without letting the user recover it.
  const go = useCallback(async (m: Module) => {
    if (m === "notes" && !notesGate.current) return;
    try {
      if (currentModule.current === "notes" && m !== "notes") await flushNotes();
    } catch (err) { toast.error(errorMessage(err)); return; }
    currentModule.current = m;
    setModule(m);
    setVisited((v) => (v.has(m) ? v : new Set(v).add(m)));
    try { localStorage.setItem(MODULE_KEY, m); } catch { /* Ignore unavailable preference storage. */ }
  }, []);

  useEffect(() => {
    const apply = (config: NoteConfig) => {
      notesGate.current = config.enabled; setNotesEnabled(config.enabled);
      if (!config.enabled && currentModule.current === "notes") {
        currentModule.current = "mail"; setModule("mail"); localStorage.setItem(MODULE_KEY, "mail");
      }
    };
    void noteApi.config().then((config) => {
      apply(config);
      if (config.enabled && localStorage.getItem(MODULE_KEY) === "notes") void go("notes");
    }).catch((err) => toast.error(errorMessage(err)));
    return onEvent(noteEvents.config, apply);
  }, [go]);

  useEffect(() => {
    api.getUnreadTotal().then(setUnread).catch(() => {});
    return onEvent(Events.unread, (n: number) => setUnread(n));
  }, []);

  useEffect(() => {
    try {
      const saved = localStorage.getItem(MODULE_KEY) as Module | null;
      if (saved && saved !== "settings" && (saved === "assistant" || getNav().some((n) => n.id === saved))) void go(saved);
    } catch {
      // 忽略
    }
  }, [go]);

  useEffect(() => {
    const offNav = listen("navigate", ({ module }) => go(module));
    // 点系统通知时切到对应模块; 具体定位由模块自己监听同一事件完成 (邮件走打开请求队列, 见 integration.tsx)。
    const offEvent = onEvent(Events.openEvent, () => go("calendar"));
    const openAbout = () => {
      go("settings");
      // 设置模块可能刚挂载, 等它订阅后再发。
      setTimeout(() => send("openSettings", { section: "about" }), 0);
    };
    const android = nativePlatform() === "android";
    const announce = (info: UpdateInfo) => {
      setUpdate(info);
      toast.info(t("CZL Mail {0} 已发布", info.release?.version ?? ""), {
        id: "update-available",
        duration: Infinity,
        description: android
          ? t("更新会自动下载并校验，再交给系统安装。")
          : t("更新会自动下载、校验并重新打开程序。"),
        action: {
          label: t("立即更新"),
          onClick: () => {
            toast.loading(t("正在下载更新…"), { id: "update-install" });
            api
              .installUpdate()
              // 桌面端装完会退出并重新打开; Android 交给系统安装器后就返回, 由用户在安装器里确认。
              .then(() => android && toast.success(t("已交给系统安装器，按提示完成更新。"), { id: "update-install" }))
              .catch((err) => toast.error(errorMessage(err), { id: "update-install" }));
          },
        },
        cancel: { label: t("稍后"), onClick: () => {} },
      });
    };
    api
      .getPendingUpdate()
      .then((info) => info?.available && announce(info))
      .catch(() => {});
    const offUpdate = onEvent(Events.updateAvailable, announce);
    const offOpenUpdate = onEvent(Events.openUpdate, openAbout);

    function onKey(e: KeyboardEvent) {
      if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
      if (e.key === ",") {
        e.preventDefault();
        go("settings");
        return;
      }
      // Shortcuts are explicit: moving AI to the bottom or hiding notes never renumbers them.
      const shortcuts: Record<string, Module> = { "1": "mail", "2": "calendar", "3": "contacts", "4": "files", "5": "assistant", "6": "notes" };
      const target = shortcuts[e.key];
      if (target && (target !== "notes" || notesGate.current)) {
        e.preventDefault(); void go(target);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => {
      offNav();
      offEvent();
      offUpdate();
      offOpenUpdate();
      window.removeEventListener("keydown", onKey);
    };
  }, [go]);

  const nav = getNav().filter((item) => item.id !== "notes" || notesEnabled);

  return (
    <div className="flex h-full flex-col md:flex-row">
      <nav className="border-border bg-sidebar hidden w-14 shrink-0 flex-col items-center gap-1 border-r py-3 md:flex" aria-label={t("模块")}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src="/logo.png" alt="CZL Mail" className="mb-3 size-8" />
        {nav.map((item) => (
          <RailButton
            key={item.id}
            item={item}
            active={module === item.id}
            onClick={() => go(item.id)}
            count={item.id === "mail" ? unread : 0}
          />
        ))}
        <div className="flex-1" />
        <RailButton item={{ label: t("AI 助手"), icon: Bot, shortcut: `${MOD}5` }} active={module === "assistant"} onClick={() => void go("assistant")} />
        <RailButton
          badge={!!update?.available}
          item={{ label: update?.available ? t("设置（有新版本）") : t("设置"), icon: Settings, shortcut: `${MOD},` }}
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
        {notesEnabled && visited.has("notes") && (
          <Pane show={module === "notes"}><NotesShell active={module === "notes"} /></Pane>
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
      {compact && (
        <BottomNav
          items={nav}
          module={module}
          unread={unread}
          updateBadge={!!update?.available}
          onSelect={(m) => void go(m)}
        />
      )}
      <Integration />
    </div>
  );
}

function Pane({ show, children }: { show: boolean; children: React.ReactNode }) {
  useI18n();
  return <div className={cn("absolute inset-0", !show && "hidden")}>{children}</div>;
}

function RailButton({
  item,
  active,
  onClick,
  badge,
  count = 0,
}: {
  item: { label: string; icon: typeof Mail; shortcut: string };
  active: boolean;
  onClick: () => void;
  badge?: boolean;
  /** 数字角标, 比 badge 的红点优先。 */
  count?: number;
}) {
  useI18n();
  const Icon = item.icon;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={onClick}
          aria-label={count > 0 ? t("{0}（{1} 封未读）", item.label, count) : item.label}
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
          {count > 0 ? (
            <UnreadBadge count={count} className="ring-sidebar absolute top-0.5 right-0.5 ring-2" />
          ) : badge && <span className="bg-destructive ring-sidebar absolute top-1.5 right-1.5 size-2 rounded-full ring-2" />}
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">
        {item.label}
        {item.shortcut && <span className="text-muted-foreground ml-2 text-xs">{item.shortcut}</span>}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * 窄屏的底部模块栏, 替代左侧竖栏; 底部留出 Home 指示条的安全区。
 * 不放 AI 助手: 它管的是 MCP, 要本机 AI 客户端连进来, 手机上没有意义。
 */
function BottomNav({
  items,
  module,
  unread,
  updateBadge,
  onSelect,
}: {
  items: { id: Module; label: string; icon: typeof Mail }[];
  module: Module;
  unread: number;
  updateBadge: boolean;
  onSelect: (m: Module) => void;
}) {
  useI18n();
  const all: { id: Module; label: string; icon: typeof Mail; count?: number; dot?: boolean }[] = [
    ...items.map((i) => ({ ...i, count: i.id === "mail" ? unread : 0 })),
    { id: "settings", label: t("设置"), icon: Settings, dot: updateBadge },
  ];
  return (
    <nav
      className="border-border bg-sidebar flex shrink-0 border-t pb-[env(safe-area-inset-bottom)]"
      aria-label={t("模块")}
    >
      {all.map((item) => {
        const Icon = item.icon;
        const active = module === item.id;
        return (
          <button
            key={item.id}
            type="button"
            onClick={() => onSelect(item.id)}
            aria-current={active ? "page" : undefined}
            className={cn(
              "text-muted-foreground relative flex h-14 min-w-0 flex-1 flex-col items-center justify-center gap-0.5 text-[11px]",
              active && "text-foreground",
            )}
          >
            <span className="relative">
              <Icon className="size-5" />
              {item.count ? (
                <UnreadBadge count={item.count} className="ring-sidebar absolute -top-1.5 -right-3 ring-2" />
              ) : item.dot ? (
                <span className="bg-destructive ring-sidebar absolute -top-0.5 -right-0.5 size-2 rounded-full ring-2" />
              ) : null}
            </span>
            <span className="max-w-full truncate px-0.5">{item.label}</span>
          </button>
        );
      })}
    </nav>
  );
}
