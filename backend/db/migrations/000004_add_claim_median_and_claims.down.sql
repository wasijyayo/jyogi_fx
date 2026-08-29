DROP TABLE IF EXISTS claims;

ALTER TABLE game_sessions
    DROP COLUMN IF EXISTS claim_median;
