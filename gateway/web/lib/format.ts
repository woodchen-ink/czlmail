import { currentLocale, t, type Locale } from "@/lib/i18n"

// Intl formatters per interface language, created once.
const cache = new Map<Locale, ReturnType<typeof make>>()

function make(locale: Locale) {
  const tag = locale === "zh-CN" ? "zh-CN" : "en-US"
  return {
    dateTime: new Intl.DateTimeFormat(tag, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
    }),
    date: new Intl.DateTimeFormat(tag, {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }),
    time: new Intl.DateTimeFormat(tag, { hour: "2-digit", minute: "2-digit" }),
    dayHour: new Intl.DateTimeFormat(tag, {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
    }),
    number: new Intl.NumberFormat(tag),
  }
}

function f() {
  const locale = currentLocale()
  let v = cache.get(locale)
  if (!v) {
    v = make(locale)
    cache.set(locale, v)
  }
  return v
}

export function formatDateTime(s: string | null | undefined): string {
  return s ? f().dateTime.format(new Date(s)) : t().common.emptyValue
}

export function formatDate(s: string | null | undefined): string {
  return s ? f().date.format(new Date(s)) : t().common.emptyValue
}

/** Axis label for an hourly point: time of day for a day, day + hour beyond. */
export function formatHour(s: string, long: boolean): string {
  return long ? f().dayHour.format(new Date(s)) : f().time.format(new Date(s))
}

export function formatNumber(n: number): string {
  return f().number.format(n)
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

/** "3 min ago"; now is passed in so the value is stable during a render. */
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
