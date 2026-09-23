"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Languages, Loader2, ShieldCheck, Sparkles, Undo2 } from "lucide-react";
import { toast } from "sonner";

import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { AttachmentList } from "@/components/attachment-list";
import { EmailBanners, UnsubscribeButton, useListHeaders } from "@/components/email-banners";
import { EmailBody } from "@/components/email-body";
import { EmailToolbar, type ToolbarActions } from "@/components/email-toolbar";
import { SenderAvatar } from "@/components/sender-avatar";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  api,
  blobUrl,
  errorMessage,
  runAI,
  type AIConfig,
  type EmailDetail,
  type EmailSummary,
  type Mailbox,
} from "@/lib/api";
import { main } from "@/wailsjs/go/models";
import {
  batchSegments,
  extractSegments,
  parsePartialTranslations,
  parseTranslations,
  isAlreadyInLanguage,
} from "@/lib/translate-html";
import { htmlToText } from "@/lib/html";
import { displayAddress, displayAddressList, fullDate } from "@/lib/format";

interface Props {
  accountId: string;
  emailId: string;
  mailboxes: Mailbox[];
  currentKind: string;
  labels: string[];
  focusMode: boolean;
  bodyMode: "light" | "dark";
  bodyAlwaysLight: boolean;
  /** 工具条操作；由外壳实现，因为多数操作要刷新列表或切换选中。 */
  actions: (email: EmailDetail) => ToolbarActions;
  /** 邮件加载完成后回传，供外壳的键盘快捷键使用。 */
  onLoaded: (email: EmailDetail | null) => void;
  /** 外壳在标签、星标等变化后递增，触发重新读取。 */
  revision: number;
  /** 打开同一会话里的另一封邮件。 */
  onOpenEmail?: (id: string) => void;
  /** 自动标记已读。由外壳乐观更新列表后在后台提交, 不阻塞正文显示。 */
  onMarkRead: (id: string) => void;
}

