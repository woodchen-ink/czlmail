"use client";

import { useState } from "react";
import { ChevronDown, MessagesSquare, Paperclip } from "lucide-react";

import { SenderAvatar } from "@/components/sender-avatar";
import { cn } from "@/lib/utils";
import type { EmailSummary } from "@/lib/api";
import { displayAddressList, listDate } from "@/lib/format";

/** 会话里其它邮件太多时默认只显示最近几封, 其余折叠。 */
const COLLAPSED_LIMIT = 4;

/**
 * 同一会话的往来邮件, 按时间正序排列, 当前这封高亮。
 * 会话跨文件夹(收件箱里的来信与已发送里的回复)一起列出, 点一下切换到那封。
 */
export function ThreadList({
  thread,
  currentId,
  own,
  onOpen,
}: {
  thread: EmailSummary[];
  currentId: string;
  own: Set<string>;
  onOpen: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const hidden = expanded ? 0 : Math.max(0, thread.length - COLLAPSED_LIMIT);
  const shown = thread.slice(hidden);

  return (
    <div className="border-border rounded-md border">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        aria-expanded={open}
        className="hover:bg-secondary flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm"
      >
        <MessagesSquare className="text-muted-foreground size-4" />
        <span className="flex-1">本会话共 {thread.length} 封邮件</span>
        <ChevronDown className={cn("text-muted-foreground size-4 transition-transform", !open && "-rotate-90")} />
      </button>
      {open && (
        <ul className="border-border border-t py-1">
          {hidden > 0 && (
            <li>
              <button
                type="button"
                onClick={() => setExpanded(true)}
                className="text-muted-foreground hover:text-foreground w-full px-3 py-1.5 text-left text-xs"
              >
                显示更早的 {hidden} 封
              </button>
            </li>
          )}
          {shown.map((e) => {
            const current = e.id === currentId;
            const mine = (e.from ?? []).some((a) => own.has((a.email || "").toLowerCase()));
            return (
              <li key={e.id}>
                <button
                  type="button"
                  disabled={current}
                  onClick={() => onOpen(e.id)}
                  className={cn(
                    "flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm",
                    current ? "bg-muted" : "hover:bg-secondary",
                  )}
                >
                  <SenderAvatar email={e.from?.[0]?.email ?? ""} name={e.from?.[0]?.name} className="size-6" />
                  <span className={cn("w-32 shrink-0 truncate", e.isUnread && "font-medium")}>
                    {mine ? "我" : displayAddressList(e.from) || "(无发件人)"}
                  </span>
                  <span className="text-muted-foreground min-w-0 flex-1 truncate text-xs">{e.preview}</span>
                  {e.hasAttachment && <Paperclip className="text-muted-foreground size-3.5 shrink-0" />}
                  <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{listDate(e.receivedAt)}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
