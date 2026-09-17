"use client";

import { useCallback, useEffect, useRef } from "react";
import { Paperclip, Star, Trash2 } from "lucide-react";

import { ScrollArea } from "@/components/ui/scroll-area";
import { SenderAvatar } from "@/components/sender-avatar";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { displayAddressList, listDate } from "@/lib/format";
import type { EmailSummary } from "@/lib/api";

interface Props {
  emails: EmailSummary[];
  selectedId: string;
  loading: boolean;
  hasMore: boolean;
  onSelect: (id: string) => void;
  onLoadMore: () => void;
  onToggleFlag: (email: EmailSummary) => void;
  onDelete?: (email: EmailSummary) => void;
  /** 多选。checked 非空时点击行切换勾选而不是打开。 */
  checked?: Set<string>;
  onCheck?: (id: string, range: boolean) => void;
}

export function EmailList({
  emails,
  selectedId,
  loading,
  hasMore,
  onSelect,
  onLoadMore,
  onToggleFlag,
  onDelete,
  checked,
  onCheck,
}: Props) {
  const sentinelRef = useRef<HTMLDivElement>(null);

  // 触底加载。用 IntersectionObserver 而不是监听 scroll 事件：
  // scroll 在快速滚动时每帧都触发，而这里只需要知道「哨兵进入视口」这一个事实。
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore || loading) return;

    const io = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting) onLoadMore();
      },
      { rootMargin: "200px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [hasMore, loading, onLoadMore]);

  const handleKey = useCallback(
    (e: React.KeyboardEvent, index: number) => {
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      e.preventDefault();
      const next = e.key === "ArrowDown" ? index + 1 : index - 1;
      const target = emails[next];
      if (target) onSelect(target.id);
    },
    [emails, onSelect],
  );

  if (!loading && emails.length === 0) {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
        没有邮件
      </div>
    );
  }

  return (
    <ScrollArea className="h-full">
      <ul className="flex flex-col">
        {emails.map((email, index) => (
          <li key={email.id}>
            <div
              role="button"
              tabIndex={0}
              aria-current={email.id === selectedId ? "true" : undefined}
              onClick={(e) => {
                if (onCheck && (e.ctrlKey || e.metaKey || e.shiftKey || (checked?.size ?? 0) > 0)) {
                  onCheck(email.id, e.shiftKey);
                  return;
                }
                onSelect(email.id);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  onSelect(email.id);
                }
                handleKey(e, index);
              }}
              className={cn(
                "group border-border flex w-full cursor-default gap-2 border-b px-3 py-2.5 text-left transition-colors",
                "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                (email.id === selectedId || checked?.has(email.id)) && "bg-muted",
              )}
            >
              {/* 未读用头像旁的色点而不是整行加粗：整行加粗在长列表里显得噪杂，
                  色点在扫视时反而更容易定位。 */}
              <div className="relative mt-0.5 shrink-0">
                <SenderAvatar
                  email={email.from?.[0]?.email ?? ""}
                  name={email.from?.[0]?.name}
                  className={cn("size-9", onCheck && ((checked?.size ?? 0) > 0 ? "invisible" : "group-hover:invisible"))}
                />
                {onCheck && (
                  <span
                    className={cn(
                      "absolute inset-0 flex items-center justify-center",
                      (checked?.size ?? 0) > 0 ? "visible" : "invisible group-hover:visible",
                    )}
                    onClick={(e) => {
                      e.stopPropagation();
                      onCheck(email.id, e.shiftKey);
                    }}
                  >
                    <Checkbox checked={checked?.has(email.id) ?? false} aria-label="选择邮件" />
                  </span>
                )}
                {email.isUnread && (
                  <span
                    className="bg-accent ring-background absolute -top-0.5 -left-0.5 size-2.5 rounded-full ring-2"
                    aria-label="未读"
                  />
                )}
              </div>

              <div className="min-w-0 flex-1">
                <div className="flex items-baseline gap-2">
                  <span
                    className={cn(
                      "min-w-0 flex-1 truncate text-sm",
                      email.isUnread && "font-medium",
                    )}
                  >
                    {displayAddressList(email.from) || "(无发件人)"}
                  </span>
                  <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                    {listDate(email.receivedAt)}
                  </span>
                </div>

                <div className="mt-0.5 flex items-center gap-1.5">
                  <span
                    className={cn(
                      "min-w-0 flex-1 truncate text-sm",
                      email.isUnread ? "text-foreground" : "text-muted-foreground",
                    )}
                  >
                    {email.subject || "(无主题)"}
                  </span>
                  {email.hasAttachment && (
                    <Paperclip className="text-muted-foreground size-3.5 shrink-0" />
                  )}
                  {onDelete && (
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation();
                        onDelete(email);
                      }}
                      aria-label="删除"
                      title="删除"
                      className="hover:bg-muted text-muted-foreground hover:text-destructive shrink-0 rounded-sm p-0.5 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
                    >
                      <Trash2 className="size-3.5" />
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={(e) => {
                      e.stopPropagation();
                      onToggleFlag(email);
                    }}
                    aria-label={email.isFlagged ? "取消星标" : "加星标"}
                    className="hover:bg-muted shrink-0 rounded-sm p-0.5"
                  >
                    <Star
                      className={cn(
                        "size-3.5",
                        email.isFlagged
                          ? "fill-chart-4 text-chart-4"
                          : "text-muted-foreground opacity-0 group-hover:opacity-100",
                      )}
                    />
                  </button>
                </div>

                <p className="text-muted-foreground mt-0.5 truncate text-xs">
                  {email.preview}
                </p>
              </div>
            </div>
          </li>
        ))}
      </ul>

      {loading && (
        <div className="flex flex-col gap-3 p-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex flex-col gap-1.5">
              <Skeleton className="h-3.5 w-1/3" />
              <Skeleton className="h-3.5 w-2/3" />
            </div>
          ))}
        </div>
      )}

      <div ref={sentinelRef} className="h-1" />
    </ScrollArea>
  );
}
