# Quorum — original implementation plan (historical)

> This is the design document the project was bootstrapped from, kept
> for the record. Where reality diverged, outcome notes are inline
> (e.g. decision D1); the living references are README.md, CLAUDE.md
> and the code itself.

Scope of this document: repository layout, complete M1 SQL schema with
indexes, HTTP routes for M1, and the three technical decisions I am
least sure about. Open questions live at the end.

## 1. Repository layout

```
quorum/
├── cmd/
│   └── quorum/
│       └── main.go              # flag-less entrypoint: config, wiring, serve
├── internal/
│   ├── config/                  # env parsing, defaults, validation
│   ├── server/                  # http.Server setup, routing, middleware,
│   │                            #   graceful shutdown, /healthz
│   ├── handler/                 # thin HTTP handlers (parse → service → render)
│   ├── poll/                    # domain: poll lifecycle, options, votes,
│   │                            #   scoring, timezone rendering rules
│   ├── space/                   # domain: spaces, membership checks (M3 logic,
│   │                            #   but the access-check helper lives here from M1)
│   ├── auth/                    # M2: OAuth/OIDC, magic links, identity merge
│   ├── mail/                    # M4: SMTP client, templates, job worker
│   ├── store/                   # sqlc-generated code + Store interface
│   │   └── sqlite/              #   sqlite implementation (see decision D1)
│   ├── ids/                     # base58 public IDs + token generation/hashing
│   └── i18n/                    # go-i18n wiring; message files embedded
├── web/
│   ├── templates/               # .templ files (layout, poll, grid, partials)
│   ├── static/
│   │   ├── css/                 # tailwind input + generated output
│   │   ├── js/                  # htmx.min.js, alpine.min.js, quorum.js (<15 KB)
│   │   └── fonts/               # woff2, latin subsets
│   └── embed.go                 # embed.FS for all of the above
├── migrations/                  # goose .sql migrations, embedded
├── translations/                # go-i18n TOML files (en, fr), embedded
├── Makefile
├── Dockerfile                   # multi-stage, final image FROM scratch
├── .github/workflows/ci.yml    # build + test + lint (golangci-lint)
├── CLAUDE.md
├── PLAN.md
└── README.md
```

Notes:

- `internal/handler` renders templates; `internal/poll` and
  `internal/space` never import `net/http` or templ — they are plain
  functions over the store, testable without HTTP.
- Table names are plural (`users`, not `user`) because `user` is a
  reserved word in PostgreSQL and we want one schema vocabulary for
  both engines.
- Public IDs are base58, 12 chars, generated from `crypto/rand`.
  Secret tokens (admin link, participant edit link) are 26+ chars,
  also base58, stored **hashed** (see D3).

## 2. M1 SQL schema (SQLite dialect, canonical)

`users`, `identities`, `spaces`, `space_members` are created in M1 even
though no handler touches them before M2: the brief requires
multi-tenancy at the data-model level from day one, and
`polls.space_id` needs a real target. `polls.space_id` is NULL for
guest-created polls until the M2 "claim via admin link" flow attaches
them to an account/space.

All timestamps are UTC RFC 3339 strings written by the application
(no `DEFAULT (datetime('now'))`) so migrations stay portable across
engines. See decision D2.

