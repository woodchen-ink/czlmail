"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ChevronLeft,
  ChevronRight,
  Loader2,
  Pencil,
  Plus,
  Trash2,
  Upload,
  UserRound,
} from "lucide-react";
import { toast } from "sonner";

import { AgendaView } from "@/components/calendar/agenda-view";
import {
  calendarColor,
  calendarKey,
  occStart,
  type CalendarView,
  type DragPayload,
} from "@/components/calendar/calendar-utils";
import {
  EventPanel,
  type EventTarget,
} from "@/components/calendar/event-dialog";
import { MiniMonth } from "@/components/calendar/mini-month";
import { MonthView } from "@/components/calendar/month-view";
import { TasksView } from "@/components/calendar/tasks-view";
import { TimeGrid } from "@/components/calendar/time-grid";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
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
import { PaneLayout } from "@/components/pane-layout";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Account,
  type Calendar,
  type EventOccurrence,
  type PIMChange,
} from "@/lib/api";
import {
  addDays,
  addMonths,
  dayTitle,
  monthGrid,
  monthTitle,
  shortDate,
  startOfDay,
  startOfWeek,
  toDateInput,
  toLocalDateTime,
  toRFC3339,
} from "@/lib/date";

const HIDDEN_KEY = "czlmail.calendar.hidden";
const VIEW_KEY = "czlmail.calendar.view";

function readJSON<T>(key: string, fallback: T): T {
  try {
    const v = localStorage.getItem(key);
    return v ? (JSON.parse(v) as T) : fallback;
  } catch {
    return fallback;
  }
}

function writeJSON(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // 存不下只影响下次启动。
  }
}

