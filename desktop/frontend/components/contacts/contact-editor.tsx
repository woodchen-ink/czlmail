"use client";

import { useMemo, useRef, useState } from "react";
import { Camera, ChevronDown, Loader2, Plus, Search, Trash2, X } from "lucide-react";
import { toast } from "sonner";

import { SenderAvatar } from "@/components/sender-avatar";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { api, errorMessage, type Account, type AddressBook, type ContactForm, type ContactSummary } from "@/lib/api";
import { main } from "@/wailsjs/go/models";

export function bookLabel(b: AddressBook): string {
  return b.name === "Trusted Senders" ? "信任的发件人" : b.name;
}

export function emptyContact(kind: "individual" | "org" | "group", accountId: string, addressBookId: string, email = ""): ContactForm {
  return main.ContactForm.createFrom({
    accountId,
    id: "",
    uid: "",
    addressBookId,
    kind,
    prefix: "",
    given: "",
    middle: "",
    surname: "",
    suffix: "",
    nickname: "",
    organization: "",
    department: "",
    jobTitle: "",
    role: "",
    emails: [{ value: email, context: "", feature: "", label: "" }],
    phones: [],
    addresses: [],
    onlineServices: [],
    anniversaries: [],
    personalInfo: [],
    keywords: [],
    notes: "",
    photo: "",
    photoUrl: "",
    photoChanged: false,
    memberIds: [],
    displayName: "",
    readOnly: false,
  });
}

interface Props {
  initial: ContactForm;
  books: AddressBook[];
  accounts: Account[];
  /** 同账号的联系人, 供群组选择成员。 */
  candidates: ContactSummary[];
  /** 已有的类别, 供快速选择。 */
  allKeywords: string[];
  onCancel: () => void;
  onSaved: (accountId: string, id: string) => void;
}

const EMAIL_CONTEXTS = [
  ["", "邮箱"],
  ["private", "个人"],
  ["work", "工作"],
];
const PHONE_FEATURES = [
  ["mobile", "手机"],
  ["voice", "电话"],
  ["fax", "传真"],
  ["pager", "寻呼"],
  ["text", "短信"],
];
const ADDRESS_CONTEXTS = [
  ["private", "家庭"],
  ["work", "工作"],
  ["", "其它"],
];
const DATE_KINDS = [
  ["birth", "生日"],
  ["wedding", "结婚纪念日"],
  ["death", "逝世"],
];
const INFO_KINDS = [
  ["expertise", "专长"],
  ["hobby", "爱好"],
  ["interest", "兴趣"],
];

