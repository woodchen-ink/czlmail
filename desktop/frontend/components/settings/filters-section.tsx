"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, Code2, ListFilter, Loader2, Pencil, Plus, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { AccountPicker, Card, NativeSelect, SectionTitle, useAccounts } from "@/components/settings/ui";
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

const FIELDS: [string, string][] = [
  ["from", "发件人"],
  ["to", "收件人"],
  ["cc", "抄送"],
  ["subject", "主题"],
  ["header", "邮件头"],
  ["body", "正文"],
  ["size", "大小"],
  ["attachment", "附件"],
];

const TEXT_COMPARATORS: [string, string][] = [
  ["contains", "包含"],
  ["not_contains", "不包含"],
  ["is", "等于"],
  ["not_is", "不等于"],
  ["starts_with", "开头是"],
  ["ends_with", "结尾是"],
  ["matches", "通配匹配"],
];

const ACTIONS: [string, string][] = [
  ["move", "移动到文件夹"],
  ["copy", "复制到文件夹"],
  ["forward", "转发到"],
  ["mark_read", "标记为已读"],
  ["star", "加星标"],
  ["add_label", "添加标签"],
  ["discard", "丢弃"],
  ["reject", "拒收并回复"],
  ["keep", "保留在收件箱"],
];

function comparatorsFor(field: string): [string, string][] {
  if (field === "size")
    return [
      ["greater_than", "大于"],
      ["less_than", "小于"],
    ];
  if (field === "body")
    return [
      ["contains", "包含"],
      ["is", "等于"],
    ];
  if (field === "attachment")
    return [
      ["has_any", "有任意附件"],
      ["has_type", "有此类型附件"],
    ];
  return TEXT_COMPARATORS;
}

const valuesOf = (v: unknown): string[] => (Array.isArray(v) ? v : typeof v === "string" ? [v] : []);