```sql
-- +goose Up

CREATE TABLE users (
    id         INTEGER PRIMARY KEY,
    public_id  TEXT    NOT NULL UNIQUE,          -- base58, 12 chars
    email      TEXT    NOT NULL UNIQUE,          -- stored lowercased
    name       TEXT    NOT NULL,
    avatar_url TEXT,
    locale     TEXT    NOT NULL DEFAULT 'en',
    timezone   TEXT    NOT NULL DEFAULT 'UTC',   -- IANA name
    created_at TEXT    NOT NULL                  -- UTC RFC 3339, app-written
);

CREATE TABLE identities (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider   TEXT    NOT NULL,   -- 'google' | 'github' | 'microsoft' | 'oidc' | 'email'
    subject    TEXT    NOT NULL,   -- provider-issued stable identifier
    created_at TEXT    NOT NULL,
    UNIQUE (provider, subject)
);
CREATE INDEX idx_identities_user ON identities(user_id);

CREATE TABLE spaces (
    id            INTEGER PRIMARY KEY,
    public_id     TEXT    NOT NULL UNIQUE,
    slug          TEXT    NOT NULL UNIQUE,
    name          TEXT    NOT NULL,
    owner_user_id INTEGER NOT NULL REFERENCES users(id),
    created_at    TEXT    NOT NULL
);

CREATE TABLE space_members (
    space_id   INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT    NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
    created_at TEXT    NOT NULL,
    PRIMARY KEY (space_id, user_id)
);
CREATE INDEX idx_space_members_user ON space_members(user_id);

CREATE TABLE polls (
    id                  INTEGER PRIMARY KEY,
    public_id           TEXT    NOT NULL UNIQUE,      -- base58, in every URL
    space_id            INTEGER REFERENCES spaces(id),         -- NULL = guest poll
    created_by_user_id  INTEGER REFERENCES users(id),          -- NULL = guest poll
    admin_token_hash    TEXT    NOT NULL UNIQUE,      -- SHA-256(admin URL token)
    title               TEXT    NOT NULL,
    description         TEXT    NOT NULL DEFAULT '',
    location            TEXT    NOT NULL DEFAULT '',
    video_url           TEXT    NOT NULL DEFAULT '',
    kind                TEXT    NOT NULL CHECK (kind IN ('timed', 'allday')),
    timezone            TEXT,                          -- IANA; NULL iff kind = 'allday'
    status              TEXT    NOT NULL DEFAULT 'live'
                        CHECK (status IN ('live', 'paused', 'finalized', 'cancelled')),
    hide_participants   INTEGER NOT NULL DEFAULT 0,
    require_voter_email INTEGER NOT NULL DEFAULT 0,
    allow_comments      INTEGER NOT NULL DEFAULT 1,
    finalized_option_id INTEGER,                       -- no FK: circular with poll_options;
                                                       -- integrity enforced in the service layer
    deletes_at          TEXT,                          -- UTC RFC 3339; purge horizon
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL,
    CHECK ((kind = 'timed') = (timezone IS NOT NULL))
);
CREATE INDEX idx_polls_space   ON polls(space_id) WHERE space_id IS NOT NULL;
CREATE INDEX idx_polls_deletes ON polls(deletes_at) WHERE deletes_at IS NOT NULL;

CREATE TABLE poll_options (
    id               INTEGER PRIMARY KEY,
    poll_id          INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    starts_at        TEXT,     -- UTC RFC 3339; NULL iff all-day option
    duration_minutes INTEGER,  -- > 0; NULL iff all-day option
    all_day_date     TEXT,     -- 'YYYY-MM-DD', timezone-less; NULL iff timed option
    position         INTEGER NOT NULL,
    CHECK (
        (starts_at IS NOT NULL AND duration_minutes > 0  AND all_day_date IS NULL)
     OR (starts_at IS NULL     AND duration_minutes IS NULL AND all_day_date IS NOT NULL)
    )
);
CREATE INDEX idx_poll_options_poll ON poll_options(poll_id, position);
-- Two partial unique indexes instead of one: SQLite treats NULLs as
-- distinct in unique indexes, so a single composite index would not
-- prevent duplicate all-day dates.
CREATE UNIQUE INDEX uq_poll_options_timed
    ON poll_options(poll_id, starts_at, duration_minutes) WHERE starts_at IS NOT NULL;
CREATE UNIQUE INDEX uq_poll_options_allday
    ON poll_options(poll_id, all_day_date) WHERE all_day_date IS NOT NULL;

CREATE TABLE participants (
    id              INTEGER PRIMARY KEY,
    public_id       TEXT    NOT NULL UNIQUE,
    poll_id         INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    email           TEXT,                                  -- lowercased when present
    user_id         INTEGER REFERENCES users(id) ON DELETE SET NULL,
    edit_token_hash TEXT    NOT NULL UNIQUE,               -- SHA-256(personal edit token)
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL
);
CREATE INDEX idx_participants_poll ON participants(poll_id);

CREATE TABLE votes (
    participant_id INTEGER NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
    option_id      INTEGER NOT NULL REFERENCES poll_options(id) ON DELETE CASCADE,
    value          TEXT    NOT NULL CHECK (value IN ('yes', 'ifneedbe', 'no')),
    updated_at     TEXT    NOT NULL,
    PRIMARY KEY (participant_id, option_id)
);
CREATE INDEX idx_votes_option ON votes(option_id);   -- per-option tallies

CREATE TABLE comments (
    id             INTEGER PRIMARY KEY,
    public_id      TEXT    NOT NULL UNIQUE,
    poll_id        INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    participant_id INTEGER REFERENCES participants(id) ON DELETE SET NULL,
    user_id        INTEGER REFERENCES users(id) ON DELETE SET NULL,
    author_name    TEXT    NOT NULL,   -- denormalized so deletion keeps attribution
    body           TEXT    NOT NULL,
    created_at     TEXT    NOT NULL
);
CREATE INDEX idx_comments_poll ON comments(poll_id, created_at);
```

