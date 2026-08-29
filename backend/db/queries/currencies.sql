-- name: ListCurrencies :many
-- 通貨をコードに埋め込まず、常に全件ループする設計にするための入口（CLAUDE.md §5.3）。
-- tick処理・価格計算・ランキング集計はすべてこれを起点にする。通貨が増えても
-- ループ回数が変わるだけで、tick の実行回数自体は増えない設計にすること。
SELECT * FROM currencies
ORDER BY id;

-- name: GetCurrencyBySymbolForUpdate :one
-- 取引処理でpressureを読み書きする前に行ロックを取る（同時注文によるpressure更新の
-- lost update を防ぐ。#36 TRADE-1）。呼び出し側は必ずトランザクション内で使うこと。
SELECT * FROM currencies
WHERE symbol = $1
FOR UPDATE;

-- name: GetCurrencyByIDForUpdate :one
-- 決済処理で通貨をIDでロックする（#37 TRADE-2）。positions.currency_id はsymbolでなく
-- IDで持っているため、GetCurrencyBySymbolForUpdateとは別に用意する。
-- 呼び出し側は必ずトランザクション内で使うこと。
SELECT * FROM currencies
WHERE id = $1
FOR UPDATE;

-- name: UpdateCurrencyPressure :exec
-- 取引・tick処理で計算した新しい需給圧力を保存する（#33 PRICE-2）。
-- pressure と pressure_at は必ずセットで更新する（Pressure()の減衰計算が
-- 「pressure_at時点でpressureだった」ことを前提にしているため、片方だけ
-- 更新すると以後の減衰計算がずれる）。
UPDATE currencies
SET pressure = $2, pressure_at = $3
WHERE id = $1;
