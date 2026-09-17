"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Archive,
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
import { MailSidebar } from "@/components/mail-sidebar";
import { mailboxLabel } from "@/components/mailbox-tree";
import { PaneLayout } from "@/components/pane-layout";
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
import { listen } from "@/lib/app-bus";
import { escapeHtml, splitDraftHtml, textToHtml } from "@/lib/html";

const PAGE_SIZE = 50;
// 定位一封邮件时最多加载多少封。再深就不值得了: 每 200 封一次查询,
// 列表也长到滚动条没有意义, 这时只打开邮件、不强求列表停在它上面。
const LOCATE_LIMIT = 2000;

/** 为了让第 index 封出现在列表里, 需要加载的封数(按整页取, 免得刚好停在页边)。 */
function locateDepth(index: number): number {
  return Math.min(LOCATE_LIMIT, (Math.floor(index / PAGE_SIZE) + 1) * PAGE_SIZE);
}

export function MailShell({ onSignedOut }: { onSignedOut: () => void }) {
  const { bodyMode, bodyAppearance, setBodyAppearance } = useAppearance();

  const [username, setUsername] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [mailboxesByAccount, setMailboxesByAccount] = useState<Record<string, Mailbox[]>>({});
  const [accountId, setAccountId] = useState("");
  const [mailboxId, setMailboxId] = useState("");
  const [labels, setLabels] = useState<string[]>([]);

  const [emails, setEmails] = useState<EmailSummary[]>([]);
  const [emailId, setEmailId] = useState("");
  // 多选的邮件; 切换文件夹或搜索时清空。
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const lastChecked = useRef("");
  const [loading, setLoading] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [revision, setRevision] = useState(0);

  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [draft, setDraftState] = useState<ComposeDraft | null>(null);
  // 每次打开写信面板都换一个 key, 让面板重新挂载而不是沿用上一封的状态。
  const [draftKey, setDraftKey] = useState(0);
  const setDraft = useCallback((d: ComposeDraft | null) => {
    setDraftState(d);
    if (d) setDraftKey((k) => k + 1);
  }, []);
  const [focusMode, setFocusMode] = useState(false);
  const [showShortcuts, setShowShortcuts] = useState(false);
  const [sourceTarget, setSourceTarget] = useState<{ accountId: string; emailId: string } | null>(null);

  // 当前打开的邮件，供键盘快捷键使用。
  const currentEmail = useRef<EmailDetail | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);

  // 每次切换账号/邮箱都会重来一遍分页，用一个自增的令牌把过期请求的结果丢掉，
  // 否则慢的那次返回会覆盖快的那次，列表显示成上一个邮箱的内容。
  const loadToken = useRef(0);

  const mailboxes = useMemo(() => mailboxesByAccount[accountId] ?? [], [mailboxesByAccount, accountId]);
  const currentKind = mailboxes.find((m) => m.id === mailboxId)?.kind ?? "";

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

      // 尚未选中文件夹时落到个人账号的收件箱。首次登录时缓存为空，
      // 这里第一次拿不到收件箱；引导同步完成后的变更事件会再调一次。
      setAccountId((curAcc) => {
        if (curAcc && list.some((a) => a.id === curAcc)) return curAcc;
        const idx = Math.max(
          0,
          list.findIndex((a) => a.isPersonal),
        );
        const inbox = all[idx]?.find((m) => m.kind === "inbox") ?? all[idx]?.[0];
        if (inbox) setMailboxId(inbox.id);
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

  useEffect(() => {
    if (!accountId) return;
    api
      .listLabels(accountId)
      .then((l) => setLabels(l ?? []))
      .catch(() => setLabels([]));
  }, [accountId, revision]);

  /* ---------- 邮件列表 ---------- */

  // 乐观更新的覆盖层。删除、移动、标记等操作发出后界面立刻显示预期结果, 请求在后台完成;
  // 完成前任何一次重新加载(同步推送、翻页、搜索)的结果都要套上这层, 否则刚删掉的邮件会闪回来。
  // 条目记下操作序号, 同一封邮件连续操作时只由最后一次操作撤掉。
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
      .filter((e) => !hidden.has(e.id))
      .map((e) => {
        const p = patched.get(e.id);
        return p ? ({ ...e, ...p.patch } as EmailSummary) : e;
      });
  }, []);

  const loadPage = useCallback(
    async (offset: number, token: number) => {
      if (!accountId || !mailboxId) return;
      if (emptying.current.has(`${accountId}/${mailboxId}`)) {
        setEmails([]);
        setHasMore(false);
        return;
      }
      setLoading(true);
      try {
        const page = await api.listEmails(accountId, mailboxId, PAGE_SIZE, offset);
        if (token !== loadToken.current) return;
        const list = page ?? [];
        const shown = overlay(list);
        setEmails((prev) => {
          if (offset === 0) return shown;
          // 乐观移除的邮件让 offset 比服务端少算几封, 下一页开头可能与已有的重叠。
          const have = new Set(prev.map((e) => e.id));
          return [...prev, ...shown.filter((e) => !have.has(e.id))];
        });
        setHasMore(list.length === PAGE_SIZE);
      } catch (err) {
        if (token === loadToken.current) toast.error(errorMessage(err));
      } finally {
        if (token === loadToken.current) setLoading(false);
      }
    },
    [accountId, mailboxId, overlay],
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
      if (!accountId || !mailboxId) return;
      if (emptying.current.has(`${accountId}/${mailboxId}`)) {
        setEmails([]);
        setHasMore(false);
        return;
      }
      const out: EmailSummary[] = [];
      try {
        // 后端单次最多 200 封。
        for (let off = 0; off < want; off += 200) {
          const n = Math.min(200, want - off);
          const page = (await api.listEmails(accountId, mailboxId, n, off)) ?? [];
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
    [accountId, mailboxId, overlay],
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
  const pendingEmail = useRef<{ id: string; index: number } | null>(null);

  /**
   * 打开某封邮件, 并让列表停在能看见它的位置。
   *
   * 只设置选中 id 不够: 邮件可能在别的账号/文件夹, 或在当前文件夹里靠后、还没翻到那一页,
   * 列表就会停在原处, 看起来"打开了但找不到"。先问后端它在哪、排第几, 再切过去并把
   * 前面的页一并加载出来, 行渲染出来后 EmailList 自己滚过去。
   */
  const openEmailIn = useCallback(
    async (acc: string, id: string) => {
      const loc = await api
        .locateEmail(acc, id, acc === accountId ? mailboxId : "")
        .catch(() => null);
      const index = loc?.index ?? 0;

      if (acc === accountId && (!loc || loc.mailboxId === mailboxId)) {
        // 搜索结果不是文件夹列表, 要先退出搜索; 由下面的搜索 effect 接着加载并选中。
        if (searching) {
          pendingEmail.current = { id, index };
          setQuery("");
          return;
        }
        if (!emailsRef.current.some((e) => e.id === id) && index >= emailsRef.current.length) {
          loadToken.current += 1;
          minDepth.current = locateDepth(index);
          loadPrefix(loadToken.current, minDepth.current);
        }
        setEmailId(id);
        return;
      }

      pendingEmail.current = { id, index };
      const boxes = mailboxesByAccount[acc] ?? [];
      const target =
        (loc && boxes.find((m) => m.id === loc.mailboxId)) ?? boxes.find((m) => m.kind === "inbox");
      setAccountId(acc);
      if (target) setMailboxId(target.id);
    },
    [accountId, mailboxId, mailboxesByAccount, searching, loadPrefix],
  );

  useEffect(() => {
    if (!mailboxId) return;
    loadToken.current += 1;
    const pending = pendingEmail.current;
    pendingEmail.current = null;
    setEmails([]);
    setEmailId(pending?.id ?? "");
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
  }, [mailboxId, accountId, loadPage, loadPrefix]);

  const reload = useCallback(() => {
    loadToken.current += 1;
    refreshLoaded(loadToken.current);
  }, [refreshLoaded]);

  // 预取当前邮件后面几封的正文, 按 j/↓ 翻下一封时不用等网络。
  useEffect(() => {
    if (!emailId || !accountId) return;
    const idx = emails.findIndex((e) => e.id === emailId);
    if (idx < 0) return;
    const ids = emails.slice(idx + 1, idx + 4).map((e) => e.id);
    if (idx > 0) ids.push(emails[idx - 1].id);
    const t = setTimeout(() => api.prefetchBodies(accountId, ids).catch(() => {}), 400);
    return () => clearTimeout(t);
  }, [emailId, accountId, emails]);

  /* ---------- 后台同步事件 ---------- */

  useEffect(() => {
    const offConnected = onEvent(Events.connected, (name: string) => {
      setUsername(name);
      loadAccounts();
    });

    const offChanged = onEvent(Events.mailChanged, (changed: string) => {
      // 登录后第一轮同步到达时可能还没有选中任何文件夹。
      if (!mailboxId) {
        loadAccounts();
        return;
      }
      // 其它账号只刷新文件夹（未读数），不打断正在看的列表。
      loadMailboxes(changed);
      if (changed === accountId) reload();
    });

    const offOpen = onEvent(Events.openEmail, (ref: string) => {
      const [acc, id] = String(ref).split("/");
      if (acc && id) openEmailIn(acc, id);
    });
    // 其它模块(通讯录)发来的「打开这封邮件」「给这个人写信」。
    const offBusOpen = listen("openEmail", ({ accountId: acc, emailId: id }) => openEmailIn(acc, id));
    const offCompose = listen("compose", ({ to, cc, bcc, subject, body }) =>
      setDraft({ to, cc: cc ?? "", bcc: bcc ?? "", subject: subject ?? "", html: body ? textToHtml(body) : "" }),
    );

    return () => {
      offConnected();
      offChanged();
      offOpen();
      offBusOpen();
      offCompose();
    };
  }, [accountId, mailboxId, reload, loadAccounts, loadMailboxes, openEmailIn, setDraft]);

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
          setEmailId(pending.id);
          setLoading(true);
          minDepth.current = locateDepth(pending.index);
          loadPrefix(loadToken.current, minDepth.current);
        } else {
          reload();
        }
      }
      return;
    }

    // 防抖：搜索打的是本地库，但每次按键重查仍会让长列表反复重排。
    const timer = setTimeout(async () => {
      loadToken.current += 1;
      const token = loadToken.current;
      setSearching(true);
      setLoading(true);
      try {
        const found = await api.searchEmails(accountId, q, 100);
        if (token !== loadToken.current) return;
        setEmails(overlay(found ?? []));
        setHasMore(false);
      } catch (err) {
        toast.error(errorMessage(err));
      } finally {
        if (token === loadToken.current) setLoading(false);
      }
    }, 250);

    return () => clearTimeout(timer);
    // searching 由本 effect 自己设置，列进依赖会造成循环。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, accountId, reload, loadPrefix, overlay]);

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
   * remove 表示邮件会离开当前列表(删除、移动、归档、垃圾邮件), 选中的邮件随之跳到下一封;
   * patch 是对摘要字段的预期修改(已读、星标、固定)。
   * 失败时提示并重新加载, 界面回到服务器的真实状态。
   */
  const mutate = useCallback(
    (m: { ids: string[]; op: () => Promise<unknown>; remove?: boolean; patch?: Partial<EmailSummary>; done?: string }) => {
      const ids = m.ids;
      if (ids.length === 0) return;
      const idSet = new Set(ids);
      const seq = ++mutationSeq.current;
      const acc = accountId;
      const box = mailboxId;
      const list = emailsRef.current;

      if (m.remove) {
        for (const id of ids) hiddenIds.current.set(id, seq);
        adjustUnread(acc, box, -list.filter((e) => idSet.has(e.id) && e.isUnread).length);
        // 阅读栏不突然变空: 选中的邮件被移走时, 跳到它后面(没有则前面)第一封没被移走的。
        setEmailId((cur) => {
          if (!idSet.has(cur)) return cur;
          const idx = list.findIndex((e) => e.id === cur);
          const after = list.slice(idx + 1).find((e) => !idSet.has(e.id));
          const before = list
            .slice(0, Math.max(0, idx))
            .reverse()
            .find((e) => !idSet.has(e.id));
          return (after ?? before)?.id ?? "";
        });
        setEmails((prev) => prev.filter((e) => !idSet.has(e.id)));
        setChecked((prev) => {
          if (![...prev].some((id) => idSet.has(id))) return prev;
          return new Set([...prev].filter((id) => !idSet.has(id)));
        });
      }
      if (m.patch) {
        const patch = m.patch;
        if (patch.isUnread !== undefined) {
          const changed = list.filter((e) => idSet.has(e.id) && e.isUnread !== patch.isUnread).length;
          adjustUnread(acc, box, patch.isUnread ? changed : -changed);
        }
        for (const id of ids) {
          patchedIds.current.set(id, { patch: { ...patchedIds.current.get(id)?.patch, ...patch }, seq });
        }
        setEmails((prev) => prev.map((e) => (idSet.has(e.id) ? ({ ...e, ...patch } as EmailSummary) : e)));
      }
      if (m.done) toast.success(m.done);

      m.op()
        .catch((err) => toast.error(errorMessage(err)))
        .finally(() => {
          for (const id of ids) {
            if (hiddenIds.current.get(id) === seq) hiddenIds.current.delete(id);
            if (patchedIds.current.get(id)?.seq === seq) patchedIds.current.delete(id);
          }
          // 成功时后端已同步完, 重新加载读到的就是结果; 失败时重新加载把界面恢复原样。
          loadMailboxes(acc);
          // 阅读栏只显示星标; 已读、固定变化不重载正文, 否则打开未读邮件时正文会闪一下。
          if (m.patch?.isFlagged !== undefined) refreshCurrent();
          else reload();
        });
    },
    [accountId, mailboxId, adjustUnread, loadMailboxes, reload, refreshCurrent],
  );

  const trashIds = useCallback(
    (ids: string[]) => {
      if (currentKind === "trash") {
        const what = ids.length > 1 ? `选中的 ${ids.length} 封邮件` : "这封邮件";
        if (!window.confirm(`彻底删除${what}？此操作无法撤销。`)) return;
        mutate({ ids, remove: true, op: () => api.deleteEmails(accountId, ids), done: "已彻底删除" });
        return;
      }
      mutate({ ids, remove: true, op: () => api.trashEmails(accountId, ids), done: "已删除" });
    },
    [accountId, currentKind, mutate],
  );

  const archiveIds = useCallback(
    (ids: string[]) => mutate({ ids, remove: true, op: () => api.archiveEmails(accountId, ids), done: "已归档" }),
    [accountId, mutate],
  );

  const moveIds = useCallback(
    (ids: string[], target: string) => {
      const box = mailboxes.find((m) => m.id === target);
      mutate({
        ids,
        remove: true,
        op: () => api.moveEmails(accountId, ids, target),
        done: box ? `已移动到「${mailboxLabel(box)}」` : "已移动",
      });
    },
    [accountId, mailboxes, mutate],
  );

  const junkIds = useCallback(
    (ids: string[], junk: boolean) =>
      mutate({
        ids,
        remove: true,
        op: () => api.markJunk(accountId, ids, junk),
        done: junk ? "已标记为垃圾邮件" : "已移回收件箱",
      }),
    [accountId, mutate],
  );

  const markReadIds = useCallback(
    (ids: string[], read: boolean) =>
      mutate({ ids, patch: { isUnread: !read }, op: () => api.markRead(accountId, ids, read) }),
    [accountId, mutate],
  );

  const flagIds = useCallback(
    (ids: string[], flagged: boolean) =>
      mutate({ ids, patch: { isFlagged: flagged }, op: () => api.markFlagged(accountId, ids, flagged) }),
    [accountId, mutate],
  );

  const toggleFlag = useCallback(
    (email: EmailSummary | EmailDetail) => flagIds([email.id], !email.isFlagged),
    [flagIds],
  );

  const togglePin = useCallback(
    (email: EmailSummary) =>
      mutate({
        ids: [email.id],
        patch: { isPinned: !email.isPinned },
        op: () => api.setPinned(accountId, [email.id], !email.isPinned),
      }),
    [accountId, mutate],
  );

  const setLabelIds = useCallback(
    (ids: string[], label: string, set: boolean) => {
      api
        .setLabel(accountId, ids, label, set)
        .then(refreshCurrent)
        .catch((err) => toast.error(errorMessage(err)));
    },
    [accountId, refreshCurrent],
  );

  /** 列表里点选邮件。草稿直接进入编辑。 */
  const selectEmail = useCallback(
    async (id: string) => {
      const summary = emails.find((e) => e.id === id);
      if (currentKind !== "drafts" && !summary?.isDraft) {
        setDraft(null);
        setEmailId(id);
        return;
      }
      try {
        const email = await api.getEmail(accountId, id);
        setEmailId(id);
        setDraft(draftFromEmail(email));
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [emails, currentKind, accountId, setDraft],
  );

  const checkEmail = useCallback(
    (id: string, range: boolean) => {
      setChecked((prev) => {
        const next = new Set(prev);
        if (range && lastChecked.current) {
          const a = emails.findIndex((e) => e.id === lastChecked.current);
          const b = emails.findIndex((e) => e.id === id);
          if (a >= 0 && b >= 0) {
            for (let i = Math.min(a, b); i <= Math.max(a, b); i++) next.add(emails[i].id);
            return next;
          }
        }
        if (next.has(id)) next.delete(id);
        else next.add(id);
        return next;
      });
      lastChecked.current = id;
    },
    [emails],
  );

  async function sync() {
    if (!accountId || syncing) return;
    setSyncing(true);
    try {
      await api.syncNow(accountId);
      reload();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSyncing(false);
    }
  }

  const importInto = useCallback(
    async (acc: string, box: string) => {
      try {
        const res = await api.importEmails(acc, box);
        if (!res || (res.imported === 0 && res.failed === 0)) return;
        if (res.failed > 0) {
          toast.warning(`导入 ${res.imported} 封，失败 ${res.failed} 封：${res.firstError}`);
        } else {
          toast.success(`已导入 ${res.imported} 封`);
        }
        loadMailboxes(acc);
        if (acc === accountId && box === mailboxId) reload();
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [accountId, mailboxId, loadMailboxes, reload],
  );

  const importEmails = useCallback(() => {
    if (!accountId || !mailboxId) {
      toast.error("请先选择一个文件夹");
      return;
    }
    importInto(accountId, mailboxId);
  }, [accountId, mailboxId, importInto]);

  /** 侧栏文件夹右键菜单里涉及邮件的操作。标记已读与清空同样先显示结果, 后台逐批处理。 */
  const folderAction = useCallback(
    async (acc: string, action: "markRead" | "import" | "empty" | "refresh", box: Mailbox) => {
      const name = mailboxLabel(box);
      const isCurrent = acc === accountId && box.id === mailboxId;
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
            setEmails((prev) => prev.map((e) => (e.isUnread ? ({ ...e, isUnread: false } as EmailSummary) : e)));
          }
          toast.loading(`正在将「${name}」标记为已读…`, { id: toastId });
          try {
            const n = await api.markMailboxRead(acc, box.id);
            toast.success(n > 0 ? `已将 ${n} 封标记为已读` : "没有未读邮件", { id: toastId });
          } catch (err) {
            toast.error(errorMessage(err), { id: toastId });
          }
          loadMailboxes(acc);
          if (isCurrent) reload();
          return;
        case "empty": {
          if (!window.confirm(`彻底删除「${name}」里的全部 ${box.totalEmails} 封邮件？此操作无法撤销。`)) return;
          const key = `${acc}/${box.id}`;
          emptying.current.add(key);
          adjustUnread(acc, box.id, "zero");
          if (isCurrent) {
            setEmails([]);
            setEmailId("");
            setChecked(new Set());
            setHasMore(false);
          }
          toast.loading(`正在清空「${name}」…`, { id: toastId });
          try {
            const n = await api.emptyMailbox(acc, box.id);
            toast.success(`已清空「${name}」，删除 ${n} 封`, { id: toastId });
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
    [accountId, mailboxId, importInto, loadMailboxes, reload, adjustUnread],
  );

  /** 列表右键的回复、转发要用完整邮件(正文、附件): 先读缓存, 没有正文时再拉。 */
  const withDetail = useCallback(
    async (id: string, fn: (email: EmailDetail) => void) => {
      try {
        let email = await api.getEmail(accountId, id);
        if (!email.bodyFetched) email = await api.fetchBody(accountId, id);
        fn(email);
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [accountId],
  );

  const menuActions = useMemo<EmailMenuActions>(
    () => ({
      reply: (email, all) => withDetail(email.id, (d) => setDraft(replyDraft(accountId, d, all))),
      forward: (email, asAttachment) =>
        withDetail(email.id, (d) => setDraft(asAttachment ? forwardAsAttachmentDraft(d) : forwardDraft(d))),
      archive: archiveIds,
      trash: trashIds,
      move: moveIds,
      junk: junkIds,
      markRead: markReadIds,
      toggleFlag,
      togglePin,
      setLabel: setLabelIds,
    }),
    [accountId, withDetail, setDraft, archiveIds, trashIds, moveIds, junkIds, markReadIds, toggleFlag, togglePin, setLabelIds],
  );

  const actionsFor = useCallback(
    (email: EmailDetail): ToolbarActions => ({
      reply: () => setDraft(replyDraft(accountId, email, false)),
      replyAll: () => setDraft(replyDraft(accountId, email, true)),
      forward: () => setDraft(forwardDraft(email)),
      forwardAsAttachment: () => setDraft(forwardAsAttachmentDraft(email)),
      archive: () => archiveIds([email.id]),
      trash: () => trashIds([email.id]),
      move: (target) => moveIds([email.id], target),
      setLabel: (label, set) => setLabelIds([email.id], label, set),
      junk: (junk) => junkIds([email.id], junk),
      markUnread: () => {
        markReadIds([email.id], false);
        // 标为未读后关闭阅读栏，否则停留在这封上会被再次自动标记已读。
        setEmailId("");
      },
      toggleFlag: () => toggleFlag(email),
      print: () => printEmail(email, false),
      viewSource: () => setSourceTarget({ accountId, emailId: email.id }),
      exportEml: async () => {
        try {
          const path = await api.exportEmail(accountId, email.id);
          if (path) toast.success(`已导出到 ${path}`);
        } catch (err) {
          toast.error(errorMessage(err));
        }
      },
      importEml: importEmails,
      showShortcuts: () => setShowShortcuts(true),
      toggleFocus: () => setFocusMode((f) => !f),
      toggleBodyLight: () => setBodyAppearance(bodyAppearance === "light" ? "follow" : "light"),
    }),
    [
      accountId,
      archiveIds,
      trashIds,
      moveIds,
      setLabelIds,
      junkIds,
      markReadIds,
      toggleFlag,
      importEmails,
      bodyAppearance,
      setBodyAppearance,
      setDraft,
    ],
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
        const idx = emails.findIndex((m) => m.id === emailId);
        const next = emails[idx < 0 ? 0 : idx + delta];
        if (next) setEmailId(next.id);
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
      if (!email || email.id !== emailId) return;
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
  }, [emails, emailId, focusMode, currentKind, actionsFor, setDraft]);

  const selectedMailbox = useMemo(() => mailboxes.find((m) => m.id === mailboxId), [mailboxes, mailboxId]);

  return (
    <div className="h-full">
      <PaneLayout
        id="mail"
        collapsed={focusMode}
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
                写邮件
              </Button>
            </div>

            <ScrollArea className="min-h-0 flex-1 px-2 pb-2">
              <MailSidebar
                accounts={accounts}
                mailboxesByAccount={mailboxesByAccount}
                accountId={accountId}
                mailboxId={mailboxId}
                onSelect={(acc, box) => {
                  setAccountId(acc);
                  setMailboxId(box);
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
              <Checkbox
                aria-label="全选"
                title="全选"
                disabled={emails.length === 0}
                checked={checked.size === 0 ? false : checked.size === emails.length ? true : "indeterminate"}
                onCheckedChange={(v) => setChecked(v === true ? new Set(emails.map((e) => e.id)) : new Set())}
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
                  placeholder={`搜索${selectedMailbox ? mailboxLabel(selectedMailbox) : "邮件"}（/）`}
                  className="h-8 pr-8 pl-8"
                  aria-label="搜索邮件"
                />
                {query && (
                  <button
                    type="button"
                    onClick={() => setQuery("")}
                    aria-label="清除搜索"
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
                onClick={sync}
                disabled={syncing}
                aria-label="立即同步"
              >
                {syncing ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
              </Button>
            </div>

            {checked.size > 0 && (
              <div className="border-border bg-secondary flex shrink-0 items-center gap-0.5 overflow-hidden border-b px-2 py-1 text-sm">
                <span className="mr-auto pl-1 text-xs whitespace-nowrap tabular-nums">已选 {checked.size} 封</span>
                <BulkBtn label="标为已读" onClick={() => markReadIds([...checked], true)}>
                  <MailOpen />
                </BulkBtn>
                <BulkBtn label="归档" onClick={() => archiveIds([...checked])}>
                  <Archive />
                </BulkBtn>
                <BulkBtn label={currentKind === "trash" ? "彻底删除" : "删除"} onClick={() => trashIds([...checked])}>
                  <Trash2 />
                </BulkBtn>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon" className="size-7 [&_svg]:size-4" title="更多" aria-label="更多批量操作">
                      <MoreHorizontal />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end" className="w-52">
                    <DropdownMenuItem onSelect={() => markReadIds([...checked], false)}>
                      <MailIcon className="size-4" />
                      标为未读
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => flagIds([...checked], true)}>
                      <Star className="size-4" />
                      加星标
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => flagIds([...checked], false)}>
                      <Star className="size-4" />
                      取消星标
                    </DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => junkIds([...checked], currentKind !== "junk")}>
                      <ShieldAlert className="size-4" />
                      {currentKind === "junk" ? "不是垃圾邮件" : "标记为垃圾邮件"}
                    </DropdownMenuItem>
                    <DropdownMenuSub>
                      <DropdownMenuSubTrigger>
                        <FolderInput className="size-4" />
                        移动到
                      </DropdownMenuSubTrigger>
                      <DropdownMenuSubContent className="max-h-80 w-52 overflow-y-auto">
                        {mailboxes
                          .filter((m) => m.id !== mailboxId && m.kind !== "drafts" && m.kind !== "scheduled")
                          .map((m) => (
                            <DropdownMenuItem key={m.id} onSelect={() => moveIds([...checked], m.id)}>
                              <span className="truncate">{mailboxLabel(m)}</span>
                            </DropdownMenuItem>
                          ))}
                      </DropdownMenuSubContent>
                    </DropdownMenuSub>
                  </DropdownMenuContent>
                </DropdownMenu>
                <BulkBtn label="取消选择" onClick={() => setChecked(new Set())}>
                  <X />
                </BulkBtn>
              </div>
            )}

            <div className="min-h-0 flex-1">
              <EmailList
                accountId={accountId}
                checked={checked}
                onCheck={checkEmail}
                emails={emails}
                selectedId={emailId}
                loading={loading}
                hasMore={hasMore && !searching}
                onSelect={selectEmail}
                onLoadMore={() => loadPage(emails.length, loadToken.current)}
                onToggleFlag={toggleFlag}
                onDelete={(email) => trashIds([email.id])}
                rowMenu={(email, row) => (
                  <EmailContextMenu
                    accountId={accountId}
                    email={email}
                    checked={checked}
                    mailboxes={mailboxes}
                    mailboxId={mailboxId}
                    currentKind={currentKind}
                    knownLabels={labels}
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
                accountId={accountId}
                draft={draft}
                onClose={() => setDraft(null)}
                onSent={reload}
              />
            ) : emailId ? (
              <EmailView
                key={`${accountId}/${emailId}`}
                accountId={accountId}
                emailId={emailId}
                mailboxes={mailboxes}
                currentKind={currentKind}
                labels={labels}
                focusMode={focusMode}
                bodyMode={bodyMode}
                bodyAlwaysLight={bodyAppearance === "light"}
                actions={actionsFor}
                onLoaded={(e) => {
                  currentEmail.current = e;
                }}
                revision={revision}
                onOpenEmail={(id) => setEmailId(id)}
                onMarkRead={(id) => markReadIds([id], true)}
              />
            ) : (
              <div className="text-muted-foreground flex h-full items-center justify-center text-sm">选择一封邮件</div>
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
  const intro = `在 ${fullDate(email.receivedAt)}，${displayAddressList(email.from)} 写道：`;
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
  };
}

function forwardDraft(email: EmailDetail): ComposeDraft {
  const header = [
    "-------- 转发的邮件 --------",
    `发件人：${displayAddressList(email.from)}`,
    `时间：${fullDate(email.receivedAt)}`,
    `收件人：${displayAddressList(email.to)}`,
    `主题：${email.subject}`,
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
  };
}

/** 作为附件转发: 原邮件的 blob 就是完整的 RFC 5322 原文，直接引用，无需下载再上传。 */
function forwardAsAttachmentDraft(email: EmailDetail): ComposeDraft {
  return {
    to: "",
    cc: "",
    subject: withPrefix("Fwd:", email.subject),
    html: "",
    attachments: [{ blobId: email.blobId, type: "message/rfc822", name: `${email.subject || "email"}.eml`, size: email.size }],
  };
}

/** 打开已有草稿继续编辑。 */
function draftFromEmail(email: EmailDetail): ComposeDraft {
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
  };
}
