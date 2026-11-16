"use client";

import { AlertTriangle, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { failureIsTemporary, failureLabel, failureText } from "@/lib/notes/sync-state";
import { t } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/**
 * The title-row state of a note that did not synchronize: a word that opens the reason in plain
 * language, a retry, and the server's own words for troubleshooting.
 */
export function NoteSyncFailure({ kind, detail, iconOnly, onRetry }: { kind: string; detail?: string; iconOnly?: boolean; onRetry: () => void }) {
  const temporary = failureIsTemporary(kind);
  const label = failureLabel(kind);
  return <Popover>
    <PopoverTrigger asChild>
      <Button variant="ghost" size={iconOnly ? "icon-sm" : "sm"} aria-label={label} className={cn("shrink-0", temporary ? "text-muted-foreground" : "text-destructive")}>
        <AlertTriangle />{!iconOnly && label}
      </Button>
    </PopoverTrigger>
    <PopoverContent align="end" className="w-80">
      <p className="font-medium">{label}</p>
      <p className="text-muted-foreground">{failureText(kind)}</p>
      {detail && <details className="text-muted-foreground text-xs">
        <summary className="cursor-pointer select-none">{t("技术详情")}</summary>
        <p className="mt-1 break-words select-text">{detail}</p>
      </details>}
      <div className="flex justify-end"><Button variant="outline" size="sm" onClick={onRetry}><RotateCw />{t("重试")}</Button></div>
    </PopoverContent>
  </Popover>;
}
