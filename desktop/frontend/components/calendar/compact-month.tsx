"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useMemo, useRef } from "react";
import { CalendarDays, FileText, MapPin, NotebookPen } from "lucide-react";

import { coversDay, occEnd, occStart, occurrenceColor, occurrenceKey } from "@/components/calendar/calendar-utils";
import { localDay, requestOpenNote, type NoteDayItem } from "@/lib/notes/api";
import { cn } from "@/lib/utils";
import { dayTitle, getWeekdays, hm, monthGrid, sameDay } from "@/lib/date";
import type { Calendar, EventOccurrence } from "@/lib/api";

interface Props {
  /** 选中的那天, 同时决定显示哪个月。 */
  cursor: Date;
  events: EventOccurrence[];
  calendars: Calendar[];
  notes?: Map<string, NoteDayItem[]>;
  onSelect: (day: Date) => void;
  /** 左右滑动网格换月。 */
  onStep: (dir: 1 | -1) => void;
  onOpen: (o: EventOccurrence) => void;
}

const MAX_DOTS = 3;

/**
 * 窄屏月视图 (手机日历的常见做法): 网格只画日期与彩点, 选中某天后在下方列出当天的日程与笔记。
 * 一格只有约 50px 宽, 放日程条连时间都显示不全。
 */
export function CompactMonth({ cursor, events, calendars, notes, onSelect, onStep, onOpen }: Props) {
  useI18n();
  const days = useMemo(() => monthGrid(cursor), [cursor]);
  const today = new Date();

  const perDay = useMemo(
    () => days.map((d) => events.filter((o) => coversDay(o, d))),
    [days, events],
  );
  const selected = useMemo(
    () =>
      events
        .filter((o) => coversDay(o, cursor))
        .sort((a, b) => Number(b.allDay) - Number(a.allDay) || +occStart(a) - +occStart(b)),
    [events, cursor],
  );
  const dayNotes = notes?.get(localDay(cursor)) ?? [];

  // 横向滑动换月; 从屏幕左边缘起手的留给拉出侧栏。
  const swipe = useRef<{ x: number; y: number } | null>(null);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div
        className="border-border shrink-0 border-b select-none"
        onTouchStart={(e) => {
          const p = e.touches[0];
          swipe.current = p.clientX > 30 ? { x: p.clientX, y: p.clientY } : null;
        }}
        onTouchEnd={(e) => {
          const s = swipe.current;
          swipe.current = null;
          if (!s) return;
          const p = e.changedTouches[0];
          const dx = p.clientX - s.x;
          if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(p.clientY - s.y) * 1.5) onStep(dx < 0 ? 1 : -1);
        }}
      >
        <div className="grid grid-cols-7">
          {getWeekdays().map((w) => (
            <div key={w} className="text-muted-foreground py-1.5 text-center text-xs">
              {w.replace(/^周/, "")}
            </div>
          ))}
        </div>
        <div className="grid grid-cols-7 pb-1">
          {days.map((d, i) => {
            const inMonth = d.getMonth() === cursor.getMonth();
            const isSel = sameDay(d, cursor);
            const isToday = sameDay(d, today);
            const list = perDay[i];
            const hasNotes = !!notes?.get(localDay(d))?.length;
            return (
              <button
                key={d.toDateString()}
                type="button"
                onClick={() => onSelect(d)}
                className="flex h-12 flex-col items-center gap-1 pt-1"
              >
                <span
                  className={cn(
                    "flex size-7 items-center justify-center rounded-full text-sm tabular-nums",
                    !inMonth && "text-muted-foreground/60",
                    isToday && !isSel && "text-primary font-semibold",
                    isSel && (isToday ? "bg-primary text-primary-foreground" : "bg-foreground text-background"),
                    isSel && "font-semibold",
                  )}
                >
                  {d.getDate()}
                </span>
                <span className="flex h-1.5 items-center gap-0.5">
                  {list.slice(0, MAX_DOTS).map((o) => (
                    <span
                      key={occurrenceKey(o)}
                      className={cn("size-1.5 rounded-full", !inMonth && "opacity-50")}
                      style={{ background: occurrenceColor(calendars, o) }}
                    />
                  ))}
                  {hasNotes && list.length < MAX_DOTS && <span className="bg-muted-foreground/50 size-1.5 rounded-full" />}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="px-4 pt-3 pb-1">
          <p className="text-sm font-medium">
            {dayTitle(cursor)}
            {sameDay(cursor, today) && <span className="text-muted-foreground ml-2 text-xs font-normal">{t("今天")}</span>}
          </p>
        </div>

        {selected.length === 0 && dayNotes.length === 0 && (
          <div className="text-muted-foreground flex flex-col items-center gap-2 py-10 text-sm">
            <CalendarDays className="size-7" />
            {t("这天没有日程")}
          </div>
        )}

        <ul className="flex flex-col gap-1.5 px-3 pb-24">
          {selected.map((o) => (
            <li key={occurrenceKey(o)}>
              <button
                type="button"
                onClick={() => onOpen(o)}
                className={cn(
                  "active:bg-secondary flex w-full items-stretch gap-3 rounded-md px-2 py-2 text-left",
                  o.status === "cancelled" && "line-through opacity-60",
                )}
              >
                <span className="w-1 shrink-0 rounded-full" style={{ background: occurrenceColor(calendars, o) }} />
                <span className="text-muted-foreground flex w-12 shrink-0 flex-col text-xs tabular-nums">
                  {o.allDay || !sameDay(occStart(o), cursor) ? (
                    <span>{t("全天")}</span>
                  ) : (
                    <>
                      <span className="text-foreground">{hm(occStart(o))}</span>
                      {/* 跨天的日程在这天结束不了, 不写结束时间以免看成当天就结束。 */}
                      <span>{sameDay(occEnd(o), cursor) ? hm(occEnd(o)) : "→"}</span>
                    </>
                  )}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{o.title || t("(无标题)")}</span>
                  {o.location && (
                    <span className="text-muted-foreground flex items-center gap-1 truncate text-xs">
                      <MapPin className="size-3 shrink-0" />
                      {o.location}
                    </span>
                  )}
                </span>
              </button>
            </li>
          ))}
          {dayNotes.map((n) => {
            const Icon = n.journal ? NotebookPen : FileText;
            return (
              <li key={n.id}>
                <button
                  type="button"
                  onClick={() => requestOpenNote(n)}
                  className="active:bg-secondary flex w-full items-center gap-3 rounded-md px-2 py-2 text-left"
                >
                  <span className="bg-muted-foreground/40 w-1 shrink-0 self-stretch rounded-full" />
                  <Icon className="text-muted-foreground size-4 shrink-0" />
                  <span className="min-w-0 flex-1 truncate text-sm">{n.name.replace(/\.md$/i, "")}</span>
                  <span className="text-muted-foreground shrink-0 text-xs">{n.journal ? t("日记") : t("笔记")}</span>
                </button>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
