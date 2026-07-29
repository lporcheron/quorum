-- name: CreatePoll :one
INSERT INTO polls (
    public_id, space_id, created_by_user_id, admin_token_hash,
    title, description, location, video_url,
    kind, timezone, hide_participants, require_voter_email, allow_comments,
    retention_days, deletes_at, created_at, updated_at
) VALUES (
    @public_id, @space_id, @created_by_user_id, @admin_token_hash,
    @title, @description, @location, @video_url,
    @kind, @timezone, @hide_participants, @require_voter_email, @allow_comments,
    @retention_days, @deletes_at, @created_at, @updated_at
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

-- name: FinalizePoll :exec
UPDATE polls SET status = 'finalized', finalized_option_id = @finalized_option_id, updated_at = @updated_at
WHERE id = @id;

-- name: UpdatePollAdminTokenHash :exec
UPDATE polls SET admin_token_hash = @admin_token_hash, updated_at = @updated_at WHERE id = @id;

-- name: ExtendPollRetention :exec
UPDATE polls SET deletes_at = @deletes_at WHERE id = @id;

-- name: DeletePoll :exec
DELETE FROM polls WHERE id = @id;

-- name: ClaimPoll :execrows
UPDATE polls SET created_by_user_id = @user_id, space_id = @space_id, updated_at = @updated_at
WHERE id = @id AND created_by_user_id IS NULL;

-- name: ListPollsByCreator :many
SELECT * FROM polls WHERE created_by_user_id = @user_id ORDER BY created_at DESC;

-- name: ListPollsVotedByUser :many
SELECT polls.* FROM polls
JOIN participants ON participants.poll_id = polls.id
WHERE participants.user_id = @user_id
ORDER BY polls.created_at DESC;
