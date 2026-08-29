-- name: UpsertPriceTick :exec
-- docs/design.md §8「UPSERT（冪等性の確保）」。tickが二重に走っても壊れず、
-- high/lowは正しく広がる方に更新される（CLAUDE.md §5.5）。
INSERT INTO price_ticks (
    currency_id, session_id, tick_index, ticked_at,
    base_price, pressure, net_volume,
    open, high, low, close, is_opening
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (currency_id, tick_index) DO UPDATE SET
    high  = GREATEST(price_ticks.high, EXCLUDED.high),
    low   = LEAST(price_ticks.low, EXCLUDED.low),
    close = EXCLUDED.close,
    net_volume = EXCLUDED.net_volume,
    base_price = EXCLUDED.base_price,
    pressure   = EXCLUDED.pressure;

-- name: GetLastPriceTick :one
-- 寄り付きキャンドルの open に使う「前セッション最終tickのclose」を取得する
-- （確定 #19 → design.md §2.8）。行が無ければ呼び出し側で initial_price
-- （currencies.base_price）にフォールバックする。
SELECT * FROM price_ticks
WHERE currency_id = $1
ORDER BY tick_index DESC
LIMIT 1;

-- name: ListRecentPriceTicks :many
-- /price（#42 CMD-2）のスパークライン用に直近Ntick分のcloseを取得する
-- （design.md §6.4「直近8本のキャンドルのcloseを8段階に写像する」）。
-- tick_index降順で返すため、呼び出し側で時系列順に並べ替えること。
SELECT * FROM price_ticks
WHERE currency_id = $1
ORDER BY tick_index DESC
LIMIT $2;
