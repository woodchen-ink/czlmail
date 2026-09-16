"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { CalendarClock, Flag, Loader2, Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import {
  calendarColor,
  calendarKey,
} from "@/components/calendar/calendar-utils";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Calendar,
  type PIMChange,
  type TaskItem,
} from "@/lib/api";
import {
  parseLocalDateTime,
  shortDate,
  toDateInput,
  toTimeInput,
  hm,
} from "@/lib/date";
import { main } from "@/wailsjs/go/models";

type Filter = "open" | "done" | "all";

/**
 * 任务列表。Stalwart 把任务存成 @type=Task 的日历对象, 因此任务挂在日历下,
 * 与 Bulwark 的「任务」视图互通。
 */
export function TasksView({
  calendars,
  hidden,
  active,
}: {
  calendars: Calendar[];
  hidden: Set<string>;
  active: boolean;
}) {
  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [filter, setFilter] = useState<Filter>("open");
  const [quick, setQuick] = useState("");
  const [editing, setEditing] = useState<TaskItem | null>(null);

  const writable = useMemo(
    () =>
      calendars.filter(
        (c) => !c.myRights || c.myRights.mayWriteAll || c.myRights.mayWriteOwn,
      ),
    [calendars],
  );
  const defaultCal = writable.find((c) => c.isDefault) ?? writable[0];

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setTasks((await api.listTasks()) ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  useEffect(() => {
    return onEvent(Events.pimChanged, (c: PIMChange) => {
      if (c.type === "" || c.type === "CalendarEvent") load();
    });
  }, [load]);

  const shown = useMemo(() => {
    const list = tasks.filter(
      (t) => !hidden.has(calendarKey(t.accountId, t.calendarId)),
    );
    const filtered = list.filter((t) =>
      filter === "all" ? true : filter === "done" ? t.completed : !t.completed,
    );
    return filtered.sort((a, b) => {
      if (a.completed !== b.completed) return a.completed ? 1 : -1;
      if (!!a.due !== !!b.due) return a.due ? -1 : 1;
      if (a.due && b.due && a.due !== b.due) return a.due < b.due ? -1 : 1;
      return (
        prioRank(a.priority) - prioRank(b.priority) ||
        a.title.localeCompare(b.title, "zh-CN")
      );
    });
  }, [tasks, hidden, filter]);

  async function addQuick() {
    const title = quick.trim();
    if (!title || !defaultCal) return;
    setQuick("");
    try {
      await api.saveTask(
        main.TaskInput.createFrom({
          accountId: defaultCal.accountId,
          id: "",
          calendarId: defaultCal.id,
          title,
          description: "",
          due: "",
          dueAllDay: false,
          priority: 0,
          completed: false,
        }),
      );
      load();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function toggle(t: TaskItem, done: boolean) {
    setTasks((prev) =>
      prev.map((x) =>
        x.accountId === t.accountId && x.id === t.id
          ? { ...x, completed: done }
          : x,
      ),
    );
    try {
      await api.setTaskCompleted(t.accountId, t.id, done);
    } catch (err) {
      toast.error(errorMessage(err));
      load();
    }
  }

  const now = new Date();

  return (
    <div className="flex h-full min-h-0">
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="border-border flex shrink-0 items-center gap-2 border-b px-4 py-2">
          <form
            className="flex min-w-0 flex-1 items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              addQuick();
            }}
          >
            <Plus className="text-muted-foreground size-4 shrink-0" />
            <Input
              value={quick}
              onChange={(e) => setQuick(e.target.value)}
              placeholder={
                defaultCal
                  ? `添加任务到「${defaultCal.name}」，回车保存`
                  : "没有可写入的日历"
              }
              disabled={!defaultCal}
              className="h-8 border-0 bg-transparent shadow-none focus-visible:ring-0"
            />
          </form>
          <div className="bg-muted flex rounded-md p-0.5 text-xs">
            {(
              [
                ["open", "未完成"],
                ["done", "已完成"],
                ["all", "全部"],
              ] as [Filter, string][]
            ).map(([k, label]) => (
              <button
                key={k}
                type="button"
                onClick={() => setFilter(k)}
                className={cn(
                  "rounded-sm px-2.5 py-1",
                  filter === k ? "bg-card shadow-xs" : "text-muted-foreground",
                )}
              >
                {label}
              </button>
            ))}
          </div>
          {loading && (
            <Loader2 className="text-muted-foreground size-4 animate-spin" />
          )}
        </div>

        <div className="czl-scroll min-h-0 flex-1 overflow-y-auto">
          {shown.length === 0 ? (
            <p className="text-muted-foreground py-12 text-center text-sm">
              {filter === "done" ? "没有已完成的任务" : "没有任务"}
            </p>
          ) : (
            <ul>
              {shown.map((t) => {
                const due = t.due ? parseLocalDateTime(t.due) : null;
                const overdue = !!due && !t.completed && due < now;
                const selected =
                  editing?.accountId === t.accountId && editing.id === t.id;
                return (
                  <li
                    key={`${t.accountId}/${t.id}`}
                    className={cn(
                      "border-border hover:bg-secondary flex cursor-default items-center gap-3 border-b px-4 py-2.5",
                      selected && "bg-muted",
                    )}
                    onClick={() => setEditing(t)}
                  >
                    <span onClick={(e) => e.stopPropagation()}>
                      <Checkbox
                        checked={t.completed}
                        onCheckedChange={(v) => toggle(t, v === true)}
                        aria-label="完成"
                      />
                    </span>
                    <span
                      className="size-2 shrink-0 rounded-full"
                      style={{
                        background: calendarColor(
                          calendars,
                          t.accountId,
                          t.calendarId,
                        ),
                      }}
                    />
                    <span
                      className={cn(
                        "min-w-0 flex-1 truncate text-sm",
                        t.completed && "text-muted-foreground line-through",
                      )}
                    >
                      {t.title || "(无标题)"}
                    </span>
                    {t.priority > 0 && t.priority <= 4 && (
                      <Flag className="text-destructive size-3.5 shrink-0" />
                    )}
                    {due && (
                      <span
                        className={cn(
                          "flex shrink-0 items-center gap-1 text-xs tabular-nums",
                          overdue
                            ? "text-destructive"
                            : "text-muted-foreground",
                        )}
                      >
                        <CalendarClock className="size-3.5" />
                        {shortDate(due)}
                        {!t.dueAllDay && ` ${hm(due)}`}
                      </span>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>

      {editing && (
        <aside className="border-border bg-card w-[380px] max-w-[50%] shrink-0 border-l">
          <TaskEditor
            key={`${editing.accountId}/${editing.id}`}
            task={editing}
            calendars={writable}
            onClose={() => setEditing(null)}
            onSaved={() => {
              setEditing(null);
              load();
            }}
          />
        </aside>
      )}
    </div>
  );
}

function TaskEditor({
  task,
  calendars,
  onClose,
  onSaved,
}: {
  task: TaskItem;
  calendars: Calendar[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const due = task.due ? parseLocalDateTime(task.due) : null;
  const [title, setTitle] = useState(task.title);
  const [description, setDescription] = useState(task.description);
  const [dueDate, setDueDate] = useState(due ? toDateInput(due) : "");
  const [dueTime, setDueTime] = useState(
    due && !task.dueAllDay ? toTimeInput(due) : "",
  );
  const [priority, setPriority] = useState(task.priority);
  const [calendar, setCalendar] = useState(
    calendarKey(task.accountId, task.calendarId),
  );
  const [busy, setBusy] = useState(false);

  async function save() {
    if (!title.trim()) {
      toast.error("请填写标题");
      return;
    }
    const [accountId, calendarId] = calendar.split("/");
    setBusy(true);
    try {
      await api.saveTask(
        main.TaskInput.createFrom({
          accountId: task.accountId,
          id: task.id,
          calendarId:
            accountId === task.accountId ? calendarId : task.calendarId,
          title,
          description,
          due: dueDate ? `${dueDate}T${dueTime || "00:00"}:00` : "",
          dueAllDay: !!dueDate && !dueTime,
          priority,
          completed: task.completed,
        }),
      );
      toast.success("已保存");
      onSaved();
    } catch (err) {
      toast.error(errorMessage(err));
      setBusy(false);
    }
  }

  async function remove() {
    try {
      await api.deleteTask(task.accountId, task.id);
      toast.success("已删除任务");
      onSaved();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  return (
    <div className="flex h-full flex-col">
      <header className="border-border flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <Button
          variant="ghost"
          size="icon"
          className="size-8"
          aria-label="关闭"
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
        <h2 className="flex-1 text-base font-semibold">编辑任务</h2>
        <Button
          variant="ghost"
          size="icon"
          className="hover:text-destructive size-8"
          aria-label="删除"
          onClick={remove}
        >
          <Trash2 className="size-4" />
        </Button>
      </header>
      <div className="czl-scroll flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="标题"
          className="h-10 text-base font-medium"
        />
        <Textarea
          rows={4}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="描述"
        />
        <div className="flex flex-col gap-1.5">
          <Label>截止时间</Label>
          <div className="flex gap-2">
            <Input
              type="date"
              value={dueDate}
              onChange={(e) => setDueDate(e.target.value)}
            />
            <Input
              type="time"
              className="w-28"
              value={dueTime}
              disabled={!dueDate}
              onChange={(e) => setDueTime(e.target.value)}
            />
            {dueDate && (
              <Button
                variant="ghost"
                size="icon"
                className="size-9 shrink-0"
                aria-label="清除截止时间"
                onClick={() => {
                  setDueDate("");
                  setDueTime("");
                }}
              >
                <X className="size-4" />
              </Button>
            )}
          </div>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label>优先级</Label>
          <select
            value={priority}
            onChange={(e) => setPriority(Number(e.target.value))}
            className="border-input h-9 rounded-sm border bg-transparent px-2 text-sm"
          >
            <option value={0} className="bg-popover">
              无
            </option>
            <option value={1} className="bg-popover">
              高
            </option>
            <option value={5} className="bg-popover">
              中
            </option>
            <option value={9} className="bg-popover">
              低
            </option>
          </select>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label>日历</Label>
          <select
            value={calendar}
            onChange={(e) => setCalendar(e.target.value)}
            className="border-input h-9 rounded-sm border bg-transparent px-2 text-sm"
          >
            {calendars
              .filter((c) => c.accountId === task.accountId)
              .map((c) => (
                <option
                  key={c.id}
                  value={calendarKey(c.accountId, c.id)}
                  className="bg-popover"
                >
                  {c.name}
                </option>
              ))}
          </select>
        </div>
      </div>
      <footer className="border-border flex shrink-0 justify-end gap-2 border-t px-4 py-2.5">
        <Button variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button onClick={save} disabled={busy}>
          {busy && <Loader2 className="size-4 animate-spin" />}
          保存
        </Button>
      </footer>
    </div>
  );
}

/** JSCalendar 优先级: 1 最高, 9 最低, 0 未设置。 */
function prioRank(p: number) {
  return p === 0 ? 10 : p;
}
