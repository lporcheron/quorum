-- name: CreatePoll :one
INSERT INTO polls (
    public_id, admin_token_hash, title, description, location, video_url,
    kind, timezone, hide_participants, require_voter_email, allow_comments,
    deletes_at, created_at, updated_at
) VALUES (
    @public_id, @admin_token_hash, @title, @description, @location, @video_url,
    @kind, @timezone, @hide_participants, @require_voter_email, @allow_comments,
    @deletes_at, @created_at, @updated_at
)
RETURNING *;

-- name: GetPollByPublicID :one
SELECT * FROM polls WHERE public_id = @public_id;

-- name: UpdatePollDetails :exec
UPDATE polls SET
    title = @title,
    description = @description,
    location = @location,
    video_url = @video_url,
    hide_participants = @hide_participants,
    require_voter_email = @require_voter_email,
    allow_comments = @allow_comments,
    updated_at = @updated_at
WHERE id = @id;

-- name: UpdatePollStatus :exec
UPDATE polls SET status = @status, updated_at = @updated_at WHERE id = @id;

-- name: UpdatePollAdminTokenHash :exec
UPDATE polls SET admin_token_hash = @admin_token_hash, updated_at = @updated_at WHERE id = @id;

-- name: ExtendPollRetention :exec
UPDATE polls SET deletes_at = @deletes_at WHERE id = @id;

-- name: DeletePoll :exec
DELETE FROM polls WHERE id = @id;
