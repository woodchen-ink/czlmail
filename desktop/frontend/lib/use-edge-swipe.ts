"use client";

import { useEffect, useRef, useState } from "react";

import { useBackAction } from "@/lib/native-back";

/**
 * 从屏幕左边缘向右拖的手势, 对应 iOS 的"侧滑返回"。
 *
 * 系统自带的侧滑返回只认 WebView 的浏览历史, 本程序是单页应用, 切栏不产生历史, 所以自己识别。
 * 也不能改用 history.pushState 去触发系统手势: Next 的路由会接管 popstate, 遇到不认识的历史记录会整页刷新。
 *
 * follow 为 true 时页面跟着手指平移 (返回), 为 false 时只在松手时判定 (拉出侧栏)。
 * 松手时拖过三分之一宽度, 或拖过 40px 且速度够快, 才算数; 否则弹回。
 * 竖向滑动先于横向超过阈值时让给页面滚动。
 *
 * 邮件正文在沙箱 iframe 里, iframe 内的触摸不会传到这里: 起手点要落在正文以外 (左边缘留白、工具栏)。
 *
 * follow 为 true 的手势是"返回", 同时登记为 Android 返回键的动作 (见 native-back.ts)。
 */
const EDGE = 24;
const DECIDE = 10;

export function useEdgeSwipe(onSwipe: (() => void) | null, follow = true) {
  const [el, setEl] = useState<HTMLElement | null>(null);
  const latest = useRef(onSwipe);
  useEffect(() => {
    latest.current = onSwipe;
  });
  const enabled = !!onSwipe;
  useBackAction(follow ? onSwipe : null, el);

  useEffect(() => {
    if (!el || !enabled) return;
    let start: { x: number; y: number; t: number } | null = null;
    let horizontal = false;
    let dx = 0;

    const move = (x: number, animate: boolean) => {
      if (!follow) return;
      el.style.transition = animate ? "transform 180ms ease-out" : "none";
      el.style.transform = x > 0 ? `translateX(${x}px)` : "";
    };

    const onStart = (e: TouchEvent) => {
      const t = e.touches[0];
      if (e.touches.length !== 1 || t.clientX > EDGE) return;
      start = { x: t.clientX, y: t.clientY, t: e.timeStamp };
      horizontal = false;
      dx = 0;
    };
    const onMove = (e: TouchEvent) => {
      if (!start) return;
      const t = e.touches[0];
      dx = t.clientX - start.x;
      const dy = t.clientY - start.y;
      if (!horizontal) {
        if (Math.abs(dy) > DECIDE && Math.abs(dy) > Math.abs(dx)) {
          start = null;
          return;
        }
        if (dx < DECIDE) return;
        horizontal = true;
      }
      // 已判定为横向: 不让页面跟着滚动或触发文字选择。
      e.preventDefault();
      move(Math.max(0, dx), false);
    };
    const onEnd = (e: TouchEvent) => {
      if (!start) return;
      const elapsed = Math.max(1, e.timeStamp - start.t);
      const done = horizontal && (dx > el.clientWidth / 3 || (dx > 40 && dx / elapsed > 0.5));
      start = null;
      if (!done) {
        move(0, true);
        return;
      }
      if (!follow) {
        latest.current?.();
        return;
      }
      move(el.clientWidth, true);
      window.setTimeout(() => {
        latest.current?.();
        // 页面已切走 (隐藏), 复位留给下次显示。
        el.style.transition = "none";
        el.style.transform = "";
      }, 180);
    };
    const onCancel = () => {
      if (start) move(0, true);
      start = null;
    };

    el.addEventListener("touchstart", onStart, { passive: true });
    el.addEventListener("touchmove", onMove, { passive: false });
    el.addEventListener("touchend", onEnd);
    el.addEventListener("touchcancel", onCancel);
    return () => {
      el.removeEventListener("touchstart", onStart);
      el.removeEventListener("touchmove", onMove);
      el.removeEventListener("touchend", onEnd);
      el.removeEventListener("touchcancel", onCancel);
    };
  }, [el, enabled, follow]);

  return setEl;
}