Schema decisions worth calling out:

- **All-day vs timed is explicit, not simulated.** The brief's entity
  sketch said `duration_minutes = 0` means all-day, but its transverse
  rule says to model the distinction explicitly. I follow the rule:
  a timed option is `(starts_at UTC, duration_minutes)`, an all-day
  option is a bare `all_day_date` with no timezone, and a CHECK makes
  the states mutually exclusive. `polls.kind` fixes the poll to one
  family (see open question Q1) and forces `timezone` presence
  accordingly.
- **A missing `votes` row is "no answer"**, distinct from an explicit
  `no`. This happens naturally when the organizer adds options after
  people voted (see Q3 for how it is displayed).
- `jobs`, `settings`, `space_invitations`, and the scs `sessions`
  table arrive with their milestones (M4, M2/M5, M3, M2) as new goose
  migrations — no speculative tables now beyond the multi-tenant core.

## 3. M1 HTTP routes

Go 1.22+ `ServeMux` patterns. `{pollID}` is the public base58 ID.
`{adminToken}` and `{editToken}` are capability tokens: possession is
authorization (compared against the stored hash, constant-time).

All mutations are `POST`: plain HTML forms cannot send PUT/DELETE, and
the no-JS degraded path must work for the critical flows. HTMX uses
the same endpoints. Handlers answer with a partial when `HX-Request`
is set, a full page (or redirect) otherwise.

