"use client";

import { ExternalLink, PlugZap } from "lucide-react";

import { useI18n } from "@/components/locale-provider";
import { Button } from "@/components/ui/button";
import { openExternal } from "@/lib/external";
import { t } from "@/lib/i18n";
import { upstreamDescription, upstreamLabel } from "@/lib/upstream";
import { cn } from "@/lib/utils";
import type { upstream } from "@/wailsjs/go/models";

/** 侧栏与设置里账号名旁的上游状态小字, 正常时不渲染。 */
export function UpstreamBadge({ info, className }: { info: upstream.Info | undefined; className?: string }) {
  useI18n();
  const label = upstreamLabel(info);
  if (!label) return null;
  return (
    <span
      className={cn(
        "shrink-0 text-[11px] tabular-nums",
        label.problem ? "text-destructive" : "text-muted-foreground",
        className,
      )}
    >
      {label.text}
    </span>
  );
}

/** 「打开网关后台」: 需要重新授权时网关给了 reauthUrl, 用系统浏览器打开。 */
export function ReauthButton({ info, size = "sm" }: { info: upstream.Info | undefined; size?: "sm" | "xs" }) {
  useI18n();
  if (!info?.reauthUrl) return null;
  return (
    <Button variant="outline" size={size} onClick={() => openExternal(info.reauthUrl)}>
      <ExternalLink className="size-3.5" />
      {t("打开网关后台")}
    </Button>
  );
}

/** 邮件列表为空且所看账号的上游不可用时的占位: 说明原因, 能处理时给出网关后台入口。 */
export function UpstreamState({ name, info }: { name: string; info: upstream.Info }) {
  useI18n();
  const label = upstreamLabel(info);
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center" role="status">
      <PlugZap className={cn("size-6", label?.problem ? "text-destructive" : "text-muted-foreground")} />
      <div className="flex flex-col gap-1">
        <p className="text-sm font-medium">{label ? `${name} · ${label.text}` : name}</p>
        <p className="text-muted-foreground text-xs leading-relaxed">{upstreamDescription(info)}</p>
      </div>
      <ReauthButton info={info} />
    </div>
  );
}
