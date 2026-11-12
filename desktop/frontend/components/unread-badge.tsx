import { cn } from "@/lib/utils";

/**
 * 未读数角标。为 0 时不渲染; 超过 99 显示 99+。
 * quiet 是关了通知的账号: 灰底, 有数但不催人处理。底色取半透明的灰字色, 在选中行(bg-muted)上也看得出。
 */
export function UnreadBadge({ count, quiet, className }: { count: number; quiet?: boolean; className?: string }) {
  if (count <= 0) return null;
  return (
    <span
      className={cn(
        quiet ? "bg-muted-foreground/20 text-foreground/80" : "bg-unread text-unread-foreground",
        "inline-flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full px-1 text-[10px] leading-none font-semibold tabular-nums",
        className,
      )}
    >
      {count > 99 ? "99+" : count}
    </span>
  );
}
