"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import {
  Fragment,
  useCallback,
  useEffect,
  useImperativeHandle,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Check,
  Paperclip,
  Pin,
  Shield,
  Star,
  Trash2,
} from "lucide-react";

import { ScrollArea } from "@/components/ui/scroll-area";
import { SenderAvatar } from "@/components/sender-avatar";
import { Checkbox } from "@/components/ui/checkbox";
import { AttachmentChips, OtpChip } from "@/components/email-chips";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import { displayAddressList, listDate } from "@/lib/format";
import { api, type EmailSummary } from "@/lib/api";
import { emailKey } from "@/lib/email-key";
import { threadKey, threadRows } from "@/lib/thread-rows";
import { endEmailDrag, onEmailDrag, startEmailDrag } from "@/lib/email-drag";

/** 列表交给 mail-shell 的键盘移动接口: 屏幕上有哪些行 (展开会话里的往来) 只有列表知道。 */
export interface ListNav {
  /**
   * 从选中的那封往下 (delta = 1) 或往上 (-1) 移一行, 返回那一行的邮件; 落在会话行上时像点击一样展开它。
   * selected: 选中的那封不在列表里时 (展开区里别的文件夹的), 由调用方给出, 用来找它所在的会话行。
   */
  move(
    selectedKey: string,
    delta: number,
    selected?: { accountId: string; threadId?: string; id: string } | null,
  ): EmailSummary | null;
}

interface Props {
  emails: EmailSummary[];
  /** 选中邮件的「账号/id」键(见 lib/email-key)。 */
  selectedKey: string;
  loading: boolean;
  hasMore: boolean;
  /** member: 展开会话里的成员行传自己的摘要, 它不在 emails 里 (调用方要据此认出草稿)。 */
  onSelect: (key: string, member?: EmailSummary) => void;
  onLoadMore: () => void;
  onToggleFlag: (email: EmailSummary) => void;
  onDelete?: (email: EmailSummary) => void;
  /** 多选的邮件键。checked 非空时点击行切换勾选而不是打开。 */
  checked?: Set<string>;
  onCheck?: (key: string, range: boolean) => void;
  /** 给行套上右键菜单。 */
  rowMenu?: (email: EmailSummary, row: React.ReactElement) => React.ReactElement;
  /**
   * 给读回来的会话成员套上外壳里还没提交完的乐观更新(删除、已读、星标)。
   * 展开区里的邮件不一定在 emails 里(如已发送的回复), 外壳改不到它们, 只能在这里套。
   */
  overlay?: (list: EmailSummary[]) => EmailSummary[];
  /** 滚动容器挂载/卸载时回调, 供外部回到顶部。 */
  onScrollContainer?: (el: HTMLDivElement | null) => void;
  /** 滚动超过一屏(或回到一屏以内)时回调。 */
  onScrolledAway?: (away: boolean) => void;
  /** 给出时每行标出来源(搜索结果所在文件夹、统一视图里的账号), 返回空串不标。 */
  whereOf?: (email: EmailSummary) => string;
  /** 给出时在头像下方标出邮件所属的邮箱 (统一视图与搜索里的共享邮箱), 返回 null 不标。 */
  accountOf?: (email: EmailSummary) => { label: string; title: string } | null;
  /** 已发送、草稿等文件夹: 头像与名字显示收件人, 发件人都是自己。 */
  showRecipients?: boolean;
  /**
   * 给选中行铺底色, 默认开。窄屏 (手机) 列表与正文一次只显示一栏, 返回列表后
   * 选中行的底色看起来像点按后没褪掉, 由调用方关掉; 选中键照常用于滚动定位。
   */
  highlightSelected?: boolean;
  /** 列表为空时代替「没有邮件」显示的内容 (首次同步还没做完时的进度)。 */
  emptyState?: React.ReactNode;
  /** 行可以拖到侧栏的文件夹上移动 (见 lib/email-drag)。 */
  draggable?: boolean;
  /**
   * 每封邮件各占一行, 不按会话合并、不能展开 (草稿、定时发送: 每封是一件单独待处理的事,
   * 同 Outlook; 合并后较早的草稿藏在会话行下面, 展开区又混进收发的往来)。
   */
  flat?: boolean;
  /** 上下键 / J/K 由 mail-shell 的全局快捷键处理, 通过它问列表下一行是哪封。 */
  navRef?: React.Ref<ListNav>;
}

