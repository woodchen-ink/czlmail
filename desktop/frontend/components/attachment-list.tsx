"use client";

import { useEffect, useState } from "react";
import {
  Download,
  Eye,
  File,
  FileArchive,
  FileImage,
  FileSpreadsheet,
  FileText,
  FolderOpen,
  FolderDown,
  Loader2,
  SquareArrowOutUpRight,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { api, blobUrl, errorMessage, isPreviewable, type Attachment } from "@/lib/api";
import { fileSize } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Props {
  accountId: string;
  emailId: string;
  attachments: Attachment[];
}

/** 折叠状态下直接显示的附件数，其余收进「+N 附件」。 */
const COLLAPSED_COUNT = 2;

/**
 * 附件栏，位于正文上方。
 *
 * 整块附件都是点击区域：点击即下载到本地附件目录并用系统程序打开，再次点击直接复用。
 * 右键给出预览、在文件夹中显示、另存为。内嵌在正文里的 cid 图片不重复列出。
 */
export function AttachmentList({ accountId, emailId, attachments }: Props) {
  const [preview, setPreview] = useState<Attachment | null>(null);
  const [busy, setBusy] = useState("");
  const [expanded, setExpanded] = useState(false);

  // 切换邮件时收起。
  useEffect(() => setExpanded(false), [emailId]);

  const visible = attachments.filter((a) => !a.inline);
  if (visible.length === 0) return null;

  const shown = expanded ? visible : visible.slice(0, COLLAPSED_COUNT);
  const hidden = visible.length - shown.length;

  async function run(key: string, fn: () => Promise<void>) {
    setBusy(key);
    try {
      await fn();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy("");
    }
  }

  const open = (a: Attachment) =>
    run(a.blobId, async () => {
      const res = await api.openAttachment(accountId, emailId, a.blobId);
      if (res.revealed) toast.warning("可执行文件不会直接打开，已在文件夹中选中，请确认来源可信后再运行");
    });

  const reveal = (a: Attachment) =>
    run(a.blobId, async () => {
      await api.revealAttachment(accountId, emailId, a.blobId);
    });

  const saveAs = (a: Attachment) =>
    run(a.blobId, async () => {
      const path = await api.saveAttachment(accountId, a.blobId, a.name || "attachment");
      if (path) toast.success(`已保存到 ${path}`);
    });

  const downloadAll = () =>
    run("all", async () => {
      await api.downloadAllAttachments(accountId, emailId);
    });

  return (
    <>
      <div className="flex items-start gap-2">
        <ul className="flex min-w-0 flex-1 flex-wrap gap-2">
          {shown.map((a) => (
            <li key={a.blobId + a.partId}>
              <AttachmentChip
                attachment={a}
                busy={busy === a.blobId}
                onOpen={() => open(a)}
                onPreview={() => setPreview(a)}
                onReveal={() => reveal(a)}
                onSaveAs={() => saveAs(a)}
              />
            </li>
          ))}
          {hidden > 0 && (
            <li>
              <Button variant="outline" size="sm" className="h-9" onClick={() => setExpanded(true)}>
                +{hidden} 附件
              </Button>
            </li>
          )}
        </ul>

        {visible.length > 1 && (
          <Button variant="outline" size="sm" className="h-9 shrink-0" onClick={downloadAll} disabled={busy === "all"}>
            {busy === "all" ? <Loader2 className="size-4 animate-spin" /> : <FolderDown className="size-4" />}
            全部下载
          </Button>
        )}
      </div>

      <AttachmentPreview
        accountId={accountId}
        attachment={preview}
        onClose={() => setPreview(null)}
        onOpen={(a) => open(a)}
      />
    </>
  );
}

function AttachmentChip({
  attachment: a,
  busy,
  onOpen,
  onPreview,
  onReveal,
  onSaveAs,
}: {
  attachment: Attachment;
  busy: boolean;
  onOpen: () => void;
  onPreview: () => void;
  onReveal: () => void;
  onSaveAs: () => void;
}) {
  const Icon = iconFor(a);
  const canPreview = isPreviewable(a.type);

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          role="button"
          tabIndex={0}
          title={`${a.name || "(未命名附件)"}\n点击打开，右键更多操作`}
          onClick={onOpen}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              onOpen();
            }
          }}
          className={cn(
            "group border-border bg-card flex h-9 max-w-72 cursor-pointer items-center gap-2 rounded-md border pr-1.5 pl-2.5 text-sm transition-colors",
            "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
          )}
        >
          {busy ? (
            <Loader2 className="text-muted-foreground size-4 shrink-0 animate-spin" />
          ) : (
            <Icon className="text-muted-foreground size-4 shrink-0" />
          )}
          <span className="min-w-0 truncate">{a.name || "(未命名附件)"}</span>

          {/* 大小与快捷操作占同一位置：平时显示大小，悬停时换成下载与预览。 */}
          <span className="text-muted-foreground shrink-0 text-xs tabular-nums group-hover:hidden group-focus-visible:hidden">
            {fileSize(a.size)}
          </span>
          <span className="hidden shrink-0 items-center group-hover:flex group-focus-visible:flex">
            <ChipAction label="另存为" onClick={onSaveAs}>
              <Download className="size-3.5" />
            </ChipAction>
            {canPreview && (
              <ChipAction label="预览" onClick={onPreview}>
                <Eye className="size-3.5" />
              </ChipAction>
            )}
          </span>
        </div>
      </ContextMenuTrigger>

      <ContextMenuContent>
        <ContextMenuItem onSelect={onOpen}>
          <SquareArrowOutUpRight />
          打开
        </ContextMenuItem>
        {canPreview && (
          <ContextMenuItem onSelect={onPreview}>
            <Eye />
            预览
          </ContextMenuItem>
        )}
        <ContextMenuItem onSelect={onReveal}>
          <FolderOpen />
          在文件夹中显示
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onSelect={onSaveAs}>
          <Download />
          另存为…
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function ChipAction({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={(e) => {
        // 不冒泡到整块的「打开」。
        e.stopPropagation();
        onClick();
      }}
      className="text-muted-foreground hover:bg-muted hover:text-foreground rounded-sm p-1"
    >
      {children}
    </button>
  );
}

