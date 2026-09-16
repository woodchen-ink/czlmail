"use client";

import { useState, useEffect } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { WEEKDAYS, addMonths, monthGrid, sameDay, startOfMonth, toDateInput } from "@/lib/date";

interface Props {
  selected: Date;
  /** 有日程的日期（YYYY-MM-DD），在数字下方打点。 */
  busyDays: Set<string>;
  onSelect: (d: Date) => void;
}

/** 侧栏的小月历。翻页只影响自身显示，点日期才跳转主视图。 */
export function MiniMonth({ selected, busyDays, onSelect }: Props) {
  const [month, setMonth] = useState(() => startOfMonth(selected));

  useEffect(() => {
    setMonth(startOfMonth(selected));
  }, [selected]);

  const today = new Date();
  const days = monthGrid(month);

  return (
    <div className="flex flex-col gap-1 px-2 select-none">
      <div className="flex items-center justify-between">
        <Button variant="ghost" size="icon" className="size-7" aria-label="上个月" onClick={() => setMonth(addMonths(month, -1))}>
          <ChevronLeft className="size-4" />
        </Button>
        <span className="text-sm font-medium">
          {month.getFullYear()} 年 {month.getMonth() + 1} 月
        </span>
        <Button variant="ghost" size="icon" className="size-7" aria-label="下个月" onClick={() => setMonth(addMonths(month, 1))}>
          <ChevronRight className="size-4" />
        </Button>
      </div>

      <div className="grid grid-cols-7 text-center">
        {WEEKDAYS.map((w) => (
          <span key={w} className="text-muted-foreground py-1 text-[11px]">
            {w.slice(1)}
          </span>
        ))}
        {days.map((d) => {
          const inMonth = d.getMonth() === month.getMonth();
          const isToday = sameDay(d, today);
          const isSelected = sameDay(d, selected);
          return (
            <button
              key={d.toISOString()}
              type="button"
              onClick={() => onSelect(d)}
              className={cn(
                "relative mx-auto flex size-7 items-center justify-center rounded-full text-xs tabular-nums transition-colors",
                "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                !inMonth && "text-muted-foreground/60",
                isSelected && !isToday && "bg-muted font-medium",
                isToday && "bg-primary text-primary-foreground hover:bg-primary font-medium",
              )}
            >
              {d.getDate()}
              {busyDays.has(toDateInput(d)) && !isToday && (
                <span className="bg-accent absolute bottom-0.5 left-1/2 size-1 -translate-x-1/2 rounded-full" />
              )}
            </button>
          );
        })}
      </div>
    </div>
  );
}
