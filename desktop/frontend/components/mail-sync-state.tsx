"use client";

import { AlertCircle, Loader2, RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useI18n } from "@/components/locale-provider";
import { t } from "@/lib/i18n";
import type { MailSyncStatus } from "@/lib/api";

/**
 * 邮件列表还没有内容、而首次同步没做完时的占位。
 *
 * 刚登录时缓存是空的, 要等后台把最近的邮件取回来; 这段时间显示「没有邮件」会让人以为
 * 邮箱是空的或程序坏了。同步失败时给出原因与重试, 不停在转圈上。
 */
export function MailSyncState({
  status,
  retrying,
  onRetry,
}: {
  status: MailSyncStatus;
  retrying: boolean;
  onRetry: () => void;
}) {
  useI18n();

  if (!status.running && !retrying) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <AlertCircle className="text-destructive size-6" />
        <div className="flex flex-col gap-1">
          <p className="text-sm font-medium">{t("邮件没有同步下来")}</p>
          {status.error && <p className="text-muted-foreground text-xs break-words">{status.error}</p>}
        </div>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCw className="size-4" />
          {t("重试")}
        </Button>
      </div>
    );
  }

  const showProgress = status.running && status.total > 1;
  const percent = status.total > 0 ? Math.round((status.done / status.total) * 100) : 0;

  return (
    <div className="flex h-full flex-col" role="status" aria-live="polite">
      {/* 骨架行占住列表的位置, 邮件到了原地换上, 不会从一片空白跳成满屏。 */}
      <div className="flex flex-col gap-4 p-3 opacity-60" aria-hidden>
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="flex items-start gap-3">
            <Skeleton className="size-8 shrink-0 rounded-full" />
            <div className="flex min-w-0 flex-1 flex-col gap-1.5 pt-0.5">
              <Skeleton className="h-3.5 w-1/3" />
              <Skeleton className="h-3.5 w-4/5" />
            </div>
          </div>
        ))}
      </div>

      <div className="flex flex-1 flex-col items-center gap-3 px-6 pt-6 text-center">
        <Loader2 className="text-muted-foreground size-5 animate-spin" />
        <div className="flex flex-col gap-1">
          <p className="text-sm font-medium">{t("正在同步邮件")}</p>
          <p className="text-muted-foreground text-xs">{t("首次登录要从服务器取回最近的邮件，完成后会自动显示。")}</p>
        </div>
        {showProgress && (
          <div className="flex w-40 flex-col gap-1.5">
            <div className="bg-muted h-1 overflow-hidden rounded-full">
              <div
                className="bg-primary h-full rounded-full transition-[width] duration-500"
                style={{ width: `${percent}%` }}
              />
            </div>
            <p className="text-muted-foreground text-xs tabular-nums">
              {t("已完成 {0} / {1} 个邮箱", status.done, status.total)}
            </p>
          </div>
        )}
      </div>
    </div>
  );
}

/** 侧栏在文件夹同步下来之前的占位。 */
export function SidebarSkeleton() {
  return (
    <div className="flex flex-col gap-2.5 px-2 py-1" aria-hidden>
      {["h-3.5 w-2/3", "h-3.5 w-1/2", "h-3.5 w-3/5", "h-3.5 w-2/5", "h-3.5 w-1/2"].map((bar, i) => (
        <div key={i} className="flex items-center gap-2">
          <Skeleton className="size-4 shrink-0" />
          <Skeleton className={bar} />
        </div>
      ))}
    </div>
  );
}
