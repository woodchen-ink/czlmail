"use client";

import { useEffect, useMemo, useState } from "react";
import {
  AlignLeft,
  Bell,
  Check,
  Clock,
  Loader2,
  MapPin,
  Plus,
  Repeat,
  Trash2,
  Users,
  Video,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { RecipientInput } from "@/components/recipient-input";
import { parseAddresses } from "@/components/compose/compose-pane";
import {
  calendarColor,
  calendarKey,
} from "@/components/calendar/calendar-utils";
import { cn } from "@/lib/utils";
import {
  api,
  errorMessage,
  type Account,
  type Calendar,
  type EventDetail,
} from "@/lib/api";
import {
  WEEKDAYS,
  addDays,
  parseLocalDateTime,
  toDateInput,
  toLocalDateTime,
  toTimeInput,
} from "@/lib/date";
import { main } from "@/wailsjs/go/models";
import { BrowserOpenURL } from "@/wailsjs/runtime/runtime";

/** 打开面板的目标：已有事件（可能是重复事件的某一次），或新建时的初始时间。 */
export type EventTarget =
  | {
      kind: "edit";
      accountId: string;
      eventId: string;
      recurrenceId: string;
      occurrenceStart?: string;
    }
  | { kind: "new"; start: Date; end: Date; allDay: boolean };

interface Props {
  target: EventTarget;
  calendars: Calendar[];
  accounts: Account[];
  onClose: () => void;
  onSaved: () => void;
}

const DAY_CODES = ["mo", "tu", "we", "th", "fr", "sa", "su"];

const ALERT_OPTIONS = [
  { value: 0, label: "开始时" },
  { value: 5, label: "提前 5 分钟" },
  { value: 10, label: "提前 10 分钟" },
  { value: 15, label: "提前 15 分钟" },
  { value: 30, label: "提前 30 分钟" },
  { value: 60, label: "提前 1 小时" },
  { value: 120, label: "提前 2 小时" },
  { value: 1440, label: "提前 1 天" },
  { value: 2880, label: "提前 2 天" },
  { value: 10080, label: "提前 1 周" },
];

interface Form {
  calendar: string; // accountId/calendarId
  title: string;
  location: string;
  virtualLocation: string;
  description: string;
  attendees: string;
  allDay: boolean;
  startDate: string;
  startTime: string;
  endDate: string;
  endTime: string;
  frequency: string; // none daily weekly monthly yearly
  interval: number;
  byDay: string[];
  endMode: "never" | "count" | "until";
  count: number;
  until: string;
  alerts: number[];
}

type Scope = "this" | "all";

/** 日程编辑面板, 显示在日历右侧。字段组织参照 Bulwark。 */
export function EventPanel({
  target,
  calendars,
  accounts,
  onClose,
  onSaved,
}: Props) {
  const [detail, setDetail] = useState<EventDetail | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [busy, setBusy] = useState(false);
  const [askScope, setAskScope] = useState<null | "save" | "delete">(null);
  // 打开某一次时表单里显示的开始时间。改"所有日程"时按与它的差值平移整个序列,
  // 否则序列首次会被挪到用户点开的那一天。
  const [shownStart, setShownStart] = useState<Date | null>(null);
  const [initialAttendees, setInitialAttendees] = useState("");

  const writable = useMemo(
    () =>
      calendars.filter(
        (c) => !c.myRights || c.myRights.mayWriteAll || c.myRights.mayWriteOwn,
      ),
    [calendars],
  );

  useEffect(() => {
    if (target.kind === "new") {
      const def =
        writable.find(
          (c) =>
            c.isDefault &&
            accounts.find((a) => a.id === c.accountId)?.isPersonal,
        ) ?? writable[0];
      setForm(
        blankForm(
          target.start,
          target.end,
          target.allDay,
          def ? calendarKey(def.accountId, def.id) : "",
        ),
      );
      return;
    }

    let cancelled = false;
    api
      .getEvent(target.accountId, target.eventId)
      .then((d) => {
        if (cancelled || !d) return;
        setDetail(d);
        const f = formFromDetail(
          d,
          target.recurrenceId ? target.occurrenceStart : undefined,
        );
        setForm(f);
        setInitialAttendees(f.attendees);
        setShownStart(
          parseLocalDateTime(
            f.allDay ? f.startDate : `${f.startDate}T${f.startTime}`,
          ),
        );
      })
      .catch((err) => {
        toast.error(errorMessage(err));
        onClose();
      });
    return () => {
      cancelled = true;
    };
    // 面板按 target 重新挂载, 这里只在首次运行。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const isEdit = target.kind === "edit";
  const recurrenceId = target.kind === "edit" ? target.recurrenceId : "";
  const recurring = !!detail?.recurrence;
  const readOnly = !!detail?.readOnly;
  const complexRule = !!detail?.recurrence?.complex;
  // 只有组织者(或新建)能编辑参加者; 受邀者改动参加者会被服务器拒绝。
  const canEditAttendees =
    !isEdit || !!detail?.isOrganizer || !(detail?.participants ?? []).length;

  function update<K extends keyof Form>(key: K, value: Form[K]) {
    setForm((f) => (f ? { ...f, [key]: value } : f));
  }

  async function save(scope: Scope) {
    if (!form) return;
    const [accountId, calendarId] = form.calendar.split("/");
    const start = form.allDay
      ? parseLocalDateTime(form.startDate)
      : parseLocalDateTime(`${form.startDate}T${form.startTime}`);
    let end = form.allDay
      ? addDays(parseLocalDateTime(form.endDate), 1)
      : parseLocalDateTime(`${form.endDate}T${form.endTime}`);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime())) {
      toast.error("请填写有效的开始与结束时间");
      return;
    }
    if (end < start) end = start;

    let sendStart = start;
    let sendEnd = end;
    if (scope === "all" && recurrenceId && detail && shownStart) {
      const shift = start.getTime() - shownStart.getTime();
      const seriesStart = parseLocalDateTime(detail.start);
      sendStart = new Date(seriesStart.getTime() + shift);
      sendEnd = new Date(
        sendStart.getTime() + (end.getTime() - start.getTime()),
      );
    }

    const recurrence =
      form.frequency === "none"
        ? undefined
        : main.Recurrence.createFrom({
            frequency: form.frequency,
            interval: Math.max(1, form.interval || 1),
            count: form.endMode === "count" ? Math.max(1, form.count) : 0,
            until:
              form.endMode === "until" && form.until
                ? `${form.until}T23:59:59`
                : "",
            byDay: form.frequency === "weekly" ? form.byDay : [],
            complex: complexRule,
          });

    // 参加者没改时传 null: 重写参加者会把他们已有的回复状态清掉。
    const attendeesChanged = form.attendees.trim() !== initialAttendees.trim();
    const attendees =
      canEditAttendees && attendeesChanged
        ? parseAddresses(form.attendees).map((a) => ({
            name: a.name,
            email: a.email,
          }))
        : null;
    if (attendees && attendees.length > 0 && !confirmInvite(isEdit)) return;

    setBusy(true);
    try {
      await api.saveEvent(
        main.EventInput.createFrom({
          accountId: isEdit ? detail!.accountId : accountId,
          id: isEdit ? detail!.id : "",
          calendarId:
            isEdit && accountId !== detail!.accountId
              ? detail!.calendarId
              : calendarId,
          title: form.title,
          description: form.description,
          location: form.location,
          virtualLocation: form.virtualLocation.trim(),
          start: toLocalDateTime(sendStart),
          end: toLocalDateTime(sendEnd),
          allDay: form.allDay,
          timeZone: form.allDay ? "" : detail?.timeZone || "",
          recurrence,
          alertMinutes: [...new Set(form.alerts)],
          scope,
          recurrenceId: scope === "this" ? recurrenceId : "",
          attendees,
        }),
      );
      toast.success(isEdit ? "已保存" : "已创建日程");
      onSaved();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(scope: Scope) {
    if (!detail) return;
    setBusy(true);
    try {
      await api.deleteEvent(detail.accountId, detail.id, recurrenceId, scope);
      toast.success("已删除");
      onSaved();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function respond(status: "accepted" | "tentative" | "declined") {
    if (!detail) return;
    try {
      await api.respondEvent(detail.accountId, detail.id, status);
      setDetail(main.EventDetail.createFrom({ ...detail, myStatus: status }));
      toast.success(
        {
          accepted: "已接受邀请",
          tentative: "已回复暂定",
          declined: "已拒绝邀请",
        }[status],
      );
      onSaved();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  function onSubmit() {
    if (!form?.title.trim()) {
      toast.error("请填写标题");
      return;
    }
    if (isEdit && recurring && recurrenceId) setAskScope("save");
    else void save("all");
  }

  function onDelete() {
    if (isEdit && recurring && recurrenceId) setAskScope("delete");
    else void remove("all");
  }

  const accountName = (id: string) =>
    accounts.find((a) => a.id === id)?.name ?? id;
  const groups = groupCalendars(
    isEdit && detail
      ? writable.filter((c) => c.accountId === detail.accountId)
      : writable,
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
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
        <h2 className="flex-1 truncate text-base font-semibold">
          {isEdit ? (readOnly ? "日程详情" : "编辑日程") : "新建日程"}
        </h2>
        {isEdit && !readOnly && (
          <Button
            variant="ghost"
            size="icon"
            className="hover:text-destructive size-8"
            aria-label="删除"
            onClick={onDelete}
            disabled={busy || !form}
          >
            <Trash2 className="size-4" />
          </Button>
        )}
      </header>

      {!form ? (
        <div className="text-muted-foreground flex flex-1 items-center justify-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          载入中
        </div>
      ) : (
        <div className="czl-scroll min-h-0 flex-1 overflow-y-auto">
          {detail?.myStatus && (
            <div className="border-border bg-secondary flex flex-wrap items-center gap-2 border-b px-4 py-2.5 text-sm">
              <span className="text-muted-foreground mr-auto">
                {detail.organizer
                  ? `${detail.organizer} 邀请你参加`
                  : "你被邀请参加此日程"}
              </span>
              {(["accepted", "tentative", "declined"] as const).map((s) => (
                <Button
                  key={s}
                  size="sm"
                  variant={detail.myStatus === s ? "default" : "outline"}
                  className="h-7"
                  onClick={() => respond(s)}
                >
                  {detail.myStatus === s && <Check className="size-3.5" />}
                  {{ accepted: "接受", tentative: "暂定", declined: "拒绝" }[s]}
                </Button>
              ))}
            </div>
          )}

          <fieldset
            disabled={readOnly || busy}
            className="flex flex-col gap-4 p-4"
          >
            <Input
              autoFocus={!isEdit}
              placeholder="标题"
              value={form.title}
              onChange={(e) => update("title", e.target.value)}
              className="h-10 text-base font-medium"
            />

            <Row icon={AlignLeft}>
              <Textarea
                placeholder="描述"
                rows={3}
                value={form.description}
                onChange={(e) => update("description", e.target.value)}
              />
            </Row>

            <Row icon={MapPin}>
              <Input
                placeholder="地点"
                value={form.location}
                onChange={(e) => update("location", e.target.value)}
              />
            </Row>

            <Row icon={Video}>
              <div className="flex min-w-0 flex-1 items-center gap-2">
                <Input
                  placeholder="会议链接"
                  value={form.virtualLocation}
                  onChange={(e) => update("virtualLocation", e.target.value)}
                />
                {/^https?:\/\//i.test(form.virtualLocation.trim()) && (
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => BrowserOpenURL(form.virtualLocation.trim())}
                  >
                    加入
                  </Button>
                )}
              </div>
            </Row>

            <Row icon={Users}>
              <div className="flex flex-col gap-2">
                {canEditAttendees && !readOnly ? (
                  <RecipientInput
                    value={form.attendees}
                    onChange={(v) => update("attendees", v)}
                    placeholder="添加参加者"
                    label="参加者"
                  />
                ) : null}
                {detail && (detail.participants ?? []).length > 0 && (
                  <ul className="flex flex-col gap-1 text-sm">
                    {detail.participants.map((p) => (
                      <li
                        key={p.id || p.email}
                        className="flex items-center gap-2"
                      >
                        <span
                          className={cn(
                            "size-2 shrink-0 rounded-full",
                            statusDot(p.status),
                          )}
                        />
                        <span className="min-w-0 flex-1 truncate">
                          {p.name || p.email}
                        </span>
                        <span className="text-muted-foreground shrink-0 text-xs">
                          {p.role === "owner"
                            ? "组织者"
                            : participationLabel(p.status)}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
                {!canEditAttendees && !(detail?.participants ?? []).length && (
                  <span className="text-muted-foreground text-sm">
                    无参加者
                  </span>
                )}
              </div>
            </Row>

            <div className="border-border border-t" />

            <Row icon={Clock}>
              <div className="flex flex-col gap-2">
                <Label className="flex w-fit items-center gap-2 text-sm font-normal">
                  <Checkbox
                    checked={form.allDay}
                    onCheckedChange={(v) => update("allDay", v === true)}
                  />
                  全天
                </Label>
                <div className="grid grid-cols-[auto_1fr_auto] items-center gap-2">
                  <span className="text-muted-foreground text-sm">开始</span>
                  <Input
                    type="date"
                    value={form.startDate}
                    onChange={(e) => update("startDate", e.target.value)}
                  />
                  {!form.allDay ? (
                    <Input
                      type="time"
                      className="w-28"
                      value={form.startTime}
                      onChange={(e) => update("startTime", e.target.value)}
                    />
                  ) : (
                    <span />
                  )}
                  <span className="text-muted-foreground text-sm">结束</span>
                  <Input
                    type="date"
                    value={form.endDate}
                    onChange={(e) => update("endDate", e.target.value)}
                  />
                  {!form.allDay ? (
                    <Input
                      type="time"
                      className="w-28"
                      value={form.endTime}
                      onChange={(e) => update("endTime", e.target.value)}
                    />
                  ) : (
                    <span />
                  )}
                </div>
              </div>
            </Row>

            <Row icon={() => <span className="size-4" />}>
              <Select
                value={form.calendar}
                onValueChange={(v) => update("calendar", v)}
              >
                <SelectTrigger className="w-full">
                  <SelectValue placeholder="选择日历" />
                </SelectTrigger>
                <SelectContent>
                  {groups.map(([acc, list]) => (
                    <SelectGroup key={acc}>
                      <SelectLabel>{accountName(acc)}</SelectLabel>
                      {list.map((c) => (
                        <SelectItem
                          key={calendarKey(c.accountId, c.id)}
                          value={calendarKey(c.accountId, c.id)}
                        >
                          <span
                            className="size-2.5 rounded-full"
                            style={{
                              background: calendarColor(
                                calendars,
                                c.accountId,
                                c.id,
                              ),
                            }}
                          />
                          {c.name}
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  ))}
                </SelectContent>
              </Select>
            </Row>

            <Row icon={Repeat}>
              <div className="flex flex-col gap-2">
                {complexRule ? (
                  <p className="text-muted-foreground py-1.5 text-sm">
                    此日程使用了复杂的重复规则，保存时保持不变
                  </p>
                ) : (
                  <>
                    <div className="flex flex-wrap items-center gap-2">
                      <Select
                        value={form.frequency}
                        onValueChange={(v) => update("frequency", v)}
                      >
                        <SelectTrigger className="w-32">
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectItem value="none">不重复</SelectItem>
                          <SelectItem value="daily">每天</SelectItem>
                          <SelectItem value="weekly">每周</SelectItem>
                          <SelectItem value="monthly">每月</SelectItem>
                          <SelectItem value="yearly">每年</SelectItem>
                        </SelectContent>
                      </Select>
                      {form.frequency !== "none" && (
                        <>
                          <span className="text-muted-foreground text-sm">
                            每
                          </span>
                          <Input
                            type="number"
                            min={1}
                            className="w-16"
                            value={form.interval}
                            onChange={(e) =>
                              update("interval", Number(e.target.value))
                            }
                          />
                          <span className="text-muted-foreground text-sm">
                            {
                              {
                                daily: "天",
                                weekly: "周",
                                monthly: "月",
                                yearly: "年",
                              }[form.frequency]
                            }
                          </span>
                        </>
                      )}
                    </div>
                    {form.frequency === "weekly" && (
                      <div className="flex flex-wrap gap-1">
                        {DAY_CODES.map((code, i) => {
                          const on = form.byDay.includes(code);
                          return (
                            <Button
                              key={code}
                              type="button"
                              size="sm"
                              variant={on ? "default" : "outline"}
                              className="h-7 px-2"
                              onClick={() =>
                                update(
                                  "byDay",
                                  on
                                    ? form.byDay.filter((d) => d !== code)
                                    : [...form.byDay, code],
                                )
                              }
                            >
                              {WEEKDAYS[i].slice(1)}
                            </Button>
                          );
                        })}
                      </div>
                    )}
                    {form.frequency !== "none" && (
                      <div className="flex flex-wrap items-center gap-2">
                        <Select
                          value={form.endMode}
                          onValueChange={(v) =>
                            update("endMode", v as Form["endMode"])
                          }
                        >
                          <SelectTrigger className="w-32">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="never">永不结束</SelectItem>
                            <SelectItem value="count">重复次数</SelectItem>
                            <SelectItem value="until">结束日期</SelectItem>
                          </SelectContent>
                        </Select>
                        {form.endMode === "count" && (
                          <Input
                            type="number"
                            min={1}
                            className="w-20"
                            value={form.count}
                            onChange={(e) =>
                              update("count", Number(e.target.value))
                            }
                          />
                        )}
                        {form.endMode === "until" && (
                          <Input
                            type="date"
                            className="w-40"
                            value={form.until}
                            onChange={(e) => update("until", e.target.value)}
                          />
                        )}
                      </div>
                    )}
                  </>
                )}
              </div>
            </Row>

            <Row icon={Bell}>
              <div className="flex flex-col gap-2">
                {form.alerts.length === 0 && (
                  <span className="text-muted-foreground py-1.5 text-sm">
                    不提醒
                  </span>
                )}
                {form.alerts.map((m, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <Select
                      value={String(m)}
                      onValueChange={(v) =>
                        update(
                          "alerts",
                          form.alerts.map((x, j) => (j === i ? Number(v) : x)),
                        )
                      }
                    >
                      <SelectTrigger className="w-44">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {withCurrent(m).map((o) => (
                          <SelectItem key={o.value} value={String(o.value)}>
                            {o.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="size-8"
                      aria-label="移除提醒"
                      onClick={() =>
                        update(
                          "alerts",
                          form.alerts.filter((_, j) => j !== i),
                        )
                      }
                    >
                      <X className="size-4" />
                    </Button>
                  </div>
                ))}
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="text-accent self-start"
                  onClick={() =>
                    update("alerts", [...form.alerts, form.allDay ? 1440 : 15])
                  }
                >
                  <Plus className="size-4" />
                  添加提醒
                </Button>
              </div>
            </Row>
          </fieldset>
        </div>
      )}

      {!readOnly && (
        <footer className="border-border flex shrink-0 justify-end gap-2 border-t px-4 py-2.5">
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={onSubmit} disabled={busy || !form}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            保存
          </Button>
        </footer>
      )}

      <AlertDialog
        open={askScope !== null}
        onOpenChange={(open) => !open && setAskScope(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {askScope === "delete" ? "删除重复日程" : "修改重复日程"}
            </AlertDialogTitle>
            <AlertDialogDescription>
              这是一个重复日程，要应用到哪些次？
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <Button
              variant="outline"
              onClick={() => {
                const action = askScope;
                setAskScope(null);
                void (action === "delete" ? remove("this") : save("this"));
              }}
            >
              仅此一次
            </Button>
            <Button
              variant={askScope === "delete" ? "destructive" : "default"}
              onClick={() => {
                const action = askScope;
                setAskScope(null);
                void (action === "delete" ? remove("all") : save("all"));
              }}
            >
              所有日程
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function Row({
  icon: Icon,
  children,
}: {
  icon: React.ComponentType<{ className?: string }>;
  children: React.ReactNode;
}) {
  return (
    <div className="grid grid-cols-[20px_1fr] items-start gap-3">
      <Icon className="text-muted-foreground mt-2.5 size-4" />
      <div className="min-w-0">{children}</div>
    </div>
  );
}

function confirmInvite(isEdit: boolean): boolean {
  return window.confirm(
    isEdit
      ? "保存后会向参加者发送更新通知。继续？"
      : "保存后会向参加者发送邀请邮件。继续？",
  );
}

function withCurrent(m: number) {
  if (ALERT_OPTIONS.some((o) => o.value === m)) return ALERT_OPTIONS;
  const label =
    m % 1440 === 0
      ? `提前 ${m / 1440} 天`
      : m % 60 === 0
        ? `提前 ${m / 60} 小时`
        : `提前 ${m} 分钟`;
  return [...ALERT_OPTIONS, { value: m, label }].sort(
    (a, b) => a.value - b.value,
  );
}

function statusDot(status: string) {
  switch (status) {
    case "accepted":
      return "bg-chart-3";
    case "declined":
      return "bg-destructive";
    case "tentative":
      return "bg-chart-4";
    default:
      return "bg-muted-foreground/40";
  }
}

function blankForm(
  start: Date,
  end: Date,
  allDay: boolean,
  calendar: string,
): Form {
  const lastDay = allDay ? addDays(end, -1) : end;
  return {
    calendar,
    title: "",
    location: "",
    virtualLocation: "",
    description: "",
    attendees: "",
    allDay,
    startDate: toDateInput(start),
    startTime: toTimeInput(start),
    endDate: toDateInput(lastDay < start ? start : lastDay),
    endTime: toTimeInput(end),
    frequency: "none",
    interval: 1,
    byDay: [DAY_CODES[(start.getDay() + 6) % 7]],
    endMode: "never",
    count: 10,
    until: "",
    alerts: allDay ? [] : [15],
  };
}

/** 编辑重复事件的某一次时，表单显示那一次的时间而不是序列首次的时间。 */
function formFromDetail(d: EventDetail, occurrenceStart?: string): Form {
  let start = parseLocalDateTime(d.start);
  let end = parseLocalDateTime(d.end);
  if (occurrenceStart) {
    const occ = new Date(occurrenceStart);
    const duration = end.getTime() - start.getTime();
    start = occ;
    end = new Date(occ.getTime() + duration);
  }
  const lastDay = d.allDay ? addDays(end, -1) : end;
  const r = d.recurrence;
  const attendees = (d.participants ?? [])
    .filter((p) => p.role !== "owner" && p.email)
    .map((p) => (p.name ? `${p.name} <${p.email}>` : p.email))
    .join(", ");
  return {
    calendar: calendarKey(d.accountId, d.calendarId),
    title: d.title,
    location: d.location,
    virtualLocation: d.virtualLocation ?? "",
    description: d.description,
    attendees,
    allDay: d.allDay,
    startDate: toDateInput(start),
    startTime: toTimeInput(start),
    endDate: toDateInput(lastDay < start ? start : lastDay),
    endTime: toTimeInput(end),
    frequency: r?.frequency || "none",
    interval: r?.interval || 1,
    byDay: r?.byDay?.length ? r.byDay : [DAY_CODES[(start.getDay() + 6) % 7]],
    endMode: r?.count ? "count" : r?.until ? "until" : "never",
    count: r?.count || 10,
    until: r?.until ? r.until.slice(0, 10) : "",
    alerts: d.alertMinutes ?? [],
  };
}

function groupCalendars(list: Calendar[]): [string, Calendar[]][] {
  const map = new Map<string, Calendar[]>();
  for (const c of list) {
    map.set(c.accountId, [...(map.get(c.accountId) ?? []), c]);
  }
  return [...map.entries()];
}

function participationLabel(status: string): string {
  return (
    {
      accepted: "已接受",
      declined: "已拒绝",
      tentative: "暂定",
      "needs-action": "未回复",
      delegated: "已委托",
    }[status] ??
    status ??
    ""
  );
}
