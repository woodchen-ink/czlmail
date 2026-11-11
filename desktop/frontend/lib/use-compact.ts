"use client";

import { useSyncExternalStore } from "react";

/**
 * 窄屏 (手机竖屏) 布局: 三栏放不下, 一次只显示一栏, 模块导航挪到底部。
 * 断点与 Tailwind 的 md 一致, 横屏 iPhone 与 iPad 仍走多栏。
 */
const QUERY = "(max-width: 767px)";

function subscribe(onChange: () => void) {
  const mq = window.matchMedia(QUERY);
  mq.addEventListener("change", onChange);
  return () => mq.removeEventListener("change", onChange);
}

export function useCompact(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia(QUERY).matches,
    () => false,
  );
}
