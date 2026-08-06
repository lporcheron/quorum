# Quorum

Self-hostable availability polls: propose dates, share a link, guests
vote **yes / if need be / no** without an account, you finalize, and
everyone gets the event in their calendar.

A single static Go binary. SQLite by default, PostgreSQL when you want
it. No Node, no Redis, no external services.

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

Grab a release — a static binary for your platform from the
[releases page](https://github.com/lporcheron/quorum/releases), or the
container image:

```sh
docker run -p 8080:8080 -v quorum-data:/data ghcr.io/lporcheron/quorum:latest
docker compose up          # or the example docker-compose.yml
```

From source, with Go ≥ 1.26:

```sh
make run                   # builds and starts on :8080
make dev                   # live-reload development loop
make test lint             # what CI runs
make test-postgres         # the same suite against PostgreSQL
```

Deploying is copying one binary (or one ~19 MB container image) and
mounting one directory for the SQLite file.

Releases are cut manually from the *Release* GitHub Action: it mints a
CalVer tag (`YYYY.MM.DD.HHmmss`), publishes cross-compiled binaries
with checksums, and pushes the `linux/amd64` + `linux/arm64` image to
GHCR. Nothing is published on push.

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

### Database

SQLite (the default) needs nothing. For bigger instances, point
`DATABASE_URL` at PostgreSQL (≥ 13) and the same binary uses it — one
schema, one query set, migrations applied at startup either way.

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | `postgres://user:pass@host/db` switches the store to PostgreSQL (empty = SQLite at `QUORUM_DB_PATH`) |

## Backups

**SQLite** (the default): the database runs in WAL mode, so never copy
`quorum.db` alone while the server runs — you would miss the WAL. Use
one of:

```sh
sqlite3 /data/quorum.db ".backup '/backups/quorum.db'"   # safe online snapshot
```

or [Litestream](https://litestream.io) for continuous replication to
object storage. Restoring is copying the file back and starting the
server.

**PostgreSQL**: standard tooling — `pg_dump`/`pg_restore` or your
provider's snapshots. Quorum keeps no state outside the database.

## Migrating from Rallly

`rallly-import` moves an existing self-hosted
[Rallly](https://rallly.co) instance into Quorum from a plain-format
dump of its PostgreSQL database:

```sh
pg_dump --format=plain rallly > rallly.sql
go run ./cmd/rallly-import -dump rallly.sql -db quorum.db -dry-run  # rehearse
go run ./cmd/rallly-import -dump rallly.sql -db quorum.db           # SQLite target
go run ./cmd/rallly-import -dump rallly.sql -database-url postgres://…  # PostgreSQL target
```

Start with `-dry-run`: it performs every insert, reports the counts and
everything it had to skip, then rolls the transaction back and leaves
the target untouched. The real import runs in a single transaction
against a fresh database (it refuses a target that already has polls
unless you pass `-force`) and brings over users, spaces, memberships,
polls, options, participants, votes and comments. Finalized Rallly
polls come out finalized in Quorum, with the chosen option matched.

Polls keep their Rallly IDs, and Quorum answers Rallly's
`/invite/{id}` URLs with a redirect — so invitation links already in
circulation keep working once Quorum serves the old domain.

If the dump comes from a Rallly version whose schema differs from the
one the importer expects, it stops before touching the database and
names the tables and columns it could not find. That is deliberate: a
partial import is worse than none.

What to know before running it:

- **Accounts** are recreated by email only. Rallly stores no passwords
  Quorum could reuse; each user just signs in on Quorum (magic link or
  OAuth) with the same address and is reunited with their polls.
- **Anonymous guests** are not turned into accounts. Their polls are
  imported as guest polls, each with a fresh admin link the importer
  hands back once — on stdout, or into a file with
  `-links-out links.txt` (written 0600). Pass those links to their
  organizers; they cannot be recovered afterwards.
- **Participant edit links** cannot be migrated (Quorum stores only
  token hashes). Votes are all there; a voter who wants to change
  theirs votes again or asks the organizer.
- **Hidden scores** have no Quorum equivalent: a poll that hid its
  results until voting comes out with them visible, and the importer
  says so.

The dump contains personal data — delete it once the import is done.

## License

[Apache 2.0](LICENSE).
