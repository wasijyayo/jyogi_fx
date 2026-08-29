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

-- name: GetUserForUpdate :one
-- 取引処理などで残高を読み書きする前に行ロックを取る（同時注文による残高更新の
-- lost update を防ぐ。#36 TRADE-1）。呼び出し側は必ずトランザクション内で使うこと。
SELECT * FROM users
WHERE discord_id = $1
FOR UPDATE;

-- name: GetUser :one
-- /balance・/profile（#41 CMD-1）用の読み取り専用フルロー取得。GetUserByIDと違い
-- balanceも返す。ロックを取らない（読むだけで書き換えないため、GetUserForUpdateを
-- 使い回さない）。
SELECT * FROM users
WHERE discord_id = $1;

-- name: UpdateUserBalance :exec
-- 取引・claim等で残高を更新する（#36 TRADE-1）。
UPDATE users
SET balance = $2
WHERE discord_id = $1;

-- name: ListAllUsers :many
-- 「全登録者」の総資産を集計する起点（#39 ECON-1。design.md §7.2の中央値算出、
-- ランキング集計 #41 でも使う）。ここでは残高（とランキング表示用のdisplay_name）
-- だけを取り、未決済ポジションの含み損益は internal/game の addOpenPositionPnL 側で
-- 通貨ごとループして合算する（CLAUDE.md §5.3: user×currencyでなく常に全通貨
-- ループで積み上げる）。
SELECT discord_id, display_name, balance FROM users;
