<p align="center"><img src="desktop/frontend/public/logo.png" width="96" alt="CZL Mail"></p>

<h1 align="center">CZL Mail</h1>

<p align="center">
A native desktop client for <a href="https://stalw.art">Stalwart</a> over <a href="https://jmap.io">JMAP</a> —
mail, calendar, contacts and files, with a built-in <b>MCP server</b> and <b>AI assistant</b>.
</p>

<p align="center"><b>English</b> · <a href="README.zh-CN.md">简体中文</a></p>

---

<p align="center"><img src="screenshots/mail.png" alt="Mail" width="860"></p>

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
shared with Bulwark · batch actions · one-click unsubscribe (RFC 8058) · calendar invitations answered right in the message ·
attachments open locally with one click · pull an entire folder from the server

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

## Screenshots

| Compose with HTML signature | Calendar with multi-day events |
|---|---|
| <img src="screenshots/compose.png" alt="Compose"> | <img src="screenshots/calendar.png" alt="Calendar"> |

| Contacts | Files |
|---|---|
| <img src="screenshots/contacts.png" alt="Contacts"> | <img src="screenshots/files.png" alt="Files"> |

| MCP for AI agents | Settings |
|---|---|
| <img src="screenshots/mcp.png" alt="MCP"> | <img src="screenshots/settings.png" alt="Settings"> |

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

- **Translate** a message into your default language in place — only the text changes, the original layout and styles stay — with one click back to the original
- **Polish** or **translate** what you've written in the compose window
- **Reply with intent** — type "accept, but ask to ship before Wednesday" and get a draft reply

## Install

Download the latest release from [Releases](https://github.com/woodchen-ink/czlmail/releases):

- Windows: `czlmail-amd64-installer.exe` (per-user install, no admin rights needed)
- macOS: `czlmail-darwin-universal.zip` (unsigned; right-click → Open the first time)

Sign in with your server address, email and an **app password** generated in Stalwart.

## Single sign-on (SSO)

Stalwart can delegate authentication to an external identity provider (an OIDC directory such as Authentik,
Keycloak or your own OIDC service). In that setup Stalwart's own authorization page only accepts local
passwords and never redirects to the identity provider, so the client **authorizes directly with the identity
provider** and uses that token against the mail server — the same approach Bulwark takes.

**An administrator needs to do two things:**

1. **Create a public client on the identity provider**: no client secret (token endpoint auth method `none`),
   PKCE enabled, with these redirect URIs:

   ```
   http://127.0.0.1:47821/callback
   http://127.0.0.1:47822/callback
   http://127.0.0.1:47823/callback
   http://127.0.0.1:47824/callback
   ```

   Request the `openid email profile` scopes; Stalwart maps the `email` claim to the account.

2. **Publish the sign-in auto-configuration** at `/.well-known/czlmail.json` on the mail server's host or a parent
   domain (for `mail.example.com`, either `https://mail.example.com/` or `https://example.com/`; HTTPS redirects are followed):

   ```json
   {
     "version": 1,
     "oauth": {
       "name": "Example SSO",
       "issuer": "https://sso.example.com",
       "clientId": "<public client id>"
     }
   }
   ```

Users then just enter the server address; the client reads this file and shows **"Sign in with Example SSO"**.
Without the file, the identity provider address and client ID can be entered manually under
"browser sign-in → single sign-on".

The file is only fetched over HTTPS, so its origin is guaranteed by the domain's certificate. The client ID is not a secret.

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

## Uninstall and delete data

- **Settings → Account → Delete all data** removes the local mail cache, settings, logs, downloaded attachments,
  and the credentials and AI key in the OS keychain, removes autostart and default-app registrations, then quits.
  Mail, calendars and contacts on the server are not affected.
- **When uninstalling**, tick "also delete all data" for the same effect. It is unticked by default, so a reinstall
  keeps you signed in with your cache intact.

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
