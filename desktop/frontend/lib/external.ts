import { OpenLink } from "@/wailsjs/go/main/App";

/** 允许交给系统浏览器打开的协议。 */
const OPENABLE = /^(https?:|mailto:)/i;

/**
 * 在系统默认浏览器里打开链接。
 *
 * 链接来自邮件正文，即完全由发件人控制，因此必须在这里再校验一次协议：
 * 消毒阶段已经过滤过，但那一层的输出要经过沙箱 iframe 再回到这里，
 * 中间任何一环出问题都不该让 file: 或其它本地协议被直接唤起。
 *
 * 交给后端的 OpenLink 而不是 Wails 的 BrowserOpenURL: 后者会静默丢弃含
 * ~ ! ( ) * ; 等合法字符的地址, 点了没有任何反应。后端同样只放行这三种协议。
 * 笔记里的外部链接与日程的会议链接也走这里, 同样不直接调 BrowserOpenURL。
 */
export function openExternal(url: string): void {
  if (!url || !OPENABLE.test(url)) return;

  try {
    // 再解析一次，拒绝畸形 URL。
    const parsed = new URL(url);
    if (!OPENABLE.test(parsed.protocol)) return;
    void OpenLink(parsed.toString()).catch(() => {});
  } catch {
    // 解析失败即丢弃，不做任何补救性猜测。
  }
}
