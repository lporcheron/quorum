# Quorum

Self-hostable availability polls: propose dates, share a link, guests
vote **yes / if need be / no** without an account, you finalize, and
everyone gets the event in their calendar.

A single static Go binary. SQLite by default. No Node, no Redis, no
external services.

## Features

- Create a poll in seconds, with or without an account — time slots
  (timezone-aware, DST-safe) or whole days
- Guest voting with a personal link to come back and change a vote;
  live tallies with the leading option highlighted
- Comments, hidden-participants mode, required-email mode
- Accounts via Google, GitHub, Microsoft, **any OIDC provider**, or
  email magic links; polls claimable from their admin link
- Spaces (organizations) with `owner`/`admin`/`member` roles, email
  invitations, ownership transfer, per-space retention and timezone
- Finalize a date → participants receive the `.ics` invitation
  (`METHOD:REQUEST`); cancel → they receive the `CANCEL`; per-poll
  calendar feed and CSV export
- Background job queue in SQLite with retries and a visible dead-letter
- English and French, automatic with a manual switch; dark mode
- Rate limiting, automatic purge of inactive polls (with a warning
  email), instance admin page, optional Prometheus metrics

## Run it

```sh
docker compose up          # see docker-compose.yml
# or, with Go ≥ 1.26:
make run                   # builds and starts on :8080
make dev                   # live-reload development loop
make test lint             # what CI runs
```

Deploying is copying one binary (or one ~15 MB container image) and
mounting one directory for the SQLite file.

## Configuration

Everything is optional; defaults in parentheses.

### Core

| Variable | Purpose |
|---|---|
| `QUORUM_ADDR` | Listen address (`:8080`) |
| `QUORUM_BASE_URL` | Public URL of the instance (`http://localhost:8080`) |
| `QUORUM_DB_PATH` | SQLite database file (`quorum.db`; `/data/quorum.db` in Docker) |
| `QUORUM_LOG_LEVEL` | `debug`, `info`, `warn`, `error` (`info`) |
| `QUORUM_LOG_FORMAT` | `json` or `text` (`json`) |

### Sign-in (without any of these, polls stay guest-only)

| Variable | Purpose |
|---|---|
| `QUORUM_OAUTH_GOOGLE_CLIENT_ID` / `_CLIENT_SECRET` | Google sign-in |
| `QUORUM_OAUTH_GITHUB_CLIENT_ID` / `_CLIENT_SECRET` | GitHub sign-in |
| `QUORUM_OAUTH_MICROSOFT_CLIENT_ID` / `_CLIENT_SECRET` | Microsoft sign-in |
| `QUORUM_OAUTH_MICROSOFT_TENANT` | Entra tenant (`common`) |
| `QUORUM_OIDC_ISSUER_URL` | Generic OIDC discovery URL |
| `QUORUM_OAUTH_OIDC_CLIENT_ID` / `_CLIENT_SECRET` | Generic OIDC client |
| `QUORUM_OIDC_NAME` | Label on the OIDC login button (`SSO`) |

OAuth callback URLs are `<base URL>/auth/<google|github|microsoft|oidc>/callback`.

### Email (without SMTP, magic links and notifications are disabled and the UI says so)

| Variable | Purpose |
|---|---|
| `QUORUM_SMTP_HOST` | SMTP server; setting it enables email |
| `QUORUM_SMTP_PORT` | SMTP port (`587`) |
| `QUORUM_SMTP_USERNAME` / `QUORUM_SMTP_PASSWORD` | SMTP credentials (optional) |
| `QUORUM_SMTP_FROM` | Sender address (required with SMTP) |

### Instance policy

| Variable | Purpose |
|---|---|
| `QUORUM_ADMIN_EMAILS` | Comma-separated account emails granted the `/admin` page |
| `QUORUM_REGISTRATIONS_OPEN` | Allow new accounts (`true`); overridable at runtime from `/admin`; existing users always sign in |
| `QUORUM_EMAIL_ALLOWED_DOMAINS` | Comma-separated sign-up domain allowlist (empty = all) |
| `QUORUM_TRUST_PROXY` | Use `X-Forwarded-For` for rate limiting (`false`; enable only behind a proxy that sets it) |
| `QUORUM_METRICS` | Serve Prometheus metrics on `/metrics` (`false`) |
| `DATABASE_URL` | Reserved for PostgreSQL support (rejected for now) |

## License

[Apache 2.0](LICENSE).
