-- 000002_game_schema.up.sql のロールバック。作成順と逆順に削除する
-- （FK 制約があるため、参照される側を先に消すとエラーになる）。

DROP INDEX IF EXISTS idx_price_ticks_session;
DROP TABLE IF EXISTS price_ticks;
DROP TABLE IF EXISTS trades;
DROP TABLE IF EXISTS positions;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS game_sessions;
DROP TABLE IF EXISTS currencies;

ALTER TABLE users
    DROP COLUMN IF EXISTS roast_enabled,
    DROP COLUMN IF EXISTS season_id,
    DROP COLUMN IF EXISTS balance;
