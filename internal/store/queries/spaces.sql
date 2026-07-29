-- name: GetSpaceByID :one
SELECT * FROM spaces WHERE id = @id;

-- name: GetSpaceBySlug :one
SELECT * FROM spaces WHERE slug = @slug;

-- name: ListSpacesForUser :many
SELECT sqlc.embed(spaces), space_members.role
FROM spaces
JOIN space_members ON space_members.space_id = spaces.id
WHERE space_members.user_id = @user_id
ORDER BY spaces.created_at, spaces.id;

-- name: GetMembership :one
SELECT role FROM space_members WHERE space_id = @space_id AND user_id = @user_id;

-- name: ListSpaceMembers :many
SELECT space_members.role, space_members.created_at AS member_since, sqlc.embed(users)
FROM space_members
JOIN users ON users.id = space_members.user_id
WHERE space_members.space_id = @space_id
ORDER BY space_members.created_at, users.id;

-- name: UpdateSpaceSettings :exec
UPDATE spaces SET name = @name, default_timezone = @default_timezone, retention_days = @retention_days
WHERE id = @id;

-- name: UpdateSpaceOwner :exec
UPDATE spaces SET owner_user_id = @owner_user_id WHERE id = @id;

-- name: UpdateMemberRole :exec
UPDATE space_members SET role = @role WHERE space_id = @space_id AND user_id = @user_id;

-- name: DeleteSpaceMember :execrows
DELETE FROM space_members WHERE space_id = @space_id AND user_id = @user_id;

-- name: CreateSpaceInvitation :one
INSERT INTO space_invitations (space_id, email, role, token_hash, invited_by_user_id, expires_at, created_at)
VALUES (@space_id, @email, @role, @token_hash, @invited_by_user_id, @expires_at, @created_at)
RETURNING *;

-- name: GetSpaceInvitationByTokenHash :one
SELECT * FROM space_invitations WHERE token_hash = @token_hash;

-- name: ListSpaceInvitations :many
SELECT * FROM space_invitations WHERE space_id = @space_id ORDER BY created_at, id;

-- name: DeleteSpaceInvitation :exec
DELETE FROM space_invitations WHERE id = @id AND space_id = @space_id;

-- name: DeleteSpaceInvitationByEmail :exec
DELETE FROM space_invitations WHERE space_id = @space_id AND email = @email;

-- name: ListPollsBySpace :many
SELECT sqlc.embed(polls), users.name AS owner_name
FROM polls
LEFT JOIN users ON users.id = polls.created_by_user_id
WHERE polls.space_id = @space_id
ORDER BY polls.created_at DESC;