export function CalendarShell({ active }: { active: boolean }) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [calendars, setCalendars] = useState<Calendar[]>([]);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [view, setView] = useState<CalendarView>("month");
  const [cursor, setCursor] = useState(() => startOfDay(new Date()));
  const [events, setEvents] = useState<EventOccurrence[]>([]);
  const [loading, setLoading] = useState(false);
  const [target, setTarget] = useState<EventTarget | null>(null);
  const [calDialog, setCalDialog] = useState<{
    accountId: string;
    calendar?: Calendar;
  } | null>(null);
  const [deleteCal, setDeleteCal] = useState<Calendar | null>(null);

  useEffect(() => {
    setHidden(new Set(readJSON<string[]>(HIDDEN_KEY, [])));
    setView(readJSON<CalendarView>(VIEW_KEY, "month"));
  }, []);

  const range = useMemo(() => {
    switch (view) {
      case "month": {
        const grid = monthGrid(cursor);
        return { from: grid[0], to: addDays(grid[41], 1) };
      }
      case "week": {
        const from = startOfWeek(cursor);
        return { from, to: addDays(from, 7) };
      }
      case "day":
        return { from: cursor, to: addDays(cursor, 1) };
      case "agenda":
        return { from: cursor, to: addDays(cursor, 60) };
      case "tasks":
        return { from: cursor, to: addDays(cursor, 1) };
    }
  }, [view, cursor]);

  const loadCalendars = useCallback(async () => {
    try {
      const [accs, cals] = await Promise.all([
        api.listAccounts(),
        api.listCalendars(),
      ]);
      setAccounts(accs ?? []);
      setCalendars(cals ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  const loadEvents = useCallback(async () => {
    setLoading(true);
    try {
      setEvents(
        (await api.listEvents(toRFC3339(range.from), toRFC3339(range.to))) ??
          [],
      );
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, [range]);

  useEffect(() => {
    if (active) loadCalendars();
  }, [active, loadCalendars]);

  useEffect(() => {
    if (active) loadEvents();
  }, [active, loadEvents]);

  useEffect(() => {
    const offChanged = onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.type === "" || c.type === "Calendar") loadCalendars();
      if (c.type === "" || c.type === "CalendarEvent") loadEvents();
    });
    const offOpen = onEvent(Events.openEvent, (ref: string) => {
      const [accountId, eventId] = String(ref).split("/");
      if (accountId && eventId)
        setTarget({ kind: "edit", accountId, eventId, recurrenceId: "" });
    });
    return () => {
      offChanged();
      offOpen();
    };
  }, [loadCalendars, loadEvents]);

  const visibleEvents = useMemo(
    () =>
      events.filter(
        (o) =>
          !(o.calendarIds ?? []).every((id) =>
            hidden.has(calendarKey(o.accountId, id)),
          ),
      ),
    [events, hidden],
  );

  const busyDays = useMemo(
    () => new Set(visibleEvents.map((o) => toDateInput(occStart(o)))),
    [visibleEvents],
  );

  const canWrite = useCallback(
    (o: EventOccurrence) => {
      const cal = calendars.find(
        (c) => c.accountId === o.accountId && o.calendarIds?.includes(c.id),
      );
      return (
        !cal?.myRights ||
        !!cal.myRights.mayWriteAll ||
        !!cal.myRights.mayWriteOwn
      );
    },
    [calendars],
  );

  function toggleCalendar(key: string, visible: boolean) {
    setHidden((prev) => {
      const next = new Set(prev);
      if (visible) next.delete(key);
      else next.add(key);
      writeJSON(HIDDEN_KEY, [...next]);
      return next;
    });
  }

  function changeView(v: CalendarView) {
    setView(v);
    writeJSON(VIEW_KEY, v);
  }

  function step(dir: 1 | -1) {
    setCursor((c) => {
      switch (view) {
        case "month":
          return addMonths(c, dir);
        case "week":
          return addDays(c, 7 * dir);
        case "day":
          return addDays(c, dir);
        case "agenda":
          return addDays(c, 30 * dir);
        case "tasks":
          return c;
      }
    });
  }

  function openOccurrence(o: EventOccurrence) {
    setTarget({
      kind: "edit",
      accountId: o.accountId,
      eventId: o.eventId,
      recurrenceId: o.recurrenceId,
      occurrenceStart: o.recurrenceId ? String(o.start) : undefined,
    });
  }

  function createAt(start: Date, allDay: boolean) {
    if (allDay) {
      setTarget({
        kind: "new",
        start: startOfDay(start),
        end: addDays(startOfDay(start), 1),
        allDay: true,
      });
    } else {
      setTarget({
        kind: "new",
        start,
        end: new Date(start.getTime() + 60 * 60 * 1000),
        allDay: false,
      });
    }
  }

  async function drop(payload: DragPayload, target: Date, keepTime: boolean) {
    const original = new Date(payload.start);
    let next = target;
    if (payload.allDay) {
      next = startOfDay(target);
    } else if (keepTime) {
      next = new Date(
        target.getFullYear(),
        target.getMonth(),
        target.getDate(),
        original.getHours(),
        original.getMinutes(),
      );
    }
    if (next.getTime() === original.getTime()) return;
    try {
      await api.moveEvent(
        payload.accountId,
        payload.eventId,
        payload.recurrenceId,
        toLocalDateTime(next),
      );
      if (payload.recurrenceId) toast.success("已改期（仅这一次）");
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function importTo(cal: Calendar) {
    try {
      const n = await api.importCalendarFile(cal.accountId, cal.id);
      if (n > 0) toast.success(`已导入 ${n} 个日程到「${cal.name}」`);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  const title =
    view === "tasks"
      ? "任务"
      : view === "month" || view === "agenda"
        ? monthTitle(cursor)
        : view === "week"
          ? `${startOfWeek(cursor).getFullYear()} 年 ${shortDate(startOfWeek(cursor))} – ${shortDate(addDays(startOfWeek(cursor), 6))}`
          : dayTitle(cursor);

  const accountName = (id: string) =>
    accounts.find((a) => a.id === id)?.name ?? id;
  const grouped = useMemo(() => {
    const map = new Map<string, Calendar[]>();
    for (const c of calendars)
      map.set(c.accountId, [...(map.get(c.accountId) ?? []), c]);
    return [...map.entries()];
  }, [calendars]);
  const writable = calendars.filter(
    (c) => !c.myRights || c.myRights.mayWriteAll || c.myRights.mayWriteOwn,
  );

  const sidebar = (
    <div className="flex h-full min-h-0 flex-col">
      <div className="p-2">
        <Button
          className="w-full"
          size="sm"
          onClick={() => createAt(nextHalfHour(), false)}
        >
          <Plus className="size-4" />
          新建日程
        </Button>
      </div>
      <ScrollArea className="min-h-0 flex-1 pb-2">
        <MiniMonth
          selected={cursor}
          busyDays={busyDays}
          onSelect={(d) => setCursor(startOfDay(d))}
        />
        <div className="mt-3 flex flex-col gap-3 px-2">
          {grouped.map(([accountId, list]) => (
            <section key={accountId} className="flex flex-col gap-0.5">
              <div className="group/acc flex items-center">
                <p className="text-muted-foreground flex min-w-0 flex-1 items-center gap-1.5 px-2 py-1 text-xs">
                  <UserRound className="size-3.5" />
                  <span className="truncate">{accountName(accountId)}</span>
                </p>
                <button
                  type="button"
                  aria-label="新建日历"
                  title="新建日历"
                  onClick={() => setCalDialog({ accountId })}
                  className="text-muted-foreground hover:bg-secondary hover:text-foreground rounded-sm p-1 opacity-0 group-hover/acc:opacity-100 focus-visible:opacity-100"
                >
                  <Plus className="size-3.5" />
                </button>
              </div>
              {list.map((c) => {
                const key = calendarKey(c.accountId, c.id);
                const color = calendarColor(calendars, c.accountId, c.id);
                const mayAdmin = !c.myRights || c.myRights.mayAdmin !== false;
                return (
                  <ContextMenu key={key}>
                    <ContextMenuTrigger asChild disabled={!mayAdmin}>
                      <label className="hover:bg-secondary flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-sm">
                        <Checkbox
                          checked={!hidden.has(key)}
                          onCheckedChange={(v) =>
                            toggleCalendar(key, v === true)
                          }
                          style={
                            { "--checkbox-color": color } as React.CSSProperties
                          }
                          className="data-[state=checked]:border-(--checkbox-color) data-[state=checked]:bg-(--checkbox-color)"
                        />
                        <span className="min-w-0 flex-1 truncate">
                          {c.name}
                        </span>
                      </label>
                    </ContextMenuTrigger>
                    <ContextMenuContent className="w-44">
                      <ContextMenuItem
                        onSelect={() =>
                          setCalDialog({ accountId: c.accountId, calendar: c })
                        }
                      >
                        <Pencil />
                        编辑日历
                      </ContextMenuItem>
                      <ContextMenuItem onSelect={() => importTo(c)}>
                        <Upload />
                        导入 .ics
                      </ContextMenuItem>
                      {!c.isDefault && (
                        <>
                          <ContextMenuSeparator />
                          <ContextMenuItem
                            variant="destructive"
                            onSelect={() => setDeleteCal(c)}
                          >
                            <Trash2 />
                            删除日历
                          </ContextMenuItem>
                        </>
                      )}
                    </ContextMenuContent>
                  </ContextMenu>
                );
              })}
            </section>
          ))}
        </div>
      </ScrollArea>
    </div>
  );

  const detail = (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-border flex shrink-0 flex-wrap items-center gap-2 border-b px-3 py-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setCursor(startOfDay(new Date()))}
        >
          今天
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          aria-label="上一页"
          onClick={() => step(-1)}
        >
          <ChevronLeft className="size-4" />
        </Button>
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          aria-label="下一页"
          onClick={() => step(1)}
        >
          <ChevronRight className="size-4" />
        </Button>
        <h2 className="min-w-0 truncate text-base font-semibold">{title}</h2>
        {loading && (
          <Loader2 className="text-muted-foreground size-4 animate-spin" />
        )}

        <div className="ml-auto flex items-center gap-2">
          <ToggleGroup
            type="single"
            variant="outline"
            size="sm"
            value={view}
            onValueChange={(v) => v && changeView(v as CalendarView)}
          >
            <ToggleGroupItem value="month">月</ToggleGroupItem>
            <ToggleGroupItem value="week">周</ToggleGroupItem>
            <ToggleGroupItem value="day">天</ToggleGroupItem>
            <ToggleGroupItem value="agenda">议程</ToggleGroupItem>
            <ToggleGroupItem value="tasks">任务</ToggleGroupItem>
          </ToggleGroup>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm">
                <Upload className="size-4" />
                导入
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-72">
              <DropdownMenuLabel>导入 .ics 到</DropdownMenuLabel>
              {writable.map((c) => (
                <DropdownMenuItem
                  key={calendarKey(c.accountId, c.id)}
                  onSelect={() => importTo(c)}
                >
                  <span
                    className="size-2.5 rounded-full"
                    style={{
                      background: calendarColor(calendars, c.accountId, c.id),
                    }}
                  />
                  <span className="min-w-0 flex-1 truncate">{c.name}</span>
                  <span className="text-muted-foreground max-w-28 shrink-0 truncate text-xs">
                    {accountName(c.accountId)}
                  </span>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1">
          {view === "month" && (
            <MonthView
              cursor={cursor}
              events={visibleEvents}
              calendars={calendars}
              canWrite={canWrite}
              onOpen={openOccurrence}
              onCreate={(d) =>
                createAt(
                  new Date(d.getFullYear(), d.getMonth(), d.getDate(), 9),
                  false,
                )
              }
              onDrop={(p, d) => drop(p, d, true)}
              onShowDay={(d) => {
                setCursor(startOfDay(d));
                changeView("day");
              }}
            />
          )}
          {(view === "week" || view === "day") && (
            <TimeGrid
              days={
                view === "week"
                  ? Array.from({ length: 7 }, (_, i) =>
                      addDays(startOfWeek(cursor), i),
                    )
                  : [cursor]
              }
              events={visibleEvents}
              calendars={calendars}
              canWrite={canWrite}
              onOpen={openOccurrence}
              onCreate={createAt}
              onDrop={(p, d, allDay) => drop(p, d, allDay)}
              onShowDay={(d) => {
                setCursor(startOfDay(d));
                changeView("day");
              }}
            />
          )}
          {view === "tasks" && (
            <TasksView calendars={calendars} hidden={hidden} active={active} />
          )}
          {view === "agenda" && (
            <AgendaView
              events={visibleEvents}
              calendars={calendars}
              onOpen={openOccurrence}
            />
          )}
        </div>
        {target && (
          <aside className="border-border bg-card w-[420px] max-w-[50%] shrink-0 border-l">
            <EventPanel
              key={panelKey(target)}
              target={target}
              calendars={calendars}
              accounts={accounts}
              onClose={() => setTarget(null)}
              onSaved={loadEvents}
            />
          </aside>
        )}
      </div>
    </div>
  );

  return (
    <>
      <PaneLayout
        id="calendar"
        sidebar={sidebar}
        detail={detail}
        sidebarWidth={260}
      />
      <CalendarDialog
        target={calDialog}
        onClose={() => setCalDialog(null)}
        onSaved={loadCalendars}
      />
      <AlertDialog
        open={deleteCal !== null}
        onOpenChange={(o) => !o && setDeleteCal(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除日历</AlertDialogTitle>
            <AlertDialogDescription>
              确定删除「{deleteCal?.name}
              」？其中的全部日程与任务会一并从服务器删除，无法恢复。
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={async () => {
                const c = deleteCal;
                if (!c) return;
                try {
                  await api.deleteCalendar(c.accountId, c.id);
                  toast.success("已删除日历");
                  loadCalendars();
                  loadEvents();
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
    </>
  );
}

const CALENDAR_COLORS = [
  "#5B7FA6",
  "#6E9A8A",
  "#9D6877",
  "#B08D57",
  "#7D6B91",
  "#5E8FA0",
  "#8A8F5E",
  "#A0725B",
];

function CalendarDialog({
  target,
  onClose,
  onSaved,
}: {
  target: { accountId: string; calendar?: Calendar } | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState("");
  const [color, setColor] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!target) return;
    setName(target.calendar?.name ?? "");
    setColor(target.calendar?.color || CALENDAR_COLORS[0]);
    setBusy(false);
  }, [target]);

  if (!target) return null;

  async function submit() {
    if (!target || !name.trim() || busy) return;
    setBusy(true);
    try {
      await api.saveCalendar(
        target.accountId,
        target.calendar?.id ?? "",
        name,
        color,
      );
      toast.success(target.calendar ? "已保存" : "已创建日历");
      onSaved();
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
          <DialogTitle>{target.calendar ? "编辑日历" : "新建日历"}</DialogTitle>
        </DialogHeader>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <Input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="日历名称"
          />
          {/* 日历颜色是存到服务器的用户数据, 各客户端共用, 因此以色值保存。 */}
          <div className="flex flex-wrap gap-2">
            {CALENDAR_COLORS.map((c) => (
              <button
                key={c}
                type="button"
                aria-label={c}
                onClick={() => setColor(c)}
                className={`size-7 rounded-full border-2 ${color.toLowerCase() === c.toLowerCase() ? "border-foreground" : "border-transparent"}`}
                style={{ background: c }}
              />
            ))}
            <input
              type="color"
              value={/^#[0-9a-f]{6}$/i.test(color) ? color : "#5B7FA6"}
              onChange={(e) => setColor(e.target.value)}
              className="size-7 cursor-pointer rounded-full bg-transparent"
              aria-label="自定义颜色"
            />
          </div>
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

function panelKey(t: EventTarget): string {
  return t.kind === "edit"
    ? `${t.accountId}/${t.eventId}/${t.recurrenceId}`
    : `new/${t.start.getTime()}/${t.allDay}`;
}

function nextHalfHour(): Date {
  const d = new Date();
  d.setSeconds(0, 0);
  d.setMinutes(d.getMinutes() < 30 ? 30 : 60);
  return d;
}
