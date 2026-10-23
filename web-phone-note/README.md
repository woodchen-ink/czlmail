# CZL Notes web reader

[简体中文](README.zh-CN.md)

A read-only web page for reading your CZL Mail notes on a phone. It is a static site with no backend. The page talks
to your JMAP server straight from the browser, and the notes it shows are the same Markdown files the desktop app
syncs to `Notes/` in your file storage.

A hosted copy runs at **https://mail-notes.czl.net**. It works with any server that meets the requirements below.
The site serves only static files: the browser talks to the server you enter directly, and the site never receives
or stores your password or notes. Or deploy your own copy.

## Features

- Sign in with server + email + app password, the same main sign-in as the desktop app
- Browse folders; the phone's back gesture steps back through folders and notes
- Markdown is rendered by the desktop app's own parser: front matter, task lists, tables, footnotes, `[[wiki links]]` and `![[embeds]]`
- Embedded images and attachments (images open full screen, other files are downloaded)
- Full-text search across titles and bodies. Note text is cached in the browser, so only the first search downloads everything
- Add to Home Screen on iOS and Android

It is read-only on purpose. The desktop app syncs with conditional writes and side-by-side conflict review, and this page does not reimplement that.

## Server requirements

- JMAP with file storage (`urn:ietf:params:jmap:filenode`). [Stalwart](https://stalw.art) has it.
- CORS on the JMAP session, API and download endpoints, including `Authorization` in the allowed headers.
  Stalwart sends `Access-Control-Allow-Origin: *` by default. If a reverse proxy sits in front of it, make sure the proxy
  passes those headers through and answers `OPTIONS` preflights.
- Notes created by the desktop app: the reader opens a top-level folder of your file storage, `Notes` by default.

The password is kept only in the browser: in `localStorage` with "remember me", otherwise in `sessionStorage`. Signing out clears it together with the cached note text.
Use a dedicated app password for the page.

## Deploy your own

The page imports the Markdown parser from `desktop/frontend/lib/notes`, so install both projects' dependencies before building.
Run these from the repository root:

```bash
npx pnpm@10 install --frozen-lockfile --dir desktop/frontend
npm ci --prefix web-phone-note
npm run build --prefix web-phone-note        # static export in web-phone-note/out
```

`out/` is plain static files, so any static host works. Serve the files in `public/_headers` as response headers
(CSP, `sw.js` without caching, the manifest content type). Cloudflare reads that file automatically; on other hosts,
translate it to the host's own config.

### Cloudflare (Workers static assets)

`wrangler.jsonc` deploys `out/` as static assets. Before deploying your own copy, change `name` and the `routes`
domain (a domain in a zone on your account). To serve on `*.workers.dev` instead, remove `routes`.

```bash
cd web-phone-note
npx wrangler login
npx wrangler deploy
```

Keep `wrangler.jsonc` in place. Without it, recent wrangler versions detect Next.js and try to convert the project
to OpenNext (server rendering, an R2 cache bucket), which this static page does not need.

### Local preview

```bash
cd web-phone-note
npm run dev                      # http://localhost:3000
npx wrangler pages dev out       # production build with _headers applied
```
