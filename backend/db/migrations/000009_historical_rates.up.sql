-- 過去チャートモード（design.md §13）用の実レート（EV Trading Labs の日足）を
-- 保存するスキーマ（#102 REPLAY-2）。
--
-- 通常モードの price_ticks（§2.8、合成レート）とは別テーブル（§13「データモデル（概要）」）。
-- 金額・価格はすべて NUMERIC(20,8)（float禁止。CLAUDE.md §5.2）。
-- 通貨ペアはテーブルの行として扱い、コードにハードコードしない（CLAUDE.md §5.3）。

-- currency_pairs: 過去チャートモードで扱う通貨ペア（§13「対象と期間」）。
-- symbol は表示名兼キー（例 'USD/JPY'）。import-rates の --pair はこれに一致する。
CREATE TABLE currency_pairs (
    id             BIGSERIAL    PRIMARY KEY,
    symbol         TEXT         NOT NULL UNIQUE,  -- 表示名（例 'USD/JPY'）
    base_currency  TEXT         NOT NULL,         -- 基軸通貨コード（例 'USD'）
    quote_currency TEXT         NOT NULL,         -- 決済通貨コード（例 'JPY'）
    max_leverage   NUMERIC(6,2) NOT NULL,         -- レバレッジの初期上限（§13。USD/JPY 25、他 30）
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- rate_datasets: 取り込み単位（§13「データソース」。取得元・ファイル名・取得日を記録する）。
-- 同じ通貨ペアで同じ内容（SHA-256）のファイルは同じデータセットとして扱い、
-- 再実行で行が増えないようにする（#102 完了条件）。
CREATE TABLE rate_datasets (
    id          BIGSERIAL    PRIMARY KEY,
    pair_id     BIGINT       NOT NULL REFERENCES currency_pairs(id),
    source      TEXT         NOT NULL,   -- 取得元（例 'EV Trading Labs'）
    filename    TEXT         NOT NULL,   -- 取得したファイル名
    fetched_on  DATE         NOT NULL,   -- 取得日
    sha256      TEXT         NOT NULL,   -- ファイル内容（.json.gz）の SHA-256
    imported_at TIMESTAMPTZ  NOT NULL,   -- 取り込んだ時刻
    UNIQUE (pair_id, sha256),
    UNIQUE (id, pair_id)                 -- daily_rates の複合 FK の参照先。ペアの食い違いを防ぐ
);

-- daily_rates: 1 行 = 1 通貨ペアの 1 日（平日のみ）。一意キーは（データセット, 日付）。
-- 取り込みは ON CONFLICT DO NOTHING（上書きしない。値を直す必要があれば
-- 新しいデータセットとして取り込む。#102）。ask 側（ao/ah/al/ac）と出来高（v）は保存しない。
CREATE TABLE daily_rates (
    id         BIGSERIAL     PRIMARY KEY,
    dataset_id BIGINT        NOT NULL,
    pair_id    BIGINT        NOT NULL REFERENCES currency_pairs(id),
    date       DATE          NOT NULL,   -- ts（UTC の Unix 秒）の日付
    open       NUMERIC(20,8) NOT NULL,
    high       NUMERIC(20,8) NOT NULL,
    low        NUMERIC(20,8) NOT NULL,
    close      NUMERIC(20,8) NOT NULL,
    UNIQUE (dataset_id, date),
    -- dataset_id 単独でなく (dataset_id, pair_id) を参照することで、
    -- 「データセットの通貨ペアと行の通貨ペアが食い違う」状態を DB で防ぐ。
    FOREIGN KEY (dataset_id, pair_id) REFERENCES rate_datasets (id, pair_id)
);

-- #103 で「ペアと日付の範囲」で引くためのインデックス。
CREATE INDEX idx_daily_rates_pair_date ON daily_rates (pair_id, date);

-- §13「対象と期間」の4ペアをシステムで投入する（#111 で決定）。
-- 000003_seed_currencies と同じく、コードにペア名や上限値を埋め込まない。
-- ON CONFLICT (symbol) DO NOTHING で再実行可能にする。
INSERT INTO currency_pairs (symbol, base_currency, quote_currency, max_leverage)
VALUES
    ('USD/JPY', 'USD', 'JPY', 25),
    ('EUR/USD', 'EUR', 'USD', 30),
    ('GBP/USD', 'GBP', 'USD', 30),
    ('AUD/USD', 'AUD', 'USD', 30)
ON CONFLICT (symbol) DO NOTHING;
