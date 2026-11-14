/**
 * 模块之间的应用内消息，例如在通讯录里点「写邮件」要切到邮件模块并打开写信窗口。
 *
 * 用 window 上的 CustomEvent 而不是把回调一层层传下去：各模块常驻挂载、彼此独立，
 * 只需约定消息名与负载。
 */

export type Module = "mail" | "calendar" | "contacts" | "files" | "notes" | "assistant" | "settings";

export interface AppMessages {
  navigate: { module: Module };
  compose: { to: string; cc?: string; bcc?: string; subject?: string; body?: string };
  openEmail: { accountId: string; emailId: string };
  /** 打开一篇笔记(笔记模块可能还没挂载, 见 lib/notes/api 的 requestOpenNote)。 */
  openNote: { vaultId: string; id: string };
  /** 打开设置里的某一页。 */
  openSettings: { section: string };
}

const PREFIX = "czl:";

export function send<K extends keyof AppMessages>(name: K, detail: AppMessages[K]) {
  window.dispatchEvent(new CustomEvent(PREFIX + name, { detail }));
}

// 「打开这封邮件」可能早于邮件模块订阅发出 (冷启动时点通知 / 链接, 界面一加载就取打开请求),
// 只发事件会丢; 同时记下来, 邮件模块订阅时取走。与 lib/notes/api 的 requestOpenNote 同一做法。
let pendingOpenEmail: AppMessages["openEmail"] | null = null;

export function requestOpenEmail(detail: AppMessages["openEmail"]) {
  pendingOpenEmail = detail;
  send("openEmail", detail);
}

/** 取走尚未处理的「打开邮件」请求, 只返回一次。 */
export function takePendingOpenEmail(): AppMessages["openEmail"] | null {
  const d = pendingOpenEmail;
  pendingOpenEmail = null;
  return d;
}

export function listen<K extends keyof AppMessages>(name: K, handler: (detail: AppMessages[K]) => void): () => void {
  const fn = (e: Event) => handler((e as CustomEvent<AppMessages[K]>).detail);
  window.addEventListener(PREFIX + name, fn);
  return () => window.removeEventListener(PREFIX + name, fn);
}
