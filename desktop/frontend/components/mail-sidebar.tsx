"use client";

import { t, tContext } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ChevronDown, ExternalLink, MailWarning, Plus, UserRound } from "lucide-react";

import { FolderDialog, type FolderAction } from "@/components/folder-dialog";

import {
  FolderButton,
  KIND_ORDER,
  MailboxTree,
  inboxUnread,
  mailboxColor,
  mailboxIcon,
  mailboxLabel,
  type FolderMenuAction,
} from "@/components/mailbox-tree";
import { SidebarSkeleton } from "@/components/mail-sync-state";
import { UnreadBadge } from "@/components/unread-badge";
import { UpstreamBadge } from "@/components/upstream-status";
import { openExternal } from "@/lib/external";
import { upstreamKind } from "@/lib/upstream";
import { cn } from "@/lib/utils";
import { draggedEmails, onEmailDrag } from "@/lib/email-drag";
import { Events, api, errorMessage, onEvent, type Account, type Mailbox, type PullStatus } from "@/lib/api";

interface Props {
  accounts: Account[];
  mailboxesByAccount: Record<string, Mailbox[]>;
  accountId: string;
  mailboxId: string;
  onSelect: (accountId: string, mailboxId: string) => void;
  /** 当前打开的统一视图(文件夹类别或 unread), 空串表示在看某个账号的文件夹。 */
  unified: string;
  onSelectUnified: (view: string) => void;
  /** 文件夹结构变化后刷新该账号的文件夹列表。 */
  onMailboxesChanged: (accountId: string) => void;
  /** 右键菜单里涉及邮件列表的操作(标记已读、导入、清空、刷新), 由外壳处理。 */
  onFolderAction: (accountId: string, action: "markRead" | "import" | "empty" | "refresh", mailbox: Mailbox) => void;
  /** 首次同步还没把文件夹取回来: 一个文件夹都没有时显示占位而不是空白。 */
  syncing?: boolean;
  /** 邮件从列表拖到 accountId 账号的 mailboxId 文件夹上放下。 */
  onDropEmails?: (keys: string[], accountId: string, mailboxId: string) => void;
}

const COLLAPSE_KEY = "czlmail.sidebar.collapsed";

/** 统一视图里的「未读」。 */
export const UNIFIED_UNREAD = "unread";

// 不算进「未读」视图的文件夹类别, 与后端 api_unified.go 的 unreadSkipKinds 一致。
const UNREAD_SKIP_KINDS = ["junk", "trash", "sent", "drafts", "scheduled"];

/**
 * 全部账号的文件夹平铺在一个侧栏里：个人账号在「文件夹」下，共享账号逐个列在「共享」下。
 *
 * 不做账号切换器：共享邮箱是日常要处理的收件箱，藏在下拉菜单后面意味着
 * 用户看不到那边有没有未读，只能挨个切过去看。
 */
