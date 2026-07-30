# Quorum

Self-hostable availability-poll tool (Doodle/Rallly equivalent) in pure
Go, licensed Apache 2.0. Clean-room implementation — never copy code,
assets, or copy text from Rallly (AGPLv3) or clone its repository.

Current state: M0–M5 complete (v1 feature set). The original design document lives
in `docs/HISTORY.md` (historical; the code is the reference). Milestones: M0 foundations → M1 poll+guest voting →
M2 auth → M3 spaces → M4 email/finalize/ICS → M5 polish. One milestone
at a time; each must be mergeable and demoable alone; stop and show
after each.

## Hard constraints

- Pure Go, `CGO_ENABLED=0`, single static binary; everything (assets,
  templates, migrations, translations, fonts) via `embed.FS`.
- < 50 MB RSS idle, final Docker image < 40 MB (`scratch` or
  `distroless/static`), cold start < 100 ms.
- No Node.js at runtime or build. Tailwind v4 standalone CLI,
  downloaded by the Makefile. No `package.json` ever.
- No SPA. Server-rendered HTML (templ) + HTMX 2.x + Alpine.js (local
  UI state only), all served locally, no CDN. Custom JS < 15 KB
  uncompressed. Viewing and voting must work without JS.
- No external infra dependency. SQLite default; Postgres opt-in via
  `DATABASE_URL`; SMTP optional (without it, email features are off
  and the UI says so).
- Multi-tenant data model from day one: polls belong to spaces.
- Out of scope (do not build; flag if it seems needed): billing,
  Google Calendar sync, Calendly-style booking, mobile app, public
  versioned API, object storage/uploads, WebSockets.

## Stack (fixed — deviations must be proposed, not done)

`net/http` ServeMux (Go 1.22+ patterns) · templ · HTMX + Alpine ·
Tailwind v4 standalone · SQLite `modernc.org/sqlite` (WAL,
busy_timeout, foreign_keys=on) / Postgres `pgx/v5` · sqlc (hand-written
SQL, no ORM) · goose migrations (embedded, run at startup) ·
`x/oauth2` + `go-oidc/v3` · scs/v2 sessions (DB store; Secure,
HttpOnly, SameSite=Lax) · wneessen/go-mail · arran4/golang-ical ·
`time/tzdata` blank import · go-i18n/v2 (EN source keys from M0, FR
catalog at M5) · slog JSON · stdlib testing + httptest.

## Code conventions

- Layout: `cmd/quorum/`, `internal/{config,server,handler,poll,space,auth,mail,store,ids,i18n}`,
  `web/` (templates + static), `migrations/`, `translations/`.
- Handlers are thin: parse → domain service → render. Business logic
  lives in domain packages (`poll`, `space`, …), testable without HTTP.
- Explicit constructor injection. No DI container, no globals, no `init()`.
- `context.Context` propagated everywhere, down to SQL.
- Errors wrapped with `fmt.Errorf("...: %w", err)`. No `panic` outside
  initialization.
- All instants stored/handled in UTC; conversion to the viewer's
  timezone at render time only. All-day dates are timezone-less
  `YYYY-MM-DD`, a distinct type — never midnight timestamps.
- Timestamps in SQLite: UTC RFC 3339 TEXT, written by the app (no DB
  defaults). `time.Time` ↔ TEXT conversion only in the store layer.
- **Single SQL source for both engines.** Migrations and sqlc queries
  are written once, in the SQLite dialect restricted to the
  SQLite/PostgreSQL common subset (named `@params`, `RETURNING`,
  `ON CONFLICT`; TEXT timestamps compare lexicographically). Exactly
  three SQLite idioms are substituted when rendering migrations for
  Postgres (`INTEGER PRIMARY KEY`, ` BLOB `, ` REAL ` — see
  `store.pgSubstitutions`); placeholders `?N` are rewritten to `$N` by
  the store's DBTX adapter. Any new migration or query MUST stay in
  that subset — `make test-postgres` (and the CI postgres job) is the
  enforcement. Never create a second migrations dir or query set.
- Public IDs: base58 ~12 chars. Secret tokens (admin/edit links):
  base58 ≥26 chars, stored SHA-256-hashed, constant-time compare,
  shown once.
- Every space-scoped query goes through the single membership-check
  helper in `internal/space` — no scattered ad-hoc checks.
- All mutations are POST (no-JS form fallback); HTMX hits the same
  endpoints, handlers branch on `HX-Request` for partial vs full page.
- Table names are plural (`users` — `user` is reserved in Postgres).
- Tests must cover: timezone logic (incl. DST transitions), score
  tallying, identity merge, space access control. Integration tests on
  handlers use in-memory SQLite. Don't test template rendering.
- Every milestone ends with `make lint test` green and atomic
  Conventional Commits per logical unit.
- Releases are manual only: the *Release* workflow (workflow_dispatch)
  mints a CalVer tag `YYYY.MM.DD.HHmmss`, publishes binaries and the
  GHCR image for that tag. Never create release tags by hand and never
  publish from pushes.

## UI direction (summary)

The availability grid *is* the product; everything else recedes.
"Ballot-counting" aesthetic, not generic SaaS. Palette: ink `#141B2D`,
paper `#F3F4F1`, yes `#3F7D62`, if-need-be `#C8892B`, no `#A8ADA6`;
vivid blue `#2F5BFF` is reserved exclusively for the winning column.
Type: display face (Bricolage Grotesque-like) for titles only, Public
Sans for body, tabular-figures monospace for every count and time.
Fonts self-hosted woff2 latin subsets, embedded — no Google Fonts.
Signature moment: winning column fills bottom-up + a quorum ring;
audacity nowhere else. Dense data UI: tight spacing, hairline rules,
small radii, no soft shadows. Motion is functional only; respect
`prefers-reduced-motion`. Floor (never mention it, just do it):
responsive to 375 px (mobile grid is a designed alternative, not a
horizontal scroll), visible focus, AA contrast, full keyboard nav on
the grid (arrows + space to cycle states).
Copy: short sentences, active voice; buttons say what they do; empty
states invite action; errors state what happened and how to fix it,
without apologizing.

## Makefile targets

- `make build` — static binary (`CGO_ENABLED=0`)
- `make run` — build and run locally
- `make dev` — live-reload dev loop (templ watch + tailwind watch + rerun)
- `make test` — `go test ./...`
- `make lint` — golangci-lint (pinned version, auto-downloaded)
- `make generate` — templ generate + sqlc generate
- Tool binaries (templ, sqlc, goose, tailwind CLI, golangci-lint) are
  version-pinned and fetched by the Makefile into `.tools/` — never
  assume they are on PATH, never `npm install` anything.

## Language

Everything that lands in the repo (comments, docs, commit messages,
UI source strings) is in English. Conversation with the maintainer may
be in French.
