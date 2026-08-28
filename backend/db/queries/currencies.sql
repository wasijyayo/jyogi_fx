-- name: ListCurrencies :many
-- 通貨をコードに埋め込まず、常に全件ループする設計にするための入口（CLAUDE.md §5.3）。
-- tick処理・価格計算・ランキング集計はすべてこれを起点にする。通貨が増えても
-- ループ回数が変わるだけで、tick の実行回数自体は増えない設計にすること。
SELECT * FROM currencies
ORDER BY id;
