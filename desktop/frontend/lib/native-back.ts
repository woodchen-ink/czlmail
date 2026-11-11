"use client";

import { useEffect, useRef } from "react";

/**
 * Android 返回键 / 返回手势。
 *
 * 系统把返回交给原生壳, 原生壳调 `window.__czlBack()`: 返回 true 表示界面处理了 (关弹窗、回列表、收侧栏),
 * false 时原生壳把程序退到后台 (不结束进程, 继续同步)。
 * 单页应用切栏不产生浏览历史, 所以返回动作由各处登记: 后登记的优先, 只认当前可见的那一处
 * (模块常驻挂载, 隐藏的模块里登记的动作不算)。
 */
type Entry = { el: () => HTMLElement | null; run: () => void };

const entries: Entry[] = [];

function visible(el: HTMLElement | null): boolean {
  return !!el && el.isConnected && el.getClientRects().length > 0;
}

export function handleBack(): boolean {
  // 打开着的弹窗、菜单先关: Radix 在 document 上监听 Escape。
  if (document.querySelector('[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"], [role="menu"][data-state="open"]')) {
    (document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    return true;
  }
  for (let i = entries.length - 1; i >= 0; i--) {
    const e = entries[i];
    if (visible(e.el())) {
      e.run();
      return true;
    }
  }
  return false;
}

if (typeof window !== "undefined") {
  (window as unknown as { __czlBack?: () => boolean }).__czlBack = handleBack;
}

/** 登记一个返回动作; el 为动作所属的界面元素, 它不可见时动作不生效。run 为 null 时不登记。 */
export function useBackAction(run: (() => void) | null, el: HTMLElement | null) {
  const latest = useRef(run);
  useEffect(() => {
    latest.current = run;
  });
  const enabled = !!run;
  useEffect(() => {
    if (!enabled || !el) return;
    const entry: Entry = { el: () => el, run: () => latest.current?.() };
    entries.push(entry);
    return () => {
      const i = entries.indexOf(entry);
      if (i >= 0) entries.splice(i, 1);
    };
  }, [enabled, el]);
}
