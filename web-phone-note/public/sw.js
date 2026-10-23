/**
 * No-op Service Worker.
 * 默认部署, 满足 iOS Safari 等场景"站点注册过 SW"的可安装前置条件,
 * 不拦截 fetch, 不做缓存. activate 时一次性清空历史 cache, 让从
 * 带 cache 的旧 SW 迁移过来的用户立刻回收存储.
 */

self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const cacheNames = await caches.keys();
      await Promise.all(cacheNames.map((name) => caches.delete(name)));
      await self.clients.claim();
    })(),
  );
});
