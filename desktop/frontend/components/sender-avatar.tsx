"use client";

import { useEffect, useState } from "react";

import { cn } from "@/lib/utils";
import { hasRuntime } from "@/lib/api";
import { initials } from "@/lib/format";

/**
 * 本会话内已确认没有头像的地址。
 *
 * 后端已有负缓存，这里再记一层是为了连请求都不发：列表里同一发件人一屏出现十几次，
 * 每行各自失败一次会让首字母在图片加载失败后才闪出来。
 */
const missing = new Set<string>();

interface Props {
  email: string;
  name?: string;
  className?: string;
}

/**
 * 发件人头像：Gravatar → 发件域名图标 → 首字母。
 *
 * 图片一律经本地 /czl-avatar 由 Go 代理获取，不直连头像服务或发件方域名 ——
 * 否则滚动列表就会把用户的 IP 与阅读行为暴露给发件方。
 */
export function SenderAvatar({ email, name, className }: Props) {
  const key = email.trim().toLowerCase();
  const [failed, setFailed] = useState(() => !key || missing.has(key));

  useEffect(() => {
    setFailed(!key || missing.has(key));
  }, [key]);

  const label = initials(name || email);

  return (
    <span
      className={cn(
        "bg-muted text-muted-foreground relative inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full text-xs font-medium select-none",
        className,
      )}
      aria-hidden
    >
      {label}
      {!failed && hasRuntime() && (
        // 图片叠在首字母上方：加载完成前首字母已经可见，不会出现空白圆圈。
        <img
          src={`/czl-avatar?email=${encodeURIComponent(key)}`}
          alt=""
          loading="lazy"
          decoding="async"
          className="bg-paper absolute inset-0 size-full object-contain"
          onError={() => {
            missing.add(key);
            setFailed(true);
          }}
        />
      )}
    </span>
  );
}
