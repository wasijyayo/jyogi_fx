-- name: CreatePosition :one
-- 成行注文による新規建玉を作成する（#36 TRADE-1。design.md §8）。
-- closed_at / pnl は決済（#37 TRADE-2）まで NULL のまま。
INSERT INTO positions (user_id, currency_id, side, size, entry_price, leverage, opened_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;