| Method | Path | Handler | Purpose |
|---|---|---|---|
| GET  | `/` | `handler.Home` | Landing page + poll creation form |
| POST | `/polls` | `handler.CreatePoll` | Create poll; redirect to admin URL (token shown once) |
| GET  | `/polls/{pollID}` | `handler.ShowPoll` | Public poll page: grid, tallies, comments |
| GET  | `/polls/{pollID}/grid` | `handler.PollGrid` | HTMX partial: grid re-rendered for `?tz=` |
| POST | `/polls/{pollID}/participants` | `handler.CreateParticipant` | Guest vote: name, optional email, votes; sets edit link |
| GET  | `/polls/{pollID}/p/{editToken}` | `handler.ShowPollAsParticipant` | Poll page in edit mode for this participant |
| POST | `/polls/{pollID}/p/{editToken}/votes` | `handler.UpdateVotes` | Update this participant's votes/name/email |
| POST | `/polls/{pollID}/p/{editToken}/delete` | `handler.DeleteParticipant` | Remove participant and their votes |
| POST | `/polls/{pollID}/comments` | `handler.CreateComment` | Add comment (participant token or free name) |
| GET  | `/polls/{pollID}/admin/{adminToken}` | `handler.ShowPollAdmin` | Admin view: edit details, options, participants |
| POST | `/polls/{pollID}/admin/{adminToken}` | `handler.UpdatePoll` | Update title/description/location/video/privacy |
| POST | `/polls/{pollID}/admin/{adminToken}/options` | `handler.AddOptions` | Add option(s), incl. duplicate-across-days |
| POST | `/polls/{pollID}/admin/{adminToken}/options/{optionID}/delete` | `handler.DeleteOption` | Remove an option (cascades votes) |
| POST | `/polls/{pollID}/admin/{adminToken}/status` | `handler.SetPollStatus` | Pause / resume voting |
| POST | `/polls/{pollID}/admin/{adminToken}/participants/{participantID}/delete` | `handler.AdminDeleteParticipant` | Organizer removes a participant |
| POST | `/polls/{pollID}/admin/{adminToken}/comments/{commentID}/delete` | `handler.AdminDeleteComment` | Organizer removes a comment |
| POST | `/polls/{pollID}/admin/{adminToken}/delete` | `handler.DeletePoll` | Delete the poll |
| GET  | `/static/` | `http.FileServerFS` | Embedded assets, hashed filenames, long cache |
| GET  | `/healthz` | `handler.Healthz` | Liveness: 200 + DB ping |

Middleware chain (M0/M1): request ID → slog access log → panic
recovery → security headers (CSP without `unsafe-inline`, X-Frame-Options)
→ gzip. CSRF for these token-authenticated forms rides on
`SameSite=Lax` cookies plus the capability tokens themselves; scs and
a proper CSRF token arrive with sessions in M2.

## 4. The three decisions I am least sure about

### D1 — Dual-engine story: SQLite-only in M1, shared portable query files when Postgres lands *(validated, amended)*

**Chosen (after review):** M1 ships with `modernc.org/sqlite` only,
behind a hand-written `Store` interface. Queries are written from day
one in a portable subset designed to compile under both sqlc engines:
named parameters (`@name` / `sqlc.arg()`) instead of `?`/`$1`,
`RETURNING` and `ON CONFLICT` (both supported by SQLite ≥ 3.35 and
Postgres), no engine-specific functions in query text. When Postgres
lands (target: M3), a second sqlc config points at the **same**
`queries/*.sql` files; only queries the sqlite parser or dialect
genuinely cannot share get a per-engine override file. CI runs the
store test suite against both engines.

Migrations are a single goose directory in portable SQL (no
engine-specific defaults; the app writes all timestamps), shared by
both engines from the start.

**Residual risk:** sqlc's per-engine parsers may reject constructs one
engine accepts; the fallback (duplicating that one query) is localized
rather than systemic. The dual-engine test suite is the guard.

**Outcome (post-v1):** implemented even leaner than planned — no
second sqlc config at all. The SQLite generation is the single source;
a store-level DBTX adapter rewrites `?N` placeholders to `$N` for pgx,
and migrations are rendered per dialect with exactly three
substitutions (`INTEGER PRIMARY KEY`, ` BLOB `, ` REAL `). The full
test suite runs against both engines (`make test-postgres`, CI job).

**Rejected:**
- *Systematic per-engine duplication of every query* — the original
  proposal; retracted after review since named parameters remove the
  placeholder blocker, keeping duplication as the exception.
- *An ORM or query builder to abstract the dialect* — excluded by the
  brief, and the abstraction cost is exactly what we're avoiding.
- *Postgres from M1* — doubles the maintenance surface before there is
  a single user; SQLite is the default deployment anyway.

### D2 — Timestamps as UTC RFC 3339 TEXT in SQLite (→ `timestamptz` in Postgres)

**Chosen:** every instant is a `TEXT` column holding
`2026-07-29T18:00:00Z` (UTC, second precision, fixed width). It sorts
lexicographically = chronologically, is human-readable in `sqlite3`
during debugging, and maps to `timestamptz` naturally when the
Postgres store lands. Conversion to/from `time.Time` happens in one
place, the store layer; domain code only ever sees `time.Time` in UTC.
All-day dates are a separate `YYYY-MM-DD` TEXT type, never a timestamp.

