"use client";

import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Check,
  ChevronDown,
  MessagesSquare,
  Paperclip,
  Pin,
  Star,
  Trash2,
} from "lucide-react";

import { ScrollArea } from "@/components/ui/scroll-area";
import { SenderAvatar } from "@/components/sender-avatar";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { displayAddressList, listDate } from "@/lib/format";
import { api, type EmailSummary } from "@/lib/api";

interface Props {
  /** 会话展开时按这个账号读取会话里的全部邮件。 */
  accountId: string;
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
  /** 给行套上右键菜单。 */
  rowMenu?: (email: EmailSummary, row: React.ReactElement) => React.ReactElement;
}

export function EmailList({
  accountId,
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
  rowMenu,
}: Props) {
  const sentinelRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const wrapRow = rowMenu ?? ((_: EmailSummary, row: React.ReactElement) => row);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [threads, setThreads] = useState<Record<string, EmailSummary[]>>({});

  // 同一会话在列表里只占一行(最新那封), 其余收进展开区。
  const rows = useMemo(() => {
    const byThread = new Map<string, { head: EmailSummary; unread: boolean }>();
    const out: { head: EmailSummary; unread: boolean }[] = [];
    for (const e of emails) {
      const key = e.threadId || e.id;
      const existing = byThread.get(key);
      if (existing) {
        existing.unread ||= e.isUnread;
        // 会话行始终代表其中最新的一封。
        if (new Date(e.receivedAt).getTime() > new Date(existing.head.receivedAt).getTime()) existing.head = e;
        continue;
      }
      const row = { head: e, unread: e.isUnread };
      byThread.set(key, row);
      out.push(row);
    }
    return out;
  }, [emails]);

  // 列表刷新(新邮件到达、标记已读)后, 已展开的会话重新读取。
  useEffect(() => {
    if (expanded.size === 0) return;
    let cancelled = false;
    Promise.all(
      [...expanded].map(
        async (tid) =>
          [
            tid,
            (await api.threadEmails(accountId, tid).catch(() => [])) ?? [],
          ] as const,
      ),
    ).then((entries) => {
      if (!cancelled) setThreads(Object.fromEntries(entries));
    });
    return () => {
      cancelled = true;
    };
    // expanded 变化由 toggleThread 自己读取, 这里只跟随列表刷新。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [emails, accountId]);

  async function toggleThread(threadId: string) {
    const next = new Set(expanded);
    if (next.has(threadId)) {
      next.delete(threadId);
      setExpanded(next);
      return;
    }
    next.add(threadId);
    setExpanded(next);
    if (!threads[threadId]) {
      const list =
        (await api.threadEmails(accountId, threadId).catch(() => [])) ?? [];
      setThreads((prev) => ({ ...prev, [threadId]: list }));
    }
  }

  // 从通讯录、通知或链接打开的邮件可能在屏幕外, 选中变化后把那一行滚进视野。
  // block: "nearest" 在行已经可见时什么都不做, 所以点击行本身不会引起跳动;
  // 记下滚过的 id, 列表刷新(新邮件、标记已读)时不把用户滚回去。
  const scrolledTo = useRef("");
  useEffect(() => {
    if (!selectedId || scrolledTo.current === selectedId) return;
    const root = scrollRef.current;
    if (!root) return;
    const find = (id: string) => root.querySelector(`[data-email-id="${CSS.escape(id)}"]`);
    // 选中的是会话里较早的一封时, 列表上只有代表整个会话的那一行, 滚到它。
    const target = emails.find((e) => e.id === selectedId);
    const key = target ? target.threadId || target.id : "";
    const head = key ? rows.find(({ head: h }) => (h.threadId || h.id) === key)?.head.id : "";
    const row = find(selectedId) ?? (head ? find(head) : null);
    if (!row) return; // 还没加载到那一页, 等列表到了再滚。
    scrolledTo.current = selectedId;
    row.scrollIntoView({ block: "nearest" });
  }, [selectedId, emails, rows, threads]);

  // 触底加载。用 IntersectionObserver 而不是监听 scroll 事件：
  // scroll 在快速滚动时每帧都触发，而这里只需要知道「哨兵进入视口」这一个事实。
  // root 必须是列表自己的滚动容器: 以窗口为 root 时哨兵先被容器裁掉, rootMargin 不起作用,
  // 要真正滚到底才触发。距底部约一屏半就预加载下一页, 正常滚动碰不到加载态。
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el || !hasMore || loading) return;

    const io = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting) onLoadMore();
      },
      { root: scrollRef.current, rootMargin: "0px 0px 1200px 0px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [hasMore, loading, onLoadMore]);

  const handleKey = useCallback(
    (e: React.KeyboardEvent, index: number) => {
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      e.preventDefault();
      const next = e.key === "ArrowDown" ? index + 1 : index - 1;
      const target = rows[next]?.head;
      if (target) onSelect(target.id);
    },
    [rows, onSelect],
  );

  if (!loading && emails.length === 0) {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
        没有邮件
      </div>
    );
  }

  return (
    <ScrollArea ref={scrollRef} className="h-full overflow-x-hidden">
      <ul className="flex flex-col">
        {rows.map(({ head: email, unread }, index) => {
          const tid = email.threadId;
          const count = email.threadCount ?? 0;
          const isOpen = !!tid && expanded.has(tid);
          const members = isOpen ? [...(threads[tid] ?? [])].reverse() : [];
          const childSelected = members.some(
            (m) => m.id === selectedId && m.id !== email.id,
          );
          return (
            <li key={email.id}>
              {wrapRow(
                email,
                <div
                  role="button"
                  tabIndex={0}
                  data-email-id={email.id}
                  aria-current={email.id === selectedId ? "true" : undefined}
                  onClick={(e) => {
                    if (
                      onCheck &&
                      (e.ctrlKey ||
                        e.metaKey ||
                        e.shiftKey ||
                        (checked?.size ?? 0) > 0)
                    ) {
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
                    (email.id === selectedId || checked?.has(email.id)) &&
                      "bg-muted",
                    childSelected && "bg-secondary",
                  )}
                >
                  {/* 未读用头像旁的色点而不是整行加粗：整行加粗在长列表里显得噪杂，
                    色点在扫视时反而更容易定位。 */}
                  {onCheck && (checked?.size ?? 0) > 0 && (
                    <span
                      className="mt-2.5 -ml-1 shrink-0"
                      onClick={(e) => {
                        e.stopPropagation();
                        onCheck(email.id, e.shiftKey);
                      }}
                    >
                      <Checkbox
                        checked={checked?.has(email.id) ?? false}
                        aria-label="选择邮件"
                        tabIndex={-1}
                      />
                    </span>
                  )}
                  <div className="relative mt-0.5 shrink-0">
                    {/* 头像就是选择按钮(同 Bulwark): 鼠标移到头像上才变成对勾, 移到行的其它位置头像照常显示;
                        选中的邮件头像保持对勾, 进入多选后左侧再出现一列勾选框。 */}
                    <button
                      type="button"
                      title={onCheck ? "选择" : undefined}
                      aria-label="选择邮件"
                      aria-pressed={checked?.has(email.id) ?? false}
                      disabled={!onCheck}
                      onClick={(e) => {
                        e.stopPropagation();
                        onCheck?.(email.id, e.shiftKey);
                      }}
                      className="group/avatar relative block size-9 rounded-full"
                    >
                      <SenderAvatar
                        email={email.from?.[0]?.email ?? ""}
                        name={email.from?.[0]?.name}
                        className="size-9"
                      />
                      {onCheck && (
                        <span
                          className={cn(
                            "bg-accent text-accent-foreground absolute inset-0 flex items-center justify-center rounded-full transition-opacity",
                            checked?.has(email.id)
                              ? "opacity-100"
                              : "opacity-0 group-hover/avatar:opacity-100",
                          )}
                        >
                          <Check className="size-5" strokeWidth={2.5} />
                        </span>
                      )}
                    </button>
                    {unread && (
                      <span
                        className="bg-accent ring-background absolute -top-0.5 -left-0.5 size-2.5 rounded-full ring-2"
                        aria-label="未读"
                      />
                    )}
                  </div>

                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span
                        className={cn(
                          "min-w-0 flex-1 truncate text-sm",
                          unread && "font-medium",
                        )}
                      >
                        {displayAddressList(email.from) || "(无发件人)"}
                      </span>
                      {count > 1 && (
                        <button
                          type="button"
                          aria-label={
                            isOpen ? "收起会话" : `展开会话，共 ${count} 封`
                          }
                          aria-expanded={isOpen}
                          title={
                            isOpen ? "收起会话" : `展开会话（共 ${count} 封）`
                          }
                          onClick={(e) => {
                            e.stopPropagation();
                            toggleThread(tid);
                          }}
                          className={cn(
                            "flex h-6 shrink-0 items-center gap-1 rounded-md border px-1.5 text-xs tabular-nums transition-colors",
                            isOpen
                              ? "border-accent bg-card text-foreground"
                              : "border-border text-muted-foreground hover:border-accent hover:text-foreground",
                          )}
                        >
                          <MessagesSquare className="size-3.5" />
                          {count}
                          <ChevronDown
                            className={cn(
                              "size-3.5 transition-transform",
                              isOpen && "rotate-180",
                            )}
                          />
                        </button>
                      )}
                      <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                        {listDate(email.receivedAt)}
                      </span>
                    </div>

                    <div className="mt-0.5 flex items-center gap-1.5">
                      <span
                        className={cn(
                          "min-w-0 flex-1 truncate text-sm",
                          unread ? "text-foreground" : "text-muted-foreground",
                        )}
                      >
                        {email.subject || "(无主题)"}
                      </span>
                      {email.hasAttachment && (
                        <Paperclip className="text-muted-foreground size-3.5 shrink-0" />
                      )}
                      {email.isPinned && (
                        <Pin className="text-accent size-3.5 shrink-0" aria-label="已固定" />
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
                </div>,
              )}
              {isOpen && (
                <ul className="border-border bg-secondary/40 border-b py-1">
                  {members.length === 0 && (
                    <li className="text-muted-foreground px-12 py-2 text-xs">
                      载入中…
                    </li>
                  )}
                  {members.map((m) => (
                    <Fragment key={m.id}>
                      <li>
                        <button
                          type="button"
                          data-email-id={m.id}
                          onClick={() => onSelect(m.id)}
                          aria-current={
                            m.id === selectedId ? "true" : undefined
                          }
                          className={cn(
                            "flex w-full items-center gap-2 py-1.5 pr-3 pl-12 text-left text-sm",
                            m.id === selectedId
                              ? "bg-muted"
                              : "hover:bg-secondary",
                          )}
                        >
                          <SenderAvatar
                            email={m.from?.[0]?.email ?? ""}
                            name={m.from?.[0]?.name}
                            className="size-5 text-[9px]"
                          />
                          <span
                            className={cn(
                              "w-24 shrink-0 truncate",
                              m.isUnread && "font-medium",
                            )}
                          >
                            {m.from?.[0]?.name ||
                              m.from?.[0]?.email ||
                              "(无发件人)"}
                          </span>
                          <span className="text-muted-foreground min-w-0 flex-1 truncate text-xs">
                            {m.preview}
                          </span>
                          {m.hasAttachment && (
                            <Paperclip className="text-muted-foreground size-3 shrink-0" />
                          )}
                          <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                            {listDate(m.receivedAt)}
                          </span>
                        </button>
                      </li>
                    </Fragment>
                  ))}
                </ul>
              )}
            </li>
          );
        })}
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
