-- name: GetSetting :one
SELECT value FROM settings WHERE key = @key;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value) VALUES (@key, @value)
ON CONFLICT (key) DO UPDATE SET value = excluded.value;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at DESC, id DESC LIMIT @max_rows;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: CountPolls :one
SELECT COUNT(*) FROM polls;

-- name: CountParticipants :one
SELECT COUNT(*) FROM participants;

-- name: CountPendingJobs :one
SELECT COUNT(*) FROM jobs WHERE attempts < @max_attempts;

-- name: ListDeadJobs :many
SELECT * FROM jobs WHERE attempts >= @max_attempts ORDER BY run_at DESC LIMIT @max_rows;

-- name: RetryJob :exec
UPDATE jobs SET attempts = 0, last_error = NULL, run_at = @run_at WHERE id = @id;

-- name: ListExpiredPolls :many
SELECT * FROM polls
WHERE deletes_at IS NOT NULL AND deletes_at <= @now
ORDER BY deletes_at LIMIT @max_rows;

-- name: ListPollsNeedingReminder :many
SELECT * FROM polls
WHERE deletes_at IS NOT NULL AND deletes_at <= @soon
  AND reminder_sent_at IS NULL AND created_by_user_id IS NOT NULL
ORDER BY deletes_at LIMIT @max_rows;

-- name: MarkPollReminded :exec
UPDATE polls SET reminder_sent_at = @reminded_at WHERE id = @id;
