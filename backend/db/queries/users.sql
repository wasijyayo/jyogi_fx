-- name: GetUserByID :one
SELECT discord_id, display_name, created_at
FROM users
WHERE discord_id = $1;
