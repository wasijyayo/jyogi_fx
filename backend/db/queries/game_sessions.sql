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

-- name: GetGameSessionByDate :one
-- 毎分tick（#35 TICK-1）が「本日のセッションは寄り付き済みか」を判定するために使う。
-- 行が無ければ呼び出し側は pgx.ErrNoRows を見て OpenSession（寄り付き処理）を実行する。
-- 既に開いている日はここで見つかるため、寄り付き処理（pressureリセット等）を
-- 毎分繰り返してしまうことを防げる。
SELECT * FROM game_sessions
WHERE date = $1;
