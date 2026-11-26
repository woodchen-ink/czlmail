"use client";

import { dateLocale, t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  BookPlus,
  BookUser,
  Building2,
  CalendarDays,
  Cake,
  Check,
  FolderInput,
  Globe,
  Loader2,
  Mail,
  MapPin,
  MoreHorizontal,
  Pencil,
  Phone,
  Plus,
  Search,
  ShieldCheck,
  StickyNote,
  Tag,
  Trash2,
  Upload,
  UserPlus,
  UserRound,
  UsersRound,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { ContactEditor, bookLabel, emptyContact } from "@/components/contacts/contact-editor";
import { Correspondence } from "@/components/contacts/correspondence";
import { PaneLayout, type MobilePane } from "@/components/pane-layout";
import { SenderAvatar } from "@/components/sender-avatar";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { OptionSelect } from "@/components/option-select";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Account,
  type AddressBook,
  type ContactForm,
  type ContactSummary,
  type EventOccurrence,
  type PIMChange,
} from "@/lib/api";
import { supports, withFeature } from "@/lib/account-features";
import { send } from "@/lib/app-bus";
import { hm, shortDate } from "@/lib/date";
import { localUrl } from "@/lib/native-bridge";

/** 当前范围: 全部 / 某通讯录 / 某类别。 */
type Scope =
  | { kind: "all" }
  | { kind: "book"; accountId: string; bookId: string }
  | { kind: "keyword"; keyword: string };

type Pane = { kind: "none" } | { kind: "view"; accountId: string; id: string } | { kind: "edit"; form: ContactForm };

type DetailTab = "info" | "emails";

type BookDialog = { accountId: string; book?: AddressBook } | null;

const TRUSTED = "Trusted Senders";

