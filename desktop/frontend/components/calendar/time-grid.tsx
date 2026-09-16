"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import { EventChip } from "@/components/calendar/event-chip";
import { layoutWeek } from "@/components/calendar/month-view";
import {
  EVENT_DRAG_TYPE,
  coversDay,
  occEnd,
  occStart,
  occurrenceColor,
  occurrenceKey,
  type DragPayload,
} from "@/components/calendar/calendar-utils";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { WEEKDAYS, hm, sameDay, weekdayIndex } from "@/lib/date";
import type { Calendar, EventOccurrence } from "@/lib/api";

interface Props {
  days: Date[];
  events: EventOccurrence[];
  calendars: Calendar[];
  canWrite: (o: EventOccurrence) => boolean;
  onOpen: (o: EventOccurrence) => void;
  onCreate: (start: Date, allDay: boolean) => void;
  onDrop: (payload: DragPayload, target: Date, allDay: boolean) => void;
  onShowDay: (day: Date) => void;
}

const HOUR_HEIGHT = 48;
const SNAP_MINUTES = 15;

/** 周视图与日视图共用的时间网格。 */
export function TimeGrid({ days, events, calendars, canWrite, onOpen, onCreate, onDrop, onShowDay }: Props) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [now, setNow] = useState(() => new Date());
  const [dropSlot, setDropSlot] = useState("");

  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 60_000);
    return () => clearInterval(t);
  }, []);

  // 首次打开滚到早上 8 点附近，而不是半夜 0 点。
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: HOUR_HEIGHT * 7.5 });
  }, []);

  // 全天与跨天日程排成连续横条, 与月视图一致。
  const bars = useMemo(() => layoutWeek(days, events, true), [days, events]);
  const laneCount = bars.reduce((m, b) => Math.max(m, b.lane + 1), 0);
  const timed = useMemo(
    () => days.map((d) => layoutDay(events.filter((o) => !o.allDay && !spansDays(o) && coversDay(o, d)))),
    [days, events],
  );

  const cols = { gridTemplateColumns: `56px repeat(${days.length}, minmax(0, 1fr))` };

  function slotFromEvent(e: React.MouseEvent | React.DragEvent, day: Date): Date {
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const minutes = Math.max(0, Math.min(24 * 60 - SNAP_MINUTES, ((e.clientY - rect.top) / HOUR_HEIGHT) * 60));
    const snapped = Math.floor(minutes / SNAP_MINUTES) * SNAP_MINUTES;
    return new Date(day.getFullYear(), day.getMonth(), day.getDate(), 0, snapped);
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="border-border grid shrink-0 border-b" style={cols}>
        <div />
        {days.map((d) => (
          <button
            key={d.toDateString()}
            type="button"
            onClick={() => onShowDay(d)}
            className="hover:bg-secondary flex flex-col items-center py-1.5"
          >
            <span className="text-muted-foreground text-xs">{WEEKDAYS[weekdayIndex(d)]}</span>
            <span
              className={cn(
                "flex size-7 items-center justify-center rounded-full text-sm tabular-nums",
                sameDay(d, now) && "bg-primary text-primary-foreground font-medium",
              )}
            >
              {d.getDate()}
            </span>
          </button>
        ))}
      </div>

      <div className="border-border relative grid shrink-0 border-b" style={cols}>
        <div className="text-muted-foreground flex items-center justify-end pr-2 text-[11px]">全天</div>
        {days.map((d) => (
          <div
            key={d.toDateString()}
            onClick={() => onCreate(d, true)}
            onDragOver={(e) => e.dataTransfer.types.includes(EVENT_DRAG_TYPE) && e.preventDefault()}
            onDrop={(e) => {
              const raw = e.dataTransfer.getData(EVENT_DRAG_TYPE);
              if (raw) onDrop(JSON.parse(raw) as DragPayload, d, true);
            }}
            className="border-border border-l"
            style={{ height: Math.min(Math.max(laneCount, 1), 5) * 22 + 6 }}
          />
        ))}
        <div className="pointer-events-none absolute inset-y-0 right-0 left-14 overflow-y-auto">
          {bars.map((b) => (
            <div
              key={`${occurrenceKey(b.occ)}@${b.col}`}
              className="pointer-events-auto absolute"
              style={{
                top: 3 + b.lane * 22,
                left: `calc(${(b.col / days.length) * 100}% + 2px)`,
                width: `calc(${(b.span / days.length) * 100}% - 4px)`,
              }}
            >
              <EventChip occ={b.occ} color={occurrenceColor(calendars, b.occ)} draggable={canWrite(b.occ)} onOpen={onOpen} compact />
            </div>
          ))}
        </div>
      </div>

      <ScrollArea ref={scrollRef} className="min-h-0 flex-1">
        <div className="relative grid" style={{ ...cols, height: HOUR_HEIGHT * 24 }}>
          <div className="relative">
            {Array.from({ length: 24 }, (_, h) => (
              <span
                key={h}
                className="text-muted-foreground absolute right-2 -translate-y-1/2 text-[11px] tabular-nums"
                style={{ top: h * HOUR_HEIGHT }}
              >
                {h === 0 ? "" : `${String(h).padStart(2, "0")}:00`}
              </span>
            ))}
          </div>

          {days.map((d, i) => {
            const isToday = sameDay(d, now);
            return (
              <div
                key={d.toDateString()}
                className="border-border relative border-l"
                onClick={(e) => onCreate(slotFromEvent(e, d), false)}
                onDragOver={(e) => {
                  if (!e.dataTransfer.types.includes(EVENT_DRAG_TYPE)) return;
                  e.preventDefault();
                  setDropSlot(`${i}:${Math.floor(slotFromEvent(e, d).getTime() / 60000)}`);
                }}
                onDragLeave={() => setDropSlot("")}
                onDrop={(e) => {
                  setDropSlot("");
                  const raw = e.dataTransfer.getData(EVENT_DRAG_TYPE);
                  if (raw) onDrop(JSON.parse(raw) as DragPayload, slotFromEvent(e, d), false);
                }}
              >
                {Array.from({ length: 24 }, (_, h) => (
                  <div key={h} className="border-border absolute inset-x-0 border-t" style={{ top: h * HOUR_HEIGHT }} />
                ))}

                {dropSlot.startsWith(`${i}:`) && (
                  <div
                    className="bg-chart-5 pointer-events-none absolute inset-x-0.5 rounded-sm"
                    style={{
                      top: (((Number(dropSlot.split(":")[1]) * 60000 - d.getTime()) / 60000) * HOUR_HEIGHT) / 60,
                      height: HOUR_HEIGHT / 2,
                    }}
                  />
                )}

                {timed[i].map(({ occ, column, columns }) => {
                  const dayStart = new Date(d.getFullYear(), d.getMonth(), d.getDate());
                  const start = Math.max(0, (occStart(occ).getTime() - dayStart.getTime()) / 60000);
                  const end = Math.min(24 * 60, (occEnd(occ).getTime() - dayStart.getTime()) / 60000);
                  const height = Math.max(((end - start) / 60) * HOUR_HEIGHT, 20);
                  return (
                    <EventChip
                      key={occurrenceKey(occ)}
                      occ={occ}
                      color={occurrenceColor(calendars, occ)}
                      draggable={canWrite(occ)}
                      onOpen={onOpen}
                      className="absolute flex-col items-stretch gap-0"
                      style={{
                        top: (start / 60) * HOUR_HEIGHT,
                        height,
                        left: `calc(${(column / columns) * 100}% + 2px)`,
                        width: `calc(${100 / columns}% - 4px)`,
                      }}
                    />
                  );
                })}

                {isToday && (
                  <div
                    className="bg-destructive pointer-events-none absolute inset-x-0 z-10 h-0.5"
                    style={{ top: ((now.getHours() * 60 + now.getMinutes()) / 60) * HOUR_HEIGHT }}
                  >
                    <span className="bg-destructive absolute -top-1 -left-1 size-2.5 rounded-full" />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </ScrollArea>

      <p className="sr-only">当前时间 {hm(now)}</p>
    </div>
  );
}

/** 跨越多个日界的定时事件放到全天行，时间格里放不下。 */
function spansDays(o: EventOccurrence): boolean {
  // 结束时间减 1ms: 恰好在次日零点结束的日程仍算当天。
  return !sameDay(occStart(o), new Date(occEnd(o).getTime() - 1));
}

interface Placed {
  occ: EventOccurrence;
  column: number;
  columns: number;
}

/** 重叠的日程并排显示：贪心分列，同一重叠簇内共享列数。 */
function layoutDay(list: EventOccurrence[]): Placed[] {
  const sorted = [...list].sort((a, b) => +occStart(a) - +occStart(b) || +occEnd(b) - +occEnd(a));
  const placed: Placed[] = [];
  let cluster: Placed[] = [];
  let clusterEnd = 0;
  let columnEnds: number[] = [];

  const flush = () => {
    const columns = Math.max(1, columnEnds.length);
    for (const p of cluster) p.columns = columns;
    placed.push(...cluster);
    cluster = [];
    columnEnds = [];
  };

  for (const occ of sorted) {
    const s = +occStart(occ);
    const e = +occEnd(occ);
    if (cluster.length && s >= clusterEnd) flush();
    let column = columnEnds.findIndex((end) => end <= s);
    if (column === -1) {
      column = columnEnds.length;
      columnEnds.push(e);
    } else {
      columnEnds[column] = e;
    }
    cluster.push({ occ, column, columns: 1 });
    clusterEnd = Math.max(clusterEnd, e);
  }
  flush();
  return placed;
}
