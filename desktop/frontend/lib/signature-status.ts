import { useEffect, useSyncExternalStore } from "react";

import { api, Events, onEvent, type SignatureCheck } from "@/lib/api";
import { isNative } from "@/lib/native-bridge";

/**
 * 签名核对结果按邮件 (「账号/id」键) 记下, 列表行与阅读栏共用: 阅读栏核对过的邮件,
 * 列表里的签名标记随即变色; 列表里显示的签名邮件也在后台核对, 同 Outlook 在列表里标出无效签名。
 * 结果存进 localStorage, 下次启动不必重新下载原文; 签名后的原文不会再变, 阅读栏打开时仍会重新核对。
 *
 * 后台核对要从服务器下载整封原文 (后端不缓存), 所以只核对值得标出来的:
 * - 手机上不核对 (常用流量), 打开邮件时由阅读栏核对;
 * - 自己发的不核对: 已发送里几乎每封都是自己签的, 下载一遍只为标出绿色;
 * - 只核对还挂在屏幕上的行: 行卸掉 (滚走、换文件夹) 时没开始的任务随之取消。
 */

type Status = SignatureCheck["status"];

const STORAGE_KEY = "czlmail.signatures";
/** 最多记这么多封, 超出时丢掉最早记下的。 */
const LIMIT = 3000;
/** 原文超过这个大小的签名邮件不在后台核对 (带大附件), 打开时由阅读栏核对。 */
const AUTO_VERIFY_MAX_BYTES = 2 * 1024 * 1024;
/** 后台同时核对的封数。 */
const CONCURRENCY = 2;
/** 核对出错 (离线、会话还没恢复) 后重试的间隔: 1 分钟起翻倍, 最长 30 分钟; 连上服务器时立即重试。 */
const RETRY_MIN_MS = 60_000;
const RETRY_MAX_MS = 30 * 60_000;

let statuses: Map<string, Status> | null = null;
const listeners = new Set<() => void>();

interface Job {
  accountId: string;
  id: string;
  /** 唯一发件地址 (小写), 是自己的就不核对。 */
  sender: string;
  /** 挂着这封的行数: 同一封可能同时显示在会话行与展开区里。 */
  refs: number;
}
/** 屏幕上等着核对的签名邮件, 按挂上的先后排队。 */
const wanted = new Map<string, Job>();
const inFlight = new Set<string>();
/** 核对出错的: 到这个时间之前不重试。 */
const retryAt = new Map<string, { at: number; delay: number }>();
/** 服务器给不出结果的 (不是 S/MIME 签名等): 本次运行不再核对。 */
const unverifiable = new Set<string>();
let running = 0;
let retryTimer: ReturnType<typeof setTimeout> | null = null;

/** 自己的地址 (小写); 取到之前不核对, 免得已发送里的邮件先被整批下载。 */
let own: Set<string> | null = null;
let ownLoading = false;
let ownWatching = false;

function load(): Map<string, Status> {
  if (statuses) return statuses;
  statuses = new Map();
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const obj = raw ? (JSON.parse(raw) as Record<string, Status>) : {};
    for (const [k, v] of Object.entries(obj)) if (typeof v === "string") statuses.set(k, v);
  } catch {
    // 存储不可用或内容损坏: 从空开始。
  }
  return statuses;
}

function save() {
  const map = load();
  while (map.size > LIMIT) map.delete(map.keys().next().value as string);
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(Object.fromEntries(map)));
  } catch {
    // 存不下就只留在内存里。
  }
}

/** 记下一封邮件的核对结果; 核对不了 (null) 时不改已有的结果。 */
export function rememberSignature(key: string, result: SignatureCheck | null | undefined) {
  if (!result?.status) return;
  retryAt.delete(key);
  const map = load();
  if (map.get(key) === result.status) return;
  map.delete(key); // 重新插到末尾, 超限时最后才被丢掉
  map.set(key, result.status);
  save();
  for (const l of listeners) l();
}

function loadOwn() {
  if (ownLoading) return;
  ownLoading = true;
  api
    .ownAddresses()
    .then((list) => {
      own = new Set((list ?? []).map((a) => a.toLowerCase()));
    })
    .catch(() => {
      // 取不到 (还没连上) 时等 connected 再取。
    })
    .finally(() => {
      ownLoading = false;
      // 取不到时不在这里续跑: pump 会再来取, 失败就成了死循环。
      if (own) pump();
    });
}

/** 连上服务器、账号变化时重新取自己的地址, 并让出过错的立即重试。 */
function watchSession() {
  if (ownWatching) return;
  ownWatching = true;
  const refresh = () => {
    retryAt.clear();
    loadOwn();
  };
  onEvent(Events.connected, refresh);
  onEvent(Events.accountsChanged, refresh);
}

function scheduleRetry() {
  if (retryTimer || retryAt.size === 0) return;
  let soonest = Infinity;
  for (const [key, r] of retryAt) if (wanted.has(key)) soonest = Math.min(soonest, r.at);
  if (soonest === Infinity) return;
  retryTimer = setTimeout(
    () => {
      retryTimer = null;
      pump();
    },
    Math.max(0, soonest - Date.now()),
  );
}

function failed(key: string) {
  const delay = Math.min((retryAt.get(key)?.delay ?? RETRY_MIN_MS / 2) * 2, RETRY_MAX_MS);
  retryAt.set(key, { at: Date.now() + delay, delay });
}

function pump() {
  if (!own) {
    loadOwn();
    return;
  }
  const now = Date.now();
  for (const [key, job] of wanted) {
    if (running >= CONCURRENCY) break;
    if (inFlight.has(key) || load().has(key) || unverifiable.has(key)) continue;
    if (job.sender && own.has(job.sender)) continue;
    if ((retryAt.get(key)?.at ?? 0) > now) continue;
    inFlight.add(key);
    running++;
    api
      .verifySignature(job.accountId, job.id)
      .then((r) => {
        if (r?.status) rememberSignature(key, r);
        else unverifiable.add(key);
      })
      .catch(() => failed(key))
      .finally(() => {
        inFlight.delete(key);
        running--;
        pump();
      });
  }
  scheduleRetry();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/**
 * 一封签名邮件的核对结果; 还不知道时在后台排队核对 (规则见文件头)。
 * 返回 undefined 表示还没有结果或核对不了。
 */
export function useSignatureStatus(email: {
  accountId: string;
  id: string;
  signed?: boolean;
  size?: number;
  from?: { email?: string }[] | null;
}): Status | undefined {
  const key = `${email.accountId}/${email.id}`;
  const status = useSyncExternalStore(
    subscribe,
    () => (email.signed ? load().get(key) : undefined),
    () => undefined,
  );
  const sender = email.from?.length === 1 ? (email.from[0].email ?? "").toLowerCase() : "";
  const want =
    !!email.signed && status === undefined && (email.size ?? 0) <= AUTO_VERIFY_MAX_BYTES && !isNative();
  useEffect(() => {
    if (!want) return;
    watchSession();
    // 自己发的由 pump 跳过: 登记时自己的地址可能还没取到。
    const job = wanted.get(key);
    if (job) job.refs++;
    else wanted.set(key, { accountId: email.accountId, id: email.id, sender, refs: 1 });
    pump();
    return () => {
      const j = wanted.get(key);
      if (j && --j.refs <= 0) wanted.delete(key);
    };
  }, [want, key, sender, email.accountId, email.id]);
  return status;
}