**Why it's shaky:** INTEGER epoch seconds would be smaller, impossible
to store in a wrong format, and directly comparable without parsing;
TEXT relies on discipline (one writer function) to stay canonical.

**Rejected:**
- *Epoch INTEGER* — loses readability and maps awkwardly to
  `timestamptz` later (bigint columns in Postgres feel wrong and lose
  type safety there).
- *SQLite `datetime()` defaults and functions* — ties data shape to the
  engine, breaks the "app writes all timestamps" portability rule.

### D3 — Capability tokens stored hashed, shown once *(validated)*

**Chosen:** admin tokens and participant edit tokens are 26-char
base58 secrets; the DB stores only `SHA-256(token)` (no salt needed —
the input is high-entropy random, not a password). Lookup is by hash,
comparison constant-time. The admin link is displayed once at
creation and cannot be re-displayed by the server. The admin page
offers "regenerate link". M2's account-claim and M4's email flows
soften the loss scenario.

**Validated with this nuance:** server-side plaintext would only ever
help the anonymous creator — precisely the person the server cannot
identify. The real-world case ("I closed the tab") is handled
client-side: the creator's browser keeps the admin URL in
`localStorage`, so returning to the site on the same browser surfaces
it again, with no plaintext secret at rest. Logged-in creators (M2+)
never need the token at all.

**Rejected:**
- *Plaintext tokens* — recoverable links, but any DB leak/backup
  exposure hands over every poll and every participant identity swap.
- *Signed tokens (HMAC, no storage)* — no revocation, and a leaked
  server key invalidates everything at once.

## 5. Milestone-M1 delivery order (inside the milestone)

1. M0 as specified (skeleton, config, migrations, healthz, CI, Docker).
2. Store + domain: schema, sqlc queries, `poll` package with scoring
   and timezone logic + tests (including a DST-crossing case: a 18:00
   Europe/Paris slot proposed across the late-March switch).
3. Create-poll flow (form → redirect to admin URL), admin page.
4. Public poll page + guest voting, no-JS first, then HTMX layering.
5. Results/tallies, best-option highlight, comments.
6. Mobile grid (per-option stacked cards with the participant's own
   vote row pinned — designed, not scrolled), timezone selector.

## 6. Open questions — resolved 2026-07-29

All proposals below were validated by the maintainer; to be refined
later if needed.

- **Q1 — Mixed polls: no.** A poll is either all-day or timed
  (`polls.kind`); simplifies the grid, the timezone selector, and the
  later `.ics` export.
- **Q2 — "Live" results:** re-render tallies after each local action;
  no passive polling for idle viewers (can be revisited).
- **Q3 — "No answer" display:** a fourth, visually inert cell state
  for options added after a participant voted; excluded from both yes
  and if-need-be counts.
- **Q4 — Lost admin link:** acceptable in M1; softened by the
  localStorage convenience (D3), then M2 account-claim and M4 email.
- **Q5 — Default retention:** guest polls get `deletes_at` = 180 days
  after creation, extended on activity; account-owned polls exempt by
  default (space-level retention setting arrives in M3).
- **Q6 — `require_voter_email` in M1:** mandatory field, stored,
  marked unverified; verification never blocks voting, even post-M4.
- **Q7 — Comment rights in M1:** anyone can comment with a free name
  (participants' comments auto-attributed); a participant deletes
  their own via edit token; the admin deletes any;
  `allow_comments = 0` hides the section entirely.

One deviation to flag: the i18n milestone is M5, but retrofitting
message keys into every template is the expensive part. I plan to wire
go-i18n in M0 and write all UI copy as message keys with English
source strings from the first template; M5 then reduces to writing
the FR catalog and the language switcher. Cost now is near zero.
