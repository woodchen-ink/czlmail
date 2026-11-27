"use client";

import { dateLocale, t } from "@/lib/i18n";
import { useI18n } from "@/components/locale-provider";

import { useEffect, useState } from "react";
import { Shield, ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { api, type Attachment, type EmailDetail, type SignatureCheck } from "@/lib/api";
import { rememberSignature, useSignatureStatus } from "@/lib/signature-status";

/** S/MIME 签名部件 (smime.p7s): 阅读视图不把它当附件列出, 由签名标记代替。 */
export function isSignaturePart(a: Attachment): boolean {
  const type = a.type.toLowerCase();
  return type === "application/pkcs7-signature" || type === "application/x-pkcs7-signature";
}

type Tone = "good" | "warn" | "bad" | "neutral";

const TONE_CLASS: Record<Tone, string> = {
  good: "text-emerald-600 border-emerald-600/40 bg-emerald-600/10 dark:text-emerald-400 dark:border-emerald-400/40 dark:bg-emerald-400/10",
  warn: "text-amber-600 border-amber-600/40 bg-amber-600/10 dark:text-amber-400 dark:border-amber-400/40 dark:bg-amber-400/10",
  bad: "text-destructive border-destructive/40 bg-destructive/10",
  neutral: "text-muted-foreground border-border bg-muted",
};

const TITLE_CLASS: Record<Tone, string> = {
  good: "text-emerald-600 dark:text-emerald-400",
  warn: "text-amber-600 dark:text-amber-400",
  bad: "text-destructive",
  neutral: "text-muted-foreground",
};

function toneOf(status: string): Tone {
  switch (status) {
    case "valid":
      return "good";
    case "invalid":
      return "bad";
    case "untrusted":
    case "sender_unknown":
    case "address":
      return "warn";
    default:
      return "neutral";
  }
}

function texts(r: SignatureCheck): { title: string; summary: string } {
  switch (r.status) {
    case "valid":
      return {
        title: t("数字签名有效"),
        summary: t("签名与正文及发件地址一致；未检查证书吊销状态。"),
      };
    case "invalid":
      return { title: t("数字签名无效"), summary: t("邮件在签名后被改动过，内容可能不是发件人写的。") };
    case "untrusted":
      return { title: t("签名证书不受信任"), summary: t("签名与内容一致，但无法确认签名证书来自可信的颁发机构。") };
    case "sender_unknown":
      return { title: t("无法确认发件人"), summary: t("签名与内容一致，但没有唯一的发件地址可供核对。") };
    case "address":
      return { title: t("签名证书与发件人不符"), summary: t("签名与内容一致，但签名证书上没有这个发件地址。") };
    default:
      return { title: t("无法核对数字签名"), summary: t("CZL Mail 不支持这种签名格式。") };
  }
}

function day(v: unknown) {
  return new Date(String(v)).toLocaleDateString(dateLocale());
}

function dateTime(v: unknown) {
  return new Date(String(v)).toLocaleString(dateLocale(), { dateStyle: "short", timeStyle: "short" });
}

/**
 * 列表行里的签名标记, 同 Outlook 列表里的签名图标: 有效绿色、可疑黄色、签名后被改动红色,
 * 还没核对 (或核对不了) 时灰色。结果与阅读栏共用, 见 lib/signature-status。
 */
export function SignatureMark({
  email,
  className,
}: {
  email: { accountId: string; id: string; signed?: boolean; size?: number; from?: { email?: string }[] | null };
  className?: string;
}) {
  useI18n();
  const status = useSignatureStatus(email);
  const tone = status ? toneOf(status) : "neutral";
  const Icon = tone === "good" ? ShieldCheck : tone === "neutral" ? Shield : ShieldAlert;
  const title = status ? texts({ status } as SignatureCheck).title : t("数字签名");
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span role="img" aria-label={title} className="inline-flex shrink-0">
          <Icon className={cn(TITLE_CLASS[tone], className)} />
        </span>
      </TooltipTrigger>
      <TooltipContent>{title}</TooltipContent>
    </Tooltip>
  );
}

