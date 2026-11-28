"use client";

import { useEffect, useRef, useState, useSyncExternalStore } from "react";

import { cn } from "@/lib/utils";
import { hasRuntime } from "@/lib/api";
import {
  avatarGeneration,
  avatarIsFresh,
  cachedAvatar,
  loadAvatar,
  markAvatarBroken,
  releaseAvatar,
  subscribeAvatarCache,
  whenVisible,
} from "@/lib/avatar-cache";
import { initials } from "@/lib/format";

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
 * 取到的图在本次运行里记在内存（lib/avatar-cache），列表重新挂载时直接显示，不再闪首字母。
 */
export function SenderAvatar({ email, name, className }: Props) {
  const key = email.trim().toLowerCase();
  const ref = useRef<HTMLSpanElement>(null);
  // 缓存清空 (退出登录、关掉头像) 时代号变, 手上的旧图作废。
  const gen = useSyncExternalStore(subscribeAvatarCache, avatarGeneration, avatarGeneration);
  // 记下这张图属于哪个地址、哪一代: 行被复用给别的发件人时, 旧地址的图不能留到新的取回来之前。
  const [shown, setShown] = useState<{ key: string; gen: number; url: string | null | undefined }>(() => ({
    key,
    gen,
    url: key ? cachedAvatar(key) : null,
  }));
  const url = shown.key === key && shown.gen === gen ? shown.url : key ? cachedAvatar(key) : null;

  useEffect(() => {
    if (!key || !hasRuntime() || !ref.current || avatarIsFresh(key)) return;
    let alive = true;
    // 是否还占着 loadAvatar 的名额: 取完或卸载时各放一次, 只放一次。
    let held = false;
    const release = () => {
      if (held) {
        held = false;
        releaseAvatar(key);
      }
    };
    const stop = whenVisible(ref.current, () => {
      held = true;
      loadAvatar(key).then((u) => {
        release();
        // undefined: 没取 (已没人要或出错), 保留手上的。
        if (alive && u !== undefined) setShown({ key, gen, url: u });
      });
    });
    return () => {
      alive = false;
      stop();
      release();
    };
  }, [key, gen]);

  const label = initials(name || email);

  return (
    <span
      ref={ref}
      className={cn(
        "bg-muted text-muted-foreground relative inline-flex shrink-0 items-center justify-center overflow-hidden rounded-full text-xs font-medium select-none",
        className,
      )}
      aria-hidden
    >
      {label}
      {url && (
        // 图片叠在首字母上方：加载完成前首字母已经可见，不会出现空白圆圈。
        <img
          src={url}
          alt=""
          decoding="async"
          className="bg-paper absolute inset-0 size-full object-contain"
          onError={() => {
            // 坏的是这张就记成没有; 缓存里已经换了新图就改用新图。
            markAvatarBroken(key, url);
            setShown({ key, gen, url: cachedAvatar(key) });
          }}
        />
      )}
    </span>
  );
}
