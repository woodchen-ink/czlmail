"use client";

import { useState } from "react";
import {
  Archive,
  Code2,
  Download,
  Expand,
  FolderInput,
  Forward,
  Keyboard,
  Mail,
  MoreVertical,
  Paperclip,
  Printer,
  Reply,
  ReplyAll,
  ShieldAlert,
  ShieldCheck,
  Shrink,
  Star,
  Sun,
  Tag,
  Trash2,
  Upload,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { EmailDetail, Mailbox } from "@/lib/api";

export interface ToolbarActions {
  reply: () => void;
  replyAll: () => void;
  forward: () => void;
  forwardAsAttachment: () => void;
  archive: () => void;
  trash: () => void;
  move: (mailboxId: string) => void;
  setLabel: (label: string, set: boolean) => void;
  junk: (junk: boolean) => void;
  markUnread: () => void;
  toggleFlag: () => void;
  print: () => void;
  viewSource: () => void;
  exportEml: () => void;
  importEml: () => void;
  showShortcuts: () => void;
  toggleFocus: () => void;
  toggleBodyLight: () => void;
}

interface Props {
  email: EmailDetail;
  mailboxes: Mailbox[];
  /** 当前所在邮箱的类别，垃圾箱里显示「不是垃圾邮件」。 */
  currentKind: string;
  labels: string[];
  focusMode: boolean;
  bodyAlwaysLight: boolean;
  actions: ToolbarActions;
}

export function EmailToolbar({
  email,
  mailboxes,
  currentKind,
  labels,
  focusMode,
  bodyAlwaysLight,
  actions,
}: Props) {
  const inJunk = currentKind === "junk";

  return (
    <div className="border-border flex shrink-0 items-center gap-0.5 overflow-x-auto border-b px-2 py-1.5">
      <Action icon={Reply} label="回复" shortcut="R" onClick={actions.reply} showLabel />
      <Action icon={ReplyAll} label="全部回复" shortcut="A" onClick={actions.replyAll} />
      <Action icon={Forward} label="转发" shortcut="F" onClick={actions.forward} showLabel />

      <Divider />

      <Action icon={Archive} label="归档" shortcut="E" onClick={actions.archive} showLabel />
      <Action icon={Trash2} label="删除" shortcut="#" onClick={actions.trash} showLabel />
      <MoveMenu mailboxes={mailboxes} onMove={actions.move} />
      <LabelMenu current={email.labels ?? []} known={labels} onSet={actions.setLabel} />
      {inJunk ? (
        <Action icon={ShieldCheck} label="不是垃圾邮件" onClick={() => actions.junk(false)} showLabel />
      ) : (
        <Action icon={ShieldAlert} label="垃圾邮件" shortcut="!" onClick={() => actions.junk(true)} showLabel />
      )}
      <Action icon={Mail} label="标为未读" shortcut="U" onClick={actions.markUnread} />

      <div className="min-w-2 flex-1" />

      <Action icon={Printer} label="打印" onClick={actions.print} />
      <Action
        icon={focusMode ? Shrink : Expand}
        label={focusMode ? "退出全屏阅读" : "全屏阅读"}
        shortcut={focusMode ? "Esc" : undefined}
        onClick={actions.toggleFocus}
      />

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="size-8 shrink-0" aria-label="更多操作">
            <MoreVertical className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-52">
          <DropdownMenuItem onSelect={actions.toggleFlag}>
            <Star className="size-4" />
            {email.isFlagged ? "取消星标" : "加星标"}
            <Kbd>S</Kbd>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={actions.viewSource}>
            <Code2 className="size-4" />
            查看源码
          </DropdownMenuItem>
          <DropdownMenuCheckboxItem checked={bodyAlwaysLight} onCheckedChange={actions.toggleBodyLight}>
            <Sun className="size-4" />
            正文始终亮色显示
          </DropdownMenuCheckboxItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={actions.forwardAsAttachment}>
            <Paperclip className="size-4" />
            作为附件转发
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={actions.exportEml}>
            <Download className="size-4" />
            导出为 .eml
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={actions.importEml}>
            <Upload className="size-4" />
            导入 .eml 或 .zip
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={actions.showShortcuts}>
            <Keyboard className="size-4" />
            键盘快捷键
            <Kbd>?</Kbd>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function Action({
  icon: Icon,
  label,
  shortcut,
  onClick,
  showLabel = false,
}: {
  icon: typeof Reply;
  label: string;
  shortcut?: string;
  onClick: () => void;
  showLabel?: boolean;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size={showLabel ? "sm" : "icon"}
          className={showLabel ? "h-8 shrink-0" : "size-8 shrink-0"}
          onClick={onClick}
          aria-label={label}
        >
          <Icon className="size-4" />
          {/* 窄窗口下只留图标，否则工具条会挤出横向滚动。 */}
          {showLabel && <span className="hidden 2xl:inline">{label}</span>}
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {label}
        {shortcut && <span className="text-muted-foreground ml-2 font-mono">{shortcut}</span>}
      </TooltipContent>
    </Tooltip>
  );
}

function MoveMenu({ mailboxes, onMove }: { mailboxes: Mailbox[]; onMove: (id: string) => void }) {
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm" className="h-8 shrink-0" aria-label="移动">
              <FolderInput className="size-4" />
              <span className="hidden 2xl:inline">移动</span>
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>移动到文件夹</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="start" className="max-h-80 w-52 overflow-y-auto">
        <DropdownMenuLabel>移动到</DropdownMenuLabel>
        {mailboxes.map((m) => (
          <DropdownMenuItem key={m.id} onSelect={() => onMove(m.id)}>
            {m.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function LabelMenu({
  current,
  known,
  onSet,
}: {
  current: string[];
  known: string[];
  onSet: (label: string, set: boolean) => void;
}) {
  const [draft, setDraft] = useState("");
  const all = Array.from(new Set([...known, ...current])).sort();

  return (
    <DropdownMenu onOpenChange={(open) => !open && setDraft("")}>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm" className="h-8 shrink-0" aria-label="标签">
              <Tag className="size-4" />
              <span className="hidden 2xl:inline">标签</span>
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent>标签</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuLabel>标签</DropdownMenuLabel>
        {all.length === 0 && (
          <p className="text-muted-foreground px-2 py-1.5 text-xs">还没有标签</p>
        )}
        {all.map((label) => (
          <DropdownMenuCheckboxItem
            key={label}
            checked={current.includes(label)}
            onCheckedChange={(checked) => onSet(label, checked)}
            // 勾选后不关闭菜单，方便一次打多个标签。
            onSelect={(e) => e.preventDefault()}
          >
            {label}
          </DropdownMenuCheckboxItem>
        ))}
        <DropdownMenuSeparator />
        <form
          className="p-1"
          onSubmit={(e) => {
            e.preventDefault();
            const v = draft.trim();
            if (v) onSet(v, true);
            setDraft("");
          }}
        >
          <Input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            // 菜单会抢键盘事件用于上下选择，输入框里的按键不能冒泡上去。
            onKeyDown={(e) => e.stopPropagation()}
            placeholder="新标签（英文），回车添加"
            className="h-8 text-sm"
          />
        </form>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function Divider() {
  return <div className="bg-border mx-1 h-5 w-px shrink-0" />;
}

function Kbd({ children }: { children: React.ReactNode }) {
  return <span className="text-muted-foreground ml-auto font-mono text-xs">{children}</span>;
}
