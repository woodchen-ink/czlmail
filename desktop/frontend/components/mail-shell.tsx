"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Archive,
  ArrowUpToLine,
  FolderInput,
  Loader2,
  MailOpen,
  MoreHorizontal,
  Mail as MailIcon,
  PenSquare,
  RefreshCw,
  Search,
  ShieldAlert,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ComposePane, type ComposeDraft } from "@/components/compose/compose-pane";
import { EmailContextMenu, type EmailMenuActions } from "@/components/email-context-menu";
import { EmailList } from "@/components/email-list";
import { EmailView } from "@/components/email-view";
import { MailSidebar, UNIFIED_UNREAD } from "@/components/mail-sidebar";
import { mailboxLabel } from "@/components/mailbox-tree";
import { MobileNavButton, PaneLayout, type MobilePane } from "@/components/pane-layout";
import { useCompact } from "@/lib/use-compact";
import { ShortcutsDialog } from "@/components/shortcuts-dialog";
import { SourceDialog } from "@/components/source-dialog";
import type { ToolbarActions } from "@/components/email-toolbar";
import { UserMenu } from "@/components/user-menu";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Account,
  type EmailDetail,
  type EmailSummary,
  type Mailbox,
} from "@/lib/api";
import { displayAddressList, fullDate } from "@/lib/format";
import { printEmail } from "@/lib/print";
import { useAppearance } from "@/lib/theme";
import { listen, takePendingOpenEmail } from "@/lib/app-bus";
import { escapeHtml, splitDraftHtml, textToHtml } from "@/lib/html";
import { emailKey, groupByAccount, parseEmailKey } from "@/lib/email-key";

const PAGE_SIZE = 50;
// 定位一封邮件时最多加载多少封。再深就不值得了: 每 200 封一次查询,
// 列表也长到滚动条没有意义, 这时只打开邮件、不强求列表停在它上面。
const LOCATE_LIMIT = 2000;

/** 为了让第 index 封出现在列表里, 需要加载的封数(按整页取, 免得刚好停在页边)。 */
function locateDepth(index: number): number {
  return Math.min(LOCATE_LIMIT, (Math.floor(index / PAGE_SIZE) + 1) * PAGE_SIZE);
}

