import { locale, t } from "@/lib/i18n"

const dateTime = new Intl.DateTimeFormat(locale, {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
})
const dateOnly = new Intl.DateTimeFormat(locale, {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
})
const hourOnly = new Intl.DateTimeFormat(locale, {
  hour: "2-digit",
  minute: "2-digit",
})
const dayHour = new Intl.DateTimeFormat(locale, {
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
})
const num = new Intl.NumberFormat(locale)

export function formatDateTime(s: string | null | undefined): string {
  return s ? dateTime.format(new Date(s)) : t().common.emptyValue
}

export function formatDate(s: string | null | undefined): string {
  return s ? dateOnly.format(new Date(s)) : t().common.emptyValue
}

/** Axis label for an hourly point: time of day for a day, day + hour beyond. */
export function formatHour(s: string, long: boolean): string {
  return long ? dayHour.format(new Date(s)) : hourOnly.format(new Date(s))
}

export function formatNumber(n: number): string {
  return num.format(n)
}

export function formatMs(ms: number | null | undefined): string {
  if (ms == null) return t().common.emptyValue
  return ms >= 1000
    ? t().time.seconds((ms / 1000).toFixed(ms >= 10_000 ? 0 : 1))
    : t().time.millis(ms)
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ["KB", "MB", "GB", "TB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`
}

/** "3 分钟前"; now is passed in so the value is stable during a render. */
export function formatRelative(
  s: string | null | undefined,
  now: number
): string {
  const m = t()
  if (!s) return m.common.never
  const diff = Math.max(0, now - new Date(s).getTime()) / 1000
  if (diff < 60) return m.time.justNow
  if (diff < 3600) return m.time.minutesAgo(Math.floor(diff / 60))
  if (diff < 86400) return m.time.hoursAgo(Math.floor(diff / 3600))
  if (diff < 30 * 86400) return m.time.daysAgo(Math.floor(diff / 86400))
  return formatDate(s)
}

export function formatInterval(seconds: number): string {
  const m = t()
  if (seconds < 60) return m.time.everySeconds(seconds)
  if (seconds < 3600) return m.time.everyMinutes(Math.round(seconds / 60))
  return m.time.everyHours(Math.round(seconds / 3600))
}

export function formatDuration(ms: number): string {
  const mins = Math.floor(Math.max(0, ms) / 60000)
  return t().time.duration(
    Math.floor(mins / 1440),
    Math.floor((mins % 1440) / 60),
    mins % 60
  )
}

/** Change against the previous window as a signed percentage, or null without a base. */
export function percentChange(cur: number, prev: number): number | null {
  if (prev <= 0) return null
  return Math.round(((cur - prev) / prev) * 100)
}
