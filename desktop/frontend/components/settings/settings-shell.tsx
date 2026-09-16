"use client";

import { useCallback, useEffect, useState } from "react";
import {
  AppWindow,
  Bell,
  BookOpen,
  BookUser,
  CalendarClock,
  CalendarDays,
  Download,
  ExternalLink,
  FileText,
  Folder,
  Code2,
  IdCard,
  Info,
  ListFilter,
  Loader2,
  LogOut,
  MessageSquare,
  Palette,
  PenLine,
  Plane,
  Plus,
  RefreshCw,
  ShieldCheck,
  Sparkles,
  Trash2,
  Upload,
  UserRound,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
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
import { cn } from "@/lib/utils";
import {
  Events,
  api,
  errorMessage,
  onEvent,
  type Account,
  type SessionStatus,
  type Template,
  type UpdateInfo,
} from "@/lib/api";
import { BrowserOpenURL } from "@/wailsjs/runtime/runtime";
import { AISection } from "@/components/settings/ai-section";
import { FiltersSection } from "@/components/settings/filters-section";
import {
  AppearanceSection,
  ComposeSection,
  IntegrationSection,
  NotificationsSection,
  ReadingSection,
} from "@/components/settings/general-sections";
import { FoldersSection, IdentitiesSection, ScheduledSection, VacationSection } from "@/components/settings/mail-sections";
import { AddressBooksSection, CalendarsSection } from "@/components/settings/pim-sections";
import { Card, Divider, Row, SectionTitle, useSettings } from "@/components/settings/ui";
import { store } from "@/wailsjs/go/models";

type Section =
  | "account"
  | "appearance"
  | "notifications"
  | "integration"
  | "reading"
  | "compose"
  | "ai"
  | "identities"
  | "vacation"
  | "filters"
  | "templates"
  | "folders"
  | "scheduled"
  | "privacy"
  | "calendars"
  | "addressbooks"
  | "about";

const GROUPS: { title: string; items: { id: Section; label: string; icon: typeof Palette }[] }[] = [
  {
    title: "通用",
    items: [
      { id: "account", label: "账户", icon: UserRound },
      { id: "appearance", label: "外观", icon: Palette },
      { id: "notifications", label: "通知", icon: Bell },
      { id: "integration", label: "默认应用与启动", icon: AppWindow },
      { id: "reading", label: "阅读", icon: BookOpen },
      { id: "compose", label: "写信", icon: PenLine },
      { id: "ai", label: "AI 助手", icon: Sparkles },
    ],
  },
  {
    title: "邮件",
    items: [
      { id: "identities", label: "发件身份与签名", icon: IdCard },
      { id: "vacation", label: "自动回复", icon: Plane },
      { id: "filters", label: "过滤规则", icon: ListFilter },
      { id: "templates", label: "邮件模板", icon: FileText },
      { id: "folders", label: "文件夹", icon: Folder },
      { id: "scheduled", label: "定时发送", icon: CalendarClock },
      { id: "privacy", label: "内容与发件人", icon: ShieldCheck },
    ],
  },
  {
    title: "日历与通讯录",
    items: [
      { id: "calendars", label: "日历", icon: CalendarDays },
      { id: "addressbooks", label: "通讯录", icon: BookUser },
    ],
  },
  {
    title: "其它",
    items: [{ id: "about", label: "关于与更新", icon: Info }],
  },
];

export const GITHUB_URL = "https://github.com/woodchen-ink/czlmail";
export const FORUM_URL = "https://sunai.net/t/topic/1485";

export function SettingsShell({ active, onSignedOut }: { active: boolean; onSignedOut: () => void }) {
  const [section, setSection] = useState<Section>("account");
  const on = (id: Section) => active && section === id;

  return (
    <div className="flex h-full">
      <nav className="border-border bg-sidebar czl-scroll flex w-56 shrink-0 flex-col gap-0.5 overflow-y-auto border-r p-2" aria-label="设置">
        <h1 className="px-2 py-2 text-base font-semibold">设置</h1>
        {GROUPS.map((g) => (
          <div key={g.title} className="mb-2 flex flex-col gap-0.5">
            <p className="text-muted-foreground px-2 pt-1 pb-0.5 text-xs">{g.title}</p>
            {g.items.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => setSection(s.id)}
                className={cn(
                  "flex items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm transition-colors",
                  "hover:bg-secondary focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                  section === s.id && "bg-muted font-medium",
                )}
              >
                <s.icon className="text-muted-foreground size-4" />
                {s.label}
              </button>
            ))}
          </div>
        ))}
      </nav>

      <div className="czl-scroll min-w-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
          {section === "account" && <AccountSection active={on("account")} onSignedOut={onSignedOut} />}
          {section === "appearance" && <AppearanceSection active={on("appearance")} />}
          {section === "notifications" && <NotificationsSection active={on("notifications")} />}
          {section === "integration" && <IntegrationSection active={on("integration")} />}
          {section === "reading" && <ReadingSection active={on("reading")} />}
          {section === "compose" && <ComposeSection active={on("compose")} />}
          {section === "ai" && <AISection active={on("ai")} />}
          {section === "identities" && <IdentitiesSection active={on("identities")} />}
          {section === "vacation" && <VacationSection active={on("vacation")} />}
          {section === "filters" && <FiltersSection active={on("filters")} />}
          {section === "templates" && <TemplateSection active={on("templates")} />}
          {section === "folders" && <FoldersSection active={on("folders")} />}
          {section === "scheduled" && <ScheduledSection active={on("scheduled")} />}
          {section === "privacy" && <PrivacySection active={on("privacy")} />}
          {section === "calendars" && <CalendarsSection active={on("calendars")} />}
          {section === "addressbooks" && <AddressBooksSection active={on("addressbooks")} />}
          {section === "about" && <AboutSection active={on("about")} />}
        </div>
      </div>
    </div>
  );
}

