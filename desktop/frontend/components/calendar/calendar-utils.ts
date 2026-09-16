import type { Calendar, EventOccurrence } from "@/lib/api";

/** 日历的显示色。服务器给了颜色就用（那是用户在任意客户端里选的数据），否则按序取主题 token。 */
export function calendarColor(calendars: Calendar[], accountId: string, calendarId: string): string {
  const index = calendars.findIndex((c) => c.accountId === accountId && c.id === calendarId);
  const cal = calendars[index];
  if (cal?.color && /^#[0-9a-f]{3,8}$/i.test(cal.color)) return cal.color;
  return `var(--event-${(Math.max(index, 0) % 6) + 1})`;
}

export function occurrenceColor(calendars: Calendar[], o: EventOccurrence): string {
  return calendarColor(calendars, o.accountId, o.calendarIds?.[0] ?? "");
}

export function calendarKey(accountId: string, calendarId: string): string {
  return `${accountId}/${calendarId}`;
}

/** 发生次的唯一键：同一事件的不同次以 recurrenceId 区分。 */
export function occurrenceKey(o: EventOccurrence): string {
  return `${o.accountId}/${o.eventId}/${o.recurrenceId}`;
}

export function occStart(o: EventOccurrence): Date {
  return new Date(o.start);
}

export function occEnd(o: EventOccurrence): Date {
  const end = new Date(o.end);
  const start = new Date(o.start);
  return end > start ? end : new Date(start.getTime() + 30 * 60 * 1000);
}

/** 发生次是否覆盖某一天（按本机日界）。全天事件的结束是次日零点，不算次日。 */
export function coversDay(o: EventOccurrence, day: Date): boolean {
  const dayStart = new Date(day.getFullYear(), day.getMonth(), day.getDate());
  const dayEnd = new Date(day.getFullYear(), day.getMonth(), day.getDate() + 1);
  const start = occStart(o);
  const end = o.allDay ? new Date(o.end) : occEnd(o);
  return start < dayEnd && end > dayStart;
}

export type CalendarView = "month" | "week" | "day" | "agenda" | "tasks";

/** 拖放时携带的数据类型，避免与外部拖入的文件混淆。 */
export const EVENT_DRAG_TYPE = "application/x-czlmail-event";

export interface DragPayload {
  accountId: string;
  eventId: string;
  recurrenceId: string;
  start: string;
  allDay: boolean;
}