function iconFor(a: Attachment) {
  const type = a.type.toLowerCase();
  const ext = (a.name.split(".").pop() ?? "").toLowerCase();
  if (type.startsWith("image/")) return FileImage;
  if (/zip|rar|7z|tar|gzip/.test(type) || ["zip", "rar", "7z", "tar", "gz"].includes(ext)) return FileArchive;
  if (/sheet|excel|csv/.test(type) || ["xls", "xlsx", "csv"].includes(ext)) return FileSpreadsheet;
  if (type.startsWith("text/") || type === "application/pdf" || /word|document/.test(type)) return FileText;
  return File;
}

function AttachmentPreview({
  accountId,
  attachment,
  onClose,
  onOpen,
}: {
  accountId: string;
  attachment: Attachment | null;
  onClose: () => void;
  onOpen: (a: Attachment) => void;
}) {
  const [text, setText] = useState<string | null>(null);
  const type = attachment?.type.split(";")[0].trim().toLowerCase() ?? "";
  const url = attachment ? blobUrl(accountId, attachment) : "";

  // 纯文本不交给 iframe 渲染，而是取回内容放进 <pre>，由 React 转义 ——
  // 避免任何被误标为 text/plain 的 HTML 有机会被解析。
  useEffect(() => {
    setText(null);
    if (!attachment || type !== "text/plain") return;
    let cancelled = false;
    fetch(url)
      .then((r) => r.text())
      .then((t) => !cancelled && setText(t))
      .catch((err) => !cancelled && toast.error(errorMessage(err)));
    return () => {
      cancelled = true;
    };
  }, [attachment, type, url]);

  return (
    <Dialog open={attachment !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[90vh] flex-col sm:max-w-5xl">
        <DialogHeader className="flex-row items-center justify-between gap-2 pr-8">
          <DialogTitle className="truncate">{attachment?.name || "附件"}</DialogTitle>
          {attachment && (
            <Button variant="ghost" size="sm" onClick={() => onOpen(attachment)}>
              <SquareArrowOutUpRight className="size-4" />
              用系统程序打开
            </Button>
          )}
        </DialogHeader>

        <div className="bg-secondary flex min-h-0 flex-1 items-center justify-center overflow-auto rounded-md">
          {type.startsWith("image/") && (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={url} alt={attachment?.name ?? ""} className="max-h-[75vh] max-w-full object-contain" />
          )}
          {type === "text/plain" &&
            (text === null ? (
              <Loader2 className="text-muted-foreground size-5 animate-spin" />
            ) : (
              <pre className="h-[75vh] w-full overflow-auto p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap">{text}</pre>
            ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
