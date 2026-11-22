"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { Fragment, useEffect, useState } from "react";
import { Plus, RotateCcw, X } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, Divider, SectionTitle } from "@/components/settings/ui";
import {
  SHORTCUT_ACTIONS,
  addShortcutKey,
  formatKey,
  hasCustomShortcuts,
  keyOf,
  removeShortcutKey,
  resetShortcuts,
  shortcutLabel,
  useShortcuts,
  type ShortcutAction,
} from "@/lib/shortcuts";

/** 邮件模块快捷键的键位。只在本机生效, 见 lib/shortcuts。 */
export function ShortcutsSection({ active }: { active: boolean }) {
  useI18n();
  const keys = useShortcuts();
  // 正在等用户按键的动作。
  const [recording, setRecording] = useState<ShortcutAction | null>(null);
  const listening = active ? recording : null;

  useEffect(() => {
    if (!listening) return;
    const action = listening;
    function onKey(e: KeyboardEvent) {
      const key = keyOf(e);
      // 只按了修饰键: 继续等真正的键。
      if (!key) return;
      // 抢在邮件模块之前拿走这次按键, 否则「删除」之类的动作会真的执行。
      e.preventDefault();
      e.stopImmediatePropagation();
      setRecording(null);
      if (key === "Escape") return;
      const taken = addShortcutKey(action, key);
      if (taken === "reserved") {
        toast.error(t("「{0}」不能用作快捷键", formatKey(key)));
      } else if (taken) {
        toast.info(t("「{0}」原来用于「{1}」，现在用于「{2}」", formatKey(key), shortcutLabel(taken), shortcutLabel(action)));
      }
    }
    const cancel = () => setRecording(null);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("blur", cancel);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("blur", cancel);
    };
  }, [listening]);

  return (
    <>
      <SectionTitle
        title={t("键盘快捷键")}
        desc={t("邮件模块里的快捷键，只在这台设备上生效。点「添加」后按下想用的键；一个键只能用于一个动作。")}
      />
      <Card>
        {SHORTCUT_ACTIONS.map((action, i) => (
          <Fragment key={action}>
            {i > 0 && <Divider />}
            <div className="flex min-h-7 items-center gap-3">
              <p className="min-w-0 flex-1 text-sm font-medium">{shortcutLabel(action)}</p>
              <div className="flex shrink-0 flex-wrap items-center justify-end gap-1.5">
                {listening === action ? (
                  <span className="text-muted-foreground text-xs" role="status">
                    {t("请按下要使用的键，按 Esc 取消")}
                  </span>
                ) : (
                  <>
                    {keys[action].length === 0 && <span className="text-muted-foreground text-xs">{t("未设置")}</span>}
                    {keys[action].map((key) => (
                      <span key={key} className="bg-secondary flex items-center gap-1 rounded-sm py-0.5 pr-1 pl-2">
                        <kbd className="font-mono text-xs">{formatKey(key)}</kbd>
                        <button
                          type="button"
                          onClick={() => removeShortcutKey(action, key)}
                          aria-label={t("移除 {0}", formatKey(key))}
                          title={t("移除 {0}", formatKey(key))}
                          className="text-muted-foreground hover:text-destructive rounded-sm"
                        >
                          <X className="size-3" />
                        </button>
                      </span>
                    ))}
                  </>
                )}
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 px-2"
                  onClick={(e) => {
                    // 按钮留着焦点时, 空格、回车会被它当成再点一次。
                    e.currentTarget.blur();
                    setRecording(listening === action ? null : action);
                  }}
                >
                  {listening === action ? <X className="size-3.5" /> : <Plus className="size-3.5" />}
                  {listening === action ? t("取消") : t("添加")}
                </Button>
              </div>
            </div>
          </Fragment>
        ))}
      </Card>
      <div>
        <Button
          variant="outline"
          size="sm"
          disabled={!hasCustomShortcuts()}
          onClick={() => {
            setRecording(null);
            resetShortcuts();
            toast.success(t("已恢复默认键位"));
          }}
        >
          <RotateCcw className="size-4" />
          {t("恢复默认键位")}
        </Button>
      </div>
    </>
  );
}
