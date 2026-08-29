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
-- 毎分繰り返してしまうことを防げる。/claim（#39）も同じクエリで当日行を引き、
-- claim_median 列（NULL なら中央値算出前）を見て利用可否を判定する。
SELECT * FROM game_sessions
WHERE date = $1;

-- name: UpdateGameSessionClaimMedian :exec
-- 寄り付き処理の手順8（design.md §2.7）で、含み損益再評価・清算判定（手順6〜7）の
-- 「後」に呼ぶこと。全登録者の総資産の中央値をここで確定させる（#39 ECON-1）。
UPDATE game_sessions
SET claim_median = $2
WHERE id = $1;

-- name: UpdateGameSessionTickerMsgID :exec
-- 市場ティッカーメッセージ（design.md §6.4）を初めて投稿した直後に呼ぶ（#43 NOTIFY-1）。
-- 以後の tick はこの ID をそのまま編集し続ける（新規投稿ではなく編集。チャンネルが荒れないため）。
UPDATE game_sessions
SET ticker_msg_id = $2
WHERE id = $1;
