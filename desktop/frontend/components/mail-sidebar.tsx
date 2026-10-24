"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ChevronDown, Plus, UserRound } from "lucide-react";

import { FolderDialog, type FolderAction } from "@/components/folder-dialog";

import { MailboxTree, inboxUnread, type FolderMenuAction } from "@/components/mailbox-tree";
import { UnreadBadge } from "@/components/unread-badge";
import { cn } from "@/lib/utils";
import { Events, api, errorMessage, onEvent, type Account, type Mailbox, type PullStatus } from "@/lib/api";

interface Props {
  accounts: Account[];
  mailboxesByAccount: Record<string, Mailbox[]>;
  accountId: string;
  mailboxId: string;
  onSelect: (accountId: string, mailboxId: string) => void;
  /** 文件夹结构变化后刷新该账号的文件夹列表。 */
  onMailboxesChanged: (accountId: string) => void;
  /** 右键菜单里涉及邮件列表的操作(标记已读、导入、清空、刷新), 由外壳处理。 */
  onFolderAction: (accountId: string, action: "markRead" | "import" | "empty" | "refresh", mailbox: Mailbox) => void;
}

const COLLAPSE_KEY = "czlmail.sidebar.collapsed";

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
  onMailboxesChanged,
  onFolderAction,
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

  useEffect(() => {
    try {
      setCollapsed(JSON.parse(localStorage.getItem(COLLAPSE_KEY) ?? "{}"));
    } catch {
      // 偏好读不出来就全部展开。
    }
  }, []);

  function toggle(key: string) {
    setCollapsed((prev) => {
      const next = { ...prev, [key]: !prev[key] };
      try {
        localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next));
      } catch {
        // 存不下只影响下次启动。
      }
      return next;
    });
  }

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

  const personal = accounts.filter((a) => a.isPersonal);
  const shared = accounts.filter((a) => !a.isPersonal);
  const sumInbox = (list: Account[]) => list.reduce((n, a) => n + inboxUnread(mailboxesByAccount[a.id]), 0);

  return (
    <div className="flex flex-col gap-3">
      {personal.length > 0 && (
        <Section
          title={t("文件夹")}
          open={!collapsed.personal}
          onToggle={() => toggle("personal")}
          unread={sumInbox(personal)}
          onAdd={() => setFolderAction({ kind: "create", accountId: personal[0].id })}
        >
          {personal.map((acc) => (
            <MailboxTree
              key={acc.id}
              mailboxes={mailboxesByAccount[acc.id] ?? []}
              selectedId={acc.id === accountId ? mailboxId : ""}
              onSelect={(id) => onSelect(acc.id, id)}
              onAction={act(acc.id)}
            />
          ))}
        </Section>
      )}

      {shared.length > 0 && (
        <Section title={t("共享")} open={!collapsed.shared} onToggle={() => toggle("shared")} unread={sumInbox(shared)}>
          {shared.map((acc) => {
            const key = `acc:${acc.id}`;
            const open = !collapsed[key];
            return (
              <div key={acc.id} className="flex flex-col">
                <button
                  type="button"
                  onClick={() => toggle(key)}
                  aria-expanded={open}
                  className="text-muted-foreground hover:text-foreground flex items-center gap-1.5 rounded-sm px-2 py-1 text-left text-xs"
                >
                  <ChevronDown
                    className={cn("size-3 shrink-0 transition-transform", !open && "-rotate-90")}
                  />
                  <UserRound className="size-3.5 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">{acc.name}</span>
                  {/* 展开时收件箱行自己有角标, 折叠时才在账号名上显示, 否则看不出是哪个共享邮箱来了信。 */}
                  {!open && <UnreadBadge count={inboxUnread(mailboxesByAccount[acc.id])} />}
                </button>
                {open && (
                  <MailboxTree
                    mailboxes={mailboxesByAccount[acc.id] ?? []}
                    selectedId={acc.id === accountId ? mailboxId : ""}
                    onSelect={(id) => onSelect(acc.id, id)}
                    onAction={act(acc.id)}
                    indent={1}
                  />
                )}
              </div>
            );
          })}
        </Section>
      )}

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
  children,
}: {
  title: string;
  open: boolean;
  onToggle: () => void;
  onAdd?: () => void;
  /** 折叠时显示的收件箱未读数。 */
  unread?: number;
  children: React.ReactNode;
}) {
  useI18n();
  return (
    <section className="flex flex-col gap-0.5">
      <div className="group/section flex items-center">
        <button
          type="button"
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
