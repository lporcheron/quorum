-- name: CreateParticipant :one
INSERT INTO participants (public_id, poll_id, name, email, user_id, edit_token_hash, created_at, updated_at)
VALUES (@public_id, @poll_id, @name, @email, @user_id, @edit_token_hash, @created_at, @updated_at)
RETURNING *;

-- name: GetParticipantByEditTokenHash :one
SELECT * FROM participants WHERE edit_token_hash = @edit_token_hash;

-- name: GetParticipantByUser :one
-- The row a signed-in account voted with, oldest first should an
-- account somehow hold two.
SELECT * FROM participants WHERE poll_id = @poll_id AND user_id = @user_id ORDER BY id LIMIT 1;

-- name: GetParticipant :one
SELECT * FROM participants WHERE id = @id AND poll_id = @poll_id;

-- name: ListPollParticipants :many
SELECT * FROM participants WHERE poll_id = @poll_id ORDER BY created_at, id;

-- name: CountPollParticipants :one
SELECT COUNT(*) FROM participants WHERE poll_id = @poll_id;

-- name: UpdateParticipant :exec
UPDATE participants SET name = @name, email = @email, updated_at = @updated_at WHERE id = @id;

-- name: DeleteParticipant :exec
DELETE FROM participants WHERE id = @id AND poll_id = @poll_id;