export function MailSidebar({
  accounts,
  mailboxesByAccount,
  accountId,
  mailboxId,
  onSelect,
  unified,
  onSelectUnified,
  onMailboxesChanged,
  onFolderAction,
  syncing,
  onDropEmails,
}: Props) {
  useI18n();
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [folderAction, setFolderAction] = useState<FolderAction | null>(null);

  const act = (accId: string) => (action: FolderMenuAction, mailbox: Mailbox, label: string) => {
    switch (action) {
      case "create":
        return setFolderAction({ kind: "create", accountId: accId, parent: mailbox });
      case "rename":
      case "move":
      case "delete":
        return setFolderAction({ kind: action, accountId: accId, mailbox });
      case "pull":
        return pull(accId, mailbox.id, label);
      default:
        return onFolderAction(accId, action, mailbox);
    }
  };

  // 关了新邮件通知的账号(多为共享邮箱): 未读不算进汇总的红色角标, 自己的收件箱改灰底。
  // 与托盘角标(tray_badge.go 跳过静音账号)一致。
  const [muted, setMuted] = useState<Set<string>>(new Set());
  // 不并入「全部账号」的账号(设置 → 账户): 统一视图的条目与未读数都不算它, 与后端 unifiedMailboxes 一致。
  const [excluded, setExcluded] = useState<Set<string>>(new Set());
  useEffect(() => {
    const load = () =>
      api
        .getSettings()
        .then((s) => {
          setMuted(new Set(s.mutedMailAccounts ?? []));
          setExcluded(new Set(s.unifiedExcludedAccounts ?? []));
        })
        .catch(() => {});
    load();
    return onEvent(Events.settingsChanged, load);
  }, []);

  useEffect(() => {
    try {
      setCollapsed(JSON.parse(localStorage.getItem(COLLAPSE_KEY) ?? "{}"));
    } catch {
      // 偏好读不出来就全部展开。
    }
  }, []);

  // 有统一视图时它是日常入口, 「文件夹」「共享」默认收起; 用户展开、收起过的按记下的来。
  const hasUnified = accounts.length > 1;
  const defaultCollapsed = (key: string) =>
    hasUnified && (key === "personal" || key === "shared" || key.startsWith("signin:"));
  const isCollapsed = (key: string) => collapsed[key] ?? defaultCollapsed(key);

  function toggle(key: string, open?: boolean) {
    setCollapsed((prev) => {
      const next = { ...prev, [key]: open === undefined ? !(prev[key] ?? defaultCollapsed(key)) : !open };
      try {
        localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next));
      } catch {
        // 存不下只影响下次启动。
      }
      return next;
    });
  }

  // 拖着邮件在收起的分组标题上停一会儿就展开, 好把邮件放进里面的文件夹(同 Outlook)。
  const [dragHover, setDragHover] = useState("");
  useEffect(() => onEmailDrag((items) => !items && setDragHover("")), []);
  useEffect(() => {
    if (!dragHover) return;
    const id = setTimeout(() => {
      setDragHover("");
      toggle(dragHover, true);
    }, 600);
    return () => clearTimeout(id);
    // toggle 每次渲染都是新函数, 计时只跟着悬停的分组走。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dragHover]);
  const dragExpand = (key: string) =>
    onDropEmails && isCollapsed(key)
      ? {
          onDragEnter: (e: React.DragEvent) => {
            if (draggedEmails(e)) setDragHover(key);
          },
          onDragLeave: (e: React.DragEvent) => {
            if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragHover((cur) => (cur === key ? "" : cur));
          },
        }
      : {};

  // 整夹拉取在后台进行，进度用一条随进度更新的 toast 显示，id 按文件夹区分。
  useEffect(() => {
    return onEvent(Events.pullProgress, (s: PullStatus) => {
      const id = `pull:${s.accountId}/${s.mailboxId}`;
      if (!s.done) {
        const total = s.total > 0 ? ` / ${s.total}` : "";
        toast.loading(t("正在拉取：已检查 {0}{1} 封，新增 {2} 封", s.scanned, total, s.fetched), { id });
      } else if (s.error) {
        toast.error(t("拉取中断：{0}（已拉取的部分保留，可重试）", s.error), { id });
      } else {
        toast.success(t("拉取完成：共 {0} 封，新增 {1} 封", s.scanned, s.fetched), { id });
      }
    });
  }, []);

  async function pull(accId: string, boxId: string, label: string) {
    try {
      await api.pullMailbox(accId, boxId);
      toast.loading(t("开始拉取「{0}」", label), { id: `pull:${accId}/${boxId}` });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  // 「文件夹」「共享」放主登录的账号; 其余登录 (#15) 各占一节, 标题是它的个人账号, 里面是它的文件夹与共享邮箱。
  const primary = accounts.filter((a) => !a.signIn);
  const personal = primary.filter((a) => a.isPersonal);
  const shared = primary.filter((a) => !a.isPersonal);
  const otherSignIns = [...new Set(accounts.filter((a) => a.signIn).map((a) => a.signIn))].map((id) => {
    const list = accounts.filter((a) => a.signIn === id);
    const own = list.filter((a) => a.isPersonal);
    // 一个登录下有多个邮箱或是网关邮箱 (czlmail 网关) 时, 每个邮箱各有一行标题, 这一节的标题用登录的服务器,
    // 不用第一个邮箱的名字, 否则看起来整节只属于那一个邮箱。
    const grouped = own.length > 1 || own.some((a) => a.upstream);
    const host = list[0].signInHost;
    return {
      id,
      list,
      own,
      shared: list.filter((a) => !a.isPersonal),
      title: grouped && host ? host : (own[0] ?? list[0]).name,
    };
  });
  const sumInbox = (list: Account[]) =>
    list.reduce((n, a) => n + (muted.has(a.id) ? 0 : inboxUnread(mailboxesByAccount[a.id])), 0);
  // 统一视图里在看的不是某个账号的文件夹, 账号文件夹都不高亮。
  const selectedIn = (acc: string) => (!unified && acc === accountId ? mailboxId : "");

  // 统一视图的条目: 至少一个账号有的系统文件夹, 按侧栏固定顺序, 「未读」跟在收件箱后面。
  // 条目按并入统一视图的账号列, 未读数只数其中没静音的账号。
  const allBoxes = accounts.flatMap((a) => mailboxesByAccount[a.id] ?? []);
  const unifiedAccounts = accounts.filter((a) => !excluded.has(a.id));
  const unifiedBoxes = unifiedAccounts.flatMap((a) => mailboxesByAccount[a.id] ?? []);
  const countedBoxes = unifiedAccounts.flatMap((a) => (muted.has(a.id) ? [] : (mailboxesByAccount[a.id] ?? [])));
  // 草稿是总数而不是未读数 (同账号文件夹), 也不是通知, 静音的账号照样算。
  const unreadOf = (kind: string) =>
    kind === "drafts"
      ? unifiedBoxes.filter((m) => m.kind === kind).reduce((n, m) => n + m.totalEmails, 0)
      : countedBoxes.filter((m) => m.kind === kind).reduce((n, m) => n + m.unreadEmails, 0);
  const unifiedRows = KIND_ORDER.filter((kind) => unifiedBoxes.some((m) => m.kind === kind)).map((kind) => ({
    view: kind,
    icon: mailboxIcon(kind),
    color: mailboxColor(kind),
    label: mailboxLabel({ kind, name: kind }),
    unread: unreadOf(kind),
    badge: kind === "inbox",
  }));
  if (unifiedRows.length > 0) {
    unifiedRows.splice(unifiedRows[0].view === "inbox" ? 1 : 0, 0, {
      view: UNIFIED_UNREAD,
      icon: MailWarning,
      color: "text-folder-inbox",
      label: t("未读"),
      unread: countedBoxes.filter((m) => !UNREAD_SKIP_KINDS.includes(m.kind)).reduce((n, m) => n + m.unreadEmails, 0),
      badge: true,
    });
  }

  // 共享邮箱, 以及同一登录下的多个个人账号 (czlmail 网关每个上游邮箱一个): 每个账号一行可折叠的标题。
  // 网关账号的标题带上游类型图标与状态 (需要重新授权 / 连不上 / 同步中), 能重新授权时旁边给网关后台入口。
  const renderAccounts = (list: Account[]) =>
    list.map((acc) => {
      const key = `acc:${acc.id}`;
      const open = !isCollapsed(key);
      const KindIcon = upstreamKind(acc.upstream)?.icon ?? UserRound;
      const reauth = acc.upstream?.reauthUrl;
      return (
        <div key={acc.id} className="flex flex-col">
          <div className="flex items-center">
            <button
              type="button"
              onClick={() => toggle(key)}
              {...dragExpand(key)}
              aria-expanded={open}
              className="text-muted-foreground hover:text-foreground flex min-w-0 flex-1 items-center gap-1.5 rounded-sm px-2 py-1 text-left text-xs"
            >
              <ChevronDown
                className={cn("size-3 shrink-0 transition-transform", !open && "-rotate-90")}
              />
              <KindIcon className="size-3.5 shrink-0" />
              <span className="min-w-0 flex-1 truncate">{acc.name}</span>
              <UpstreamBadge info={acc.upstream} />
              {/* 展开时收件箱行自己有角标, 折叠时才在账号名上显示, 否则看不出是哪个共享邮箱来了信。 */}
              {!open && <UnreadBadge count={inboxUnread(mailboxesByAccount[acc.id])} quiet={muted.has(acc.id)} />}
            </button>
            {reauth && (
              <button
                type="button"
                onClick={() => openExternal(reauth)}
                title={t("打开网关后台")}
                aria-label={t("打开网关后台")}
                className="text-muted-foreground hover:text-foreground shrink-0 rounded-sm p-1"
              >
                <ExternalLink className="size-3.5" />
              </button>
            )}
          </div>
          {open && (
            <MailboxTree
              mailboxes={mailboxesByAccount[acc.id] ?? []}
              selectedId={selectedIn(acc.id)}
              onSelect={(id) => onSelect(acc.id, id)}
              onAction={act(acc.id)}
              indent={1}
              quiet={muted.has(acc.id)}
              accountId={acc.id}
              onDropEmails={onDropEmails && ((keys, box) => onDropEmails(keys, acc.id, box))}
            />
          )}
        </div>
      );
    });

  // 登录自己的个人账号: 只有一个普通账号时直接列文件夹 (与原来相同);
  // 有多个 (czlmail 网关) 或是网关账号时按账号分组, 否则几个收件箱连在一起分不清, 上游状态也没处显示。
  const renderOwn = (list: Account[]) =>
    list.length === 1 && !list[0].upstream
      ? list.map((acc) => (
          <MailboxTree
            key={acc.id}
            mailboxes={mailboxesByAccount[acc.id] ?? []}
            selectedId={selectedIn(acc.id)}
            onSelect={(id) => onSelect(acc.id, id)}
            onAction={act(acc.id)}
            quiet={muted.has(acc.id)}
            accountId={acc.id}
            onDropEmails={onDropEmails && ((keys, box) => onDropEmails(keys, acc.id, box))}
          />
        ))
      : renderAccounts(list);

  if (syncing && allBoxes.length === 0) return <SidebarSkeleton />;

  return (
    <div className="flex flex-col gap-3">
      {/* 只有一个账号时统一视图与「文件夹」完全重复, 不显示。 */}
      {hasUnified && unifiedRows.length > 0 && (
        <Section
          title={t("全部账号")}
          open={!isCollapsed("unified")}
          onToggle={() => toggle("unified")}
          unread={sumInbox(accounts)}
        >
          <nav className="flex flex-col gap-0.5" aria-label={t("全部账号")}>
            {unifiedRows.map((row) => (
              <FolderButton
                key={row.view}
                icon={row.icon}
                color={row.color}
                label={row.label}
                selected={unified === row.view}
                onClick={() => onSelectUnified(row.view)}
                unread={row.unread}
                badge={row.badge}
              />
            ))}
          </nav>
        </Section>
      )}

      {personal.length > 0 && (
        <Section
          title={tContext("heading", "文件夹")}
          open={!isCollapsed("personal")}
          onToggle={() => toggle("personal")}
          unread={sumInbox(personal)}
          onAdd={() => setFolderAction({ kind: "create", accountId: personal[0].id })}
          headerProps={dragExpand("personal")}
        >
          {renderOwn(personal)}
        </Section>
      )}

      {shared.length > 0 && (
        <Section
          title={t("共享")}
          open={!isCollapsed("shared")}
          onToggle={() => toggle("shared")}
          unread={sumInbox(shared)}
          headerProps={dragExpand("shared")}
        >
          {renderAccounts(shared)}
        </Section>
      )}
      {otherSignIns.map((g) => {
        const key = `signin:${g.id}`;
        return (
          <Section
            key={key}
            title={g.title}
            open={!isCollapsed(key)}
            onToggle={() => toggle(key)}
            unread={sumInbox(g.list)}
            onAdd={g.own.length > 0 ? () => setFolderAction({ kind: "create", accountId: g.own[0].id }) : undefined}
            headerProps={dragExpand(key)}
          >
            {renderOwn(g.own)}
            {renderAccounts(g.shared)}
          </Section>
        );
      })}

      <FolderDialog
        action={folderAction}
        mailboxes={folderAction ? (mailboxesByAccount[folderAction.accountId] ?? []) : []}
        onClose={() => setFolderAction(null)}
        onDone={onMailboxesChanged}
      />
    </div>
  );
}

function Section({
  title,
  open,
  onToggle,
  onAdd,
  unread = 0,
  headerProps,
  children,
}: {
  title: string;
  open: boolean;
  onToggle: () => void;
  onAdd?: () => void;
  /** 折叠时显示的收件箱未读数。 */
  unread?: number;
  /** 标题按钮上的额外属性 (拖动邮件时悬停展开)。 */
  headerProps?: React.ComponentProps<"button">;
  children: React.ReactNode;
}) {
  useI18n();
  return (
    <section className="flex flex-col gap-0.5">
      <div className="group/section flex items-center">
        <button
          type="button"
          {...headerProps}
          onClick={onToggle}
          aria-expanded={open}
          className="hover:bg-secondary flex min-w-0 flex-1 items-center gap-1.5 rounded-sm px-2 py-1 text-left text-sm font-semibold"
        >
          <ChevronDown className={cn("size-3.5 shrink-0 transition-transform", !open && "-rotate-90")} />
          <span className="min-w-0 flex-1 truncate">{title}</span>
          {!open && <UnreadBadge count={unread} />}
        </button>
        {onAdd && (
          <button
            type="button"
            onClick={onAdd}
            aria-label={t("新建文件夹")}
            title={t("新建文件夹")}
            className="text-muted-foreground hover:bg-secondary hover:text-foreground rounded-sm p-1 opacity-0 group-hover/section:opacity-100 focus-visible:opacity-100"
          >
            <Plus className="size-3.5" />
          </button>
        )}
      </div>
      {open && <div className="flex flex-col gap-1">{children}</div>}
    </section>
  );
}
