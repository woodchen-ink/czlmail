# CZL 笔记网页阅读器

[English](README.md)

在手机上只读查看 CZL Mail 笔记的网页。它是纯静态站点，没有后端：页面在浏览器里直接请求你的 JMAP 服务器。
看到的笔记就是桌面端同步到网盘 `Notes/` 目录的那些 Markdown 文件。

公开实例在 **https://mail-notes.czl.net**。只要服务器满足下面的条件就能用，凭据只发给你填写的服务器。也可以自己部署一份。

## 功能

- 服务器 + 邮箱 + 应用专用密码登录，和桌面端的主登录方式相同
- 逐层浏览文件夹，手机的返回手势逐级后退
- Markdown 用桌面端同一套解析器渲染：属性、任务列表、表格、脚注、`[[Wiki 链接]]`、`![[嵌入]]`
- 嵌入图片与附件：图片全屏查看，其它文件下载保存
- 标题与正文全文搜索。正文缓存在浏览器里，只有第一次搜索要全部下载
- iOS / Android 都能「添加到主屏幕」

刻意只读：桌面端靠条件写和冲突并排对比保证同步安全，网页端不重做这一套。

## 服务器要求

- JMAP，并且带文件存储（`urn:ietf:params:jmap:filenode`）。[Stalwart](https://stalw.art) 支持。
- JMAP 会话、API、下载接口允许跨域，并且允许 `Authorization` 请求头。Stalwart 默认回 `Access-Control-Allow-Origin: *`；
  前面有反向代理时，要让代理透传这些响应头，并正常应答 `OPTIONS` 预检。
- 笔记由桌面端创建：阅读器打开网盘根目录下的一个文件夹，默认 `Notes`。

密码只存在浏览器里：勾选「记住」存 `localStorage`，否则存 `sessionStorage`。退出登录时连同正文缓存一起清掉。建议给它单独签发一个应用专用密码。

## 自己部署

页面直接 import 桌面端 `desktop/frontend/lib/notes` 里的 Markdown 解析代码，所以构建前两个项目的依赖都要装。在仓库根目录执行：

```bash
npx pnpm@10 install --frozen-lockfile --dir desktop/frontend
npm ci --prefix web-phone-note
npm run build --prefix web-phone-note        # 静态导出到 web-phone-note/out
```

`out/` 是普通静态文件，任何静态托管都能用。`public/_headers` 里的规则要作为响应头下发（CSP、`sw.js` 不缓存、manifest 类型）；
Cloudflare 会自动读取这个文件，别的托管要换成它们自己的配置写法。

### Cloudflare（Workers 静态资源）

`wrangler.jsonc` 把 `out/` 部署成静态资源。部署你自己的副本前，先改掉 `name` 和 `routes` 里的域名（须是你账号下某个站点的域名）；
只想用 `*.workers.dev` 地址就删掉 `routes`。

```bash
cd web-phone-note
npx wrangler login
npx wrangler deploy
```

不要删掉 `wrangler.jsonc`。没有它的话，新版 wrangler 会识别出 Next.js，自动把项目改造成 OpenNext（服务端渲染，还会建一个 R2 缓存桶），这个静态页面用不上。

### 本地预览

```bash
cd web-phone-note
npm run dev                      # http://localhost:3000
npx wrangler pages dev out       # 生产构建，并应用 _headers
```
