import { useEffect, useSyncExternalStore } from "react";

import { api, type SignatureCheck } from "@/lib/api";

/**
 * 签名核对结果按邮件 (「账号/id」键) 记下, 列表行与阅读栏共用: 阅读栏核对过的邮件,
 * 列表里的签名标记随即变色; 列表里显示的签名邮件也在后台核对, 同 Outlook 在列表里标出无效签名。
 * 结果存进 localStorage, 下次启动不必重新下载原文; 签名后的原文不会再变, 阅读栏打开时仍会重新核对。
 */

type Status = SignatureCheck["status"];

const STORAGE_KEY = "czlmail.signatures";
/** 最多记这么多封, 超出时丢掉最早记下的。 */
const LIMIT = 3000;
/** 原文超过这个大小的签名邮件不在后台核对 (带大附件), 打开时由阅读栏核对。 */
const AUTO_VERIFY_MAX_BYTES = 2 * 1024 * 1024;
/** 后台同时核对的封数。 */
const CONCURRENCY = 2;

let statuses: Map<string, Status> | null = null;
const listeners = new Set<() => void>();
/** 本次运行里核对不了的 (离线、登录过期): 不反复重试, 下次启动再试。 */
const failed = new Set<string>();
const inFlight = new Set<string>();
const queue: { key: string; accountId: string; id: string }[] = [];
let running = 0;

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
  const map = load();
  if (map.get(key) === result.status) return;
  map.delete(key); // 重新插到末尾, 超限时最后才被丢掉
  map.set(key, result.status);
  save();
  for (const l of listeners) l();
}

function pump() {
  while (running < CONCURRENCY && queue.length > 0) {
    const job = queue.shift()!;
    running++;
    api
      .verifySignature(job.accountId, job.id)
      .then((r) => {
        if (r?.status) rememberSignature(job.key, r);
        else failed.add(job.key);
      })
      .catch(() => failed.add(job.key))
      .finally(() => {
        inFlight.delete(job.key);
        running--;
        pump();
      });
  }
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/**
 * 一封签名邮件的核对结果; 还不知道时在后台排队核对 (只核对 signed 的、原文不太大的)。
 * 返回 undefined 表示还没有结果或核对不了。
 */
export function useSignatureStatus(email: {
  accountId: string;
  id: string;
  signed?: boolean;
  size?: number;
}): Status | undefined {
  const key = `${email.accountId}/${email.id}`;
  const status = useSyncExternalStore(
    subscribe,
    () => (email.signed ? load().get(key) : undefined),
    () => undefined,
  );
  const want = !!email.signed && status === undefined && (email.size ?? 0) <= AUTO_VERIFY_MAX_BYTES;
  useEffect(() => {
    if (!want || inFlight.has(key) || failed.has(key)) return;
    inFlight.add(key);
    queue.push({ key, accountId: email.accountId, id: email.id });
    pump();
  }, [want, key, email.accountId, email.id]);
  return status;
}
