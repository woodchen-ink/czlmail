# czlmail JMAP gateway

> **Work in progress.** IMAP mailboxes sync and push; the JMAP Mail methods and sending land in the phase 1 issues tracked in [#48](https://github.com/woodchen-ink/czlmail/issues/48).

czlmail is a pure JMAP client. The gateway is a self-hosted companion that connects your other mailboxes — plain IMAP + SMTP, Gmail, Microsoft 365 / Outlook.com — and serves them to czlmail as one JMAP server:

- Sign in to the gateway once; every mailbox you add in its admin appears as its own account in czlmail.
- The gateway watches the upstreams (IMAP NOTIFY / IDLE, polling where needed) and pushes changes over EventSource. czlmail never polls.
- Each mailbox can use its own SOCKS5 proxy.
- You run it yourself. Mail and credentials stay on your server; passwords and OAuth tokens are encrypted in its SQLite database.

## Run

```sh
export GATEWAY_MASTER_KEY=$(openssl rand -base64 32)   # back this up
export GATEWAY_BASE_URL=https://mail-gw.example.com
docker compose up -d
```

Put a TLS reverse proxy in front of port 8080. Images: `ghcr.io/woodchen-ink/czlmail-gateway` with tags `latest`, `YYYY.M`, `YYYY.M.N`, and `edge` (latest `main`).

### Environment

Only startup settings are environment variables; everything else is configured in the admin and stored in the database.

| Variable | Purpose | Default |
| --- | --- | --- |
| `GATEWAY_MASTER_KEY` | 32 random bytes, base64. Encrypts stored secrets. The gateway refuses to start with a different key than the one the data was written with. | required |
| `GATEWAY_BASE_URL` | Public origin, used in JMAP session URLs and OAuth redirect URIs. `https`, or `http://localhost`. | required |
| `GATEWAY_LISTEN` | Listen address | `:8080` |
| `GATEWAY_DATA_DIR` | SQLite files and temporary blobs | `/data` |
| `GATEWAY_WEB_DIR` | The admin web build served under `/admin/` (the image sets it) | `web/out` |
| `GATEWAY_TRUSTED_PROXIES` | Trusted reverse proxies as `Header@cidr,cidr`, e.g. `X-Real-IP@172.16.0.0/12`. Empty: use the direct peer address. | empty |
| `TZ` | Time zone for logs and the admin | `UTC` |

### Commands

```sh
gateway              # run the server
gateway version      # print the version
gateway rotate-key   # re-encrypt secrets: set GATEWAY_OLD_MASTER_KEY (current) and GATEWAY_MASTER_KEY (new), with the server stopped
```

## First run

There is no admin password in the environment. While the gateway has no user, it logs a one-time setup link at startup:

```json
{"level":"WARN","msg":"no user yet: open the setup link ...","setup_url":"https://mail-gw.example.com/admin/setup/?token=…"}
```

```sh
docker compose logs gateway | grep setup_url
```

Open the link and choose the owner's email and password (at least 10 characters); you are signed in to the admin right away. Without a browser, do the same with the API:

```sh
curl -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"token":"<token from the link>","email":"you@example.com","password":"<at least 10 characters>"}' \
  https://mail-gw.example.com/api/admin/setup
```

The link works once and for 30 minutes; restart the gateway to get a new one. Once the owner exists, no link is printed again.

Roles: the **owner** (the first user) manages everyone; **admins** manage plain users and the settings; **users** manage their own app passwords. Nobody can change their own role or disable or delete themselves, so the owner always stays. Self-service registration is off by default (`site.registration_open`); connecting to private-network hosts is off by default too (`security.allow_private_networks`). `proxy.default` and `proxy.default_password` set the SOCKS5 proxy mailboxes use unless they say otherwise (see Mailboxes).

## Admin

The admin is served by the gateway itself at `/admin/` (the bare origin redirects there), next to the API and JMAP on the same port. It is in Chinese and English: it follows the browser language (Chinese for `zh*`, English otherwise) until you pick one in the user menu, on the sign-in page or under Settings → Appearance; the choice is kept in the browser. Error messages from the API are shown in the chosen language with their numeric code.

| Page | |
| --- | --- |
| 概览 Overview | Whether everything works and what needs doing: a card per mailbox (status, how it is watched, last sync, first-sync progress, last error, proxy), online clients, to-dos (mailboxes to re-authorise, a Microsoft OAuth secret expiring within 30 days, master key backup not confirmed), and hourly trends over 24 h / 7 d / 30 d: upstream requests, rate-limit hits and push latency p50 / p95, compared with the previous period. Admins see every user's mailboxes, users their own. |
| 我的邮箱 Mailboxes | Add an IMAP mailbox (server detection, TLS or STARTTLS only, a connection test before saving) or a Gmail / Microsoft one; test, pause, re-authorise, set a proxy, set the default, remove. |
| 应用密码 App passwords | Create one per device; the password is shown once, with what to enter in czlmail. |
| 用户 Users | Admins: create, edit, disable and delete users. |
| OAuth 应用 OAuth apps | Admins: step-by-step setup of the Google and Microsoft apps, with the redirect URI and scopes to copy. |
| 设置 Settings | Your account and password, theme, registration, network and security settings. |
| 系统 System | Version, database, master key, startup environment; background tasks with their recent runs and a "run now" button; the audit log. |

IMAP mailboxes are managed with the upstream API (`/api/admin/upstreams`). Gmail and Microsoft mailboxes and the OAuth apps arrive with [#42](https://github.com/woodchen-ink/czlmail/issues/42) and [#43](https://github.com/woodchen-ink/czlmail/issues/43), automatic server detection later; until then those parts say the feature is not ready.

## App passwords

czlmail signs in to the gateway with HTTP Basic: your gateway **email** as the username and an **app password**, never your admin password.

1. Sign in to the admin and create an app password per device (for example "MacBook", "Phone").
2. Copy it right away: it looks like `czl_ab12cd34_…` and is shown only once. The gateway stores the `czl_ab12cd34_` prefix (shown in the list to tell devices apart) and an argon2id hash.
3. In czlmail, add a JMAP account with the gateway URL, your email and the app password.

The list shows when and from which IP each app password was last used. Revoking one, or disabling a user, takes effect on the next request even for clients whose credentials were cached, and ends their push connections.

Failed sign-ins (admin and Basic) are rate limited per username and per IP; over the limit the gateway answers `429` with `Retry-After`. Requests with valid credentials are never limited, and a device retrying the same stale password does not lock its user out.

## Mailboxes

Each mailbox you connect (an *upstream*) becomes one JMAP account in czlmail, with a random account id that never changes. Until the admin pages land ([#44](https://github.com/woodchen-ink/czlmail/issues/44)), add them with the admin API.

### Add an IMAP mailbox

Test the settings first; nothing is saved and nothing is sent:

```sh
curl -b cookies.txt -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json'   -d '{"name":"Work","email":"me@example.com",
       "imap":{"host":"imap.example.com","port":993,"tls":"tls","username":"me@example.com","password":"…"},
       "smtp":{"host":"smtp.example.com","port":465,"tls":"tls"}}'   https://mail-gw.example.com/api/admin/upstreams/test
```

The answer reports the IMAP sign-in with the server's capabilities (`condstore`, `qresync`, `idle`, `notify`, `move`, `uidplus`, `objectid`, `xguid`, `specialUse`, `listStatus`, `sort`) and the SMTP EHLO + AUTH result, each with `ok`, a code and the server's own reply in `detail`. Then `POST /api/admin/upstreams` with the same body adds it; the first mailbox of a user becomes the default (czlmail's primary account).

- `tls`: `tls` (implicit, ports 993 / 465), `starttls` (ports 143 / 587, required), `plain` (no TLS; trusted networks only).
- `username` defaults to `email`; SMTP `username` / `password` default to the IMAP ones.
- Passwords are encrypted in the database and never shown again; when editing, leave a password out (or empty) to keep it.
- Hosts that resolve to private, loopback or link-local addresses are refused unless `security.allow_private_networks` is on (also for proxies, and checked again at every connection).

The first sync imports every folder's message metadata, newest first, in batches of 500. Until it finishes the account shows `syncing` with `initialSync: {done, total}`; mail methods answer `serverUnavailable` (50310). Message bodies are fetched on demand.

### Add a Gmail account

Gmail is read over IMAP and sent through the Gmail API, both signed in with OAuth (scope `https://mail.google.com/`). Google requires an OAuth app of your own, created once per gateway in Google Cloud. Your users never see Google Cloud: they click "Add Gmail" and sign in to Google.

**1. Create the OAuth app** (once, as a site admin)

1. Open [console.cloud.google.com](https://console.cloud.google.com/), create a project (top bar → project picker → *New project*, any name, e.g. "Mail gateway").
2. *APIs & Services → Library*: search **Gmail API** and click *Enable*. Without it sending fails with 403.
3. *APIs & Services → OAuth consent screen* (called *Google Auth Platform → Branding / Audience* in the new console):
   - User type **External** (or **Internal** if every mailbox is in your own Google Workspace; then steps 6 and the warning below do not apply).
   - App name, user support email and developer contact email: anything you like; your users see the app name on Google's sign-in page.
   - *Data access / Scopes*: add `https://mail.google.com/` (listed as "Read, compose, send, and permanently delete all your email from Gmail"). It is a restricted scope; no other scope is needed (the address is read from the Gmail profile, not from `openid` / `email`).
4. *APIs & Services → Credentials → Create credentials → OAuth client ID*:
   - Application type **Web application**.
   - *Authorized redirect URIs*: `<GATEWAY_BASE_URL>/oauth/google/callback`, exactly, e.g. `https://mail-gw.example.com/oauth/google/callback` (the admin's "OAuth apps" page shows the right value).
   - *Create*, then copy the **Client ID** and **Client secret**.
5. In the gateway admin, *Settings → OAuth apps → Google*, paste the client ID and secret (or `PUT /api/admin/oauth-apps/google` with `{"clientId", "clientSecret"}`). The secret is encrypted and never shown again.
6. Back on the consent screen (*Audience*), set **Publishing status** to **In production** (*Publish app*). In *Testing* status Google expires refresh tokens after **7 days**: every Gmail mailbox would drop to `authRequired` a week after it was added, and only the listed test users can sign in at all.

**Unverified app warning.** A published External app with a restricted scope that Google has not verified shows "Google hasn't verified this app" on sign-in. For a self-hosted gateway that is expected: click *Advanced → Go to <app name> (unsafe)*. Unverified apps are limited to 100 users over the app's lifetime; verification (a security assessment for restricted scopes) is only worth it beyond that.

**2. Add a mailbox** (each user)

*Mailboxes → Add → Gmail*, optionally with the address (preselected on Google's page) and a proxy. Through the API: `POST /api/admin/upstreams/oauth/google/start` with `{"email", "name", "proxy"}` returns `{url}`; open it in the same browser. After signing in, Google sends the browser back to `/oauth/google/callback`, which adds the mailbox and redirects to `/admin/mailboxes/?added=<accountId>` (or `?oauthError=<msg>&code=<n>`).

- The request is tied to your admin session, valid once and for 10 minutes; Google is asked for offline access with consent every time, so a refresh token always comes back.
- Adding an address you already have only renews its token: the account id (and czlmail's data) stay.
- A mailbox in `authRequired` (password changed, access removed in the Google account, app secret replaced) is fixed with *Re-authorize* (`POST /upstreams/{id}/reauth` → `{url}`), same account id. It has to be the same address.
- The token exchange, IMAP and the Gmail API all go through the mailbox's proxy. Access tokens are refreshed 5 minutes before they expire; a refused refresh (`invalid_grant`) stops only that mailbox.
- Deleting the mailbox revokes the gateway's access at Google (unless another gateway user still has the same address). If the user unticks the Gmail permission on Google's consent page, the mailbox is not added (40331).
- A sent draft keeps its Email id but takes the sent message's content (Gmail gives it a new Message-ID and Date); clients should re-fetch it.

**How Gmail looks in czlmail.** Labels are mailboxes and a message can be in several (`maxMailboxesPerEmail` is null). Inbox, Sent and Drafts are the `\Inbox`, `\Sent` and `\Draft` labels; your labels keep their hierarchy; mail without any label is in **Archive**. Starred is the flagged keyword; Important and the inbox categories are not shown. Only All Mail, Trash and Spam are synced, so every message is fetched once, whatever its labels. Moving a message changes its labels, archiving removes only Inbox, Trash and Spam are real moves (restoring from Trash brings the labels back), and deleting for good empties it from Trash. Ids come from Gmail (`X-GM-MSGID`, threads `X-GM-THRID`), so they survive relabelling and Trash. Server search uses Gmail's own search (`X-GM-RAW`). Sending goes through the Gmail API in the right conversation; the identities are Gmail's *Send mail as* addresses (refreshed every 6 hours; edit them in Gmail).

### Add a Microsoft 365 / Outlook.com mailbox

Work and school accounts (Microsoft 365, Exchange Online) and personal accounts (Outlook.com, Hotmail, Live) both connect through IMAP `outlook.office365.com:993` and SMTP `smtp.office365.com:587` (STARTTLS), signing in with OAuth 2.0 (XOAUTH2); no password is stored. The gateway needs its own app registration in Microsoft Entra, done once by the site admin.

**1. Register the app in Microsoft Entra.** Registering needs a Microsoft Entra directory (any Microsoft 365 or Azure account has one; a free Azure account creates one).

1. Open <https://entra.microsoft.com> → **Identity** → **Applications** → **App registrations** → **New registration**.
2. **Name**: what users see on the consent screen, e.g. `czlmail gateway`.
3. **Supported account types**: *Accounts in any organizational directory (Any Microsoft Entra ID tenant – Multitenant) and personal Microsoft accounts*. This is the `common` authority the gateway uses; pick a narrower type only if you also set `tenant` below.
4. **Redirect URI**: platform **Web**, `<GATEWAY_BASE_URL>/oauth/microsoft/callback` (for example `https://mail-gw.example.com/oauth/microsoft/callback`). It must match `GATEWAY_BASE_URL` exactly, scheme and port included. **Register**.
5. On the app's **Overview**, copy the **Application (client) ID**.
6. **Certificates & secrets** → **Client secrets** → **New client secret**. Choose an expiry (at most 24 months) and copy the secret's **Value** at once (it is shown only once; the *Secret ID* is not the secret). Note the expiry date.
7. **API permissions** → **Add a permission** → **Microsoft Graph** → **Delegated permissions**: `openid`, `email`, `offline_access` (under *OpenId permissions*), `IMAP.AccessAsUser.All` and `SMTP.Send`. Leave `User.Read` or remove it; nothing else is needed. If your own organisation is the only one using the gateway, **Grant admin consent for <organisation>** here saves its users the consent screen.

Nothing else changes in the registration: no implicit grant, no mobile / SPA platform, no certificates.

**2. Enter the app in the gateway** (site admins):

```sh
curl -b cookies.txt -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -X PUT \
  -d '{"clientId":"<Application (client) ID>","clientSecret":"<secret Value>","secretExpiresAt":"2028-11-26"}' \
  https://mail-gw.example.com/api/admin/oauth-apps/microsoft
```

- `secretExpiresAt` (`YYYY-MM-DD`) drives the reminder: `GET /api/admin/oauth-apps` shows `secretState` `ok`, `expiring` (from 30 days before) or `expired`, and the admin overview lists it as a to-do once the admin pages ([#44](https://github.com/woodchen-ink/czlmail/issues/44)) are in. To rotate, create a new secret in Entra and `PUT` it with its new date; mailboxes that stopped on the expired secret restart by themselves. Leaving the secret out keeps the stored one.
- `tenant` (optional): empty or `common` for every account type; `organizations` for work accounts only; `consumers` for personal accounts only; a tenant id or domain (`contoso.onmicrosoft.com`) for an app registered as single tenant.

**3. Add a mailbox.** `POST /api/admin/upstreams/oauth/microsoft/start` with `{"name":"Work","email":"me@contoso.com"}` (both optional; `email` preselects the account, admins may add `"user"`, and `proxy` as for IMAP) returns `{url}`. Open it in the same browser, sign in and accept; Microsoft sends you back to `/admin/mailboxes/?added=<accountId>`. The address comes from the sign-in (the `email` claim, else the sign-in name). Adding the same address again only renews its token and keeps the account id.

When the grant ends (the user changed the password, removed the app, an admin revoked sessions, MFA or Conditional Access now asks for a new sign-in), only that mailbox turns `authRequired`; `POST /api/admin/upstreams/{id}/reauth` returns a sign-in URL for the same address, and the account id stays. The `AADSTS…` code from Microsoft is in the status `detail`.

**Organisations that block user consent.** Many tenants only let admins approve apps; users then see *Need admin approval* (AADSTS90094 / AADSTS65001). `GET /api/admin/oauth-apps` gives `adminConsentUrl` for the microsoft app: send it to that organisation's admin, who signs in with an admin account and accepts for the whole organisation (the browser comes back to `/admin/mailboxes/?adminConsent=granted`, or `?oauthError=oauth_denied&code=40330`). Afterwards the users add their mailboxes as above. The admin can also do it in their own Entra: **Enterprise applications** → the app → **Permissions** → **Grant admin consent**.

**Exchange settings that block a working sign-in.** Microsoft 365 tenants can turn IMAP and authenticated SMTP off; new tenants have SMTP AUTH off by default.

- IMAP off for the mailbox: the connection test reports 40313 `imap_access_disabled` (Exchange answers *User is authenticated but not connected*), the mailbox shows `authRequired`. Turn on **IMAP** in Microsoft 365 admin center → **Users** → **Active users** → the user → **Mail** → **Manage email apps**, or `Set-CASMailbox -Identity me@contoso.com -ImapEnabled $true`. The same error appears when the signed-in account is not the mailbox's owner.
- SMTP AUTH off: the test reports 40312 `smtp_auth_disabled` (*5.7.139 … SmtpClientAuthentication is disabled*). Turn on **Authenticated SMTP** in the same place, or `Set-CASMailbox -Identity me@contoso.com -SmtpClientAuthenticationDisabled $false` (per mailbox) / `Set-TransportConfig -SmtpClientAuthenticationDisabled $false` (whole tenant). Reading mail keeps working meanwhile.

How Microsoft mailboxes differ from other IMAP servers:

- Exchange has neither NOTIFY nor CONDSTORE / QRESYNC: push is IDLE on INBOX, the other folders are polled, and changes are found by comparing UIDs and flags (the newest 2000 messages on every check, everything once a day).
- Folder roles come from the special-use flags Exchange puts on its folders and else from their names (`Sent Items`, `Deleted Items`, `Junk Email`, `Archive` and the translated names of the mailbox language).
- Exchange files every message sent over SMTP in Sent Items itself, so the gateway does not append a copy.
- Microsoft has no endpoint for revoking one token: deleting the mailbox in the gateway deletes its tokens, and the user removes the app's access at <https://account.live.com/consent/Manage> (personal) or <https://myapps.microsoft.com> (work).

### Proxies

Every connection of a mailbox (IMAP, SMTP, and for Gmail / Microsoft also OAuth and their APIs) can go through a SOCKS5 proxy. Host names are resolved by the proxy.

- `"proxy": {"mode": "follow"}` (default) uses the gateway-wide proxy: settings `proxy.default` (`socks5://[user@]host:port`, empty for direct) and `proxy.default_password` (encrypted; shown as `********`).
- `{"mode": "none"}` always connects directly.
- `{"mode": "custom", "url": "socks5://user@proxy.example:1080", "password": "…"}` uses its own proxy.

A proxy that fails affects only the mailboxes using it: they show `unreachable` and retry with backoff (5 s up to 5 min), the others keep syncing. A proxy on a private address (a sidecar container, for example) needs `security.allow_private_networks`.

### Status and controls

| Status | Code | Meaning |
| --- | --- | --- |
| `ok` | | synced and watching |
| `syncing` | 50310 | first import running |
| `authRequired` | 40110 | the server rejected the password (or the OAuth grant is gone); retried only after you change it |
| `unreachable` | 50210 / 42910 | network, proxy, TLS or server failure, or the provider throttles the gateway (42910); retried with backoff |
| `paused` | 42310 | paused by you |

`POST /upstreams/{id}/pause` and `/resume`, `/default` (primary account), `DELETE /upstreams/{id}` (stops it, deletes its index, removes the account from the Session). Changing the connection settings restarts the mailbox. Each status change also changes the Session state and closes the user's push connections, so clients re-read it.

### How changes reach czlmail

- Push: IMAP NOTIFY for every folder when the server has it, otherwise IDLE on INBOX (renewed every 25 minutes). A change appears in czlmail in about a second.
- Polling for the other folders: every minute after a change, every 3 minutes otherwise, every 10 minutes for Junk and Trash; every 15 minutes once the user has had no push connection for 30 minutes. A client connecting triggers a check of every folder at once.
- Sync tiers by advertised capability: QRESYNC (changes and expunges since the last state in one SELECT), CONDSTORE (changes since the last MODSEQ, deletions found by comparing UIDs when counts differ), or neither (the newest 2000 messages compared on every check, everything once a day).
- Ids stay stable: OBJECTID when the server has it, else Dovecot's X-GUID, else UID binding with (Message-ID, size, date) matching, so a message moved in another client keeps its id, and a UIDVALIDITY reset only reports the messages that really changed. States survive restarts; the changelog keeps 30 days or 200,000 entries.
- Per mailbox at most one watching and two working connections, with new connections rate limited.

### Sending

czlmail sends the way RFC 8621 describes: `Email/set` stores the draft in Drafts, `EmailSubmission/set` sends it by reference, and `onSuccessUpdateEmail` moves it to Sent and drops `$draft`. The gateway fetches the draft from the mailbox, removes its `Bcc` header, adds `Message-ID` and `Date` when missing, and delivers it over the mailbox's SMTP server through the mailbox's proxy. The envelope is the submission's `envelope`, or the `Sender` (else first `From`) address and every `To`, `Cc` and `Bcc` address. Sending is immediate: `undoStatus` is always `final`, `maxDelayedSend` is 0; submissions are kept 7 days.

- **One Sent copy, same Email id.** Per mailbox, `sentCopy` says how sent mail reaches Sent: `gateway` (the server keeps nothing: the draft is moved to Sent with `UID MOVE`), `server` (the server saves its own copy: the gateway waits up to 15 s for it by Message-ID, gives it the draft's Email id and deletes the draft), or `auto` (default: the first send waits like `server` and stores what it saw). When no server copy shows up in time the gateway files its own (the draft moved to Sent, or the delivered message appended when the gateway had to add a Message-ID or Date; Bcc stays in that copy); a server copy arriving within the next hour then replaces the gateway's copy (same Email id) and switches the mailbox to `server`. `POST /upstreams/{id}/send-test` decides it at once: it sends a message to the mailbox itself, looks for the copy in Sent, deletes the test message and stores the result.
- **Senders.** The `From` addresses and the envelope sender must be identities of the account, else `forbiddenFrom` / `forbiddenMailFrom`. An IMAP mailbox has its own address plus the aliases you register under `/upstreams/{id}/identities`; `*@example.com` allows any address of the domain. Names, Reply-To, Bcc and signatures are stored by the gateway and shared by every client (`Identity/set`). Identity ids are random and stable.
- **SMTP errors.** A recipient refused with 5xx cancels the whole message: `invalidRecipients` with the addresses. 552 is `tooLarge`; 4xx replies are retried twice, then `rateLimit`; 535 turns the mailbox to `authRequired` and the submission fails with `serverUnavailable` (`czlUpstream.code` 40110); a refused sender is `forbiddenMailFrom`, a refused message `forbiddenToSend`. The server's own reply only goes to the gateway log, never to the client.
- **Duplicates.** Submitting the same draft again within 10 minutes (same content, or the same Message-ID after the server's copy took over the id) returns the first submission instead of sending twice, without running `onSuccessUpdateEmail` again.
- **Provider APIs.** A mailbox whose upstream implements `upstream.MessageSender` (Gmail) sends through the provider instead of SMTP: recipients come from the header (an `envelope` with other recipients is refused), and the provider files the Sent copy under the draft's id. Its identities come from `upstream.IdentitySource` and keep the provider's ids.

## Admin API

JSON under `/api/admin/`, responses `{"code","data","msg"}`. The session is an HttpOnly cookie (`gw_session`); writes need the `csrf_token` cookie's value in an `X-CSRF-Token` header and a JSON body. Every write is recorded in the audit log with password, secret, token and key fields masked; records are kept 180 days.

| Endpoint | |
| --- | --- |
| `GET /setup`, `POST /setup` | setup status; create the owner with `{token, email, name, password}` |
| `POST /login`, `POST /logout`, `POST /register` | sign in with `{email, password}`; sign out; sign up while registration is open |
| `GET /me`, `PUT /me/password` | the signed-in user; change password with `{currentPassword, newPassword}` (signs out other sessions) |
| `GET /app-passwords`, `POST /app-passwords`, `DELETE /app-passwords/{id}` | your app passwords; create with `{name}` (the response holds the password); revoke |
| `GET /users`, `POST /users`, `GET /users/{id}`, `PATCH /users/{id}`, `DELETE /users/{id}` | admins: manage users (`{email, name, password, role, disabled}`) |
| `GET /upstreams`, `POST /upstreams`, `POST /upstreams/test` | your mailboxes (admins: `?user=<id>`, or `"user"` in the body); add an IMAP mailbox; test settings without saving |
| `GET /upstreams/{id}`, `PATCH /upstreams/{id}`, `DELETE /upstreams/{id}` | one mailbox; change it (sections left out stay, empty passwords keep the stored ones); delete it |
| `POST /upstreams/{id}/test`, `/pause`, `/resume`, `/default` | test the saved settings (optionally with changes, not saved); pause; resume; make it the primary account |
| `PATCH /upstreams/{id}` with `{"sentCopy": "auto"\|"gateway"\|"server"}`, `POST /upstreams/{id}/send-test` | how sent mail reaches Sent (see Sending); test send → `{ok, sentCopy, serverSaves, waitedMs, code?, msg?, detail?}` |
| `GET /upstreams/{id}/identities`, `POST …/identities`, `PATCH …/identities/{identityId}`, `DELETE …/identities/{identityId}` | sending identities `{id, source, email, name, replyTo, bcc, textSignature, htmlSignature, mayDelete, updatedAt}`; `source` is `primary` (the mailbox address), `alias` (yours, `*@domain` allowed) or `provider` (Gmail send-as); only aliases can be deleted, only an alias's address can change |
| `POST /upstreams/oauth/{provider}/start` | add a Gmail (`google`) or Microsoft mailbox: `{user?, name?, email?, proxy?}` → `{url}` of the provider's sign-in page |
| `POST /upstreams/{id}/reauth` | OAuth mailboxes: `{url}` to sign in again (same account id) |
| `GET /oauth-apps`, `PUT /oauth-apps/{provider}`, `DELETE /oauth-apps/{provider}` | admins: OAuth apps `[{provider, configured, clientId, hasSecret, redirectUri, scopes, tenant?, secretExpiresAt, updatedAt}]`; set with `{clientId, clientSecret?, tenant?, secretExpiresAt?}` (secret left out or `""` keeps it; required the first time) |
| `GET /settings`, `PATCH /settings` | admins: settings, update with `{"key": value}`; a secret setting takes a string (`""` keeps it, `null` clears it) |
| `GET /audit-logs?cursor=&limit=` | admins: audit log, newest first |
| `GET /overview?scope=mine\|all&range=24h\|7d\|30d` | upstream cards, `statusCounts`, `onlineClients`, `todos` and `trend`; `scope` defaults to `all` for admins and must be `mine` for users |
| `GET /system`, `POST /system/master-key-backup` | admins: version, database, master key and startup environment (secrets shown as `set`); record that the master key is backed up |
| `GET /system/tasks`, `POST /system/tasks/{name}/run` | admins: background jobs with their last 20 runs; run one now and wait for it |

`GET /setup` also says whether self-service registration is open (`registrationOpen`), so the sign-in page can offer it.

The overview's `trend` has one point per hour of the window, oldest first, ending with the current hour: `total` merges all mailboxes, `series` holds the five busiest mailboxes by requests plus `other` for the rest, and `summary` / `previous` sum the window and the one before it. Latencies are quantiles over a histogram of all pushes in the hour or window, `null` without pushes. Counts are kept per mailbox and hour for 30 days.

Error codes (HTTP status + sequence; upstream input errors use the x30 sequence):

| Code | msg | | Code | msg |
| --- | --- | --- | --- | --- |
| 40000 | `invalid_request` | | 40102 | `account_disabled` |
| 40001 | `invalid_email` | | 40103 | `invalid_setup_token` |
| 40002 | `invalid_password` | | 40300 | `csrf_failed` |
| 40003 | `invalid_role` | | 40301 | `forbidden` |
| 40004 | `invalid_name` | | 40302 | `cannot_modify_self` |
| 40005 | `unknown_setting` | | 40303 | `registration_closed` |
| 40006 | `invalid_setting` | | 40400 | `not_found` |
| 40007 | `wrong_password` | | 40401 | `user_not_found` |
| 40008 | `invalid_cursor` | | 40402 | `app_password_not_found` |
| 40009 | `invalid_scope` | | 40403 | `task_not_found` |
| 40010 | `invalid_range` | | 40430 | `upstream_not_found` |
| 40030 | `invalid_host` | | 40900 | `email_taken` |
| 40031 | `invalid_port` | | 40901 | `setup_completed` |
| 40032 | `invalid_tls_mode` | | 40902 | `task_running` |
| 40033 | `invalid_proxy` | | 40930 | `upstream_kind_managed` |
| 40034 | `missing_password` | | 41500 | `unsupported_media_type` |
| 40035 | `private_network_not_allowed` | | 40931 | `identity_managed` |
| 40036 | `invalid_sent_copy` | | 40932 | `upstream_not_running` |
| 40037 | `invalid_identity` | | 40933 | `send_test_unsupported` |
| 40100 | `unauthenticated` | | 42900 | `too_many_requests` |
| 40101 | `invalid_credentials` | | 50000 | `internal_error` |
| 40431 | `identity_not_found` | | 50300 | `database_unavailable` |

OAuth codes: 40038 `missing_client_secret`, 40039 `invalid_oauth_state`, 40040 `invalid_secret_expiry`, 40041 `invalid_client_id`, 40330 `oauth_denied`, 40331 `oauth_scope_denied`, 40432 `oauth_provider_not_found`, 40934 `oauth_app_not_configured`, 40935 `oauth_account_mismatch`, 40936 `oauth_kind_mismatch`, 50230 `oauth_exchange_failed`.

Connection tests report per server: 40110 `authentication_failed`, 40311 `private_network_not_allowed`, 40312 `smtp_auth_disabled` and 40313 `imap_access_disabled` (Microsoft), 42910 `rate_limited`, 50210 `unreachable` / `timeout` / `connection_closed`, 50211 `proxy_failed`, 50212 `tls_failed`, 50213 `server_rejected`.

`GET /oauth/{provider}/callback` (outside `/api`) is where the provider sends the browser back; it answers with a redirect to `/admin/mailboxes/` carrying `added`, `reauthorized` or `oauthError` + `code`.

Only the gateway's own credentials produce 401; problems with an upstream mailbox never do.

## JMAP endpoints

All endpoints except the two `.well-known` files use HTTP Basic with the user's app password. Only invalid credentials answer 401 (429 with `Retry-After` after too many failures); a failing mailbox never does. Disabling a user or revoking an app password ends that user's EventSource streams; disabling or deleting a user also deletes their push subscriptions, and revoking an app password deletes the ones created with it.

| Endpoint | |
| --- | --- |
| `GET /.well-known/jmap` | 302 to `/jmap/session` |
| `GET /jmap/session` | Session resource; 403 `application/problem+json` with type `https://czl.net/jmap/gateway/no-upstream` (code 40301) while the user has no mailbox |
| `POST /jmap/api` | method calls, with result references |
| `POST /jmap/upload/{accountId}/` | upload a blob (kept 24 h unless used), answers 200 like Stalwart |
| `GET /jmap/download/{accountId}/{blobId}/{name}?type=` | download; 503 problem JSON with `czlUpstream` when the mailbox is unavailable |
| `GET /jmap/eventsource?types=&closeafter=&ping=` | push: changes within 500 ms are merged into one `StateChange`, possibly for several accounts |
| `GET /.well-known/czlmail.json` | `{"version":1,"gateway":{"name","adminUrl","protocolVersion","version"}}`, so czlmail knows it talks to a gateway |

Limits (`urn:ietf:params:jmap:core`): `maxCallsInRequest` 16, `maxObjectsInGet`/`Set` 500, `maxSizeUpload` 50 MB, `maxConcurrentRequests` 4 per user, `maxSizeRequest` 10 MB.

Each upstream mailbox is one account (`isPersonal: true`). czlmail extensions:

- Session capability `https://czl.net/jmap/gateway`: `version`, `protocolVersion` (1), `adminUrl`.
- Account capability `https://czl.net/jmap/upstream`: `kind` (`imap`, `gmail`, `microsoft`), `status` (`ok`, `syncing`, `authRequired`, `unreachable`, `paused`), `statusSince`, `code`, `reauthUrl`, `initialSync` (`{"done":1200,"total":48000}` while the first import runs, else null). An account never leaves the Session because its upstream fails; its status changes, so does the Session `state`, and the user's EventSource streams are closed so the client re-reads the Session.
- A call that needs an unavailable upstream fails with `serverUnavailable` plus `czlUpstream: {status, code}`.

### Push (iOS background notifications)

czlmail on iOS is suspended seconds after it leaves the foreground, so it asks the gateway to push new mail to the CZL Connect push relay (`https://connect.czl.net/api/push`), which turns it into an APNs notification. It works exactly as with Stalwart; nothing needs to be configured.

- `PushSubscription/get` and `/set` (RFC 8620 §7.2, `using` core). `url` must be `https://`; it and `keys` are write-only. A new subscription gets an encrypted `PushVerification` at once and receives nothing else until the client sets `verificationCode`. `expires` is capped at 7 days (null or missing means 7 days); expired subscriptions disappear and are pruned every 6 hours. At most 32 per user.
- Messages are encrypted with the subscription's `keys` (RFC 8291, `aes128gcm`, one record) and sent with `TTL` and `Urgency`. Transient failures (network, 429, 5xx) are retried 5 times with exponential backoff; 404 and 410 delete the subscription; other 4xx drop the message.
- New-mail push (draft-ietf-jmap-emailpush): session capability `urn:ietf:params:jmap:emailpush` (`{}`), and on every mail account `{"filterConditions":[…],"maxFilterDepth":4,"countProperties":[]}` (no `EmailCount`). The `emailPush` property maps an accountId to `{filter, properties, urgency}`; matching delivered messages are sent as `{"@type":"EmailPush","accountId","emails":[…],"state"}`. Filters support `inMailbox`, `inMailboxOtherThan`, `hasKeyword`, `notKeyword`, `from`, `to`, `cc`, `bcc`, `subject` and `AND`/`OR`/`NOT`.
- One message's plaintext stays within 2600 bytes: the relay drops the ciphertext from the APNs payload (4 KB, base64url) beyond about 2900 bytes and the phone then only shows "New mail". Messages are batched while they fit and split otherwise; a message too large alone keeps the longest prefix of the requested `properties`.
- `StateChange` pushes follow `types` (czlmail sends `[]`, so only `EmailPush`). Push uses the same 500 ms debounce as EventSource.
- Push URLs that resolve to private, loopback or link-local addresses are refused, at creation and again at connect time, unless `security.allow_private_networks` is on. Redirects are not followed.
- `url` and `keys.auth` are sealed in `push_subscriptions` (purpose `push-key`).

Upstream codes: 40110 `authRequired`, 50210 `unreachable`, 42910 `unreachable` because the provider throttles the gateway, 50310 `syncing`, 42310 `paused`.

Request-level codes (problem JSON): 40020 `not_json`, 40021 `not_request`, 40022 `unknown_capability`, 40023 `limit_exceeded` (with `limit`), 40024 `invalid_query`, 40100 `unauthenticated`, 40301 `no_upstream`, 40420 `account_not_found`, 40421 `blob_not_found`, 41320 `upload_too_large`, 42900 `too_many_requests`, 50000 `internal_error`, 50320 `upstream_unavailable`. JMAP codes use the x20 sequence and upstream codes x10 so they stay apart from the admin API's; 40301 means `no_upstream` on `/jmap/session` and `forbidden` in the admin API.

### Mail (`urn:ietf:params:jmap:mail`)

| Method | Notes |
| --- | --- |
| `Mailbox/get`, `Mailbox/changes` | roles from SPECIAL-USE, then folder names; `totalEmails` / `unreadEmails` / `totalThreads` / `unreadThreads` counted from the index; `updatedProperties` lists the four counts when only they changed |
| `Mailbox/query` | filter `parentId`, `name`, `role`, `hasAnyRole`, `isSubscribed`; sort `sortOrder`, `name`; `sortAsTree`, `filterAsTree` |
| `Mailbox/set` | create, rename / move (the id is kept, children follow), destroy (`mailboxHasChild`, `mailboxHasEmail` unless `onDestroyRemoveEmails`); roles, subscriptions and `sortOrder` come from the server and cannot be set |
| `Email/get` | every property, `header:{name}[:as{Form}][:all]`, `bodyProperties`, `fetchTextBodyValues` / `fetchHTMLBodyValues` / `fetchAllBodyValues`, `maxBodyValueBytes`; header fields and body parts are fetched from the server on demand and cached on disk (500 MB, least recently used out); `textBody` / `htmlBody` / `attachments` follow RFC 8621 §4.1.4, so a message without HTML has its text part in `htmlBody` |
| `Email/changes`, `Thread/get`, `Thread/changes` | from the changelog; `Thread/get` needs `ids` |
| `Email/query` | mailbox, keyword, thread keyword, date, size, attachment, from / to / cc / subject from the index; `text`, `body`, `bcc`, `header` with the server's `UID SEARCH` (four folders at a time when no `inMailbox`); sort `receivedAt`, `sentAt`, `size`, `subject`, `from`, `to`, `hasKeyword`, `someInThreadHaveKeyword`, `allInThreadHaveKeyword`; `position`, `anchor`, `anchorOffset`, `limit` (at most 10,000), `calculateTotal`, `collapseThreads`; `canCalculateChanges` is false |
| `Email/queryChanges`, `Mailbox/queryChanges` | always `cannotCalculateChanges` |
| `Email/set` | `keywords` and `mailboxIds` (whole or `keywords/…`, `mailboxIds/…`) become STORE and MOVE, one command per identical change; destroy is `\Deleted` + `UID EXPUNGE`; create builds the message from `from`…`subject`, `textBody` / `htmlBody` with `bodyValues` and `attachments` by `blobId` (not `bodyStructure`) and APPENDs it; `ifInState`; an email is in exactly one mailbox (`tooManyMailboxes`) |
| `Email/import` | APPEND of an uploaded (or stored) blob |
| `SearchSnippet/get` | subject and preview with `<mark>` around the filter's terms; no body snippets |

Blob ids: `m<emailId>` is the whole message as stored, `p<emailId>.<section>` one part with its transfer encoding removed (`section` is the IMAP section number). Downloads go through the same cache; an uncached blob of an unavailable mailbox answers 503 with `czlUpstream`.

While a mailbox is `authRequired` or `unreachable`, everything the index holds still reads (mailboxes, list properties, queries without server-side terms, changes); a call that needs the server (body values, header fields, server-side search, any write) fails with `serverUnavailable`. Before the first import finishes every Mail method fails with code 50310.

The engine is `pkg/jmapserver` (RFC 8620 Core, no mail semantics): authentication and the user's accounts are injected (`Authenticator`, `AccountSource`), methods are registered with `Register(name, capability, fn)`, downloads of blob ids other than uploads (`u…`) go to a `BlobSource` registered for the id's first letter, and changes are published with `Changes().Publish(userID, {accountId: {type: state}})`. `EnablePush(ctx, PushOptions{Store, …})` adds PushSubscription and emailpush; the mail layer reports every delivered message with `NewMail(userID, jmapserver.NewMail{…})` (sender, recipients, subject, mailboxes and keywords for filters, plus the Email/get-shaped properties to push) next to its `Publish`. Accounts with `urn:ietf:params:jmap:mail` get the emailpush account capability automatically.

## Development

```sh
cd gateway
go test ./...
GATEWAY_MASTER_KEY=$(openssl rand -base64 32) GATEWAY_BASE_URL=http://localhost:8080 GATEWAY_DATA_DIR=./data go run .
```

- Separate Go module (`github.com/woodchen-ink/czlmail/gateway`); it does not import `desktop/`.
- Top-level packages: `config` (startup env), `database` (SQLite, pure Go), `model`, `service` (business logic and data access), `handler`, `router`, `middleware`, `initapp`, `cron`, `pkg` (`pkg/crypto` for sealed secrets and argon2id password hashes, `pkg/jmapserver` for the JMAP Core engine, `pkg/netx` for per-upstream dialers, `pkg/imapc` for the IMAP client; `pkg/*test` are test helpers, see Testing).
- Upstreams: `service/upstream` defines the `Upstream` interface, `Config`, error classes (`KindAuth`, `KindUnreachable`, `KindThrottled`, `KindRejected`) and the `Runner` (state machine, backoff, polling, watcher restarts); `service/upstream/imap` implements it (Gmail and Microsoft reuse it with their own sign-in and `Dialect`); `service/mailindex` is the per-upstream index and changelog (`data/upstreams/<accountId>.sqlite`, states `<generation>-<seq>`); `service.UpstreamManager` runs one runner per mailbox, publishes every index commit (sync rounds and gateway writes alike, through `mailindex.Store.OnCommit`) to the push bus and closes the user's streams on status changes; `UpstreamManager.Lookup(accountId)` gives JMAP methods the running upstream or the `UpstreamError` for `serverUnavailable`.
- JMAP Mail: `service/mail` registers the Mail methods and the `m` / `p` blob sources (`initapp` wires it). Reads use the index (`mailindex.Store.DB`, rows with `orphaned_from <> ''` do not exist); bodies go through `mail.Cache` (`data/cache/mail/<accountId>/`); searches the index cannot answer go to `Upstream.Search` (a provider can replace the criteria with `imap.Dialect.SearchCriteria`, e.g. Gmail's `X-GM-RAW`). Emails the sync engine finds new (`mailindex.Tx.Delivered`) reach `mail.Options.OnNewMail`, which `initapp` forwards to `jmapserver.Server.NewMail` for push, except in Sent, Drafts, Trash and Junk. `Service.ImplicitEmailSet` runs the Email/set part of `onSuccessUpdateEmail` / `onSuccessDestroyEmail` for EmailSubmission.
- `pkg/imapc` instead of go-imap: go-imap v2 rejects responses it does not know (`unsupported msg-att name`, `unsupported response type`) and drops the connection, so VANISHED (QRESYNC), EMAILID / X-GUID and X-GM-* cannot be read with it. `imapc` parses every response into a generic value tree first.
- Sending: `service/submission` serves `EmailSubmission/*` and `Identity/*` (identities, submissions and their changelog live in `gateway.sqlite`, keyed by accountId). IMAP delivery goes through `upstream.Sender` (SMTP via `imap.DialSMTP`) and the Sent copy through `upstream.SentFiler` (`PrepareSent` before sending, `FileSent` after), the test send through `upstream.SentCopyTester`. Provider APIs implement the optional `upstream.MessageSender` and `upstream.IdentitySource` (`service/upstream/optional.go`, from #42) instead.
- Settings are registered once in `service/settings.go` (key, type, default) and read through `service.Settings`; never query the `settings` table directly.
- JMAP endpoints authenticate with `service.BasicAuthenticator` through `initapp.jmapAuth`, which also reports the app password used (`ErrUnauthenticated` → 401, `*service.RateLimitError` → 429), and subscribe to `service.Invalidations` to close the EventSource connections and delete the push subscriptions of revoked credentials.
- JMAP tests drive the server with go-jmap, the client library czlmail uses.
- `initapp.NewApp(cfg, db, log, version)` assembles the whole gateway (services, router, background jobs); `Serve` and the end-to-end tests both use it, so tests run the real routes. `NewAppWith(…, initapp.Options)` / `gatewaytest.StartWith` shorten the sending waits for tests.
- A table column holding a sealed secret must call `service.RegisterSealedColumn` so `rotate-key` re-encrypts it. Seal with the row's public id in the AAD, never the auto-increment key.
- Errors to clients are a numeric code plus an English identifier (`{"code":40400,"msg":"not_found"}`); upstream server replies go to logs and the admin only.
- Logs are JSON lines with `trace_id`; every response carries `X-Request-Id`.
- Upstream activity for the overview trend is recorded by `service/metrics.Recorder`, which `UpstreamManager.SetMetrics` hands to every upstream it starts: each IMAP command counts as a request (`upstream.Config.OnRequest`, via `imapc.Options.OnCommand`); the runner counts a rate-limit hit when a round fails as `KindThrottled` and a push latency from a watch hint to the published change; removing an upstream forgets its counts. Recording only touches memory; counts are flushed every minute. New upstream kinds (#42 / #43) call `Config.OnRequest` per HTTP request. Upstream cards come from `service.Upstreams` (`UpstreamSource`); the Microsoft secret expiry will come from `App.Overview.Secrets` (`OAuthSecretSource`, #43).
- Background jobs are `cron.Task`s registered in `initapp.NewApp`; they show in the admin's task center.

### Admin web

`web/` is a Next.js static export (basePath `/admin`) with Tailwind and shadcn/ui; the gateway serves `web/out` (see `handler/admin_web.go`). All API calls go through `web/lib/api/`; every text is in `web/lib/i18n/zh-CN.ts` and `en.ts`, which must have the same keys (`npm run i18n:check`, run in CI).

```sh
cd gateway/web
npm ci
npm run dev       # http://localhost:3000/admin/, /api proxied to GATEWAY_DEV_URL (default http://localhost:8080)
npm run lint && npm run typecheck && npm run i18n:check && npm run build   # build writes web/out, which `go run .` serves at /admin/
```

### Testing

`go test ./...` passes without Docker: tests that need a real server skip. To run them, start the test servers and point the tests at them:

```sh
cd gateway
docker compose -f docker-compose.test.yml up -d
export GATEWAY_TEST_IMAP_ADDR=127.0.0.1:1143 GATEWAY_TEST_IMAP_BASIC_ADDR=127.0.0.1:1144 \
       GATEWAY_TEST_SMTP_ADDR=127.0.0.1:1025 GATEWAY_TEST_MAILPIT_URL=http://127.0.0.1:8025
go test ./...
docker compose -f docker-compose.test.yml down -v
```

PowerShell: set the same variables with `$env:GATEWAY_TEST_IMAP_ADDR = "127.0.0.1:1143"` and so on. CI (`.github/workflows/gateway.yml`) starts the same compose file and runs `go test -race ./...` with these variables.

| Server | Port | What it is |
| --- | --- | --- |
| `dovecot` | 1143 | Dovecot 2.4 with CONDSTORE, QRESYNC, NOTIFY, IDLE, MOVE, UIDPLUS, SPECIAL-USE, LIST-STATUS |
| `dovecot-basic` | 1144 | The same without CONDSTORE / QRESYNC / NOTIFY advertised, for the STATUS polling tier. Dovecot still honours CONDSTORE if asked, so choose the tier by advertised capabilities |
| `mailpit` | 1025, 8025 | SMTP sink accepting any credentials without TLS; API to read what was sent |

Test-only settings: plaintext auth, users `alice` / `alice-pass` and `bob` / `bob-pass`, and any other user name with password `test` gets its own empty mailbox (INBOX, Drafts, Sent, Trash, Junk, Archive). Nothing persists across `down`.

Dovecot has no OBJECTID (RFC 8474); it offers `X-GUID` instead. OBJECTID is exercised with the fake server below (`Options.ObjectID`).

Test helpers in `pkg/` (imported by tests only):

- `pkg/imaptest`: `Dovecot(t)` / `DovecotBasic(t)` → `NewUser(t)` (a fresh mailbox per test) → `Dial(t)` for setup: `Create`, `Delete`, `Recreate` (new UIDVALIDITY), `Append` / `AppendSample`, `AddFlags`, `RemoveFlags`, `Move`, `Expunge`, `UIDs`. `DialRaw` is a line-level client for checking exact replies and extensions.
- `pkg/smtptest`: Mailpit lookups; send each test's mail to its own `UniqueAddress`. `NewFake(t)` is a scripted SMTP server for replies Mailpit never gives (refused recipients, 552, 421, 535). Mailpit's raw view starts with its own `Bcc:` / `Received:` lines; envelope-only recipients appear in the summary's `Bcc`.
- `pkg/gmailtest`: in-memory Gmail account. The IMAP side has X-GM-MSGID / X-GM-THRID / X-GM-LABELS (read and STORE), X-GM-RAW search, XLIST and SPECIAL-USE, APPENDUID / COPYUID, CONDSTORE and IDLE, with per-folder UIDs over one label store, so the jmap-perl#125 traps are reproducible (UIDs only valid in their own folder, labels with spaces must be quoted, `\Draft` flag = `[Gmail]/Drafts`). `SetIntercept` injects command failures, `DropConnections` simulates a network cut. `NewAPI` is the Gmail REST stand-in (`users.messages.send` incl. media upload, `settings.sendAs`, `profile`); sent mail appears in `[Gmail]/Sent Mail` under the returned id.
- `pkg/oauthtest`: OAuth provider stand-in (authorization endpoint, token endpoint with PKCE); `RevokeRefreshToken`, `RevokeUser`, `FailNext("invalid_grant")`, `FailNextWith(Failure{Code, AADSTS, Status})` for Microsoft-shaped errors (`error_codes`, `AADSTSnnnn:` descriptions), `ExpireAccessTokens`. Grants whose authorization asked for `openid` get an unsigned `id_token` (`email`, `preferred_username`, `tid`). Its access tokens are what the Gmail fakes accept.
- `pkg/gatewaytest`: starts the gateway in-process with `initapp.NewApp` and returns a go-jmap client. `NewUser` goes through the admin API (owner setup token, `POST /api/admin/users`, login, app password); `SessionProblem` reads the Session status (403 `no-upstream` until a mailbox is added). `AddIMAPUpstream` adds a Dovecot user through the admin API and waits for its first sync (`IMAPUpstreamBody`, `CreateUpstream`, `WaitUpstream` for custom cases); `AddGmailUpstream` skips until #42.
- `pkg/netx/netxtest`: a SOCKS5 server (no-auth or username/password) that records targets and can be stopped like a failing proxy.

### Releases

Gateway versions are dated like the app (`YYYY.M.N`) but independent of it. Release with an annotated tag `gateway-vYYYY.M.N`; `.github/workflows/gateway.yml` publishes the images. The gateway never creates a GitHub Release, because the desktop and Android updaters read `releases/latest`. Label gateway PRs `gateway` so they stay out of the app's release notes.
