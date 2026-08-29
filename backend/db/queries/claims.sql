-- name: CreateClaim :one
-- /claim を実行する（#39 ECON-1）。PRIMARY KEY (session_id, user_id) の一意制約に
-- ON CONFLICT DO NOTHING をぶつけることで「同一セッション内で2回claimできない」を
-- DBレベルで保証する。競合時は RETURNING が0行になるため、呼び出し側は
-- pgx.ErrNoRows を ErrAlreadyClaimed として扱う（CreateGameSessionの冪等性パターンとは
-- 逆に、ここでは「既存行を返す」のではなく「弾く」ためにあえて DO NOTHING のままにする）。
INSERT INTO claims (session_id, user_id, amount, claimed_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (session_id, user_id) DO NOTHING
RETURNING *;
