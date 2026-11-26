# czlmail JMAP gateway

> **Work in progress.** This is the module skeleton; mail support lands in the phase 1 issues tracked in [#48](https://github.com/woodchen-ink/czlmail/issues/48).

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
{"level":"WARN","msg":"no user yet: open the setup link ...","setup_url":"https://mail-gw.example.com/setup?token=…"}
```

```sh
docker compose logs gateway | grep setup_url
```

Open the link and choose the owner's email and password (at least 10 characters). Until the admin pages land ([#44](https://github.com/woodchen-ink/czlmail/issues/44)), do the same with the API:

```sh
curl -c cookies.txt -H 'Content-Type: application/json' \
  -d '{"token":"<token from the link>","email":"you@example.com","password":"<at least 10 characters>"}' \
  https://mail-gw.example.com/api/admin/setup
```

The link works once and for 30 minutes; restart the gateway to get a new one. Once the owner exists, no link is printed again.

Roles: the **owner** (the first user) manages everyone; **admins** manage plain users and the settings; **users** manage their own app passwords. Nobody can change their own role or disable or delete themselves, so the owner always stays. Self-service registration is off by default (`site.registration_open`); connecting to private-network hosts is off by default too (`security.allow_private_networks`).

## App passwords

czlmail signs in to the gateway with HTTP Basic: your gateway **email** as the username and an **app password**, never your admin password.

1. Sign in to the admin and create an app password per device (for example "MacBook", "Phone").
2. Copy it right away: it looks like `czl_ab12cd34_…` and is shown only once. The gateway stores the `czl_ab12cd34_` prefix (shown in the list to tell devices apart) and an argon2id hash.
3. In czlmail, add a JMAP account with the gateway URL, your email and the app password.

The list shows when and from which IP each app password was last used. Revoking one, or disabling a user, takes effect on the next request even for clients whose credentials were cached, and ends their push connections.

Failed sign-ins (admin and Basic) are rate limited per username and per IP; over the limit the gateway answers `429` with `Retry-After`. Requests with valid credentials are never limited, and a device retrying the same stale password does not lock its user out.

## Admin API

JSON under `/api/admin/`, responses `{"code","data","msg"}`. The session is an HttpOnly cookie (`gw_session`); writes need the `csrf_token` cookie's value in an `X-CSRF-Token` header and a JSON body. Every write is recorded in the audit log with password, secret, token and key fields masked; records are kept 180 days.

| Endpoint | |
| --- | --- |
| `GET /setup`, `POST /setup` | setup status; create the owner with `{token, email, name, password}` |
| `POST /login`, `POST /logout`, `POST /register` | sign in with `{email, password}`; sign out; sign up while registration is open |
| `GET /me`, `PUT /me/password` | the signed-in user; change password with `{currentPassword, newPassword}` (signs out other sessions) |
| `GET /app-passwords`, `POST /app-passwords`, `DELETE /app-passwords/{id}` | your app passwords; create with `{name}` (the response holds the password); revoke |
| `GET /users`, `POST /users`, `GET /users/{id}`, `PATCH /users/{id}`, `DELETE /users/{id}` | admins: manage users (`{email, name, password, role, disabled}`) |
| `GET /settings`, `PATCH /settings` | admins: settings, update with `{"key": value}` |
| `GET /audit-logs?cursor=&limit=` | admins: audit log, newest first |

Error codes (HTTP status + sequence):

| Code | msg | | Code | msg |
| --- | --- | --- | --- | --- |
| 40000 | `invalid_request` | | 40300 | `csrf_failed` |
| 40001 | `invalid_email` | | 40301 | `forbidden` |
| 40002 | `invalid_password` | | 40302 | `cannot_modify_self` |
| 40003 | `invalid_role` | | 40303 | `registration_closed` |
| 40004 | `invalid_name` | | 40400 | `not_found` |
| 40005 | `unknown_setting` | | 40401 | `user_not_found` |
| 40006 | `invalid_setting` | | 40402 | `app_password_not_found` |
| 40007 | `wrong_password` | | 40900 | `email_taken` |
| 40008 | `invalid_cursor` | | 40901 | `setup_completed` |
| 40100 | `unauthenticated` | | 41500 | `unsupported_media_type` |
| 40101 | `invalid_credentials` | | 42900 | `too_many_requests` |
| 40102 | `account_disabled` | | 50000 | `internal_error` |
| 40103 | `invalid_setup_token` | | 50300 | `database_unavailable` |

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

The engine is `pkg/jmapserver` (RFC 8620 Core, no mail semantics): authentication and the user's accounts are injected (`Authenticator`, `AccountSource`), methods are registered with `Register(name, capability, fn)`, downloads of blob ids other than uploads (`u…`) go to a `BlobSource` registered for the id's first letter, and changes are published with `Changes().Publish(userID, {accountId: {type: state}})`. `EnablePush(ctx, PushOptions{Store, …})` adds PushSubscription and emailpush; the mail layer reports every delivered message with `NewMail(userID, jmapserver.NewMail{…})` (sender, recipients, subject, mailboxes and keywords for filters, plus the Email/get-shaped properties to push) next to its `Publish`. Accounts with `urn:ietf:params:jmap:mail` get the emailpush account capability automatically.

## Development

```sh
cd gateway
go test ./...
GATEWAY_MASTER_KEY=$(openssl rand -base64 32) GATEWAY_BASE_URL=http://localhost:8080 GATEWAY_DATA_DIR=./data go run .
```

- Separate Go module (`github.com/woodchen-ink/czlmail/gateway`); it does not import `desktop/`.
- Top-level packages: `config` (startup env), `database` (SQLite, pure Go), `model`, `service` (business logic and data access), `handler`, `router`, `middleware`, `initapp`, `cron`, `pkg` (`pkg/crypto` for sealed secrets and argon2id password hashes, `pkg/jmapserver` for the JMAP Core engine; `pkg/*test` are test helpers, see Testing).
- Settings are registered once in `service/settings.go` (key, type, default) and read through `service.Settings`; never query the `settings` table directly.
- JMAP endpoints authenticate with `service.BasicAuthenticator` through `initapp.jmapAuth`, which also reports the app password used (`ErrUnauthenticated` → 401, `*service.RateLimitError` → 429), and subscribe to `service.Invalidations` to close the EventSource connections and delete the push subscriptions of revoked credentials.
- JMAP tests drive the server with go-jmap, the client library czlmail uses.
- `initapp.NewApp(cfg, db, log, version)` assembles the whole gateway (services, router, background jobs); `Serve` and the end-to-end tests both use it, so tests run the real routes.
- A table column holding a sealed secret must call `service.RegisterSealedColumn` so `rotate-key` re-encrypts it. Seal with the row's public id in the AAD, never the auto-increment key.
- Errors to clients are a numeric code plus an English identifier (`{"code":40400,"msg":"not_found"}`); upstream server replies go to logs and the admin only.
- Logs are JSON lines with `trace_id`; every response carries `X-Request-Id`.

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
- `pkg/smtptest`: Mailpit lookups; send each test's mail to its own `UniqueAddress`.
- `pkg/gmailtest`: in-memory Gmail account. The IMAP side has X-GM-MSGID / X-GM-THRID / X-GM-LABELS (read and STORE), X-GM-RAW search, XLIST and SPECIAL-USE, APPENDUID / COPYUID, CONDSTORE and IDLE, with per-folder UIDs over one label store, so the jmap-perl#125 traps are reproducible (UIDs only valid in their own folder, labels with spaces must be quoted, `\Draft` flag = `[Gmail]/Drafts`). `SetIntercept` injects command failures, `DropConnections` simulates a network cut. `NewAPI` is the Gmail REST stand-in (`users.messages.send` incl. media upload, `settings.sendAs`, `profile`); sent mail appears in `[Gmail]/Sent Mail` under the returned id.
- `pkg/oauthtest`: OAuth provider stand-in (authorization endpoint, token endpoint with PKCE); `RevokeRefreshToken`, `FailNext("invalid_grant")`, `ExpireAccessTokens`. Its access tokens are what the Gmail fakes accept.
- `pkg/gatewaytest`: starts the gateway in-process with `initapp.NewApp` and returns a go-jmap client. `NewUser` goes through the admin API (owner setup token, `POST /api/admin/users`, login, app password); `SessionProblem` reads the Session status (403 `no-upstream` until a mailbox is added). `AddIMAPUpstream` / `AddGmailUpstream` skip the test until #39 / #42.

### Releases

Gateway versions are dated like the app (`YYYY.M.N`) but independent of it. Release with an annotated tag `gateway-vYYYY.M.N`; `.github/workflows/gateway.yml` publishes the images. The gateway never creates a GitHub Release, because the desktop and Android updaters read `releases/latest`. Label gateway PRs `gateway` so they stay out of the app's release notes.
