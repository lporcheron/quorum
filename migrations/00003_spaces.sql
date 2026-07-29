-- Spaces become first-class: invitations, per-space settings, and a
-- per-poll retention snapshot (taken from the space at creation time,
-- so later setting changes do not rewrite history).

-- +goose Up

CREATE TABLE space_invitations (
    id                 INTEGER PRIMARY KEY,
    space_id           INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    email              TEXT    NOT NULL,           -- lowercased
    role               TEXT    NOT NULL CHECK (role IN ('admin', 'member')),
    token_hash         TEXT    NOT NULL UNIQUE,    -- SHA-256, token never stored
    invited_by_user_id INTEGER NOT NULL REFERENCES users(id),
    expires_at         TEXT    NOT NULL,
    created_at         TEXT    NOT NULL,
    UNIQUE (space_id, email)
);

ALTER TABLE spaces ADD COLUMN default_timezone TEXT;   -- NULL = no preference
ALTER TABLE spaces ADD COLUMN retention_days INTEGER;  -- NULL = instance default

ALTER TABLE polls ADD COLUMN retention_days INTEGER;   -- NULL = instance default

-- +goose Down

ALTER TABLE polls DROP COLUMN retention_days;
ALTER TABLE spaces DROP COLUMN retention_days;
ALTER TABLE spaces DROP COLUMN default_timezone;
DROP TABLE space_invitations;
