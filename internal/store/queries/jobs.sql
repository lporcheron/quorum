-- name: CreateJob :one
INSERT INTO jobs (type, payload, run_at, created_at)
VALUES (@type, @payload, @run_at, @created_at)
RETURNING *;

-- name: DueJobs :many
SELECT * FROM jobs
WHERE run_at <= @now AND attempts < @max_attempts
ORDER BY run_at, id
LIMIT @max_batch;

-- name: RescheduleJob :exec
UPDATE jobs SET attempts = @attempts, last_error = @last_error, run_at = @run_at
WHERE id = @id;

-- name: DeleteJob :exec
DELETE FROM jobs WHERE id = @id;

-- name: CountDeadJobs :one
SELECT COUNT(*) FROM jobs WHERE attempts >= @max_attempts;
