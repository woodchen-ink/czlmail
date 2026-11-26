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

## Development

```sh
cd gateway
go test ./...
GATEWAY_MASTER_KEY=$(openssl rand -base64 32) GATEWAY_BASE_URL=http://localhost:8080 GATEWAY_DATA_DIR=./data go run .
```

- Separate Go module (`github.com/woodchen-ink/czlmail/gateway`); it does not import `desktop/`.
- Top-level packages: `config` (startup env), `database` (SQLite, pure Go), `model`, `service` (business logic and data access), `handler`, `router`, `middleware`, `initapp`, `cron`, `pkg` (`pkg/crypto` for sealed secrets).
- A table column holding a sealed secret must call `service.RegisterSealedColumn` so `rotate-key` re-encrypts it. Seal with the row's public id in the AAD, never the auto-increment key.
- Errors to clients are a numeric code plus an English identifier (`{"code":40400,"msg":"not_found"}`); upstream server replies go to logs and the admin only.
- Logs are JSON lines with `trace_id`; every response carries `X-Request-Id`.

### Releases

Gateway versions are dated like the app (`YYYY.M.N`) but independent of it. Release with an annotated tag `gateway-vYYYY.M.N`; `.github/workflows/gateway.yml` publishes the images. The gateway never creates a GitHub Release, because the desktop and Android updaters read `releases/latest`. Label gateway PRs `gateway` so they stay out of the app's release notes.
