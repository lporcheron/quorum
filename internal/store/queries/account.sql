-- Account deletion. Every statement stays in the SQLite/PostgreSQL
-- common subset, like the rest of the query set.

-- name: ListSolelyOwnedSpaceIDs :many
SELECT spaces.id FROM spaces
WHERE spaces.owner_user_id = @user_id
  AND NOT EXISTS (
    SELECT 1 FROM space_members
    WHERE space_members.space_id = spaces.id AND space_members.user_id <> @user_id
  );

-- name: ListSharedOwnedSpaces :many
SELECT spaces.* FROM spaces
WHERE spaces.owner_user_id = @user_id
  AND EXISTS (
    SELECT 1 FROM space_members
    WHERE space_members.space_id = spaces.id AND space_members.user_id <> @user_id
  );

-- name: DeleteCommentsByUserParticipants :exec
DELETE FROM comments WHERE participant_id IN (
  SELECT participants.id FROM participants WHERE participants.user_id = @author_id
);

-- name: DeleteParticipantsByUser :exec
DELETE FROM participants WHERE user_id = @user_id;

-- name: DeletePollsBySpace :exec
DELETE FROM polls WHERE space_id = @space_id;

-- name: DetachPollsFromUser :exec
UPDATE polls SET created_by_user_id = NULL WHERE created_by_user_id = @user_id;

-- name: DeleteSpace :exec
DELETE FROM spaces WHERE id = @id;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = @id;

-- name: DeleteLoginTokensByEmail :exec
DELETE FROM login_tokens WHERE email = @email;