export function EmailList({
  emails,
  selectedKey,
  loading,
  hasMore,
  highlightSelected = true,
  onSelect,
  onLoadMore,
  onToggleFlag,
  onDelete,
  checked,
  onCheck,
  rowMenu,
  overlay,
  onScrollContainer,
  onScrolledAway,
  whereOf,
  accountOf,
  showRecipients,
  emptyState,
  draggable = false,
  flat = false,
  navRef,
}: Props) {
  useI18n();
  // 正被拖走的邮件键: 这些行变淡, 看得出拖的是哪几封。
  const [dragging, setDragging] = useState<Set<string>>(new Set());
  useEffect(() => onEmailDrag((items) => setDragging(new Set(items?.map((i) => i.key) ?? []))), []);
  // 拖的行在多选里时拖走整个多选, 否则只拖这一封 (与右键菜单作用于谁一致)。
  const dragProps = (email: EmailSummary) =>
    draggable
      ? {
          draggable: true,
          onDragStart: (e: React.DragEvent) => {
            const sel = checked ?? new Set<string>();
            const batch = sel.has(emailKey(email)) && sel.size > 1;
            startEmailDrag(e, batch ? emails.filter((m) => sel.has(emailKey(m))) : [email]);
          },
          onDragEnd: endEmailDrag,
        }
      : {};
  const sentinelRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const setScrollEl = useCallback(
    (el: HTMLDivElement | null) => {
      scrollRef.current = el;
      onScrollContainer?.(el);
    },
    [onScrollContainer],
  );

  // 骨架屏与空列表时没有滚动容器, 切换文件夹后容器重新挂载在顶部; 列表每次变化都重新报一次。
  const reportScroll = useCallback(() => {
    const el = scrollRef.current;
    onScrolledAway?.(!!el && el.scrollTop > el.clientHeight);
  }, [onScrolledAway]);
  useEffect(reportScroll, [reportScroll, emails, loading]);
  const wrapRow = rowMenu ?? ((_: EmailSummary, row: React.ReactElement) => row);
  const applyOverlay = overlay ?? ((list: EmailSummary[]) => list);
  // 同一时间只展开一个会话, 键为「账号/会话 id」(统一视图里不同账号的会话 id 可能相同)。
  const [expanded, setExpanded] = useState("");
  const [threads, setThreads] = useState<Record<string, EmailSummary[]>>({});

  const rows = useMemo(() => threadRows(emails, flat), [emails, flat]);


  // 列表刷新(新邮件到达、标记已读)后, 展开的会话重新读取。
  // 按会话行代表的那封读取: 它在回收站之外时, 删到回收站的往来不再列出。
  useEffect(() => {
    if (!expanded) return;
    const head = rows.find((r) => threadKey(r.head) === expanded)?.head;
    if (!head) return;
    let cancelled = false;
    api
      .threadEmails(head.accountId, head.id)
      .catch(() => [])
      .then((list) => {
        if (!cancelled) setThreads({ [expanded]: applyOverlay(list ?? []) });
      });
    return () => {
      cancelled = true;
    };
    // expanded 变化由 expandThread 自己读取, 这里只跟随列表刷新。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [emails]);

  function toggleThread(email: EmailSummary) {
    if (expanded === threadKey(email)) setExpanded("");
    else expandThread(email);
  }

  // 打开一行时只留它自己的会话展开: 是多封的会话就展开(会话行只代表最新一封, 不展开就看不到
  // 也不会去点较早的未读), 单封邮件则收起之前展开的会话。
  async function expandThread(email: EmailSummary) {
    const key = threadKey(email);
    if (expanded === key) return;
    if (flat || !email.threadId || (email.threadCount ?? 0) < 2) {
      setExpanded("");
      return;
    }
    setExpanded(key);
    // 先显示上次读到的成员, 再重新读一次: 会话收起期间可能多了往来 (例如离开写信面板时存下的草稿),
    // 那时的列表刷新不会重读收起的会话。
    const list = (await api.threadEmails(email.accountId, email.id).catch(() => [])) ?? [];
    setThreads((prev) => ({ ...prev, [key]: applyOverlay(list) }));
  }

  /** 键盘移动前焦点在列表的某一行上: 那一行随会话收起被卸掉时, 焦点仍要跟到新选中的行。 */
  const keyboardInList = useRef(false);
  // 键盘上下移动按屏幕上的行走, 同 Outlook: 落到会话行就展开它 (与点击一样, 同时收起别的会话),
  // 展开的会话里逐封走, 包括别的文件夹里的往来。会话行代表的那封在展开区里也有一行, 不重复走。
  useImperativeHandle(navRef, () => ({
    move(selectedKey, delta, selected) {
      keyboardInList.current = !!scrollRef.current?.contains(document.activeElement);
      const visible: { email: EmailSummary; head: boolean }[] = [];
      for (const { head } of rows) {
        visible.push({ email: head, head: true });
        const tk = threadKey(head);
        if (flat || !head.threadId || expanded !== tk) continue;
        for (const m of [...(threads[tk] ?? [])].reverse()) {
          if (emailKey(m) !== emailKey(head)) visible.push({ email: m, head: false });
        }
      }
      let idx = visible.findIndex((v) => emailKey(v.email) === selectedKey);
      // 选中的是收起的会话里的一封 (较早的、别的文件夹的): 从代表该会话的行起步, 否则会跳回列表第一行。
      if (idx < 0) {
        const sel = emails.find((m) => emailKey(m) === selectedKey) ?? selected;
        if (sel) idx = visible.findIndex((v) => v.head && threadKey(v.email) === threadKey(sel));
      }
      const next = visible[idx < 0 ? 0 : idx + delta];
      if (!next) {
        keyboardInList.current = false;
        return null;
      }
      if (next.head) expandThread(next.email);
      return next.email;
    },
  }));

  // 从通讯录、通知或链接打开的邮件可能在屏幕外, 选中变化后把那一行滚进视野。
  // block: "nearest" 在行已经可见时什么都不做, 所以点击行本身不会引起跳动;
  // 记下滚过的键, 列表刷新(新邮件、标记已读)时不把用户滚回去。
  const scrolledTo = useRef("");
  /** 选中的邮件在列表上的那一行; 是会话里较早的一封时, 列表上只有代表整个会话的那一行。 */
  const rowOf = useCallback(
    (key: string): HTMLElement | null => {
      const root = scrollRef.current;
      if (!root) return null;
      const find = (k: string) => root.querySelector<HTMLElement>(`[data-email-key="${CSS.escape(k)}"]`);
      const target = emails.find((e) => emailKey(e) === key);
      const tk = target ? threadKey(target) : "";
      const head = tk ? rows.find(({ head: h }) => threadKey(h) === tk)?.head : undefined;
      return find(key) ?? (head ? find(emailKey(head)) : null);
    },
    [emails, rows],
  );
  useEffect(() => {
    if (!selectedKey || scrolledTo.current === selectedKey) return;
    const row = rowOf(selectedKey);
    if (!row) return; // 还没加载到那一页, 等列表到了再滚。
    scrolledTo.current = selectedKey;
    row.scrollIntoView({ block: "nearest" });
  }, [selectedKey, rowOf, threads]);

  // 上下键与 J/K 一样由 mail-shell 的全局快捷键处理 (键位可在设置里改), 行本身不再处理。
  // 点过某一行后焦点留在那一行上; 选中换到别处时焦点跟过去, 否则焦点框停在旧行上,
  // 下一次按 Enter 也会打开旧行。焦点不在列表的行上 (搜索框、阅读栏) 时不抢焦点。
  const focusedFor = useRef("");
  useEffect(() => {
    if (!selectedKey || focusedFor.current === selectedKey) return;
    const root = scrollRef.current;
    const active = document.activeElement;
    const inList = active instanceof HTMLElement && !!active.dataset.emailKey && !!root?.contains(active);
    if (!root || (!inList && !keyboardInList.current)) {
      focusedFor.current = selectedKey;
      keyboardInList.current = false;
      return;
    }
    const row = rowOf(selectedKey);
    if (!row) return;
    focusedFor.current = selectedKey;
    keyboardInList.current = false;
    if (row !== active) row.focus({ preventScroll: true });
  }, [selectedKey, rowOf, threads]);

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

  if (!loading && emails.length === 0) {
    if (emptyState) return <>{emptyState}</>;
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center text-sm">
        {t("没有邮件")}</div>
    );
  }

  return (
    <ScrollArea ref={setScrollEl} onScroll={reportScroll} className="h-full overflow-x-hidden">
      <ul className="flex flex-col">
        {rows.map(({ head: email, unread }) => {
          const key = emailKey(email);
          const tk = threadKey(email);
          const count = flat ? 0 : (email.threadCount ?? 0);
          // 会话里有草稿 (多半是没写完的回复, 可能在草稿箱里) 时会话行标红, 同 Outlook; 草稿数含本行自己。
          const drafts = Math.max(flat ? 0 : (email.draftCount ?? 0), email.isDraft ? 1 : 0);
          const isOpen = !flat && !!email.threadId && expanded === tk;
          const members = isOpen ? [...(threads[tk] ?? [])].reverse() : [];
          const where = whereOf?.(email) ?? "";
          const account = accountOf?.(email) ?? null;
          const isChecked = checked?.has(key) ?? false;
          // 收件人为空的草稿退回显示发件人。
          const party = showRecipients && email.to?.length ? email.to : email.from;
          const childSelected =
            highlightSelected && members.some((m) => emailKey(m) === selectedKey && m.id !== email.id);
          // 多选时会话行左侧多一列勾选框, 成员行也要同样右移, 串联细线才能对齐头像。
          const multi = !!onCheck && (checked?.size ?? 0) > 0;
          return (
            <li key={key}>
              {wrapRow(
                email,
                <div
                  role="button"
                  tabIndex={0}
                  data-email-key={key}
                  aria-current={key === selectedKey ? "true" : undefined}
                  {...dragProps(email)}
                  onClick={(e) => {
                    if (
                      onCheck &&
                      (e.ctrlKey ||
                        e.metaKey ||
                        e.shiftKey ||
                        (checked?.size ?? 0) > 0)
                    ) {
                      onCheck(key, e.shiftKey);
                      return;
                    }
                    onSelect(key);
                    expandThread(email);
                  }}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      onSelect(key);
                      expandThread(email);
                    }
                  }}
                  className={cn(
                    "group border-border flex w-full cursor-default gap-2 border-b px-3 py-2.5 text-left transition-colors",
                    "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                    ((highlightSelected && key === selectedKey) || isChecked) && "bg-muted",
                    childSelected && "bg-secondary",
                    dragging.has(key) && "opacity-40",
                  )}
                >
                  {/* 未读用头像旁的色点而不是整行加粗：整行加粗在长列表里显得噪杂，
                    色点在扫视时反而更容易定位。 */}
                  {multi && (
                    <span
                      className="mt-2.5 -ml-1 shrink-0"
                      onClick={(e) => {
                        e.stopPropagation();
                        onCheck(key, e.shiftKey);
                      }}
                    >
                      <Checkbox
                        checked={isChecked}
                        aria-label={t("选择邮件")}
                        tabIndex={-1}
                      />
                    </span>
                  )}
                  {/* 头像列撑满行高: 会话展开时, 头像下方接出串联成员的细线, 起点跟着头像走, 不依赖其它元素的高度。 */}
                  <div className="mt-0.5 flex shrink-0 flex-col items-center">
                    <div className="relative">
                      {/* 头像就是选择按钮(同 Bulwark): 鼠标移到头像上才变成对勾, 移到行的其它位置头像照常显示;
                          选中的邮件头像保持对勾, 进入多选后左侧再出现一列勾选框。 */}
                      <button
                        type="button"
                        title={onCheck ? t("选择") : undefined}
                        aria-label={t("选择邮件")}
                        aria-pressed={isChecked}
                        disabled={!onCheck}
                        onClick={(e) => {
                          e.stopPropagation();
                          onCheck?.(key, e.shiftKey);
                        }}
                        className="group/avatar relative block size-9 rounded-full"
                      >
                        <SenderAvatar
                          email={party?.[0]?.email ?? ""}
                          name={party?.[0]?.name}
                          className="size-9"
                        />
                        {onCheck && (
                          <span
                            className={cn(
                              "bg-accent text-accent-foreground absolute inset-0 flex items-center justify-center rounded-full transition-opacity",
                              isChecked
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
                          aria-label={t("未读")}
                        />
                      )}
                    </div>
                    {/* 共享邮箱标记放头像下方, 不占主题行; 负外边距让它最宽可比头像多 8px 而不撑宽头像列 (串联细线按头像列定位)。 */}
                    {account && (
                      <span
                        title={account.title}
                        className="bg-secondary text-muted-foreground -mx-1 mt-1 max-w-11 truncate rounded-sm px-1 text-[10px] leading-4"
                      >
                        {account.label}
                      </span>
                    )}
                    {/* 不能靠 items-center 居中: 36px 列里 1px 线落在 17.5px, 与下方成员行的 30px 细线错开半像素, 看着是歪的。 */}
                    {isOpen && members.length > 0 && (
                      <span aria-hidden className="bg-muted-foreground/30 -mb-2.5 ml-[18px] w-px flex-1 self-start" />
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
                        {showRecipients && email.to?.length
                          ? t("致：{0}", displayAddressList(email.to))
                          : displayAddressList(email.from) || t("(无发件人)")}
                      </span>
                      {count > 1 && (
                        <button
                          type="button"
                          aria-label={
                            isOpen ? t("收起会话") : t("展开会话，共 {0} 封", count)
                          }
                          aria-expanded={isOpen}
                          title={
                            isOpen ? t("收起会话") : t("展开会话（共 {0} 封）", count)
                          }
                          onClick={(e) => {
                            e.stopPropagation();
                            toggleThread(email);
                          }}
                          className={cn(
                            "flex h-4 shrink-0 items-center rounded-full px-1.5 text-[11px] leading-none tabular-nums transition-colors",
                            isOpen
                              ? "bg-accent/15 text-accent"
                              : "bg-muted text-muted-foreground hover:text-foreground",
                          )}
                        >
                          {count}
                        </button>
                      )}
                      {email.hasAttachment && (
                        <Paperclip className="text-muted-foreground size-3.5 shrink-0" aria-label={t("附件")} />
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
                        {drafts > 0 && (
                          <span className="text-destructive">
                            {drafts > 1 ? t("[草稿：{0}]", drafts) : t("[草稿]")}{" "}
                          </span>
                        )}
                        {email.subject || t("(无主题)")}
                      </span>
                      {where && (
                        <span
                          className="bg-secondary text-muted-foreground max-w-32 shrink-0 truncate rounded-sm px-1.5 text-xs"
                          title={where}
                        >
                          {where}
                        </span>
                      )}
                      {email.signed && (
                        <Shield className="text-muted-foreground size-3.5 shrink-0" aria-label={t("数字签名")} />
                      )}
                      {email.isPinned && (
                        <Pin className="text-accent size-3.5 shrink-0" aria-label={t("已固定")} />
                      )}
                      {onDelete && (
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            onDelete(email);
                          }}
                          aria-label={t("删除")}
                          title={t("删除")}
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
                        aria-label={email.isFlagged ? t("取消星标") : t("加星标")}
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

                    {/* 验证码与附件名单独一行, 只在有的时候出现; 一行放不下时附件名先缩, 不换行。 */}
                    {(email.code || (email.attachmentNames?.length ?? 0) > 0) && (
                      <div className="mt-1.5 flex min-w-0 items-center gap-1.5 overflow-hidden">
                        {email.code && <OtpChip code={email.code} />}
                        {email.attachmentNames && email.attachmentNames.length > 0 && (
                          <AttachmentChips names={email.attachmentNames} />
                        )}
                      </div>
                    )}
                  </div>
                </div>,
              )}
              {isOpen && (
                <ul className="border-border bg-secondary/40 border-b py-1">
                  {members.length === 0 && (
                    <li className="text-muted-foreground px-12 py-2 text-xs">
                      {t("载入中…")}</li>
                  )}
                  {members.map((m, i) => (
                    <Fragment key={emailKey(m)}>
                      <li className="relative">
                        <ThreadRail
                          first={i === 0}
                          last={i === members.length - 1}
                          selected={highlightSelected && emailKey(m) === selectedKey}
                          inset={multi}
                        />
                        {/* 与会话行一样可以右键、加星标、删除; 行里有按钮, 所以外层不能是 button。 */}
                        {wrapRow(
                          m,
                          <div
                            role="button"
                            tabIndex={0}
                            data-email-key={emailKey(m)}
                            {...dragProps(m)}
                            onClick={() => onSelect(emailKey(m), m)}
                            onKeyDown={(e) => {
                              if (e.target !== e.currentTarget) return;
                              if (e.key === "Enter" || e.key === " ") {
                                e.preventDefault();
                                onSelect(emailKey(m), m);
                              }
                            }}
                            aria-current={
                              emailKey(m) === selectedKey ? "true" : undefined
                            }
                            className={cn(
                              "group/member flex w-full cursor-default items-center gap-2 py-1.5 pr-3 text-left text-sm",
                              multi ? "pl-[68px]" : "pl-12",
                              "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                              highlightSelected && emailKey(m) === selectedKey
                                ? "bg-muted"
                                : "hover:bg-secondary",
                              dragging.has(emailKey(m)) && "opacity-40",
                            )}
                          >
                            {/* 未读与会话行一样在头像角上标色点, 名字加粗、摘要用正文色; 只靠字重在小字里看不出来。 */}
                            <span className="relative shrink-0">
                              <SenderAvatar
                                email={m.from?.[0]?.email ?? ""}
                                name={m.from?.[0]?.name}
                                className="size-5 text-[9px]"
                              />
                              {m.isUnread && (
                                <span
                                  className="bg-accent ring-background absolute -top-0.5 -left-0.5 size-2 rounded-full ring-2"
                                  aria-label={t("未读")}
                                />
                              )}
                            </span>
                            <span
                              className={cn(
                                "w-24 shrink-0 truncate",
                                m.isUnread && "font-semibold",
                              )}
                            >
                              {m.from?.[0]?.name ||
                                m.from?.[0]?.email ||
                                t("(无发件人)")}
                            </span>
                            <span
                              className={cn(
                                "min-w-0 flex-1 truncate text-xs",
                                m.isUnread ? "text-foreground" : "text-muted-foreground",
                              )}
                            >
                              {m.isDraft && <span className="text-destructive">{t("[草稿]")} </span>}
                              {m.preview}
                            </span>
                            {m.hasAttachment && (
                              <Paperclip className="text-muted-foreground size-3 shrink-0" />
                            )}
                            {m.signed && (
                              <Shield className="text-muted-foreground size-3 shrink-0" aria-label={t("数字签名")} />
                            )}
                            {onDelete && (
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  onDelete(m);
                                }}
                                aria-label={t("删除")}
                                title={t("删除")}
                                className="hover:bg-muted text-muted-foreground hover:text-destructive shrink-0 rounded-sm p-0.5 opacity-0 group-hover/member:opacity-100 focus-visible:opacity-100"
                              >
                                <Trash2 className="size-3.5" />
                              </button>
                            )}
                            <button
                              type="button"
                              onClick={(e) => {
                                e.stopPropagation();
                                onToggleFlag(m);
                              }}
                              aria-label={m.isFlagged ? t("取消星标") : t("加星标")}
                              className="hover:bg-muted shrink-0 rounded-sm p-0.5"
                            >
                              <Star
                                className={cn(
                                  "size-3.5",
                                  m.isFlagged
                                    ? "fill-chart-4 text-chart-4"
                                    : "text-muted-foreground opacity-0 group-hover/member:opacity-100",
                                )}
                              />
                            </button>
                            <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                              {listDate(m.receivedAt)}
                            </span>
                          </div>,
                        )}
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

/**
 * 展开会话里串起成员行的细线, 接着会话行头像下方那段往下走, 最后一封只画到行中点。
 * 水平位置对准会话行头像中心: 行左内边距 12px + 半个头像 18px = 30px;
 * 多选时会话行多出勾选框列 (16px 宽、-4px 外边距、8px 间距), 整体右移 20px, 成员行跟着右移。
 * 首行往上多画 5px, 盖住成员区 4px 上内边距和会话行 1px 底边框。
 * 选中的成员在线上盖一段 3px 强调色竖条。
 */
function ThreadRail({
  first,
  last,
  selected,
  inset,
}: {
  first: boolean;
  last: boolean;
  selected: boolean;
  inset: boolean;
}) {
  return (
    <>
      <span
        aria-hidden
        className={cn(
          "bg-muted-foreground/30 pointer-events-none absolute w-px",
          inset ? "left-[50px]" : "left-[30px]",
          first ? "-top-[5px]" : "top-0",
          last ? "bottom-1/2" : "bottom-0",
        )}
      />
      {selected && (
        <span
          aria-hidden
          className={cn(
            "bg-accent pointer-events-none absolute top-1 bottom-1 w-[3px] rounded-full",
            inset ? "left-[49px]" : "left-[29px]",
          )}
        />
      )}
    </>
  );
}
