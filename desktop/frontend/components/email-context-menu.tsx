"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useState } from "react";
import {
  Archive,
  Forward,
  FolderInput,
  Link as LinkIcon,
  Mail,
  MailOpen,
  MessagesSquare,
  Paperclip,
  Pin,
  PinOff,
  Reply,
  ReplyAll,
  ShieldAlert,
  ShieldCheck,
  Star,
  StarOff,
  Tag,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";

import {
  ContextMenu,
  ContextMenuCheckboxItem,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Input } from "@/components/ui/input";
import { mailboxIcon, mailboxLabel, mailboxTreeOrder } from "@/components/mailbox-tree";
import { api, type EmailSummary, type Mailbox } from "@/lib/api";
import { emailKey, parseEmailKey } from "@/lib/email-key";

/**
 * 右键菜单里的操作, keys 为单封或当前多选的「账号/id」键(统一视图里可能跨账号)。
 * 由外壳实现, 删除、移动等都是乐观更新。
 */
export interface EmailMenuActions {
  reply: (email: EmailSummary, all: boolean) => void;
  forward: (email: EmailSummary, asAttachment: boolean) => void;
  archive: (keys: string[]) => void;
  trash: (keys: string[]) => void;
  /** 移动到 accountId 账号的 mailboxId 文件夹; keys 必须都属于这个账号。 */
  move: (keys: string[], accountId: string, mailboxId: string) => void;
  junk: (keys: string[], junk: boolean) => void;
  markRead: (keys: string[], read: boolean) => void;
  toggleFlag: (email: EmailSummary) => void;
  togglePin: (email: EmailSummary) => void;
  setLabel: (keys: string[], label: string, set: boolean) => void;
}

interface Props {
  email: EmailSummary;
  /** 右键的邮件在多选里且多选超过一封时, 操作作用于整个多选。 */
  checked: Set<string>;
  /** 这封邮件所属账号的文件夹, 供「移动到」。 */
  mailboxes: Mailbox[];
  /** 邮件当前所在、不必出现在「移动到」里的文件夹。 */
  mailboxId: string;
  currentKind: string;
  knownLabels: string[];
  actions: EmailMenuActions;
  children: React.ReactElement;
}

/** 邮件链接: czlmail://email/<账号>/<id>, 由系统交回本程序打开。 */
export function emailLink(kind: "email" | "thread", accountId: string, id: string): string {
  return `czlmail://${kind}/${encodeURIComponent(accountId)}/${encodeURIComponent(id)}`;
}

async function copyLink(link: string) {
  try {
    await navigator.clipboard.writeText(link);
    toast.success(t("已复制链接"));
  } catch {
    toast.error(t("复制失败"));
  }
}

/** 邮件列表行的右键菜单, 条目与顺序同 Bulwark。 */
export function EmailContextMenu({
  email,
  checked,
  mailboxes,
  mailboxId,
  currentKind,
  knownLabels,
  actions,
  children,
}: Props) {
  useI18n();
  const accountId = email.accountId;
  const key = emailKey(email);
  const batch = checked.has(key) && checked.size > 1;
  const keys = batch ? [...checked] : [key];
  // 文件夹属于各自的账号, 多选跨账号时没有共同的目标文件夹。
  const oneAccount = keys.every((k) => parseEmailKey(k).accountId === accountId);
  // 列表摘要里没有标签, 打开菜单时从本地缓存读。
  const [labels, setLabels] = useState<string[] | null>(null);
  const [draft, setDraft] = useState("");
  // 每行都挂着一个菜单, 文件夹列表只在打开时才计算。
  const [open, setOpen] = useState(false);

  const spamApplicable = !["sent", "drafts", "scheduled"].includes(currentKind);
  const inJunk = currentKind === "junk";
  const targets = open && oneAccount
    ? mailboxTreeOrder(mailboxes).filter((m) => m.id !== mailboxId && m.kind !== "drafts" && m.kind !== "scheduled")
    : [];
  const allLabels = Array.from(new Set([...knownLabels, ...(labels ?? [])])).sort();

  return (
    <ContextMenu
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          setDraft("");
          return;
        }
        if (!batch) {
          api
            .getEmail(accountId, email.id)
            .then((d) => setLabels(d.labels ?? []))
            .catch(() => setLabels([]));
        }
      }}
    >
      <ContextMenuTrigger asChild>{children}</ContextMenuTrigger>
      <ContextMenuContent className="w-56">
        {batch ? (
          <ContextMenuLabel>{t("已选{0}封", keys.length)}</ContextMenuLabel>
        ) : (
          <>
            <ContextMenuItem onSelect={() => actions.reply(email, false)}>
              <Reply />
              {t("回复")}</ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.reply(email, true)}>
              <ReplyAll />
              {t("全部回复")}</ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.forward(email, false)}>
              <Forward />
              {t("转发")}</ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.forward(email, true)}>
              <Paperclip />
              {t("作为附件转发")}</ContextMenuItem>
            <ContextMenuSeparator />
            <ContextMenuItem onSelect={() => copyLink(emailLink("email", accountId, email.id))}>
              <LinkIcon />
              {t("复制邮件链接")}</ContextMenuItem>
            {email.threadId && (
              <ContextMenuItem onSelect={() => copyLink(emailLink("thread", accountId, email.threadId))}>
                <MessagesSquare />
                {t("复制会话链接")}</ContextMenuItem>
            )}
          </>
        )}
        <ContextMenuSeparator />

        <ContextMenuItem onSelect={() => actions.archive(keys)} disabled={currentKind === "archive"}>
          <Archive />
          {t("归档")}</ContextMenuItem>
        <ContextMenuItem variant="destructive" onSelect={() => actions.trash(keys)}>
          <Trash2 />
          {currentKind === "trash" ? t("彻底删除") : t("删除")}
        </ContextMenuItem>
        <ContextMenuSeparator />

        {targets.length > 0 && (
          <ContextMenuSub>
            <ContextMenuSubTrigger>
              <FolderInput />
              {t("移动到…")}</ContextMenuSubTrigger>
            <ContextMenuSubContent className="max-h-80 w-52 overflow-y-auto">
              {targets.map((m) => {
                const Icon = mailboxIcon(m.kind);
                return (
                  <ContextMenuItem
                    key={m.id}
                    onSelect={() => actions.move(keys, accountId, m.id)}
                    style={{ paddingLeft: `${m.depth * 12 + 6}px` }}
                  >
                    <Icon />
                    <span className="truncate">{mailboxLabel(m)}</span>
                  </ContextMenuItem>
                );
              })}
            </ContextMenuSubContent>
          </ContextMenuSub>
        )}
        {!batch && (
          <>
            <ContextMenuItem onSelect={() => actions.toggleFlag(email)}>
              {email.isFlagged ? <StarOff /> : <Star />}
              {email.isFlagged ? t("取消星标") : t("加星标")}
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.togglePin(email)}>
              {email.isPinned ? <PinOff /> : <Pin />}
              {email.isPinned ? t("取消固定") : t("固定")}
            </ContextMenuItem>
            <ContextMenuSub>
              <ContextMenuSubTrigger>
                <Tag />
                {t("标签")}</ContextMenuSubTrigger>
              <ContextMenuSubContent className="w-56">
                {allLabels.length === 0 && <p className="text-muted-foreground px-2 py-1.5 text-xs">{t("还没有标签")}</p>}
                {allLabels.map((label) => {
                  const on = labels?.includes(label) ?? false;
                  return (
                    <ContextMenuCheckboxItem
                      key={label}
                      checked={on}
                      disabled={labels === null}
                      onCheckedChange={(v) => {
                        setLabels((prev) => (v ? [...(prev ?? []), label] : (prev ?? []).filter((l) => l !== label)));
                        actions.setLabel(keys, label, v);
                      }}
                      // 勾选后不关闭菜单，方便一次打多个标签。
                      onSelect={(e) => e.preventDefault()}
                    >
                      {label}
                    </ContextMenuCheckboxItem>
                  );
                })}
                <ContextMenuSeparator />
                <form
                  className="p-1"
                  onSubmit={(e) => {
                    e.preventDefault();
                    const v = draft.trim();
                    if (!v) return;
                    setLabels((prev) => [...(prev ?? []), v]);
                    actions.setLabel(keys, v, true);
                    setDraft("");
                  }}
                >
                  <Input
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    // 菜单会抢键盘事件用于上下选择，输入框里的按键不能冒泡上去。
                    onKeyDown={(e) => e.stopPropagation()}
                    placeholder={t("新标签（英文），回车添加")}
                    className="h-8 text-sm"
                  />
                </form>
              </ContextMenuSubContent>
            </ContextMenuSub>
          </>
        )}

        {spamApplicable && (
          <>
            <ContextMenuSeparator />
            <ContextMenuItem variant={inJunk ? "default" : "destructive"} onSelect={() => actions.junk(keys, !inJunk)}>
              {inJunk ? <ShieldCheck /> : <ShieldAlert />}
              {inJunk ? t("不是垃圾邮件") : t("举报垃圾邮件")}
            </ContextMenuItem>
          </>
        )}
        <ContextMenuSeparator />

        <ContextMenuItem onSelect={() => actions.markRead(keys, email.isUnread)}>
          {email.isUnread ? <MailOpen /> : <Mail />}
          {email.isUnread ? t("标记为已读") : t("标记为未读")}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
