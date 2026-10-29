-- name: GetCurrencyPairBySymbol :one
-- import-rates（#102 REPLAY-2）で --pair の値を currency_pairs.symbol に解決する。
-- 存在しなければ呼び出し側で ErrCurrencyPairNotFound にする。
SELECT * FROM currency_pairs
WHERE symbol = $1;

-- name: GetRateDatasetByPairAndSha256 :one
-- 同じ通貨ペア・同じ内容（SHA-256）のファイルは同じデータセット（#102）。
-- CreateRateDataset が ON CONFLICT DO NOTHING で 0 行になったときに呼び、
-- 既存のデータセットを取り出す。
SELECT * FROM rate_datasets
WHERE pair_id = $1 AND sha256 = $2;

-- name: CreateRateDataset :one
-- データセットを1つ作る（1ファイル=1データセット。§13「データソース」）。
-- 既に同じ（pair_id, sha256）が存在する場合は何もせず 0 行になるので、
-- 呼び出し側は pgx.ErrNoRows を見て GetRateDatasetByPairAndSha256 にフォールバックする。
INSERT INTO rate_datasets (pair_id, source, filename, fetched_on, sha256, imported_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (pair_id, sha256) DO NOTHING
RETURNING *;

-- name: InsertDailyRate :one
-- 日足を1本入れる。同じ（dataset_id, date）が既に存在すれば何もせず 0 行
-- （pgx.ErrNoRows）になる。呼び出し側はこれを「既にあった件数」として数える。
-- 上書きしない（CLAUDE.md §5.5・#102）。値を直す必要があれば新しいデータセットで取り込む。
INSERT INTO daily_rates (dataset_id, pair_id, date, open, high, low, close)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (dataset_id, date) DO NOTHING
RETURNING id;
