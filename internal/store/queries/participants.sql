-- name: CreateParticipant :one
INSERT INTO participants (public_id, poll_id, name, email, edit_token_hash, created_at, updated_at)
VALUES (@public_id, @poll_id, @name, @email, @edit_token_hash, @created_at, @updated_at)
RETURNING *;

-- name: GetParticipantByEditTokenHash :one
SELECT * FROM participants WHERE edit_token_hash = @edit_token_hash;

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
