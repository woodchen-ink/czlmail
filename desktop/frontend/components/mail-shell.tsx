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
  RotateCcw,
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
import { MailSyncState } from "@/components/mail-sync-state";
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
  type EmailPlacement,
  type EmailSummary,
  type MailSyncStatus,
  type Mailbox,
} from "@/lib/api";
import { displayAddressList, fullDate } from "@/lib/format";
import { printEmail } from "@/lib/print";
import { useAppearance } from "@/lib/theme";
import { listen, takePendingOpenEmail } from "@/lib/app-bus";
import { escapeHtml, splitDraftHtml, textToHtml } from "@/lib/html";
import { emailKey, groupByAccount, parseEmailKey } from "@/lib/email-key";
import { formatKey, formatKeys, shortcutActionOf, useShortcuts, type ShortcutAction } from "@/lib/shortcuts";

const PAGE_SIZE = 50;

// 定位一封邮件时最多加载多少封。再深就不值得了: 每 200 封一次查询,
// 列表也长到滚动条没有意义, 这时只打开邮件、不强求列表停在它上面。
const LOCATE_LIMIT = 2000;

/** 为了让第 index 封出现在列表里, 需要加载的封数(按整页取, 免得刚好停在页边)。 */
function locateDepth(index: number): number {
  return Math.min(LOCATE_LIMIT, (Math.floor(index / PAGE_SIZE) + 1) * PAGE_SIZE);
}

/** 一次邮件操作, 见 mutate。 */
interface Mutation {
  keys: string[];
  op: (accountId: string, ids: string[]) => Promise<unknown>;
  remove?: boolean;
  patch?: Partial<EmailSummary>;
  done?: string;
  /** 能否撤销, 默认能。彻底删除、打开邮件时自动标为已读传 false。 */
  undoable?: boolean;
}

/** 可撤销的一步。trigger 来自 Ctrl+Z 或提示里的「撤销」按钮, 只生效一次。 */
interface UndoEntry {
  trigger: () => void;
}

/** 带「撤销」按钮的提示多停留一会儿, 来得及点。 */
const UNDO_TOAST_MS = 8000;

const PATCH_FIELDS = ["isUnread", "isFlagged", "isPinned"] as const;

/**
 * patch 生效前各封的原值, 按原值分组: 键是原值的 JSON, 值是这一组的邮件键。
 * 列表里没有的(展开会话里别的文件夹的)取阅读栏的详情, 都没有就当是与 patch 相反的值。
 * 原值与 patch 相同的邮件不会变化, 不记。
 */
function previousValues(
  keys: string[],
  patch: Partial<EmailSummary>,
  list: EmailSummary[],
  shown: EmailSummary | null,
): Map<string, string[]> {
  const byKey = new Map(list.map((e) => [emailKey(e), e]));
  const out = new Map<string, string[]>();
  for (const key of keys) {
    const e = byKey.get(key) ?? (shown && emailKey(shown) === key ? shown : undefined);
    const old: Partial<Record<(typeof PATCH_FIELDS)[number], boolean>> = {};
    let changes = false;
    for (const f of PATCH_FIELDS) {
      const v = patch[f];
      if (v === undefined) continue;
      old[f] = e ? e[f] : !v;
      if (old[f] !== v) changes = true;
    }
    if (!changes) continue;
    const sig = JSON.stringify(old);
    out.set(sig, [...(out.get(sig) ?? []), key]);
  }
  return out;
}

/** 把 patch(已读、星标、固定之一)提交到服务器。 */
function patchOp(accountId: string, ids: string[], patch: Partial<EmailSummary>): Promise<unknown> {
  if (patch.isUnread !== undefined) return api.markRead(accountId, ids, !patch.isUnread);
  if (patch.isFlagged !== undefined) return api.markFlagged(accountId, ids, patch.isFlagged);
  if (patch.isPinned !== undefined) return api.setPinned(accountId, ids, patch.isPinned);
  return Promise.resolve();
}

