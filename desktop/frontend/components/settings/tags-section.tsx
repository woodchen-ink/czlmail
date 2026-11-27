"use client";

import { t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Card, Divider, Row, SectionTitle, useSettings } from "@/components/settings/ui";
import { ColorPicker, NameInput } from "@/components/settings/mailboxes-card";
import { api, errorMessage } from "@/lib/api";
import { ensureTag, nextTagColor, useMessageMarksState, type MessageTag } from "@/lib/message-tags";

/**
 * 设置 → 标签: 标签的名字与颜色 (跨设备同步), 以及待办旗标的开关。
 * 改名只改显示名, 邮件上的关键字不变; 删除只删名字与颜色, 已打的标签照旧显示为关键字。
 */
export function TagsSection({ active }: { active: boolean }) {
  useI18n();
  const marks = useMessageMarksState();
  const { settings, save } = useSettings(active);
  const [used, setUsed] = useState<string[]>([]);
  const [draft, setDraft] = useState("");

  // 邮件上出现过、但没有名字的关键字 (别的客户端打的, 或本程序旧版本打的), 可以在这里起名。
  useEffect(() => {
    if (!active) return;
    let cancelled = false;
    api
      .listAccounts()
      .then((accounts) => Promise.all((accounts ?? []).map((a) => api.listLabels(a.id).catch(() => [] as string[]))))
      .then((lists) => {
        if (!cancelled) setUsed([...new Set(lists.flatMap((l) => l ?? []))].sort());
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [active]);

  const unnamed = used.filter((k) => !marks.byKeyword[k]);

  async function saveTag(tag: MessageTag) {
    try {
      await api.saveMessageTag(tag);
    } catch (err) {
      toast.error(errorMessage(err));
    }
    void marks.reload();
  }

  async function remove(tag: MessageTag) {
    if (!window.confirm(t("删除标签「{0}」？已打这个标签的邮件不受影响，只是不再显示名称与颜色。", tag.name))) return;
    try {
      await api.deleteMessageTag(tag.keyword);
    } catch (err) {
      toast.error(errorMessage(err));
    }
    void marks.reload();
  }

  async function add() {
    const v = draft.trim();
    if (!v) return;
    try {
      await ensureTag(v, marks, used);
      setDraft("");
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  return (
    <>
      <SectionTitle
        title={t("标签")}
        desc={t("标签存在服务器上的邮件里，其它邮件客户端也能看到；名称与颜色只在本程序里显示，并通过个人网盘同步到你的其它设备。")}
      />
      <Card className="gap-2">
        {marks.tags.length === 0 && <p className="text-muted-foreground text-sm">{t("还没有标签")}</p>}
        {marks.tags.map((g) => (
          <div key={g.keyword} className="flex items-center gap-2">
            <ColorPicker value={g.color ?? ""} onChange={(color) => saveTag({ ...g, color })} />
            <NameInput value={g.name} placeholder={t("名称")} onCommit={(name) => name && saveTag({ ...g, name })} />
            <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-xs" title={t("服务器上的关键字")}>
              {g.keyword}
            </span>
            <Button variant="ghost" size="icon" className="size-7" aria-label={t("删除")} title={t("删除")} onClick={() => remove(g)}>
              <Trash2 className="size-4" />
            </Button>
          </div>
        ))}
        <Divider />
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void add();
          }}
        >
          <Input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={t("新标签名称")}
            className="h-8 w-56 text-sm"
          />
          <Button type="submit" size="sm" variant="outline" disabled={!draft.trim()}>
            <Plus className="size-4" />
            {t("添加")}
          </Button>
        </form>
      </Card>

      {unnamed.length > 0 && (
        <>
          <SectionTitle title={t("邮件上未命名的标签")} desc={t("其它客户端打的标签。起个名字、挑个颜色后，在列表里显示得更清楚。")} small />
          <Card className="gap-2">
            {unnamed.map((k) => (
              <div key={k} className="flex items-center gap-2">
                <span className="min-w-0 flex-1 truncate font-mono text-sm">{k}</span>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => saveTag({ keyword: k, name: k, color: nextTagColor(marks.tags) } as MessageTag)}
                >
                  {t("命名")}
                </Button>
              </div>
            ))}
          </Card>
        </>
      )}

      <SectionTitle title={t("待办旗标")} small />
      <Card>
        <Row
          title={t("用旗标标记待办")}
          desc={t("同 Outlook：点旗标标记为待办，再点一下标记为已完成，清除旗标在右键菜单里。其它客户端里待办显示为旗标或星标。关闭时显示为星标。")}
        >
          <Switch checked={!!settings?.taskFlags} disabled={!settings} onCheckedChange={(on) => save({ taskFlags: on })} />
        </Row>
      </Card>
    </>
  );
}
