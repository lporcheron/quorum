# Quorum

Self-hostable availability polls: propose dates, share a link, guests
vote yes / if need be / no without an account, you finalize, everyone
gets a calendar invite.

A single static Go binary. SQLite by default. No Node, no Redis, no
external services.

**Status: early development (M0 — foundations).** Not usable yet.

## Build and run

Requires Go 1.26+ (`templ` and the Tailwind CLI are fetched
automatically by the Makefile).

```sh
make run           # build and start on :8080
make dev           # live-reload development loop
make test lint     # what CI runs
```

## Configuration

Everything is optional; defaults in parentheses.

| Variable | Purpose |
|---|---|
| `QUORUM_ADDR` | Listen address (`:8080`) |
| `QUORUM_BASE_URL` | Public URL of the instance (`http://localhost:8080`) |
| `QUORUM_DB_PATH` | SQLite database file (`quorum.db`) |
| `QUORUM_LOG_LEVEL` | `debug`, `info`, `warn`, `error` (`info`) |
| `QUORUM_LOG_FORMAT` | `json` or `text` (`json`) |

### Sign-in (all optional — without any of them, polls stay guest-only)

| Variable | Purpose |
|---|---|
| `QUORUM_OAUTH_GOOGLE_CLIENT_ID` / `_CLIENT_SECRET` | Google sign-in |
| `QUORUM_OAUTH_GITHUB_CLIENT_ID` / `_CLIENT_SECRET` | GitHub sign-in |
| `QUORUM_OAUTH_MICROSOFT_CLIENT_ID` / `_CLIENT_SECRET` | Microsoft sign-in |
| `QUORUM_OAUTH_MICROSOFT_TENANT` | Entra tenant (`common`) |
| `QUORUM_OIDC_ISSUER_URL` | Generic OIDC discovery URL |
| `QUORUM_OAUTH_OIDC_CLIENT_ID` / `_CLIENT_SECRET` | Generic OIDC client |
| `QUORUM_OIDC_NAME` | Label on the OIDC login button (`SSO`) |
| `QUORUM_REGISTRATIONS_OPEN` | Allow new accounts (`true`); existing users always sign in |
| `QUORUM_EMAIL_ALLOWED_DOMAINS` | Comma-separated sign-up domain allowlist (empty = all) |

OAuth callback URLs are `<base URL>/auth/<google|github|microsoft|oidc>/callback`.

### Email (optional — without SMTP, magic links are disabled and the UI says so)

| Variable | Purpose |
|---|---|
| `QUORUM_SMTP_HOST` | SMTP server; setting it enables email |
| `QUORUM_SMTP_PORT` | SMTP port (`587`) |
| `QUORUM_SMTP_USERNAME` / `QUORUM_SMTP_PASSWORD` | SMTP credentials (optional) |
| `QUORUM_SMTP_FROM` | Sender address (required with SMTP) |

## License

[Apache 2.0](LICENSE).
