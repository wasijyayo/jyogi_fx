-- name: CreateTrade :one
-- 約定記録を残す（#36 TRADE-1。design.md §8）。新規建玉なら position_id には
-- 直前に CreatePosition で作った行のIDを渡す（決済時の trades 行は #37 TRADE-2で
-- 既存ポジションのIDを渡す形で追加する想定）。
-- created_at はDBの now() に任せず引数で受ける。時刻は必ず呼び出し側から注入する
-- （CLAUDE.md §5.1）ことで、テストで trades.created_at を発注時刻と正確に突き合わせられる。
INSERT INTO trades (user_id, currency_id, position_id, side, size, price, fee, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;
