"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Editor } from "@tiptap/react";
import {
  BookmarkPlus,
  CalendarClock,
  ChevronDown,
  FileText,
  Loader2,
  MailCheck,
  Paperclip,
  Send,
  Sparkles,
  Users,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { RichEditor } from "@/components/compose/rich-editor";
import { RecipientInput } from "@/components/recipient-input";
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
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import {
  api,
  errorMessage,
  runAI,
  type AIConfig,
  type Address,
  type AppSettings,
  type Identity,
  type Template,
  type UploadedAttachment,
} from "@/lib/api";
import { fileSize } from "@/lib/format";
import { htmlToText, isHtmlEmpty, sanitizeInline, textToHtml } from "@/lib/html";
import { main, store } from "@/wailsjs/go/models";

export interface ComposeDraft {
  to: string;
  cc: string;
  bcc?: string;
  subject: string;
  /** 用户可编辑的正文 HTML。 */
  html: string;
  /** 回复/转发时附在签名之后的引用内容(HTML), 不进编辑器。 */
  quoteHtml?: string;
  inReplyTo?: string;
  references?: string[];
  attachments?: UploadedAttachment[];
  /** 编辑已有草稿时的草稿 id。 */
  draftId?: string;
  /** 回复的原邮件, 供 AI 起草回复使用。 */
  replyTo?: { accountId: string; emailId: string };
}

interface Props {
  accountId: string;
  draft: ComposeDraft;
  onClose: () => void;
  onSent: () => void;
}

const AUTOSAVE_MS = 4000;
const ATTACHMENT_WORDS = ["附件", "附上", "见附", "attached", "attachment", "enclosed"];
const LANGUAGES = ["简体中文", "English", "日本語", "한국어", "Deutsch", "Français", "Español", "Русский"];

/**
 * 写信面板, 占据阅读窗格。
 *
 * 签名与引用不放进编辑器: 切换发件身份时签名要整体替换, 放在编辑器里就得在用户改过的 HTML 里
 * 找旧签名; 分开放, 发送时再按「正文 + 分隔线 + 签名 + 引用」拼起来。
 */
export function ComposePane({ accountId, draft, onClose, onSent }: Props) {
  const [identities, setIdentities] = useState<Identity[]>([]);
  const [identityId, setIdentityId] = useState("");
  const [settings, setSettings] = useState<AppSettings | null>(null);
  const [ai, setAI] = useState<AIConfig | null>(null);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [to, setTo] = useState(draft.to);
  const [cc, setCc] = useState(draft.cc);
  const [bcc, setBcc] = useState(draft.bcc ?? "");
  const [showCc, setShowCc] = useState(!!draft.cc);
  const [showBcc, setShowBcc] = useState(!!draft.bcc);
  const [subject, setSubject] = useState(draft.subject);
  const [html, setHtml] = useState(draft.html);
  const [attachments, setAttachments] = useState<UploadedAttachment[]>(draft.attachments ?? []);
  const [receipt, setReceipt] = useState(false);
  const [separately, setSeparately] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [sending, setSending] = useState(false);
  const [maxDelay, setMaxDelay] = useState(0);
  const [scheduleOpen, setScheduleOpen] = useState(false);
  const [scheduleAt, setScheduleAt] = useState("");
  const [confirm, setConfirm] = useState<null | { message: string; proceed: () => void }>(null);
  const [aiPanel, setAIPanel] = useState<null | { title: string; text: string; running: boolean }>(null);
  const [replyIntentOpen, setReplyIntentOpen] = useState(false);
  const [intent, setIntent] = useState("");

  const editorRef = useRef<Editor | null>(null);
  const draftId = useRef(draft.draftId ?? "");
  const dirty = useRef(false);
  const saving = useRef<Promise<void> | null>(null);
  const aiAbort = useRef<AbortController | null>(null);

  useEffect(() => {
    let cancelled = false;
    Promise.all([
      api.listIdentities(accountId).catch(() => []),
      api.getSettings().catch(() => null),
      api.getAIConfig().catch(() => null),
      api.listTemplates().catch(() => []),
      api.maxDelayedSend(accountId).catch(() => 0),
      api.getSessionStatus().catch(() => null),
    ]).then(([ids, st, aiCfg, tpls, delay, session]) => {
      if (cancelled) return;
      const list = ids ?? [];
      setIdentities(list);
      setSettings(st);
      setAI(aiCfg);
      setTemplates(tpls ?? []);
      setMaxDelay(delay ?? 0);
      if (st) setReceipt(st.readReceiptDefault);
      const preferred = st?.defaultIdentities?.[accountId];
      // 没有设置默认身份时, 优先用与登录账号同地址的身份。
      const login = session?.username?.toLowerCase() ?? "";
      setIdentityId(
        (list.find((i) => i.id === preferred) ?? list.find((i) => i.email.toLowerCase() === login) ?? list[0])?.id ?? "",
      );
    });
    return () => {
      cancelled = true;
    };
  }, [accountId]);

  const identity = identities.find((i) => i.id === identityId);
  const signatureHtml = useMemo(() => {
    if (!identity) return "";
    if (identity.htmlSignature?.trim()) return sanitizeInline(identity.htmlSignature);
    if (identity.textSignature?.trim()) return textToHtml(identity.textSignature.trim());
    return "";
  }, [identity]);
  const separator = settings?.signatureSeparator ?? true;

  const markDirty = useCallback(() => {
    dirty.current = true;
  }, []);

  function buildRequest(): main.ComposeRequest {
    const parts = [html];
    if (signatureHtml) parts.push((separator ? "<p>-- </p>" : "") + `<div class="signature">${signatureHtml}</div>`);
    if (draft.quoteHtml) parts.push(sanitizeInline(draft.quoteHtml));
    const fullHtml = parts.join("");
    return main.ComposeRequest.createFrom({
      accountId,
      identityId,
      to: parseAddresses(to),
      cc: parseAddresses(cc),
      bcc: parseAddresses(bcc),
      subject,
      htmlBody: fullHtml,
      textBody: htmlToText(fullHtml),
      inReplyTo: draft.inReplyTo ?? "",
      references: draft.references ?? [],
      attachments: attachments.map((a) => ({ blobId: a.blobId, type: a.type, name: a.name })),
      draftId: draftId.current,
      requestReadReceipt: receipt,
      sendAt: "",
      sendSeparately: separately,
    });
  }

  const saveDraft = useCallback(async () => {
    if (!dirty.current || sending || !identityId) return;
    // 什么都没写时不在服务器上留空草稿。
    if (!to.trim() && !cc.trim() && !bcc.trim() && !subject.trim() && isHtmlEmpty(html) && attachments.length === 0 && !draftId.current) return;
    dirty.current = false;
    const run = (async () => {
      try {
        draftId.current = await api.saveDraft(buildRequest());
      } catch (err) {
        dirty.current = true;
        console.warn("save draft", err);
      }
    })();
    saving.current = run;
    await run;
    // buildRequest 读取的是当前 state, 由下面的定时器在每次变化后重新调度。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sending, identityId, to, cc, bcc, subject, html, attachments, receipt, separately, signatureHtml]);

  useEffect(() => {
    if (!dirty.current) return;
    const t = setTimeout(saveDraft, AUTOSAVE_MS);
    return () => clearTimeout(t);
  }, [saveDraft]);

  async function close() {
    aiAbort.current?.abort();
    const hasContent = parseAddresses(to).length > 0 || subject.trim() || !isHtmlEmpty(html) || attachments.length > 0;
    if (dirty.current && hasContent) {
      await saveDraft();
      toast.success("已存为草稿");
    } else if (!hasContent && draftId.current) {
      // 内容被清空的草稿不再保留。
      await saving.current;
      api.discardDraft(accountId, draftId.current).catch(() => {});
    }
    onClose();
  }

  async function discard() {
    aiAbort.current?.abort();
    await saving.current;
    if (draftId.current) {
      try {
        await api.discardDraft(accountId, draftId.current);
      } catch (err) {
        toast.error(errorMessage(err));
      }
    }
    onClose();
  }

  async function addAttachments() {
    if (uploading) return;
    setUploading(true);
    try {
      const uploaded = await api.addAttachments(accountId);
      if (uploaded?.length) {
        setAttachments((prev) => [...prev, ...uploaded]);
        markDirty();
      }
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setUploading(false);
    }
  }

  async function applyTemplate(t: Template) {
    try {
      const recipient = parseAddresses(to)[0];
      const fill = await api.applyTemplate(t.id, {}, recipient?.name || "");
      if (fill.subject && !subject.trim()) setSubject(fill.subject);
      const body = fill.isHtml ? fill.body : textToHtml(fill.body);
      editorRef.current?.commands.setContent(body + (isHtmlEmpty(html) ? "" : html));
      if (!to.trim() && fill.to?.length) setTo(fill.to.join(", "));
      if (!cc.trim() && fill.cc?.length) {
        setCc(fill.cc.join(", "));
        setShowCc(true);
      }
      markDirty();
      if (fill.unresolved?.length) {
        toast.warning(`模板里还有未填写的占位符：${fill.unresolved.map((n) => `{{${n}}}`).join("、")}`);
      }
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  async function saveAsTemplate() {
    const name = window.prompt("模板名称", subject || "新模板");
    if (!name?.trim()) return;
    try {
      await api.saveTemplate(store.Template.createFrom({ id: "", name: name.trim(), category: "", subject, body: html, isHtml: true }));
      toast.success("已保存为模板");
    } catch (err) {
      toast.error(errorMessage(err));
    }
  }

  function checks(then: () => void) {
    if (parseAddresses(to).length + parseAddresses(cc).length + parseAddresses(bcc).length === 0) {
      toast.error("请填写收件人");
      return;
    }
    if (!identityId) {
      toast.error("没有可用的发件身份");
      return;
    }
    const text = htmlToText(html).toLowerCase();
    if ((settings?.warnMissingAttachment ?? true) && attachments.length === 0 && ATTACHMENT_WORDS.some((w) => text.includes(w) || subject.toLowerCase().includes(w))) {
      setConfirm({ message: "正文提到了附件，但还没有添加附件。仍然发送？", proceed: then });
      return;
    }
    if ((settings?.warnEmptySubject ?? true) && !subject.trim()) {
      setConfirm({ message: "这封邮件没有主题。仍然发送？", proceed: then });
      return;
    }
    then();
  }

  async function send(sendAt = "") {
    if (sending || uploading) return;
    setSending(true);
    await saving.current;
    try {
      const req = buildRequest();
      req.sendAt = sendAt;
      await api.sendEmail(req);
      toast.success(sendAt ? `已安排在 ${new Date(sendAt).toLocaleString("zh-CN")} 发送` : separately ? "已分别发送" : "已发送");
      onSent();
      onClose();
    } catch (err) {
      toast.error(errorMessage(err));
    } finally {
      setSending(false);
    }
  }

  function schedule(date: Date) {
    if (date.getTime() <= Date.now() + 60_000) {
      toast.error("定时发送的时间需要晚于现在");
      return;
    }
    if (maxDelay > 0 && date.getTime() > Date.now() + maxDelay * 1000) {
      toast.error(`服务器最多支持延后 ${Math.floor(maxDelay / 86400)} 天发送`);
      return;
    }
    checks(() => send(date.toISOString()));
  }

  async function runAITask(title: string, req: main.AIRequest) {
    aiAbort.current?.abort();
    const ctl = new AbortController();
    aiAbort.current = ctl;
    setAIPanel({ title, text: "", running: true });
    try {
      const text = await runAI(req, (full) => setAIPanel((p) => (p ? { ...p, text: full } : p)), ctl.signal);
      setAIPanel((p) => (p ? { ...p, text, running: false } : p));
    } catch (err) {
      if ((err as Error).name === "AbortError") return;
      toast.error(errorMessage(err));
      setAIPanel(null);
    }
  }

  function applyAI(mode: "replace" | "insert") {
    if (!aiPanel || !editorRef.current) return;
    const generated = textToHtml(aiPanel.text.trim());
    if (mode === "replace") editorRef.current.commands.setContent(generated, { emitUpdate: true });
    else editorRef.current.chain().focus().insertContent(generated).run();
    markDirty();
    setAIPanel(null);
  }

  const aiReady = !!ai?.enabled && ai.hasKey && !!ai.model && !!ai.baseUrl;
  const bodyText = () => htmlToText(html);

  const aiMenu = aiReady ? (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="h-7 gap-1">
          <Sparkles className="size-4" />
          AI
          <ChevronDown className="size-3" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        {draft.replyTo && (
          <>
            <DropdownMenuItem onSelect={() => setReplyIntentOpen(true)}>帮我写回复…</DropdownMenuItem>
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem
          disabled={isHtmlEmpty(html)}
          onSelect={() => runAITask("优化正文", main.AIRequest.createFrom({ kind: "polish", text: bodyText() }))}
        >
          优化润色正文
        </DropdownMenuItem>
        <DropdownMenuLabel className="text-muted-foreground text-xs">把正文翻译成</DropdownMenuLabel>
        {LANGUAGES.map((lang) => (
          <DropdownMenuItem
            key={lang}
            disabled={isHtmlEmpty(html)}
            onSelect={() => runAITask(`翻译成${lang}`, main.AIRequest.createFrom({ kind: "translate_text", text: bodyText(), language: lang }))}
          >
            {lang}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  ) : null;

  const fromLabel = (i: Identity) => (i.name ? `${i.name} <${i.email}>` : i.email);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="border-border flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <Button variant="ghost" size="icon" className="size-8" aria-label="关闭" onClick={close}>
          <X className="size-4" />
        </Button>
        <h2 className="text-base font-semibold">{draft.replyTo ? "回复" : "新邮件"}</h2>
      </header>

      <div className="border-border flex shrink-0 flex-col border-b text-sm">
        <Field label="发件人">
          <select
            value={identityId}
            onChange={(e) => {
              setIdentityId(e.target.value);
              markDirty();
            }}
            className="h-9 min-w-0 flex-1 truncate bg-transparent outline-none"
            aria-label="发件人"
          >
            {identities.map((i) => (
              <option key={i.id} value={i.id} className="bg-popover">
                {fromLabel(i)}
              </option>
            ))}
          </select>
        </Field>
        <Field
          label="收件人"
          extra={
            <div className="flex gap-1">
              {!showCc && (
                <button type="button" className="text-muted-foreground hover:text-foreground px-1 text-xs" onClick={() => setShowCc(true)}>
                  抄送
                </button>
              )}
              {!showBcc && (
                <button type="button" className="text-muted-foreground hover:text-foreground px-1 text-xs" onClick={() => setShowBcc(true)}>
                  密送
                </button>
              )}
            </div>
          }
        >
          <Borderless>
            <RecipientInput value={to} onChange={(v) => (setTo(v), markDirty())} placeholder="收件人" label="收件人" />
          </Borderless>
        </Field>
        {showCc && (
          <Field label="抄送">
            <Borderless>
              <RecipientInput value={cc} onChange={(v) => (setCc(v), markDirty())} placeholder="抄送" label="抄送" />
            </Borderless>
          </Field>
        )}
        {showBcc && (
          <Field label="密送">
            <Borderless>
              <RecipientInput value={bcc} onChange={(v) => (setBcc(v), markDirty())} placeholder="密送" label="密送" />
            </Borderless>
          </Field>
        )}
        <Field label="主题">
          <input
            value={subject}
            onChange={(e) => {
              setSubject(e.target.value);
              markDirty();
            }}
            placeholder="主题"
            className="placeholder:text-muted-foreground h-9 min-w-0 flex-1 bg-transparent outline-none"
            aria-label="主题"
          />
        </Field>
      </div>

      <div className="min-h-0 flex-1 overflow-hidden">
        <div className="czl-scroll flex h-full flex-col overflow-y-auto">
          <RichEditor
            content={draft.html}
            placeholder="正文"
            className="shrink-0"
            onReady={(e) => (editorRef.current = e)}
            onChange={(v) => {
              setHtml(v);
              markDirty();
            }}
            toolbarExtra={aiMenu}
          />

          {aiPanel && (
            <div className="border-border bg-secondary mx-4 mb-3 flex flex-col gap-2 rounded-md border p-3">
              <div className="flex items-center gap-2 text-sm font-medium">
                <Sparkles className="size-4" />
                {aiPanel.title}
                {aiPanel.running && <Loader2 className="text-muted-foreground size-3.5 animate-spin" />}
              </div>
              <p className="max-h-72 overflow-y-auto text-sm leading-relaxed whitespace-pre-wrap select-text">
                {aiPanel.text || "正在生成…"}
              </p>
              <div className="flex justify-end gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    aiAbort.current?.abort();
                    setAIPanel(null);
                  }}
                >
                  {aiPanel.running ? "停止" : "放弃"}
                </Button>
                <Button variant="outline" size="sm" disabled={aiPanel.running || !aiPanel.text} onClick={() => applyAI("insert")}>
                  插入到光标处
                </Button>
                <Button size="sm" disabled={aiPanel.running || !aiPanel.text} onClick={() => applyAI("replace")}>
                  替换正文
                </Button>
              </div>
            </div>
          )}

          {signatureHtml && (
            <div className="text-muted-foreground px-4 pb-3 text-sm">
              {separator && <p>--</p>}
              <div className="czl-signature" dangerouslySetInnerHTML={{ __html: signatureHtml }} />
            </div>
          )}
          {draft.quoteHtml && (
            <details className="text-muted-foreground px-4 pb-4 text-sm">
              <summary className="cursor-pointer select-none">显示引用的原邮件</summary>
              <div className="czl-signature mt-2" dangerouslySetInnerHTML={{ __html: sanitizeInline(draft.quoteHtml) }} />
            </details>
          )}
        </div>
      </div>

      {attachments.length > 0 && (
        <ul className="border-border flex shrink-0 flex-wrap gap-2 border-t px-4 py-2">
          {attachments.map((a) => (
            <li key={a.blobId} className="border-border bg-card flex items-center gap-2 rounded-md border py-1 pr-1 pl-2.5 text-sm">
              <Paperclip className="text-muted-foreground size-3.5 shrink-0" />
              <span className="max-w-48 truncate" title={a.name}>
                {a.name}
              </span>
              <span className="text-muted-foreground text-xs tabular-nums">{fileSize(a.size)}</span>
              <Button
                variant="ghost"
                size="icon"
                className="size-6"
                aria-label={`移除 ${a.name}`}
                onClick={() => {
                  setAttachments((prev) => prev.filter((x) => x.blobId !== a.blobId));
                  markDirty();
                }}
              >
                <X className="size-3.5" />
              </Button>
            </li>
          ))}
        </ul>
      )}

      <footer className="border-border flex shrink-0 items-center gap-1 border-t px-3 py-2">
        <IconBtn label={uploading ? "上传中" : "添加附件"} onClick={addAttachments} disabled={uploading}>
          {uploading ? <Loader2 className="animate-spin" /> : <Paperclip />}
        </IconBtn>
        <DropdownMenu>
          <Tooltip>
            <TooltipTrigger asChild>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon" className="size-8" aria-label="使用模板">
                  <FileText className="size-4" />
                </Button>
              </DropdownMenuTrigger>
            </TooltipTrigger>
            <TooltipContent>使用模板</TooltipContent>
          </Tooltip>
          <DropdownMenuContent align="start" className="max-h-80 w-56 overflow-y-auto">
            <DropdownMenuLabel>插入模板</DropdownMenuLabel>
            {templates.length === 0 && <p className="text-muted-foreground px-2 py-1.5 text-xs">还没有模板</p>}
            {templates.map((t) => (
              <DropdownMenuItem key={t.id} onSelect={() => applyTemplate(t)}>
                <span className="min-w-0 flex-1 truncate">{t.name}</span>
                {t.category && <span className="text-muted-foreground text-xs">{t.category}</span>}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <IconBtn label="存为模板" onClick={saveAsTemplate}>
          <BookmarkPlus />
        </IconBtn>
        <IconBtn label={receipt ? "已请求已读回执" : "请求已读回执"} active={receipt} onClick={() => (setReceipt(!receipt), markDirty())}>
          <MailCheck />
        </IconBtn>
        <IconBtn
          label={separately ? "分开发送：每位收件人单独收到一封" : "分开发送"}
          active={separately}
          onClick={() => setSeparately(!separately)}
        >
          <Users />
        </IconBtn>

        <div className="flex-1" />
        <Button variant="ghost" size="sm" onClick={discard} disabled={sending}>
          丢弃
        </Button>
        <div className="flex">
          <Button className="rounded-r-none" onClick={() => checks(() => send())} disabled={sending || uploading}>
            {sending ? <Loader2 className="size-4 animate-spin" /> : <Send className="size-4" />}
            发送
          </Button>
          {maxDelay > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button className="border-primary-foreground/20 rounded-l-none border-l px-2" aria-label="定时发送" disabled={sending || uploading}>
                  <ChevronDown className="size-4" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="flex items-center gap-1.5">
                  <CalendarClock className="size-4" />
                  定时发送
                </DropdownMenuLabel>
                {schedulePresets().map((p) => (
                  <DropdownMenuItem key={p.label} onSelect={() => schedule(p.date)}>
                    <span className="flex-1">{p.label}</span>
                    <span className="text-muted-foreground text-xs">{p.date.toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}</span>
                  </DropdownMenuItem>
                ))}
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setScheduleOpen(true)}>自定义时间…</DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
        </div>
      </footer>

      <Dialog open={scheduleOpen} onOpenChange={setScheduleOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>定时发送</DialogTitle>
          </DialogHeader>
          <Input type="datetime-local" value={scheduleAt} onChange={(e) => setScheduleAt(e.target.value)} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setScheduleOpen(false)}>
              取消
            </Button>
            <Button
              disabled={!scheduleAt}
              onClick={() => {
                setScheduleOpen(false);
                schedule(new Date(scheduleAt));
              }}
            >
              安排发送
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={replyIntentOpen} onOpenChange={setReplyIntentOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>让 AI 帮我回复</DialogTitle>
          </DialogHeader>
          <Textarea
            autoFocus
            rows={4}
            value={intent}
            onChange={(e) => setIntent(e.target.value)}
            placeholder="想怎么回复？例如：同意报价，但希望下周三前发货，并确认发票抬头"
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setReplyIntentOpen(false)}>
              取消
            </Button>
            <Button
              onClick={() => {
                setReplyIntentOpen(false);
                if (draft.replyTo) {
                  runAITask("AI 回复草稿", main.AIRequest.createFrom({ kind: "reply", accountId: draft.replyTo.accountId, emailId: draft.replyTo.emailId, text: intent }));
                }
              }}
            >
              <Sparkles className="size-4" />
              生成
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AlertDialog open={confirm !== null} onOpenChange={(o) => !o && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>发送前确认</AlertDialogTitle>
            <AlertDialogDescription>{confirm?.message}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>返回修改</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const next = confirm?.proceed;
                setConfirm(null);
                next?.();
              }}
            >
              仍然发送
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

function Field({ label, extra, children }: { label: string; extra?: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="border-border flex items-center gap-3 border-b px-4 last:border-b-0">
      <span className="text-muted-foreground w-12 shrink-0 text-sm">{label}</span>
      <div className="flex min-w-0 flex-1 items-center">{children}</div>
      {extra}
    </div>
  );
}

/** 让收件人输入框融入字段行: 去掉边框与底色。 */
function Borderless({ children }: { children: React.ReactNode }) {
  return <div className="min-w-0 flex-1 [&_input]:h-9 [&_input]:border-0 [&_input]:bg-transparent [&_input]:px-0 [&_input]:shadow-none [&_input]:ring-0">{children}</div>;
}

function IconBtn({
  label,
  active,
  disabled,
  onClick,
  children,
}: {
  label: string;
  active?: boolean;
  disabled?: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={label}
          aria-pressed={active}
          disabled={disabled}
          onClick={onClick}
          className={cn("size-8 [&_svg]:size-4", active && "bg-muted text-accent")}
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

function schedulePresets(): { label: string; date: Date }[] {
  const now = new Date();
  const at = (days: number, hour: number) => new Date(now.getFullYear(), now.getMonth(), now.getDate() + days, hour, 0);
  const nextMonday = (8 - now.getDay()) % 7 || 7;
  const list = [
    { label: "明天上午", date: at(1, 8) },
    { label: "明天下午", date: at(1, 13) },
    { label: "下周一上午", date: at(nextMonday, 8) },
  ];
  if (now.getHours() < 17) list.unshift({ label: "今天傍晚", date: at(0, 18) });
  return list;
}

/**
 * 解析收件人输入。
 *
 * 支持 `姓名 <addr@example.com>` 与裸地址两种写法，用逗号或分号分隔 ——
 * 用户从别处复制过来的地址串通常是这两种形态之一。
 */
export function parseAddresses(input: string): Address[] {
  return input
    .split(/[,;，；]/)
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const match = part.match(/^(.*?)\s*<([^>]+)>$/);
      if (match) {
        return { name: match[1].replace(/^["']|["']$/g, "").trim(), email: match[2].trim() };
      }
      return { name: "", email: part };
    })
    .filter((a) => a.email.includes("@"));
}
