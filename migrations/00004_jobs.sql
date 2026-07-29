-- Background job queue for outgoing email. Jobs are deleted on
-- success; failed jobs are retried with exponential backoff and kept
-- (with attempts and last_error) once the retry budget is exhausted,
-- so operators can inspect them.

-- +goose Up

CREATE TABLE jobs (
    id         INTEGER PRIMARY KEY,
    type       TEXT    NOT NULL,
    payload    TEXT    NOT NULL,             -- JSON
    run_at     TEXT    NOT NULL,             -- UTC RFC 3339
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TEXT    NOT NULL
);
CREATE INDEX idx_jobs_run_at ON jobs(run_at);

-- +goose Down

DROP TABLE jobs;
