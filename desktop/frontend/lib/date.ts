/**
 * 日历视图用的日期工具。全部按本机时区计算，周一为一周第一天（国内习惯）。
 *
 * 不引入日期库：用到的只有加减天、取月格、格式化几种操作，Date 足够。
 */

export const WEEKDAYS = ["周一", "周二", "周三", "周四", "周五", "周六", "周日"];

export function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

export function addDays(d: Date, n: number): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n, d.getHours(), d.getMinutes());
}

export function addMonths(d: Date, n: number): Date {
  // 月末日期加月时钳到目标月最后一天，避免 1 月 31 日加一个月跳到 3 月。
  const target = new Date(d.getFullYear(), d.getMonth() + n, 1);
  const last = new Date(target.getFullYear(), target.getMonth() + 1, 0).getDate();
  return new Date(target.getFullYear(), target.getMonth(), Math.min(d.getDate(), last));
}

/** 周一为 0 的星期序号。 */
export function weekdayIndex(d: Date): number {
  return (d.getDay() + 6) % 7;
}

export function startOfWeek(d: Date): Date {
  return addDays(startOfDay(d), -weekdayIndex(d));
}

export function startOfMonth(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), 1);
}

export function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

/** 月视图的 6×7 格，从包含本月 1 日的那一周的周一开始。 */
export function monthGrid(d: Date): Date[] {
  const first = startOfWeek(startOfMonth(d));
  return Array.from({ length: 42 }, (_, i) => addDays(first, i));
}

const pad = (n: number) => String(n).padStart(2, "0");

/** JSCalendar 的 LocalDateTime：本地时间、无时区后缀。 */
export function toLocalDateTime(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:00`;
}

export function parseLocalDateTime(s: string): Date {
  const m = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2})(?::(\d{2}))?)?/.exec(s);
  if (!m) return new Date(NaN);
  return new Date(+m[1], +m[2] - 1, +m[3], +(m[4] ?? 0), +(m[5] ?? 0), +(m[6] ?? 0));
}

/** RFC 3339（带本机偏移），传给后端做区间查询。 */
export function toRFC3339(d: Date): string {
  const offset = -d.getTimezoneOffset();
  const sign = offset >= 0 ? "+" : "-";
  const abs = Math.abs(offset);
  return `${toLocalDateTime(d).slice(0, 19)}${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`;
}

/** input[type=date] 的值。 */
export function toDateInput(d: Date): string {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** input[type=time] 的值。 */
export function toTimeInput(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function hm(d: Date): string {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function monthTitle(d: Date): string {
  return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月`;
}

export function dayTitle(d: Date): string {
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日 ${WEEKDAYS[weekdayIndex(d)]}`;
}

export function shortDate(d: Date): string {
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}
