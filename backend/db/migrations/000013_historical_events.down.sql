-- 子テーブル（replay_game_events）から先に落とす（外部キーの依存順）。
DROP TABLE IF EXISTS replay_game_events;
DROP TABLE IF EXISTS historical_events;
