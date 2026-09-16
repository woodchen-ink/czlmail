import { OnFileDrop } from "@/wailsjs/runtime/runtime";

/**
 * 从系统拖进窗口的文件。
 *
 * Wails 只允许注册一个 OnFileDrop 回调, 这里注册一次再分发: 后订阅的处理器先处理,
 * 返回 true 表示已处理, 不再往下传。文件模块在前台时优先由它上传。
 */
type Handler = (paths: string[]) => boolean;

const handlers: Handler[] = [];
let registered = false;

export function onFileDrop(handler: Handler): () => void {
  if (!registered && typeof window !== "undefined" && (window as unknown as { runtime?: unknown }).runtime) {
    registered = true;
    OnFileDrop((_x, _y, paths) => {
      for (let i = handlers.length - 1; i >= 0; i--) {
        if (handlers[i](paths)) return;
      }
    }, false);
  }
  handlers.push(handler);
  return () => {
    const i = handlers.indexOf(handler);
    if (i >= 0) handlers.splice(i, 1);
  };
}
