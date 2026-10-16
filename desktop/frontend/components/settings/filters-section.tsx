"use client";

import { dateLocale, t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Code2, ListFilter, Loader2, Pencil, Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { OptionSelect } from "@/components/option-select";
import { AccountPicker, Card, SectionTitle, useAccounts } from "@/components/settings/ui";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { api, errorMessage, type Mailbox } from "@/lib/api";
import { sieve } from "@/wailsjs/go/models";

const getFields: () => ([string, string][]) = () => ([
  ["from", t("发件人")],
  ["to", t("收件人")],
  ["cc", t("抄送")],
  ["subject", t("主题")],
  ["header", t("邮件头")],
  ["body", t("正文")],
  ["size", t("大小")],
  ["attachment", t("附件")],
]);

const getTextComparators: () => ([string, string][]) = () => ([
  ["contains", t("包含")],
  ["not_contains", t("不包含")],
  ["is", t("等于")],
  ["not_is", t("不等于")],
  ["starts_with", t("开头是")],
  ["ends_with", t("结尾是")],
  ["matches", t("通配匹配")],
]);

const getActions: () => ([string, string][]) = () => ([
  ["move", t("移动到文件夹")],
  ["copy", t("复制到文件夹")],
  ["forward", t("转发到")],
  ["mark_read", t("标记为已读")],
  ["star", t("加星标")],
  ["add_label", t("添加标签")],
  ["discard", t("丢弃")],
  ["reject", t("拒收并回复")],
  ["keep", t("保留在收件箱")],
]);

function comparatorsFor(field: string): [string, string][] {
  if (field === "size")
    return [
      ["greater_than", t("大于")],
      ["less_than", t("小于")],
    ];
  if (field === "body")
    return [
      ["contains", t("包含")],
      ["is", t("等于")],
    ];
  if (field === "attachment")
    return [
      ["has_any", t("有任意附件")],
      ["has_type", t("有此类型附件")],
    ];
  return getTextComparators();
}

const valuesOf = (v: unknown): string[] => (Array.isArray(v) ? v : typeof v === "string" ? [v] : []);

/** 过滤规则: 规则编辑器生成 Sieve 脚本, 元数据格式与 Bulwark 相同, 两边可以互相编辑。 */
export function FiltersSection({ active }: { active: boolean }) {
  useI18n();
  const { accounts, accountId, setAccountId } = useAccounts(active);
  const [rules, setRules] = useState<sieve.Rule[] | null>(null);
  const [managed, setManaged] = useState(true);
  const [script, setScript] = useState("");
  const [mode, setMode] = useState<"rules" | "raw">("rules");
  const [editing, setEditing] = useState<sieve.Rule | null>(null);
  const [deleting, setDeleting] = useState<sieve.Rule | null>(null);
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    if (!accountId) return;
    setRules(null);
    try {
      const [state, boxes] = await Promise.all([api.getFilters(accountId), api.listMailboxes(accountId)]);
      setManaged(state.managed);
      setRules(state.rules ?? []);
      setScript(state.script ?? "");
      setMailboxes(boxes ?? []);
      if (!state.managed) setMode("raw");
    } catch (err) {
      setRules([]);
      toast.error(errorMessage(err));
    }
  }, [accountId]);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  const folderPaths = useMemo(() => mailboxPaths(mailboxes), [mailboxes]);

  async function persist(next: sieve.Rule[]) {
    setSaving(true);
    try {
      await api.saveFilters(accountId, next);
      setRules(next);
      setManaged(true);
      const state = await api.getFilters(accountId);
      setScript(state.script ?? "");
      toast.success(t("过滤规则已保存并生效"));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function saveRaw() {
    setSaving(true);
    try {
      await api.saveSieveScript(accountId, script);
      toast.success(t("脚本已校验并生效"));
      load();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSaving(false);
    }
  }

  function move(i: number, dir: -1 | 1) {
    if (!rules) return;
    const next = [...rules];
    const j = i + dir;
    if (j < 0 || j >= next.length) return;
    [next[i], next[j]] = [next[j], next[i]];
    persist(next);
  }

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle title={t("过滤规则")} desc={t("由服务器在收信时执行（Sieve），关机时也生效。规则按顺序匹配，与 Bulwark 网页端共用同一套规则。")} />
        <div className="flex shrink-0 items-center gap-2">
          <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
          <div className="bg-muted flex rounded-md p-0.5 text-xs">
            <button
              type="button"
              onClick={() => setMode("rules")}
              className={cn("flex items-center gap-1 rounded-sm px-2.5 py-1", mode === "rules" ? "bg-card shadow-xs" : "text-muted-foreground")}
            >
              <ListFilter className="size-3.5" />
              {t("规则")}</button>
            <button
              type="button"
              onClick={() => setMode("raw")}
              className={cn("flex items-center gap-1 rounded-sm px-2.5 py-1", mode === "raw" ? "bg-card shadow-xs" : "text-muted-foreground")}
            >
              <Code2 className="size-3.5" />
              {t("Sieve 脚本")}</button>
          </div>
        </div>
      </div>

      {rules === null ? (
        <Card>
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
            <Loader2 className="size-4 animate-spin" />
            {t("载入中")}</div>
        </Card>
      ) : mode === "raw" ? (
        <Card>
          {!managed && (
            <p className="text-muted-foreground text-xs leading-relaxed">
              {t("当前脚本不是由规则编辑器生成的，只能在这里直接编辑。切换到「规则」并保存会用新规则覆盖此脚本。")}</p>
          )}
          <Textarea rows={18} value={script} onChange={(e) => setScript(e.target.value)} className="font-mono text-xs" spellCheck={false} />
          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" onClick={load} disabled={saving}>
              {t("放弃修改")}</Button>
            <Button onClick={saveRaw} disabled={saving}>
              {saving && <Loader2 className="size-4 animate-spin" />}
              {t("校验并保存")}</Button>
          </div>
        </Card>
      ) : (
        <>
          {!managed && (
            <Card>
              <p className="text-muted-foreground text-sm">
                {t("服务器上已有手写的 Sieve 脚本。在这里新建规则并保存会替换它，原脚本可以先在「Sieve 脚本」里复制备份。")}</p>
            </Card>
          )}
          <Card className="gap-0 p-0">
            {rules.length === 0 ? (
              <p className="text-muted-foreground py-8 text-center text-sm">{t("还没有过滤规则")}</p>
            ) : (
              rules.map((r, i) => (
                <div key={r.id || i} className={cn("flex items-center gap-3 px-4 py-2.5", i > 0 && "border-border border-t")}>
                  <Switch
                    checked={r.enabled}
                    disabled={saving}
                    onCheckedChange={(on) => persist(rules.map((x, j) => (j === i ? sieve.Rule.createFrom({ ...x, enabled: on }) : x)))}
                  />
                  <div className="min-w-0 flex-1">
                    <p className={cn("truncate text-sm font-medium", !r.enabled && "text-muted-foreground")}>{r.name || t("(未命名规则)")}</p>
                    <p className="text-muted-foreground truncate text-xs">{describeRule(r)}</p>
                  </div>
                  <Button variant="ghost" size="icon" className="size-7" aria-label={t("上移")} disabled={i === 0 || saving} onClick={() => move(i, -1)}>
                    <ArrowUp className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    aria-label={t("下移")}
                    disabled={i === rules.length - 1 || saving}
                    onClick={() => move(i, 1)}
                  >
                    <ArrowDown className="size-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="size-7" aria-label={t("编辑")} onClick={() => setEditing(r)}>
                    <Pencil className="size-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="hover:text-destructive size-7" aria-label={t("删除")} onClick={() => setDeleting(r)}>
                    <Trash2 className="size-4" />
                  </Button>
                </div>
              ))
            )}
          </Card>
          <div>
            <Button
              size="sm"
              onClick={() =>
                setEditing(
                  sieve.Rule.createFrom({
                    id: "",
                    name: "",
                    enabled: true,
                    matchType: "all",
                    conditions: [{ field: "from", comparator: "contains", value: [""] }],
                    actions: [{ type: "move", value: "" }],
                    stopProcessing: false,
                  }),
                )
              }
            >
              <Plus className="size-4" />
              {t("新建规则")}</Button>
          </div>
        </>
      )}

      {editing && (
        <RuleDialog
          rule={editing}
          folders={folderPaths}
          onClose={() => setEditing(null)}
          onSave={(rule) => {
            const list = rules ?? [];
            const next = rule.id && list.some((r) => r.id === rule.id) ? list.map((r) => (r.id === rule.id ? rule : r)) : [...list, rule];
            setEditing(null);
            persist(next);
          }}
        />
      )}

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("删除规则")}</AlertDialogTitle>
            <AlertDialogDescription>{t("确定删除「{0}」？", deleting?.name)}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("取消")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => deleting && persist((rules ?? []).filter((r) => r !== deleting))}>
              {t("删除")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function RuleDialog({
  rule,
  folders,
  onClose,
  onSave,
}: {
  rule: sieve.Rule;
  folders: string[];
  onClose: () => void;
  onSave: (r: sieve.Rule) => void;
}) {
  useI18n();
  const [r, setR] = useState(() =>
    sieve.Rule.createFrom({
      ...rule,
      conditions: (rule.conditions ?? []).map((c) => ({ ...c, value: valuesOf(c.value) })),
      actions: rule.actions ?? [],
    }),
  );

  const set = (patch: Partial<sieve.Rule>) => setR((prev) => sieve.Rule.createFrom({ ...prev, ...patch }));
  const setCond = (i: number, patch: Partial<sieve.Condition>) =>
    set({ conditions: r.conditions.map((c, j) => (j === i ? sieve.Condition.createFrom({ ...c, ...patch }) : c)) });
  const setAct = (i: number, patch: Partial<sieve.Action>) =>
    set({ actions: r.actions.map((a, j) => (j === i ? sieve.Action.createFrom({ ...a, ...patch }) : a)) });

  function submit() {
    if (!r.name.trim()) {
      toast.error(t("请填写规则名称"));
      return;
    }
    if (r.conditions.length === 0 || r.actions.length === 0) {
      toast.error(t("至少需要一个条件和一个动作"));
      return;
    }
    for (const c of r.conditions) {
      const needsValue = !(c.field === "attachment" && c.comparator === "has_any");
      if (needsValue && !valuesOf(c.value).some((v) => v.trim())) {
        toast.error(t("请填写条件的值"));
        return;
      }
    }
    for (const a of r.actions) {
      if (["move", "copy", "forward", "add_label"].includes(a.type) && !a.value?.trim()) {
        toast.error(t("请填写动作的目标"));
        return;
      }
    }
    onSave(
      sieve.Rule.createFrom({
        ...r,
        id: r.id || `rule-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`,
        conditions: r.conditions.map((c) => ({ ...c, value: valuesOf(c.value).map((v) => v.trim()).filter(Boolean) })),
      }),
    );
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{rule.id ? t("编辑规则") : t("新建规则")}</DialogTitle>
        </DialogHeader>
        <div className="czl-scroll flex max-h-[65vh] flex-col gap-4 overflow-y-auto pr-1">
          <div className="flex flex-col gap-1.5">
            <Label>{t("规则名称")}</Label>
            <Input autoFocus value={r.name} onChange={(e) => set({ name: e.target.value })} placeholder={t("例如：通知邮件归档")} />
          </div>

          <section className="flex flex-col gap-2">
            <div className="flex items-center gap-2 text-sm">
              <span>{t("满足")}</span>
              <OptionSelect
                value={r.matchType}
                onChange={(v) => set({ matchType: v })}
                options={[
                  ["all", t("全部条件")],
                  ["any", t("任一条件")],
                ]}
              />
              <span>{t("时")}</span>
            </div>
            {r.conditions.map((c, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <OptionSelect
                  value={c.field}
                  onChange={(v) => setCond(i, { field: v, comparator: comparatorsFor(v)[0][0] })}
                  options={getFields()}
                  className="w-24"
                />
                {c.field === "header" && (
                  <Input
                    className="h-8 w-36"
                    value={c.headerName ?? ""}
                    onChange={(e) => setCond(i, { headerName: e.target.value })}
                    placeholder={t("邮件头名称")}
                  />
                )}
                <OptionSelect value={c.comparator} onChange={(v) => setCond(i, { comparator: v })} options={comparatorsFor(c.field)} className="w-32" />
                {!(c.field === "attachment" && c.comparator === "has_any") && (
                  <Input
                    className="h-8 min-w-40 flex-1"
                    value={valuesOf(c.value).join(", ")}
                    onChange={(e) => setCond(i, { value: e.target.value.split(/\s*,\s*/) })}
                    placeholder={c.field === "size" ? t("如 5M、500K") : c.field === "attachment" ? t("扩展名，如 pdf") : t("多个值用逗号分隔，任一匹配")}
                  />
                )}
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  aria-label={t("删除条件")}
                  onClick={() => set({ conditions: r.conditions.filter((_, j) => j !== i) })}
                >
                  <X className="size-4" />
                </Button>
              </div>
            ))}
            <Button
              variant="ghost"
              size="sm"
              className="text-accent self-start"
              onClick={() => set({ conditions: [...r.conditions, sieve.Condition.createFrom({ field: "subject", comparator: "contains", value: [""] })] })}
            >
              <Plus className="size-4" />
              {t("添加条件")}</Button>
          </section>

          <section className="flex flex-col gap-2">
            <p className="text-sm">{t("执行以下动作")}</p>
            {r.actions.map((a, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <OptionSelect value={a.type} onChange={(v) => setAct(i, { type: v, value: "" })} options={getActions()} className="w-36" />
                {(a.type === "move" || a.type === "copy") && (
                  <OptionSelect
                    value={a.value ?? ""}
                    onChange={(v) => setAct(i, { value: v })}
                    options={folders.map((f) => [f, f] as const)}
                    placeholder={t("选择文件夹")}
                    className="min-w-40 flex-1"
                  />
                )}
                {(a.type === "forward" || a.type === "add_label" || a.type === "reject") && (
                  <Input
                    className="h-8 min-w-40 flex-1"
                    value={a.value ?? ""}
                    onChange={(e) => setAct(i, { value: e.target.value })}
                    placeholder={a.type === "forward" ? t("转发地址") : a.type === "add_label" ? t("标签名") : t("拒收说明")}
                  />
                )}
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  aria-label={t("删除动作")}
                  onClick={() => set({ actions: r.actions.filter((_, j) => j !== i) })}
                >
                  <X className="size-4" />
                </Button>
              </div>
            ))}
            <Button
              variant="ghost"
              size="sm"
              className="text-accent self-start"
              onClick={() => set({ actions: [...r.actions, sieve.Action.createFrom({ type: "mark_read", value: "" })] })}
            >
              <Plus className="size-4" />
              {t("添加动作")}</Button>
          </section>

          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={r.stopProcessing} onCheckedChange={(v) => set({ stopProcessing: v === true })} />
            {t("匹配后不再处理后续规则")}</label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("取消")}</Button>
          <Button onClick={submit}>{t("保存规则")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function describeRule(r: sieve.Rule): string {
  const f = Object.fromEntries(getFields());
  const cmp = Object.fromEntries([...getTextComparators(), ["greater_than", t("大于")], ["less_than", t("小于")], ["has_any", t("有附件")], ["has_type", t("有附件类型")]]);
  const act = Object.fromEntries(getActions());
  const conds = (r.conditions ?? [])
    .map((c) => `${c.field === "header" ? c.headerName : f[c.field] ?? c.field}${cmp[c.comparator] ?? c.comparator}${valuesOf(c.value).join("/")}`)
    .join(r.matchType === "any" ? t(" 或 ") : t(" 且 "));
  const acts = (r.actions ?? []).map((a) => `${act[a.type] ?? a.type}${a.value ? ` ${a.value}` : ""}`).join("，");
  return `${conds} → ${acts}`;
}

/** 文件夹的 Sieve 路径: 父子用 / 连接。 */
function mailboxPaths(list: Mailbox[]): string[] {
  const byId = new Map(list.map((m) => [m.id, m]));
  const path = (m: Mailbox): string => {
    const parts = [m.name];
    let cur = m;
    for (let i = 0; i < 16 && cur.parentId && byId.has(cur.parentId); i++) {
      cur = byId.get(cur.parentId)!;
      parts.unshift(cur.name);
    }
    return parts.join("/");
  };
  return list
    .filter((m) => m.kind !== "drafts" && m.kind !== "scheduled")
    .map(path)
    .sort((a, b) => a.localeCompare(b, dateLocale()));
}
