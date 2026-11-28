"use client";

import { t, tContext } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useMemo, useState } from "react";
import { CalendarClock, ChevronDown, ChevronRight, Flag, Loader2, Mail, Plus, Repeat } from "lucide-react";
import { toast } from "sonner";

import { calendarColor, calendarKey } from "@/components/calendar/calendar-utils";
import { OptionSelect } from "@/components/option-select";
import { TaskEditor, prioRank } from "@/components/todo/task-editor";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { listen, requestOpenEmail, send, takePendingOpenTask } from "@/lib/app-bus";
import { addDays, hm, parseLocalDateTime, shortDate, startOfDay } from "@/lib/date";
import { useMessageMarksState } from "@/lib/message-tags";
import { useCompact } from "@/lib/use-compact";
import { cn } from "@/lib/utils";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Calendar,
  type EmailSummary,
  type PIMChange,
  type TaskItem,
} from "@/lib/api";
import { main } from "@/wailsjs/go/models";

/** 清单选择里的「全部清单」。 */
const ALL = "";

/** 向服务端补查旗标邮件的最短间隔: 每次切到待办都让每个账号查一遍太浪费。 */
const REFRESH_EVERY = 5 * 60 * 1000;
let lastRefresh = 0;

type Group = "overdue" | "today" | "later" | "someday" | "emails" | "done";

/**
 * 待办模块: 任务 (存在日历里的 Task, 「清单」即日历, 与 Bulwark 及 CalDAV 客户端互通) 与打了待办旗标的邮件
 * (设置 → 标签开了「用旗标标记待办」时) 合在一起, 按 已过期 / 今天 / 之后 / 没有日期 / 旗标邮件 分组, 已完成的任务收在最后。
 * 到期提醒在后端 (task_reminders.go)。
 */
