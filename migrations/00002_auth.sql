-- Authentication: scs session store, magic-link tokens, and the
-- personal space pointer on users.
--
-- The sessions table shape is dictated by the scs sqlite3store driver
-- (julianday REAL expiry); the PostgreSQL store will bring its own
-- table when that engine lands.

-- +goose Up

CREATE TABLE sessions (
    token  TEXT PRIMARY KEY,
    data   BLOB NOT NULL,
    expiry REAL NOT NULL
);
CREATE INDEX idx_sessions_expiry ON sessions(expiry);

CREATE TABLE login_tokens (
    id          INTEGER PRIMARY KEY,
    email       TEXT NOT NULL,               -- lowercased
    token_hash  TEXT NOT NULL UNIQUE,        -- SHA-256, token never stored
    redirect    TEXT NOT NULL DEFAULT '',    -- local path to land on after login
    expires_at  TEXT NOT NULL,
    consumed_at TEXT,
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_login_tokens_email ON login_tokens(email);

ALTER TABLE users ADD COLUMN personal_space_id INTEGER REFERENCES spaces(id);

-- +goose Down

DROP TABLE login_tokens;
DROP TABLE sessions;
ALTER TABLE users DROP COLUMN personal_space_id;
