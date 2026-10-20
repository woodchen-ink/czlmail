"use client";

import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { useI18n } from "@/components/locale-provider";
import { Switch } from "@/components/ui/switch";
import { Button } from "@/components/ui/button";
import { Card, Row, SectionTitle } from "@/components/settings/ui";
import { errorMessage } from "@/lib/api";
import { flushNotes, noteApi, type NoteConfig } from "@/lib/notes/api";
import { t } from "@/lib/i18n";

/** Local module and MCP permissions are independent of mail settings and cross-device preferences. */
export function NotesSection({ active }: { active: boolean }) {
  useI18n();
  const [config, setConfig] = useState<NoteConfig | null>(null);
  const [busy, setBusy] = useState(false);
  const locked = useRef(false);
  useEffect(() => { if (active) void noteApi.config().then(setConfig).catch((err) => toast.error(errorMessage(err))); }, [active]);
  /** Persist before changing visible navigation; a failed editor flush aborts disabling. */
  async function save(patch: Partial<NoteConfig>) {
    if (!config || locked.current) return;
    locked.current = true; setBusy(true);
    try {
      if (patch.enabled === false) await flushNotes();
      const next = { ...config, ...patch };
      await noteApi.configure(next); setConfig(next);
    } catch (err) { toast.error(errorMessage(err)); }
    finally { locked.current = false; setBusy(false); }
  }
  /** Low-frequency transfers live in settings and always flush the shared editor first. */
  async function transfer(kind: "import" | "export") {
    if (!config?.enabled || locked.current) return;
    locked.current = true; setBusy(true);
    try {
      await flushNotes();
      const vault = (await noteApi.vaults())?.[0];
      if (!vault) { toast.error(t("请先在笔记中完成初始化")); return; }
      if (kind === "import") {
        const result = await noteApi.importFolder(vault.id);
        toast.success(t("已导入 {0} 个文件，跳过 {1} 个", result.imported, result.skipped));
      } else {
        const path = await noteApi.exportVault(vault.id);
        if (path) toast.success(t("已保存到 {0}", path));
      }
    } catch (err) { toast.error(errorMessage(err)); }
    finally { locked.current = false; setBusy(false); }
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
    <Card>
      <Row title={t("导入笔记")} desc={t("从本机文件夹导入 Markdown 和附件，保留目录结构。")}>
        <Button variant="outline" disabled={!config?.enabled || busy} onClick={() => void transfer("import")}>{t("导入")}</Button>
      </Row>
      <Row title={t("导出笔记库")} desc={t("导出全部笔记和附件为普通文件，可用于备份或迁移。")}>
        <Button variant="outline" disabled={!config?.enabled || busy} onClick={() => void transfer("export")}>{t("导出")}</Button>
      </Row>
    </Card>
  </>;
}
