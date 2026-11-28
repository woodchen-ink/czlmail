"use client";

import { localUrl } from "@/lib/native-bridge";

/**
 * 发件人头像在本次运行里的内存缓存: 地址 → 图片的 object URL, 或确认没有头像 (null)。
 *
 * /czl-avatar 回 no-store (WebView2 复用缓存的响应时图片可能是坏的), WebView 自己不存;
 * 不在这里记一份, 切文件夹、列表重新挂载时每个头像都要再过一遍 Go, 首字母先闪一下才换成图。
 * 后端的库里缓存负责跨次运行与过期刷新, 这里只管本次运行, 过了 FRESH_MS 再向后端要一次新的。
 */

const FRESH_MS = 60 * 60 * 1000;
const MAX_ENTRIES = 600;
/** 同时在途的头像请求: 一屏几十行时不一次塞满 Wails 的资源通道, 先出来的先显示。 */
const MAX_INFLIGHT = 6;

type Entry = { url: string | null; at: number };

const entries = new Map<string, Entry>();
const inflight = new Map<string, Promise<string | null | undefined>>();
/** 每个地址当前有几个看得见的头像在等它; 排队的请求轮到时已经没人等了就不发。 */
const wanted = new Map<string, number>();
/** 排队的请求, 后进先出: 快速滚动时眼前的行先取, 滚过去的排在后面或干脆取消。 */
const queue: (() => void)[] = [];
let running = 0;

/* ---------- 清空与代号 ---------- */

let generation = 0;
const listeners = new Set<() => void>();

/** 缓存清空一次代号加一; 已挂载的头像据此丢掉手上的旧图。 */
export function avatarGeneration(): number {
  return generation;
}

export function subscribeAvatarCache(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** 退出登录、换人登录、关掉头像时清空: 已经显示的头像也一并撤下。 */
export function clearAvatarCache() {
  for (const e of entries.values()) if (e.url) URL.revokeObjectURL(e.url);
  entries.clear();
  generation++;
  for (const fn of listeners) fn();
}

/* ---------- 读写 ---------- */

/** 已知的结果: 图片地址、null (没有头像), 或 undefined (还不知道)。过期的照样给, 由 load 换新。 */
export function cachedAvatar(email: string): string | null | undefined {
  const e = entries.get(email);
  if (!e) return undefined;
  // 挪到末尾, 淘汰时先淘汰最久没用到的。
  entries.delete(email);
  entries.set(email, e);
  return e.url;
}

export function avatarIsFresh(email: string): boolean {
  const e = entries.get(email);
  return !!e && Date.now() - e.at < FRESH_MS;
}

/**
 * 向后端取头像; 同一地址同时只取一次。结果 undefined 表示没取 (轮到时已没人要, 或出错),
 * 不记 "没有", 下次再试。调用方取完 (或不再需要) 时必须调 releaseAvatar。
 */
export function loadAvatar(email: string): Promise<string | null | undefined> {
  wanted.set(email, (wanted.get(email) ?? 0) + 1);
  const pending = inflight.get(email);
  if (pending) return pending;
  const gen = generation;
  const p = schedule(email, () => fetchAvatar(email, gen))
    .catch(() => undefined)
    .finally(() => inflight.delete(email));
  inflight.set(email, p);
  return p;
}

export function releaseAvatar(email: string) {
  const n = (wanted.get(email) ?? 0) - 1;
  if (n > 0) wanted.set(email, n);
  else wanted.delete(email);
}

async function fetchAvatar(email: string, gen: number): Promise<string | null | undefined> {
  const res = await fetch(localUrl(`/czl-avatar?email=${encodeURIComponent(email)}`));
  // 取的途中缓存被清空 (退出登录、关掉头像): 结果作废。
  if (gen !== generation) return undefined;
  // 不是图片 (浏览器里直开的开发页面, /czl-avatar 落到前端开发服务器回了 HTML) 与 404 一样按没有处理。
  if (res.status === 404 || (res.ok && !res.headers.get("content-type")?.startsWith("image/"))) {
    remember(email, null);
    return null;
  }
  if (!res.ok) return undefined;
  const blob = await res.blob();
  if (gen !== generation) return undefined;
  const url = URL.createObjectURL(blob);
  remember(email, url);
  return url;
}

/** 图片解码失败: 缓存里还是这张图时才记成没有 (已经换了新图就不动, 新图没坏)。 */
export function markAvatarBroken(email: string, url: string) {
  if (entries.get(email)?.url === url) remember(email, null);
}

function remember(email: string, url: string | null) {
  const old = entries.get(email);
  entries.delete(email);
  entries.set(email, { url, at: Date.now() });
  // 换新后旧图不再有人引用 (组件拿到新地址就换掉)。
  if (old?.url && old.url !== url) URL.revokeObjectURL(old.url);
  while (entries.size > MAX_ENTRIES) {
    const [first, e] = entries.entries().next().value!;
    entries.delete(first);
    if (e.url) URL.revokeObjectURL(e.url);
  }
}

function schedule(email: string, job: () => Promise<string | null | undefined>): Promise<string | null | undefined> {
  return new Promise((resolve, reject) => {
    const run = () => {
      if (!wanted.has(email)) {
        resolve(undefined);
        queue.pop()?.();
        return;
      }
      running++;
      job()
        .then(resolve, reject)
        .finally(() => {
          running--;
          queue.pop()?.();
        });
    };
    if (running < MAX_INFLIGHT) run();
    else queue.push(run);
  });
}

/* ---------- 进入可见区才加载 ---------- */

type Watcher = () => void;
const watchers = new WeakMap<Element, Watcher>();
let observer: IntersectionObserver | null = null;

/**
 * 元素进入可见区时调用 onVisible 一次, 返回取消函数。所有头像共用一个 IntersectionObserver。
 * 以窗口为 root: 余量会被列表的滚动容器裁掉, 实际上是滚到眼前才取, 好在取的是本机缓存。
 */
export function whenVisible(el: Element, onVisible: () => void): () => void {
  if (typeof IntersectionObserver === "undefined") {
    onVisible();
    return () => {};
  }
  observer ??= new IntersectionObserver(
    (records) => {
      for (const r of records) {
        if (!r.isIntersecting) continue;
        const fn = watchers.get(r.target);
        observer!.unobserve(r.target);
        watchers.delete(r.target);
        fn?.();
      }
    },
    { rootMargin: "300px 0px" },
  );
  watchers.set(el, onVisible);
  observer.observe(el);
  return () => {
    watchers.delete(el);
    observer?.unobserve(el);
  };
}
