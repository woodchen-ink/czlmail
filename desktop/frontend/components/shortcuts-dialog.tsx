"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

/** 与 mail-shell 里的键位处理保持一一对应。 */
export const getShortcuts: () => ({ keys: string; action: string }[]) = () => ([
  { keys: "j / ↓", action: t("下一封") },
  { keys: "k / ↑", action: t("上一封") },
  { keys: "c", action: t("写邮件") },
  { keys: "r", action: t("回复") },
  { keys: "a", action: t("全部回复") },
  { keys: "f", action: t("转发") },
  { keys: "e", action: t("归档") },
  { keys: "#  / Delete", action: t("删除") },
  { keys: "s", action: t("加星标 / 取消星标") },
  { keys: "u", action: t("标为未读") },
  { keys: "!", action: t("标记为垃圾邮件") },
  { keys: "/", action: t("搜索") },
  { keys: "Esc", action: t("退出全屏阅读 / 关闭对话框") },
  { keys: "?", action: t("显示本帮助") },
]);

export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  useI18n();
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("键盘快捷键")}</DialogTitle>
        </DialogHeader>
        <ul className="flex flex-col">
          {getShortcuts().map((s) => (
            <li
              key={s.keys}
              className="border-border flex items-center justify-between border-b py-2 text-sm last:border-b-0"
            >
              <span>{s.action}</span>
              <kbd className="bg-secondary rounded-sm px-2 py-0.5 font-mono text-xs">{s.keys}</kbd>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
