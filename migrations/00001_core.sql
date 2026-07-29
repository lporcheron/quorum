-- Core schema. Multi-tenant from day one: users/identities/spaces exist
-- even though no handler touches them before M2; polls.space_id is NULL
-- for guest-created polls until they are claimed by an account.
--
-- Portability rules (SQLite today, PostgreSQL later):
--  * all timestamps are UTC RFC 3339 TEXT written by the application,
--    never by SQL defaults;
--  * all-day dates are timezone-less 'YYYY-MM-DD' TEXT;
--  * no engine-specific functions or defaults.

-- +goose Up

CREATE TABLE users (
    id         INTEGER PRIMARY KEY,
    public_id  TEXT    NOT NULL UNIQUE,
    email      TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL,
    avatar_url TEXT,
    locale     TEXT    NOT NULL DEFAULT 'en',
    timezone   TEXT    NOT NULL DEFAULT 'UTC',
    created_at TEXT    NOT NULL
);

CREATE TABLE identities (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider   TEXT    NOT NULL,
    subject    TEXT    NOT NULL,
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
    public_id           TEXT    NOT NULL UNIQUE,
    space_id            INTEGER REFERENCES spaces(id),
    created_by_user_id  INTEGER REFERENCES users(id),
    admin_token_hash    TEXT    NOT NULL UNIQUE,
    title               TEXT    NOT NULL,
    description         TEXT    NOT NULL DEFAULT '',
    location            TEXT    NOT NULL DEFAULT '',
    video_url           TEXT    NOT NULL DEFAULT '',
    kind                TEXT    NOT NULL CHECK (kind IN ('timed', 'allday')),
    timezone            TEXT,
    status              TEXT    NOT NULL DEFAULT 'live'
                        CHECK (status IN ('live', 'paused', 'finalized', 'cancelled')),
    hide_participants   INTEGER NOT NULL DEFAULT 0,
    require_voter_email INTEGER NOT NULL DEFAULT 0,
    allow_comments      INTEGER NOT NULL DEFAULT 1,
    -- No FK: circular with poll_options. Integrity enforced in the
    -- service layer.
    finalized_option_id INTEGER,
    deletes_at          TEXT,
    created_at          TEXT    NOT NULL,
    updated_at          TEXT    NOT NULL,
    CHECK ((kind = 'timed') = (timezone IS NOT NULL))
);
CREATE INDEX idx_polls_space ON polls(space_id) WHERE space_id IS NOT NULL;
CREATE INDEX idx_polls_deletes ON polls(deletes_at) WHERE deletes_at IS NOT NULL;

CREATE TABLE poll_options (
    id               INTEGER PRIMARY KEY,
    poll_id          INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    starts_at        TEXT,
    duration_minutes INTEGER,
    all_day_date     TEXT,
    position         INTEGER NOT NULL,
    CHECK (
        (starts_at IS NOT NULL AND duration_minutes > 0 AND all_day_date IS NULL)
     OR (starts_at IS NULL AND duration_minutes IS NULL AND all_day_date IS NOT NULL)
    )
);
CREATE INDEX idx_poll_options_poll ON poll_options(poll_id, position);
-- Two partial unique indexes: SQLite treats NULLs as distinct in unique
-- indexes, so one composite index would not catch duplicate all-day dates.
CREATE UNIQUE INDEX uq_poll_options_timed
    ON poll_options(poll_id, starts_at, duration_minutes) WHERE starts_at IS NOT NULL;
CREATE UNIQUE INDEX uq_poll_options_allday
    ON poll_options(poll_id, all_day_date) WHERE all_day_date IS NOT NULL;

CREATE TABLE participants (
    id              INTEGER PRIMARY KEY,
    public_id       TEXT    NOT NULL UNIQUE,
    poll_id         INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    email           TEXT,
    user_id         INTEGER REFERENCES users(id) ON DELETE SET NULL,
    edit_token_hash TEXT    NOT NULL UNIQUE,
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
CREATE INDEX idx_votes_option ON votes(option_id);

CREATE TABLE comments (
    id             INTEGER PRIMARY KEY,
    public_id      TEXT    NOT NULL UNIQUE,
    poll_id        INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
    participant_id INTEGER REFERENCES participants(id) ON DELETE SET NULL,
    user_id        INTEGER REFERENCES users(id) ON DELETE SET NULL,
    author_name    TEXT    NOT NULL,
    body           TEXT    NOT NULL,
    created_at     TEXT    NOT NULL
);
CREATE INDEX idx_comments_poll ON comments(poll_id, created_at);

-- +goose Down

DROP TABLE comments;
DROP TABLE votes;
DROP TABLE participants;
DROP TABLE poll_options;
DROP TABLE polls;
DROP TABLE space_members;
DROP TABLE spaces;
DROP TABLE identities;
DROP TABLE users;
