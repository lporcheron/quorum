-- Per-poll organizer notification toggle.

-- +goose Up

ALTER TABLE polls ADD COLUMN notify_organizer INTEGER NOT NULL DEFAULT 1;

-- +goose Down

ALTER TABLE polls DROP COLUMN notify_organizer;
