"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Loader2, PenSquare, RefreshCw, Search, X } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ComposePane, type ComposeDraft } from "@/components/compose/compose-pane";
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

  const mailboxes = mailboxesByAccount[accountId] ?? [];
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

  const loadPage = useCallback(
    async (offset: number, token: number) => {
      if (!accountId || !mailboxId) return;
      setLoading(true);
      try {
        const page = await api.listEmails(accountId, mailboxId, PAGE_SIZE, offset);
        if (token !== loadToken.current) return;
        const list = page ?? [];
        setEmails((prev) => (offset === 0 ? list : [...prev, ...list]));
        setHasMore(list.length === PAGE_SIZE);
      } catch (err) {
        if (token === loadToken.current) toast.error(errorMessage(err));
      } finally {
        if (token === loadToken.current) setLoading(false);
      }
    },
    [accountId, mailboxId],
  );

  // 从通知或其它模块打开某封邮件时, 切换账号会触发下面的列表重置;
  // 先记下要打开的邮件, 重置时选中它而不是清空。
  const pendingEmail = useRef("");

  const openEmailIn = useCallback(
    (acc: string, id: string) => {
      if (acc === accountId) {
        setEmailId(id);
        return;
      }
      pendingEmail.current = id;
      const inbox = (mailboxesByAccount[acc] ?? []).find((m) => m.kind === "inbox");
      setAccountId(acc);
      if (inbox) setMailboxId(inbox.id);
    },
    [accountId, mailboxesByAccount],
  );

  useEffect(() => {
    if (!mailboxId) return;
    loadToken.current += 1;
    setEmails([]);
    setEmailId(pendingEmail.current);
    pendingEmail.current = "";
    setHasMore(true);
    setQuery("");
    loadPage(0, loadToken.current);
  }, [mailboxId, accountId, loadPage]);

  const reload = useCallback(() => {
    loadToken.current += 1;
    loadPage(0, loadToken.current);
  }, [loadPage]);

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
        reload();
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
        setEmails(found ?? []);
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
  }, [query, accountId, reload]);

  /* ---------- 操作 ---------- */

  /** 执行一个会让当前邮件离开列表的操作，然后选中下一封，避免阅读栏突然变空。 */
  const runAndAdvance = useCallback(
    async (id: string, op: () => Promise<unknown>, done?: string) => {
      const idx = emails.findIndex((e) => e.id === id);
      const next = emails[idx + 1] ?? emails[idx - 1];
      try {
        await op();
        if (done) toast.success(done);
        setEmailId(next?.id ?? "");
        reload();
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [emails, reload],
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

  /** 列表悬停的删除按钮: 已删除文件夹里彻底删除, 其它文件夹移到已删除。 */
  const deleteFromList = useCallback(
    (email: EmailSummary) => {
      if (currentKind === "trash") {
        if (!window.confirm("彻底删除这封邮件？此操作无法撤销。")) return;
        runAndAdvance(email.id, () => api.deleteEmails(accountId, [email.id]), "已彻底删除");
        return;
      }
      runAndAdvance(email.id, () => api.trashEmails(accountId, [email.id]), "已删除");
    },
    [accountId, currentKind, runAndAdvance],
  );

  const refreshCurrent = useCallback(() => {
    setRevision((r) => r + 1);
    reload();
  }, [reload]);

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

  const toggleFlag = useCallback(
    async (email: EmailSummary | EmailDetail) => {
      try {
        await api.markFlagged(accountId, [email.id], !email.isFlagged);
        refreshCurrent();
      } catch (err) {
        toast.error(errorMessage(err));
      }
    },
    [accountId, refreshCurrent],
  );

  const importEmails = useCallback(async () => {
    if (!accountId || !mailboxId) {
      toast.error("请先选择一个文件夹");
      return;
    }
    try {
      const res = await api.importEmails(accountId, mailboxId);
      if (!res || (res.imported === 0 && res.failed === 0)) return;
      if (res.failed > 0) {
        toast.warning(`导入 ${res.imported} 封，失败 ${res.failed} 封：${res.firstError}`);
      } else {
        toast.success(`已导入 ${res.imported} 封`);
      }
      reload();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, [accountId, mailboxId, reload]);

  const actionsFor = useCallback(
    (email: EmailDetail): ToolbarActions => ({
      reply: () => setDraft(replyDraft(accountId, email, false)),
      replyAll: () => setDraft(replyDraft(accountId, email, true)),
      forward: () => setDraft(forwardDraft(email)),
      forwardAsAttachment: () =>
        setDraft({
          to: "",
          cc: "",
          subject: withPrefix("Fwd:", email.subject),
          html: "",
          // 原邮件的 blob 就是完整的 RFC 5322 原文，直接引用，无需下载再上传。
          attachments: [
            { blobId: email.blobId, type: "message/rfc822", name: `${email.subject || "email"}.eml`, size: email.size },
          ],
        }),
      archive: () => runAndAdvance(email.id, () => api.archiveEmails(accountId, [email.id]), "已归档"),
      trash: () => runAndAdvance(email.id, () => api.trashEmails(accountId, [email.id])),
      move: (target) => runAndAdvance(email.id, () => api.moveEmails(accountId, [email.id], target), "已移动"),
      setLabel: async (label, set) => {
        try {
          await api.setLabel(accountId, [email.id], label, set);
          refreshCurrent();
        } catch (err) {
          toast.error(errorMessage(err));
        }
      },
      junk: (junk) =>
        runAndAdvance(
          email.id,
          () => api.markJunk(accountId, [email.id], junk),
          junk ? "已标记为垃圾邮件" : "已移回收件箱",
        ),
      markUnread: async () => {
        try {
          await api.markRead(accountId, [email.id], false);
          // 标为未读后关闭阅读栏，否则停留在这封上会被再次自动标记已读。
          setEmailId("");
          reload();
        } catch (err) {
          toast.error(errorMessage(err));
        }
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
    [accountId, runAndAdvance, refreshCurrent, reload, toggleFlag, importEmails, bodyAppearance, setBodyAppearance, setDraft],
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
              />
            </ScrollArea>
          </>
        }
        list={
          <>
            <div className="border-border flex shrink-0 items-center gap-2 border-b px-3 py-2">
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

            <div className="min-h-0 flex-1">
              <EmailList
                emails={emails}
                selectedId={emailId}
                loading={loading}
                hasMore={hasMore && !searching}
                onSelect={selectEmail}
                onLoadMore={() => loadPage(emails.length, loadToken.current)}
                onToggleFlag={toggleFlag}
                onDelete={deleteFromList}
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
    // 普通转发带上原邮件的附件，引用服务器上已有的 blob，无需重新上传。
    attachments: (email.attachments ?? [])
      .filter((a) => !a.inline)
      .map((a) => ({ blobId: a.blobId, type: a.type, name: a.name, size: a.size })),
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
  };
}
