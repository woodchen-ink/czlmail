"use client";

import { useSyncExternalStore } from "react";

import { t } from "@/lib/i18n";

/**
 * 邮件模块的键盘快捷键: 动作表、默认键位、用户改过的键位。
 *
 * 键位存 localStorage 而不是后端: 它跟着这台机器的键盘布局走 (捷克语键盘上打不出 #),
 * 换一台机器未必合适; 与主题、语言一样是本机偏好。
 *
 * 一个键位是一个字符串: 可选的修饰键前缀 (Ctrl+ Alt+ Shift+ Meta+, 固定这个顺序) 加上键名。
 * 键名取 KeyboardEvent.key: 字母一律小写 (大写锁定不影响), 符号就是打出来的那个字符
 * (所以 "?" 不写成 Shift+/, 换键盘布局也认), 其余是 Delete、ArrowDown 这样的名字。
 */

export const SHORTCUT_ACTIONS = [
  "next",
  "prev",
  "compose",
  "reply",
  "replyAll",
  "forward",
  "archive",
  "trash",
  "toggleFlag",
  "markRead",
  "markUnread",
  "toggleRead",
  "junk",
  "search",
  "help",
] as const;

export type ShortcutAction = (typeof SHORTCUT_ACTIONS)[number];
export type ShortcutMap = Readonly<Record<ShortcutAction, readonly string[]>>;

export const DEFAULT_SHORTCUTS: ShortcutMap = {
  next: ["j", "ArrowDown"],
  prev: ["k", "ArrowUp"],
  compose: ["c"],
  reply: ["r"],
  replyAll: ["a"],
  forward: ["f"],
  archive: ["e"],
  trash: ["#", "Delete"],
  toggleFlag: ["s"],
  markRead: ["q"],
  markUnread: ["u"],
  toggleRead: ["m"],
  junk: ["!"],
  search: ["/"],
  help: ["?"],
};

/** 动作在帮助与设置里显示的名字。 */
export function shortcutLabel(action: ShortcutAction): string {
  switch (action) {
    case "next":
      return t("下一封");
    case "prev":
      return t("上一封");
    case "compose":
      return t("写邮件");
    case "reply":
      return t("回复");
    case "replyAll":
      return t("全部回复");
    case "forward":
      return t("转发");
    case "archive":
      return t("归档");
    case "trash":
      return t("删除");
    case "toggleFlag":
      return t("加星标 / 取消星标");
    case "markRead":
      return t("标为已读");
    case "markUnread":
      return t("标为未读");
    case "toggleRead":
      return t("切换已读 / 未读");
    case "junk":
      return t("标记为垃圾邮件");
    case "search":
      return t("搜索");
    case "help":
      return t("显示本帮助");
  }
}

const STORAGE_KEY = "czlmail.shortcuts";
const CHANGE_EVENT = "czlmail:shortcuts";
const MODIFIERS = ["Ctrl", "Alt", "Shift", "Meta"] as const;
const MODIFIER_PREFIX = /^(?:(?:Ctrl|Alt|Shift|Meta)\+)*/;
/** 只按下修饰键、输入法合成中的按键, 不构成键位。 */
const IGNORED_KEYS = new Set(["Shift", "Control", "Alt", "Meta", "AltGraph", "CapsLock", "NumLock", "ScrollLock", "Dead", "Process", "Unidentified", "OS", "Fn"]);
/**
 * 不能绑的键: 界面本身的键盘操作 (Tab 走焦点, Enter / 空格激活, Esc 关闭),
 * 系统的复制粘贴撤销, 以及模块切换 (Ctrl+1…6、Ctrl+,)。
 * Ctrl+R / Ctrl+F / Ctrl+P / F5 不必保留: Wails 在 Windows 上关掉了 WebView2 自带的快捷键
 * (AreBrowserAcceleratorKeysEnabled=false), 正式版里它们不会刷新、查找或打印。
 */
