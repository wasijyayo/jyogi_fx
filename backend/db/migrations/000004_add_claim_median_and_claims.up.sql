-- /claim 資金配布（#39 ECON-1。design.md §7.2・§2.7「寄り付き処理の順序」手順8）用のスキーマ。
--
-- claim_median: セッション開始時点（寄り付き処理の手順6〜7＝含み損益再評価・清算判定の
-- 「後」）に、全登録者の総資産（残高＋未決済ポジションの含み損益）の中央値を算出して
-- 保存する（internal/game.ClaimService.RecordMedian）。この値以下の総資産のユーザーは
-- 配布額が1.5倍になる（確定#15 → design.md §7.0/§7.2）。
-- 寄り付き直後にしか値が定まらないため NULL 許容にし、算出前の /claim 呼び出しは
-- ClaimService 側で「まだ利用できない」として弾く。
ALTER TABLE game_sessions
    ADD COLUMN claim_median NUMERIC(20,8);

-- claims: 「同一セッション内で2回claimできない」（#39完了条件）を
-- PRIMARY KEY (session_id, user_id) の一意性でDBレベルに保証する。
-- INSERT ... ON CONFLICT DO NOTHING の結果0行なら二重取得とみなせるため、
-- アプリ側でSELECTしてから判定する二段階チェックが不要になる（TOCTOU回避）。
CREATE TABLE claims (
    session_id  BIGINT        NOT NULL REFERENCES game_sessions(id),
    user_id     TEXT          NOT NULL REFERENCES users(discord_id),
    amount      NUMERIC(20,8) NOT NULL,  -- バフ適用後の実際の配布額
    claimed_at  TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (session_id, user_id)
);
