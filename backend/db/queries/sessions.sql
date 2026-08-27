-- name: CreateSession :exec
-- CLAUDE.md §5.4: セッションはDBに置く（インスタンスは常に落ちる前提）。
INSERT INTO sessions_auth (id, user_id, expires_at)
VALUES ($1, $2, $3);

-- name: GetSession :one
SELECT id, user_id, expires_at
FROM sessions_auth
WHERE id = $1;

-- name: DeleteSession :exec
DELETE FROM sessions_auth
WHERE id = $1;