/** 联系人编辑表单, 显示在详情栏。字段按 Bulwark 的分组组织, 次要分组默认折叠。 */
export function ContactEditor({ initial, books, accounts, candidates, allKeywords, onCancel, onSaved }: Props) {
  const [f, setF] = useState<ContactForm>(initial);
  const [busy, setBusy] = useState(false);
  const [keywordInput, setKeywordInput] = useState("");
  const [memberQuery, setMemberQuery] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const isNew = !initial.id;
  const isGroup = f.kind === "group";
  const writable = books.filter((b) => (!b.myRights || b.myRights.mayWrite) && (isNew || b.accountId === initial.accountId));

  const set = <K extends keyof ContactForm>(key: K, value: ContactForm[K]) => setF((prev) => main.ContactForm.createFrom({ ...prev, [key]: value }));
  function setItem<K extends "emails" | "phones" | "addresses" | "onlineServices" | "anniversaries" | "personalInfo">(
    key: K,
    index: number,
    patch: Partial<ContactForm[K][number]>,
  ) {
    const list = [...((f[key] as unknown[]) ?? [])];
    list[index] = { ...(list[index] as object), ...patch };
    set(key, list as ContactForm[K]);
  }
  function addItem<K extends "emails" | "phones" | "addresses" | "onlineServices" | "anniversaries" | "personalInfo">(key: K, item: ContactForm[K][number]) {
    set(key, [...((f[key] as unknown[]) ?? []), item] as ContactForm[K]);
  }
  function removeItem<K extends "emails" | "phones" | "addresses" | "onlineServices" | "anniversaries" | "personalInfo">(key: K, index: number) {
    set(key, ((f[key] as unknown[]) ?? []).filter((_, i) => i !== index) as ContactForm[K]);
  }

  const members = useMemo(() => {
    const ids = new Set(f.memberIds ?? []);
    return candidates.filter((c) => c.accountId === f.accountId && ids.has(c.id));
  }, [candidates, f.memberIds, f.accountId]);
  const memberMatches = useMemo(() => {
    const q = memberQuery.trim().toLowerCase();
    if (!q) return [];
    const ids = new Set(f.memberIds ?? []);
    return candidates
      .filter((c) => c.accountId === f.accountId && c.kind !== "group" && !ids.has(c.id) && c.id !== f.id)
      .filter((c) => `${c.displayName} ${c.email}`.toLowerCase().includes(q))
      .slice(0, 8);
  }, [candidates, memberQuery, f.memberIds, f.accountId, f.id]);

  async function pickPhoto(file: File) {
    try {
      const data = await resizeImage(file, 256);
      setF((prev) => main.ContactForm.createFrom({ ...prev, photo: data, photoChanged: true }));
    } catch {
      toast.error("无法读取这张图片");
    }
  }

  async function save() {
    if (busy) return;
    const hasName = isGroup || f.kind === "org" ? (isGroup ? f.displayName : f.organization).trim() : [f.given, f.surname, f.nickname].some((s) => s.trim());
    const hasEmail = (f.emails ?? []).some((e) => e.value.trim());
    if (!hasName && !hasEmail) {
      toast.error(isGroup ? "请填写群组名称" : "请至少填写姓名或邮箱");
      return;
    }
    if (!f.addressBookId) {
      toast.error("请选择通讯录");
      return;
    }
    setBusy(true);
    try {
      const clean = main.ContactForm.createFrom({
        ...f,
        emails: (f.emails ?? []).filter((e) => e.value.trim()),
        phones: (f.phones ?? []).filter((p) => p.value.trim()),
        addresses: (f.addresses ?? []).filter((a) => [a.street, a.locality, a.region, a.postcode, a.country, a.full].some((s) => s?.trim())),
        onlineServices: (f.onlineServices ?? []).filter((s) => s.uri.trim()),
        anniversaries: (f.anniversaries ?? []).filter((d) => d.date.trim()),
        personalInfo: (f.personalInfo ?? []).filter((i) => i.value.trim()),
      });
      const id = await api.saveContact(clean);
      toast.success(isNew ? "已创建" : "已保存");
      onSaved(clean.accountId, id);
    } catch (err) {
      toast.error(errorMessage(err));
      setBusy(false);
    }
  }

  const bookKey = `${f.accountId}/${f.addressBookId}`;
  const accountName = (id: string) => accounts.find((a) => a.id === id)?.name ?? id;
  const photoSrc = f.photo || f.photoUrl;
  const title = isNew ? (isGroup ? "新建群组" : "新建联系人") : isGroup ? "编辑群组" : "编辑联系人";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="border-border flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <Button variant="ghost" size="icon" className="size-8" aria-label="关闭" onClick={onCancel}>
          <X className="size-4" />
        </Button>
        <h2 className="flex-1 text-base font-semibold">{title}</h2>
        <Button variant="outline" size="sm" onClick={onCancel} disabled={busy}>
          取消
        </Button>
        <Button size="sm" onClick={save} disabled={busy}>
          {busy && <Loader2 className="size-4 animate-spin" />}
          保存
        </Button>
      </header>

      <div className="czl-scroll min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-2xl flex-col gap-6 p-6">
          <div className="flex items-center gap-4">
            <button
              type="button"
              onClick={() => fileRef.current?.click()}
              className="group bg-muted relative size-20 shrink-0 overflow-hidden rounded-full"
              aria-label="更换照片"
            >
              {photoSrc ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img src={photoSrc} alt="" className="size-full object-cover" />
              ) : (
                <SenderAvatar email={f.emails?.[0]?.value ?? ""} name={f.displayName || f.given} className="size-20 text-xl" />
              )}
              <span className="bg-foreground/40 text-background absolute inset-0 flex items-center justify-center opacity-0 transition-opacity group-hover:opacity-100">
                <Camera className="size-5" />
              </span>
            </button>
            <input
              ref={fileRef}
              type="file"
              accept="image/png,image/jpeg,image/webp,image/gif"
              className="hidden"
              onChange={(e) => {
                const file = e.target.files?.[0];
                if (file) pickPhoto(file);
                e.target.value = "";
              }}
            />
            <div className="flex min-w-0 flex-1 flex-col gap-2">
              {photoSrc && (
                <button
                  type="button"
                  className="text-muted-foreground hover:text-destructive self-start text-xs"
                  onClick={() => setF((prev) => main.ContactForm.createFrom({ ...prev, photo: "", photoUrl: "", photoChanged: true }))}
                >
                  移除照片
                </button>
              )}
              <div className="flex flex-col gap-1.5">
                <Label>通讯录</Label>
                <select
                  value={bookKey}
                  disabled={!isNew && writable.length <= 1}
                  onChange={(e) => {
                    const [acc, book] = e.target.value.split("/");
                    setF((prev) => main.ContactForm.createFrom({ ...prev, accountId: acc, addressBookId: book, memberIds: acc === prev.accountId ? prev.memberIds : [] }));
                  }}
                  className="border-input h-9 rounded-sm border bg-transparent px-2 text-sm"
                >
                  {!f.addressBookId && <option value="/">请选择</option>}
                  {writable.map((b) => (
                    <option key={`${b.accountId}/${b.id}`} value={`${b.accountId}/${b.id}`} className="bg-popover">
                      {bookLabel(b)}
                      {accounts.length > 1 ? ` — ${accountName(b.accountId)}` : ""}
                    </option>
                  ))}
                </select>
              </div>
            </div>
          </div>

          {isGroup ? (
            <>
              <Text label="群组名称" value={f.displayName} onChange={(v) => set("displayName", v)} autoFocus />
              <section className="flex flex-col gap-2">
                <Label>成员（{members.length}）</Label>
                <div className="relative">
                  <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
                  <Input value={memberQuery} onChange={(e) => setMemberQuery(e.target.value)} placeholder="搜索联系人添加到群组" className="pl-8" />
                  {memberMatches.length > 0 && (
                    <ul className="border-border bg-popover absolute top-full right-0 left-0 z-20 mt-1 rounded-md border p-1 shadow-md">
                      {memberMatches.map((c) => (
                        <li key={c.id}>
                          <button
                            type="button"
                            className="hover:bg-secondary flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-sm"
                            onClick={() => {
                              set("memberIds", [...(f.memberIds ?? []), c.id]);
                              setMemberQuery("");
                            }}
                          >
                            <SenderAvatar email={c.email} name={c.displayName} className="size-6" />
                            <span className="min-w-0 flex-1 truncate">{c.displayName || c.email}</span>
                            <span className="text-muted-foreground truncate text-xs">{c.email}</span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
                <ul className="flex flex-col">
                  {members.map((c) => (
                    <li key={c.id} className="border-border flex items-center gap-2 border-b py-1.5 text-sm last:border-b-0">
                      <SenderAvatar email={c.email} name={c.displayName} className="size-7" />
                      <span className="min-w-0 flex-1 truncate">{c.displayName || c.email}</span>
                      <span className="text-muted-foreground truncate text-xs">{c.email}</span>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-7"
                        aria-label="移出群组"
                        onClick={() => set("memberIds", (f.memberIds ?? []).filter((id) => id !== c.id))}
                      >
                        <X className="size-3.5" />
                      </Button>
                    </li>
                  ))}
                </ul>
              </section>
            </>
          ) : (
            <>
              <div className="bg-muted flex self-start rounded-md p-0.5 text-sm">
                {[
                  ["individual", "个人"],
                  ["org", "组织"],
                ].map(([k, label]) => (
                  <button
                    key={k}
                    type="button"
                    onClick={() => set("kind", k)}
                    className={cn("rounded-sm px-3 py-1", f.kind === k ? "bg-card shadow-xs" : "text-muted-foreground")}
                  >
                    {label}
                  </button>
                ))}
              </div>

              {f.kind === "org" ? (
                <Text label="组织名称" value={f.organization} onChange={(v) => set("organization", v)} autoFocus />
              ) : (
                <div className="grid grid-cols-2 gap-3">
                  <Text label="姓" value={f.surname} onChange={(v) => set("surname", v)} autoFocus />
                  <Text label="名" value={f.given} onChange={(v) => set("given", v)} />
                  <Text label="前缀" value={f.prefix} onChange={(v) => set("prefix", v)} placeholder="如 博士" />
                  <Text label="后缀" value={f.suffix} onChange={(v) => set("suffix", v)} placeholder="如 Jr." />
                  <Text label="中间名" value={f.middle} onChange={(v) => set("middle", v)} />
                  <Text label="昵称" value={f.nickname} onChange={(v) => set("nickname", v)} />
                </div>
              )}

              <ListSection label="邮箱" onAdd={() => addItem("emails", { value: "", context: "", feature: "", label: "" })}>
                {(f.emails ?? []).map((e, i) => (
                  <Row key={i} onRemove={() => removeItem("emails", i)}>
                    <Choice value={e.context} options={EMAIL_CONTEXTS} onChange={(v) => setItem("emails", i, { context: v })} />
                    <Input type="email" value={e.value} onChange={(ev) => setItem("emails", i, { value: ev.target.value })} placeholder="name@example.com" />
                  </Row>
                ))}
              </ListSection>

              <ListSection label="电话" onAdd={() => addItem("phones", { value: "", context: "", feature: "mobile", label: "" })}>
                {(f.phones ?? []).map((p, i) => (
                  <Row key={i} onRemove={() => removeItem("phones", i)}>
                    <Choice value={p.feature || "voice"} options={PHONE_FEATURES} onChange={(v) => setItem("phones", i, { feature: v })} />
                    <Input type="tel" value={p.value} onChange={(ev) => setItem("phones", i, { value: ev.target.value })} placeholder="电话号码" />
                  </Row>
                ))}
              </ListSection>

              <Collapsible
                title="工作与组织"
                defaultOpen={!!(f.organization && f.kind !== "org") || !!f.jobTitle || !!f.department}
              >
                <div className="grid grid-cols-2 gap-3">
                  {f.kind !== "org" && <Text label="公司" value={f.organization} onChange={(v) => set("organization", v)} />}
                  <Text label="部门" value={f.department} onChange={(v) => set("department", v)} />
                  <Text label="职位" value={f.jobTitle} onChange={(v) => set("jobTitle", v)} />
                  <Text label="角色" value={f.role} onChange={(v) => set("role", v)} />
                </div>
              </Collapsible>

              <Collapsible title="地址" defaultOpen={(f.addresses ?? []).length > 0}>
                <ListSection
                  onAdd={() => addItem("addresses", { street: "", locality: "", region: "", postcode: "", country: "", context: "private", full: "" })}
                >
                  {(f.addresses ?? []).map((a, i) => (
                    <div key={i} className="border-border flex flex-col gap-2 rounded-md border p-3">
                      <div className="flex items-center gap-2">
                        <Choice value={a.context} options={ADDRESS_CONTEXTS} onChange={(v) => setItem("addresses", i, { context: v })} />
                        <div className="flex-1" />
                        <Button variant="ghost" size="icon" className="size-8" aria-label="删除地址" onClick={() => removeItem("addresses", i)}>
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                      {a.full && !a.street && <p className="text-muted-foreground text-xs">原地址：{a.full}</p>}
                      <Input value={a.street} onChange={(e) => setItem("addresses", i, { street: e.target.value })} placeholder="街道" />
                      <div className="grid grid-cols-2 gap-2">
                        <Input value={a.locality} onChange={(e) => setItem("addresses", i, { locality: e.target.value })} placeholder="城市" />
                        <Input value={a.region} onChange={(e) => setItem("addresses", i, { region: e.target.value })} placeholder="省/州" />
                        <Input value={a.postcode} onChange={(e) => setItem("addresses", i, { postcode: e.target.value })} placeholder="邮编" />
                        <Input value={a.country} onChange={(e) => setItem("addresses", i, { country: e.target.value })} placeholder="国家/地区" />
                      </div>
                    </div>
                  ))}
                </ListSection>
              </Collapsible>

              <Collapsible title="在线服务" defaultOpen={(f.onlineServices ?? []).length > 0}>
                <ListSection onAdd={() => addItem("onlineServices", { service: "", uri: "", label: "" })}>
                  {(f.onlineServices ?? []).map((s, i) => (
                    <Row key={i} onRemove={() => removeItem("onlineServices", i)}>
                      <Input className="w-32 shrink-0" value={s.service} onChange={(e) => setItem("onlineServices", i, { service: e.target.value })} placeholder="服务名" />
                      <Input value={s.uri} onChange={(e) => setItem("onlineServices", i, { uri: e.target.value })} placeholder="网址或账号" />
                    </Row>
                  ))}
                </ListSection>
              </Collapsible>

              <Collapsible title="周年纪念日" defaultOpen={(f.anniversaries ?? []).length > 0}>
                <ListSection onAdd={() => addItem("anniversaries", { kind: "birth", date: "" })}>
                  {(f.anniversaries ?? []).map((d, i) => (
                    <Row key={i} onRemove={() => removeItem("anniversaries", i)}>
                      <Choice value={d.kind} options={DATE_KINDS} onChange={(v) => setItem("anniversaries", i, { kind: v })} />
                      <Input
                        type={d.date.startsWith("--") ? "text" : "date"}
                        value={d.date}
                        onChange={(e) => setItem("anniversaries", i, { date: e.target.value })}
                        placeholder="--MM-DD"
                      />
                    </Row>
                  ))}
                </ListSection>
              </Collapsible>

              <Collapsible title="个人信息" defaultOpen={(f.personalInfo ?? []).length > 0}>
                <ListSection onAdd={() => addItem("personalInfo", { kind: "hobby", value: "", level: "" })}>
                  {(f.personalInfo ?? []).map((p, i) => (
                    <Row key={i} onRemove={() => removeItem("personalInfo", i)}>
                      <Choice value={p.kind} options={INFO_KINDS} onChange={(v) => setItem("personalInfo", i, { kind: v })} />
                      <Input value={p.value} onChange={(e) => setItem("personalInfo", i, { value: e.target.value })} />
                    </Row>
                  ))}
                </ListSection>
              </Collapsible>
            </>
          )}

          <Collapsible title="类别" defaultOpen={(f.keywords ?? []).length > 0}>
            <div className="flex flex-col gap-2">
              <div className="flex flex-wrap gap-1.5">
                {(f.keywords ?? []).map((k) => (
                  <span key={k} className="bg-muted flex items-center gap-1 rounded-sm py-0.5 pr-1 pl-2 text-xs">
                    {k}
                    <button type="button" aria-label={`移除 ${k}`} onClick={() => set("keywords", (f.keywords ?? []).filter((x) => x !== k))}>
                      <X className="size-3" />
                    </button>
                  </span>
                ))}
              </div>
              <form
                className="flex gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  const k = keywordInput.trim();
                  if (k && !(f.keywords ?? []).includes(k)) set("keywords", [...(f.keywords ?? []), k]);
                  setKeywordInput("");
                }}
              >
                <Input value={keywordInput} onChange={(e) => setKeywordInput(e.target.value)} placeholder="输入类别后回车" list="contact-keywords" />
                <datalist id="contact-keywords">
                  {allKeywords.map((k) => (
                    <option key={k} value={k} />
                  ))}
                </datalist>
              </form>
            </div>
          </Collapsible>

          <div className="flex flex-col gap-1.5">
            <Label>备注</Label>
            <Textarea rows={4} value={f.notes} onChange={(e) => set("notes", e.target.value)} />
          </div>
        </div>
      </div>
    </div>
  );
}

function Text({
  label,
  value,
  onChange,
  placeholder,
  autoFocus,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  autoFocus?: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <Label>{label}</Label>
      <Input value={value ?? ""} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} autoFocus={autoFocus} />
    </div>
  );
}

