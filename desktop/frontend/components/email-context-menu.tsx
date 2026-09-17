"use client";

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

/** 右键菜单里的操作, ids 为单封或当前多选。由外壳实现, 删除、移动等都是乐观更新。 */
export interface EmailMenuActions {
  reply: (email: EmailSummary, all: boolean) => void;
  forward: (email: EmailSummary, asAttachment: boolean) => void;
  archive: (ids: string[]) => void;
  trash: (ids: string[]) => void;
  move: (ids: string[], mailboxId: string) => void;
  junk: (ids: string[], junk: boolean) => void;
  markRead: (ids: string[], read: boolean) => void;
  toggleFlag: (email: EmailSummary) => void;
  togglePin: (email: EmailSummary) => void;
  setLabel: (ids: string[], label: string, set: boolean) => void;
}

interface Props {
  accountId: string;
  email: EmailSummary;
  /** 右键的邮件在多选里且多选超过一封时, 操作作用于整个多选。 */
  checked: Set<string>;
  mailboxes: Mailbox[];
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
    toast.success("已复制链接");
  } catch {
    toast.error("复制失败");
  }
}

/** 邮件列表行的右键菜单, 条目与顺序同 Bulwark。 */
export function EmailContextMenu({
  accountId,
  email,
  checked,
  mailboxes,
  mailboxId,
  currentKind,
  knownLabels,
  actions,
  children,
}: Props) {
  const batch = checked.has(email.id) && checked.size > 1;
  const ids = batch ? [...checked] : [email.id];
  // 列表摘要里没有标签, 打开菜单时从本地缓存读。
  const [labels, setLabels] = useState<string[] | null>(null);
  const [draft, setDraft] = useState("");
  // 每行都挂着一个菜单, 文件夹列表只在打开时才计算。
  const [open, setOpen] = useState(false);

  const spamApplicable = !["sent", "drafts", "scheduled"].includes(currentKind);
  const inJunk = currentKind === "junk";
  const targets = open
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
          <ContextMenuLabel>已选 {ids.length} 封</ContextMenuLabel>
        ) : (
          <>
            <ContextMenuItem onSelect={() => actions.reply(email, false)}>
              <Reply />
              回复
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.reply(email, true)}>
              <ReplyAll />
              全部回复
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.forward(email, false)}>
              <Forward />
              转发
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.forward(email, true)}>
              <Paperclip />
              作为附件转发
            </ContextMenuItem>
            <ContextMenuSeparator />
            <ContextMenuItem onSelect={() => copyLink(emailLink("email", accountId, email.id))}>
              <LinkIcon />
              复制邮件链接
            </ContextMenuItem>
            {email.threadId && (
              <ContextMenuItem onSelect={() => copyLink(emailLink("thread", accountId, email.threadId))}>
                <MessagesSquare />
                复制会话链接
              </ContextMenuItem>
            )}
          </>
        )}
        <ContextMenuSeparator />

        <ContextMenuItem onSelect={() => actions.archive(ids)} disabled={currentKind === "archive"}>
          <Archive />
          归档
        </ContextMenuItem>
        <ContextMenuItem variant="destructive" onSelect={() => actions.trash(ids)}>
          <Trash2 />
          {currentKind === "trash" ? "彻底删除" : "删除"}
        </ContextMenuItem>
        <ContextMenuSeparator />

        {targets.length > 0 && (
          <ContextMenuSub>
            <ContextMenuSubTrigger>
              <FolderInput />
              移动到…
            </ContextMenuSubTrigger>
            <ContextMenuSubContent className="max-h-80 w-52 overflow-y-auto">
              {targets.map((m) => {
                const Icon = mailboxIcon(m.kind);
                return (
                  <ContextMenuItem
                    key={m.id}
                    onSelect={() => actions.move(ids, m.id)}
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
              {email.isFlagged ? "取消星标" : "加星标"}
            </ContextMenuItem>
            <ContextMenuItem onSelect={() => actions.togglePin(email)}>
              {email.isPinned ? <PinOff /> : <Pin />}
              {email.isPinned ? "取消固定" : "固定"}
            </ContextMenuItem>
            <ContextMenuSub>
              <ContextMenuSubTrigger>
                <Tag />
                标签
              </ContextMenuSubTrigger>
              <ContextMenuSubContent className="w-56">
                {allLabels.length === 0 && <p className="text-muted-foreground px-2 py-1.5 text-xs">还没有标签</p>}
                {allLabels.map((label) => {
                  const on = labels?.includes(label) ?? false;
                  return (
                    <ContextMenuCheckboxItem
                      key={label}
                      checked={on}
                      disabled={labels === null}
                      onCheckedChange={(v) => {
                        setLabels((prev) => (v ? [...(prev ?? []), label] : (prev ?? []).filter((l) => l !== label)));
                        actions.setLabel(ids, label, v);
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
                    actions.setLabel(ids, v, true);
                    setDraft("");
                  }}
                >
                  <Input
                    value={draft}
                    onChange={(e) => setDraft(e.target.value)}
                    // 菜单会抢键盘事件用于上下选择，输入框里的按键不能冒泡上去。
                    onKeyDown={(e) => e.stopPropagation()}
                    placeholder="新标签（英文），回车添加"
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
            <ContextMenuItem variant={inJunk ? "default" : "destructive"} onSelect={() => actions.junk(ids, !inJunk)}>
              {inJunk ? <ShieldCheck /> : <ShieldAlert />}
              {inJunk ? "不是垃圾邮件" : "举报垃圾邮件"}
            </ContextMenuItem>
          </>
        )}
        <ContextMenuSeparator />

        <ContextMenuItem onSelect={() => actions.markRead(ids, email.isUnread)}>
          {email.isUnread ? <MailOpen /> : <Mail />}
          {email.isUnread ? "标记为已读" : "标记为未读"}
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}
