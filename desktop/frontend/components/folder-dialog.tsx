"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useEffect, useMemo, useState } from "react";
import { Loader2 } from "lucide-react";
import { toast } from "sonner";

import { OptionSelect } from "@/components/option-select";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { mailboxLabel } from "@/components/mailbox-tree";
import { api, errorMessage, type Mailbox } from "@/lib/api";

export type FolderAction =
  | { kind: "create"; accountId: string; parent?: Mailbox }
  | { kind: "rename"; accountId: string; mailbox: Mailbox }
  | { kind: "move"; accountId: string; mailbox: Mailbox }
  | { kind: "delete"; accountId: string; mailbox: Mailbox };

interface Props {
  action: FolderAction | null;
  mailboxes: Mailbox[];
  onClose: () => void;
  onDone: (accountId: string) => void;
}

const ROOT = "__root__";

/** 新建、重命名、移动、删除文件夹共用一个对话框。 */
export function FolderDialog({ action, mailboxes, onClose, onDone }: Props) {
  useI18n();
  const [name, setName] = useState("");
  const [parent, setParent] = useState(ROOT);
  const [deleteEmails, setDeleteEmails] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!action) return;
    setBusy(false);
    setDeleteEmails(false);
    setName(action.kind === "rename" ? action.mailbox.name : "");
    setParent(action.kind === "create" ? (action.parent?.id ?? ROOT) : action.kind === "move" ? action.mailbox.parentId || ROOT : ROOT);
  }, [action]);

  // 移动时排除自身与所有子孙, 否则会形成环。
  const targets = useMemo(() => {
    if (!action || action.kind !== "move") return mailboxes;
    const banned = new Set([action.mailbox.id]);
    let grew = true;
    while (grew) {
      grew = false;
      for (const m of mailboxes) {
        if (m.parentId && banned.has(m.parentId) && !banned.has(m.id)) {
          banned.add(m.id);
          grew = true;
        }
      }
    }
    return mailboxes.filter((m) => !banned.has(m.id));
  }, [action, mailboxes]);

  if (!action) return null;

  const title = { create: t("新建文件夹"), rename: t("重命名文件夹"), move: t("移动文件夹"), delete: t("删除文件夹") }[action.kind];
  const target = action.kind === "create" ? undefined : action.mailbox;
  const count = target?.totalEmails ?? 0;

  async function submit() {
    if (!action || busy) return;
    const parentId = parent === ROOT ? "" : parent;
    setBusy(true);
    try {
      switch (action.kind) {
        case "create":
          await api.createMailbox(action.accountId, parentId, name);
          toast.success(t("已创建「{0}」", name.trim()));
          break;
        case "rename":
          await api.renameMailbox(action.accountId, action.mailbox.id, name);
          break;
        case "move":
          await api.moveMailbox(action.accountId, action.mailbox.id, parentId);
          toast.success(t("已移动"));
          break;
        case "delete":
          await api.deleteMailbox(action.accountId, action.mailbox.id, deleteEmails);
          toast.success(t("已删除"));
          break;
      }
      onDone(action.accountId);
      onClose();
    } catch (err) {
      toast.error(folderError(err));
      setBusy(false);
    }
  }

  const needsName = action.kind === "create" || action.kind === "rename";
  const disabled = busy || (needsName && !name.trim()) || (action.kind === "delete" && count > 0 && !deleteEmails);

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {target && <DialogDescription>「{mailboxLabel(target)}」</DialogDescription>}
        </DialogHeader>

        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!disabled) submit();
          }}
        >
          {needsName && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="folder-name">{t("名称")}</Label>
              <Input id="folder-name" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder={t("文件夹名称")} />
            </div>
          )}

          {(action.kind === "create" || action.kind === "move") && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="folder-parent">{action.kind === "create" ? t("位置") : t("移动到")}</Label>
              <OptionSelect
                id="folder-parent"
                value={parent}
                onChange={setParent}
                options={[[ROOT, t("（顶层）")], ...targets.map((m) => [m.id, pathOf(m, mailboxes)] as const)]}
                className="w-full"
              />
            </div>
          )}

          {action.kind === "delete" && (
            <div className="flex flex-col gap-3 text-sm">
              <p className="text-muted-foreground">
                {count > 0 ? t("这个文件夹里有 {0} 封邮件。", count) : t("这个文件夹是空的。")}
                {t("有子文件夹时需要先删除或移走子文件夹。删除后无法恢复。")}</p>
              {count > 0 && (
                <label className="flex items-center gap-2">
                  <Checkbox checked={deleteEmails} onCheckedChange={(v) => setDeleteEmails(v === true)} />
                  {t("同时删除其中的邮件（仅存在于此文件夹的邮件将被永久删除）")}</label>
              )}
            </div>
          )}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t("取消")}</Button>
            <Button type="submit" variant={action.kind === "delete" ? "destructive" : "default"} disabled={disabled}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              {action.kind === "delete" ? t("删除") : t("确定")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function pathOf(m: Mailbox, all: Mailbox[]): string {
  const parts = [mailboxLabel(m)];
  let cur = m;
  for (let i = 0; i < 16 && cur.parentId; i++) {
    const p = all.find((x) => x.id === cur.parentId);
    if (!p) break;
    parts.unshift(mailboxLabel(p));
    cur = p;
  }
  return parts.join(" / ");
}

function folderError(err: unknown): string {
  const msg = errorMessage(err);
  if (msg.startsWith("2191")) return t("文件夹里还有邮件，勾选「同时删除其中的邮件」后再删除");
  if (msg.startsWith("2192")) return t("请先删除或移走子文件夹");
  if (msg.startsWith("2193")) return t("文件夹名称不能为空，也不能包含 /");
  if (msg.startsWith("2194")) return t("不能把文件夹移动到它自己里面");
  if (msg.startsWith("2195")) return t("系统文件夹不能删除或移动");
  if (/alreadyExists/i.test(msg)) return t("同一位置已有同名文件夹");
  return msg;
}
