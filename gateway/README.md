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

## Development

```sh
cd gateway
go test ./...
GATEWAY_MASTER_KEY=$(openssl rand -base64 32) GATEWAY_BASE_URL=http://localhost:8080 GATEWAY_DATA_DIR=./data go run .
```

- Separate Go module (`github.com/woodchen-ink/czlmail/gateway`); it does not import `desktop/`.
- Top-level packages: `config` (startup env), `database` (SQLite, pure Go), `model`, `service` (business logic and data access), `handler`, `router`, `middleware`, `initapp`, `cron`, `pkg` (`pkg/crypto` for sealed secrets and argon2id password hashes).
- Settings are registered once in `service/settings.go` (key, type, default) and read through `service.Settings`; never query the `settings` table directly.
- JMAP endpoints authenticate with `service.BasicAuthenticator.Authenticate` (`ErrUnauthenticated` → 401, `*service.RateLimitError` → 429) and subscribe to `service.Invalidations` to close the EventSource connections of users whose credentials were revoked.
- A table column holding a sealed secret must call `service.RegisterSealedColumn` so `rotate-key` re-encrypts it. Seal with the row's public id in the AAD, never the auto-increment key.
- Errors to clients are a numeric code plus an English identifier (`{"code":40400,"msg":"not_found"}`); upstream server replies go to logs and the admin only.
- Logs are JSON lines with `trace_id`; every response carries `X-Request-Id`.

### Releases

Gateway versions are dated like the app (`YYYY.M.N`) but independent of it. Release with an annotated tag `gateway-vYYYY.M.N`; `.github/workflows/gateway.yml` publishes the images. The gateway never creates a GitHub Release, because the desktop and Android updaters read `releases/latest`. Label gateway PRs `gateway` so they stay out of the app's release notes.
