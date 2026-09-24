"use client";

import { useEffect, useState } from "react";
import { Check, Loader2, Minus, ShieldAlert, ShieldCheck, X } from "lucide-react";

import {
  api,
  errorMessage,
  type Address,
  type AuthResult,
  type EmailDetail,
  type HeaderInfo,
} from "@/lib/api";
import { detailDate, fileSize } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Props {
  accountId: string;
  email: EmailDetail;
  /** 自己的发件身份地址(小写)，列表里标出哪个是自己。 */
  ownAddresses: Set<string>;
}

const METHOD_LABEL: Record<string, string> = {
  spf: "SPF",
  dkim: "DKIM",
  dmarc: "DMARC",
  iprev: "反向 DNS",
  arc: "ARC",
  "dkim-atps": "DKIM-ATPS",
};

const RESULT_LABEL: Record<string, string> = {
  pass: "通过",
  fail: "失败",
  softfail: "软失败",
  neutral: "中性",
  none: "无",
  temperror: "临时错误",
  permerror: "永久错误",
  policy: "策略拒绝",
  hardfail: "失败",
};

type Tone = "good" | "bad" | "neutral";

function tone(result: string): Tone {
  if (result === "pass") return "good";
  if (["fail", "softfail", "hardfail", "permerror", "policy"].includes(result)) return "bad";
  return "neutral";
}

const TONE_CLASS: Record<Tone, string> = {
  good: "text-chart-3",
  bad: "text-destructive",
  neutral: "text-muted-foreground",
};

/**
 * 阅读栏「显示详情」: 收件路由、发件认证、标识符与邮件属性。
 *
 * 地址一律写全 —— 收件人是自己时，只有看到完整地址才知道对方发到了哪个邮箱(别名、共享账号)。
 * 认证与垃圾评分要现取邮件头(不落库)，取回之前先显示缓存里就有的部分。
 */
export function EmailDetails({ accountId, email, ownAddresses }: Props) {
  // 结果按邮件记下, 切到另一封时旧结果自然失效, 不必在 effect 里先清空。
  const key = `${accountId}/${email.id}`;
  const [loaded, setLoaded] = useState<{ key: string; info: HeaderInfo | null; error: string } | null>(null);
  const info = loaded?.key === key ? loaded.info : null;
  const error = loaded?.key === key ? loaded.error : "";

  useEffect(() => {
    let cancelled = false;
    api
      .getHeaderInfo(accountId, email.id)
      .then((i) => !cancelled && setLoaded({ key: `${accountId}/${email.id}`, info: i, error: "" }))
      .catch(
        (err) => !cancelled && setLoaded({ key: `${accountId}/${email.id}`, info: null, error: errorMessage(err) }),
      );
    return () => {
      cancelled = true;
    };
  }, [accountId, email.id]);

  const from = email.from?.[0];
  // Sender 与 From 不同才有意义(代发)。
  const sender = info?.sender?.find((s) => s.email && s.email.toLowerCase() !== from?.email?.toLowerCase());
  const replyTo = (email.replyTo ?? []).filter((a) => a.email?.toLowerCase() !== from?.email?.toLowerCase());
  const references = info?.references ?? [];

  return (
    <div className="@container border-border bg-card rounded-md border px-4 py-3 text-xs">
      <div className="grid gap-x-8 gap-y-4 @2xl:grid-cols-2">
        <Section title="收件人与路由">
          <Row label="发件人">
            <AddressList list={email.from} own={ownAddresses} />
          </Row>
          {sender && (
            <Row label="代发">
              <AddressList list={[sender]} own={ownAddresses} />
            </Row>
          )}
          {replyTo.length > 0 && (
            <Row label="回复地址">
              <AddressList list={replyTo} own={ownAddresses} />
            </Row>
          )}
          <Row label="收件人">
            {email.to && email.to.length > 0 ? (
              <AddressList list={email.to} own={ownAddresses} />
            ) : (
              <span className="text-muted-foreground">(未列出)</span>
            )}
          </Row>
          {email.cc && email.cc.length > 0 && (
            <Row label="抄送">
              <AddressList list={email.cc} own={ownAddresses} />
            </Row>
          )}
          {email.bcc && email.bcc.length > 0 && (
            <Row label="密送">
              <AddressList list={email.bcc} own={ownAddresses} />
            </Row>
          )}
          {info?.deliveredTo && info.deliveredTo.length > 0 && (
            <Row label="投递到">
              <span className="break-all">{info.deliveredTo.join("、")}</span>
            </Row>
          )}
          {info?.sentAt && <Row label="发送时间">{detailDate(info.sentAt)}</Row>}
          <Row label="接收时间">{detailDate(email.receivedAt)}</Row>
        </Section>

        <Section title="身份验证与安全">
          {error ? (
            <p className="text-destructive">{error}</p>
          ) : !info ? (
            <p className="text-muted-foreground flex items-center gap-1.5">
              <Loader2 className="size-3.5 animate-spin" />
              正在读取邮件头
            </p>
          ) : (info.auth?.length ?? 0) === 0 && !info.spamVerdict ? (
            <p className="text-muted-foreground">邮件头里没有认证结果</p>
          ) : (
            <div className="flex flex-wrap gap-1.5">
              {(info.auth ?? []).map((r, i) => (
                <AuthBadge key={i} result={r} />
              ))}
              {info.spamVerdict && <SpamBadge verdict={info.spamVerdict} score={info.spamScore} />}
            </div>
          )}
          {info?.authServer && (
            <p className="text-muted-foreground mt-1.5">由 {info.authServer} 检查</p>
          )}
        </Section>

        <Section title="标识符与会话">
          {email.messageId && (
            <Row label="消息 ID">
              <Mono>{email.messageId}</Mono>
            </Row>
          )}
          {email.inReplyTo && (
            <Row label="回复于">
              <Mono>{email.inReplyTo}</Mono>
            </Row>
          )}
          {references.length > 0 && (
            <Row label="引用">
              <span title={references.join("\n")}>{references.length} 封邮件</span>
            </Row>
          )}
          <Row label="会话 ID">
            <Mono>{email.threadId}</Mono>
          </Row>
          <Row label="邮件 ID">
            <Mono>{email.id}</Mono>
          </Row>
        </Section>

        <Section title="邮件属性">
          <Row label="主题">{email.subject || "(无主题)"}</Row>
          <Row label="大小">
            {fileSize(email.size) || "—"}
            {info?.contentType && <Mono className="text-muted-foreground ml-2">{info.contentType}</Mono>}
          </Row>
          {email.attachments && email.attachments.length > 0 && (
            <Row label="附件">{email.attachments.length} 个</Row>
          )}
        </Section>
      </div>
    </div>
  );
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="min-w-0">
      <h3 className="text-muted-foreground mb-2 font-medium">{title}</h3>
      <div className="flex flex-col gap-1.5">{children}</div>
    </section>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[4.5rem_minmax(0,1fr)] items-baseline gap-2">
      <span className="text-muted-foreground">{label}</span>
      <div className="min-w-0 text-sm">{children}</div>
    </div>
  );
}

