"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { useI18n } from "@/components/locale-provider";
import { Switch } from "@/components/ui/switch";
import { Card, Row, SectionTitle } from "@/components/settings/ui";
import { errorMessage } from "@/lib/api";
import { flushNotes, noteApi, type NoteConfig } from "@/lib/notes/api";
import { t } from "@/lib/i18n";

/** Local module and MCP permissions are independent of mail settings and cross-device preferences. */
export function NotesSection({ active }: { active: boolean }) {
  useI18n();
  const [config, setConfig] = useState<NoteConfig | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (active) void noteApi.config().then(setConfig).catch((err) => toast.error(errorMessage(err))); }, [active]);
  /** Persist before changing visible navigation; a failed editor flush aborts disabling. */
  async function save(patch: Partial<NoteConfig>) {
    if (!config || busy) return;
    setBusy(true);
    try {
      if (patch.enabled === false) await flushNotes();
      const next = { ...config, ...patch };
      await noteApi.configure(next); setConfig(next);
    } catch (err) { toast.error(errorMessage(err)); }
    finally { setBusy(false); }
  }
  return <>
    <SectionTitle title={t("笔记")} desc={t("本机 Markdown 笔记与附件管理，后台同步到账号的文件存储。")} />
    <Card>
      <Row title={t("启用笔记")} desc={t("默认开启。关闭后隐藏入口并暂停笔记同步，保留全部本地和远端文件。此开关只影响当前设备。")}>
        <Switch aria-label={t("启用笔记")} checked={config?.enabled ?? true} disabled={!config || busy} onCheckedChange={(enabled) => void save({ enabled })} />
      </Row>
      <Row title={t("允许 MCP 读取笔记")} desc={t("需要同时开启 MCP 服务。关闭笔记工具不会撤销通用网盘工具原有的文件访问权限。")}>
        <Switch aria-label={t("允许 MCP 读取笔记")} checked={config?.allowRead ?? false} disabled={!config?.enabled || busy} onCheckedChange={(allowRead) => void save({ allowRead })} />
      </Row>
      <Row title={t("允许 MCP 修改笔记")} desc={t("允许 AI 新建、编辑、导入和软删除笔记。默认关闭，所有更新仍需版本校验。")}>
        <Switch aria-label={t("允许 MCP 修改笔记")} checked={config?.allowWrite ?? false} disabled={!config?.enabled || !config.allowRead || busy} onCheckedChange={(allowWrite) => void save({ allowWrite })} />
      </Row>
    </Card>
  </>;
}
