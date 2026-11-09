-- 過去チャートモード（design.md §13）の出来事（#116 REPLAY-6）。
--
-- 出来事はコードにハードコードせず、テーブルの行として持つ（CLAUDE.md §5.3 と同じ考え方）。
-- 実在・架空を問わず、出来事のデータ自体はこのマイグレーションでは投入しない。
-- 年ごとの JSON（backend/data/events/<年>.json）をリポジトリに置き、PR のマージで
-- 採用が確定する。本番への取り込みは deploy.yml が import-events で自動的に行う
-- （docs/dev-guide.md §2.3）。

-- historical_events: 採用済みの出来事 1 件。
-- 日付は「日まで確定」「月まで」「年まで」の 3 段階の精度を持つ。
-- 日まで確定した行だけ event_date を持ち、カットインの対象になり得る。
-- 月・年までしか分からない行は新聞（開始年の一覧）にだけ載せる。
CREATE TABLE historical_events (
    id            BIGSERIAL   PRIMARY KEY,
    key           TEXT        NOT NULL,          -- 取り込みの冪等キー（例: 2016-uk-eu-referendum）
    year          INT         NOT NULL,          -- 開始年の判定に使う（event_date と一致する）
    event_date    DATE,                          -- 日まで確定した場合のみ。月・年精度では NULL
    date_precision TEXT       NOT NULL,          -- day / month / year
    headline      TEXT        NOT NULL,          -- 見出し（出来事の名前）
    description   TEXT        NOT NULL,          -- 説明（相場への影響・売買の推奨は書かない）
    category      TEXT        NOT NULL,          -- 分野（例: 金融政策）
    source        TEXT        NOT NULL,          -- 出典（媒体名・URL など）
    source_url    TEXT,                          -- 出典の URL（無ければ NULL）
    adopted       BOOLEAN     NOT NULL DEFAULT false, -- ゲームが使うのは true の行だけ
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT historical_events_precision_check CHECK (
        date_precision IN ('day', 'month', 'year')
    ),
    CONSTRAINT historical_events_date_check CHECK (
        (date_precision = 'day') = (event_date IS NOT NULL)
    ),
    CONSTRAINT historical_events_year_check CHECK (
        event_date IS NULL OR EXTRACT(YEAR FROM event_date) = year
    ),
    -- 取り込みの冪等キー。英小文字・数字・ハイフンだけで作り、全ファイルを通じて一意にする。
    CONSTRAINT historical_events_key_format_check CHECK (
        key ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    ),
    CONSTRAINT historical_events_key_key UNIQUE (key)
);

-- 新聞（開始年の採用済みの出来事）と、開始年・日精度の抽出に使う。
CREATE INDEX idx_historical_events_year_adopted ON historical_events (year, adopted);

-- replay_game_events: ゲーム開始時に確定した「採用済みの出来事 → 区間の営業日」の対応。
-- day_index は replay_game_days の通し番号（0..79）。カットインの対象は区間の 60 日
-- （20..79）だけなので、開始前の 20 日（0..19）に当たる出来事は保存しない。
-- 日付ではなく通し番号で保存し、終了前はどの API にも日付を返さない（先読み対策）。
CREATE TABLE replay_game_events (
    id          BIGSERIAL   PRIMARY KEY,
    game_id     BIGINT      NOT NULL REFERENCES replay_games(id),
    event_id    BIGINT      NOT NULL REFERENCES historical_events(id),
    day_index   INT         NOT NULL,          -- 20..79（区間の 60 日）
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 1 ゲームで同じ出来事は 1 回だけ。
    UNIQUE (game_id, event_id),
    CONSTRAINT replay_game_events_day_check CHECK (day_index BETWEEN 20 AND 79)
);

-- 状態取得・カットインの並び（公開済みの日を day_index 順、同じ日は id 順）に使う。
CREATE INDEX idx_replay_game_events_game ON replay_game_events (game_id, day_index, id);