export function MailShell({ active, onSignedOut }: { active: boolean; onSignedOut: () => void }) {
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
  // 连上后那一轮全账号同步的进度; 首次登录时据此把空列表显示成「正在同步」。
  const [syncStatus, setSyncStatus] = useState<MailSyncStatus | null>(null);
  const [retryingSync, setRetryingSync] = useState(false);
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
  // 搜索框提示里显示「搜索」动作的第一个键位; 用户把键位删光时不带括号。
  const shortcuts = useShortcuts();
  const searchKey = shortcuts.search[0];
  const searchHint = searchKey ? formatKey(searchKey) : "";

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
  /** 阅读栏里被操作的那封所在会话, 移走它后选中该会话在列表里的行。 */
  const openThread = useRef<{ key: string; accountId: string; threadId: string } | null>(null);
  // 正在清空的文件夹(账号/文件夹): 后台逐批删除期间列表保持为空。
  const emptying = useRef(new Set<string>());
  /** 最近一次可撤销的邮件操作, Ctrl+Z 撤销它。只记一步, 与 Outlook 相同。 */
  const lastUndo = useRef<UndoEntry | null>(null);
  // 列表上方的「撤销」按钮据此置灰。
  const [canUndo, setCanUndo] = useState(false);

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

  // 当前看的是哪个列表。撤销删除时只在同一个列表里把选中放回恢复的那封。
  const viewKey = `${unified}|${accountId}|${mailboxId}`;
  const viewRef = useRef(viewKey);
  useEffect(() => {
    viewRef.current = viewKey;
  }, [viewKey]);

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
    const loadSyncStatus = () =>
      api
        .getMailSyncStatus()
        .then(setSyncStatus)
        .catch(() => {});
    loadSyncStatus();
    const offConnected = onEvent(Events.connected, (name: string) => {
      setUsername(name);
      loadAccounts();
      loadSyncStatus();
    });
    const offSyncStatus = onEvent(Events.syncStatus, (s: MailSyncStatus) => setSyncStatus(s));
    // 运行中新共享过来或被撤销的邮箱: 重读账号与文件夹, 侧栏随之增减; 统一视图里可能有被撤销账号的邮件, 一并重载。
    const offAccounts = onEvent(Events.accountsChanged, () => {
      loadAccounts();
      if (unified) reload();
    });
    // 「并入全部账号」开关改了: 打开着的统一视图要换成新的账号范围。
    const offSettings = onEvent(Events.settingsChanged, () => {
      if (unified) reload();
    });

    const offChanged = onEvent(Events.mailChanged, (changed: string) => {
      // 手动同步、重试补上的账号不经过那一轮的进度事件, 这里顺带更新。
      loadSyncStatus();
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
      offSyncStatus();
      offAccounts();
      offSettings();
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

  // 撤销已读、星标时再走一次 mutate; 它在自己的回调里拿不到自身, 经 ref 取。
  const mutateRef = useRef<(m: Mutation) => void>(() => {});

  /**
   * 乐观执行一个邮件操作: 立刻按预期结果更新界面, 请求在后台提交。
   * keys 是「账号/id」键, 统一视图里可能跨账号, 按账号分组逐个提交 op。
   * remove 表示邮件会离开当前列表(删除、移动、归档、垃圾邮件), 选中的邮件随之跳到下一封;
   * patch 是对摘要字段的预期修改(已读、星标、固定)。
   * 失败时提示并重新加载, 界面回到服务器的真实状态。
   * 除非 undoable 为 false, 操作记作最近一步, Ctrl+Z 或提示里的「撤销」把它改回去。
   */
  const mutate = useCallback(
    (m: Mutation) => {
      const keys = m.keys;
      if (keys.length === 0) return;
      const keySet = new Set(keys);
      const groups = groupByAccount(keys);
      const seq = ++mutationSeq.current;
      const list = emailsRef.current;
      const inSet = (e: EmailSummary) => keySet.has(emailKey(e));
      const undoable = m.undoable !== false;
      const view = viewRef.current;
      let wasSelected = "";

      // 撤销已读、星标、固定要知道每封原来的值, 在乐观更新之前记下。
      const shown = currentEmail.current;
      const previous = undoable && m.patch ? previousValues(keys, m.patch, list, shown) : null;
      // 阅读栏里那封的详情不随已读变化重载; 快捷键「切换已读」要看它, 这里跟着改。
      if (m.patch?.isUnread !== undefined && shown && keySet.has(emailKey(shown))) {
        currentEmail.current = { ...shown, isUnread: m.patch.isUnread } as EmailDetail;
      }
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
          wasSelected = cur;
          const idx = list.findIndex((e) => emailKey(e) === cur);
          // 选中的是展开会话里不在本文件夹的一封(如已发送的回复): 列表里没有它, 回到代表该会话的行。
          if (idx < 0) {
            // 工具栏操作记在 openThread 里; 展开区里直接点删除或右键时没有记, 用阅读栏正开着的那封。
            const shown = currentEmail.current;
            const open =
              openThread.current?.key === cur
                ? openThread.current
                : shown && emailKey(shown) === cur
                  ? { key: cur, accountId: shown.accountId, threadId: shown.threadId }
                  : null;
            const head = open?.key === cur && open.threadId && list.find((e) => e.accountId === open.accountId && e.threadId === open.threadId && !inSet(e));
            return head ? emailKey(head) : "";
          }
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
      // 移动类操作撤销时要把邮件放回原处: 提交前先从本地缓存记下各封所在的文件夹。
      const placements = new Map<string, Promise<EmailPlacement[] | null>>();
      const job = Promise.all(
        [...groups].map(async ([acc, ids]) => {
          if (undoable && m.remove) {
            const snap = api.emailPlacements(acc, ids).catch(() => null);
            placements.set(acc, snap);
            await snap;
          }
          await m.op(acc, ids);
        }),
      );
      // 撤销要等原操作提交完, 否则两个请求可能颠倒顺序到达服务器。
      const succeeded = job.then(
        () => true,
        () => false,
      );

      /** 把移走的邮件放回原来的文件夹。 */
      const restore = async () => {
        if (!(await succeeded)) return; // 原操作失败, 界面已经回到服务器的真实状态
        try {
          let count = 0;
          await Promise.all(
            [...placements].map(async ([acc, snap]) => {
              const places = await snap;
              if (!places?.length) return;
              await api.restoreEmails(acc, places);
              count += places.length;
            }),
          );
          if (count === 0) {
            toast.error(t("无法撤销"));
            return;
          }
          toast.success(t("已撤销"));
          // 还在原来的列表里时, 选中放回刚才那封。
          if (wasSelected && viewRef.current === view) setSelectedKey(wasSelected);
        } catch (err) {
          toast.error(errorMessage(err));
        } finally {
          for (const acc of placements.keys()) loadMailboxes(acc);
          reload();
        }
      };

      /** 已读、星标、固定改回原来的值, 界面照样乐观更新。 */
      const revert = () => {
        for (const [sig, group] of previous ?? []) {
          const old = JSON.parse(sig) as Partial<EmailSummary>;
          mutateRef.current({
            keys: group,
            patch: old,
            undoable: false,
            op: async (acc, ids) => {
              if (await succeeded) await patchOp(acc, ids, old);
            },
          });
        }
        toast.success(t("已撤销"));
      };

      // 没有任何一封真正变化(如把已读的标为已读)时不记, 免得盖掉上一步真正的操作。
      let entry: UndoEntry | null = null;
      const toastId = `mail-op-${seq}`;
      if (undoable && (m.remove || (previous && previous.size > 0))) {
        let used = false;
        const e: UndoEntry = {
          trigger: () => {
            if (used) return;
            used = true;
            if (lastUndo.current === e) {
              lastUndo.current = null;
              setCanUndo(false);
            }
            toast.dismiss(toastId);
            if (m.remove) void restore();
            else revert();
          },
        };
        lastUndo.current = e;
        setCanUndo(true);
        entry = e;
      }
      if (m.done) {
        toast.success(m.done, {
          id: toastId,
          ...(entry && { duration: UNDO_TOAST_MS, action: { label: t("撤销"), onClick: entry.trigger } }),
        });
      }

      job
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
  useEffect(() => {
    mutateRef.current = mutate;
  }, [mutate]);

  /** Ctrl+Z: 撤销最近一次邮件操作。 */
  const undoLast = useCallback(() => lastUndo.current?.trigger(), []);

  const trashIds = useCallback(
    (keys: string[]) => {
      if (currentKind === "trash") {
        const what = keys.length > 1 ? t("选中的 {0} 封邮件", keys.length) : t("这封邮件");
        if (!window.confirm(t("彻底删除{0}？此操作无法撤销。", what))) return;
        mutate({ keys, remove: true, undoable: false, op: (acc, ids) => api.deleteEmails(acc, ids), done: t("已彻底删除") });
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

  // 拖到侧栏文件夹: 进出垃圾邮件与「标记为垃圾邮件 / 不是垃圾邮件」一样打 $junk / $notjunk, 供服务端学习。
  const dropIds = useCallback(
    (keys: string[], acc: string, target: string) => {
      const kind = mailboxesByAccount[acc]?.find((m) => m.id === target)?.kind;
      const own = keys.filter((key) => parseEmailKey(key).accountId === acc);
      if (kind === "junk") junkIds(own, true);
      else if (kind === "inbox" && currentKind === "junk") junkIds(own, false);
      else moveIds(keys, acc, target);
    },
    [mailboxesByAccount, currentKind, junkIds, moveIds],
  );

  /** undoable 为 false 用于打开邮件时自动标为已读: 那不是用户的操作, 不该占掉 Ctrl+Z。 */
  const markReadIds = useCallback(
    (keys: string[], read: boolean, undoable = true) =>
      mutate({ keys, patch: { isUnread: !read }, undoable, op: (acc, ids) => api.markRead(acc, ids, read) }),
    [mutate],
  );

  /**
   * 回复、回复全部、转发一封未读邮件时把它标为已读 (与 Outlook 一样)。设置为「从不 (手动标记)」时也标:
   * 那个设置管的是打开邮件时的自动标记, 回复是用户对这封邮件的明确操作。
   * 不占 Ctrl+Z: 已读只是回复的附带结果。
   */
  const markReadOnReply = useCallback(
    (email: EmailSummary | EmailDetail) => {
      const key = emailKey(email);
      const shown = currentEmail.current;
      const unread =
        emailsRef.current.find((e) => emailKey(e) === key)?.isUnread ??
        (shown && emailKey(shown) === key ? shown.isUnread : email.isUnread);
      if (unread) markReadIds([key], true, false);
    },
    [markReadIds],
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

  // 当前列表为空是不是因为首次同步还没做完: 还没有任何文件夹可选, 或在看的账号还没同步过。
  const pendingAccounts = syncStatus?.pending ?? [];
  const viewPending = unified
    ? pendingAccounts.length > 0
    : mailboxId
      ? pendingAccounts.includes(accountId)
      : pendingAccounts.length > 0;
  const firstSyncRunning = viewPending && (!!syncStatus?.running || retryingSync);

  async function retryFirstSync() {
    if (retryingSync) return;
    setRetryingSync(true);
    try {
      // 逐个来: 第一个就失败时 (多半是网络) 不必把剩下的也各等一遍超时。
      for (const acc of pendingAccounts) await api.syncNow(acc);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setRetryingSync(false);
      api
        .getMailSyncStatus()
        .then(setSyncStatus)
        .catch(() => {});
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
      reply: (email, all) =>
        withDetail(email, (d) => {
          setDraft(replyDraft(email.accountId, d, all));
          markReadOnReply(email);
        }),
      forward: (email, asAttachment) =>
        withDetail(email, (d) => {
          setDraft(asAttachment ? forwardAsAttachmentDraft(d, email.accountId) : forwardDraft(d, email.accountId));
          markReadOnReply(email);
        }),
      archive: archiveIds,
      trash: trashIds,
      move: moveIds,
      junk: junkIds,
      markRead: markReadIds,
      toggleFlag,
      togglePin,
      setLabel: setLabelIds,
    }),
    [withDetail, setDraft, markReadOnReply, archiveIds, trashIds, moveIds, junkIds, markReadIds, toggleFlag, togglePin, setLabelIds],
  );

  /**
   * 单封标为未读之后关闭阅读栏, 否则停留在这封上会被再次自动标记已读。
   * 设置为「从不（手动标记）」时没有自动标记, 邮件留在原处, 可以接着用键盘操作。
   */
  const leaveAfterUnread = useCallback((key: string) => {
    api
      .getSettings()
      .then((s) => s?.markRead ?? "immediate")
      .catch(() => "immediate")
      .then((mode) => {
        if (mode !== "never") setSelectedKey((cur) => (cur === key ? "" : cur));
      });
  }, []);

  // 阅读栏打开的邮件; 统一视图里它的账号不一定是 accountId。
  const actionsFor = useCallback(
    (email: EmailDetail): ToolbarActions => {
      const acc = email.accountId;
      const key = emailKey(email);
      const remember = () => {
        openThread.current = { key, accountId: acc, threadId: email.threadId };
      };
      const startFrom = (d: ComposeDraft) => {
        setDraft(d);
        markReadOnReply(email);
      };
      return {
        reply: () => startFrom(replyDraft(acc, email, false)),
        replyAll: () => startFrom(replyDraft(acc, email, true)),
        forward: () => startFrom(forwardDraft(email, acc)),
        forwardAsAttachment: () => startFrom(forwardAsAttachmentDraft(email, acc)),
        archive: () => {
          remember();
          archiveIds([key]);
        },
        trash: () => {
          remember();
          trashIds([key]);
        },
        move: (target) => {
          remember();
          moveIds([key], acc, target);
        },
        setLabel: (label, set) => setLabelIds([key], label, set),
        junk: (junk) => {
          remember();
          junkIds([key], junk);
        },
        markUnread: () => {
          markReadIds([key], false);
          leaveAfterUnread(key);
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
    [archiveIds, trashIds, moveIds, setLabelIds, junkIds, markReadIds, markReadOnReply, leaveAfterUnread, toggleFlag, importEmails, bodyAppearance, setBodyAppearance, setDraft],
  );

  /* ---------- 键盘快捷键 ---------- */

  useEffect(() => {
    // 模块切走时只是隐藏, 不卸载: 不拦这里, 在日历、设置等页面按 Delete 会删掉邮件里选中的那封。
    if (!active) return;
    function onKey(e: KeyboardEvent) {
      const target = e.target as HTMLElement | null;
      // 在输入框、对话框、菜单、工具条 (写邮件的格式按钮) 里操作时不触发。
      if (target?.closest("input, textarea, select, [contenteditable=true], [role=dialog], [role=menu], [role=toolbar]")) return;

      // Esc 是固定的, 不在可自定义的键位里。
      if (e.key === "Escape" && !e.ctrlKey && !e.metaKey && !e.altKey) {
        if (focusMode) setFocusMode(false);
        return;
      }

      // 键位见 lib/shortcuts, 用户可以在设置里改。
      const action = shortcutActionOf(e);
      if (!action) return;

      const step = (delta: number) => {
        let idx = emails.findIndex((m) => emailKey(m) === selectedKey);
        // 选中的是展开会话里不在本列表的一封 (较早的、别的文件夹的): 从代表该会话的行起步,
        // 否则会跳回列表第一封。
        const cur = currentEmail.current;
        if (idx < 0 && cur && emailKey(cur) === selectedKey) {
          const tk = `${cur.accountId}/${cur.threadId || cur.id}`;
          idx = emails.findIndex((m) => `${m.accountId}/${m.threadId || m.id}` === tk);
        }
        const next = emails[idx < 0 ? 0 : idx + delta];
        if (next) setSelectedKey(emailKey(next));
      };

      /** 已读类操作的对象: 有多选时是多选, 否则是选中的那封。 */
      const readTarget = (): { keys: string[]; unread: boolean; batch: boolean } | null => {
        if (checked.size > 0) {
          return { keys: [...checked], unread: emails.some((m) => m.isUnread && checked.has(emailKey(m))), batch: true };
        }
        const shown = currentEmail.current;
        // 展开会话里不在本文件夹的邮件不在列表里, 用阅读栏里读到的详情。
        const one = emails.find((m) => emailKey(m) === selectedKey) ?? (shown && emailKey(shown) === selectedKey ? shown : null);
        return one ? { keys: [selectedKey], unread: one.isUnread, batch: false } : null;
      };
      const setRead = (read: boolean | "toggle") => {
        const target = readTarget();
        if (!target) return;
        const value = read === "toggle" ? target.unread : read;
        markReadIds(target.keys, value);
        if (!value && !target.batch) leaveAfterUnread(target.keys[0]);
      };

      const general: Partial<Record<ShortcutAction, () => void>> = {
        next: () => step(1),
        prev: () => step(-1),
        search: () => searchRef.current?.focus(),
        help: () => setShowShortcuts(true),
        compose: () => setDraft({ to: "", cc: "", subject: "", html: "" }),
        markRead: () => setRead(true),
        markUnread: () => setRead(false),
        toggleRead: () => setRead("toggle"),
        undo: undoLast,
      };
      const run = general[action];
      if (run) {
        e.preventDefault();
        return run();
      }

      const email = currentEmail.current;
      if (!email || emailKey(email) !== selectedKey) return;
      const a = actionsFor(email);
      const map: Partial<Record<ShortcutAction, () => void>> = {
        reply: a.reply,
        replyAll: a.replyAll,
        forward: a.forward,
        archive: a.archive,
        trash: a.trash,
        toggleFlag: a.toggleFlag,
        junk: () => a.junk(currentKind !== "junk"),
      };
      const fn = map[action];
      if (fn) {
        e.preventDefault();
        fn();
      }
    }

    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [active, emails, selectedKey, checked, focusMode, currentKind, actionsFor, markReadIds, leaveAfterUnread, setDraft, undoLast]);


  /**
   * 这封邮件所在的文件夹, 供右键菜单的「移动到」排除。
   * 展开会话里的邮件可能在别的文件夹(如已发送的回复), 此时不是当前列表的文件夹。
   */
  function boxOf(email: EmailSummary): string {
    const box = listBoxOf(email.accountId);
    const own = email.mailboxIds ?? [];
    return own.length === 0 || own.includes(box) ? box : own[0];
  }

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
                syncing={firstSyncRunning}
                unified={unified}
                onSelectUnified={(v) => {
                  setUnified(v);
                  setMobileView("list");
                }}
                onMailboxesChanged={loadMailboxes}
                onFolderAction={folderAction}
                onDropEmails={dropIds}
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
                  placeholder={
                    searchHint
                      ? unified ? t("搜索全部账号（{0}）", searchHint) : t("搜索全部文件夹（{0}）", searchHint)
                      : unified ? t("搜索全部账号") : t("搜索全部文件夹")
                  }
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

              <Button
                variant="ghost"
                size="icon"
                className="size-8 shrink-0"
                onClick={undoLast}
                disabled={!canUndo}
                aria-label={t("撤销")}
                title={shortcuts.undo.length > 0 ? `${t("撤销")} (${formatKeys(shortcuts.undo)})` : t("撤销")}
              >
                <RotateCcw className="size-4" />
              </Button>
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
                  disabled={syncing || firstSyncRunning}
                  aria-label={t("立即同步")}
                  title={t("立即同步")}
                >
                  {syncing || firstSyncRunning ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
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
                // 拖到侧栏文件夹上移动; 窄屏侧栏收起没有可放的地方, 已计划的邮件不移动。
                draggable={!compact && currentKind !== "scheduled"}
                emails={emails}
                selectedKey={selectedKey}
                highlightSelected={!compact}
                loading={loading}
                emptyState={
                  syncStatus && viewPending && !searching && (firstSyncRunning || syncStatus.error) ? (
                    <MailSyncState status={syncStatus} retrying={retryingSync} onRetry={retryFirstSync} />
                  ) : undefined
                }
                hasMore={hasMore && !searching}
                onSelect={selectEmail}
                onLoadMore={() => loadPage(emails.length, loadToken.current)}
                onToggleFlag={toggleFlag}
                onDelete={(email) => trashIds([emailKey(email)])}
                overlay={overlay}
                rowMenu={(email, row) => (
                  <EmailContextMenu
                    email={email}
                    checked={checked}
                    mailboxes={mailboxesByAccount[email.accountId] ?? []}
                    mailboxId={boxOf(email)}
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
                onMarkRead={(id) => markReadIds([emailKey({ accountId: selected.accountId, id })], true, false)}
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

/**
 * 回复、转发时优先用的发件地址: 原邮件寄到的地址(收件人、抄送、密送), 最后是发件人
 * (回复自己发出的邮件时沿用当时的身份)。对方的地址不会是自己的身份, 匹配时自然跳过。
 */
function fromCandidatesOf(email: EmailDetail): string[] {
  return [...(email.to ?? []), ...(email.cc ?? []), ...(email.bcc ?? []), ...(email.from ?? [])]
    .map((a) => a.email ?? "")
    .filter(Boolean);
}

/** 原邮件正文转成可以引用的 HTML。 */
function originalHtml(email: EmailDetail): string {
  return email.bodyHtml || textToHtml(email.bodyText || "");
}

/*
 * 引用原邮件的内联样式(发出去的邮件里只有内联样式可靠)。与 mcp_tools_compose.go 的同名常量一致。
 * 不写 color / background: 带配色的邮件在暗色主题下会被整封按亮色渲染(hasOwnColors), 灰度用 opacity。
 */
const QUOTE_STYLE = "margin-top:24px";
const QUOTE_META_STYLE = "font-size:13px;opacity:.65;margin:0 0 8px";
const QUOTE_BLOCK_STYLE = "margin:0;padding:0 0 0 12px;border-left:3px solid #d0d7de";
const FORWARD_META_STYLE = "font-size:13px;line-height:1.7;opacity:.7;margin:0 0 12px;padding:10px 0 0;border-top:1px solid #d0d7de";

function replyDraft(accountId: string, email: EmailDetail, all: boolean): ComposeDraft {
  // References 应是原邮件的 References 加上它自己的 Message-ID。本地没存完整链，
  // 用 In-Reply-To + Message-ID 近似，足够对方客户端把回复挂进线程。
  const refs = [email.inReplyTo, email.messageId].filter(Boolean) as string[];
  const intro = t("在 {0}，{1} 写道：", fullDate(email.receivedAt), displayAddressList(email.from));
  const to = email.replyTo?.length ? email.replyTo : email.from;
  // 抄送去掉已在收件人里的与重复的地址; 自己的地址由写信面板按发件身份去掉(这里还不知道身份)。
  const seen = new Set((to ?? []).map((a) => a.email?.toLowerCase()));
  const cc = [...(email.to ?? []), ...(email.cc ?? [])].filter((a) => {
    const key = a.email?.toLowerCase();
    if (!key || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
  return {
    to: addressesOf(to),
    cc: all ? addressesOf(cc) : "",
    dropOwnFromCc: all,
    original: {
      from: email.from?.[0]?.email ?? "",
      to: addressesOf(email.to),
      cc: all ? addressesOf(email.cc) : "",
    },
    subject: withPrefix("Re:", email.subject),
    html: "",
    quoteHtml: `<div class="czl-quote" style="${QUOTE_STYLE}"><div style="${QUOTE_META_STYLE}">${escapeHtml(intro)}</div><blockquote style="${QUOTE_BLOCK_STYLE}">${originalHtml(email)}</blockquote></div>`,
    inReplyTo: email.messageId,
    references: refs,
    replyTo: { accountId, emailId: email.id },
    inlineParts: inlinePartsOf(email),
    fromCandidates: fromCandidatesOf(email),
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
    quoteHtml: `<div class="czl-quote" style="${QUOTE_STYLE}"><div style="${FORWARD_META_STYLE}">${header}</div>${originalHtml(email)}</div>`,
    inlineParts: inlinePartsOf(email),
    // 普通转发带上原邮件的附件，引用服务器上已有的 blob，无需重新上传。
    attachments: (email.attachments ?? [])
      .filter((a) => !a.inline)
      .map((a) => ({ blobId: a.blobId, type: a.type, name: a.name, size: a.size })),
    fromCandidates: fromCandidatesOf(email),
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
    fromCandidates: fromCandidatesOf(email),
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
    // 草稿的发件人就是当时选的身份, 续编时不换成默认身份。
    fromCandidates: (email.from ?? []).map((a) => a.email ?? "").filter(Boolean),
    accountId,
  };
}
