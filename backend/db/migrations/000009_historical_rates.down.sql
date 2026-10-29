-- 子テーブル（daily_rates → rate_datasets）から順に落とす。
DROP TABLE IF EXISTS daily_rates;
DROP TABLE IF EXISTS rate_datasets;
DROP TABLE IF EXISTS currency_pairs;
