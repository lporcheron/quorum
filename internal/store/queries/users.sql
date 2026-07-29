-- name: CreateUser :one
INSERT INTO users (public_id, email, name, avatar_url, locale, timezone, created_at)
VALUES (@public_id, @email, @name, @avatar_url, @locale, @timezone, @created_at)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = @email;

-- name: SetUserPersonalSpace :exec
UPDATE users SET personal_space_id = @personal_space_id WHERE id = @id;

-- name: UpdateUserProfile :exec
UPDATE users SET name = @name, avatar_url = @avatar_url WHERE id = @id;

-- name: CreateIdentity :one
INSERT INTO identities (user_id, provider, subject, created_at)
VALUES (@user_id, @provider, @subject, @created_at)
RETURNING *;

-- name: GetIdentity :one
SELECT * FROM identities WHERE provider = @provider AND subject = @subject;

-- name: CreateSpace :one
INSERT INTO spaces (public_id, slug, name, owner_user_id, created_at)
VALUES (@public_id, @slug, @name, @owner_user_id, @created_at)
RETURNING *;

-- name: CreateSpaceMember :exec
INSERT INTO space_members (space_id, user_id, role, created_at)
VALUES (@space_id, @user_id, @role, @created_at);