export function ContactsShell({ active }: { active: boolean }) {
  const { locale } = useI18n();
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [books, setBooks] = useState<AddressBook[]>([]);
  const [all, setAll] = useState<ContactSummary[]>([]);
  const [contacts, setContacts] = useState<ContactSummary[]>([]);
  const [scope, setScope] = useState<Scope>({ kind: "all" });
  const [query, setQuery] = useState("");
  const [pane, setPaneState] = useState<Pane>({ kind: "none" });
  // 窄屏下当前显示的那一栏: 打开或新建联系人就进详情, 关掉 (删除后) 回列表。
  const [mobileView, setMobileView] = useState<MobilePane>("list");
  const setPane = (p: Pane) => {
    setPaneState(p);
    setMobileView(p.kind === "none" ? "list" : "detail");
  };
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const lastChecked = useRef<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [bookDialog, setBookDialog] = useState<BookDialog>(null);
  const [deleteBook, setDeleteBook] = useState<AddressBook | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  // 详情的标签页跟着用户走: 在看往来邮件时换一位联系人, 仍停在往来邮件。
  const [detailTab, setDetailTab] = useState<DetailTab>("info");

  const loadBooks = useCallback(async () => {
    try {
      const [accs, list, everyone] = await Promise.all([api.listAccounts(), api.listAddressBooks(), api.listContacts("", "", "")]);
      // 只列服务器公布了通讯录的账号: 新建通讯录、导入的账号选择都跟着它。
      const capable = withFeature(accs ?? [], "contacts");
      const ids = new Set(capable.map((a) => a.id));
      setAccounts(capable);
      setBooks((list ?? []).filter((b) => ids.has(b.accountId)));
      setAll(everyone ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  const loadContacts = useCallback(async () => {
    setLoading(true);
    try {
      const list =
        scope.kind === "book"
          ? await api.listContacts(scope.accountId, scope.bookId, query)
          : await api.listContacts("", "", query);
      let out = list ?? [];
      // 「全部」里不列信任发件人通讯录: 那是自动维护的地址清单, 混进来会淹没真正的联系人。
      if (scope.kind !== "book") out = out.filter((c) => !isTrustedOnly(c, books));
      if (scope.kind === "keyword") out = out.filter((c) => (c.keywords ?? []).includes(scope.keyword));
      setContacts(out);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [scope, query, books]);

  useEffect(() => {
    if (active) loadBooks();
  }, [active, loadBooks]);

  useEffect(() => {
    if (!active) return;
    const t = setTimeout(loadContacts, query ? 150 : 0);
    return () => clearTimeout(t);
  }, [active, loadContacts, query]);

  useEffect(() => {
    setChecked(new Set());
  }, [scope]);

  useEffect(() => {
    const offChanged = onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.type === "" || c.type === "AddressBook" || c.type === "ContactCard") {
        loadBooks();
        loadContacts();
      }
    });
    // 共享被撤销时那个账号的通讯录连同缓存一起删掉, 不会有 pim:changed。
    const offAccounts = onEvent(Events.accountsChanged, () => {
      loadBooks();
      loadContacts();
    });
    return () => {
      offChanged();
      offAccounts();
    };
  }, [loadBooks, loadContacts]);

  const counts = useMemo(() => {
    const c: Record<string, number> = { all: 0 };
    for (const contact of all) {
      const trusted = isTrustedOnly(contact, books);
      if (!trusted) c.all += 1;
      for (const b of contact.addressBookIds ?? []) {
        const key = `${contact.accountId}/${b}`;
        c[key] = (c[key] ?? 0) + 1;
      }
      if (!trusted) for (const k of contact.keywords ?? []) c[`kw:${k}`] = (c[`kw:${k}`] ?? 0) + 1;
    }
    return c;
  }, [all, books]);

  const keywords = useMemo(
    () => [...new Set(all.flatMap((c) => c.keywords ?? []))].sort((a, b) => a.localeCompare(b, locale)),
    [all, locale],
  );

  const accountName = (id: string) => accounts.find((a) => a.id === id)?.name ?? id;
  const groupedBooks = useMemo(() => {
    const map = new Map<string, AddressBook[]>();
    for (const b of books) map.set(b.accountId, [...(map.get(b.accountId) ?? []), b]);
    return [...map.entries()];
  }, [books]);

  const sections = useMemo(() => groupByInitial(contacts), [contacts]);
  const writableBooks = books.filter((b) => (!b.myRights || b.myRights.mayWrite) && b.name !== TRUSTED);
  // 导入 .vcf 要服务器解析 (contacts:parse)。
  const canImport = (b: AddressBook) => supports(accounts.find((a) => a.id === b.accountId), "contactParse");
  const importableBooks = writableBooks.filter(canImport);

  /** 新建时的默认通讯录: 当前选中的通讯录, 否则个人账号的默认通讯录。 */
  function defaultBook(): AddressBook | undefined {
    if (scope.kind === "book") {
      const b = books.find((x) => x.accountId === scope.accountId && x.id === scope.bookId);
      if (b && b.name !== TRUSTED) return b;
    }
    const personal = accounts.find((a) => a.isPersonal)?.id;
    return (
      writableBooks.find((b) => b.accountId === personal && b.isDefault) ??
      writableBooks.find((b) => b.accountId === personal) ??
      writableBooks[0]
    );
  }

  function startNew(kind: "individual" | "group") {
    const b = defaultBook();
    if (!b) {
      toast.error(t("没有可写入的通讯录，请先新建通讯录"));
      return;
    }
    const form = emptyContact(kind, b.accountId, b.id);
    if (scope.kind === "keyword") form.keywords = [scope.keyword];
    setPane({ kind: "edit", form });
  }

  async function edit(accountId: string, id: string) {
    try {
      const form = await api.getContact(accountId, id);
      if (form) setPane({ kind: "edit", form });
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function importTo(b: AddressBook) {
    try {
      const n = await api.importContactsFile(b.accountId, b.id);
      if (n > 0) toast.success(t("已导入 {0} 位联系人到「{1}」", n, bookLabel(b)));
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  const checkedList = contacts.filter((c) => checked.has(key(c)));

  async function bulkDelete() {
    const byAccount = groupByAccount(checkedList);
    try {
      for (const [acc, ids] of byAccount) await api.deleteContacts(acc, ids);
      toast.success(t("已删除 {0} 位联系人", checkedList.length));
      setChecked(new Set());
      setPane({ kind: "none" });
      loadContacts();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function bulkMove(b: AddressBook) {
    const ids = checkedList.filter((c) => c.accountId === b.accountId).map((c) => c.id);
    if (ids.length < checkedList.length) toast.warning(t("只能在同一账号内移动，其它账号的联系人已跳过"));
    if (ids.length === 0) return;
    try {
      await api.moveContacts(b.accountId, ids, b.id);
      toast.success(t("已移动到「{0}」", bookLabel(b)));
      setChecked(new Set());
      loadContacts();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  const sidebar = (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1 p-2">
        <Button className="flex-1" size="sm" onClick={() => startNew("individual")}>
          <UserPlus className="size-4" />
          {t("新建联系人")}</Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" className="size-8" aria-label={t("更多新建选项")}>
              <Plus className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <DropdownMenuItem onSelect={() => startNew("individual")}>
              <UserPlus className="size-4" />
              {t("新联系人")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => startNew("group")}>
              <UsersRound className="size-4" />
              {t("新建群组")}</DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() => setBookDialog({ accountId: accounts.find((a) => a.isPersonal)?.id ?? accounts[0]?.id ?? "" })}
            >
              <BookPlus className="size-4" />
              {t("新建通讯录")}</DropdownMenuItem>
            {importableBooks.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuSub>
                  <DropdownMenuSubTrigger>
                    <Upload className="size-4" />
                    {t("导入联系人（.vcf）")}</DropdownMenuSubTrigger>
                  <DropdownMenuSubContent className="w-64">
                    {importableBooks.map((b) => (
                      <DropdownMenuItem key={`${b.accountId}/${b.id}`} onSelect={() => importTo(b)}>
                        <span className="min-w-0 flex-1 truncate">{bookLabel(b)}</span>
                        <span className="text-muted-foreground max-w-24 shrink-0 truncate text-xs">{accountName(b.accountId)}</span>
                      </DropdownMenuItem>
                    ))}
                  </DropdownMenuSubContent>
                </DropdownMenuSub>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        <SidebarItem
          icon={UsersRound}
          label={t("全部联系人")}
          count={counts.all}
          active={scope.kind === "all"}
          onClick={() => setScope({ kind: "all" })}
        />
        {groupedBooks.map(([accountId, list]) => (
          <section key={accountId} className="mt-3 flex flex-col gap-0.5">
            <div className="group/acc flex items-center">
              <p className="text-muted-foreground flex min-w-0 flex-1 items-center gap-1.5 px-2 py-1 text-xs">
                <UserRound className="size-3.5" />
                <span className="truncate">{accountName(accountId)}</span>
              </p>
              <button
                type="button"
                aria-label={t("新建通讯录")}
                title={t("新建通讯录")}
                onClick={() => setBookDialog({ accountId })}
                className="text-muted-foreground hover:bg-secondary hover:text-foreground rounded-sm p-1 opacity-0 group-hover/acc:opacity-100 focus-visible:opacity-100"
              >
                <Plus className="size-3.5" />
              </button>
            </div>
            {list.map((b) => {
              const item = (
                <SidebarItem
                  icon={b.name === TRUSTED ? ShieldCheck : BookUser}
                  label={bookLabel(b)}
                  count={counts[`${b.accountId}/${b.id}`] ?? 0}
                  active={scope.kind === "book" && scope.accountId === b.accountId && scope.bookId === b.id}
                  onClick={() => setScope({ kind: "book", accountId: b.accountId, bookId: b.id })}
                />
              );
              const mayEdit = (!b.myRights || b.myRights.mayWrite) && b.name !== TRUSTED;
              if (!mayEdit) return <div key={b.id}>{item}</div>;
              return (
                <ContextMenu key={b.id}>
                  <ContextMenuTrigger asChild>
                    <div>{item}</div>
                  </ContextMenuTrigger>
                  <ContextMenuContent className="w-48">
                    {canImport(b) && (
                      <ContextMenuItem onSelect={() => importTo(b)}>
                        <Upload />
                        {t("导入联系人")}</ContextMenuItem>
                    )}
                    <ContextMenuItem onSelect={() => setBookDialog({ accountId: b.accountId, book: b })}>
                      <Pencil />
                      {t("重命名")}</ContextMenuItem>
                    {!b.isDefault && (
                      <>
                        <ContextMenuSeparator />
                        <ContextMenuItem variant="destructive" onSelect={() => setDeleteBook(b)}>
                          <Trash2 />
                          {t("删除通讯录")}</ContextMenuItem>
                      </>
                    )}
                  </ContextMenuContent>
                </ContextMenu>
              );
            })}
          </section>
        ))}

        {keywords.length > 0 && (
          <section className="mt-3 flex flex-col gap-0.5">
            <p className="text-muted-foreground flex items-center gap-1.5 px-2 py-1 text-xs">
              <Tag className="size-3.5" />
              {t("类别")}</p>
            {keywords.map((k) => (
              <SidebarItem
                key={k}
                icon={Tag}
                label={k}
                count={counts[`kw:${k}`] ?? 0}
                active={scope.kind === "keyword" && scope.keyword === k}
                onClick={() => setScope({ kind: "keyword", keyword: k })}
              />
            ))}
          </section>
        )}
      </div>
    </div>
  );

  const list = (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-border shrink-0 border-b px-3 py-2">
        <div className="relative">
          <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("搜索姓名、邮箱、电话、公司")}
            className="h-8 pr-8 pl-8"
          />
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
      </div>

      {checked.size > 0 && (
        <div className="border-border bg-secondary flex shrink-0 items-center gap-1 border-b px-3 py-1.5 text-sm">
          <Checkbox
            checked={checked.size === contacts.length ? true : "indeterminate"}
            onCheckedChange={(v) => setChecked(v === true ? new Set(contacts.map(key)) : new Set())}
            aria-label={t("全选")}
          />
          <span className="ml-1 flex-1">{t("已选{0}位", checked.size)}</span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="h-7">
                <FolderInput className="size-4" />
                {t("移动")}</Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60">
              <DropdownMenuLabel>{t("移动到通讯录")}</DropdownMenuLabel>
              {writableBooks.map((b) => (
                <DropdownMenuItem key={`${b.accountId}/${b.id}`} onSelect={() => bulkMove(b)}>
                  <span className="min-w-0 flex-1 truncate">{bookLabel(b)}</span>
                  <span className="text-muted-foreground max-w-24 shrink-0 truncate text-xs">{accountName(b.accountId)}</span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button variant="ghost" size="sm" className="hover:text-destructive h-7" onClick={() => setConfirmBulkDelete(true)}>
            <Trash2 className="size-4" />
            {t("删除")}</Button>
          <Button variant="ghost" size="icon" className="size-7" aria-label={t("取消选择")} onClick={() => setChecked(new Set())}>
            <X className="size-4" />
          </Button>
        </div>
      )}

      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto">
        {loading && contacts.length === 0 ? (
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-10 text-sm">
            <Loader2 className="size-4 animate-spin" />
            {t("载入中")}</div>
        ) : contacts.length === 0 ? (
          <p className="text-muted-foreground py-10 text-center text-sm">{query ? t("没有匹配的联系人") : t("没有联系人")}</p>
        ) : (
          sections.map(([initial, items]) => (
            <section key={initial}>
              <h3 className="bg-background/95 text-muted-foreground sticky top-0 z-10 px-3 py-1 text-xs font-medium backdrop-blur">
                {initial}
              </h3>
              {items.map((c) => {
                const k = key(c);
                const isSelected =
                  (pane.kind === "view" && pane.accountId === c.accountId && pane.id === c.id) ||
                  (pane.kind === "edit" && pane.form.accountId === c.accountId && pane.form.id === c.id);
                const isChecked = checked.has(k);
                return (
                  <div
                    key={k}
                    role="button"
                    tabIndex={0}
                    onClick={(e) => {
                      if (e.ctrlKey || e.metaKey || e.shiftKey || checked.size > 0) {
                        toggleChecked(k, e.shiftKey);
                        return;
                      }
                      setPane({ kind: "view", accountId: c.accountId, id: c.id });
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") setPane({ kind: "view", accountId: c.accountId, id: c.id });
                    }}
                    className={cn(
                      "border-border flex w-full cursor-default items-center gap-2 border-b px-3 py-2 text-left",
                      "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                      (isSelected || isChecked) && "bg-muted",
                    )}
                  >
                    {checked.size > 0 && (
                      <span
                        className="-ml-1 shrink-0"
                        onClick={(e) => {
                          e.stopPropagation();
                          toggleChecked(k, e.shiftKey);
                        }}
                      >
                        <Checkbox checked={isChecked} aria-label={t("选择")} tabIndex={-1} />
                      </span>
                    )}
                    {/* 头像就是选择按钮 (与邮件列表相同): 鼠标移到头像上才变成对勾, 选中的保持对勾,
                        进入多选后左侧再出现一列勾选框。 */}
                    <button
                      type="button"
                      title={t("选择")}
                      aria-label={t("选择")}
                      aria-pressed={isChecked}
                      onClick={(e) => {
                        e.stopPropagation();
                        toggleChecked(k, e.shiftKey);
                      }}
                      className="group/avatar relative mr-1 block size-9 shrink-0 rounded-full"
                    >
                      <ContactAvatar c={c} className="size-9" />
                      <span
                        className={cn(
                          "bg-accent text-accent-foreground absolute inset-0 flex items-center justify-center rounded-full transition-opacity",
                          isChecked ? "opacity-100" : "opacity-0 group-hover/avatar:opacity-100",
                        )}
                      >
                        <Check className="size-5" strokeWidth={2.5} />
                      </span>
                    </button>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5">
                        <span className="truncate text-sm font-medium">{c.displayName || c.email || t("(未命名)")}</span>
                        {c.kind === "group" && <UsersRound className="text-muted-foreground size-3.5 shrink-0" />}
                        {c.kind === "org" && <Building2 className="text-muted-foreground size-3.5 shrink-0" />}
                      </span>
                      <span className="text-muted-foreground block truncate text-xs">{c.email || c.phone || c.organization}</span>
                    </span>
                  </div>
                );
              })}
            </section>
          ))
        )}
      </div>
    </div>
  );

  /** 勾选 / 取消一位联系人; range 为真 (Shift) 时把上次点的那位到这位之间按列表顺序全部勾上。 */
  function toggleChecked(k: string, range = false) {
    const order = sections.flatMap(([, items]) => items.map(key));
    const from = lastChecked.current ? order.indexOf(lastChecked.current) : -1;
    const to = order.indexOf(k);
    lastChecked.current = k;
    setChecked((prev) => {
      const next = new Set(prev);
      if (range && from >= 0 && to >= 0) {
        for (const x of order.slice(Math.min(from, to), Math.max(from, to) + 1)) next.add(x);
        return next;
      }
      if (next.has(k)) next.delete(k);
      else next.add(k);
      return next;
    });
  }

  let detail: React.ReactNode;
  if (pane.kind === "edit") {
    detail = (
      <ContactEditor
        key={`${pane.form.accountId}/${pane.form.id || "new"}/${pane.form.kind}`}
        initial={pane.form}
        books={books.filter((b) => b.name !== TRUSTED)}
        accounts={accounts}
        candidates={all}
        allKeywords={keywords}
        onCancel={() => setPane(pane.form.id ? { kind: "view", accountId: pane.form.accountId, id: pane.form.id } : { kind: "none" })}
        onSaved={(accountId, id) => {
          loadBooks();
          loadContacts();
          setPane({ kind: "view", accountId, id });
        }}
      />
    );
  } else if (pane.kind === "view") {
    detail = (
      <ContactDetailPane
        key={`${pane.accountId}/${pane.id}`}
        accountId={pane.accountId}
        id={pane.id}
        books={books}
        accounts={accounts}
        all={all}
        tab={detailTab}
        onTab={setDetailTab}
        onOpen={(accountId, id) => setPane({ kind: "view", accountId, id })}
        onEdit={() => edit(pane.accountId, pane.id)}
        onDeleted={() => {
          setPane({ kind: "none" });
          loadContacts();
          loadBooks();
        }}
      />
    );
  } else {
    detail = (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 text-sm">
        <UsersRound className="size-8" />
        {t("选择一位联系人")}</div>
    );
  }

  return (
    <>
      <PaneLayout
        id="contacts"
        sidebar={sidebar}
        list={list}
        detail={detail}
        listWidth={340}
        mobilePane={pane.kind === "none" && mobileView === "detail" ? "list" : mobileView}
        onMobilePaneChange={(p) => {
          // 编辑中按返回等于取消编辑, 回到列表时详情栏不留一个看不见的表单。
          if (p !== "detail" && pane.kind === "edit") {
            setPaneState(pane.form.id ? { kind: "view", accountId: pane.form.accountId, id: pane.form.id } : { kind: "none" });
          }
          setMobileView(p);
        }}
      />

      <BookNameDialog
        target={bookDialog}
        accounts={accounts}
        onClose={() => setBookDialog(null)}
        onSaved={(accountId, id) => {
          loadBooks();
          setScope({ kind: "book", accountId, bookId: id });
        }}
      />

      <AlertDialog open={deleteBook !== null} onOpenChange={(o) => !o && setDeleteBook(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("删除通讯录")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("确定删除「")}{deleteBook ? bookLabel(deleteBook) : ""}{t("」？其中的")} {deleteBook ? (counts[`${deleteBook.accountId}/${deleteBook.id}`] ?? 0) : 0}{" "}
              {t("位联系人会一并从服务器删除，无法恢复。")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("取消")}</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={async () => {
                const b = deleteBook;
                if (!b) return;
                try {
                  await api.deleteAddressBook(b.accountId, b.id);
                  toast.success(t("已删除通讯录"));
                  setScope({ kind: "all" });
                  loadBooks();
                } catch (err) {
                  toast.error(errorMessage(err));
                }
              }}
            >
              {t("删除")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={confirmBulkDelete} onOpenChange={setConfirmBulkDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("删除联系人")}</AlertDialogTitle>
            <AlertDialogDescription>{t("确定删除选中的{0}位联系人？删除会同步到服务器。", checked.size)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("取消")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={bulkDelete}>
              {t("删除")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function BookNameDialog({
  target,
  accounts,
  onClose,
  onSaved,
}: {
  target: BookDialog;
  accounts: Account[];
  onClose: () => void;
  onSaved: (accountId: string, id: string) => void;
}) {
  useI18n();
  const [name, setName] = useState("");
  const [accountId, setAccountId] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!target) return;
    setName(target.book?.name ?? "");
    setAccountId(target.accountId);
    setBusy(false);
  }, [target]);

  if (!target) return null;

  async function submit() {
    if (!name.trim() || busy) return;
    setBusy(true);
    try {
      const id = await api.saveAddressBook(accountId, target?.book?.id ?? "", name);
      onSaved(accountId, id);
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{target.book ? t("重命名通讯录") : t("新建通讯录")}</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={t("通讯录名称")} />
          {!target.book && accounts.length > 1 && (
            <OptionSelect
              value={accountId}
              onChange={setAccountId}
              options={accounts.map((a) => [a.id, a.name] as const)}
              className="w-full"
              aria-label={t("账号")}
            />
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t("取消")}</Button>
            <Button type="submit" disabled={!name.trim() || busy}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              {t("确定")}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ContactAvatar({ c, className }: { c: ContactSummary; className?: string }) {
  useI18n();
  if (c.hasPhoto) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={localUrl(`/czl-contact-photo?account=${encodeURIComponent(c.accountId)}&id=${encodeURIComponent(c.id)}`)}
        alt=""
        className={cn("rounded-full object-cover", className)}
      />
    );
  }
  return <SenderAvatar email={c.email} name={c.displayName} className={className} />;
}

function SidebarItem({
  icon: Icon,
  label,
  count,
  active,
  onClick,
}: {
  icon: typeof UsersRound;
  label: string;
  count?: number;
  active: boolean;
  onClick: () => void;
}) {
  useI18n();
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition-colors",
        "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
        active && "bg-muted font-medium",
      )}
    >
      <Icon className="text-muted-foreground size-4 shrink-0" />
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {count !== undefined && <span className="text-muted-foreground text-xs tabular-nums">{count}</span>}
    </button>
  );
}

const getContextLabel: () => (Record<string, string>) = () => ({ work: t("工作"), private: t("个人") });
const getPhoneLabel: () => (Record<string, string>) = () => ({ mobile: t("手机"), voice: t("电话"), fax: t("传真"), pager: t("寻呼"), text: t("短信") });
const getDateLabel: () => (Record<string, string>) = () => ({ birth: t("生日"), wedding: t("结婚纪念日"), death: t("逝世") });
const getInfoLabel: () => (Record<string, string>) = () => ({ expertise: t("专长"), hobby: t("爱好"), interest: t("兴趣") });

function ContactDetailPane({
  accountId,
  id,
  books,
  accounts,
  all,
  tab,
  onTab,
  onOpen,
  onEdit,
  onDeleted,
}: {
  accountId: string;
  id: string;
  books: AddressBook[];
  accounts: Account[];
  all: ContactSummary[];
  tab: DetailTab;
  onTab: (tab: DetailTab) => void;
  onOpen: (accountId: string, id: string) => void;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  useI18n();
  const [detail, setDetail] = useState<ContactForm | null>(null);
  const [events, setEvents] = useState<EventOccurrence[]>([]);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const load = useCallback(async () => {
    try {
      const d = await api.getContact(accountId, id);
      setDetail(d);
      const primary = d?.emails?.[0]?.value;
      setEvents(primary ? ((await api.upcomingEventsWith(primary)) ?? []) : []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, [accountId, id]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    return onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.accountId === accountId && (c.type === "" || c.type === "ContactCard")) load();
    });
  }, [accountId, load]);

  if (!detail) {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        {t("载入中")}</div>
    );
  }

  const primary = detail.emails?.[0]?.value ?? "";
  const book = books.find((x) => x.accountId === accountId && x.id === detail.addressBookId);
  const accountName = accounts.find((a) => a.id === accountId)?.name ?? accountId;
  const isGroup = detail.kind === "group";
  const members = isGroup ? all.filter((c) => c.accountId === accountId && (detail.memberIds ?? []).includes(c.id)) : [];
  const memberAddresses = members.filter((m) => m.email).map((m) => (m.displayName ? `${m.displayName} <${m.email}>` : m.email));
  const subtitle = [detail.jobTitle, detail.department, detail.kind !== "org" ? detail.organization : ""].filter(Boolean).join(" · ");

  async function remove() {
    try {
      await api.deleteContacts(accountId, [id]);
      toast.success(isGroup ? t("已删除群组") : t("已删除联系人"));
      onDeleted();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  function compose(to: string) {
    send("navigate", { module: "mail" });
    send("compose", { to });
  }

  const addresses = (detail.emails ?? []).map((e) => e.value).filter(Boolean);
  const showEmails = !isGroup && addresses.length > 0;
  const current = showEmails ? tab : "info";

  return (
    <Tabs value={current} onValueChange={(v) => onTab(v as DetailTab)} className="flex h-full min-h-0 flex-col gap-0">
      <div className="border-border @container shrink-0 border-b">
        <div className="mx-auto flex max-w-4xl flex-col gap-3 px-4 pt-4 @xl:gap-4 @xl:px-6 @xl:pt-6">
          <header className="flex flex-wrap items-center gap-x-4 gap-y-3">
            {detail.photoUrl ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={detail.photoUrl} alt="" className="size-12 rounded-full object-cover @xl:size-16" />
            ) : (
              <SenderAvatar email={primary} name={detail.displayName} className="size-12 text-base @xl:size-16 @xl:text-lg" />
            )}
            <div className="min-w-0 flex-1">
              <h1 className="flex items-center gap-2 truncate text-xl font-semibold">
                {detail.displayName || t("(未命名)")}
                {isGroup && <UsersRound className="text-muted-foreground size-4" />}
              </h1>
              {subtitle && <p className="text-muted-foreground truncate text-sm">{subtitle}</p>}
              {detail.nickname && <p className="text-muted-foreground truncate text-sm">{t("昵称：{0}", detail.nickname)}</p>}
              <p className="text-muted-foreground truncate text-xs">
                {accountName} · {book ? bookLabel(book) : ""}
              </p>
            </div>
            {/* 详情栏窄 (手机) 时按钮换到姓名下面一行, 不跟姓名抢宽度。 */}
            <div className="flex w-full gap-2 @xl:w-auto">
              {(primary || memberAddresses.length > 0) && (
                <Button variant="outline" size="sm" onClick={() => compose(isGroup ? memberAddresses.join(", ") : detail.displayName ? `${detail.displayName} <${primary}>` : primary)}>
                  <Mail className="size-4" />
                  {isGroup ? t("群发邮件") : t("写邮件")}
                </Button>
              )}
              {!detail.readOnly && (
                <Button variant="outline" size="sm" onClick={onEdit}>
                  <Pencil className="size-4" />
                  {t("编辑")}</Button>
              )}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="icon" className="size-8" aria-label={t("更多")}>
                    <MoreHorizontal className="size-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-44">
                  <DropdownMenuItem
                    onSelect={() => navigator.clipboard?.writeText(primary).then(() => toast.success(t("已复制邮箱")))}
                    disabled={!primary}
                  >
                    <Mail className="size-4" />
                    {t("复制邮箱")}</DropdownMenuItem>
                  {!detail.readOnly && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem variant="destructive" onSelect={() => setConfirmDelete(true)}>
                        <Trash2 className="size-4" />
                        {isGroup ? t("删除群组") : t("删除联系人")}
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          </header>

          {(detail.keywords ?? []).length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {detail.keywords.map((k) => (
                <span key={k} className="bg-muted flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs">
                  <Tag className="size-3" />
                  {k}
                </span>
              ))}
            </div>
          )}

          {showEmails ? (
            <TabsList variant="line" className="-mb-px h-9">
              <TabsTrigger value="info">{t("资料")}</TabsTrigger>
              <TabsTrigger value="emails">{t("往来邮件")}</TabsTrigger>
            </TabsList>
          ) : (
            <div className="h-2" />
          )}
        </div>
      </div>

      <TabsContent value="info" className="czl-scroll @container min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-4xl flex-col gap-6 p-4 @xl:p-6">
          {isGroup ? (
            <section className="flex flex-col gap-1">
              <h2 className="text-muted-foreground mb-1 text-xs font-medium">{t("成员（{0}）", members.length)}</h2>
              {members.length === 0 && <p className="text-muted-foreground text-sm">{t("群组里还没有成员")}</p>}
              {members.map((m) => (
                <button
                  key={m.id}
                  type="button"
                  onClick={() => onOpen(m.accountId, m.id)}
                  className="hover:bg-secondary -mx-2 flex items-center gap-3 rounded-md px-2 py-1.5 text-left"
                >
                  <ContactAvatar c={m} className="size-8" />
                  <span className="min-w-0 flex-1 truncate text-sm">{m.displayName || m.email}</span>
                  <span className="text-muted-foreground truncate text-xs">{m.email}</span>
                </button>
              ))}
            </section>
          ) : (
            <section className="grid gap-4 sm:grid-cols-2">
              {detail.emails?.map((e, i) => (
                <InfoRow key={`e${i}`} icon={Mail} label={e.label || getContextLabel()[e.context] || t("邮箱")}>
                  <button type="button" className="text-accent truncate text-left hover:underline" onClick={() => compose(e.value)}>
                    {e.value}
                  </button>
                </InfoRow>
              ))}
              {detail.phones?.map((p, i) => (
                <InfoRow key={`p${i}`} icon={Phone} label={p.label || getPhoneLabel()[p.feature] || t("电话")}>
                  <span className="select-text">{p.value}</span>
                </InfoRow>
              ))}
              {detail.organization && detail.kind !== "org" && (
                <InfoRow icon={Building2} label={t("公司")}>
                  {detail.organization}
                </InfoRow>
              )}
              {detail.anniversaries?.map((d, i) => (
                <InfoRow key={`d${i}`} icon={d.kind === "birth" ? Cake : CalendarDays} label={getDateLabel()[d.kind] || d.kind || t("纪念日")}>
                  {d.date.replace(/^--/, "")}
                </InfoRow>
              ))}
              {detail.addresses?.map((a, i) => (
                <InfoRow key={`a${i}`} icon={MapPin} label={getContextLabel()[a.context] ? t("{0}地址", getContextLabel()[a.context]) : t("地址")}>
                  <span className="whitespace-pre-wrap select-text">
                    {a.full || [a.street, [a.locality, a.region, a.postcode].filter(Boolean).join(" "), a.country].filter(Boolean).join("\n")}
                  </span>
                </InfoRow>
              ))}
              {detail.onlineServices?.map((s, i) => (
                <InfoRow key={`s${i}`} icon={Globe} label={s.service || s.label || (s.link ? t("网站") : t("在线服务"))}>
                  <span className="select-text">{s.uri}</span>
                </InfoRow>
              ))}
              {detail.personalInfo?.map((p, i) => (
                <InfoRow key={`i${i}`} icon={UserRound} label={getInfoLabel()[p.kind] || t("个人信息")}>
                  {p.value}
                </InfoRow>
              ))}
              {detail.notes && (
                <InfoRow icon={StickyNote} label={t("备注")} wide>
                  <span className="whitespace-pre-wrap select-text">{detail.notes}</span>
                </InfoRow>
              )}
            </section>
          )}

          {!isGroup && (
            <section className="border-border flex flex-col gap-2 border-t pt-5">
              <h2 className="text-muted-foreground text-xs font-medium">{t("近期日程")}</h2>
              {events.length === 0 ? (
                <p className="text-muted-foreground text-sm">{t("没有即将到来的日程")}</p>
              ) : (
                events.map((o) => (
                  <button
                    key={`${o.accountId}/${o.eventId}/${o.recurrenceId}`}
                    type="button"
                    onClick={() => send("navigate", { module: "calendar" })}
                    className="hover:bg-secondary -mx-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-left"
                  >
                    <CalendarDays className="text-muted-foreground size-4 shrink-0" />
                    <span className="min-w-0 flex-1 truncate text-sm">{o.title || t("(无标题)")}</span>
                    <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                      {shortDate(new Date(o.start))} {o.allDay ? t("全天") : hm(new Date(o.start))}
                    </span>
                  </button>
                ))
              )}
            </section>
          )}
        </div>
      </TabsContent>

      {showEmails && (
        <TabsContent value="emails" className="min-h-0 flex-1">
          <Correspondence key={addresses.join(",")} addresses={addresses} />
        </TabsContent>
      )}

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{isGroup ? t("删除群组") : t("删除联系人")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("确定删除「")}{detail.displayName}{t("」？删除会同步到服务器，其它设备上也会消失。")}{isGroup ? t("群组成员本身不会被删除。") : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("取消")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              {t("删除")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Tabs>
  );
}

function InfoRow({
  icon: Icon,
  label,
  wide,
  children,
}: {
  icon: typeof Mail;
  label: string;
  wide?: boolean;
  children: React.ReactNode;
}) {
  useI18n();
  return (
    <div className={cn("flex min-w-0 items-start gap-3", wide && "sm:col-span-2")}>
      <Icon className="text-muted-foreground mt-0.5 size-4 shrink-0" />
      <div className="min-w-0 flex-1 text-sm">
        <p className="text-muted-foreground text-xs">{label}</p>
        <div className="min-w-0 truncate">{children}</div>
      </div>
    </div>
  );
}

const key = (c: ContactSummary) => `${c.accountId}/${c.id}`;

function groupByAccount(list: ContactSummary[]): [string, string[]][] {
  const map = new Map<string, string[]>();
  for (const c of list) map.set(c.accountId, [...(map.get(c.accountId) ?? []), c.id]);
  return [...map.entries()];
}

/** 只属于「Trusted Senders」通讯录的条目。 */
function isTrustedOnly(c: ContactSummary, books: AddressBook[]): boolean {
  const ids = c.addressBookIds ?? [];
  return ids.length > 0 && ids.every((id) => books.find((b) => b.accountId === c.accountId && b.id === id)?.name === TRUSTED);
}

/** 按首字分组：拉丁字母按大写字母，中文按首字，其余归到 #。 */
function groupByInitial(list: ContactSummary[]): [string, ContactSummary[]][] {
  const map = new Map<string, ContactSummary[]>();
  for (const c of list) {
    const name = (c.displayName || c.email || "").trim();
    const first = name.charAt(0);
    let k = "#";
    if (/[a-z]/i.test(first)) k = first.toUpperCase();
    else if (/[一-鿿]/.test(first)) k = first;
    map.set(k, [...(map.get(k) ?? []), c]);
  }
  return [...map.entries()].sort(([a], [b]) => {
    if (a === "#") return 1;
    if (b === "#") return -1;
    const la = /[A-Z]/.test(a);
    const lb = /[A-Z]/.test(b);
    if (la !== lb) return la ? 1 : -1;
    return a.localeCompare(b, dateLocale());
  });
}
