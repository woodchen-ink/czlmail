import { BrowserOpenURL } from "@/wailsjs/runtime/runtime";

/** 允许交给系统浏览器打开的协议。 */
const OPENABLE = /^(https?:|mailto:)/i;

/**
 * 在系统默认浏览器里打开链接。
 *
 * 链接来自邮件正文，即完全由发件人控制，因此必须在这里再校验一次协议：
 * 消毒阶段已经过滤过，但那一层的输出要经过沙箱 iframe 再回到这里，
 * 中间任何一环出问题都不该让 file: 或其它本地协议被直接唤起。
 */
export function openExternal(url: string): void {
  if (!url || !OPENABLE.test(url)) return;

  try {
    // 再解析一次，拒绝畸形 URL。
    const parsed = new URL(url);
    if (!OPENABLE.test(parsed.protocol)) return;
    BrowserOpenURL(parsed.toString());
  } catch {
    // 解析失败即丢弃，不做任何补救性猜测。
  }
}
