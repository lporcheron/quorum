-- name: CreateComment :one
INSERT INTO comments (public_id, poll_id, participant_id, author_name, body, created_at)
VALUES (@public_id, @poll_id, @participant_id, @author_name, @body, @created_at)
RETURNING *;

-- name: ListPollComments :many
SELECT * FROM comments WHERE poll_id = @poll_id ORDER BY created_at, id;

-- name: GetComment :one
SELECT * FROM comments WHERE id = @id AND poll_id = @poll_id;

-- name: DeleteComment :exec
DELETE FROM comments WHERE id = @id AND poll_id = @poll_id;
