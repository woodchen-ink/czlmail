"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Languages, Loader2, Undo2 } from "lucide-react";
import { toast } from "sonner";

import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import { AttachmentList } from "@/components/attachment-list";
import { EmailBanners } from "@/components/email-banners";
import { EmailBody } from "@/components/email-body";
import { EmailToolbar, type ToolbarActions } from "@/components/email-toolbar";
import { SenderAvatar } from "@/components/sender-avatar";
import { Button } from "@/components/ui/button";
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
}: Props) {
  const [email, setEmail] = useState<EmailDetail | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [senderTrusted, setSenderTrusted] = useState(false);
  const [ai, setAI] = useState<AIConfig | null>(null);
  // 译文替换正文显示; null 表示显示原文。
  // html/text 是替换了文字之后的正文, 排版与样式保持原样。
  const [translation, setTranslation] = useState<null | {
    html: string;
    text: string;
    running: boolean;
    done: number;
    total: number;
  }>(null);
  const translateAbort = useRef<AbortController | null>(null);
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
    return () => translateAbort.current?.abort();
  }, []);

  async function translate() {
    if (!email) return;
    translateAbort.current?.abort();
    const ctl = new AbortController();
    translateAbort.current = ctl;

    const seg = extractSegments(email.bodyHtml || "", email.bodyText || "");
    if (seg.texts.length === 0) {
      toast.info("没有需要翻译的文字");
      return;
    }
    const batches = batchSegments(seg.texts);
    const result: (string | undefined)[] = new Array(seg.texts.length);
    let done = 0;
    let failed = 0;
    const show = (running: boolean) => {
      const out = seg.render(result);
      setTranslation({
        html: seg.isHtml ? out : "",
        text: seg.isHtml ? "" : out,
        running,
        done,
        total: batches.length,
      });
    };
    show(true);

    // 三批并行: 长邮件逐批显示译文, 不必等整封翻完。
    let next = 0;
    const worker = async () => {
      while (next < batches.length && !ctl.signal.aborted) {
        const batch = batches[next++];
        try {
          const output = await runAI(
            main.AIRequest.createFrom({
              kind: "translate_segments",
              accountId,
              emailId,
              text: JSON.stringify(batch.map((k) => seg.texts[k])),
              language: "",
            }),
            () => {},
            ctl.signal,
          );
          const arr = parseTranslations(output);
          if (!arr) failed++;
          else batch.forEach((k, n) => (result[k] = arr[n] ?? undefined));
        } catch (err) {
          if ((err as Error).name === "AbortError") return;
          failed++;
          if (failed === 1) toast.error(errorMessage(err));
        }
        done++;
        if (!ctl.signal.aborted) show(done < batches.length);
      }
    };
    await Promise.all([worker(), worker(), worker()]);
    if (ctl.signal.aborted) return;
    if (failed === batches.length) {
      setTranslation(null);
      return;
    }
    if (failed > 0) toast.warning("部分段落没有翻译成功，已保留原文");
  }

  useEffect(() => {
    let cancelled = false;
    let markTimer: ReturnType<typeof setTimeout> | undefined;

    async function load() {
      setLoading(true);
      setError("");
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
            await api.markRead(accountId, [emailId], true);
          } else if (mode === "delay") {
            // 停留 3 秒才算读过; 快速翻过的邮件保持未读。
            markTimer = setTimeout(() => {
              if (!cancelled)
                api.markRead(accountId, [emailId], true).catch(() => {});
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
            {email.subject || "(无主题)"}
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

          {ai?.enabled &&
            ai.hasKey &&
            ai.model &&
            email.bodyFetched &&
            (translation !== null ||
              !isAlreadyInLanguage(
                `${email.subject}
${email.bodyText || htmlToText(email.bodyHtml)}`,
                ai.translateLang || "简体中文",
              )) && (
              <div className="-my-1 flex items-center gap-2 text-sm">
                {translation === null ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-muted-foreground h-7 px-2"
                    onClick={translate}
                  >
                    <Languages className="size-4" />
                    翻译为{ai.translateLang || "简体中文"}
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
