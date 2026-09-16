"use client";

import { useMemo } from "react";
import {
  Archive,
  CloudDownload,
  CalendarClock,
  FileText,
  Folder,
  FolderInput,
  FolderPlus,
  Inbox,
  Pencil,
  Send,
  ShieldAlert,
  Trash2,
} from "lucide-react";

import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import type { Mailbox } from "@/lib/api";

interface Props {
  mailboxes: Mailbox[];
  selectedId: string;
  onSelect: (id: string) => void;
  /** 右键「从服务器拉取全部邮件」。label 是界面上显示的文件夹名。 */
  onPull?: (id: string, label: string) => void;
  /** 右键文件夹管理。未提供时不显示这些菜单项。 */
  onManage?: (action: "create" | "rename" | "move" | "delete", mailbox: Mailbox) => void;
  /** 整棵树的额外缩进，供共享账号的文件夹挂在账号名下方。 */
  indent?: number;
}

/**
 * 按类别（kind）而非名称决定图标、中文名与排序。kind 由后端给出：
 * 服务器有 role 就用 role，没有则按常见名称识别；识别不出的是自建文件夹，保留原名。
 */
// 颜色类名必须是完整字面量，Tailwind 按源码文本扫描生成样式，拼接出来的类名不会生效。
const KIND_META: Record<string, { icon: typeof Inbox; label: string; color: string }> = {
  inbox: { icon: Inbox, label: "收件箱", color: "text-folder-inbox" },
  drafts: { icon: FileText, label: "草稿", color: "text-folder-drafts" },
  scheduled: { icon: CalendarClock, label: "已计划", color: "text-folder-scheduled" },
  sent: { icon: Send, label: "已发送", color: "text-folder-sent" },
  archive: { icon: Archive, label: "归档", color: "text-folder-archive" },
  junk: { icon: ShieldAlert, label: "垃圾邮件", color: "text-folder-junk" },
  trash: { icon: Trash2, label: "已删除", color: "text-folder-trash" },
};

/** 常用邮箱的固定顺序。服务端的 sortOrder 多数为 0，排不出有意义的次序。 */
/** 界面上显示的文件夹名: 系统文件夹用中文名, 自建文件夹保留原名。 */
export function mailboxLabel(m: Pick<Mailbox, "kind" | "name">): string {
  return KIND_META[m.kind]?.label ?? m.name;
}

const KIND_ORDER = ["inbox", "drafts", "scheduled", "sent", "archive", "junk", "trash"];

interface Node extends Mailbox {
  children: Node[];
  depth: number;
}

export function MailboxTree({ mailboxes, selectedId, onSelect, onPull, onManage, indent = 0 }: Props) {
  const nodes = useMemo(() => buildTree(mailboxes), [mailboxes]);

  return (
    <nav className="flex flex-col gap-0.5" aria-label="邮箱">
      {nodes.map((node) => (
        <MailboxRow
          key={node.id}
          node={node}
          selected={node.id === selectedId}
          onSelect={onSelect}
          onPull={onPull}
          onManage={onManage}
          indent={indent}
        />
      ))}
    </nav>
  );
}

function MailboxRow({
  node,
  selected,
  onSelect,
  onPull,
  onManage,
  indent,
}: {
  node: Node;
  selected: boolean;
  onSelect: (id: string) => void;
  onPull?: (id: string, label: string) => void;
  onManage?: Props["onManage"];
  indent: number;
}) {
  const meta = KIND_META[node.kind];
  const Icon = meta?.icon ?? Folder;
  const label = meta?.label ?? node.name;

  const row = (
    <button
      type="button"
      onClick={() => onSelect(node.id)}
      aria-current={selected ? "page" : undefined}
      className={cn(
        "group flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition-colors",
        "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
        selected && "bg-muted font-medium",
      )}
      style={{ paddingLeft: `${(node.depth + indent) * 12 + 8}px` }}
    >
      <Icon className={cn("size-4 shrink-0", meta?.color ?? "text-muted-foreground")} />
      <span className="min-w-0 flex-1 truncate" title={node.name}>
        {label}
      </span>
      {node.unreadEmails > 0 && (
        <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
          {node.unreadEmails}
        </span>
      )}
    </button>
  );

  if (!onPull && !onManage) return row;
  // 有 role 的系统文件夹不能改名、移动、删除。
  const custom = !node.role;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>{row}</ContextMenuTrigger>
      <ContextMenuContent className="w-52">
        {onPull && (
          <ContextMenuItem onSelect={() => onPull(node.id, label)}>
            <CloudDownload />
            从服务器拉取全部邮件
          </ContextMenuItem>
        )}
        {onManage && (
          <>
            {onPull && <ContextMenuSeparator />}
            <ContextMenuItem onSelect={() => onManage("create", node)}>
              <FolderPlus />
              新建子文件夹
            </ContextMenuItem>
            {custom && (
              <>
                <ContextMenuItem onSelect={() => onManage("rename", node)}>
                  <Pencil />
                  重命名
                </ContextMenuItem>
                <ContextMenuItem onSelect={() => onManage("move", node)}>
                  <FolderInput />
                  移动到…
                </ContextMenuItem>
                <ContextMenuSeparator />
                <ContextMenuItem variant="destructive" onSelect={() => onManage("delete", node)}>
                  <Trash2 />
                  删除文件夹
                </ContextMenuItem>
              </>
            )}
          </>
        )}
      </ContextMenuContent>
    </ContextMenu>
  );
}

/**
 * 把平表建成树后再拍平成带缩进层级的列表。
 *
 * 渲染成扁平列表而不是嵌套 DOM，是为了让键盘上下键的移动顺序与视觉顺序一致 ——
 * 嵌套结构下 Tab 序会在展开折叠时跳变。
 */
function buildTree(mailboxes: Mailbox[]): Node[] {
  const byId = new Map<string, Node>();
  for (const m of mailboxes) {
    byId.set(m.id, { ...m, children: [], depth: 0 });
  }

  const roots: Node[] = [];
  for (const node of byId.values()) {
    // 父邮箱可能因为未订阅而不在列表里，此时把它当作顶层，
    // 否则整棵子树会连同其中的未读邮件一起从界面上消失。
    const parent = node.parentId ? byId.get(node.parentId) : undefined;
    if (parent) {
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }

  sortNodes(roots);

  const flat: Node[] = [];
  const walk = (list: Node[], depth: number) => {
    for (const node of list) {
      node.depth = depth;
      flat.push(node);
      sortNodes(node.children);
      walk(node.children, depth + 1);
    }
  };
  walk(roots, 0);

  return flat;
}

function sortNodes(list: Node[]) {
  list.sort((a, b) => {
    const ra = KIND_ORDER.indexOf(a.kind);
    const rb = KIND_ORDER.indexOf(b.kind);
    // 有角色的排在自建文件夹前面，各自内部再按名称。
    if (ra !== rb) {
      if (ra === -1) return 1;
      if (rb === -1) return -1;
      return ra - rb;
    }
    return a.name.localeCompare(b.name, "zh-CN");
  });
}
