-- 子テーブルから順に落とす（外部キーの依存順）。currency_pairs は残す（#102 の投入）。
DROP TABLE IF EXISTS replay_leverage_unlocks;
DROP TABLE IF EXISTS replay_positions;
DROP TABLE IF EXISTS replay_speed_changes;
DROP TABLE IF EXISTS replay_game_datasets;
DROP TABLE IF EXISTS replay_game_days;
DROP TABLE IF EXISTS replay_games;
ALTER TABLE currency_pairs DROP COLUMN IF EXISTS fee_rate;
