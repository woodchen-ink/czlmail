"use client";

import { useEffect, useState } from "react";
import { Copy, Loader2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api, errorMessage } from "@/lib/api";

interface Props {
  target: { accountId: string; emailId: string } | null;
  onClose: () => void;
}

/**
 * 邮件原文查看。
 *
 * 原文按纯文本放进 <pre>，React 会转义其中的一切 —— 不经过沙箱 iframe 也安全，
 * 因为这里从不把它当 HTML 解析。
 */
export function SourceDialog({ target, onClose }: Props) {
  const [source, setSource] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!target) return;
    let cancelled = false;
    setSource("");
    setLoading(true);
    api
      .viewSource(target.accountId, target.emailId)
      .then((raw) => !cancelled && setSource(raw))
      .catch((err) => !cancelled && toast.error(errorMessage(err)))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [target]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(source);
      toast.success("已复制原文");
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  return (
    <Dialog open={target !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-4xl">
        <DialogHeader className="flex-row items-center justify-between gap-2">
          <DialogTitle>邮件原文</DialogTitle>
          <Button variant="ghost" size="sm" onClick={copy} disabled={!source}>
            <Copy className="size-4" />
            复制
          </Button>
        </DialogHeader>
        {loading ? (
          <div className="text-muted-foreground flex items-center gap-2 py-8 text-sm">
            <Loader2 className="size-4 animate-spin" />
            正在下载原文
          </div>
        ) : (
          <pre className="bg-secondary min-h-0 flex-1 overflow-auto rounded-md p-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap">
            {source}
          </pre>
        )}
      </DialogContent>
    </Dialog>
  );
}
