-- Hot instance settings and the purge-reminder marker.

-- +goose Up

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

ALTER TABLE polls ADD COLUMN reminder_sent_at TEXT;

-- +goose Down

ALTER TABLE polls DROP COLUMN reminder_sent_at;
DROP TABLE settings;
