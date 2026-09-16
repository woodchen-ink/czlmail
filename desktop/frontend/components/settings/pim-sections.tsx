"use client";

import { useCallback, useEffect, useState } from "react";
import { BookUser, CalendarDays, Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { calendarColor } from "@/components/calendar/calendar-utils";
import { bookLabel } from "@/components/contacts/contact-editor";
import { AccountPicker, Card, SectionTitle, useAccounts } from "@/components/settings/ui";
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
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { api, errorMessage, type AddressBook, type Calendar } from "@/lib/api";

type Edit = { kind: "calendar" | "book"; id: string; name: string; color: string };

/** 日历与通讯录的管理(新建、重命名、改色、删除)。 */
export function CalendarsSection({ active }: { active: boolean }) {
  const { accounts, accountId, setAccountId } = useAccounts(active);
  const [calendars, setCalendars] = useState<Calendar[] | null>(null);
  const [edit, setEdit] = useState<Edit | null>(null);
  const [del, setDel] = useState<Calendar | null>(null);

  const load = useCallback(async () => {
    try {
      setCalendars((await api.listCalendars()) ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  const list = (calendars ?? []).filter((c) => c.accountId === accountId);

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle title="日历" desc="日历保存在服务器上，与 Bulwark、手机上的 CalDAV 客户端同步。在日历侧栏里右键日历也能管理。" />
        <div className="flex shrink-0 items-center gap-2">
          <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
          <Button size="sm" onClick={() => setEdit({ kind: "calendar", id: "", name: "", color: "#5B7FA6" })} disabled={!accountId}>
            <Plus className="size-4" />
            新建日历
          </Button>
        </div>
      </div>
      <Card className="gap-0 p-0">
        {calendars === null ? (
          <Loading />
        ) : list.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">没有日历</p>
        ) : (
          list.map((c, i) => {
            const mayAdmin = !c.myRights || c.myRights.mayAdmin !== false;
            return (
              <div key={c.id} className={cn("flex items-center gap-3 px-4 py-2.5", i > 0 && "border-border border-t")}>
                <span className="size-3 shrink-0 rounded-full" style={{ background: calendarColor(calendars, c.accountId, c.id) }} />
                <CalendarDays className="text-muted-foreground size-4 shrink-0" />
                <span className="min-w-0 flex-1 truncate text-sm">{c.name}</span>
                {c.isDefault && <span className="bg-muted rounded-sm px-1.5 py-0.5 text-[11px]">默认</span>}
                {mayAdmin && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    aria-label="编辑"
                    onClick={() => setEdit({ kind: "calendar", id: c.id, name: c.name, color: c.color || "#5B7FA6" })}
                  >
                    <Pencil className="size-4" />
                  </Button>
                )}
                {mayAdmin && !c.isDefault && (
                  <Button variant="ghost" size="icon" className="hover:text-destructive size-7" aria-label="删除" onClick={() => setDel(c)}>
                    <Trash2 className="size-4" />
                  </Button>
                )}
              </div>
            );
          })
        )}
      </Card>

      <NameDialog
        edit={edit}
        onClose={() => setEdit(null)}
        onSubmit={async (e) => {
          await api.saveCalendar(accountId, e.id, e.name, e.color);
          load();
        }}
      />
      <Confirm
        open={del !== null}
        title="删除日历"
        desc={`确定删除「${del?.name}」？其中的全部日程与任务会从服务器删除，无法恢复。`}
        onClose={() => setDel(null)}
        onConfirm={async () => {
          if (!del) return;
          await api.deleteCalendar(del.accountId, del.id);
          toast.success("已删除日历");
          load();
        }}
      />
    </>
  );
}

export function AddressBooksSection({ active }: { active: boolean }) {
  const { accounts, accountId, setAccountId } = useAccounts(active);
  const [books, setBooks] = useState<AddressBook[] | null>(null);
  const [edit, setEdit] = useState<Edit | null>(null);
  const [del, setDel] = useState<AddressBook | null>(null);

  const load = useCallback(async () => {
    try {
      setBooks((await api.listAddressBooks()) ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  const list = (books ?? []).filter((b) => b.accountId === accountId);

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle title="通讯录" desc="「信任的发件人」由程序自动维护，用于放行远程图片，不能在这里删除。" />
        <div className="flex shrink-0 items-center gap-2">
          <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
          <Button size="sm" onClick={() => setEdit({ kind: "book", id: "", name: "", color: "" })} disabled={!accountId}>
            <Plus className="size-4" />
            新建通讯录
          </Button>
        </div>
      </div>
      <Card className="gap-0 p-0">
        {books === null ? (
          <Loading />
        ) : list.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">没有通讯录</p>
        ) : (
          list.map((b, i) => {
            const editable = (!b.myRights || b.myRights.mayWrite !== false) && b.name !== "Trusted Senders";
            return (
              <div key={b.id} className={cn("flex items-center gap-3 px-4 py-2.5", i > 0 && "border-border border-t")}>
                <BookUser className="text-muted-foreground size-4 shrink-0" />
                <span className="min-w-0 flex-1 truncate text-sm">{bookLabel(b)}</span>
                {b.isDefault && <span className="bg-muted rounded-sm px-1.5 py-0.5 text-[11px]">默认</span>}
                {editable && (
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    aria-label="重命名"
                    onClick={() => setEdit({ kind: "book", id: b.id, name: b.name, color: "" })}
                  >
                    <Pencil className="size-4" />
                  </Button>
                )}
                {editable && !b.isDefault && (
                  <Button variant="ghost" size="icon" className="hover:text-destructive size-7" aria-label="删除" onClick={() => setDel(b)}>
                    <Trash2 className="size-4" />
                  </Button>
                )}
              </div>
            );
          })
        )}
      </Card>

      <NameDialog
        edit={edit}
        onClose={() => setEdit(null)}
        onSubmit={async (e) => {
          await api.saveAddressBook(accountId, e.id, e.name);
          load();
        }}
      />
      <Confirm
        open={del !== null}
        title="删除通讯录"
        desc={`确定删除「${del ? bookLabel(del) : ""}」？其中的联系人会从服务器删除，无法恢复。`}
        onClose={() => setDel(null)}
        onConfirm={async () => {
          if (!del) return;
          await api.deleteAddressBook(del.accountId, del.id);
          toast.success("已删除通讯录");
          load();
        }}
      />
    </>
  );
}

function Loading() {
  return (
    <div className="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
      <Loader2 className="size-4 animate-spin" />
      载入中
    </div>
  );
}

function NameDialog({ edit, onClose, onSubmit }: { edit: Edit | null; onClose: () => void; onSubmit: (e: Edit) => Promise<void> }) {
  const [form, setForm] = useState<Edit | null>(edit);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setForm(edit);
    setBusy(false);
  }, [edit]);

  if (!edit || !form) return null;
  const label = edit.kind === "calendar" ? "日历" : "通讯录";

  async function submit() {
    if (!form || !form.name.trim()) return;
    setBusy(true);
    try {
      await onSubmit(form);
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
          <DialogTitle>{edit.id ? `编辑${label}` : `新建${label}`}</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <div className="flex gap-2">
            {edit.kind === "calendar" && (
              <input
                type="color"
                value={/^#[0-9a-f]{6}$/i.test(form.color) ? form.color : "#5B7FA6"}
                onChange={(e) => setForm({ ...form, color: e.target.value })}
                className="h-9 w-10 shrink-0 cursor-pointer rounded-sm bg-transparent"
                aria-label="颜色"
              />
            )}
            <Input autoFocus value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={`${label}名称`} />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button type="submit" disabled={busy || !form.name.trim()}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              确定
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function Confirm({
  open,
  title,
  desc,
  onClose,
  onConfirm,
}: {
  open: boolean;
  title: string;
  desc: string;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  return (
    <AlertDialog open={open} onOpenChange={(o) => !o && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{desc}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => {
              onConfirm().catch((err) => toast.error(errorMessage(err)));
            }}
          >
            删除
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