export function TodoShell({ active }: { active: boolean }) {
  const { locale } = useI18n();
  const compact = useCompact();
  const { taskFlags } = useMessageMarksState();
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [emails, setEmails] = useState<EmailSummary[]>([]);
  const [calendars, setCalendars] = useState<Calendar[]>([]);
  const [list, setList] = useState(ALL);
  const [quick, setQuick] = useState("");
  const [loading, setLoading] = useState(false);
  const [showDone, setShowDone] = useState(false);
  const [editing, setEditing] = useState<TaskItem | null>(null);
  // 分组按此刻判断「已过期 / 今天」: 模块开着时每分钟重算一次, 过了截止时间的任务挪到「已过期」。
  const [now, setNow] = useState(() => new Date());

  const writable = useMemo(
    () => calendars.filter((c) => !c.myRights || c.myRights.mayWriteAll || c.myRights.mayWriteOwn),
    [calendars],
  );
  const target = (list && writable.find((c) => calendarKey(c.accountId, c.id) === list)) || writable.find((c) => c.isDefault) || writable[0];

  const loadTasks = useCallback(async (): Promise<TaskItem[] | null> => {
    try {
      const [items, cals] = await Promise.all([api.listTasks(), api.listCalendars()]);
      setTasks(items ?? []);
      setCalendars(cals ?? []);
      return items ?? [];
    } catch (err) {
      toast.error(errorMessage(err));
      return null;
    }
  }, []);

  const loadEmails = useCallback(async () => {
    try {
      setEmails((await api.listTodoEmails()) ?? []);
    } catch {
      /* 缓存读不出时旗标邮件这一组留空, 任务照常。 */
    }
  }, []);

  // 打开模块时读一次本地, 再让各账号服务端补查旗标邮件 (本机只缓存最近的邮件)。
  useEffect(() => {
    if (!active) return;
    let cancelled = false;
    void (async () => {
      setLoading(true);
      await loadTasks();
      if (taskFlags) {
        await loadEmails();
        if (Date.now() - lastRefresh > REFRESH_EVERY) {
          lastRefresh = Date.now();
          await api.refreshTodoEmails().catch(() => {});
          if (!cancelled) await loadEmails();
        }
      }
      if (!cancelled) setLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [active, taskFlags, loadTasks, loadEmails]);

  useEffect(() => {
    const offPim = onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.type === "" || c.type === "CalendarEvent" || c.type === "Calendar") void loadTasks();
    });
    // 同步时 mail:changed 一轮会来好几次, 合并成一次本地查询。
    let timer: ReturnType<typeof setTimeout> | undefined;
    const offMail = onEvent(Events.mailChanged, () => {
      if (!taskFlags) return;
      clearTimeout(timer);
      timer = setTimeout(() => void loadEmails(), 500);
    });
    return () => {
      offPim();
      offMail();
      clearTimeout(timer);
    };
  }, [taskFlags, loadTasks, loadEmails]);

  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(id);
  }, [active]);

  // 点了任务提醒: 重新读一遍任务再打开它; 找不到 (已被删除) 时提示, 不留着等以后突然弹出来。
  const openRequested = useCallback(
    async (want: { accountId: string; id: string }) => {
      const items = await loadTasks();
      const found = items?.find((x) => x.accountId === want.accountId && x.id === want.id);
      if (found) setEditing(found);
      else if (items) toast.error(t("找不到这个任务，可能已被删除"));
    },
    [loadTasks],
  );

  // 冷启动时请求早于挂载, 先取走排队的那一个。
  useEffect(() => {
    const pending = takePendingOpenTask();
    const timer = pending ? setTimeout(() => void openRequested(pending), 0) : undefined;
    const off = listen("openTask", (d) => {
      takePendingOpenTask();
      void openRequested(d);
    });
    return () => {
      clearTimeout(timer);
      off();
    };
  }, [openRequested]);

  const groups = useMemo(() => {
    const today = startOfDay(now);
    const tomorrow = addDays(today, 1);
    const out: Record<Group, TaskItem[]> = { overdue: [], today: [], later: [], someday: [], emails: [], done: [] };
    const sorted = tasks
      .filter((x) => !list || calendarKey(x.accountId, x.calendarId) === list)
      .sort((a, b) => {
        if (!!a.due !== !!b.due) return a.due ? -1 : 1;
        if (a.due && b.due && a.due !== b.due) return a.due < b.due ? -1 : 1;
        return prioRank(a.priority) - prioRank(b.priority) || a.title.localeCompare(b.title, locale);
      });
    for (const task of sorted) {
      if (task.completed) {
        out.done.push(task);
        continue;
      }
      if (!task.due) {
        out.someday.push(task);
        continue;
      }
      const due = parseLocalDateTime(task.due);
      // 全天任务当天都不算过期; 定时任务过了时刻就算。
      const overdue = task.dueAllDay ? due < today : due < now;
      if (overdue) out.overdue.push(task);
      else if (due < tomorrow) out.today.push(task);
      else out.later.push(task);
    }
    // 最近完成的在前。
    out.done.sort((a, b) => (a.updated < b.updated ? 1 : -1));
    return out;
  }, [tasks, list, locale, now]);

  async function addQuick() {
    const title = quick.trim();
    if (!title || !target) return;
    setQuick("");
    try {
      await api.saveTask(
        main.TaskInput.createFrom({
          accountId: target.accountId,
          id: "",
          calendarId: target.id,
          title,
          description: "",
          due: "",
          dueAllDay: false,
          priority: 0,
          completed: false,
        }),
      );
      void loadTasks();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function toggleTask(task: TaskItem, done: boolean) {
    const same = (x: TaskItem) => x.accountId === task.accountId && x.id === task.id;
    setTasks((prev) => prev.map((x) => (same(x) ? { ...x, completed: done } : x)));
    // 编辑栏开着同一个任务时跟着改, 否则之后点保存会把完成状态改回去。
    setEditing((cur) => (cur && same(cur) ? { ...cur, completed: done } : cur));
    try {
      await api.setTaskCompleted(task.accountId, task.id, done);
    } catch (err) {
      toast.error(errorMessage(err));
      void loadTasks();
    }
  }

  async function completeEmail(e: EmailSummary) {
    setEmails((prev) => prev.filter((x) => !(x.accountId === e.accountId && x.id === e.id)));
    try {
      await api.setTaskFlag(e.accountId, [e.id], "completed");
    } catch (err) {
      toast.error(errorMessage(err));
      void loadEmails();
    }
  }

  function openEmail(e: EmailSummary) {
    send("navigate", { module: "mail" });
    requestOpenEmail({ accountId: e.accountId, emailId: e.id });
  }

  const listOptions = useMemo(
    () => [[ALL, t("全部清单")] as const, ...writable.map((c) => [calendarKey(c.accountId, c.id), c.name] as const)],
    [writable],
  );
  const open = groups.overdue.length + groups.today.length + groups.later.length + groups.someday.length;
  const empty = open === 0 && (!taskFlags || emails.length === 0);

  const listPane = (
    <div className="flex min-w-0 flex-1 flex-col">
      <header className="border-border flex h-11 shrink-0 items-center gap-2 border-b px-4">
        <h2 className="text-base font-semibold">{tContext("module", "待办")}</h2>
        {loading && <Loader2 className="text-muted-foreground size-4 animate-spin" />}
        {writable.length > 1 && (
          <OptionSelect value={list} onChange={setList} options={listOptions} className="ml-auto h-8 w-40" />
        )}
      </header>
      <form
        className="border-border flex shrink-0 items-center gap-2 border-b px-4 py-1.5"
        onSubmit={(e) => {
          e.preventDefault();
          void addQuick();
        }}
      >
        <Plus className="text-muted-foreground size-4 shrink-0" />
        <Input
          value={quick}
          onChange={(e) => setQuick(e.target.value)}
          placeholder={target ? t("添加任务到「{0}」，回车保存", target.name) : t("没有可写入的日历")}
          disabled={!target}
          className="h-8 border-0 bg-transparent px-0 shadow-none focus-visible:ring-0"
        />
      </form>
      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto pb-6">
        {empty && !loading && (
          <p className="text-muted-foreground px-6 py-12 text-center text-sm leading-relaxed">
            {t("没有待办。")}
            {!taskFlags && (
              <>
                <br />
                {t("在设置 → 标签里打开「用旗标标记待办」后，打了旗标的邮件也会列在这里。")}
              </>
            )}
          </p>
        )}
        <TaskGroup title={t("已过期")} tone="danger" items={groups.overdue} calendars={calendars} editing={editing} onOpen={setEditing} onToggle={toggleTask} />
        <TaskGroup title={t("今天")} items={groups.today} calendars={calendars} editing={editing} onOpen={setEditing} onToggle={toggleTask} />
        <TaskGroup title={t("之后")} items={groups.later} calendars={calendars} editing={editing} onOpen={setEditing} onToggle={toggleTask} />
        <TaskGroup title={t("没有日期")} items={groups.someday} calendars={calendars} editing={editing} onOpen={setEditing} onToggle={toggleTask} />
        {taskFlags && emails.length > 0 && (
          <section>
            <GroupTitle title={t("旗标邮件")} count={emails.length} />
            <ul>
              {emails.map((e) => (
                <li
                  key={`${e.accountId}/${e.id}`}
                  className="hover:bg-secondary flex cursor-default items-center gap-3 px-4 py-2"
                  onClick={() => openEmail(e)}
                >
                  <span onClick={(ev) => ev.stopPropagation()} className="flex">
                    <Checkbox checked={false} onCheckedChange={() => void completeEmail(e)} aria-label={t("标记为已完成")} />
                  </span>
                  <Mail className="text-muted-foreground size-3.5 shrink-0" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm">{e.subject || t("(无主题)")}</span>
                    <span className="text-muted-foreground block truncate text-xs">
                      {e.from?.[0]?.name || e.from?.[0]?.email || ""}
                    </span>
                  </span>
                  <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{shortDate(new Date(e.receivedAt))}</span>
                </li>
              ))}
            </ul>
          </section>
        )}
        {groups.done.length > 0 && (
          <section>
            <button
              type="button"
              onClick={() => setShowDone((v) => !v)}
              className="text-muted-foreground hover:text-foreground flex w-full items-center gap-1 px-4 pt-4 pb-1 text-xs font-medium"
            >
              {showDone ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
              {t("已完成")}
              <span className="tabular-nums">{groups.done.length}</span>
            </button>
            {showDone && (
              <TaskGroup items={groups.done} calendars={calendars} editing={editing} onOpen={setEditing} onToggle={toggleTask} />
            )}
          </section>
        )}
      </div>
    </div>
  );

  const editor = editing && (
    <TaskEditor
      key={`${editing.accountId}/${editing.id}`}
      task={editing}
      calendars={writable}
      onClose={() => setEditing(null)}
      onSaved={() => {
        setEditing(null);
        void loadTasks();
      }}
    />
  );

  // 窄屏: 编辑时占满, 关掉回到列表。
  if (compact) {
    return <div className="bg-background flex h-full min-h-0">{editor || listPane}</div>;
  }
  return (
    <div className="bg-background flex h-full min-h-0">
      {listPane}
      {editor && <aside className="border-border bg-card w-[380px] max-w-[50%] shrink-0 border-l">{editor}</aside>}
    </div>
  );
}

function GroupTitle({ title, count, tone }: { title: string; count: number; tone?: "danger" }) {
  return (
    <h3 className={cn("flex items-center gap-1.5 px-4 pt-4 pb-1 text-xs font-medium", tone === "danger" ? "text-destructive" : "text-muted-foreground")}>
      {title}
      <span className="tabular-nums">{count}</span>
    </h3>
  );
}

function TaskGroup({
  title,
  tone,
  items,
  calendars,
  editing,
  onOpen,
  onToggle,
}: {
  title?: string;
  tone?: "danger";
  items: TaskItem[];
  calendars: Calendar[];
  editing: TaskItem | null;
  onOpen: (task: TaskItem) => void;
  onToggle: (task: TaskItem, done: boolean) => void;
}) {
  useI18n();
  if (items.length === 0) return null;
  const now = new Date();
  return (
    <section>
      {title && <GroupTitle title={title} count={items.length} tone={tone} />}
      <ul>
        {items.map((task) => {
          const due = task.due ? parseLocalDateTime(task.due) : null;
          const overdue = !!due && !task.completed && (task.dueAllDay ? due < startOfDay(now) : due < now);
          const selected = editing?.accountId === task.accountId && editing.id === task.id;
          return (
            <li
              key={`${task.accountId}/${task.id}`}
              className={cn("hover:bg-secondary flex cursor-default items-center gap-3 px-4 py-2", selected && "bg-muted")}
              onClick={() => onOpen(task)}
            >
              <span onClick={(e) => e.stopPropagation()} className="flex">
                <Checkbox checked={task.completed} onCheckedChange={(v) => onToggle(task, v === true)} aria-label={t("完成")} />
              </span>
              <span
                className="size-2 shrink-0 rounded-full"
                style={{ background: calendarColor(calendars, task.accountId, task.calendarId) }}
              />
              <span className={cn("min-w-0 flex-1 truncate text-sm", task.completed && "text-muted-foreground line-through")}>
                {task.title || t("(无标题)")}
              </span>
              {task.recurring && <Repeat className="text-muted-foreground size-3.5 shrink-0" aria-label={t("重复")} />}
              {task.priority > 0 && task.priority <= 4 && <Flag className="text-destructive size-3.5 shrink-0" />}
              {due && (
                <span className={cn("flex shrink-0 items-center gap-1 text-xs tabular-nums", overdue ? "text-destructive" : "text-muted-foreground")}>
                  <CalendarClock className="size-3.5" />
                  {shortDate(due)}
                  {!task.dueAllDay && ` ${hm(due)}`}
                </span>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
