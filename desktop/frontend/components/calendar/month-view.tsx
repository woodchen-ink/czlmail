"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import { EventChip } from "@/components/calendar/event-chip";
import {
  EVENT_DRAG_TYPE,
  occEnd,
  occStart,
  occurrenceColor,
  occurrenceKey,
  type DragPayload,
} from "@/components/calendar/calendar-utils";
import { cn } from "@/lib/utils";
import { WEEKDAYS, addDays, monthGrid, sameDay } from "@/lib/date";
import type { Calendar, EventOccurrence } from "@/lib/api";

interface Props {
  cursor: Date;
  events: EventOccurrence[];
  calendars: Calendar[];
  canWrite: (o: EventOccurrence) => boolean;
  onOpen: (o: EventOccurrence) => void;
  onCreate: (day: Date) => void;
  onDrop: (payload: DragPayload, day: Date) => void;
  onShowDay: (day: Date) => void;
}

const HEADER = 28; // 日期数字所占高度
const LANE = 22; // 每条日程的行高

export interface Segment {
  occ: EventOccurrence;
  col: number; // 0-6
  span: number; // 1-7
  lane: number;
  /** 在本周之前/之后还有延续, 这一侧不画圆角。 */
  continuesBefore: boolean;
  continuesAfter: boolean;
  bar: boolean;
}

/**
 * 月视图按周分行布局。
 *
 * 跨多天的日程(以及全天日程)画成横跨多列的连续色条, 与 Bulwark 和多数日历一致;
 * 每周先给跨天条分配行, 再把当天的定时日程排进空行。放不下的折叠成「还有 N 项」。
 */
export function MonthView({
  cursor,
  events,
  calendars,
  canWrite,
  onOpen,
  onCreate,
  onDrop,
  onShowDay,
}: Props) {
  const days = useMemo(() => monthGrid(cursor), [cursor]);
  const weeks = useMemo(
    () => Array.from({ length: 6 }, (_, i) => days.slice(i * 7, i * 7 + 7)),
    [days],
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-border grid shrink-0 grid-cols-7 border-b">
        {WEEKDAYS.map((w) => (
          <div
            key={w}
            className="text-muted-foreground py-2 text-center text-xs"
          >
            {w}
          </div>
        ))}
      </div>

      <div className="grid min-h-0 flex-1 grid-rows-6">
        {weeks.map((week) => (
          <WeekRow
            key={week[0].toDateString()}
            week={week}
            month={cursor.getMonth()}
            events={events}
            calendars={calendars}
            canWrite={canWrite}
            onOpen={onOpen}
            onCreate={onCreate}
            onDrop={onDrop}
            onShowDay={onShowDay}
          />
        ))}
      </div>
    </div>
  );
}