export function EmailView({
  accountId,
  emailId,
  mailboxes,
  currentKind,
  labels,
  focusMode,
  bodyMode,
  bodyAlwaysLight,
  actions,
  onLoaded,
  revision,
  onOpenEmail,
  onMarkRead,
}: Props) {
  const [email, setEmail] = useState<EmailDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [senderTrusted, setSenderTrusted] = useState(false);
  const [ai, setAI] = useState<AIConfig | null>(null);
  // 译文替换正文显示; null 表示显示原文。
  // html/text 是替换了文字之后的正文, 排版与样式保持原样。
  const [translation, setTranslation] = useState<null | {
    subject: string;
    /** 带 data-czl-tr 标记的正文；译文经 postMessage 打进 iframe，这个字符串整轮翻译里都不变。 */
    html: string;
    text: string;
    /** 已翻译的片段，全量。 */
    translated: [number, string][];
    running: boolean;
    done: number;
    total: number;
  }>(null);
  const translateAbort = useRef<AbortController | null>(null);
  // AI 总结: 流式追加, 不落库 —— 关掉再点就是重新生成。
  const [summary, setSummary] = useState<null | { text: string; running: boolean }>(null);
  const summaryAbort = useRef<AbortController | null>(null);
  const [hasCachedTranslation, setHasCachedTranslation] = useState(false);
  // 译过的邮件打开时自动显示译文, 每次挂载只触发一次(点了「显示原文」后不再自动切回)。
  const autoTranslated = useRef(false);

  useEffect(() => {
    if (!ai?.enabled) return;
    api
      .getTranslation(accountId, emailId, ai.translateLang || "简体中文")
      .then((c) => setHasCachedTranslation(!!c))
      .catch(() => {});
  }, [accountId, emailId, ai]);

  useEffect(() => {
    if (!hasCachedTranslation || !email?.bodyFetched || !ai?.hasKey || !ai.model) return;
    if (autoTranslated.current) return;
    autoTranslated.current = true;
    translate();
    // translate 每次渲染都是新函数, 这里只关心缓存与正文就绪的时机。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasCachedTranslation, email?.bodyFetched, ai]);

  const [own, setOwn] = useState<Set<string>>(new Set());

  useEffect(() => {
    api
      .listIdentities(accountId)
      .then((ids) =>
        setOwn(new Set((ids ?? []).map((i) => i.email.toLowerCase()))),
      )
      .catch(() => {});
  }, [accountId]);

  useEffect(() => {
    api
      .getAIConfig()
      .then(setAI)
      .catch(() => setAI(null));
    return () => {
      translateAbort.current?.abort();
      summaryAbort.current?.abort();
    };
  }, []);

  async function translate() {
    if (!email) return;
    translateAbort.current?.abort();
    const ctl = new AbortController();
    translateAbort.current = ctl;

    const seg = extractSegments(email.bodyHtml || "", email.bodyText || "");
    // 主题排在正文片段之前一起翻译; 没有文字的主题不送模型。
    const subject = (email.subject || "").trim();
    const hasSubject = /\p{L}/u.test(subject);
    const offset = hasSubject ? 1 : 0;
    const texts = hasSubject ? [subject, ...seg.texts] : seg.texts;
    if (texts.length === 0) {
      toast.info("没有需要翻译的文字");
      return;
    }
    const lang = ai?.translateLang || "简体中文";
    const result: (string | undefined)[] = new Array(texts.length);

    // 译过的邮件直接用缓存。片段数对不上(正文重新取回后结构变了)时正文重新翻译。
    // 缓存格式 {subject, segments}; 旧版只存正文片段数组, 此时只补译主题。
    try {
      const cached = await api.getTranslation(accountId, emailId, lang);
      const parsed = cached ? JSON.parse(cached) : null;
      const segments = Array.isArray(parsed) ? parsed : parsed?.segments;
      if (Array.isArray(segments) && segments.length === seg.texts.length) {
        segments.forEach((t: string, i: number) => (result[offset + i] = t));
      }
      if (hasSubject && typeof parsed?.subject === "string" && parsed.subject) {
        result[0] = parsed.subject;
      }
    } catch {
      // 缓存损坏时忽略, 重新翻译。
    }
    if (ctl.signal.aborted) return;

    const pending = texts.map((_, i) => i).filter((i) => result[i] === undefined);
    const batches = batchSegments(pending.map((i) => texts[i])).map((b) => b.map((k) => pending[k]));
    // live 是还在生成、没定稿的片段: 这一批最后没通过校验时只丢掉它, 不污染 result。
    const live: (string | undefined)[] = new Array(texts.length);
    let completed = 0;
    let failed = 0;

    // HTML 正文只在开头渲染一次(每段文字包上 data-czl-tr 标记), 之后的译文经 postMessage
    // 打进 iframe 原地替换 —— 重新生成 srcDoc 会让整个文档连同图片一起重载, 流式翻译时就是一直在闪。
    // 纯文本正文没有可以挂标记的元素, 只能整篇重渲染, 所以它不参与流式更新。
    const marked = seg.isHtml ? seg.renderMarked(result.slice(offset)) : "";
    const show = (running: boolean) => {
      const merged = texts.map((_, i) => result[i] ?? live[i]);
      const body = merged.slice(offset);
      const translated: [number, string][] = [];
      body.forEach((t, i) => t !== undefined && translated.push([i, t]));
      setTranslation({
        subject: (hasSubject && merged[0]) || email.subject,
        html: marked,
        text: marked ? "" : seg.render(body),
        translated,
        running,
        done: pending.filter((i) => merged[i] !== undefined).length,
        total: pending.length,
      });
    };
    if (batches.length === 0) {
      show(false);
      return;
    }
    show(true);

    // 流式片段一个接一个地来, 而正文每次都是整篇重新渲染的 —— 不限流会闪。
    let timer: ReturnType<typeof setTimeout> | undefined;
    let lastShow = Date.now();
    const showSoon = () => {
      if (timer || ctl.signal.aborted) return;
      timer = setTimeout(
        () => {
          timer = undefined;
          lastShow = Date.now();
          if (!ctl.signal.aborted) show(true);
        },
        Math.max(0, 700 - (Date.now() - lastShow)),
      );
    };

    // 三批并行, 每批边收边写: 长邮件的译文逐段浮现, 不必等整封翻完。
    let next = 0;
    const worker = async () => {
      while (next < batches.length && !ctl.signal.aborted) {
        const batch = batches[next++];
        try {
          let arr: string[] | null = null;
          // 模型没按格式回时再要一次: 中转会把同一个模型名分给不同上游, 换一次请求多半就正常了
          // (实测有的上游把整段思维链当正文吐出来)。两次都不行才保留原文。
          for (let attempt = 0; attempt < 2 && !ctl.signal.aborted; attempt++) {
            const output = await runAI(
              main.AIRequest.createFrom({
                kind: "translate_segments",
                accountId,
                emailId,
                text: JSON.stringify(batch.map((k) => texts[k])),
                language: "",
              }),
              (full) => {
                if (!seg.isHtml) return; // 纯文本只能整篇重渲染, 流式期间不动它
                const partial = parsePartialTranslations(full, batch.length);
                if (partial.length === 0) return;
                batch.forEach((k, n) => (live[k] = partial[n]));
                showSoon();
              },
              ctl.signal,
            );
            arr = parseTranslations(output, batch.length);
            if (arr) break;
            console.warn("[translate] 返回的不是长度匹配的 JSON 数组:", output.slice(0, 500));
            batch.forEach((k) => (live[k] = undefined));
          }
          if (arr) batch.forEach((k, n) => (result[k] = arr[n]));
          else if (!ctl.signal.aborted) failed++;
        } catch (err) {
          if ((err as Error).name === "AbortError") return;
          failed++;
          if (failed === 1) toast.error(errorMessage(err));
        }
        batch.forEach((k) => (live[k] = undefined));
        completed++;
        if (!ctl.signal.aborted) show(completed < batches.length);
      }
    };
    await Promise.all([worker(), worker(), worker()]);
    clearTimeout(timer);
    if (ctl.signal.aborted) return;
    if (failed === batches.length && pending.length === texts.length) {
      setTranslation(null);
      toast.error("AI 没有按要求返回译文，翻译失败");
      return;
    }
    if (failed > 0) {
      toast.warning("部分段落没有翻译成功，已保留原文");
      return;
    }
    api
      .saveTranslation(
        accountId,
        emailId,
        lang,
        JSON.stringify({ subject: hasSubject ? result[0] : undefined, segments: result.slice(offset) }),
      )
      .catch(() => {});
    setHasCachedTranslation(true);
  }

  async function summarize() {
    summaryAbort.current?.abort();
    const ctl = new AbortController();
    summaryAbort.current = ctl;
    setSummary({ text: "", running: true });
    try {
      const text = await runAI(
        main.AIRequest.createFrom({ kind: "summarize", accountId, emailId, text: "", language: "" }),
        (full) => setSummary({ text: full, running: true }),
        ctl.signal,
      );
      if (!ctl.signal.aborted) setSummary({ text: text.trim(), running: false });
    } catch (err) {
      if ((err as Error).name === "AbortError") return;
      setSummary(null);
      toast.error(errorMessage(err));
    }
  }

  useEffect(() => {
    let cancelled = false;
    let markTimer: ReturnType<typeof setTimeout> | undefined;

    async function load() {
      setLoading(true);
      setError("");
      summaryAbort.current?.abort();
      setSummary(null);
      try {
        const detail = await api.getEmail(accountId, emailId);
        if (cancelled) return;
        setEmail(detail);
        onLoaded(detail);

        // 先问信任状态再渲染正文，否则可信发件人的图片会先被屏蔽一瞬再出现。
        const from = detail.from?.[0]?.email ?? "";
        if (from) {
          const trusted = await api.isSenderTrusted(accountId, from);
          if (!cancelled) setSenderTrusted(trusted);
        }

        // 正文未缓存时再打一次网络。先渲染已有的元数据，
        // 让用户立刻看到发件人与主题，而不是盯着一个整体的加载态。
        if (!detail.bodyFetched) {
          const withBody = await api.fetchBody(accountId, emailId);
          if (cancelled) return;
          setEmail(withBody);
          onLoaded(withBody);
        }

        // 按设置标记已读。放在正文取回之后，避免网络失败时
        // 邮件已被标记已读却什么都没显示。
        if (detail.isUnread && !cancelled && revision === 0) {
          const mode =
            (await api.getSettings().catch(() => null))?.markRead ??
            "immediate";
          if (mode === "immediate") {
            onMarkRead(emailId);
          } else if (mode === "delay") {
            // 停留 3 秒才算读过; 快速翻过的邮件保持未读。
            markTimer = setTimeout(() => {
              if (!cancelled) onMarkRead(emailId);
            }, 3000);
          }
        }
      } catch (err) {
        if (!cancelled) setError(errorMessage(err));
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    return () => {
      cancelled = true;
      if (markTimer) clearTimeout(markTimer);
    };
    // onLoaded 由外壳传入且每次渲染都是新函数，列进依赖会导致反复重载。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, emailId, revision]);

  // cid → 本地预览地址，供正文里的内嵌图片使用。
  const inlineImages = useMemo(() => {
    const map: Record<string, string> = {};
    for (const a of email?.attachments ?? []) {
      if (a.cid && a.type.startsWith("image/")) {
        map[a.cid.toLowerCase()] =
          `${window.location.origin}${blobUrl(accountId, a)}`;
      }
    }
    return map;
  }, [email?.attachments, accountId]);

  const listHeaders = useListHeaders(accountId, email);

  if (error) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <p className="text-destructive text-sm">{error}</p>
      </div>
    );
  }

  if (!email) {
    return (
      <div className="text-muted-foreground flex h-full items-center justify-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        载入中
      </div>
    );
  }

  const sender = email.from?.[0];
  const aiReady = !!(ai?.enabled && ai.hasKey && ai.model);
  const canUnsubscribe = !!(listHeaders && (listHeaders.http || listHeaders.mailto));

  return (
    <div className="flex h-full flex-col">
      <EmailToolbar
        email={email}
        mailboxes={mailboxes}
        currentKind={currentKind}
        labels={labels}
        focusMode={focusMode}
        bodyAlwaysLight={bodyAlwaysLight}
        actions={actions(email)}
      />

      <ScrollArea className="min-h-0 flex-1">
        <div className="mx-auto flex max-w-4xl flex-col gap-4 p-5">
          <h1 className="text-lg leading-snug font-semibold">
            {translation?.subject || email.subject || "(无主题)"}
          </h1>

          {email.labels && email.labels.length > 0 && (
            <div className="flex flex-wrap gap-1.5">
              {email.labels.map((l) => (
                <span
                  key={l}
                  className="bg-secondary text-secondary-foreground rounded-sm px-1.5 py-0.5 text-xs"
                >
                  {l}
                </span>
              ))}
            </div>
          )}

          <div className="flex items-start gap-3">
            <SenderAvatar
              email={sender?.email ?? ""}
              name={sender?.name}
              className="size-10"
            />

            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="text-sm font-medium">
                  {displayAddress(sender)}
                </span>
                {sender?.email && sender.name && (
                  <span className="text-muted-foreground text-xs">
                    {sender.email}
                  </span>
                )}
              </div>
              <p className="text-muted-foreground mt-0.5 text-xs">
                收件人：{displayAddressList(email.to) || "(未列出)"}
              </p>
              {email.cc && email.cc.length > 0 && (
                <p className="text-muted-foreground text-xs">
                  抄送：{displayAddressList(email.cc)}
                </p>
              )}
            </div>

            <span className="text-muted-foreground shrink-0 text-xs">
              {fullDate(email.receivedAt)}
            </span>
          </div>

          <Separator />

          {/* AI、退订与信任状态合成一行：各占一行时，正文要往下推三四行。 */}
          {email.bodyFetched && (aiReady || canUnsubscribe || senderTrusted) && (
            <div className="-my-1 flex flex-wrap items-center gap-2 text-sm">
              {aiReady && (
                <>
                  {(translation !== null ||
                    !isAlreadyInLanguage(
                      `${email.subject}
    ${email.bodyText || htmlToText(email.bodyHtml)}`,
                      ai.translateLang || "简体中文",
                    )) &&
                    (translation === null ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="text-muted-foreground h-7 px-2"
                        onClick={translate}
                      >
                        <Languages className="size-4" />
                        {hasCachedTranslation ? "显示译文" : `翻译为${ai.translateLang || "简体中文"}`}
                      </Button>
                    ) : (
                      <>
                        <span className="text-muted-foreground flex items-center gap-1.5 text-xs">
                          {translation.running ? (
                            <Loader2 className="size-3.5 animate-spin" />
                          ) : (
                            <Languages className="size-3.5" />
                          )}
                          {translation.running
                            ? `正在翻译… ${translation.done}/${translation.total}`
                            : `已由 AI 翻译为${ai.translateLang || "简体中文"}`}
                        </span>
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2"
                          onClick={() => {
                            translateAbort.current?.abort();
                            setTranslation(null);
                          }}
                        >
                          <Undo2 className="size-4" />
                          {translation.running ? "停止" : "显示原文"}
                        </Button>
                      </>
                    ))}

                  {summary === null ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-muted-foreground h-7 px-2"
                      onClick={summarize}
                    >
                      <Sparkles className="size-4" />
                      AI 总结
                    </Button>
                  ) : (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="h-7 px-2"
                      onClick={() => {
                        summaryAbort.current?.abort();
                        setSummary(null);
                      }}
                    >
                      {summary.running ? (
                        <Loader2 className="size-4 animate-spin" />
                      ) : (
                        <Undo2 className="size-4" />
                      )}
                      {summary.running ? "停止总结" : "收起总结"}
                    </Button>
                  )}
                </>
              )}

              <div className="ml-auto flex items-center gap-1">
                {senderTrusted && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span
                        className="text-muted-foreground flex size-7 items-center justify-center"
                        aria-label="此发件人在信任名单中，已加载远程内容"
                      >
                        <ShieldCheck className="size-4" />
                      </span>
                    </TooltipTrigger>
                    <TooltipContent>此发件人在信任名单中，已加载远程内容</TooltipContent>
                  </Tooltip>
                )}
                <UnsubscribeButton accountId={accountId} email={email} info={listHeaders} />
              </div>
            </div>
          )}

          {summary && (
            <div className="border-border bg-card rounded-md border px-3 py-2.5">
              <div className="text-muted-foreground mb-1.5 flex items-center gap-1.5 text-xs">
                <Sparkles className="size-3.5" />
                AI 总结
              </div>
              {summary.text ? (
                <p className="text-sm leading-relaxed whitespace-pre-wrap">{summary.text}</p>
              ) : (
                <p className="text-muted-foreground text-sm">正在生成…</p>
              )}
            </div>
          )}

          {loading && !email.bodyFetched ? (
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <Loader2 className="size-4 animate-spin" />
              正在取回正文
            </div>
          ) : (
            <>
              <EmailBanners
                accountId={accountId}
                email={email}
                info={listHeaders}
                ownAddresses={own}
              />
              {/* 附件放在正文上方：长邮件里附件沉在底部，用户常常读完才发现有附件。 */}
              <AttachmentList
                accountId={accountId}
                emailId={email.id}
                attachments={email.attachments ?? []}
              />
              {
                <EmailBody
                  html={translation ? translation.html : email.bodyHtml}
                  text={translation?.text || email.bodyText}
                  translated={translation?.translated}
                  senderEmail={sender?.email ?? ""}
                  senderTrusted={senderTrusted}
                  mode={bodyMode}
                  inlineImages={inlineImages}
                  onTrustSender={async (address) => {
                    try {
                      await api.trustSender(accountId, address);
                      setSenderTrusted(true);
                    } catch (err) {
                      setError(errorMessage(err));
                    }
                  }}
                />
              }
            </>
          )}
        </div>
      </ScrollArea>
    </div>
  );
}
