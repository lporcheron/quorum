-- The language a participant voted in, so the emails they get (the
-- finalized date, a cancellation) speak it. NULL for rows from before
-- this column: those fall back to the organizer's language.

-- +goose Up

ALTER TABLE participants ADD COLUMN locale TEXT;

-- +goose Down

ALTER TABLE participants DROP COLUMN locale;