function Choice({ value, options, onChange }: { value: string; options: string[][]; onChange: (v: string) => void }) {
  return (
    <select
      value={value ?? ""}
      onChange={(e) => onChange(e.target.value)}
      className="border-input h-9 w-24 shrink-0 rounded-sm border bg-transparent px-2 text-sm"
    >
      {options.map(([v, label]) => (
        <option key={v} value={v} className="bg-popover">
          {label}
        </option>
      ))}
    </select>
  );
}

function Row({ onRemove, children }: { onRemove: () => void; children: React.ReactNode }) {
  return (
    <div className="flex items-center gap-2">
      {children}
      <Button variant="ghost" size="icon" className="size-8 shrink-0" aria-label="删除" onClick={onRemove}>
        <X className="size-4" />
      </Button>
    </div>
  );
}

function ListSection({ label, onAdd, children }: { label?: string; onAdd: () => void; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      {label && <Label>{label}</Label>}
      {children}
      <Button variant="ghost" size="sm" className="text-accent self-start" onClick={onAdd}>
        <Plus className="size-4" />
        添加
      </Button>
    </section>
  );
}

function Collapsible({ title, defaultOpen, children }: { title: string; defaultOpen?: boolean; children: React.ReactNode }) {
  const [open, setOpen] = useState(!!defaultOpen);
  return (
    <section className="border-border flex flex-col gap-3 border-t pt-4">
      <button type="button" onClick={() => setOpen(!open)} className="flex items-center gap-1.5 text-left text-sm font-medium" aria-expanded={open}>
        <ChevronDown className={cn("size-4 transition-transform", !open && "-rotate-90")} />
        {title}
      </button>
      {open && children}
    </section>
  );
}

/** 把图片缩到 max 边长以内并转成 JPEG data URI, 联系人照片不需要原图。 */
function resizeImage(file: File, max: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const img = new Image();
    img.onload = () => {
      const scale = Math.min(1, max / Math.max(img.width, img.height));
      const w = Math.round(img.width * scale);
      const h = Math.round(img.height * scale);
      const canvas = document.createElement("canvas");
      canvas.width = w;
      canvas.height = h;
      const ctx = canvas.getContext("2d");
      if (!ctx) return reject(new Error("canvas"));
      ctx.drawImage(img, 0, 0, w, h);
      URL.revokeObjectURL(url);
      resolve(canvas.toDataURL("image/jpeg", 0.85));
    };
    img.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error("image"));
    };
    img.src = url;
  });
}