export function MailShell({ onSignedOut }: { onSignedOut: () => void }) {
  useI18n();
  const { bodyMode, bodyAppearance, setBodyAppearance } = useAppearance();

  const [username, setUsername] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [mailboxesByAccount, setMailboxesByAccount] = useState<Record<string, Mailbox[]>>({});
  const [accountId, setAccountId] = useState("");
  const [mailboxId, setMailboxId] = useState("");
  // 统一视图: 所有账号的同类文件夹(或全部未读)合成一个列表。值是文件夹类别或 "unread",
  // 空串表示在看 accountId/mailboxId 这个文件夹。
  const [unified, setUnified] = useState("");
  const [labelsByAccount, setLabelsByAccount] = useState<Record<string, string[]>>({});

  const [emails, setEmails] = useState<EmailSummary[]>([]);
  // 选中的邮件与多选一律用「账号/id」键(lib/email-key): 统一视图里各账号的邮件 id 可能重复。
  const [selectedKey, setSelectedKey] = useState("");
  // 多选的邮件; 切换文件夹或搜索时清空。
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const lastChecked = useRef("");
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [revision, setRevision] = useState(0);

  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  // 本地结果已出、正在等服务端全文检索补齐。
  const [searchingServer, setSearchingServer] = useState(false);
  // 邮件列表滚离顶部后, 刷新按钮换成「回到顶部」。
  const listScrollRef = useRef<HTMLDivElement | null>(null);
  const setListScrollEl = useCallback((el: HTMLDivElement | null) => {
    listScrollRef.current = el;
  }, []);
  const [scrolledAway, setScrolledAway] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [draft, setDraftState] = useState<ComposeDraft | null>(null);
  // 每次打开写信面板都换一个 key, 让面板重新挂载而不是沿用上一封的状态。
  const [draftKey, setDraftKey] = useState(0);
  const setDraft = useCallback((d: ComposeDraft | null) => {
    setDraftState(d);
    if (d) setDraftKey((k) => k + 1);
  }, []);
  const [focusMode, setFocusMode] = useState(false);
  // 窄屏下显示哪一栏。选中的邮件一变 (点了列表、通知或链接打开、删除后跳到下一封) 就进正文;
  // 返回列表时保留选中, 回来还能看到刚才那一行。用渲染期比较而不是 effect, 避免先闪一帧列表。
  const compact = useCompact();
  const [mobileView, setMobileView] = useState<MobilePane>("list");
  const [seenKey, setSeenKey] = useState(selectedKey);
  if (seenKey !== selectedKey) {
    setSeenKey(selectedKey);
    if (selectedKey) setMobileView("detail");
  }
  const mobilePane: MobilePane = draft ? "detail" : mobileView === "detail" && !selectedKey ? "list" : mobileView;
  const [showShortcuts, setShowShortcuts] = useState(false);
  const [sourceTarget, setSourceTarget] = useState<{ accountId: string; emailId: string } | null>(null);

  // 当前打开的邮件，供键盘快捷键使用。
  const currentEmail = useRef<EmailDetail | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // 每次切换账号/邮箱都会重来一遍分页，用一个自增的令牌把过期请求的结果丢掉，
  // 否则慢的那次返回会覆盖快的那次，列表显示成上一个邮箱的内容。
  const loadToken = useRef(0);

  const mailboxes = useMemo(() => mailboxesByAccount[accountId] ?? [], [mailboxesByAccount, accountId]);
  const currentKind = unified
    ? unified === UNIFIED_UNREAD
      ? ""
      : unified
    : (mailboxes.find((m) => m.id === mailboxId)?.kind ?? "");
  const selected = parseEmailKey(selectedKey);
  // 新写的信从哪个账号发: 在看某个账号的文件夹时用它, 统一视图里用个人账号。
  const composeAccount = unified ? (accounts.find((a) => a.isPersonal)?.id ?? accountId) : accountId;

  /**
   * 列表里某个账号的邮件所在的文件夹: 文件夹视图是当前文件夹, 统一视图是该账号同类别的文件夹,
   * 未读视图没有单一的文件夹(返回空串)。用于调整侧栏未读数、「移动到」里排除当前文件夹。
   */
  const listBoxOf = useCallback(
    (acc: string) => {
      if (!unified) return acc === accountId ? mailboxId : "";
      if (unified === UNIFIED_UNREAD) return "";
      return mailboxesByAccount[acc]?.find((m) => m.kind === unified)?.id ?? "";
    },
    [unified, accountId, mailboxId, mailboxesByAccount],
  );

  /* ---------- 账号与邮箱 ---------- */

  const loadMailboxes = useCallback(async (account: string) => {
    if (!account) return [];
    try {
      const list = (await api.listMailboxes(account)) ?? [];
      setMailboxesByAccount((prev) => ({ ...prev, [account]: list }));
      return list;
    } catch (err) {
      toast.error(errorMessage(err));
      return [];
    }
  }, []);

  const loadAccounts = useCallback(async () => {
    try {
      const list = (await api.listAccounts()) ?? [];
      setAccounts(list);
      const all = await Promise.all(list.map((a) => loadMailboxes(a.id)));

      // 尚未选中文件夹时落到个人账号的收件箱; 有多个账号时打开的是统一视图的收件箱(侧栏的日常入口),
      // 个人收件箱仍记为当前文件夹, 写信账号等按它取。首次登录时缓存为空，
      // 这里第一次拿不到收件箱；引导同步完成后的变更事件会再调一次。
      setAccountId((curAcc) => {
        if (curAcc && list.some((a) => a.id === curAcc)) return curAcc;
        const idx = Math.max(
          0,
          list.findIndex((a) => a.isPersonal),
        );
        const inbox = all[idx]?.find((m) => m.kind === "inbox") ?? all[idx]?.[0];
        if (inbox) setMailboxId(inbox.id);
        if (inbox && list.length > 1 && all.some((boxes) => boxes.some((m) => m.kind === "inbox"))) {
          setUnified((cur) => cur || "inbox");
        }
        return inbox ? list[idx].id : "";
      });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, [loadMailboxes]);

  useEffect(() => {
    loadAccounts();
    api
      .getSessionStatus()
      .then((s) => setUsername(s.username))
      .catch(() => {});
  }, [loadAccounts]);

  // 标签按账号各自一套; 统一视图里每封邮件用它所属账号的标签。
  useEffect(() => {
    let cancelled = false;
    Promise.all(accounts.map(async (a) => [a.id, (await api.listLabels(a.id).catch(() => [])) ?? []] as const)).then(
      (entries) => {
        if (!cancelled) setLabelsByAccount(Object.fromEntries(entries));
      },
    );
    return () => {
      cancelled = true;
    };
  }, [accounts, revision]);

  /* ---------- 邮件列表 ---------- */

  // 乐观更新的覆盖层。删除、移动、标记等操作发出后界面立刻显示预期结果, 请求在后台完成;
  // 完成前任何一次重新加载(同步推送、翻页、搜索)的结果都要套上这层, 否则刚删掉的邮件会闪回来。
  // 条目按「账号/id」键记下操作序号, 同一封邮件连续操作时只由最后一次操作撤掉。
  const hiddenIds = useRef(new Map<string, number>());
  const patchedIds = useRef(new Map<string, { patch: Partial<EmailSummary>; seq: number }>());
  const mutationSeq = useRef(0);
  // 正在清空的文件夹(账号/文件夹): 后台逐批删除期间列表保持为空。
  const emptying = useRef(new Set<string>());

  const overlay = useCallback((list: EmailSummary[]) => {
    const hidden = hiddenIds.current;
    const patched = patchedIds.current;
    if (hidden.size === 0 && patched.size === 0) return list;
    return list
      .filter((e) => !hidden.has(emailKey(e)))
      .map((e) => {
        const p = patched.get(emailKey(e));
        return p ? ({ ...e, ...p.patch } as EmailSummary) : e;
      });
  }, []);

  // 当前视图的一页: 某个账号的文件夹, 或统一视图。
  const fetchPage = useCallback(
    (limit: number, offset: number) =>
      unified ? api.listUnified(unified, limit, offset) : api.listEmails(accountId, mailboxId, limit, offset),
    [unified, accountId, mailboxId],
  );

  const loadPage = useCallback(
    async (offset: number, token: number) => {
      if (!unified && (!accountId || !mailboxId)) return;
      if (!unified && emptying.current.has(`${accountId}/${mailboxId}`)) {
        setEmails([]);
        setHasMore(false);
        return;
      }
      setLoading(true);
      try {
        const page = await fetchPage(PAGE_SIZE, offset);
        if (token !== loadToken.current) return;
        const list = page ?? [];
        const shown = overlay(list);
        setEmails((prev) => {
          if (offset === 0) return shown;
          // 乐观移除的邮件让 offset 比服务端少算几封, 下一页开头可能与已有的重叠。
          const have = new Set(prev.map(emailKey));
          return [...prev, ...shown.filter((e) => !have.has(emailKey(e)))];
        });
        setHasMore(list.length === PAGE_SIZE);
      } catch (err) {
        if (token === loadToken.current) toast.error(errorMessage(err));
      } finally {
        if (token === loadToken.current) setLoading(false);
      }
    },
    [unified, accountId, mailboxId, fetchPage, overlay],
  );

  // 列表的最新值, 供乐观更新与静默刷新在回调里读取, 不必把 emails 列进依赖。
  const emailsRef = useRef(emails);
  useEffect(() => {
    emailsRef.current = emails;
  }, [emails]);

  // 一次加载列表开头的 want 封并整段替换。
  // 静默刷新(同步推送、操作完成后)与定位到某封邮件都走这里: 前者不能只重载第一页,
  // 翻过几页后列表会缩回 50 封, 滚动位置跳动、触底又加载回来, 看起来连闪几下;
  // 也不显示加载骨架, 数据到了一次性替换。
  const loadPrefix = useCallback(
    async (token: number, want: number) => {
      if (!unified && (!accountId || !mailboxId)) return;
      if (!unified && emptying.current.has(`${accountId}/${mailboxId}`)) {
        setEmails([]);
        setHasMore(false);
        return;
      }
      const out: EmailSummary[] = [];
      try {
        // 后端单次最多 200 封。
        for (let off = 0; off < want; off += 200) {
          const n = Math.min(200, want - off);
          const page = (await fetchPage(n, off)) ?? [];
          if (token !== loadToken.current) return;
          out.push(...page);
          if (page.length < n) break;
        }
        setEmails(overlay(out));
        setHasMore(out.length >= want);
      } catch (err) {
        if (token === loadToken.current) toast.error(errorMessage(err));
      } finally {
        // 首屏加载途中被刷新顶替时, 由这里收起加载态。
        if (token === loadToken.current) setLoading(false);
      }
    },
    [unified, accountId, mailboxId, fetchPage, overlay],
  );

  // 定位到某封邮件后列表至少要加载这么深(切换文件夹时归零)。
  // 定位与"退出搜索/切文件夹后的重新加载"可能在同一轮里都发生, 没有这个下限,
  // 后者会把刚加载到第 N 页的列表缩回第一页, 要打开的邮件又不见了。
  const minDepth = useRef(0);

  const refreshLoaded = useCallback(
    (token: number) => loadPrefix(token, Math.max(PAGE_SIZE, emailsRef.current.length, minDepth.current)),
    [loadPrefix],
  );

  // 从通知、链接或其它模块打开某封邮件时, 切换账号/文件夹会触发下面的列表重置;
  // 先记下要打开的邮件与它在列表里的位置, 重置时选中它并加载到那一页。
  const pendingEmail = useRef<{ key: string; index: number } | null>(null);

  /**
   * 打开某封邮件, 并让列表停在能看见它的位置。
   *
   * 只设置选中 id 不够: 邮件可能在别的账号/文件夹, 或在当前文件夹里靠后、还没翻到那一页,
   * 列表就会停在原处, 看起来"打开了但找不到"。先问后端它在哪、排第几, 再切过去并把
   * 前面的页一并加载出来, 行渲染出来后 EmailList 自己滚过去。
   */
  const openEmailIn = useCallback(
    async (acc: string, id: string) => {
      const key = emailKey({ accountId: acc, id });
      // 统一视图里已经列出这封邮件时不切走。
      if (unified && !searching && emailsRef.current.some((e) => emailKey(e) === key)) {
        setSelectedKey(key);
        return;
      }
      const inFolder = !unified && acc === accountId;
      const loc = await api
        .locateEmail(acc, id, inFolder ? mailboxId : "")
        .catch(() => null);
      const index = loc?.index ?? 0;

      if (inFolder && (!loc || loc.mailboxId === mailboxId)) {
        // 搜索结果不是文件夹列表, 要先退出搜索; 由下面的搜索 effect 接着加载并选中。
        if (searching) {
          pendingEmail.current = { key, index };
          setQuery("");
          return;
        }
        if (!emailsRef.current.some((e) => emailKey(e) === key) && index >= emailsRef.current.length) {
          loadToken.current += 1;
          minDepth.current = locateDepth(index);
          loadPrefix(loadToken.current, minDepth.current);
        }
        setSelectedKey(key);
        return;
      }

      pendingEmail.current = { key, index };
      const boxes = mailboxesByAccount[acc] ?? [];
      const target =
        (loc && boxes.find((m) => m.id === loc.mailboxId)) ?? boxes.find((m) => m.kind === "inbox");
      setUnified("");
      setAccountId(acc);
      if (target) setMailboxId(target.id);
    },
    [unified, accountId, mailboxId, mailboxesByAccount, searching, loadPrefix],
  );

  useEffect(() => {
    if (!unified && !mailboxId) return;
    loadToken.current += 1;
    const pending = pendingEmail.current;
    pendingEmail.current = null;
    setEmails([]);
    setSelectedKey(pending?.key ?? "");
    setHasMore(true);
    setQuery("");
    setChecked(new Set());
    minDepth.current = pending ? locateDepth(pending.index) : 0;
    // 要打开的邮件不在第一页时一次加载到它所在的那一页。
    if (pending && pending.index >= PAGE_SIZE) {
      setLoading(true);
      loadPrefix(loadToken.current, minDepth.current);
    } else {
      loadPage(0, loadToken.current);
    }
  }, [unified, mailboxId, accountId, loadPage, loadPrefix]);

  // 操作(标记已读、移动、立即同步、导入)完成后才调用 reload, 那时用户可能已切到别的文件夹。
  // 经 ref 取最新的 refreshLoaded, 重新加载的总是正在看的列表; 用发起时的闭包会把旧文件夹
  // 加载回来, 而且令牌是新的, 覆盖掉刚切过去的列表。
  const refreshLoadedRef = useRef(refreshLoaded);
  useEffect(() => {
    refreshLoadedRef.current = refreshLoaded;
  }, [refreshLoaded]);

  const reload = useCallback(() => {
    loadToken.current += 1;
    refreshLoadedRef.current(loadToken.current);
  }, []);

  // 预取当前邮件后面几封的正文, 按 j/↓ 翻下一封时不用等网络。
  // 统一视图里只预取与当前邮件同一账号的。
  useEffect(() => {
    if (!selectedKey) return;
    const idx = emails.findIndex((e) => emailKey(e) === selectedKey);
    if (idx < 0) return;
    const acc = emails[idx].accountId;
    const near = emails.slice(idx + 1, idx + 4);
    if (idx > 0) near.push(emails[idx - 1]);
    const ids = near.filter((e) => e.accountId === acc).map((e) => e.id);
    if (ids.length === 0) return;
    const t = setTimeout(() => api.prefetchBodies(acc, ids).catch(() => {}), 400);
    return () => clearTimeout(t);
  }, [selectedKey, emails]);

  /* ---------- 后台同步事件 ---------- */

  useEffect(() => {
    const offConnected = onEvent(Events.connected, (name: string) => {
      setUsername(name);
      loadAccounts();
    });

    const offChanged = onEvent(Events.mailChanged, (changed: string) => {
      // 登录后第一轮同步到达时可能还没有选中任何文件夹。
      if (!unified && !mailboxId) {
        loadAccounts();
        return;
      }
      // 其它账号只刷新文件夹（未读数），不打断正在看的列表; 统一视图包含所有账号。
      loadMailboxes(changed);
      if (unified || changed === accountId) reload();
    });

    // 其它模块(通讯录、系统通知、链接)发来的「打开这封邮件」「给这个人写信」。
    // 订阅前就发出的请求留在 pending 里, 这里先取走。
    const pending = takePendingOpenEmail();
    if (pending) queueMicrotask(() => openEmailIn(pending.accountId, pending.emailId));
    const offBusOpen = listen("openEmail", ({ accountId: acc, emailId: id }) => {
      takePendingOpenEmail();
      openEmailIn(acc, id);
    });
    const offCompose = listen("compose", ({ to, cc, bcc, subject, body }) =>
      setDraft({ to, cc: cc ?? "", bcc: bcc ?? "", subject: subject ?? "", html: body ? textToHtml(body) : "" }),
    );

    return () => {
      offConnected();
      offChanged();
      offBusOpen();
      offCompose();
    };
  }, [unified, accountId, mailboxId, reload, loadAccounts, loadMailboxes, openEmailIn, setDraft]);

  /* ---------- 搜索 ---------- */

  useEffect(() => {
    const q = query.trim();
    if (!q) {
      if (searching) {
        setSearching(false);
        // 退出搜索是为了打开某封邮件时, 直接加载到它所在的那一页。
        const pending = pendingEmail.current;
        pendingEmail.current = null;
        if (pending) {
          loadToken.current += 1;
          setSelectedKey(pending.key);
          setLoading(true);
          minDepth.current = locateDepth(pending.index);
          loadPrefix(loadToken.current, minDepth.current);
        } else {
          reload();
        }
      }
      return;
    }

    // 防抖：本地搜索很快，但每次按键重查仍会让长列表反复重排；服务端检索更不能每键一发。
    // 先出本地结果，再用服务端全文检索补上缓存之外的邮件（归档里的旧邮件多半不在本地）。
    const timer = setTimeout(async () => {
      loadToken.current += 1;
      const token = loadToken.current;
      setSearching(true);
      setLoading(true);
      try {
        // 统一视图里搜全部账号。
        const found = unified ? await api.searchAllEmails(q, 100) : await api.searchEmails(accountId, q, 100);
        if (token !== loadToken.current) return;
        setEmails(overlay(found ?? []));
        setHasMore(false);
      } catch (err) {
        toast.error(errorMessage(err));
      } finally {
        if (token === loadToken.current) setLoading(false);
      }
      if (token !== loadToken.current) return;
      setSearchingServer(true);
      try {
        const merged = unified
          ? await api.searchAllEmailsOnServer(q, 100)
          : await api.searchEmailsOnServer(accountId, q, 100);
        if (token === loadToken.current) setEmails(overlay(merged ?? []));
      } catch {
        // 服务端检索只是补充，失败时保留本地结果。
      } finally {
        if (token === loadToken.current) setSearchingServer(false);
      }
    }, 300);

    return () => clearTimeout(timer);
    // searching 由本 effect 自己设置，列进依赖会造成循环。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, unified, accountId, reload, loadPrefix, overlay]);

  /* ---------- 操作 ---------- */

  const refreshCurrent = useCallback(() => {
    setRevision((r) => r + 1);
    reload();
  }, [reload]);

  /** 调整侧栏某个文件夹的未读数, 同步回来前先显示预期值。 */
  const adjustUnread = useCallback((acc: string, box: string, delta: number | "zero") => {
    if (delta === 0) return;
    setMailboxesByAccount((prev) => {
      const list = prev[acc];
      if (!list) return prev;
      return {
        ...prev,
        [acc]: list.map((m) =>
          m.id === box
            ? ({ ...m, unreadEmails: delta === "zero" ? 0 : Math.max(0, m.unreadEmails + delta) } as Mailbox)
            : m,
        ),
      };
    });
  }, []);

  /**
   * 乐观执行一个邮件操作: 立刻按预期结果更新界面, 请求在后台提交。
   * keys 是「账号/id」键, 统一视图里可能跨账号, 按账号分组逐个提交 op。
   * remove 表示邮件会离开当前列表(删除、移动、归档、垃圾邮件), 选中的邮件随之跳到下一封;
   * patch 是对摘要字段的预期修改(已读、星标、固定)。
   * 失败时提示并重新加载, 界面回到服务器的真实状态。
   */
  const mutate = useCallback(
    (m: {
      keys: string[];
      op: (accountId: string, ids: string[]) => Promise<unknown>;
      remove?: boolean;
      patch?: Partial<EmailSummary>;
      done?: string;
    }) => {
      const keys = m.keys;
      if (keys.length === 0) return;
      const keySet = new Set(keys);
      const groups = groupByAccount(keys);
      const seq = ++mutationSeq.current;
      const list = emailsRef.current;
      const inSet = (e: EmailSummary) => keySet.has(emailKey(e));
      // 侧栏未读数按账号各调各的文件夹; 未读视图没有单一文件夹, 等操作完成后重读。
      const adjust = (count: (e: EmailSummary) => number) => {
        for (const acc of groups.keys()) {
          const box = listBoxOf(acc);
          if (!box) continue;
          adjustUnread(acc, box, list.filter((e) => e.accountId === acc && inSet(e)).reduce((n, e) => n + count(e), 0));
        }
      };

      if (m.remove) {
        for (const key of keys) hiddenIds.current.set(key, seq);
        adjust((e) => (e.isUnread ? -1 : 0));
        // 阅读栏不突然变空: 选中的邮件被移走时, 跳到它后面(没有则前面)第一封没被移走的。
        setSelectedKey((cur) => {
          if (!keySet.has(cur)) return cur;
          const idx = list.findIndex((e) => emailKey(e) === cur);
          const after = list.slice(idx + 1).find((e) => !inSet(e));
          const before = list
            .slice(0, Math.max(0, idx))
            .reverse()
            .find((e) => !inSet(e));
          const next = after ?? before;
          return next ? emailKey(next) : "";
        });
        setEmails((prev) => prev.filter((e) => !inSet(e)));
        setChecked((prev) => {
          if (![...prev].some((key) => keySet.has(key))) return prev;
          return new Set([...prev].filter((key) => !keySet.has(key)));
        });
      }
      if (m.patch) {
        const patch = m.patch;
        if (patch.isUnread !== undefined) {
          adjust((e) => (e.isUnread === patch.isUnread ? 0 : patch.isUnread ? 1 : -1));
        }
        for (const key of keys) {
          patchedIds.current.set(key, { patch: { ...patchedIds.current.get(key)?.patch, ...patch }, seq });
        }
        setEmails((prev) => prev.map((e) => (inSet(e) ? ({ ...e, ...patch } as EmailSummary) : e)));
      }
      if (m.done) toast.success(m.done);

      Promise.all([...groups].map(([acc, ids]) => m.op(acc, ids)))
        .catch((err) => toast.error(errorMessage(err)))
        .finally(() => {
          for (const key of keys) {
            if (hiddenIds.current.get(key) === seq) hiddenIds.current.delete(key);
            if (patchedIds.current.get(key)?.seq === seq) patchedIds.current.delete(key);
          }
          // 成功时后端已同步完, 重新加载读到的就是结果; 失败时重新加载把界面恢复原样。
          for (const acc of groups.keys()) loadMailboxes(acc);
          // 阅读栏只显示星标; 已读、固定变化不重载正文, 否则打开未读邮件时正文会闪一下。
          if (m.patch?.isFlagged !== undefined) refreshCurrent();
          else reload();
        });
    },
    [listBoxOf, adjustUnread, loadMailboxes, reload, refreshCurrent],
  );

  const trashIds = useCallback(
    (keys: string[]) => {
      if (currentKind === "trash") {
        const what = keys.length > 1 ? t("选中的 {0} 封邮件", keys.length) : t("这封邮件");
        if (!window.confirm(t("彻底删除{0}？此操作无法撤销。", what))) return;
        mutate({ keys, remove: true, op: (acc, ids) => api.deleteEmails(acc, ids), done: t("已彻底删除") });
        return;
      }
      mutate({ keys, remove: true, op: (acc, ids) => api.trashEmails(acc, ids), done: t("已删除") });
    },
    [currentKind, mutate],
  );

  const archiveIds = useCallback(
    (keys: string[]) =>
      mutate({ keys, remove: true, op: (acc, ids) => api.archiveEmails(acc, ids), done: t("已归档") }),
    [mutate],
  );

  /** 移动到某账号的文件夹。文件夹属于各自的账号, 只移动 keys 里属于该账号的邮件。 */
  const moveIds = useCallback(
    (keys: string[], acc: string, target: string) => {
      const box = mailboxesByAccount[acc]?.find((m) => m.id === target);
      mutate({
        keys: keys.filter((key) => parseEmailKey(key).accountId === acc),
        remove: true,
        op: (a, ids) => api.moveEmails(a, ids, target),
        done: box ? t("已移动到「{0}」", mailboxLabel(box)) : t("已移动"),
      });
    },
    [mailboxesByAccount, mutate],
  );

  const junkIds = useCallback(
    (keys: string[], junk: boolean) =>
      mutate({
        keys,
        remove: true,
        op: (acc, ids) => api.markJunk(acc, ids, junk),
        done: junk ? t("已标记为垃圾邮件") : t("已移回收件箱"),
      }),
    [mutate],
  );

  const markReadIds = useCallback(
    (keys: string[], read: boolean) =>
      mutate({ keys, patch: { isUnread: !read }, op: (acc, ids) => api.markRead(acc, ids, read) }),
    [mutate],
  );

  const flagIds = useCallback(
    (keys: string[], flagged: boolean) =>
      mutate({ keys, patch: { isFlagged: flagged }, op: (acc, ids) => api.markFlagged(acc, ids, flagged) }),
    [mutate],
  );

  const toggleFlag = useCallback(
    (email: EmailSummary | EmailDetail) => flagIds([emailKey(email)], !email.isFlagged),
    [flagIds],
  );

  const togglePin = useCallback(
    (email: EmailSummary) =>
      mutate({
        keys: [emailKey(email)],
        patch: { isPinned: !email.isPinned },
        op: (acc, ids) => api.setPinned(acc, ids, !email.isPinned),
      }),
    [mutate],
  );

  const setLabelIds = useCallback(
    (keys: string[], label: string, set: boolean) => {
      Promise.all([...groupByAccount(keys)].map(([acc, ids]) => api.setLabel(acc, ids, label, set)))
        .then(refreshCurrent)
        .catch((err) => toast.error(errorMessage(err)));
    },
    [refreshCurrent],
  );

  /** 列表里点选邮件。草稿直接进入编辑。 */
  const selectEmail = useCallback(
    async (key: string) => {
      const summary = emails.find((e) => emailKey(e) === key);
      if (currentKind !== "drafts" && !summary?.isDraft) {
        setDraft(null);
        setSelectedKey(key);
        setMobileView("detail");
        return;
      }
      const { accountId: acc, id } = parseEmailKey(key);
      try {
        const email = await api.getEmail(acc, id);
        setSelectedKey(key);
        setDraft(draftFromEmail(email, acc));
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [emails, currentKind, setDraft],
  );

  const checkEmail = useCallback(
    (key: string, range: boolean) => {
      setChecked((prev) => {
        const next = new Set(prev);
        if (range && lastChecked.current) {
          const a = emails.findIndex((e) => emailKey(e) === lastChecked.current);
          const b = emails.findIndex((e) => emailKey(e) === key);
          if (a >= 0 && b >= 0) {
            for (let i = Math.min(a, b); i <= Math.max(a, b); i++) next.add(emailKey(emails[i]));
            return next;
          }
        }
        if (next.has(key)) next.delete(key);
        else next.add(key);
        return next;
      });
      lastChecked.current = key;
    },
    [emails],
  );

  async function sync() {
    if (syncing || (!unified && !accountId)) return;
    setSyncing(true);
    try {
      // 统一视图里同步全部账号。
      await Promise.all((unified ? accounts.map((a) => a.id) : [accountId]).map((acc) => api.syncNow(acc)));
      reload();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSyncing(false);
    }
  }

  /** 文件夹 acc/box 的变化是否会出现在当前列表里。 */
  const showsMailbox = useCallback(
    (acc: string, box: string) => (unified ? unified === UNIFIED_UNREAD || listBoxOf(acc) === box : acc === accountId && box === mailboxId),
    [unified, listBoxOf, accountId, mailboxId],
  );

  const importInto = useCallback(
    async (acc: string, box: string) => {
      try {
        const res = await api.importEmails(acc, box);
        if (!res || (res.imported === 0 && res.failed === 0)) return;
        if (res.failed > 0) {
          toast.warning(t("导入 {0} 封，失败 {1} 封：{2}", res.imported, res.failed, res.firstError));
        } else {
          toast.success(t("已导入 {0} 封", res.imported));
        }
        loadMailboxes(acc);
        if (showsMailbox(acc, box)) reload();
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [showsMailbox, loadMailboxes, reload],
  );

  const importEmails = useCallback(() => {
    // 统一视图不是一个文件夹, 不知道导入到哪个账号。
    if (unified || !accountId || !mailboxId) {
      toast.error(t("请先选择一个文件夹"));
      return;
    }
    importInto(accountId, mailboxId);
  }, [unified, accountId, mailboxId, importInto]);

  /** 侧栏文件夹右键菜单里涉及邮件的操作。标记已读与清空同样先显示结果, 后台逐批处理。 */
  const folderAction = useCallback(
    async (acc: string, action: "markRead" | "import" | "empty" | "refresh", box: Mailbox) => {
      const name = mailboxLabel(box);
      const isCurrent = showsMailbox(acc, box.id);
      // 列表里属于这个文件夹的邮件; 未读视图分不出, 等重新加载。
      const inBox = (e: EmailSummary) => e.accountId === acc && unified !== UNIFIED_UNREAD;
      const toastId = `folder:${action}:${acc}/${box.id}`;
      switch (action) {
        case "import":
          return importInto(acc, box.id);
        case "refresh":
          try {
            await api.syncNow(acc);
            loadMailboxes(acc);
            if (isCurrent) reload();
          } catch (err) {
            toast.error(errorMessage(err));
          }
          return;
        case "markRead":
          adjustUnread(acc, box.id, "zero");
          if (isCurrent) {
            setEmails((prev) =>
              prev.map((e) => (e.isUnread && inBox(e) ? ({ ...e, isUnread: false } as EmailSummary) : e)),
            );
          }
          toast.loading(t("正在将「{0}」标记为已读…", name), { id: toastId });
          try {
            const n = await api.markMailboxRead(acc, box.id);
            toast.success(n > 0 ? t("已将 {0} 封标记为已读", n) : t("没有未读邮件"), { id: toastId });
          } catch (err) {
            toast.error(errorMessage(err), { id: toastId });
          }
          loadMailboxes(acc);
          if (isCurrent) reload();
          return;
        case "empty": {
          if (!window.confirm(t("彻底删除「{0}」里的全部 {1} 封邮件？此操作无法撤销。", name, box.totalEmails))) return;
          const key = `${acc}/${box.id}`;
          emptying.current.add(key);
          adjustUnread(acc, box.id, "zero");
          if (isCurrent) {
            if (unified) {
              setEmails((prev) => prev.filter((e) => !inBox(e)));
            } else {
              setEmails([]);
              setSelectedKey("");
              setChecked(new Set());
              setHasMore(false);
            }
          }
          toast.loading(t("正在清空「{0}」…", name), { id: toastId });
          try {
            const n = await api.emptyMailbox(acc, box.id);
            toast.success(t("已清空「{0}」，删除 {1} 封", name, n), { id: toastId });
          } catch (err) {
            toast.error(errorMessage(err), { id: toastId });
          } finally {
            emptying.current.delete(key);
          }
          loadMailboxes(acc);
          if (isCurrent) reload();
          return;
        }
      }
    },
    [unified, showsMailbox, importInto, loadMailboxes, reload, adjustUnread],
  );

  /** 列表右键的回复、转发要用完整邮件(正文、附件): 先读缓存, 没有正文时再拉。 */
  const withDetail = useCallback(async (e: EmailSummary, fn: (email: EmailDetail) => void) => {
    try {
      let email = await api.getEmail(e.accountId, e.id);
      if (!email.bodyFetched) email = await api.fetchBody(e.accountId, e.id);
      fn(email);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  const menuActions = useMemo<EmailMenuActions>(
    () => ({
      reply: (email, all) => withDetail(email, (d) => setDraft(replyDraft(email.accountId, d, all))),
      forward: (email, asAttachment) =>
        withDetail(email, (d) =>
          setDraft(asAttachment ? forwardAsAttachmentDraft(d, email.accountId) : forwardDraft(d, email.accountId)),
        ),
      archive: archiveIds,
      trash: trashIds,
      move: moveIds,
      junk: junkIds,
      markRead: markReadIds,
      toggleFlag,
      togglePin,
      setLabel: setLabelIds,
    }),
    [withDetail, setDraft, archiveIds, trashIds, moveIds, junkIds, markReadIds, toggleFlag, togglePin, setLabelIds],
  );

  // 阅读栏打开的邮件; 统一视图里它的账号不一定是 accountId。
  const actionsFor = useCallback(
    (email: EmailDetail): ToolbarActions => {
      const acc = email.accountId;
      const key = emailKey(email);
      return {
        reply: () => setDraft(replyDraft(acc, email, false)),
        replyAll: () => setDraft(replyDraft(acc, email, true)),
        forward: () => setDraft(forwardDraft(email, acc)),
        forwardAsAttachment: () => setDraft(forwardAsAttachmentDraft(email, acc)),
        archive: () => archiveIds([key]),
        trash: () => trashIds([key]),
        move: (target) => moveIds([key], acc, target),
        setLabel: (label, set) => setLabelIds([key], label, set),
        junk: (junk) => junkIds([key], junk),
        markUnread: () => {
          markReadIds([key], false);
          // 标为未读后关闭阅读栏，否则停留在这封上会被再次自动标记已读。
          setSelectedKey("");
        },
        toggleFlag: () => toggleFlag(email),
        print: () => printEmail(email, false),
        viewSource: () => setSourceTarget({ accountId: acc, emailId: email.id }),
        exportEml: async () => {
          try {
            const path = await api.exportEmail(acc, email.id);
            if (path) toast.success(t("已导出到 {0}", path));
          } catch (err) {
            toast.error(errorMessage(err));
          }
        },
        importEml: importEmails,
        showShortcuts: () => setShowShortcuts(true),
        toggleFocus: () => setFocusMode((f) => !f),
        toggleBodyLight: () => setBodyAppearance(bodyAppearance === "light" ? "follow" : "light"),
      };
    },
    [archiveIds, trashIds, moveIds, setLabelIds, junkIds, markReadIds, toggleFlag, importEmails, bodyAppearance, setBodyAppearance, setDraft],
  );

  /* ---------- 键盘快捷键 ---------- */

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null;
      // 在输入框、对话框、菜单里操作时不触发。
      if (
        target?.closest("input, textarea, select, [contenteditable=true], [role=dialog], [role=menu]") ||
        e.ctrlKey ||
        e.metaKey ||
        e.altKey
      ) {
        return;
      }

      const step = (delta: number) => {
        const idx = emails.findIndex((m) => emailKey(m) === selectedKey);
        const next = emails[idx < 0 ? 0 : idx + delta];
        if (next) setSelectedKey(emailKey(next));
      };

      switch (e.key) {
        case "j":
        case "ArrowDown":
          e.preventDefault();
          return step(1);
        case "k":
        case "ArrowUp":
          e.preventDefault();
          return step(-1);
        case "/":
          e.preventDefault();
          return searchRef.current?.focus();
        case "?":
          return setShowShortcuts(true);
        case "c":
          return setDraft({ to: "", cc: "", subject: "", html: "" });
        case "Escape":
          if (focusMode) setFocusMode(false);
          return;
      }

      const email = currentEmail.current;
      if (!email || emailKey(email) !== selectedKey) return;
      const a = actionsFor(email);
      const map: Record<string, () => void> = {
        r: a.reply,
        a: a.replyAll,
        f: a.forward,
        e: a.archive,
        "#": a.trash,
        Delete: a.trash,
        s: a.toggleFlag,
        u: a.markUnread,
        "!": () => a.junk(currentKind !== "junk"),
      };
      const fn = map[e.key];
      if (fn) {
        e.preventDefault();
        fn();
      }
    }

    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [emails, selectedKey, focusMode, currentKind, actionsFor, setDraft]);


  // 多选都属于同一个账号时的账号 id, 否则为空(跨账号没有共同的目标文件夹)。
  const checkedAccounts = new Set([...checked].map((key) => parseEmailKey(key).accountId));
  const checkedAccount = checkedAccounts.size === 1 ? [...checkedAccounts][0] : "";

  /** 列表行的来源: 统一视图标出账号, 搜索结果标出所在文件夹。 */
  function whereOf(email: EmailSummary): string {
    const parts: string[] = [];
    if (unified) {
      const name = accounts.find((a) => a.id === email.accountId)?.name ?? "";
      // 账号名是邮箱地址, 列表里只放 @ 前面一段, 完整地址在悬停提示里。
      if (name) parts.push(name.split("@")[0]);
    }
    if (searching) {
      const boxes = mailboxesByAccount[email.accountId] ?? [];
      const names = (email.mailboxIds ?? [])
        .map((id) => boxes.find((m) => m.id === id))
        .filter((m): m is Mailbox => !!m)
        .map(mailboxLabel);
      if (names.length > 0) parts.push(names.join("、"));
    }
    return parts.join(" · ");
  }

  return (
    <div className="h-full">
      <PaneLayout
        id="mail"
        collapsed={focusMode}
        mobilePane={mobilePane}
        ownMobileNav
        onMobilePaneChange={(p) => {
          // 写信面板占着正文栏: 返回即关闭 (草稿已自动保存)。
          if (draft && p !== "detail") setDraft(null);
          setMobileView(p);
        }}
        sidebar={
          <>
            <UserMenu
              username={username}
              onImport={importEmails}
              onShortcuts={() => setShowShortcuts(true)}
              onSignOut={async () => {
                await api.signOut();
                onSignedOut();
              }}
            />

            <div className="px-2 pb-2">
              <Button className="w-full" size="sm" onClick={() => setDraft({ to: "", cc: "", subject: "", html: "" })}>
                <PenSquare className="size-4" />
                {t("写邮件")}</Button>
            </div>

            <ScrollArea className="min-h-0 flex-1 px-2 pb-2">
              <MailSidebar
                accounts={accounts}
                mailboxesByAccount={mailboxesByAccount}
                accountId={accountId}
                mailboxId={mailboxId}
                onSelect={(acc, box) => {
                  setUnified("");
                  setAccountId(acc);
                  setMailboxId(box);
                  setMobileView("list");
                }}
                unified={unified}
                onSelectUnified={(v) => {
                  setUnified(v);
                  setMobileView("list");
                }}
                onMailboxesChanged={loadMailboxes}
                onFolderAction={folderAction}
              />
            </ScrollArea>
          </>
        }
        list={
          <>
            <div className="border-border flex shrink-0 items-center gap-2 border-b px-3 py-2">
              <MobileNavButton kind="menu" className="-ml-1.5" />
              <Checkbox
                aria-label={t("全选")}
                title={t("全选")}
                disabled={emails.length === 0}
                checked={checked.size === 0 ? false : checked.size === emails.length ? true : "indeterminate"}
                onCheckedChange={(v) => setChecked(v === true ? new Set(emails.map(emailKey)) : new Set())}
              />
              <div className="relative min-w-0 flex-1">
                <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
                <Input
                  ref={searchRef}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Escape") {
                      setQuery("");
                      e.currentTarget.blur();
                    }
                  }}
                  placeholder={unified ? t("搜索全部账号（/）") : t("搜索全部文件夹（/）")}
                  className="h-8 pr-8 pl-8"
                  aria-label={t("搜索邮件")}
                />
                {searching && searchingServer && (
                  <Loader2
                    className="text-muted-foreground absolute top-1/2 right-7 size-3.5 -translate-y-1/2 animate-spin"
                    aria-label={t("正在搜索服务器")}
                  />
                )}
                {query && (
                  <button
                    type="button"
                    onClick={() => setQuery("")}
                    aria-label={t("清除搜索")}
                    className="text-muted-foreground hover:text-foreground absolute top-1/2 right-2 -translate-y-1/2"
                  >
                    <X className="size-3.5" />
                  </button>
                )}
              </div>

              {scrolledAway ? (
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8 shrink-0"
                  onClick={() => listScrollRef.current?.scrollTo({ top: 0, behavior: "smooth" })}
                  aria-label={t("回到顶部")}
                  title={t("回到顶部")}
                >
                  <ArrowUpToLine className="size-4" />
                </Button>
              ) : (
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8 shrink-0"
                  onClick={sync}
                  disabled={syncing}
                  aria-label={t("立即同步")}
                  title={t("立即同步")}
                >
                  {syncing ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                </Button>
              )}
            </div>

            {checked.size > 0 && (
              <div className="border-border bg-secondary flex shrink-0 items-center gap-0.5 overflow-hidden border-b px-2 py-1 text-sm">
                <span className="mr-auto pl-1 text-xs whitespace-nowrap tabular-nums">{t("已选{0}封", checked.size)}</span>
                <BulkBtn label={t("标为已读")} onClick={() => markReadIds([...checked], true)}>
                  <MailOpen />
                </BulkBtn>
                <BulkBtn label={t("归档")} onClick={() => archiveIds([...checked])}>
                  <Archive />
                </BulkBtn>
                <BulkBtn label={currentKind === "trash" ? t("彻底删除") : t("删除")} onClick={() => trashIds([...checked])}>
                  <Trash2 />
                </BulkBtn>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon" className="size-7 [&_svg]:size-4" title={t("更多")} aria-label={t("更多批量操作")}>
                      <MoreHorizontal />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="w-52">
                    <DropdownMenuItem onSelect={() => markReadIds([...checked], false)}>
                      <MailIcon className="size-4" />
                      {t("标为未读")}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => flagIds([...checked], true)}>
                      <Star className="size-4" />
                      {t("加星标")}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => flagIds([...checked], false)}>
                      <Star className="size-4" />
                      {t("取消星标")}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => junkIds([...checked], currentKind !== "junk")}>
                      <ShieldAlert className="size-4" />
                      {currentKind === "junk" ? t("不是垃圾邮件") : t("标记为垃圾邮件")}
                    </DropdownMenuItem>
                    {/* 文件夹属于各自的账号, 多选跨账号时没有共同的目标文件夹。 */}
                    {checkedAccount && (
                      <DropdownMenuSub>
                        <DropdownMenuSubTrigger>
                          <FolderInput className="size-4" />
                          {t("移动到")}</DropdownMenuSubTrigger>
                        <DropdownMenuSubContent className="max-h-80 w-52 overflow-y-auto">
                          {(mailboxesByAccount[checkedAccount] ?? [])
                            .filter(
                              (m) => m.id !== listBoxOf(checkedAccount) && m.kind !== "drafts" && m.kind !== "scheduled",
                            )
                            .map((m) => (
                              <DropdownMenuItem key={m.id} onSelect={() => moveIds([...checked], checkedAccount, m.id)}>
                                <span className="truncate">{mailboxLabel(m)}</span>
                              </DropdownMenuItem>
                            ))}
                        </DropdownMenuSubContent>
                      </DropdownMenuSub>
                    )}
                  </DropdownMenuContent>
                </DropdownMenu>
                <BulkBtn label={t("取消选择")} onClick={() => setChecked(new Set())}>
                  <X />
                </BulkBtn>
              </div>
            )}

            <div className="relative min-h-0 flex-1">
              {/* 窄屏下侧栏收起, 「写邮件」浮在列表右下角。 */}
              {compact && (
                <Button
                  size="icon"
                  className="absolute right-4 bottom-4 z-10 size-12 rounded-full shadow-lg"
                  onClick={() => setDraft({ to: "", cc: "", subject: "", html: "" })}
                  aria-label={t("写邮件")}
                >
                  <PenSquare className="size-5" />
                </Button>
              )}
              <EmailList
                onScrollContainer={setListScrollEl}
                onScrolledAway={setScrolledAway}
                whereOf={searching || unified ? whereOf : undefined}
                showRecipients={!searching && ["sent", "drafts", "scheduled"].includes(currentKind)}
                checked={checked}
                onCheck={checkEmail}
                emails={emails}
                selectedKey={selectedKey}
                highlightSelected={!compact}
                loading={loading}
                hasMore={hasMore && !searching}
                onSelect={selectEmail}
                onLoadMore={() => loadPage(emails.length, loadToken.current)}
                onToggleFlag={toggleFlag}
                onDelete={(email) => trashIds([emailKey(email)])}
                rowMenu={(email, row) => (
                  <EmailContextMenu
                    email={email}
                    checked={checked}
                    mailboxes={mailboxesByAccount[email.accountId] ?? []}
                    mailboxId={listBoxOf(email.accountId)}
                    currentKind={currentKind}
                    knownLabels={labelsByAccount[email.accountId] ?? []}
                    actions={menuActions}
                  >
                    {row}
                  </EmailContextMenu>
                )}
              />
            </div>
          </>
        }
        detail={
          <div className="h-full">
            {draft ? (
              <ComposePane
                key={draftKey}
                accountId={draft.accountId ?? composeAccount}
                draft={draft}
                onClose={() => setDraft(null)}
                onSent={reload}
              />
            ) : selectedKey ? (
              <EmailView
                key={selectedKey}
                accountId={selected.accountId}
                emailId={selected.id}
                mailboxes={mailboxesByAccount[selected.accountId] ?? []}
                currentKind={currentKind}
                labels={labelsByAccount[selected.accountId] ?? []}
                focusMode={focusMode}
                bodyMode={bodyMode}
                bodyAlwaysLight={bodyAppearance === "light"}
                actions={actionsFor}
                onLoaded={(e) => {
                  currentEmail.current = e;
                }}
                revision={revision}
                onOpenEmail={(id) => setSelectedKey(emailKey({ accountId: selected.accountId, id }))}
                onMarkRead={(id) => markReadIds([emailKey({ accountId: selected.accountId, id })], true)}
              />
            ) : (
              <div className="text-muted-foreground flex h-full items-center justify-center text-sm">{t("选择一封邮件")}</div>
            )}
          </div>
        }
      />

      <SourceDialog target={sourceTarget} onClose={() => setSourceTarget(null)} />
      <ShortcutsDialog open={showShortcuts} onClose={() => setShowShortcuts(false)} />
    </div>
  );
}

function BulkBtn({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  useI18n();
  return (
    <Button variant="ghost" size="icon" className="size-7 [&_svg]:size-4" title={label} aria-label={label} onClick={onClick}>
      {children}
    </Button>
  );
}

function addressesOf(list: { name?: string; email?: string }[] | undefined): string {
  if (!list) return "";
  return list
    .map((a) => (a.name ? `${a.name} <${a.email}>` : (a.email ?? "")))
    .filter(Boolean)
    .join(", ");
}

/** 已经带前缀的主题不再叠加，否则长回复链会变成 Re: Re: Re:。 */
function withPrefix(prefix: "Re:" | "Fwd:", subject: string): string {
  const re = prefix === "Re:" ? /^re\s*:/i : /^(fwd?|转发)\s*[:：]/i;
  if (re.test(subject.trim())) return subject;
  return `${prefix} ${subject}`;
}

/** 原邮件里被正文 cid: 引用的内嵌图片, 回复、转发时随信带上。 */
function inlinePartsOf(email: EmailDetail) {
  return (email.attachments ?? [])
    .filter((a) => a.cid && a.inline)
    .map((a) => ({ blobId: a.blobId, type: a.type, name: a.name || a.cid, cid: a.cid }));
}

/** 原邮件正文转成可以引用的 HTML。 */
function originalHtml(email: EmailDetail): string {
  return email.bodyHtml || textToHtml(email.bodyText || "");
}

function replyDraft(accountId: string, email: EmailDetail, all: boolean): ComposeDraft {
  // References 应是原邮件的 References 加上它自己的 Message-ID。本地没存完整链，
  // 用 In-Reply-To + Message-ID 近似，足够对方客户端把回复挂进线程。
  const refs = [email.inReplyTo, email.messageId].filter(Boolean) as string[];
  const intro = t("在 {0}，{1} 写道：", fullDate(email.receivedAt), displayAddressList(email.from));
  return {
    to: addressesOf(email.replyTo?.length ? email.replyTo : email.from),
    cc: all ? addressesOf([...(email.to ?? []), ...(email.cc ?? [])]) : "",
    subject: withPrefix("Re:", email.subject),
    html: "",
    quoteHtml: `<div class="czl-quote"><p>${escapeHtml(intro)}</p><blockquote style="margin:0 0 0 .8ex;border-left:1px solid #ccc;padding-left:1ex">${originalHtml(email)}</blockquote></div>`,
    inReplyTo: email.messageId,
    references: refs,
    replyTo: { accountId, emailId: email.id },
    inlineParts: inlinePartsOf(email),
    accountId,
  };
}

function forwardDraft(email: EmailDetail, accountId: string): ComposeDraft {
  const header = [
    t("-------- 转发的邮件 --------"),
    t("发件人：{0}", displayAddressList(email.from)),
    t("时间：{0}", fullDate(email.receivedAt)),
    t("收件人：{0}", displayAddressList(email.to)),
    t("主题：{0}", email.subject),
  ]
    .map(escapeHtml)
    .join("<br>");
  return {
    to: "",
    cc: "",
    subject: withPrefix("Fwd:", email.subject),
    html: "",
    quoteHtml: `<div class="czl-quote"><p>${header}</p>${originalHtml(email)}</div>`,
    inlineParts: inlinePartsOf(email),
    // 普通转发带上原邮件的附件，引用服务器上已有的 blob，无需重新上传。
    attachments: (email.attachments ?? [])
      .filter((a) => !a.inline)
      .map((a) => ({ blobId: a.blobId, type: a.type, name: a.name, size: a.size })),
    accountId,
  };
}

/** 作为附件转发: 原邮件的 blob 就是完整的 RFC 5322 原文，直接引用，无需下载再上传。 */
function forwardAsAttachmentDraft(email: EmailDetail, accountId: string): ComposeDraft {
  return {
    accountId,
    to: "",
    cc: "",
    subject: withPrefix("Fwd:", email.subject),
    html: "",
    attachments: [{ blobId: email.blobId, type: "message/rfc822", name: `${email.subject || "email"}.eml`, size: email.size }],
  };
}

/** 打开已有草稿继续编辑。 */
function draftFromEmail(email: EmailDetail, accountId: string): ComposeDraft {
  const { body, quote } = email.bodyHtml ? splitDraftHtml(email.bodyHtml) : { body: textToHtml(email.bodyText || ""), quote: "" };
  return {
    to: addressesOf(email.to),
    cc: addressesOf(email.cc),
    bcc: addressesOf(email.bcc),
    subject: email.subject,
    html: body,
    quoteHtml: quote || undefined,
    inReplyTo: email.inReplyTo || undefined,
    attachments: (email.attachments ?? [])
      .filter((a) => !a.inline)
      .map((a) => ({ blobId: a.blobId, type: a.type, name: a.name, size: a.size })),
    draftId: email.id,
    inlineParts: inlinePartsOf(email),
    accountId,
  };
}
