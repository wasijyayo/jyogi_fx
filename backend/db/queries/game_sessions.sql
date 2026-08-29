-- name: CreateGameSession :one
-- その日のセッション行を作成する（#34 PRICE-3）。
-- date に UNIQUE 制約があるため、tickの重複実行（Cloud Schedulerの再試行等）で
-- 複数回呼ばれても同じ行を返す（冪等性の確保。CLAUDE.md §5.5）。
-- DO NOTHING だと競合時に RETURNING が0行になってしまうため、date を自分自身に
-- 上書きする no-op update にして既存行を必ず RETURNING で取得できるようにしている。
INSERT INTO game_sessions (date, seed, opened_at, closed_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (date) DO UPDATE SET date = EXCLUDED.date
RETURNING *;
