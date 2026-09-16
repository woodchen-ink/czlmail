import type { Address } from "@/lib/api";

/**
 * 邮件列表的时间列，始终带时刻。
 *
 * 今天只显示时刻，今年省去年份，更早的补上年份。日期与时刻都用固定位数，
 * 列表里上下行的时间能按列对齐。
 */
export function listDate(value: string | Date): string {
  const d = toDate(value);
  if (!d) return "";

  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  const sameDay =
    d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();

  if (sameDay) return time;
  const date = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
  if (d.getFullYear() === now.getFullYear()) return `${date} ${time}`;
  return `${d.getFullYear()}-${date} ${time}`;
}

/** 阅读窗格的完整时间。 */
export function fullDate(value: string | Date): string {
  const d = toDate(value);
  if (!d) return "";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    weekday: "short",
    hour12: false,
  }).format(d);
}

/**
 * Go 的 time.Time 经 JSON 过来是 RFC 3339 字符串。
 * 零值时间（0001-01-01）表示「没有这个时间」，不该显示成 1 年 1 月。
 */
function toDate(value: string | Date): Date | null {
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return null;
  if (d.getFullYear() < 1970) return null;
  return d;
}

export function fileSize(bytes: number): string {
  if (!bytes) return "";
  const units = ["B", "KB", "MB", "GB"];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i += 1;
  }
  return `${n < 10 && i > 0 ? n.toFixed(1) : Math.round(n)} ${units[i]}`;
}

/** 显示名优先，退回地址。 */
export function displayAddress(a: Address | undefined): string {
  if (!a) return "";
  return a.name || a.email || "";
}

export function displayAddressList(list: Address[] | undefined): string {
  if (!list || list.length === 0) return "";
  return list.map(displayAddress).filter(Boolean).join("、");
}

/** 头像回退用的首字母。中文取首字，英文取首字母。 */
export function initials(name: string): string {
  const s = name.trim();
  if (!s) return "?";
  return [...s][0].toUpperCase();
}
