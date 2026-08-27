-- Walking Skeleton 最小スキーマ。
-- docs/design.md §8 のスキーマは未確定（design-decision ラベルの issue 参照）のため、
-- 現段階で必要な users / sessions_auth のみを切り出す。
-- balance・season_id など経済まわりの列は初期資金額等が未確定（#15）なので、
-- ゲームロジックに着手するタイミングで別マイグレーションとして追加する。

CREATE TABLE users (
    discord_id      TEXT        PRIMARY KEY,
    display_name    TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cookie セッション。インスタンスは常に落ちる前提なので実体は DB に持つ（CLAUDE.md §5.4）。
CREATE TABLE sessions_auth (
    id              TEXT        PRIMARY KEY,
    user_id         TEXT        NOT NULL REFERENCES users(discord_id),
    expires_at      TIMESTAMPTZ NOT NULL
);
