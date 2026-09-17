/**
 * 模块之间的应用内消息，例如在通讯录里点「写邮件」要切到邮件模块并打开写信窗口。
 *
 * 用 window 上的 CustomEvent 而不是把回调一层层传下去：各模块常驻挂载、彼此独立，
 * 只需约定消息名与负载。
 */

export type Module = "mail" | "calendar" | "contacts" | "files" | "assistant" | "settings";

export interface AppMessages {
  navigate: { module: Module };
  compose: { to: string; cc?: string; bcc?: string; subject?: string; body?: string };
  openEmail: { accountId: string; emailId: string };
  /** 打开设置里的某一页。 */
  openSettings: { section: string };
}

const PREFIX = "czl:";

export function send<K extends keyof AppMessages>(name: K, detail: AppMessages[K]) {
  window.dispatchEvent(new CustomEvent(PREFIX + name, { detail }));
}

export function listen<K extends keyof AppMessages>(name: K, handler: (detail: AppMessages[K]) => void): () => void {
  const fn = (e: Event) => handler((e as CustomEvent<AppMessages[K]>).detail);
  window.addEventListener(PREFIX + name, fn);
  return () => window.removeEventListener(PREFIX + name, fn);
}
