"use client";

import { Repeat } from "lucide-react";

import { EVENT_DRAG_TYPE, type DragPayload } from "@/components/calendar/calendar-utils";
import { cn } from "@/lib/utils";
import { hm } from "@/lib/date";
import type { EventOccurrence } from "@/lib/api";

interface Props {
  occ: EventOccurrence;
  color: string;
  draggable: boolean;
  onOpen: (o: EventOccurrence) => void;
  /** 紧凑模式用于月视图的单行条目。 */
  compact?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

/**
 * 日程条目。颜色只画在左侧色条与淡底上，文字保持前景色：
 * 日历颜色来自服务器，可能是任意高饱和色，用作文字色会不可读。
 */
export function EventChip({ occ, color, draggable, onOpen, compact, className, style }: Props) {
  const start = new Date(occ.start);
  return (
    <button
      type="button"
      draggable={draggable}
      onDragStart={(e) => {
        const payload: DragPayload = {
          accountId: occ.accountId,
          eventId: occ.eventId,
          recurrenceId: occ.recurrenceId,
          start: occ.start,
          allDay: occ.allDay,
        };
        e.dataTransfer.setData(EVENT_DRAG_TYPE, JSON.stringify(payload));
        e.dataTransfer.effectAllowed = "move";
      }}
      onClick={(e) => {
        e.stopPropagation();
        onOpen(occ);
      }}
      title={occ.title}
      className={cn(
        "group relative flex w-full min-w-0 items-start gap-1 overflow-hidden rounded-sm border-l-[3px] px-1.5 text-left text-xs",
        "hover:brightness-95 focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none dark:hover:brightness-110",
        compact ? "h-5 items-center" : "py-0.5",
        occ.status === "cancelled" && "line-through opacity-60",
        className,
      )}
      style={{
        borderLeftColor: color,
        background: `color-mix(in srgb, ${color} 16%, var(--card))`,
        ...style,
      }}
    >
      {!occ.allDay && <span className="text-muted-foreground shrink-0 tabular-nums">{hm(start)}</span>}
      <span className="min-w-0 flex-1 truncate font-medium">{occ.title || "(无标题)"}</span>
      {occ.recurring && !compact && <Repeat className="text-muted-foreground mt-0.5 size-3 shrink-0" />}
    </button>
  );
}