/* ---------------- 隐私 ---------------- */

function PrivacySection({ active }: { active: boolean }) {
  const { settings, save } = useSettings(active);
  const [accountId, setAccountId] = useState("");
  const [senders, setSenders] = useState<string[]>([]);
  const [adding, setAdding] = useState("");
  const [filter, setFilter] = useState("");

  const load = useCallback(async () => {
    try {
      const accounts = (await api.listAccounts()) ?? [];
      const personal = accounts.find((a) => a.isPersonal) ?? accounts[0];
      if (!personal) return;
      setAccountId(personal.id);
      setSenders((await api.listTrustedSenders(personal.id)) ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  async function add() {
    const addr = adding.trim().toLowerCase();
    if (!/^[^@\s]+@[^@\s]+$/.test(addr)) {
      toast.error("请输入完整的邮箱地址");
      return;
    }
    try {
      await api.trustSender(accountId, addr);
      setAdding("");
      setSenders((s) => [...new Set([...s, addr])].sort());
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function remove(addr: string) {
    try {
      await api.untrustSender(accountId, addr);
      setSenders((s) => s.filter((x) => x !== addr));
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  const shown = senders.filter((s) => s.includes(filter.trim().toLowerCase()));

  return (
    <>
      <SectionTitle title="隐私与安全" desc="远程图片默认全部拦截：加载图片会让发件人知道你何时、在哪打开了邮件。" />
      <Card>
        <Row title="信任通讯录里的联系人" desc="来自通讯录联系人的邮件自动加载图片">
          <Switch
            checked={!!settings?.trustAddressBookSenders}
            disabled={!settings}
            onCheckedChange={(on) => save({ trustAddressBookSenders: on })}
          />
        </Row>
      </Card>

      <SectionTitle
        title="信任的发件人"
        desc="这些地址的邮件会直接加载图片。名单保存在服务器的「Trusted Senders」通讯录里，与 Bulwark 网页端及其它设备共用。"
        small
      />
      <Card>
        <div className="flex gap-2">
          <Input
            value={adding}
            onChange={(e) => setAdding(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && add()}
            placeholder="添加邮箱地址"
          />
          <Button onClick={add} disabled={!adding.trim()}>
            <Plus className="size-4" />
            添加
          </Button>
        </div>
        {senders.length > 8 && <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder={`在 ${senders.length} 个地址中筛选`} />}
        {shown.length === 0 ? (
          <p className="text-muted-foreground py-4 text-center text-sm">{senders.length ? "没有匹配的地址" : "还没有信任的发件人"}</p>
        ) : (
          <ul className="border-border max-h-96 overflow-y-auto rounded-md border">
            {shown.map((addr, i) => (
              <li key={addr} className={cn("flex items-center gap-2 px-3 py-1.5 text-sm", i > 0 && "border-border border-t")}>
                <span className="min-w-0 flex-1 truncate">{addr}</span>
                <Button variant="ghost" size="icon" className="size-7" aria-label={`移除 ${addr}`} onClick={() => remove(addr)}>
                  <X className="size-4" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  );
}

/* ---------------- 模板 ---------------- */

const BUILTIN_PLACEHOLDERS = [
  { name: "date", label: "今天日期" },
  { name: "day_of_week", label: "星期" },
  { name: "sender_name", label: "你的名字" },
  { name: "recipient_name", label: "收件人名字" },
];

function TemplateSection({ active }: { active: boolean }) {
  const [templates, setTemplates] = useState<Template[]>([]);
  const [editing, setEditing] = useState<Template | null>(null);
  const [deleting, setDeleting] = useState<Template | null>(null);

  const load = useCallback(async () => {
    try {
      setTemplates((await api.listTemplates()) ?? []);
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }, []);

  useEffect(() => {
    if (active) load();
  }, [active, load]);

  useEffect(() => onEvent(Events.templatesChanged, load), [load]);

  async function importFile() {
    try {
      const n = await api.importTemplatesFile();
      if (n > 0) {
        toast.success(`已导入 ${n} 个模板`);
        load();
      }
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function remove(t: Template) {
    try {
      await api.deleteTemplate(t.id);
      load();
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  return (
    <>
      <div className="flex items-end justify-between gap-4">
        <SectionTitle
          title="邮件模板"
          desc="写信时从模板开始，可以使用 {{占位符}}。模板保存在网盘的「CZL Mail」文件夹里，自动同步到你的其它设备。"
        />
        <div className="flex shrink-0 gap-2">
          <Button variant="outline" size="sm" onClick={importFile} title="支持本程序与 Bulwark 导出的模板文件">
            <Upload className="size-4" />
            导入
          </Button>
          <Button size="sm" onClick={() => setEditing(store.Template.createFrom({ id: "", name: "", category: "", subject: "", body: "" }))}>
            <Plus className="size-4" />
            新建模板
          </Button>
        </div>
      </div>

      {templates.length === 0 ? (
        <Card>
          <p className="text-muted-foreground py-6 text-center text-sm">还没有模板</p>
        </Card>
      ) : (
        <Card className="gap-0 p-0">
          {templates.map((t, i) => (
            <div key={t.id} className={cn("flex items-center gap-3 px-4 py-3", i > 0 && "border-border border-t")}>
              <FileText className="text-muted-foreground size-4 shrink-0" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {t.name}
                  {t.category && <span className="text-muted-foreground ml-2 text-xs font-normal">{t.category}</span>}
                </p>
                <p className="text-muted-foreground truncate text-xs">{t.subject || t.body.slice(0, 80)}</p>
              </div>
              <Button variant="ghost" size="sm" onClick={() => setEditing(t)}>
                编辑
              </Button>
              <Button variant="ghost" size="icon" className="size-8" aria-label="删除" onClick={() => setDeleting(t)}>
                <Trash2 className="size-4" />
              </Button>
            </div>
          ))}
        </Card>
      )}

      <TemplateDialog template={editing} onClose={() => setEditing(null)} onSaved={load} />

      <AlertDialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>删除模板</AlertDialogTitle>
            <AlertDialogDescription>确定删除「{deleting?.name}」？</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => deleting && remove(deleting)}>
              删除
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function TemplateDialog({ template, onClose, onSaved }: { template: Template | null; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState<Template | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setForm(template ? store.Template.createFrom({ ...template }) : null);
  }, [template]);

  function update<K extends keyof Template>(key: K, value: Template[K]) {
    setForm((f) => (f ? store.Template.createFrom({ ...f, [key]: value }) : f));
  }

  function insert(name: string) {
    if (!form) return;
    update("body", `${form.body}{{${name}}}`);
  }

  async function save() {
    if (!form?.name.trim()) {
      toast.error("请填写模板名称");
      return;
    }
    setBusy(true);
    try {
      await api.saveTemplate(form);
      toast.success("模板已保存");
      onSaved();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={template !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{template?.id ? "编辑模板" : "新建模板"}</DialogTitle>
        </DialogHeader>
        {form && (
          <div className="flex flex-col gap-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="flex flex-col gap-1.5">
                <Label>名称</Label>
                <Input value={form.name} onChange={(e) => update("name", e.target.value)} autoFocus />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label>分类</Label>
                <Input value={form.category} onChange={(e) => update("category", e.target.value)} placeholder="可选" />
              </div>
            </div>
            <div className="flex flex-col gap-1.5">
              <Label>主题</Label>
              <Input value={form.subject} onChange={(e) => update("subject", e.target.value)} />
            </div>
            <div className="flex flex-col gap-1.5">
              <div className="flex flex-wrap items-center gap-1">
                <Label className="mr-2">正文</Label>
                {BUILTIN_PLACEHOLDERS.map((p) => (
                  <Button key={p.name} type="button" variant="outline" size="sm" className="h-6 px-2 text-xs" onClick={() => insert(p.name)}>
                    {p.label}
                  </Button>
                ))}
              </div>
              <Textarea rows={12} value={form.body} onChange={(e) => update("body", e.target.value)} />
              <p className="text-muted-foreground text-xs">
                自定义占位符写成 {"{{客户名称}}"} 这样的形式，套用模板时会让你填写。
              </p>
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button onClick={save} disabled={busy}>
            {busy && <Loader2 className="size-4 animate-spin" />}
            保存
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/* ---------------- 账户 ---------------- */

function AccountSection({ active, onSignedOut }: { active: boolean; onSignedOut: () => void }) {
  const [status, setStatus] = useState<SessionStatus | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [confirm, setConfirm] = useState(false);

  useEffect(() => {
    if (!active) return;
    api.getSessionStatus().then(setStatus).catch(() => {});
    api.listAccounts().then((a) => setAccounts(a ?? [])).catch(() => {});
  }, [active]);

  return (
    <>
      <SectionTitle title="账户" />
      <Card>
        <Row title={status?.username || "未登录"} desc={status ? `服务器 ${status.server} · ${status.connected ? "已连接" : "离线"}` : ""}>
          <Button variant="outline" size="sm" onClick={() => setConfirm(true)}>
            <LogOut className="size-4" />
            退出登录
          </Button>
        </Row>
      </Card>

      {accounts.length > 1 && (
        <>
          <SectionTitle title="可访问的邮箱" desc="由服务器按你的权限共享，在邮件侧栏的「共享」下显示。" small />
          <Card className="gap-0 p-0">
            {accounts.map((a, i) => (
              <div key={a.id} className={cn("flex items-center gap-2 px-4 py-2.5 text-sm", i > 0 && "border-border border-t")}>
                <UserRound className="text-muted-foreground size-4" />
                <span className="flex-1 truncate">{a.name}</span>
                <span className="text-muted-foreground text-xs">{a.isPersonal ? "个人" : "共享"}</span>
              </div>
            ))}
          </Card>
        </>
      )}

      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>退出登录</AlertDialogTitle>
            <AlertDialogDescription>会删除本机保存的应用专用密码。本地缓存的邮件保留，重新登录后无需重新下载。</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>取消</AlertDialogCancel>
            <AlertDialogAction
              onClick={async () => {
                try {
                  await api.signOut();
                  onSignedOut();
                } catch (err) {
                  toast.error(errorMessage(err));
                }
              }}
            >
              退出登录
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

/* ---------------- 关于 ---------------- */

function AboutSection({ active }: { active: boolean }) {
  const [version, setVersion] = useState("");
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);

  useEffect(() => {
    if (active) api.getVersion().then(setVersion).catch(() => {});
  }, [active]);

  useEffect(() => onEvent(Events.updateProgress, (p: { done: number; total: number }) => setProgress(p)), []);

  async function check() {
    setChecking(true);
    try {
      const res = await api.checkForUpdate();
      setInfo(res);
      if (!res.available) toast.success(res.message || "已是最新版本");
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setChecking(false);
    }
  }

  async function install() {
    setInstalling(true);
    try {
      await api.installUpdate();
    } catch (err) {
      toast.error(errorMessage(err));
      setInstalling(false);
    }
  }

  return (
    <>
      <SectionTitle title="关于与更新" />
      <Card>
        <div className="flex items-center gap-4">
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="" className="size-12" />
          <div className="min-w-0 flex-1">
            <p className="font-semibold">CZL Mail</p>
            <p className="text-muted-foreground text-sm">版本 {version || "—"}</p>
          </div>
          <Button variant="outline" size="sm" onClick={check} disabled={checking || installing}>
            {checking ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
            检查更新
          </Button>
        </div>
        {info?.available && info.release && (
          <>
            <Divider />
            <div className="flex flex-col gap-2">
              <p className="text-sm font-medium">发现新版本 {info.release.version}</p>
              {info.release.notes && (
                <pre className="bg-secondary text-muted-foreground max-h-48 overflow-y-auto rounded-md p-3 font-sans text-xs whitespace-pre-wrap">
                  {info.release.notes}
                </pre>
              )}
              <div className="flex items-center gap-3">
                <Button onClick={install} disabled={installing || !info.release.assetUrl}>
                  {installing ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" />}
                  下载并安装
                </Button>
                {progress && progress.total > 0 && installing && (
                  <span className="text-muted-foreground text-xs tabular-nums">
                    {Math.round((progress.done / progress.total) * 100)}%
                  </span>
                )}
                {!info.release.assetUrl && <span className="text-muted-foreground text-xs">此版本没有适用于本系统的安装包</span>}
              </div>
              <p className="text-muted-foreground text-xs">下载后会校验安装包的 SHA-256，安装完成后自动重新打开。</p>
            </div>
          </>
        )}
      </Card>
      <Card>
        <Row title="开源项目" desc="源代码、发布记录与问题反馈（AGPL-3.0）">
          <Button variant="outline" size="sm" onClick={() => BrowserOpenURL(GITHUB_URL)}>
            <Code2 className="size-4" />
            GitHub
            <ExternalLink className="size-3.5" />
          </Button>
        </Row>
        <Divider />
        <Row title="反馈与交流" desc="使用中遇到问题或有建议，欢迎到论坛的反馈帖留言">
          <Button variant="outline" size="sm" onClick={() => BrowserOpenURL(FORUM_URL)}>
            <MessageSquare className="size-4" />
            去论坛反馈
            <ExternalLink className="size-3.5" />
          </Button>
        </Row>
      </Card>
      <p className="text-muted-foreground text-xs leading-relaxed">
        CZL Mail 是为 Stalwart 打造的原生 JMAP 桌面客户端：邮件、日历、通讯录、文件、系统通知、AI 助手与 MCP 接入。
      </p>
    </>
  );
}

