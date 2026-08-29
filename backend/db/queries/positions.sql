-- name: CreatePosition :one
-- 成行注文による新規建玉を作成する（#36 TRADE-1。design.md §8）。
-- closed_at / pnl は決済（#37 TRADE-2）まで NULL のまま。
INSERT INTO positions (user_id, currency_id, side, size, entry_price, leverage, opened_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetPositionForUpdate :one
-- 決済処理でポジションを読み書きする前に行ロックを取る（同時決済によるlost updateを防ぐ。
-- #37 TRADE-2）。user_id も条件に含めることで、「存在しない」場合と「他人のポジション」の
-- 場合を区別せず ErrPositionNotFound 1つにまとめられるようにする（他人のポジションIDの
-- 存在有無を呼び出し元に漏らさないため）。呼び出し側は必ずトランザクション内で使うこと。
SELECT * FROM positions
WHERE id = $1 AND user_id = $2
FOR UPDATE;

-- name: ClosePosition :one
-- 決済によりポジションを確定させる（#37 TRADE-2。design.md §8）。closed_at と pnl を
-- セットする。size / entry_price / leverage は建玉時点の記録として書き換えない。
UPDATE positions
SET closed_at = $2, pnl = $3
WHERE id = $1
RETURNING *;

-- name: ListOpenPositionsByCurrency :many
-- ロスカット判定対象の未決済ポジション一覧を返す（#38 TRADE-3）。全通貨ループの
-- 内側で呼ぶ（CLAUDE.md §5.3）。ここではロックを取らない。含み損益の再計算は
-- 読み取り専用で行い、実際に清算するポジションだけ ClosePosition 側で個別に
-- 行ロックを取る（#37 の行ロックをそのまま再利用する）。
SELECT * FROM positions
WHERE currency_id = $1 AND closed_at IS NULL;