function WeekRow({
  week,
  month,
  events,
  calendars,
  canWrite,
  onOpen,
  onCreate,
  onDrop,
  onShowDay,
}: Omit<Props, "cursor"> & { week: Date[]; month: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [height, setHeight] = useState(120);
  const [dropCol, setDropCol] = useState(-1);
  const today = new Date();

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) =>
      setHeight(entry.contentRect.height),
    );
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const segments = useMemo(() => layoutWeek(week, events), [week, events]);
  const maxLanes = Math.max(1, Math.floor((height - HEADER - 2) / LANE));

  // 每列被占用到的最大行, 以及放不下的数量。
  const { visible, hiddenPerCol } = useMemo(() => {
    const laneCount = Array(7).fill(0) as number[];
    for (const s of segments)
      for (let c = s.col; c < s.col + s.span; c++)
        laneCount[c] = Math.max(laneCount[c], s.lane + 1);
    const hidden = Array(7).fill(0) as number[];
    const shown: Segment[] = [];
    for (const s of segments) {
      // 这一段覆盖的列里只要有一列溢出, 最后一行就要留给「还有 N 项」。
      const overflow = Array.from(
        { length: s.span },
        (_, i) => laneCount[s.col + i] > maxLanes,
      ).some(Boolean);
      const limit = overflow ? maxLanes - 1 : maxLanes;
      if (s.lane < limit) {
        shown.push(s);
      } else {
        for (let c = s.col; c < s.col + s.span; c++) hidden[c] += 1;
      }
    }
    return { visible: shown, hiddenPerCol: hidden };
  }, [segments, maxLanes]);

  return (
    <div
      ref={ref}
      className="border-border relative min-h-0 border-b last:border-b-0"
    >
      <div className="absolute inset-0 grid grid-cols-7">
        {week.map((d, col) => {
          const inMonth = d.getMonth() === month;
          return (
            <div
              key={col}
              onClick={() => onCreate(d)}
              onDragOver={(e) => {
                if (!e.dataTransfer.types.includes(EVENT_DRAG_TYPE)) return;
                e.preventDefault();
                setDropCol(col);
              }}
              onDragLeave={() => setDropCol((c) => (c === col ? -1 : c))}
              onDrop={(e) => {
                setDropCol(-1);
                const raw = e.dataTransfer.getData(EVENT_DRAG_TYPE);
                if (raw) onDrop(JSON.parse(raw) as DragPayload, d);
              }}
              className={cn(
                "border-border flex min-w-0 cursor-default justify-center border-r pt-1",
                col === 6 && "border-r-0",
                !inMonth && "bg-secondary/40",
                dropCol === col && "bg-chart-5",
              )}
            >
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onShowDay(d);
                }}
                className={cn(
                  "flex size-6 items-center justify-center rounded-full text-xs tabular-nums hover:underline",
                  !inMonth && "text-muted-foreground",
                  sameDay(d, today) &&
                    "bg-primary text-primary-foreground font-medium hover:no-underline",
                )}
              >
                {d.getDate()}
              </button>
            </div>
          );
        })}
      </div>

      {visible.map((s) => {
        const style: React.CSSProperties = {
          position: "absolute",
          top: HEADER + s.lane * LANE,
          left: `calc(${(s.col / 7) * 100}% + ${s.continuesBefore ? 0 : 3}px)`,
          width: `calc(${(s.span / 7) * 100}% - ${(s.continuesBefore ? 0 : 3) + (s.continuesAfter ? 0 : 3)}px)`,
        };
        if (!s.bar) {
          return (
            <div key={`${occurrenceKey(s.occ)}@${s.col}`} style={style}>
              <EventChip
                occ={s.occ}
                color={occurrenceColor(calendars, s.occ)}
                draggable={canWrite(s.occ)}
                onOpen={onOpen}
                compact
              />
            </div>
          );
        }
        const color = occurrenceColor(calendars, s.occ);
        return (
          <button
            key={`${occurrenceKey(s.occ)}@${s.col}`}
            type="button"
            draggable={canWrite(s.occ)}
            onDragStart={(e) => {
              const payload: DragPayload = {
                accountId: s.occ.accountId,
                eventId: s.occ.eventId,
                recurrenceId: s.occ.recurrenceId,
                start: s.occ.start,
                allDay: s.occ.allDay,
              };
              e.dataTransfer.setData(EVENT_DRAG_TYPE, JSON.stringify(payload));
              e.dataTransfer.effectAllowed = "move";
            }}
            onClick={(e) => {
              e.stopPropagation();
              onOpen(s.occ);
            }}
            title={s.occ.title}
            className={cn(
              "flex h-5 min-w-0 items-center overflow-hidden px-1.5 text-left text-xs font-medium",
              "hover:brightness-95 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none dark:hover:brightness-110",
              !s.continuesBefore && "rounded-l-sm",
              !s.continuesAfter && "rounded-r-sm",
              s.occ.status === "cancelled" && "line-through opacity-60",
            )}
            style={{
              ...style,
              background: `color-mix(in srgb, ${color} 28%, var(--card))`,
              boxShadow: s.continuesBefore
                ? undefined
                : `inset 3px 0 0 ${color}`,
            }}
          >
            <span className="truncate">
              {s.continuesBefore ? "← " : ""}
              {s.occ.title || "(无标题)"}
            </span>
          </button>
        );
      })}

      {hiddenPerCol.map((n, col) =>
        n > 0 ? (
          <button
            key={`more${col}`}
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onShowDay(week[col]);
            }}
            className="text-muted-foreground hover:text-foreground absolute truncate px-1.5 text-left text-xs"
            style={{
              top: HEADER + (maxLanes - 1) * LANE + 2,
              left: `${(col / 7) * 100}%`,
              width: `${100 / 7}%`,
            }}
          >
            还有 {n} 项
          </button>
        ) : null,
      )}
    </div>
  );
}

/** 为一周内的日程分配列与行。 */
export function layoutWeek(week: Date[], events: EventOccurrence[], barsOnly = false): Segment[] {
  const n = week.length;
  const weekStart = week[0];
  const weekEnd = addDays(weekStart, n);
  const dayIndex = (d: Date) =>
    Math.round(
      (new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() -
        weekStart.getTime()) /
        86400000,
    );

  const items: Omit<Segment, "lane">[] = [];
  for (const o of events) {
    const start = occStart(o);
    // 全天事件的结束是次日零点; 定时事件结束在零点时也不算占用那一天。
    const endExclusive = o.allDay ? new Date(o.end) : occEnd(o);
    if (!(start < weekEnd && endExclusive > weekStart)) continue;
    const lastMoment = new Date(
      Math.max(start.getTime(), endExclusive.getTime() - 1),
    );
    const firstCol = dayIndex(start);
    const lastCol = dayIndex(lastMoment);
    const col = Math.max(0, firstCol);
    const end = Math.min(n - 1, lastCol);
    if (end < col) continue;
    const multiDay = lastCol > firstCol;
    if (barsOnly && !(o.allDay || multiDay)) continue;
    items.push({
      occ: o,
      col,
      span: end - col + 1,
      continuesBefore: firstCol < 0,
      continuesAfter: lastCol > n - 1,
      bar: o.allDay || multiDay,
    });
  }

  // 跨天条优先且越长越靠上, 然后是单天全天, 再按开始时间。
  items.sort(
    (a, b) =>
      Number(b.bar) - Number(a.bar) ||
      b.span - a.span ||
      a.col - b.col ||
      +new Date(a.occ.start) - +new Date(b.occ.start) ||
      (a.occ.title || "").localeCompare(b.occ.title || ""),
  );

  const occupied: boolean[][] = [];
  const out: Segment[] = [];
  for (const it of items) {
    let lane = 0;
    for (;;) {
      const row = (occupied[lane] ??= Array(n).fill(false));
      let free = true;
      for (let c = it.col; c < it.col + it.span; c++) if (row[c]) free = false;
      if (free) {
        for (let c = it.col; c < it.col + it.span; c++) row[c] = true;
        break;
      }
      lane++;
    }
    out.push({ ...it, lane });
  }
  return out;
}
