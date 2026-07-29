-- name: CreatePollOption :one
INSERT INTO poll_options (poll_id, starts_at, duration_minutes, all_day_date, position)
VALUES (@poll_id, @starts_at, @duration_minutes, @all_day_date, @position)
RETURNING *;

-- name: ListPollOptions :many
SELECT * FROM poll_options WHERE poll_id = @poll_id ORDER BY position, id;

-- name: GetPollOption :one
SELECT * FROM poll_options WHERE id = @id AND poll_id = @poll_id;

-- name: DeletePollOption :exec
DELETE FROM poll_options WHERE id = @id AND poll_id = @poll_id;

-- name: MaxPollOptionPosition :one
SELECT CAST(COALESCE(MAX(position), -1) AS INTEGER) AS max_position
FROM poll_options WHERE poll_id = @poll_id;