/**
 * 签名邮件在发件人邮箱地址后面的标记: 打开邮件时下载原文核对签名, 绿色有效, 黄色可疑, 红色被改动。
 * 点开显示签名者、颁发者与有效期。不是签名邮件或核对不了 (离线) 时不显示。
 */
export function SignatureBadge({ accountId, email }: { accountId: string; email: EmailDetail }) {
  useI18n();
  const signed = (email.attachments ?? []).some(isSignaturePart);
  const key = `${accountId}/${email.id}`;
  // 结果按邮件记下, 换了邮件旧结果自然失效, 不必在 effect 里先清空。
  const [checked, setChecked] = useState<{ key: string; result: SignatureCheck | null } | null>(null);

  useEffect(() => {
    if (!signed) return;
    let cancelled = false;
    api
      .verifySignature(accountId, email.id)
      .then((result) => {
        // 列表里的签名标记跟着变色, 不必再核对一次。
        rememberSignature(key, result);
        if (!cancelled) setChecked({ key, result: result ?? null });
      })
      .catch(() => !cancelled && setChecked({ key, result: null }));
    return () => {
      cancelled = true;
    };
  }, [signed, accountId, email.id, key]);

  if (!signed) return null;
  const result = checked?.key === key ? checked.result : undefined;
  if (result === null) return null;

  if (result === undefined) {
    return (
      <span className={cn("inline-flex h-[18px] w-6 shrink-0 items-center justify-center rounded-full border", TONE_CLASS.neutral)} aria-label={t("正在核对数字签名")}>
        <ShieldQuestion className="size-3 animate-pulse" />
      </span>
    );
  }

  const tone = toneOf(result.status);
  const Icon = tone === "good" ? ShieldCheck : tone === "neutral" ? ShieldQuestion : ShieldAlert;
  const { title, summary } = texts(result);
  const signer = result.signer;

  return (
    <Popover>
      <Tooltip>
        <TooltipTrigger asChild>
          <PopoverTrigger asChild>
            <button
              type="button"
              className={cn("inline-flex h-[18px] w-6 shrink-0 items-center justify-center rounded-full border", TONE_CLASS[tone])}
              aria-label={title}
            >
              <Icon className="size-3" />
            </button>
          </PopoverTrigger>
        </TooltipTrigger>
        <TooltipContent>{title}</TooltipContent>
      </Tooltip>
      <PopoverContent align="start" className="w-80 text-sm">
        <p className={cn("flex items-center gap-2 font-semibold", TITLE_CLASS[tone])}>
          <Icon className="size-4.5 shrink-0" />
          {title}
        </p>
        <p className="text-muted-foreground mt-1">{summary}</p>
        {signer && (
          <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5">
            <dt className="text-muted-foreground">{t("签名者")}</dt>
            <dd className="min-w-0">
              <span className="block break-words">{signer.subject}</span>
              {(signer.emails ?? []).length > 0 && <span className="text-muted-foreground block break-all text-xs">{(signer.emails ?? []).join(", ")}</span>}
            </dd>
            <dt className="text-muted-foreground">{t("颁发者")}</dt>
            <dd className="min-w-0 break-words">{signer.issuer}</dd>
            <dt className="text-muted-foreground">{t("有效期")}</dt>
            <dd>{t("{0} 至 {1}", day(signer.notBefore), day(signer.notAfter))}</dd>
            {result.signedAt && !String(result.signedAt).startsWith("0001") && (
              <>
                <dt className="text-muted-foreground">{t("签名时间")}</dt>
                <dd>{dateTime(result.signedAt)}</dd>
              </>
            )}
            <dt className="text-muted-foreground">{t("指纹")}</dt>
            <dd className="min-w-0 font-mono text-[11px] break-all">{signer.thumbprint}</dd>
          </dl>
        )}
      </PopoverContent>
    </Popover>
  );
}