const RESERVED = new Set([
  "Tab", "Shift+Tab", "Enter", "Space", "Escape",
  ...["c", "v", "x", "a", "z", "y", "1", "2", "3", "4", "5", "6", ","].flatMap((k) => [`Ctrl+${k}`, `Meta+${k}`]),
]);

/** 按键事件对应的键位; 只按了修饰键时返回空串。 */
export function keyOf(e: KeyboardEvent): string {
  if (IGNORED_KEYS.has(e.key) || e.isComposing) return "";
  // Windows 上 AltGr 会同时报 Ctrl 与 Alt。它是用来打字符的 (捷克语键盘的 # 是 AltGr+X), 不算修饰键。
  const altGraph = e.getModifierState?.("AltGraph") ?? false;
  const printable = e.key.length === 1;
  const letter = printable && e.key.toLowerCase() !== e.key.toUpperCase();
  const key = e.key === " " ? "Space" : letter ? e.key.toLowerCase() : e.key;
  const held: Record<(typeof MODIFIERS)[number], boolean> = {
    Ctrl: e.ctrlKey && !altGraph,
    Alt: e.altKey && !altGraph,
    // 符号本身就是 Shift 打出来的结果, 不再重复记 Shift。
    Shift: e.shiftKey && (letter || !printable || e.key === " "),
    Meta: e.metaKey,
  };
  return MODIFIERS.filter((m) => held[m]).map((m) => `${m}+`).join("") + key;
}

export function isReservedKey(key: string): boolean {
  return RESERVED.has(key);
}

const KEY_NAMES: Record<string, string> = {
  ArrowUp: "↑",
  ArrowDown: "↓",
  ArrowLeft: "←",
  ArrowRight: "→",
  Escape: "Esc",
  Delete: "Del",
  Insert: "Ins",
  PageUp: "PgUp",
  PageDown: "PgDn",
};

/** 键位的显示形式: 字母大写, 方向键用箭头。 */
export function formatKey(key: string): string {
  const prefix = MODIFIER_PREFIX.exec(key)?.[0] ?? "";
  const name = key.slice(prefix.length);
  return prefix + (KEY_NAMES[name] ?? (name.length === 1 ? name.toUpperCase() : name));
}

/** 一个动作的全部键位, 供帮助与设置显示。 */
export function formatKeys(keys: readonly string[]): string {
  return keys.map(formatKey).join(" / ");
}

/** 存储里只放与默认不同的动作; 损坏或过时的内容按没改过处理。 */
function parse(raw: string | null): ShortcutMap {
  if (!raw) return DEFAULT_SHORTCUTS;
  try {
    const saved: unknown = JSON.parse(raw);
    if (!saved || typeof saved !== "object" || Array.isArray(saved)) return DEFAULT_SHORTCUTS;
    const next: Record<string, readonly string[]> = { ...DEFAULT_SHORTCUTS };
    const taken = new Set<string>();
    for (const action of SHORTCUT_ACTIONS) {
      const keys = (saved as Record<string, unknown>)[action];
      if (!Array.isArray(keys)) continue;
      next[action] = keys.filter((k): k is string => typeof k === "string" && k !== "" && !RESERVED.has(k));
    }
    // 一个键只能属于一个动作: 用户改过的动作优先, 默认键位让出来。
    for (const action of SHORTCUT_ACTIONS) {
      if (next[action] === DEFAULT_SHORTCUTS[action]) continue;
      next[action] = next[action].filter((k) => !taken.has(k) && !!taken.add(k));
    }
    for (const action of SHORTCUT_ACTIONS) {
      if (next[action] !== DEFAULT_SHORTCUTS[action]) continue;
      if (next[action].some((k) => taken.has(k))) next[action] = next[action].filter((k) => !taken.has(k));
    }
    return next as ShortcutMap;
  } catch {
    return DEFAULT_SHORTCUTS;
  }
}

let cachedRaw: string | null | undefined;
let cached: ShortcutMap = DEFAULT_SHORTCUTS;
let lookup: Map<string, ShortcutAction> | null = null;

