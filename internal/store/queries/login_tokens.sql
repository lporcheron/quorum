-- name: CreateLoginToken :one
INSERT INTO login_tokens (email, token_hash, redirect, expires_at, created_at)
VALUES (@email, @token_hash, @redirect, @expires_at, @created_at)
RETURNING *;

-- name: GetLoginTokenByHash :one
SELECT * FROM login_tokens WHERE token_hash = @token_hash;

-- name: ConsumeLoginToken :execrows
UPDATE login_tokens SET consumed_at = @consumed_at
WHERE id = @id AND consumed_at IS NULL;

-- name: DeleteExpiredLoginTokens :exec
DELETE FROM login_tokens WHERE expires_at < @now;
