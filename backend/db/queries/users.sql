-- name: GetUserByID :one
SELECT discord_id, display_name, created_at
FROM users
WHERE discord_id = $1;

-- name: UpsertUser :one
-- Discord OAuth コールバックでの初回ログイン時にユーザー行を作成する。
-- 既存ユーザーなら display_name（Discordのユーザー名）だけ更新する。
INSERT INTO users (discord_id, display_name)
VALUES ($1, $2)
ON CONFLICT (discord_id) DO UPDATE SET display_name = EXCLUDED.display_name
RETURNING discord_id, display_name, created_at;
