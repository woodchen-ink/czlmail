// Links to the deployment and usage guide in the repository (gateway/docs).
// Every in-app "guide" link comes from this table; anchors are GitHub's heading
// slugs of guide.md / guide.zh-CN.md, so a renamed heading is fixed here only.
import type { Locale } from "@/lib/i18n"

const BASE = "https://github.com/woodchen-ink/czlmail/blob/main/gateway/docs/"

const DOCS = {
  imap: ["4-add-an-imap-mailbox", "4-添加普通-imap-邮箱"],
  proxy: ["socks5-proxy", "socks5-代理"],
  googleApp: [
    "51-create-the-oauth-client-in-google-cloud-admin-once",
    "51-在-google-cloud-创建-oauth-客户端-管理员-只做一次",
  ],
  gmail: ["52-add-a-gmail-mailbox-each-user", "52-添加-gmail-邮箱-每个用户"],
  microsoftApp: [
    "61-register-the-app-in-microsoft-entra-admin-once",
    "61-在-microsoft-entra-注册应用-管理员-只做一次",
  ],
  microsoft: [
    "63-add-a-microsoft-mailbox-each-user",
    "63-添加-microsoft-邮箱-每个用户",
  ],
  adminConsent: [
    "64-organisations-that-block-user-consent",
    "64-组织不允许用户自行授权",
  ],
  exchangeDisabled: [
    "65-exchange-has-imap-or-smtp-auth-turned-off",
    "65-exchange-关掉了-imap-或-smtp-认证",
  ],
  sentCopy: ["71-sent-copy", "71-已发送副本"],
  identities: ["72-sending-identities", "72-发件身份"],
  signIn: ["8-sign-in-from-czlmail", "8-在-czlmail-里登录"],
  statuses: ["statuses-and-what-to-do", "状态与处理"],
  testCodes: ["connection-test-codes", "测试连接的错误码"],
  oauthErrors: [
    "gmail--microsoft-sign-in-errors",
    "gmail--microsoft-授权的错误码",
  ],
  upgrade: ["10-upgrades-and-backups", "10-升级与备份"],
} as const satisfies Record<string, readonly [string, string]>

export type DocTopic = keyof typeof DOCS

/** The guide section for topic, in the Chinese or English guide by UI language. */
export function docUrl(topic: DocTopic, locale: Locale): string {
  const [en, zh] = DOCS[topic]
  return locale === "zh-CN"
    ? `${BASE}guide.zh-CN.md#${zh}`
    : `${BASE}guide.md#${en}`
}

/** Every topic's anchors, for the anchor check. */
export const DOC_ANCHORS = DOCS