function Mono({ children, className }: { children: React.ReactNode; className?: string }) {
  return <span className={cn("font-mono text-xs break-all", className)}>{children}</span>;
}

function AddressList({ list, own }: { list: Address[] | undefined; own: Set<string> }) {
  return (
    <span className="flex flex-col">
      {(list ?? []).map((a, i) => (
        <span key={i} className="break-all">
          {a.name && <span>{a.name} </span>}
          <span className={a.name ? "text-muted-foreground" : undefined}>
            {a.name ? `<${a.email}>` : a.email}
          </span>
          {own.has((a.email ?? "").toLowerCase()) && <span className="text-muted-foreground ml-1.5 text-xs">(我)</span>}
        </span>
      ))}
    </span>
  );
}

function AuthBadge({ result }: { result: AuthResult }) {
  const t = tone(result.result);
  const Icon = t === "good" ? Check : t === "bad" ? X : Minus;
  return (
    <span className="border-border flex items-center gap-1.5 rounded-sm border px-2 py-1">
      <Icon className={cn("size-3.5", TONE_CLASS[t])} />
      <span className="font-medium">{METHOD_LABEL[result.method] ?? result.method.toUpperCase()}</span>
      <span className={TONE_CLASS[t]}>{RESULT_LABEL[result.result] ?? result.result}</span>
      {result.detail && <span className="text-muted-foreground break-all">· {result.detail}</span>}
      {result.policy && <span className="text-muted-foreground">· 策略: {result.policy}</span>}
    </span>
  );
}

function SpamBadge({ verdict, score }: { verdict: string; score: string }) {
  const spam = verdict === "spam";
  const Icon = spam ? ShieldAlert : ShieldCheck;
  const cls = spam ? TONE_CLASS.bad : TONE_CLASS.good;
  return (
    <span className="border-border flex items-center gap-1.5 rounded-sm border px-2 py-1">
      <Icon className={cn("size-3.5", cls)} />
      <span className="font-medium">垃圾邮件评分</span>
      {score && <span className={cls}>{score}</span>}
      <span className="text-muted-foreground">· {spam ? "疑似垃圾邮件" : "正常"}</span>
    </span>
  );
}
