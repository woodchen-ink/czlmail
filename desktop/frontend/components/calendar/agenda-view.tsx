"use client";

import { useMemo } from "react";
import { CalendarDays, MapPin } from "lucide-react";

import { occEnd, occStart, occurrenceColor, occurrenceKey } from "@/components/calendar/calendar-utils";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { dayTitle, hm, sameDay, startOfDay, toDateInput } from "@/lib/date";
import type { Calendar, EventOccurrence } from "@/lib/api";

interface Props {
  events: EventOccurrence[];
  calendars: Calendar[];
  onOpen: (o: EventOccurrence) => void;
}

/** 议程：按天分组的列表，只列有日程的日子。 */
export function AgendaView({ events, calendars, onOpen }: Props) {
  const groups = useMemo(() => {
    const map = new Map<string, { day: Date; list: EventOccurrence[] }>();
    for (const o of events) {
      const day = startOfDay(occStart(o));
      const key = toDateInput(day);
      if (!map.has(key)) map.set(key, { day, list: [] });
      map.get(key)!.list.push(o);
    }
    return [...map.values()];
  }, [events]);

  if (groups.length === 0) {
    return (
      <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 text-sm">
        <CalendarDays className="size-8" />
        这段时间没有日程
      </div>
    );
  }

  const today = new Date();
  return (
    <ScrollArea className="h-full">
      <div className="mx-auto flex max-w-3xl flex-col gap-5 p-5">
        {groups.map(({ day, list }) => (
          <section key={day.toDateString()} className="flex gap-4">
            <div className="w-24 shrink-0 pt-1">
              <p className={cn("text-sm font-medium", sameDay(day, today) && "text-accent")}>{dayTitle(day)}</p>
              {sameDay(day, today) && <p className="text-muted-foreground text-xs">今天</p>}
            </div>
            <ul className="flex min-w-0 flex-1 flex-col gap-1.5">
              {list.map((o) => (
                <li key={occurrenceKey(o)}>
                  <button
                    type="button"
                    onClick={() => onOpen(o)}
                    className="hover:bg-secondary border-border flex w-full items-start gap-3 rounded-md border px-3 py-2 text-left"
                  >
                    <span className="mt-1 size-2.5 shrink-0 rounded-full" style={{ background: occurrenceColor(calendars, o) }} />
                    <span className="text-muted-foreground w-24 shrink-0 text-sm tabular-nums">
                      {o.allDay ? "全天" : `${hm(occStart(o))} – ${hm(occEnd(o))}`}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{o.title || "(无标题)"}</span>
                      {o.location && (
                        <span className="text-muted-foreground flex items-center gap-1 truncate text-xs">
                          <MapPin className="size-3" />
                          {o.location}
                        </span>
                      )}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </ScrollArea>
  );
}
