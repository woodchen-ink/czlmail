"use client";

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

/** 与 mail-shell 里的键位处理保持一一对应。 */
export const SHORTCUTS: { keys: string; action: string }[] = [
  { keys: "j / ↓", action: "下一封" },
  { keys: "k / ↑", action: "上一封" },
  { keys: "c", action: "写邮件" },
  { keys: "r", action: "回复" },
  { keys: "a", action: "全部回复" },
  { keys: "f", action: "转发" },
  { keys: "e", action: "归档" },
  { keys: "#  / Delete", action: "删除" },
  { keys: "s", action: "加星标 / 取消星标" },
  { keys: "u", action: "标为未读" },
  { keys: "!", action: "标记为垃圾邮件" },
  { keys: "/", action: "搜索" },
  { keys: "Esc", action: "退出全屏阅读 / 关闭对话框" },
  { keys: "?", action: "显示本帮助" },
];

export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>键盘快捷键</DialogTitle>
        </DialogHeader>
        <ul className="flex flex-col">
          {SHORTCUTS.map((s) => (
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