/** 过滤规则: 规则编辑器生成 Sieve 脚本, 元数据格式与 Bulwark 相同, 两边可以互相编辑。 */
export function FiltersSection({ active }: { active: boolean }) {
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
      toast.success("过滤规则已保存并生效");
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
      toast.success("脚本已校验并生效");
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
        <SectionTitle title="过滤规则" desc="由服务器在收信时执行（Sieve），关机时也生效。规则按顺序匹配，与 Bulwark 网页端共用同一套规则。" />
        <div className="flex shrink-0 items-center gap-2">
          <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
          <div className="bg-muted flex rounded-md p-0.5 text-xs">
            <button
              type="button"
              onClick={() => setMode("rules")}
              className={cn("flex items-center gap-1 rounded-sm px-2.5 py-1", mode === "rules" ? "bg-card shadow-xs" : "text-muted-foreground")}
            >
              <ListFilter className="size-3.5" />
              规则
            </button>
            <button
              type="button"
              onClick={() => setMode("raw")}
              className={cn("flex items-center gap-1 rounded-sm px-2.5 py-1", mode === "raw" ? "bg-card shadow-xs" : "text-muted-foreground")}
            >
              <Code2 className="size-3.5" />
              Sieve 脚本
            </button>
          </div>
        </div>
      </div>

      {rules === null ? (
        <Card>
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
            <Loader2 className="size-4 animate-spin" />
            载入中
          </div>
        </Card>
      ) : mode === "raw" ? (
        <Card>
          {!managed && (
            <p className="text-muted-foreground text-xs leading-relaxed">
              当前脚本不是由规则编辑器生成的，只能在这里直接编辑。切换到「规则」并保存会用新规则覆盖此脚本。
            </p>
          )}
          <Textarea rows={18} value={script} onChange={(e) => setScript(e.target.value)} className="font-mono text-xs" spellCheck={false} />
          <div className="flex items-center justify-end gap-2">
            <Button variant="outline" onClick={load} disabled={saving}>
              放弃修改
            </Button>
            <Button onClick={saveRaw} disabled={saving}>
              {saving && <Loader2 className="size-4 animate-spin" />}
              校验并保存
            </Button>
          </div>
        </Card>
      ) : (
        <>
          {!managed && (
            <Card>
              <p className="text-muted-foreground text-sm">
                服务器上已有手写的 Sieve 脚本。在这里新建规则并保存会替换它，原脚本可以先在「Sieve 脚本」里复制备份。
              </p>
            </Card>
          )}
          <Card className="gap-0 p-0">
            {rules.length === 0 ? (
              <p className="text-muted-foreground py-8 text-center text-sm">还没有过滤规则</p>
            ) : (
              rules.map((r, i) => (
                <div key={r.id || i} className={cn("flex items-center gap-3 px-4 py-2.5", i > 0 && "border-border border-t")}>
                  <Switch
                    checked={r.enabled}
                    disabled={saving}
                    onCheckedChange={(on) => persist(rules.map((x, j) => (j === i ? sieve.Rule.createFrom({ ...x, enabled: on }) : x)))}
                  />
                  <div className="min-w-0 flex-1">
                    <p className={cn("truncate text-sm font-medium", !r.enabled && "text-muted-foreground")}>{r.name || "(未命名规则)"}</p>
                    <p className="text-muted-foreground truncate text-xs">{describeRule(r)}</p>
                  </div>
                  <Button variant="ghost" size="icon" className="size-7" aria-label="上移" disabled={i === 0 || saving} onClick={() => move(i, -1)}>
                    <ArrowUp className="size-4" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="size-7"
                    aria-label="下移"
                    disabled={i === rules.length - 1 || saving}
                    onClick={() => move(i, 1)}
                  >
                    <ArrowDown className="size-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="size-7" aria-label="编辑" onClick={() => setEditing(r)}>
                    <Pencil className="size-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="hover:text-destructive size-7" aria-label="删除" onClick={() => setDeleting(r)}>
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
              新建规则
            </Button>
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
            <AlertDialogTitle>删除规则</AlertDialogTitle>
            <AlertDialogDescription>确定删除「{deleting?.name}」？</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => deleting && persist((rules ?? []).filter((r) => r !== deleting))}>
              删除
            </AlertDialogAction>
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
      toast.error("请填写规则名称");
      return;
    }
    if (r.conditions.length === 0 || r.actions.length === 0) {
      toast.error("至少需要一个条件和一个动作");
      return;
    }
    for (const c of r.conditions) {
      const needsValue = !(c.field === "attachment" && c.comparator === "has_any");
      if (needsValue && !valuesOf(c.value).some((v) => v.trim())) {
        toast.error("请填写条件的值");
        return;
      }
    }
    for (const a of r.actions) {
      if (["move", "copy", "forward", "add_label"].includes(a.type) && !a.value?.trim()) {
        toast.error("请填写动作的目标");
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
          <DialogTitle>{rule.id ? "编辑规则" : "新建规则"}</DialogTitle>
        </DialogHeader>
        <div className="czl-scroll flex max-h-[65vh] flex-col gap-4 overflow-y-auto pr-1">
          <div className="flex flex-col gap-1.5">
            <Label>规则名称</Label>
            <Input autoFocus value={r.name} onChange={(e) => set({ name: e.target.value })} placeholder="例如：通知邮件归档" />
          </div>

          <section className="flex flex-col gap-2">
            <div className="flex items-center gap-2 text-sm">
              <span>满足</span>
              <NativeSelect
                value={r.matchType}
                onChange={(v) => set({ matchType: v })}
                options={[
                  ["all", "全部条件"],
                  ["any", "任一条件"],
                ]}
              />
              <span>时</span>
            </div>
            {r.conditions.map((c, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <NativeSelect
                  value={c.field}
                  onChange={(v) => setCond(i, { field: v, comparator: comparatorsFor(v)[0][0] })}
                  options={FIELDS}
                  className="w-24"
                />
                {c.field === "header" && (
                  <Input
                    className="h-8 w-36"
                    value={c.headerName ?? ""}
                    onChange={(e) => setCond(i, { headerName: e.target.value })}
                    placeholder="邮件头名称"
                  />
                )}
                <NativeSelect value={c.comparator} onChange={(v) => setCond(i, { comparator: v })} options={comparatorsFor(c.field)} className="w-32" />
                {!(c.field === "attachment" && c.comparator === "has_any") && (
                  <Input
                    className="h-8 min-w-40 flex-1"
                    value={valuesOf(c.value).join(", ")}
                    onChange={(e) => setCond(i, { value: e.target.value.split(/\s*,\s*/) })}
                    placeholder={c.field === "size" ? "如 5M、500K" : c.field === "attachment" ? "扩展名，如 pdf" : "多个值用逗号分隔，任一匹配"}
                  />
                )}
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  aria-label="删除条件"
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
              添加条件
            </Button>
          </section>

          <section className="flex flex-col gap-2">
            <p className="text-sm">执行以下动作</p>
            {r.actions.map((a, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2">
                <NativeSelect value={a.type} onChange={(v) => setAct(i, { type: v, value: "" })} options={ACTIONS} className="w-36" />
                {(a.type === "move" || a.type === "copy") && (
                  <NativeSelect
                    value={a.value ?? ""}
                    onChange={(v) => setAct(i, { value: v })}
                    options={[["", "选择文件夹"], ...folders.map((f) => [f, f] as [string, string])]}
                    className="min-w-40 flex-1"
                  />
                )}
                {(a.type === "forward" || a.type === "add_label" || a.type === "reject") && (
                  <Input
                    className="h-8 min-w-40 flex-1"
                    value={a.value ?? ""}
                    onChange={(e) => setAct(i, { value: e.target.value })}
                    placeholder={a.type === "forward" ? "转发地址" : a.type === "add_label" ? "标签名" : "拒收说明"}
                  />
                )}
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-8"
                  aria-label="删除动作"
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
              添加动作
            </Button>
          </section>

          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={r.stopProcessing} onCheckedChange={(v) => set({ stopProcessing: v === true })} />
            匹配后不再处理后续规则
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={submit}>保存规则</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function describeRule(r: sieve.Rule): string {
  const f = Object.fromEntries(FIELDS);
  const cmp = Object.fromEntries([...TEXT_COMPARATORS, ["greater_than", "大于"], ["less_than", "小于"], ["has_any", "有附件"], ["has_type", "有附件类型"]]);
  const act = Object.fromEntries(ACTIONS);
  const conds = (r.conditions ?? [])
    .map((c) => `${c.field === "header" ? c.headerName : f[c.field] ?? c.field}${cmp[c.comparator] ?? c.comparator}${valuesOf(c.value).join("/")}`)
    .join(r.matchType === "any" ? " 或 " : " 且 ");
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
    .sort((a, b) => a.localeCompare(b, "zh-CN"));
}
