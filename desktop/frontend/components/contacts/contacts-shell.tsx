"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  BookPlus,
  BookUser,
  Building2,
  CalendarDays,
  Cake,
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
import { PaneLayout } from "@/components/pane-layout";
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
  type EmailWithAccount,
  type EventOccurrence,
  type PIMChange,
} from "@/lib/api";
import { send } from "@/lib/app-bus";
import { displayAddressList, listDate } from "@/lib/format";
import { hm, shortDate } from "@/lib/date";

/** 当前范围: 全部 / 某通讯录 / 某类别。 */
type Scope =
  | { kind: "all" }
  | { kind: "book"; accountId: string; bookId: string }
  | { kind: "keyword"; keyword: string };

type Pane = { kind: "none" } | { kind: "view"; accountId: string; id: string } | { kind: "edit"; form: ContactForm };

type BookDialog = { accountId: string; book?: AddressBook } | null;

const TRUSTED = "Trusted Senders";

export function ContactsShell({ active }: { active: boolean }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [books, setBooks] = useState<AddressBook[]>([]);
  const [all, setAll] = useState<ContactSummary[]>([]);
  const [contacts, setContacts] = useState<ContactSummary[]>([]);
  const [scope, setScope] = useState<Scope>({ kind: "all" });
  const [query, setQuery] = useState("");
  const [pane, setPane] = useState<Pane>({ kind: "none" });
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [bookDialog, setBookDialog] = useState<BookDialog>(null);
  const [deleteBook, setDeleteBook] = useState<AddressBook | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);

  const loadBooks = useCallback(async () => {
    try {
      const [accs, list, everyone] = await Promise.all([api.listAccounts(), api.listAddressBooks(), api.listContacts("", "", "")]);
      setAccounts(accs ?? []);
      setBooks(list ?? []);
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
    return onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.type === "" || c.type === "AddressBook" || c.type === "ContactCard") {
        loadBooks();
        loadContacts();
      }
    });
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
    () => [...new Set(all.flatMap((c) => c.keywords ?? []))].sort((a, b) => a.localeCompare(b, "zh-CN")),
    [all],
  );

  const accountName = (id: string) => accounts.find((a) => a.id === id)?.name ?? id;
  const groupedBooks = useMemo(() => {
    const map = new Map<string, AddressBook[]>();
    for (const b of books) map.set(b.accountId, [...(map.get(b.accountId) ?? []), b]);
    return [...map.entries()];
  }, [books]);

  const sections = useMemo(() => groupByInitial(contacts), [contacts]);
  const writableBooks = books.filter((b) => (!b.myRights || b.myRights.mayWrite) && b.name !== TRUSTED);

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
      toast.error("没有可写入的通讯录，请先新建通讯录");
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
      if (n > 0) toast.success(`已导入 ${n} 位联系人到「${bookLabel(b)}」`);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  const checkedList = contacts.filter((c) => checked.has(key(c)));

  async function bulkDelete() {
    const byAccount = groupByAccount(checkedList);
    try {
      for (const [acc, ids] of byAccount) await api.deleteContacts(acc, ids);
      toast.success(`已删除 ${checkedList.length} 位联系人`);
      setChecked(new Set());
      setPane({ kind: "none" });
      loadContacts();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function bulkMove(b: AddressBook) {
    const ids = checkedList.filter((c) => c.accountId === b.accountId).map((c) => c.id);
    if (ids.length < checkedList.length) toast.warning("只能在同一账号内移动，其它账号的联系人已跳过");
    if (ids.length === 0) return;
    try {
      await api.moveContacts(b.accountId, ids, b.id);
      toast.success(`已移动到「${bookLabel(b)}」`);
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
          新建联系人
        </Button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="icon" className="size-8" aria-label="更多新建选项">
              <Plus className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-56">
            <DropdownMenuItem onSelect={() => startNew("individual")}>
              <UserPlus className="size-4" />
              新联系人
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => startNew("group")}>
              <UsersRound className="size-4" />
              新建群组
            </DropdownMenuItem>
            <DropdownMenuItem
              onSelect={() => setBookDialog({ accountId: accounts.find((a) => a.isPersonal)?.id ?? accounts[0]?.id ?? "" })}
            >
              <BookPlus className="size-4" />
              新建通讯录
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                <Upload className="size-4" />
                导入联系人（.vcf）
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="w-64">
                {writableBooks.map((b) => (
                  <DropdownMenuItem key={`${b.accountId}/${b.id}`} onSelect={() => importTo(b)}>
                    <span className="min-w-0 flex-1 truncate">{bookLabel(b)}</span>
                    <span className="text-muted-foreground max-w-24 shrink-0 truncate text-xs">{accountName(b.accountId)}</span>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        <SidebarItem
          icon={UsersRound}
          label="全部联系人"
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
                aria-label="新建通讯录"
                title="新建通讯录"
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
                    <ContextMenuItem onSelect={() => importTo(b)}>
                      <Upload />
                      导入联系人
                    </ContextMenuItem>
                    <ContextMenuItem onSelect={() => setBookDialog({ accountId: b.accountId, book: b })}>
                      <Pencil />
                      重命名
                    </ContextMenuItem>
                    {!b.isDefault && (
                      <>
                        <ContextMenuSeparator />
                        <ContextMenuItem variant="destructive" onSelect={() => setDeleteBook(b)}>
                          <Trash2 />
                          删除通讯录
                        </ContextMenuItem>
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
              类别
            </p>
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
            placeholder="搜索姓名、邮箱、电话、公司"
            className="h-8 pr-8 pl-8"
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
      </div>

      {checked.size > 0 && (
        <div className="border-border bg-secondary flex shrink-0 items-center gap-1 border-b px-3 py-1.5 text-sm">
          <Checkbox
            checked={checked.size === contacts.length ? true : "indeterminate"}
            onCheckedChange={(v) => setChecked(v === true ? new Set(contacts.map(key)) : new Set())}
            aria-label="全选"
          />
          <span className="ml-1 flex-1">已选 {checked.size} 位</span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="sm" className="h-7">
                <FolderInput className="size-4" />
                移动
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-60">
              <DropdownMenuLabel>移动到通讯录</DropdownMenuLabel>
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
            删除
          </Button>
          <Button variant="ghost" size="icon" className="size-7" aria-label="取消选择" onClick={() => setChecked(new Set())}>
            <X className="size-4" />
          </Button>
        </div>
      )}

      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto">
        {loading && contacts.length === 0 ? (
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-10 text-sm">
            <Loader2 className="size-4 animate-spin" />
            载入中
          </div>
        ) : contacts.length === 0 ? (
          <p className="text-muted-foreground py-10 text-center text-sm">{query ? "没有匹配的联系人" : "没有联系人"}</p>
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
                      if (e.ctrlKey || e.metaKey || checked.size > 0) {
                        toggleChecked(k);
                        return;
                      }
                      setPane({ kind: "view", accountId: c.accountId, id: c.id });
                    }}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") setPane({ kind: "view", accountId: c.accountId, id: c.id });
                    }}
                    className={cn(
                      "group border-border flex w-full cursor-default items-center gap-3 border-b px-3 py-2 text-left",
                      "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                      (isSelected || isChecked) && "bg-muted",
                    )}
                  >
                    <div className="relative size-9 shrink-0">
                      <ContactAvatar c={c} className={cn("size-9", (isChecked || checked.size > 0) && "invisible", "group-hover:invisible")} />
                      <span
                        className={cn(
                          "absolute inset-0 flex items-center justify-center",
                          isChecked || checked.size > 0 ? "visible" : "invisible group-hover:visible",
                        )}
                        onClick={(e) => {
                          e.stopPropagation();
                          toggleChecked(k);
                        }}
                      >
                        <Checkbox checked={isChecked} aria-label="选择" />
                      </span>
                    </div>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5">
                        <span className="truncate text-sm font-medium">{c.displayName || c.email || "(未命名)"}</span>
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

  function toggleChecked(k: string) {
    setChecked((prev) => {
      const next = new Set(prev);
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
        选择一位联系人
      </div>
    );
  }

  return (
    <>
      <PaneLayout id="contacts" sidebar={sidebar} list={list} detail={detail} listWidth={340} />

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
            <AlertDialogTitle>删除通讯录</AlertDialogTitle>
            <AlertDialogDescription>
              确定删除「{deleteBook ? bookLabel(deleteBook) : ""}」？其中的 {deleteBook ? (counts[`${deleteBook.accountId}/${deleteBook.id}`] ?? 0) : 0}{" "}
              位联系人会一并从服务器删除，无法恢复。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={async () => {
                const b = deleteBook;
                if (!b) return;
                try {
                  await api.deleteAddressBook(b.accountId, b.id);
                  toast.success("已删除通讯录");
                  setScope({ kind: "all" });
                  loadBooks();
                } catch (err) {
                  toast.error(errorMessage(err));
                }
              }}
            >
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={confirmBulkDelete} onOpenChange={setConfirmBulkDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除联系人</AlertDialogTitle>
            <AlertDialogDescription>确定删除选中的 {checked.size} 位联系人？删除会同步到服务器。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={bulkDelete}>
              删除
            </AlertDialogAction>
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
          <DialogTitle>{target.book ? "重命名通讯录" : "新建通讯录"}</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="通讯录名称" />
          {!target.book && accounts.length > 1 && (
            <select
              value={accountId}
              onChange={(e) => setAccountId(e.target.value)}
              className="border-input h-9 rounded-sm border bg-transparent px-2 text-sm"
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id} className="bg-popover">
                  {a.name}
                </option>
              ))}
            </select>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button type="submit" disabled={!name.trim() || busy}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              确定
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function ContactAvatar({ c, className }: { c: ContactSummary; className?: string }) {
  if (c.hasPhoto) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={`/czl-contact-photo?account=${encodeURIComponent(c.accountId)}&id=${encodeURIComponent(c.id)}`}
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

const CONTEXT_LABEL: Record<string, string> = { work: "工作", private: "个人" };
const PHONE_LABEL: Record<string, string> = { mobile: "手机", voice: "电话", fax: "传真", pager: "寻呼", text: "短信" };
const DATE_LABEL: Record<string, string> = { birth: "生日", wedding: "结婚纪念日", death: "逝世" };
const INFO_LABEL: Record<string, string> = { expertise: "专长", hobby: "爱好", interest: "兴趣" };

function ContactDetailPane({
  accountId,
  id,
  books,
  accounts,
  all,
  onOpen,
  onEdit,
  onDeleted,
}: {
  accountId: string;
  id: string;
  books: AddressBook[];
  accounts: Account[];
  all: ContactSummary[];
  onOpen: (accountId: string, id: string) => void;
  onEdit: () => void;
  onDeleted: () => void;
}) {
  const [detail, setDetail] = useState<ContactForm | null>(null);
  const [emails, setEmails] = useState<EmailWithAccount[]>([]);
  const [events, setEvents] = useState<EventOccurrence[]>([]);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const load = useCallback(async () => {
    try {
      const d = await api.getContact(accountId, id);
      setDetail(d);
      const primary = d?.emails?.[0]?.value;
      if (primary) {
        const [recent, upcoming] = await Promise.all([api.recentEmailsWith(primary), api.upcomingEventsWith(primary)]);
        setEmails(recent ?? []);
        setEvents(upcoming ?? []);
      } else {
        setEmails([]);
        setEvents([]);
      }
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
        载入中
      </div>
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
      toast.success(isGroup ? "已删除群组" : "已删除联系人");
      onDeleted();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  function compose(to: string) {
    send("navigate", { module: "mail" });
    send("compose", { to });
  }

  return (
    <div className="czl-scroll h-full overflow-y-auto">
      <div className="mx-auto flex max-w-4xl flex-col gap-6 p-6">
        <header className="flex flex-wrap items-center gap-4">
          {detail.photoUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={detail.photoUrl} alt="" className="size-16 rounded-full object-cover" />
          ) : (
            <SenderAvatar email={primary} name={detail.displayName} className="size-16 text-lg" />
          )}
          <div className="min-w-0 flex-1">
            <h1 className="flex items-center gap-2 truncate text-xl font-semibold">
              {detail.displayName || "(未命名)"}
              {isGroup && <UsersRound className="text-muted-foreground size-4" />}
            </h1>
            {subtitle && <p className="text-muted-foreground truncate text-sm">{subtitle}</p>}
            {detail.nickname && <p className="text-muted-foreground truncate text-sm">昵称：{detail.nickname}</p>}
            <p className="text-muted-foreground truncate text-xs">
              {accountName} · {book ? bookLabel(book) : ""}
            </p>
          </div>
          <div className="flex gap-2">
            {(primary || memberAddresses.length > 0) && (
              <Button variant="outline" size="sm" onClick={() => compose(isGroup ? memberAddresses.join(", ") : detail.displayName ? `${detail.displayName} <${primary}>` : primary)}>
                <Mail className="size-4" />
                {isGroup ? "群发邮件" : "写邮件"}
              </Button>
            )}
            {!detail.readOnly && (
              <Button variant="outline" size="sm" onClick={onEdit}>
                <Pencil className="size-4" />
                编辑
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon" className="size-8" aria-label="更多">
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-44">
                <DropdownMenuItem
                  onSelect={() => navigator.clipboard?.writeText(primary).then(() => toast.success("已复制邮箱"))}
                  disabled={!primary}
                >
                  <Mail className="size-4" />
                  复制邮箱
                </DropdownMenuItem>
                {!detail.readOnly && (
                  <>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onSelect={() => setConfirmDelete(true)}>
                      <Trash2 className="size-4" />
                      {isGroup ? "删除群组" : "删除联系人"}
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

        {isGroup ? (
          <section className="border-border flex flex-col gap-1 border-t pt-5">
            <h2 className="text-muted-foreground mb-1 text-xs font-medium">成员（{members.length}）</h2>
            {members.length === 0 && <p className="text-muted-foreground text-sm">群组里还没有成员</p>}
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
          <section className="border-border grid gap-4 border-t pt-5 sm:grid-cols-2">
            {detail.emails?.map((e, i) => (
              <InfoRow key={`e${i}`} icon={Mail} label={e.label || CONTEXT_LABEL[e.context] || "邮箱"}>
                <button type="button" className="text-accent truncate text-left hover:underline" onClick={() => compose(e.value)}>
                  {e.value}
                </button>
              </InfoRow>
            ))}
            {detail.phones?.map((p, i) => (
              <InfoRow key={`p${i}`} icon={Phone} label={p.label || PHONE_LABEL[p.feature] || "电话"}>
                <span className="select-text">{p.value}</span>
              </InfoRow>
            ))}
            {detail.organization && detail.kind !== "org" && (
              <InfoRow icon={Building2} label="公司">
                {detail.organization}
              </InfoRow>
            )}
            {detail.anniversaries?.map((d, i) => (
              <InfoRow key={`d${i}`} icon={d.kind === "birth" ? Cake : CalendarDays} label={DATE_LABEL[d.kind] || d.kind || "纪念日"}>
                {d.date.replace(/^--/, "")}
              </InfoRow>
            ))}
            {detail.addresses?.map((a, i) => (
              <InfoRow key={`a${i}`} icon={MapPin} label={CONTEXT_LABEL[a.context] ? `${CONTEXT_LABEL[a.context]}地址` : "地址"}>
                <span className="whitespace-pre-wrap select-text">
                  {a.full || [a.street, [a.locality, a.region, a.postcode].filter(Boolean).join(" "), a.country].filter(Boolean).join("\n")}
                </span>
              </InfoRow>
            ))}
            {detail.onlineServices?.map((s, i) => (
              <InfoRow key={`s${i}`} icon={Globe} label={s.service || s.label || "在线服务"}>
                <span className="select-text">{s.uri}</span>
              </InfoRow>
            ))}
            {detail.personalInfo?.map((p, i) => (
              <InfoRow key={`i${i}`} icon={UserRound} label={INFO_LABEL[p.kind] || "个人信息"}>
                {p.value}
              </InfoRow>
            ))}
            {detail.notes && (
              <InfoRow icon={StickyNote} label="备注" wide>
                <span className="whitespace-pre-wrap select-text">{detail.notes}</span>
              </InfoRow>
            )}
          </section>
        )}

        {!isGroup && (
          <div className="border-border grid gap-6 border-t pt-5 lg:grid-cols-2">
            <section className="flex flex-col gap-2">
              <h2 className="text-muted-foreground text-xs font-medium">最近往来邮件</h2>
              {emails.length === 0 ? (
                <p className="text-muted-foreground text-sm">没有往来邮件</p>
              ) : (
                emails.map((e) => (
                  <button
                    key={`${e.accountId}/${e.id}`}
                    type="button"
                    onClick={() => {
                      send("navigate", { module: "mail" });
                      send("openEmail", { accountId: e.accountId, emailId: e.id });
                    }}
                    className="hover:bg-secondary -mx-2 flex flex-col rounded-md px-2 py-1.5 text-left"
                  >
                    <span className="flex items-baseline gap-2">
                      <span className="min-w-0 flex-1 truncate text-sm font-medium">{e.subject || "(无主题)"}</span>
                      <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{listDate(e.receivedAt)}</span>
                    </span>
                    <span className="text-muted-foreground truncate text-xs">
                      {displayAddressList(e.from)} · {e.preview}
                    </span>
                  </button>
                ))
              )}
            </section>

            <section className="flex flex-col gap-2">
              <h2 className="text-muted-foreground text-xs font-medium">近期日程</h2>
              {events.length === 0 ? (
                <p className="text-muted-foreground text-sm">没有即将到来的日程</p>
              ) : (
                events.map((o) => (
                  <button
                    key={`${o.accountId}/${o.eventId}/${o.recurrenceId}`}
                    type="button"
                    onClick={() => send("navigate", { module: "calendar" })}
                    className="hover:bg-secondary -mx-2 flex items-center gap-2 rounded-md px-2 py-1.5 text-left"
                  >
                    <CalendarDays className="text-muted-foreground size-4 shrink-0" />
                    <span className="min-w-0 flex-1 truncate text-sm">{o.title || "(无标题)"}</span>
                    <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                      {shortDate(new Date(o.start))} {o.allDay ? "全天" : hm(new Date(o.start))}
                    </span>
                  </button>
                ))
              )}
            </section>
          </div>
        )}
      </div>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{isGroup ? "删除群组" : "删除联系人"}</AlertDialogTitle>
            <AlertDialogDescription>
              确定删除「{detail.displayName}」？删除会同步到服务器，其它设备上也会消失。{isGroup ? "群组成员本身不会被删除。" : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={remove}>
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
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
    return a.localeCompare(b, "zh-CN");
  });
}
