"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { send } from "@/lib/app-bus";
import { isNative } from "@/lib/native-bridge";
import { SHORTCUT_ACTIONS, formatKey, formatKeys, shortcutLabel, useShortcuts } from "@/lib/shortcuts";

/** 键位来自 lib/shortcuts (mail-shell 按同一张表处理按键); Ctrl+Z 与 Esc 是固定的。 */
export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  useI18n();
  const keys = useShortcuts();
  const rows = [
    ...SHORTCUT_ACTIONS.map((action) => ({ id: action, keys: formatKeys(keys[action]), action: shortcutLabel(action) })),
    { id: "undo", keys: formatKey("Ctrl+z"), action: t("撤销上一步操作") },
    { id: "escape", keys: "Esc", action: t("退出全屏阅读 / 关闭对话框") },
  ];
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("键盘快捷键")}</DialogTitle>
        </DialogHeader>
        <ul className="flex flex-col">
          {rows.map((s) => (
            <li
              key={s.id}
              className="border-border flex items-center justify-between border-b py-2 text-sm last:border-b-0"
            >
              <span>{s.action}</span>
              {s.keys ? (
                <kbd className="bg-secondary rounded-sm px-2 py-0.5 font-mono text-xs">{s.keys}</kbd>
              ) : (
                <span className="text-muted-foreground text-xs">{t("未设置")}</span>
              )}
            </li>
          ))}
        </ul>
        {/* 移动端壳里没有这一页设置。 */}
        {!isNative() && (
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                onClose();
                send("navigate", { module: "settings" });
                // 设置模块第一次打开时才挂载, 等它订阅上再发 (同 app-shell 里打开「关于」的做法)。
                setTimeout(() => send("openSettings", { section: "shortcuts" }), 0);
              }}
            >
              {t("自定义快捷键…")}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}