function readRaw(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

// 存储内容只在本模块写入 (store) 或别的窗口写入 (storage 事件) 时变化, 其余时候不再读 localStorage:
// 每次按键、每次渲染都会调 getShortcuts。
let stale = true;
let watching = false;

/** 当前生效的键位。结果按存储内容缓存, 没变化时返回同一个对象。 */
export function getShortcuts(): ShortcutMap {
  if (typeof window === "undefined") return DEFAULT_SHORTCUTS;
  if (!watching) {
    watching = true;
    window.addEventListener("storage", (e) => {
      if (e.key === STORAGE_KEY || e.key === null) stale = true;
    });
  }
  if (!stale) return cached;
  stale = false;
  const raw = readRaw();
  if (raw !== cachedRaw) {
    cachedRaw = raw;
    cached = parse(raw);
    lookup = null;
  }
  return cached;
}

function sameKeys(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((k, i) => k === b[i]);
}

function store(map: ShortcutMap) {
  const changed: Partial<Record<ShortcutAction, readonly string[]>> = {};
  for (const action of SHORTCUT_ACTIONS) {
    if (!sameKeys(map[action], DEFAULT_SHORTCUTS[action])) changed[action] = map[action];
  }
  try {
    if (Object.keys(changed).length === 0) window.localStorage.removeItem(STORAGE_KEY);
    else window.localStorage.setItem(STORAGE_KEY, JSON.stringify(changed));
  } catch {
    // 存不下时本次修改不生效, 界面仍显示存储里的真实状态。
  }
  stale = true;
  window.dispatchEvent(new Event(CHANGE_EVENT));
}

/**
 * 把 key 绑到 action 上。这个键原来属于别的动作时从那边拿走, 返回被拿走的动作。
 * 保留键不能绑, 返回 "reserved"。
 */
export function addShortcutKey(action: ShortcutAction, key: string): ShortcutAction | "reserved" | null {
  if (!key || RESERVED.has(key)) return "reserved";
  const current = getShortcuts();
  if (current[action].includes(key)) return null;
  const previous = SHORTCUT_ACTIONS.find((a) => current[a].includes(key)) ?? null;
  const next: Record<string, readonly string[]> = { ...current };
  if (previous) next[previous] = current[previous].filter((k) => k !== key);
  next[action] = [...current[action], key];
  store(next as ShortcutMap);
  return previous;
}

export function removeShortcutKey(action: ShortcutAction, key: string) {
  const current = getShortcuts();
  store({ ...current, [action]: current[action].filter((k) => k !== key) });
}

export function resetShortcuts() {
  store(DEFAULT_SHORTCUTS);
}

export function hasCustomShortcuts(): boolean {
  const current = getShortcuts();
  return SHORTCUT_ACTIONS.some((a) => !sameKeys(current[a], DEFAULT_SHORTCUTS[a]));
}

/** 按键事件触发的动作; 没有绑定时返回 null。 */
export function shortcutActionOf(e: KeyboardEvent): ShortcutAction | null {
  const key = keyOf(e);
  if (!key) return null;
  const map = getShortcuts();
  if (!lookup) {
    lookup = new Map();
    for (const action of SHORTCUT_ACTIONS) for (const k of map[action]) lookup.set(k, action);
  }
  return lookup.get(key) ?? null;
}

function subscribe(listener: () => void): () => void {
  window.addEventListener(CHANGE_EVENT, listener);
  // 另一个窗口 (如单独打开的写信窗口) 改了键位。
  window.addEventListener("storage", listener);
  return () => {
    window.removeEventListener(CHANGE_EVENT, listener);
    window.removeEventListener("storage", listener);
  };
}

/** 当前键位, 用户在设置里修改后跟着更新。静态导出与首帧用默认键位。 */
export function useShortcuts(): ShortcutMap {
  return useSyncExternalStore(subscribe, getShortcuts, () => DEFAULT_SHORTCUTS);
}
