"use client";

import { useCallback, useEffect, useState } from "react";

/**
 * 界面主题与邮件正文外观。
 *
 * 偏好存 localStorage 而不是后端：它必须在首帧前就生效（见 layout 里的内联脚本），
 * 等一次 Go 绑定调用回来再切换会出现整页闪一下白。
 */

export type ThemePreference = "system" | "light" | "dark";

/**
 * 邮件正文外观。
 *
 * 与应用主题分开设置：多数营销与通知邮件是按白底设计的，硬塞进暗色里要么
 * 文字对比度不够、要么内嵌图片带着一块白底。所以允许应用是暗色、正文仍保持亮色。
 */
export type BodyAppearance = "follow" | "light";

const THEME_KEY = "czlmail.theme";
const BODY_KEY = "czlmail.bodyAppearance";
const CHANGE_EVENT = "czlmail:appearance";

function read<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  try {
    const v = localStorage.getItem(key);
    return v && (allowed as readonly string[]).includes(v) ? (v as T) : fallback;
  } catch {
    return fallback;
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // 存不下只影响下次启动，本次会话照常生效。
  }
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

function systemPrefersDark(): boolean {
  return typeof window !== "undefined" && window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function resolve(pref: ThemePreference): "light" | "dark" {
  if (pref === "system") return systemPrefersDark() ? "dark" : "light";
  return pref;
}

function apply(pref: ThemePreference) {
  const dark = resolve(pref) === "dark";
  document.documentElement.classList.toggle("dark", dark);
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
}

/** 当前生效的主题，并在系统主题或用户偏好变化时更新。 */
export function useAppearance() {
  const [theme, setThemeState] = useState<ThemePreference>("system");
  const [bodyAppearance, setBodyState] = useState<BodyAppearance>("follow");
  const [resolved, setResolved] = useState<"light" | "dark">("light");

  const sync = useCallback(() => {
    const t = read(THEME_KEY, ["system", "light", "dark"] as const, "system");
    setThemeState(t);
    setBodyState(read(BODY_KEY, ["follow", "light"] as const, "follow"));
    setResolved(resolve(t));
    apply(t);
  }, []);

  useEffect(() => {
    sync();
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    media.addEventListener("change", sync);
    window.addEventListener(CHANGE_EVENT, sync);
    return () => {
      media.removeEventListener("change", sync);
      window.removeEventListener(CHANGE_EVENT, sync);
    };
  }, [sync]);

  return {
    theme,
    resolved,
    bodyAppearance,
    /** 正文最终用亮色还是暗色渲染。 */
    bodyMode: (bodyAppearance === "light" ? "light" : resolved) as "light" | "dark",
    setTheme: (t: ThemePreference) => write(THEME_KEY, t),
    setBodyAppearance: (b: BodyAppearance) => write(BODY_KEY, b),
  };
}

/**
 * 首帧前执行的内联脚本。放在 <head> 里同步运行，避免暗色用户启动时先闪白。
 * 与 apply() 的逻辑保持一致；这里不能 import 任何模块。
 */
export const themeBootScript = `(function(){try{
var p=localStorage.getItem("${THEME_KEY}")||"system";
var d=p==="dark"||(p==="system"&&matchMedia("(prefers-color-scheme: dark)").matches);
var e=document.documentElement;if(d)e.classList.add("dark");e.style.colorScheme=d?"dark":"light";
}catch(_){}})();`;
