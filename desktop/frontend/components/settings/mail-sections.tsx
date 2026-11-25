"use client";

import { dateLocale, t, tContext } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useCallback, useEffect, useMemo, useState } from "react";
import { CalendarClock, Folder, FolderPlus, Loader2, MoreHorizontal, ShieldCheck, Star, X } from "lucide-react";
import { toast } from "sonner";

import { RichEditor } from "@/components/compose/rich-editor";
import { FolderDialog, type FolderAction } from "@/components/folder-dialog";
import { mailboxLabel } from "@/components/mailbox-tree";
import { AccountPicker, Card, Divider, Row, SectionTitle, useAccounts, useSettings } from "@/components/settings/ui";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { api, errorMessage, type Account, type Identity, type Mailbox, type ScheduledItem, type SigningCertificate, type Vacation } from "@/lib/api";
import { htmlToText, isHtmlEmpty, sanitizeInline, textToHtml } from "@/lib/html";
import { main } from "@/wailsjs/go/models";

/* ---------------- 发件身份与签名 ---------------- */

export function IdentitiesSection({ active }: { active: boolean }) {
  useI18n();
  const { settings, save } = useSettings(active);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [identities, setIdentities] = useState<Record<string, Identity[]>>({});
  const [editing, setEditing] = useState<{ accountId: string; identity: Identity } | null>(null);
  // null = 系统不支持签名证书存储, 不显示签名设置。
  const [certs, setCerts] = useState<SigningCertificate[] | null>(null);
  const [smimeFor, setSmimeFor] = useState<Identity | null>(null);

  const load = useCallback(async () => {
    try {
      const accs = (await api.listAccounts()) ?? [];
      setAccounts(accs);
      const entries = await Promise.all(accs.map(async (a) => [a.id, (await api.listIdentities(a.id).catch(() => [])) ?? []] as const));
      setIdentities(Object.fromEntries(entries));
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  useEffect(() => {
    if (!active) return;
    api
      .listSigningCertificates()
      .then((list) => setCerts(list ?? []))
      .catch(() => setCerts(null));
  }, [active]);

  return (
    <>
      <SectionTitle
        title={t("发件身份与签名")}
        desc={t("发件身份由服务器管理员配置。签名保存在服务器的发件身份上，写信时按所选发件人自动附上，与 Bulwark 网页端共用。")}
      />
      {accounts.map((acc) => {
        const list = identities[acc.id] ?? [];
        if (list.length === 0) return null;
        const def = settings?.defaultIdentities?.[acc.id] || list[0].id;
        return (
          <Card key={acc.id} className="gap-0 p-0">
            <p className="text-muted-foreground border-border border-b px-4 py-2 text-xs">{acc.name}</p>
            {list.map((id, i) => {
              const isDefault = id.id === def;
              return (
                <div key={id.id} className={cn("flex items-start gap-3 px-4 py-3", i > 0 && "border-border border-t")}>
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-2 truncate text-sm font-medium">
                      {id.name ? `${id.name} <${id.email}>` : id.email}
                      {isDefault && <span className="bg-muted rounded-sm px-1.5 py-0.5 text-[11px] font-normal">{t("默认")}</span>}
                    </p>
                    {id.htmlSignature?.trim() ? (
                      <div
                        className="czl-signature text-muted-foreground mt-1.5 max-h-24 overflow-hidden text-xs"
                        dangerouslySetInnerHTML={{ __html: sanitizeInline(id.htmlSignature) }}
                      />
                    ) : (
                      <p className="text-muted-foreground mt-1 line-clamp-3 text-xs whitespace-pre-wrap">{id.textSignature || t("未设置签名")}</p>
                    )}
                    {certs && <SmimeSummary identity={id} certs={certs} choice={settings?.smimeSigning?.[id.email.toLowerCase()]} />}
                  </div>
                  <div className="flex shrink-0 gap-1">
                    {!isDefault && list.length > 1 && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={!settings}
                        onClick={() => save({ defaultIdentities: { ...(settings?.defaultIdentities ?? {}), [acc.id]: id.id } })}
                      >
                        <Star className="size-4" />
                        {t("设为默认")}</Button>
                    )}
                    <Button variant="outline" size="sm" onClick={() => setEditing({ accountId: acc.id, identity: id })}>
                      {t("编辑签名")}</Button>
                    {certs && (
                      <Button variant="outline" size="sm" disabled={!settings} onClick={() => setSmimeFor(id)}>
                        <ShieldCheck className="size-4" />
                        {t("数字签名")}</Button>
                    )}
                  </div>
                </div>
              );
            })}
          </Card>
        );
      })}

      {smimeFor && certs && settings && (
        <SmimeDialog
          identity={smimeFor}
          certs={certs}
          choice={settings.smimeSigning?.[smimeFor.email.toLowerCase()]}
          onClose={() => setSmimeFor(null)}
          onSave={(choice) => {
            const next = { ...(settings.smimeSigning ?? {}) };
            const key = smimeFor.email.toLowerCase();
            if (choice) next[key] = main.SmimeChoice.createFrom(choice);
            else delete next[key];
            save({ smimeSigning: next });
            setSmimeFor(null);
          }}
        />
      )}

      {editing && (
        <SignatureDialog
          accountId={editing.accountId}
          identity={editing.identity}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
    </>
  );
}

/** 证书能否给这个地址签名: 没有问题码且证书上有这个地址。 */
function certFits(c: SigningCertificate, email: string) {
  return !c.problem && (c.emails ?? []).some((e) => e.toLowerCase() === email.toLowerCase());
}

function certDate(v: unknown) {
  return new Date(String(v)).toLocaleDateString(dateLocale());
}

function SmimeSummary({ identity, certs, choice }: { identity: Identity; certs: SigningCertificate[]; choice?: main.SmimeChoice }) {
  useI18n();
  if (!choice?.thumbprint) return null;
  const cert = certs.find((c) => c.thumbprint === choice.thumbprint);
  const ok = !!cert && certFits(cert, identity.email);
  return (
    <p className={cn("mt-1.5 flex items-center gap-1.5 text-xs", ok ? "text-muted-foreground" : "text-destructive")}>
      <ShieldCheck className="size-3.5 shrink-0" />
      <span className="truncate">
        {!cert
          ? t("签名证书已不在系统证书存储中")
          : !ok
            ? t("签名证书不能用于这个地址：{0}", cert.subject)
            : choice.auto
              ? t("数字签名：{0}，有效期至 {1}，默认签名", cert.subject, certDate(cert.notAfter))
              : t("数字签名：{0}，有效期至 {1}", cert.subject, certDate(cert.notAfter))}
      </span>
    </p>
  );
}

function SmimeDialog({
  identity,
  certs,
  choice,
  onClose,
  onSave,
}: {
  identity: Identity;
  certs: SigningCertificate[];
  choice?: main.SmimeChoice;
  onClose: () => void;
  onSave: (choice: { thumbprint: string; auto: boolean } | null) => void;
}) {
  useI18n();
  const usable = certs.filter((c) => certFits(c, identity.email));
  const others = certs.length - usable.length;
  const [thumbprint, setThumbprint] = useState(
    choice?.thumbprint && usable.some((c) => c.thumbprint === choice.thumbprint) ? choice.thumbprint : (usable[0]?.thumbprint ?? ""),
  );
  const [auto, setAuto] = useState(choice?.auto ?? false);

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("数字签名 (S/MIME)")}</DialogTitle>
        </DialogHeader>
        <p className="text-muted-foreground text-sm">
          {t("用系统证书存储里的证书给 {0} 发出的邮件签名。收件人能确认邮件确实来自你、途中没有被改动。", identity.email)}
        </p>
        {usable.length === 0 ? (
          <p className="text-sm">{t("系统证书存储里没有可用于这个地址的签名证书（需要带私钥、包含这个邮箱地址且仍在有效期内）。")}</p>
        ) : (
          <div className="grid gap-1.5" role="radiogroup" aria-label={t("签名证书")}>
            {usable.map((c) => (
              <label
                key={c.thumbprint}
                className={cn("border-border flex cursor-pointer items-start gap-2.5 rounded-md border px-3 py-2", thumbprint === c.thumbprint && "border-primary")}
              >
                <input type="radio" name="smime-cert" className="mt-1" checked={thumbprint === c.thumbprint} onChange={() => setThumbprint(c.thumbprint)} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{c.subject}</span>
                  <span className="text-muted-foreground block truncate text-xs">{t("颁发者：{0}，有效期至 {1}", c.issuer, certDate(c.notAfter))}</span>
                </span>
              </label>
            ))}
          </div>
        )}
        {others > 0 && <p className="text-muted-foreground text-xs">{t("另有 {0} 张证书不能用于这个地址（已过期、不含这个地址或不能签名邮件）。", others)}</p>}
        {usable.length > 0 && (
          <div className="flex items-center justify-between gap-3">
            <Label htmlFor="smime-auto" className="text-sm font-normal">
              {t("写信时默认签名")}</Label>
            <Switch id="smime-auto" checked={auto} onCheckedChange={setAuto} />
          </div>
        )}
        <DialogFooter>
          {choice?.thumbprint && (
            <Button variant="ghost" className="sm:mr-auto" onClick={() => onSave(null)}>
              {t("不再签名")}</Button>
          )}
          <Button variant="outline" onClick={onClose}>
            {t("取消")}</Button>
          <Button disabled={!thumbprint} onClick={() => onSave({ thumbprint, auto })}>
            {t("保存")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function SignatureDialog({
  accountId,
  identity,
  onClose,
  onSaved,
}: {
  accountId: string;
  identity: Identity;
  onClose: () => void;
  onSaved: () => void;
}) {
  useI18n();
  const initial = identity.htmlSignature?.trim() ? identity.htmlSignature : identity.textSignature ? textToHtml(identity.textSignature) : "";
  // 带内联样式的签名(常见于从其它客户端复制的 HTML 签名)用可视化编辑会丢样式, 默认进源码模式。
  const [mode, setMode] = useState<"visual" | "source">(/style=|<table/i.test(initial) ? "source" : "visual");
  const [html, setHtml] = useState(initial);
  const [busy, setBusy] = useState(false);

  async function save() {
    setBusy(true);
    try {
      const clean = isHtmlEmpty(html) ? "" : sanitizeInline(html);
      await api.updateSignatureHTML(accountId, identity.id, clean, htmlToText(clean));
      toast.success(t("签名已保存"));
      onSaved();
    } catch (err) {
      toast.error(errorMessage(err));
      setBusy(false);
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("编辑签名")}</DialogTitle>
        </DialogHeader>
        <div className="flex items-center gap-2">
          <p className="text-muted-foreground min-w-0 flex-1 truncate text-sm">{identity.email}</p>
          <div className="bg-muted flex rounded-md p-0.5 text-xs">
            {(
              [
                ["visual", t("可视化")],
                ["source", t("HTML 源码")],
              ] as const
            ).map(([k, label]) => (
              <button
                key={k}
                type="button"
                onClick={() => setMode(k)}
                className={cn("rounded-sm px-2.5 py-1", mode === k ? "bg-card shadow-xs" : "text-muted-foreground")}
              >
                {label}
              </button>
            ))}
          </div>
        </div>
        {mode === "visual" ? (
          <div className="border-border h-72 overflow-hidden rounded-md border">
            <RichEditor key="visual" content={html} placeholder={t("签名内容")} onChange={setHtml} className="h-full" />
          </div>
        ) : (
          <div className="grid gap-2">
            <Textarea rows={8} value={html} onChange={(e) => setHtml(e.target.value)} className="font-mono text-xs" spellCheck={false} />
            <p className="text-muted-foreground text-xs">{t("预览")}</p>
            <div
              className="czl-signature border-border max-h-48 overflow-auto rounded-md border p-3 text-sm"
              dangerouslySetInnerHTML={{ __html: sanitizeInline(html) }}
            />
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("取消")}</Button>
          <Button onClick={save} disabled={busy}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            {t("保存")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ---------------- 自动回复 ---------------- */

export function VacationSection({ active }: { active: boolean }) {
  useI18n();
  const { accounts, accountId, setAccountId } = useAccounts(active);
  const [v, setV] = useState<Vacation | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!active || !accountId) return;
    setV(null);
    api
      .getVacation(accountId)
      .then(setV)
      .catch((err) => toast.error(errorMessage(err)));
  }, [active, accountId]);

  function update<K extends keyof Vacation>(key: K, value: Vacation[K]) {
    setV((prev) => (prev ? main.Vacation.createFrom({ ...prev, [key]: value }) : prev));
  }

  async function save() {
    if (!v) return;
    setBusy(true);
    try {
      await api.saveVacation(accountId, main.Vacation.createFrom({ ...v, htmlBody: "" }));
      toast.success(v.enabled ? t("自动回复已开启") : t("已保存"));
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle title={t("自动回复")} desc={t("休假或外出时自动回复来信。由服务器执行，关机时也会生效；同一发件人在一段时间内只回复一次。")} />
        <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
      </div>
      {!v ? (
        <Card>
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
            <Loader2 className="size-4 animate-spin" />
            {t("载入中")}</div>
        </Card>
      ) : (
        <Card>
          <Row title={t("开启自动回复")}>
            <Switch checked={v.enabled} onCheckedChange={(on) => update("enabled", on)} />
          </Row>
          <Divider />
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1.5">
              <Label>{t("开始日期（可选）")}</Label>
              <Input type="date" value={isoToDate(v.fromDate)} onChange={(e) => update("fromDate", dateToIso(e.target.value, false))} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>{t("结束日期（可选）")}</Label>
              <Input type="date" value={isoToDate(v.toDate)} onChange={(e) => update("toDate", dateToIso(e.target.value, true))} />
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>{t("主题")}</Label>
            <Input value={v.subject} onChange={(e) => update("subject", e.target.value)} placeholder={t("留空时使用「自动回复：原主题」")} />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label>{t("回复内容")}</Label>
            <Textarea
              rows={6}
              value={v.textBody || htmlToText(v.htmlBody)}
              onChange={(e) => update("textBody", e.target.value)}
              placeholder={t("您好，我目前不在办公室，将于 X 月 X 日返回，届时会尽快回复您。")}
            />
          </div>
          <div className="flex justify-end">
            <Button onClick={save} disabled={busy}>
              {busy && <Loader2 className="size-4 animate-spin" />}
              {t("保存")}</Button>
          </div>
        </Card>
      )}
    </>
  );
}

function isoToDate(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** 本地日期转 UTC 时间: 开始取当天零点, 结束取当天最后一刻。 */
function dateToIso(date: string, endOfDay: boolean): string {
  if (!date) return "";
  const [y, m, d] = date.split("-").map(Number);
  const local = endOfDay ? new Date(y, m - 1, d, 23, 59, 59) : new Date(y, m - 1, d, 0, 0, 0);
  return local.toISOString().replace(/\.\d{3}Z$/, "Z");
}

/* ---------------- 文件夹 ---------------- */

export function FoldersSection({ active }: { active: boolean }) {
  useI18n();
  const { accounts, accountId, setAccountId } = useAccounts(active);
  const [mailboxes, setMailboxes] = useState<Mailbox[]>([]);
  const [action, setAction] = useState<FolderAction | null>(null);

  const load = useCallback(async () => {
    if (!accountId) return;
    try {
      setMailboxes((await api.listMailboxes(accountId)) ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, [accountId]);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  const rows = useMemo(() => flatten(mailboxes), [mailboxes]);

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle title={tContext("heading", "文件夹")} desc={t("系统文件夹（收件箱、已发送等）不能改名或删除。也可以在邮件侧栏里右键文件夹管理。")} />
        <div className="flex shrink-0 items-center gap-2">
          <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
          <Button size="sm" onClick={() => setAction({ kind: "create", accountId })} disabled={!accountId}>
            <FolderPlus className="size-4" />
            {t("新建文件夹")}</Button>
        </div>
      </div>
      <Card className="gap-0 p-0">
        {rows.map(({ m, depth }, i) => (
          <div key={m.id} className={cn("flex items-center gap-2 px-4 py-2", i > 0 && "border-border border-t")}>
            <Folder className="text-muted-foreground size-4 shrink-0" style={{ marginLeft: depth * 16 }} />
            <span className="min-w-0 flex-1 truncate text-sm">{mailboxLabel(m)}</span>
            <span className="text-muted-foreground text-xs tabular-nums">
              {m.totalEmails}  {t("封")}{m.unreadEmails > 0 ? t(" · {0} 未读", m.unreadEmails) : ""}
            </span>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="size-7" aria-label={t("操作")}>
                  <MoreHorizontal className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-40">
                <DropdownMenuItem onSelect={() => setAction({ kind: "create", accountId, parent: m })}>{t("新建子文件夹")}</DropdownMenuItem>
                {!m.role && (
                  <>
                    <DropdownMenuItem onSelect={() => setAction({ kind: "rename", accountId, mailbox: m })}>{t("重命名")}</DropdownMenuItem>
                    <DropdownMenuItem onSelect={() => setAction({ kind: "move", accountId, mailbox: m })}>{t("移动到…")}</DropdownMenuItem>
                    <DropdownMenuSeparator />
                    <DropdownMenuItem variant="destructive" onSelect={() => setAction({ kind: "delete", accountId, mailbox: m })}>
                      {t("删除")}</DropdownMenuItem>
                  </>
                )}
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        ))}
      </Card>
      <FolderDialog action={action} mailboxes={mailboxes} onClose={() => setAction(null)} onDone={() => load()} />
    </>
  );
}

function flatten(list: Mailbox[]): { m: Mailbox; depth: number }[] {
  const children = new Map<string, Mailbox[]>();
  const ids = new Set(list.map((m) => m.id));
  for (const m of list) {
    const p = m.parentId && ids.has(m.parentId) ? m.parentId : "";
    children.set(p, [...(children.get(p) ?? []), m]);
  }
  const order = ["inbox", "drafts", "scheduled", "sent", "archive", "junk", "trash"];
  const sort = (a: Mailbox, b: Mailbox) => {
    const ra = order.indexOf(a.kind);
    const rb = order.indexOf(b.kind);
    if (ra !== rb) return (ra < 0 ? 99 : ra) - (rb < 0 ? 99 : rb);
    return a.name.localeCompare(b.name, dateLocale());
  };
  const out: { m: Mailbox; depth: number }[] = [];
  const walk = (parent: string, depth: number) => {
    for (const m of (children.get(parent) ?? []).sort(sort)) {
      out.push({ m, depth });
      walk(m.id, depth + 1);
    }
  };
  walk("", 0);
  return out;
}

/* ---------------- 定时发送 ---------------- */

export function ScheduledSection({ active }: { active: boolean }) {
  useI18n();
  const { accounts, accountId, setAccountId } = useAccounts(active);
  const [items, setItems] = useState<ScheduledItem[] | null>(null);

  const load = useCallback(async () => {
    if (!accountId) return;
    try {
      setItems((await api.listScheduledSends(accountId)) ?? []);
    } catch (err) {
      setItems([]);
      toast.error(errorMessage(err));
    }
  }, [accountId]);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  async function cancel(it: ScheduledItem) {
    try {
      await api.cancelScheduledSend(accountId, it.submissionId, it.emailId);
      toast.success(t("已取消发送，邮件已移回草稿"));
      load();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle title={tContext("heading", "定时发送")} desc={t("等待发出的邮件保存在服务器上，关机也会按时发送。取消后邮件回到草稿。")} />
        <AccountPicker accounts={accounts} value={accountId} onChange={setAccountId} />
      </div>
      <Card className="gap-0 p-0">
        {items === null ? (
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-6 text-sm">
            <Loader2 className="size-4 animate-spin" />
            {t("载入中")}</div>
        ) : items.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">{t("没有等待发送的邮件")}</p>
        ) : (
          items.map((it, i) => (
            <div key={it.submissionId} className={cn("flex items-center gap-3 px-4 py-2.5", i > 0 && "border-border border-t")}>
              <CalendarClock className="text-muted-foreground size-4 shrink-0" />
              <span className="min-w-0 flex-1 truncate text-sm">{it.subject || t("(无主题)")}</span>
              <span className="text-muted-foreground text-xs tabular-nums">{new Date(it.sendAt).toLocaleString(dateLocale())}</span>
              <Button variant="ghost" size="sm" onClick={() => cancel(it)}>
                <X className="size-4" />
                {t("取消发送")}</Button>
            </div>
          ))
        )}
      </Card>
    </>
  );
}
