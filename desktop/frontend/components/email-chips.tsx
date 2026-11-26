"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { File, FileArchive, FileImage, FileText, KeyRound } from "lucide-react";
import { toast } from "sonner";

import { errorMessage } from "@/lib/api";
import { cn } from "@/lib/utils";

/** 列表行里最多列出的附件名, 其余合成「+N」。 */
const MAX_ATTACHMENTS = 2;

/**
 * 验证码标签: 点击复制。列表行与阅读栏共用, 在列表里不能触发选中整行。
 */
export function OtpChip({ code, size = "sm" }: { code: string; size?: "sm" | "md" }) {
  useI18n();
  async function copy(e: React.MouseEvent) {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(code);
      toast.success(t("已复制验证码"));
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }
  return (
    <button
      type="button"
      onClick={copy}
      onKeyDown={(e) => e.stopPropagation()}
      title={t("复制验证码")}
      aria-label={t("复制验证码")}
      className={cn(
        "border-border bg-card hover:border-accent inline-flex shrink-0 items-center gap-1 rounded-md border font-mono tabular-nums transition-colors",
        size === "md" ? "h-7 px-2 text-sm" : "h-5 px-1.5 text-[11px]",
      )}
    >
      <KeyRound className={cn("text-accent shrink-0", size === "md" ? "size-3.5" : "size-3")} />
      {code}
    </button>
  );
}

/** 附件名标签: 文件名过长时截断主名, 保留扩展名 (发票、报表常常只靠扩展名区分)。 */
export function AttachmentChips({ names }: { names: string[] }) {
  useI18n();
  const shown = names.slice(0, MAX_ATTACHMENTS);
  const rest = names.length - shown.length;
  return (
    <>
      {shown.map((name, i) => {
        const dot = name.lastIndexOf(".");
        const ext = dot > 0 && name.length - dot <= 6 ? name.slice(dot) : "";
        const base = ext ? name.slice(0, dot) : name;
        const Icon = iconFor(ext);
        return (
          <span
            key={i}
            title={name}
            className="border-border bg-card text-muted-foreground inline-flex h-5 max-w-40 min-w-0 shrink items-center gap-1 rounded-md border px-1.5 text-[11px]"
          >
            <Icon className={cn("size-3 shrink-0", ext.toLowerCase() === ".pdf" && "text-destructive")} />
            <span className="min-w-0 truncate">{base}</span>
            {ext && <span className="shrink-0">{ext}</span>}
          </span>
        );
      })}
      {rest > 0 && (
        <span
          className="text-muted-foreground shrink-0 text-[11px] tabular-nums"
          title={names.slice(MAX_ATTACHMENTS).join("\n")}
          aria-label={t("另有 {0} 个附件", rest)}
        >
          +{rest}
        </span>
      )}
    </>
  );
}

function iconFor(ext: string) {
  const e = ext.toLowerCase();
  if (/^\.(png|jpe?g|gif|webp|heic|bmp|svg|tiff?)$/.test(e)) return FileImage;
  if (/^\.(zip|rar|7z|gz|tgz|tar|bz2|xz)$/.test(e)) return FileArchive;
  if (/^\.(pdf|docx?|odt|rtf|txt|md|xlsx?|ods|csv|pptx?|odp|ofd|xml)$/.test(e)) return FileText;
  return File;
}
