"use client";

import { clearAvatarCache } from "@/lib/avatar-cache";
import { forgetCachedEmails } from "@/lib/email-cache";
import { clearListCache } from "@/lib/list-cache";

/**
 * 清空界面的内存快照 (头像、邮件列表、阅读栏)。退出登录或换人登录时调用:
 * 快照按账号 id + 邮件 id 记, Stalwart 的 id 很短, 换一个服务器或用户也会撞上,
 * 不清的话新用户切过去的那一下会看到上一个人的列表与正文。
 */
export function clearSessionCaches() {
  clearAvatarCache();
  forgetCachedEmails();
  clearListCache();
}
