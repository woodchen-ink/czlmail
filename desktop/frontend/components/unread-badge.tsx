import { cn } from "@/lib/utils";

/** 未读数角标。为 0 时不渲染; 超过 99 显示 99+。 */
export function UnreadBadge({ count, className }: { count: number; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      className={cn(
        "bg-unread text-unread-foreground inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full px-1 text-[10px] leading-none font-semibold tabular-nums",
        className,
      )}
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}
