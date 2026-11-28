"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useState } from "react";
import { Loader2, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { calendarKey } from "@/components/calendar/calendar-utils";
import { OptionSelect } from "@/components/option-select";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { api, errorMessage, type Calendar, type TaskItem } from "@/lib/api";
import { parseLocalDateTime, toDateInput, toTimeInput } from "@/lib/date";
import { main } from "@/wailsjs/go/models";

// iCalendar 的优先级: 1 最高, 9 最低, 0 未设置。
const getPriorityOptions = () => ([
  ["0", t("无")],
  ["1", t("高")],
  ["5", t("中")],
  ["9", t("低")],
] as const);

/** 任务的编辑栏 (待办模块右侧; 窄屏占满)。任务存成日历里的 Task, 「清单」即所属日历。 */
export function TaskEditor({
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
  useI18n();
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
      toast.error(t("请填写标题"));
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
      toast.success(t("已保存"));
      onSaved();
    } catch (err) {
      toast.error(errorMessage(err));
      setBusy(false);
    }
  }

  async function remove() {
    try {
      await api.deleteTask(task.accountId, task.id);
      toast.success(t("已删除任务"));
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
          aria-label={t("关闭")}
          onClick={onClose}
        >
          <X className="size-4" />
        </Button>
        <h2 className="flex-1 text-base font-semibold">{t("编辑任务")}</h2>
        <Button
          variant="ghost"
          size="icon"
          className="hover:text-destructive size-8"
          aria-label={t("删除")}
          onClick={remove}
        >
          <Trash2 className="size-4" />
        </Button>
      </header>
      <div className="czl-scroll flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4">
        <Input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder={t("标题")}
          className="h-10 text-base font-medium"
        />
        <Textarea
          rows={4}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder={t("描述")}
        />
        <div className="flex flex-col gap-1.5">
          <Label>{t("截止时间")}</Label>
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
                aria-label={t("清除截止时间")}
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
          <Label>{t("优先级")}</Label>
          <OptionSelect
            value={String(priority)}
            onChange={(v) => setPriority(Number(v))}
            options={getPriorityOptions()}
            className="w-full"
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label>{t("清单")}</Label>
          <OptionSelect
            value={calendar}
            onChange={setCalendar}
            options={calendars
              .filter((c) => c.accountId === task.accountId)
              .map((c) => [calendarKey(c.accountId, c.id), c.name] as const)}
            className="w-full"
          />
        </div>
      </div>
      <footer className="border-border flex shrink-0 justify-end gap-2 border-t px-4 py-2.5">
        <Button variant="outline" onClick={onClose}>
          {t("取消")}</Button>
        <Button onClick={save} disabled={busy}>
          {busy && <Loader2 className="size-4 animate-spin" />}
          {t("保存")}</Button>
      </footer>
    </div>
  );
}

/** JSCalendar 优先级: 1 最高, 9 最低, 0 未设置。 */
export function prioRank(p: number) {
  return p === 0 ? 10 : p;
}
