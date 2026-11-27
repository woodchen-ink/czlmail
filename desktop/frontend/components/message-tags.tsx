"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useState } from "react";
import { Check, Flag, Star } from "lucide-react";
import { toast } from "sonner";

import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { errorMessage, type EmailSummary } from "@/lib/api";
import {
  ensureTag,
  taskFlagState,
  taskFlagTitle,
  tagColor,
  tagName,
  useMessageMarks,
  type TaskFlagState,
} from "@/lib/message-tags";

/** 标签前的小色点; 没有颜色的标签不画点。 */
export function TagDot({ color, className }: { color?: string; className?: string }) {
  if (!color) return null;
  return <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", className)} style={{ background: color }} />;
}

/**
 * 一组标签: 浅底小字, 颜色只画在名字前的小圆点上 (同账号颜色)。
 * max 限制显示个数 (列表行里), 其余收成「+N」, 鼠标停留时列出全部。
 */
export function TagChips({
  labels,
  max,
  className,
}: {
  labels: string[] | undefined;
  max?: number;
  className?: string;
}) {
  useI18n();
  const { byKeyword } = useMessageMarks();
  if (!labels || labels.length === 0) return null;
  const shown = max ? labels.slice(0, max) : labels;
  const rest = labels.length - shown.length;
  const all = labels.map((k) => tagName(k, byKeyword)).join(", ");
  return (
    <span className={cn("flex min-w-0 items-center gap-1", className)} title={all}>
      {shown.map((k) => (
        <span
          key={k}
          className="bg-secondary text-muted-foreground inline-flex max-w-32 min-w-0 shrink items-center gap-1 rounded-sm px-1.5 text-xs leading-4"
        >
          <TagDot color={tagColor(k, byKeyword)} />
          <span className="truncate">{tagName(k, byKeyword)}</span>
        </span>
      ))}
      {rest > 0 && <span className="text-muted-foreground shrink-0 text-xs">+{rest}</span>}
    </span>
  );
}

/** 只画色点的标签 (会话展开后的成员行, 地方窄)。 */
export function TagDots({ labels }: { labels: string[] | undefined }) {
  const { byKeyword } = useMessageMarks();
  if (!labels || labels.length === 0) return null;
  return (
    <span className="flex shrink-0 items-center gap-0.5" title={labels.map((k) => tagName(k, byKeyword)).join(", ")}>
      {labels.map((k) => (
        <TagDot key={k} color={tagColor(k, byKeyword) ?? "var(--muted-foreground)"} />
      ))}
    </span>
  );
}

/** 旗标图标: 待办是红色实心旗, 完成是灰色对勾, 没有时是空心旗 (只在鼠标移上行时显示, 由 idleClass 控制)。 */
export function TaskFlagIcon({ state, className, idleClass }: { state: TaskFlagState; className?: string; idleClass?: string }) {
  if (state === "completed") return <Check className={cn("text-muted-foreground", className)} strokeWidth={2.5} />;
  if (state === "flagged") return <Flag className={cn("text-[var(--account-red)]", className)} fill="currentColor" />;
  return <Flag className={cn("text-muted-foreground", idleClass, className)} />;
}

/**
 * 列表行里的旗标或星标按钮。设置里打开「用旗标标记待办」时是旗标 (点一下: 待办 → 完成 → 待办),
 * 否则是原来的星标。
 */
export function FlagButton({
  email,
  idleClass,
  onToggleFlag,
}: {
  email: EmailSummary;
  /** 没有标记时图标的类名, 用于只在鼠标移上行时显示。 */
  idleClass: string;
  /** 点击: 星标模式切换星标, 旗标模式按 nextTaskFlag 走下一步 (由调用方的 toggleFlag 处理)。 */
  onToggleFlag: (email: EmailSummary) => void;
}) {
  useI18n();
  const { taskFlags } = useMessageMarks();
  if (taskFlags) {
    const state = taskFlagState(email);
    return (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onToggleFlag(email);
        }}
        aria-label={taskFlagTitle(state)}
        title={taskFlagTitle(state)}
        className="hover:bg-muted shrink-0 rounded-sm p-0.5"
      >
        <TaskFlagIcon state={state} className="size-3.5" idleClass={idleClass} />
      </button>
    );
  }
  return (
    <button
      type="button"
      onClick={(e) => {
        e.stopPropagation();
        onToggleFlag(email);
      }}
      aria-label={email.isFlagged ? t("取消星标") : t("加星标")}
      className="hover:bg-muted shrink-0 rounded-sm p-0.5"
    >
      <Star
        className={cn(
          "size-3.5",
          email.isFlagged ? "fill-chart-4 text-chart-4" : cn("text-muted-foreground", idleClass),
        )}
      />
    </button>
  );
}

/**
 * 标签菜单底部的输入框: 输入名字 (可以有空格与带音调的字母) 回车, 找到或新建这个标签并打到邮件上。
 */
export function NewTagInput({ known, onAdd }: { known: string[]; onAdd: (keyword: string) => void }) {
  useI18n();
  const marks = useMessageMarks();
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      className="p-1"
      onSubmit={async (e) => {
        e.preventDefault();
        const v = draft.trim();
        if (!v || busy) return;
        setBusy(true);
        try {
          onAdd(await ensureTag(v, marks, known));
          setDraft("");
        } catch (err) {
          toast.error(errorMessage(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <Input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        // 菜单会抢键盘事件用于上下选择，输入框里的按键不能冒泡上去。
        onKeyDown={(e) => e.stopPropagation()}
        placeholder={t("新标签，回车添加")}
        disabled={busy}
        className="h-8 text-sm"
      />
    </form>
  );
}
