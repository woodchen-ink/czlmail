<p align="center"><img src="desktop/frontend/public/logo.png" width="96" alt="CZL Mail"></p>

<h1 align="center">CZL Mail</h1>

<p align="center">
A native desktop client for <a href="https://stalw.art">Stalwart</a> over <a href="https://jmap.io">JMAP</a> —
mail, calendar, contacts and files, with a built-in <b>MCP server</b> and <b>AI assistant</b>.
</p>

<p align="center"><b>English</b> · <a href="README.zh-CN.md">简体中文</a></p>

---

## Why this exists

[Bulwark](https://github.com/bulwarkmail/webmail) is an excellent JMAP webmail for Stalwart, and it shaped
many decisions here. But a webmail lives in a browser tab. CZL Mail exists so that **Stalwart + one native
app** is the whole setup:

- **Starts instantly.** A native Go + WebView2 binary — no browser, no server-side web app to deploy.
- **Opens mail instantly.** Everything is cached in a local SQLite database and kept current over a single
  JMAP push (EventSource) connection. Reading, searching and switching folders hit the disk, not the network.
- **System-level notifications.** New mail and calendar reminders arrive through the OS notification center,
  and keep arriving with the window closed (tray) or at login (autostart).
- **Agents can use your mailbox.** A local MCP server lets Claude, Codex and other agents read, search and
  send mail, check your calendar and look up contacts — with your consent and a local token.
- **AI where you need it.** Plug in any OpenAI-compatible Responses API: translate a message in place,
  polish or translate a draft, or describe what you want to say and let it draft the reply.

## Features

**Mail** — multiple accounts and shared mailboxes · rich-text compose with HTML signatures, templates,
read receipts, "send separately" and scheduled send · drafts auto-save · folder create / rename / move /
delete · full-text search (CJK-aware) · remote content blocked by default with a trusted-senders list
shared with Bulwark · attachments open locally with one click · pull an entire folder from the server

**Calendar** — month / week / day / agenda views with multi-day events drawn as connected bars ·
side-panel editor with meeting links, attendees and invitations, RSVP, multiple reminders and recurrence ·
tasks · calendar management · `.ics` / `webcal:` import · desktop reminders

**Contacts** — full JSContact editor (photo, name components, organization, addresses, online services,
anniversaries, categories) · groups · address books · vCard import

**Files** — Stalwart file storage with list / grid views, favorites, cut / copy / paste, drag-and-drop upload

**Server-side settings** — identities and HTML signatures, vacation auto-reply, Sieve filters
(rule builder compatible with Bulwark's rules, plus a raw Sieve editor)

**Desktop integration** — tray, autostart, default `mailto:` / `.ics` / `webcal:` handler, signed
auto-update from GitHub Releases (SHA-256 verified)

## MCP: let agents use your mailbox

Enable it in the app under **AI Assistant → MCP**. The page shows ready-to-paste configuration.

Tools: `list_accounts`, `list_mailboxes`, `list_emails`, `search_emails`, `read_email`, `mark_read`,
`send_email`, `list_events`, `create_event`, `search_contacts`, `list_files`.

Two transports are available:

```bash
# stdio (the app bridges to the running instance)
claude mcp add --scope user czlmail -- "C:\Users\<you>\AppData\Local\Programs\CZL Mail\czlmail.exe" mcp

# Streamable HTTP on 127.0.0.1 with a bearer token
claude mcp add --scope user --transport http czlmail http://127.0.0.1:47830/mcp --header "Authorization: Bearer <token>"
```

The server only listens on loopback, requires the token, and rejects browser (cross-origin) requests.
Message content is always handed to the model as data, not instructions.

## AI assistant

**Settings → AI Assistant**: base URL, API key (stored in the OS keychain) and model name. Any service that
implements the OpenAI **Responses API** (`/v1/responses`) works. AI buttons only appear after AI is configured
and enabled.

- **Translate** a message into your default language, in place, with one click back to the original
- **Polish** or **translate** what you've written in the compose window
- **Reply with intent** — type "accept, but ask to ship before Wednesday" and get a draft reply

## Install

Download the latest release from [Releases](https://github.com/woodchen-ink/czlmail/releases):

- Windows: `czlmail-amd64-installer.exe` (per-user install, no admin rights needed)
- macOS: `czlmail-darwin-universal.zip` (unsigned; right-click → Open the first time)

Sign in with your server address, email and an **app password** generated in Stalwart.

## Build from source

Requires Go 1.26+, Node 22+, pnpm and the [Wails v2 CLI](https://wails.io/docs/gettingstarted/installation) (v2.12).

```bash
cd desktop
wails dev      # development with hot reload
wails build    # build/bin/czlmail(.exe)
go test ./...  # unit tests
```

On Windows, `build.bat v0.1.0` builds the executable and the NSIS installer.

Integration tests run against a live server and are skipped unless credentials come from the environment
(`CZLMAIL_TEST_SESSION`, `CZLMAIL_TEST_USER`, `CZLMAIL_TEST_PASS`).

## Security

- Mail bodies are sanitized (DOMPurify), rendered in a sandboxed iframe without same-origin, under a strict CSP.
- Remote images are blocked unless the sender is trusted; trust is per address, never per domain.
- Credentials and API keys live only in the OS keychain; there is no plaintext fallback.
- Updates are only installed when the download matches the release's `SHA256SUMS`.

## Feedback

Questions, bugs and ideas: open an [issue](https://github.com/woodchen-ink/czlmail/issues) or leave a
comment in the [forum thread](https://sunai.net/t/topic/1485).

## License

[AGPL-3.0](LICENSE), the same license as Bulwark.

## Acknowledgements

- [Stalwart](https://stalw.art) — the mail server this client is built for
- [Bulwark](https://github.com/bulwarkmail/webmail) — whose feature set and filter format this client follows
- [go-jmap](https://git.sr.ht/~rockorager/go-jmap), [Wails](https://wails.io), [TipTap](https://tiptap.dev), [shadcn/ui](https://ui.shadcn.com)
