package game

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// replayTestClock はテストで時間を進められる時計（CLAUDE.md §5.1）。
type replayTestClock struct{ now time.Time }

func (c *replayTestClock) Now() time.Time { return c.now }

// seededPairs はマイグレーション 000009 で投入された 4 ペアを返す。
func seededPairs(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []db.CurrencyPair {
	t.Helper()
	pairs, err := db.New(pool).ListCurrencyPairs(ctx)
	if err != nil {
		t.Fatalf("list currency pairs: %v", err)
	}
	var seeded []db.CurrencyPair
	for _, p := range pairs {
		switch p.Symbol {
		case "USD/JPY", "EUR/USD", "GBP/USD", "AUD/USD":
			seeded = append(seeded, p)
		}
	}
	if len(seeded) != 4 {
		t.Fatalf("want 4 seeded pairs, got %d", len(seeded))
	}
	return seeded
}

// ohlcBar は (symbol, dayIndex) から OHLC 文字列を返す。
type ohlcBar func(symbol string, day int) (o, h, l, c string)

// replayTestDates は start から連続する n 営業日（平日）を返す。
func replayTestDates(start time.Time, n int) []time.Time {
	out := make([]time.Time, 0, n)
	d := start
	for len(out) < n {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			out = append(out, d)
		}
		d = d.AddDate(0, 0, 1)
	}
	return out
}

// replayCleanupOrder は replay 系の後始末の SQL を子テーブルから順に並べたもの。
var replayCleanupOrder = []string{
	`DELETE FROM replay_positions`,
	`DELETE FROM replay_leverage_unlocks`,
	`DELETE FROM replay_speed_changes`,
	`DELETE FROM replay_game_rates`,
	`DELETE FROM replay_game_days`,
	`DELETE FROM replay_games`,
	`DELETE FROM daily_rates`,
	`DELETE FROM rate_datasets`,
}

// insertReplayData は 4 ペアに日足データを投入する。過去の失敗等で残った行を先に掃除し、
// t.Cleanup で（ゲーム行も含めて）子テーブルから順に消す。
func insertReplayData(t *testing.T, ctx context.Context, pool *pgxpool.Pool, dates []time.Time, bar ohlcBar) []db.CurrencyPair {
	t.Helper()

	// 前回のテスト失敗等で残った行を先に掃除する（専用 DB なので安全）。
	// replay 系 → レート系 → 非シードの通貨ペア の順（外部キー依存順）。
	pre := append([]string{}, replayCleanupOrder...)
	pre = append(pre, `DELETE FROM currency_pairs WHERE symbol NOT IN ('USD/JPY','EUR/USD','GBP/USD','AUD/USD')`)
	for _, s := range pre {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("pre-cleanup %q: %v", s, err)
		}
	}

	pairs := seededPairs(t, ctx, pool)
	for _, p := range pairs {
		var datasetID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO rate_datasets (pair_id, source, filename, fetched_on, sha256, imported_at)
			VALUES ($1, 'test', 'test.json.gz', $2, $3, $4)
			RETURNING id`,
			p.ID,
			pgtype.Date{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
			"test-sha-"+p.Symbol,
			pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		).Scan(&datasetID); err != nil {
			t.Fatalf("insert dataset for %s: %v", p.Symbol, err)
		}
		for i, d := range dates {
			o, h, l, c := bar(p.Symbol, i)
			if _, err := pool.Exec(ctx, `
				INSERT INTO daily_rates (dataset_id, pair_id, date, open, high, low, close)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				datasetID, p.ID, pgtype.Date{Time: d, Valid: true}, o, h, l, c,
			); err != nil {
				t.Fatalf("insert daily rate %s day %d: %v", p.Symbol, i, err)
			}
		}
	}

	// 後始末。connectTestDB の pool.Close より後に登録されるので先に走る（LIFO）。
	t.Cleanup(func() {
		cctx := context.Background()
		for _, s := range replayCleanupOrder {
			if _, err := pool.Exec(cctx, s); err != nil {
				t.Errorf("cleanup %q: %v", s, err)
			}
		}
	})

	return pairs
}

// pairBySymbol は pairs から symbol で引く。
func pairBySymbol(t *testing.T, pairs []db.CurrencyPair, symbol string) db.CurrencyPair {
	t.Helper()
	for _, p := range pairs {
		if p.Symbol == symbol {
			return p
		}
	}
	t.Fatalf("pair %s not found", symbol)
	return db.CurrencyPair{}
}

// replaySetupCleanup は replay 系・レート系・非シード通貨ペアを先に掃除し、t.Cleanup で
// 後始末（子テーブルから順に削除、失敗は t.Errorf）を登録する。
func replaySetupCleanup(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pre := append([]string{}, replayCleanupOrder...)
	pre = append(pre, `DELETE FROM currency_pairs WHERE symbol NOT IN ('USD/JPY','EUR/USD','GBP/USD','AUD/USD')`)
	for _, s := range pre {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("pre-cleanup %q: %v", s, err)
		}
	}
	t.Cleanup(func() {
		cctx := context.Background()
		for _, s := range replayCleanupOrder {
			if _, err := pool.Exec(cctx, s); err != nil {
				t.Errorf("cleanup %q: %v", s, err)
			}
		}
	})
}

// insertDataset は通貨ペアに 1 データセットと日足を投入し、データセット ID を返す。
func insertDataset(t *testing.T, ctx context.Context, pool *pgxpool.Pool, p db.CurrencyPair, shaSuffix string, importedAt time.Time, dates []time.Time, bar ohlcBar) int64 {
	t.Helper()
	var datasetID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO rate_datasets (pair_id, source, filename, fetched_on, sha256, imported_at)
		VALUES ($1, 'test', 'test.json.gz', $2, $3, $4) RETURNING id`,
		p.ID,
		pgtype.Date{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		shaSuffix,
		pgtype.Timestamptz{Time: importedAt, Valid: true},
	).Scan(&datasetID); err != nil {
		t.Fatalf("insert dataset: %v", err)
	}
	for i, d := range dates {
		o, h, l, c := bar(p.Symbol, i)
		if _, err := pool.Exec(ctx, `
			INSERT INTO daily_rates (dataset_id, pair_id, date, open, high, low, close)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			datasetID, p.ID, pgtype.Date{Time: d, Valid: true}, o, h, l, c,
		); err != nil {
			t.Fatalf("insert daily rate: %v", err)
		}
	}
	return datasetID
}

// newReplayServiceForTest は固定シードの乱数と時計を持ったサービスを作る。
func newReplayServiceForTest(pool *pgxpool.Pool, clock Clock) *ReplayService {
	svc := NewReplayService(pool, clock)
	svc.rng = rand.New(rand.NewSource(1))
	return svc
}

// constantBar は全ペア・全日同じ OHLC を返す。
func constantBar(o, h, l, c string) ohlcBar {
	return func(string, int) (string, string, string, string) { return o, h, l, c }
}

// TestReplay_FullGameToCompletion は 1 ゲームを最後まで進め、2 ペアの同時保有・最終日の
// 一括決済・最終資金 = スコア・終了後にのみ正確な日付が返ることを確認する（#103 完了条件）。
func TestReplay_FullGameToCompletion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	pairs := insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	token, gameID := res.Token, res.GameID

	if res.StartYear != 2015 {
		t.Errorf("startYear = %d, want 2015", res.StartYear)
	}
	if !res.Funds.Equal(dec("1000")) {
		t.Errorf("initial funds = %s, want 1000", res.Funds)
	}
	if res.PublishedDays != 0 {
		t.Errorf("publishedDays = %d, want 0", res.PublishedDays)
	}
	if len(res.Candles) != 4 {
		t.Fatalf("candles pairs = %d, want 4", len(res.Candles))
	}
	for _, pc := range res.Candles {
		if len(pc.Candles) != 20 {
			t.Errorf("pre candles = %d, want 20", len(pc.Candles))
		}
		for _, c := range pc.Candles {
			if c.Date != nil {
				t.Errorf("pre candle has date before end: %v", c.Date)
			}
		}
	}

	usd := pairBySymbol(t, pairs, "USD/JPY")
	eur := pairBySymbol(t, pairs, "EUR/USD")

	// 2 ペアを同時に保有する（買い・売り両方）。
	ord1, err := svc.PlaceOrder(ctx, token, gameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideLong, Margin: dec("100"), Leverage: dec("10"),
	})
	if err != nil {
		t.Fatalf("place order 1: %v", err)
	}
	if !ord1.Position.EntryPrice.Equal(dec("100")) {
		t.Errorf("entry price 1 = %s, want 100", ord1.Position.EntryPrice)
	}
	ord2, err := svc.PlaceOrder(ctx, token, gameID, ReplayOrderParams{
		PairID: eur.ID, Side: SideShort, Margin: dec("50"), Leverage: dec("5"),
	})
	if err != nil {
		t.Fatalf("place order 2: %v", err)
	}
	// 100 - 100(証拠金) - 0.5(手数料) - 50 - 0.125 = 849.375
	if !ord2.Funds.Equal(dec("849.375")) {
		t.Errorf("funds after 2 orders = %s, want 849.375", ord2.Funds)
	}

	// 途中状態（published=1）では日付も終了情報も返らない。
	clock.now = clock.now.Add(3 * time.Second)
	state, err := svc.GetState(ctx, token, gameID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Status != replayStatusPlaying {
		t.Errorf("status = %s, want playing", state.Status)
	}
	if state.PublishedDays != 1 {
		t.Errorf("publishedDays = %d, want 1", state.PublishedDays)
	}
	if state.IntervalStart != nil || state.IntervalEnd != nil || state.FinalFunds != nil {
		t.Errorf("mid-game state leaks end info: %+v", state)
	}
	if len(state.Positions) != 2 {
		t.Errorf("open positions = %d, want 2", len(state.Positions))
	}

	// 終了（60 営業日 = 180 秒）まで進める。
	clock.now = clock.now.Add(177 * time.Second) // 3s × 59 = 177s → 合計 180s
	state, err = svc.GetState(ctx, token, gameID)
	if err != nil {
		t.Fatalf("GetState(final): %v", err)
	}
	if state.Status != replayStatusFinished {
		t.Fatalf("status = %s, want finished", state.Status)
	}
	if state.PublishedDays != 60 {
		t.Errorf("publishedDays = %d, want 60", state.PublishedDays)
	}
	if len(state.Positions) != 0 {
		t.Errorf("final open positions = %d, want 0", len(state.Positions))
	}
	// 手数料のみ差し引かれる: 1000 - (0.5×2 + 0.125×2) = 998.75
	wantFinal := dec("998.75")
	if !state.Funds.Equal(wantFinal) {
		t.Errorf("final funds = %s, want %s", state.Funds, wantFinal)
	}
	if state.FinalFunds == nil || !state.FinalFunds.Equal(wantFinal) {
		t.Errorf("final funds (score) = %v, want %s", state.FinalFunds, wantFinal)
	}
	if state.IntervalStart == nil || !state.IntervalStart.Equal(dates[20]) {
		t.Errorf("intervalStart = %v, want %s", state.IntervalStart, dates[20].Format("2006-01-02"))
	}
	if state.IntervalEnd == nil || !state.IntervalEnd.Equal(dates[79]) {
		t.Errorf("intervalEnd = %v, want %s", state.IntervalEnd, dates[79].Format("2006-01-02"))
	}
	// 終了後は日足に日付が付く。
	for _, pc := range state.Candles {
		if len(pc.Candles) != 80 {
			t.Errorf("final candles = %d, want 80", len(pc.Candles))
		}
		for _, c := range pc.Candles {
			if c.Date == nil {
				t.Errorf("final candle day %d has no date", c.Day)
			}
		}
	}
}

// TestReplay_ForcedLiquidation は強制決済（買いは安値がラインに届いたら証拠金を失う）を確認する。
func TestReplay_ForcedLiquidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	// day 20（最初の区間日）だけ安値を 85 にし、他は 95 にして長い買いを強制決済させる。
	bar := func(symbol string, day int) (string, string, string, string) {
		low := "95"
		if day == 20 {
			low = "85"
		}
		return "100", "105", low, "100"
	}
	pairs := insertReplayData(t, ctx, pool, dates, bar)

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	usd := pairBySymbol(t, pairs, "USD/JPY")

	ord, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideLong, Margin: dec("100"), Leverage: dec("10"),
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	// 証拠金100・10倍（取引規模1000）→ ライン = 100 × (1 - 100/1000) = 90。
	if !ord.Position.LiquidationLine.Equal(dec("90")) {
		t.Errorf("liquidation line = %s, want 90", ord.Position.LiquidationLine)
	}

	// 1 日進めると day 20（安値 85）が公開され、強制決済される。
	clock.now = clock.now.Add(3 * time.Second)
	state, err := svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(state.Positions) != 0 {
		t.Fatalf("open positions = %d, want 0 (should be liquidated)", len(state.Positions))
	}
	// 決済で戻る額は 0。残る資金は開いた時に払った手数料分だけ減る: 1000 - 100.5 = 899.5。
	if !state.Funds.Equal(dec("899.5")) {
		t.Errorf("funds = %s, want 899.5", state.Funds)
	}

	// DB 上の建玉が liquidated で、ラインの値段（90）で決済されていることを確認する。
	var side, status string
	var closePrice decimal.Decimal
	var pnl decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT side, status, close_price, pnl FROM replay_positions WHERE game_id = $1`, res.GameID).
		Scan(&side, &status, &closePrice, &pnl); err != nil {
		t.Fatalf("query liquidated position: %v", err)
	}
	if status != replayPositionLiquidated {
		t.Errorf("status = %s, want liquidated", status)
	}
	if !closePrice.Equal(dec("90")) {
		t.Errorf("close_price = %s, want 90", closePrice)
	}
	if !pnl.Equal(dec("-100")) {
		t.Errorf("pnl = %s, want -100", pnl)
	}
}

// TestReplay_AddMargin は証拠金の追加でラインが遠ざかり、取引規模・損益の基準が変わらないことを確認する。
func TestReplay_AddMargin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	pairs := insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	usd := pairBySymbol(t, pairs, "USD/JPY")

	ord, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideLong, Margin: dec("100"), Leverage: dec("10"),
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	posID := ord.Position.ID
	if !ord.Position.LiquidationLine.Equal(dec("90")) {
		t.Fatalf("line before = %s, want 90", ord.Position.LiquidationLine)
	}

	// 証拠金を 50 追加 → ラインは 85 に遠ざかる。取引規模（initial_margin）は 100 のまま。
	after, err := svc.AddMargin(ctx, res.Token, res.GameID, posID, dec("50"))
	if err != nil {
		t.Fatalf("AddMargin: %v", err)
	}
	if !after.Position.Margin.Equal(dec("150")) {
		t.Errorf("margin = %s, want 150", after.Position.Margin)
	}
	if !after.Position.InitialMargin.Equal(dec("100")) {
		t.Errorf("initialMargin = %s, want 100 (must not change)", after.Position.InitialMargin)
	}
	if !after.Position.LiquidationLine.Equal(dec("85")) {
		t.Errorf("line after = %s, want 85", after.Position.LiquidationLine)
	}
	// 追加額は未使用資金から差し引かれる: 899.5 - 50 = 849.5。
	if !after.Funds.Equal(dec("849.5")) {
		t.Errorf("funds = %s, want 849.5", after.Funds)
	}

	// 1% の値動きに対する損益は証拠金追加後も 10 のまま（取引規模 1000 が基準）。
	pnl := replayPnL(SideLong, after.Position.EntryPrice, dec("101"), replayNotional(after.Position.InitialMargin, int32(after.Position.Leverage)))
	if !pnl.Equal(dec("10")) {
		t.Errorf("pnl for 1%% move = %s, want 10", pnl)
	}
}

// TestReplay_LeverageUnlock は上限解放の費用が 250 → 500 とペアごとに数えられることを確認する。
func TestReplay_LeverageUnlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	pairs := insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	usd := pairBySymbol(t, pairs, "USD/JPY")
	eur := pairBySymbol(t, pairs, "EUR/USD")

	// USD/JPY を 2 段解放（250 → 500）。
	u1, err := svc.UnlockLeverage(ctx, res.Token, res.GameID, usd.ID)
	if err != nil {
		t.Fatalf("unlock 1: %v", err)
	}
	if !u1.Cost.Equal(dec("250")) || u1.Tier != 1 || !u1.Funds.Equal(dec("750")) {
		t.Errorf("unlock 1 = %+v, want cost 250/tier 1/funds 750", u1)
	}
	if !u1.MaxLeverage.Equal(dec("35")) {
		t.Errorf("maxLeverage 1 = %s, want 35", u1.MaxLeverage)
	}

	u2, err := svc.UnlockLeverage(ctx, res.Token, res.GameID, usd.ID)
	if err != nil {
		t.Fatalf("unlock 2: %v", err)
	}
	if !u2.Cost.Equal(dec("500")) || u2.Tier != 2 || !u2.Funds.Equal(dec("250")) {
		t.Errorf("unlock 2 = %+v, want cost 500/tier 2/funds 250", u2)
	}
	if !u2.MaxLeverage.Equal(dec("45")) {
		t.Errorf("maxLeverage 2 = %s, want 45", u2.MaxLeverage)
	}

	// 別ペア（EUR/USD）は 250 から数え直す。
	u3, err := svc.UnlockLeverage(ctx, res.Token, res.GameID, eur.ID)
	if err != nil {
		t.Fatalf("unlock 3: %v", err)
	}
	if !u3.Cost.Equal(dec("250")) || u3.Tier != 1 {
		t.Errorf("unlock 3 = %+v, want cost 250/tier 1", u3)
	}
	if !u3.MaxLeverage.Equal(dec("40")) {
		t.Errorf("maxLeverage 3 = %s, want 40", u3.MaxLeverage)
	}
}

// TestReplay_SpeedChange は速さの変更で公開済み日数が速さに応じて進むことを確認する。
func TestReplay_SpeedChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}

	// 即 ×2 にして 6 秒経過 → 6s×2/3s = 4 日。
	if _, err := svc.ChangeSpeed(ctx, res.Token, res.GameID, 2); err != nil {
		t.Fatalf("ChangeSpeed: %v", err)
	}
	clock.now = clock.now.Add(6 * time.Second)
	state, err := svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.PublishedDays != 4 {
		t.Errorf("publishedDays = %d, want 4", state.PublishedDays)
	}
	if state.Speed != 2 {
		t.Errorf("speed = %d, want 2", state.Speed)
	}

	// ×3 にしてさらに 6 秒 → 4 + 6s×3/3s = 10 日。
	if _, err := svc.ChangeSpeed(ctx, res.Token, res.GameID, 3); err != nil {
		t.Fatalf("ChangeSpeed(3): %v", err)
	}
	clock.now = clock.now.Add(6 * time.Second)
	state, err = svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState(3): %v", err)
	}
	if state.PublishedDays != 10 {
		t.Errorf("publishedDays = %d, want 10", state.PublishedDays)
	}

	// 不正な速さはエラー。
	if _, err := svc.ChangeSpeed(ctx, res.Token, res.GameID, 5); !errors.Is(err, ErrReplayInvalidSpeed) {
		t.Errorf("ChangeSpeed(5) = %v, want ErrReplayInvalidSpeed", err)
	}
}

// TestReplay_EarlyTermination は早期終了（建玉なし・資金が最低注文額に届かない）を確認する。
func TestReplay_EarlyTermination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	pairs := insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	usd := pairBySymbol(t, pairs, "USD/JPY")
	eur := pairBySymbol(t, pairs, "EUR/USD")

	// 上限解放で資金を 0 にする（USD/JPY を 2 段 + EUR/USD を 1 段 = 1000）。
	for _, p := range []db.CurrencyPair{usd, usd, eur} {
		if _, err := svc.UnlockLeverage(ctx, res.Token, res.GameID, p.ID); err != nil {
			t.Fatalf("unlock %s: %v", p.Symbol, err)
		}
	}

	// 建玉なし・資金 0 → 早期終了。
	state, err := svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Status != replayStatusEarlyFinished {
		t.Fatalf("status = %s, want early_finished", state.Status)
	}
	if state.FinalFunds == nil || !state.FinalFunds.Equal(decimal.Zero) {
		t.Errorf("final funds = %v, want 0", state.FinalFunds)
	}
	// 早期終了でも区間の正確な日付は明かされる。
	if state.IntervalStart == nil || state.IntervalEnd == nil {
		t.Errorf("early-finished state should reveal interval dates")
	}
	// 終了後は全区間（80 日）の日足が日付付きで返る。
	for _, pc := range state.Candles {
		if len(pc.Candles) != 80 {
			t.Errorf("early-finished candles = %d, want 80", len(pc.Candles))
		}
		for _, c := range pc.Candles {
			if c.Date == nil {
				t.Errorf("early-finished candle day %d has no date", c.Day)
			}
		}
	}
}

// TestReplay_OrderRestrictions は反対方向の建玉の拒否・トークン不一致・ゲスト名検証を確認する。
func TestReplay_OrderRestrictions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	pairs := insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	// ゲスト名の検証（前後の空白を除いて 1〜16 文字。文字数はルーンで数える。#104）。
	if _, err := svc.StartGame(ctx, "", ""); !errors.Is(err, ErrReplayInvalidGuestName) {
		t.Errorf("empty guest name = %v, want ErrReplayInvalidGuestName", err)
	}
	if _, err := svc.StartGame(ctx, "", "   \t  "); !errors.Is(err, ErrReplayInvalidGuestName) {
		t.Errorf("whitespace-only guest name = %v, want ErrReplayInvalidGuestName", err)
	}
	if _, err := svc.StartGame(ctx, "", "1234567890123456"); err != nil {
		t.Errorf("16-char guest name = %v, want nil", err)
	}
	if _, err := svc.StartGame(ctx, "", "12345678901234567"); !errors.Is(err, ErrReplayInvalidGuestName) {
		t.Errorf("17-char guest name = %v, want ErrReplayInvalidGuestName", err)
	}
	if _, err := svc.StartGame(ctx, "", "   1234567890123456   "); err != nil {
		t.Errorf("16-char guest name with spaces = %v, want nil", err)
	}
	if _, err := svc.StartGame(ctx, "", strings.Repeat("あ", 16)); err != nil {
		t.Errorf("16 multibyte guest name = %v, want nil", err)
	}
	if _, err := svc.StartGame(ctx, "", strings.Repeat("あ", 17)); !errors.Is(err, ErrReplayInvalidGuestName) {
		t.Errorf("17 multibyte guest name = %v, want ErrReplayInvalidGuestName", err)
	}

	res, err := svc.StartGame(ctx, "", "  guest  ")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	usd := pairBySymbol(t, pairs, "USD/JPY")
	eur := pairBySymbol(t, pairs, "EUR/USD")

	// トークン不一致は ErrReplayGameNotFound。
	if _, err := svc.GetState(ctx, "wrong-token", res.GameID); !errors.Is(err, ErrReplayGameNotFound) {
		t.Errorf("wrong token = %v, want ErrReplayGameNotFound", err)
	}

	// 買いを建てる。
	if _, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideLong, Margin: dec("10"), Leverage: dec("1"),
	}); err != nil {
		t.Fatalf("place long: %v", err)
	}

	// 同じペアの反対方向は拒否。
	if _, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideShort, Margin: dec("10"), Leverage: dec("1"),
	}); !errors.Is(err, ErrReplayOppositePosition) {
		t.Errorf("opposite order = %v, want ErrReplayOppositePosition", err)
	}

	// 異なるペアの売りは許可。
	if _, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: eur.ID, Side: SideShort, Margin: dec("10"), Leverage: dec("1"),
	}); err != nil {
		t.Errorf("short on other pair = %v, want nil", err)
	}
}

// TestReplay_CommonDaysSkipMissing は、あるペアだけ値が無い日（祝日など）を飛ばして
// 共通営業日だけが使われることを確認する（design.md §13「対象と期間」）。
func TestReplay_CommonDaysSkipMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	// 81 連続平日を用意し、EUR/USD だけ index 30 の日を欠損させる（祝日を模す）。
	allDates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 81)
	eurDates := make([]time.Time, 0, len(allDates)-1)
	eurDates = append(eurDates, allDates[:30]...)
	eurDates = append(eurDates, allDates[31:]...)
	datesBySymbol := map[string][]time.Time{
		"USD/JPY": allDates, "EUR/USD": eurDates, "GBP/USD": allDates, "AUD/USD": allDates,
	}

	for _, s := range append([]string{
		`DELETE FROM currency_pairs WHERE symbol NOT IN ('USD/JPY','EUR/USD','GBP/USD','AUD/USD')`,
	}, replayCleanupOrder...) {
		if _, err := pool.Exec(ctx, s); err != nil {
			t.Fatalf("pre-cleanup %q: %v", s, err)
		}
	}
	pairs := seededPairs(t, ctx, pool)
	for _, p := range pairs {
		dates := datesBySymbol[p.Symbol]
		var datasetID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO rate_datasets (pair_id, source, filename, fetched_on, sha256, imported_at)
			VALUES ($1, 'test', 'test.json.gz', $2, $3, $4) RETURNING id`,
			p.ID,
			pgtype.Date{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
			"gap-sha-"+p.Symbol,
			pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		).Scan(&datasetID); err != nil {
			t.Fatalf("insert dataset: %v", err)
		}
		for _, d := range dates {
			if _, err := pool.Exec(ctx, `
				INSERT INTO daily_rates (dataset_id, pair_id, date, open, high, low, close)
				VALUES ($1, $2, $3, 100, 105, 95, 100)`,
				datasetID, p.ID, pgtype.Date{Time: d, Valid: true},
			); err != nil {
				t.Fatalf("insert daily rate: %v", err)
			}
		}
	}
	t.Cleanup(func() {
		cctx := context.Background()
		for _, s := range replayCleanupOrder {
			if _, err := pool.Exec(cctx, s); err != nil {
				t.Errorf("cleanup %q: %v", s, err)
			}
		}
	})

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	// 共通営業日は 80 日（81 − 欠損 1）。区間 = 共通 80 日のうち後半 60 日。
	// 開始前 20 日の日足が返る（欠損日を含まない）。
	if len(res.Candles) != 4 {
		t.Fatalf("candles pairs = %d, want 4", len(res.Candles))
	}
	for _, pc := range res.Candles {
		if len(pc.Candles) != 20 {
			t.Errorf("pre candles = %d, want 20", len(pc.Candles))
		}
	}
	// 区間の開始日は共通営業日の index 20。欠損は allDates[30]（index 30）なので、
	// index 20 は欠損より前にある → allDates[20] のまま。
	// 終了まで進めて区間の開始日を確認する。
	clock.now = clock.now.Add(180 * time.Second)
	state, err := svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.Status != replayStatusFinished {
		t.Fatalf("status = %s, want finished", state.Status)
	}
	if state.IntervalStart == nil || !state.IntervalStart.Equal(allDates[20]) {
		t.Errorf("intervalStart = %v, want %s", state.IntervalStart, allDates[20].Format("2006-01-02"))
	}
}

// TestReplay_DoesNotAffectUsersBalance はログインユーザーのゲームでも users.balance が変わらないことを確認する。
func TestReplay_DoesNotAffectUsersBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const userID = "replay-user"
	const wantBalance = "5000.00000000"
	// ユーザーの後始末は insertReplayData の後始末（replay_games の削除）より先に登録しておく
	// （LIFO なので replay 系が先に消え、その後にユーザーを消せる）。
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (discord_id, display_name, balance) VALUES ($1, $2, $3)
		ON CONFLICT (discord_id) DO UPDATE SET balance = EXCLUDED.balance`,
		userID, "Replay User", wantBalance); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, userID); err != nil {
			t.Errorf("cleanup user: %v", err)
		}
	})

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	pairs := insertReplayData(t, ctx, pool, dates, constantBar("100", "105", "95", "100"))

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, userID, "")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	usd := pairBySymbol(t, pairs, "USD/JPY")
	if _, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideLong, Margin: dec("100"), Leverage: dec("10"),
	}); err != nil {
		t.Fatalf("place order: %v", err)
	}

	var balance decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT balance FROM users WHERE discord_id = $1`, userID).Scan(&balance); err != nil {
		t.Fatalf("query user balance: %v", err)
	}
	if !balance.Equal(dec(wantBalance)) {
		t.Errorf("users.balance = %s, want %s (must not change)", balance, wantBalance)
	}
}

// TestReplay_IntervalAcrossDatasets は、1 ペアに複数のデータセット（年ごと）があるとき、
// 各日付で imported_at が最新のデータセットの値を使い、年をまたぐ区間も選べることを確認する
// （#103 レビュー対応）。旧実装はペアごとに最新 1 件だけを使うため、2015 年分が失われて
// 区間を選べなかった。
func TestReplay_IntervalAcrossDatasets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	// 2015-11-02（月）から 80 営業日（2015 → 2016 をまたぐ）。
	allDates := replayTestDates(time.Date(2015, 11, 2, 0, 0, 0, 0, time.UTC), 80)
	// 2016 年になる最初の index。それより前が 2015 年。
	splitIdx := 0
	for i, d := range allDates {
		if d.Year() == 2016 {
			splitIdx = i
			break
		}
	}
	if splitIdx == 0 || splitIdx >= 80 {
		t.Fatalf("unexpected year split: %d", splitIdx)
	}

	replaySetupCleanup(t, pool)
	pairs := seededPairs(t, ctx, pool)
	for _, p := range pairs {
		// 2015 年分（close=100）と 2016 年分（close=200）を別データセットで投入。
		insertDataset(t, ctx, pool, p, "years-2015-"+p.Symbol,
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), allDates[:splitIdx], constantBar("100", "101", "99", "100"))
		insertDataset(t, ctx, pool, p, "years-2016-"+p.Symbol,
			time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), allDates[splitIdx:], constantBar("200", "201", "199", "200"))
	}

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	if res.StartYear != 2015 {
		t.Errorf("startYear = %d, want 2015", res.StartYear)
	}
	// 開始前 20 日（2015 年）は close=100。
	for _, pc := range res.Candles {
		if len(pc.Candles) != 20 {
			t.Fatalf("pre candles = %d, want 20", len(pc.Candles))
		}
		for _, c := range pc.Candles {
			if !c.Close.Equal(dec("100")) {
				t.Errorf("pre candle day %d close = %s, want 100", c.Day, c.Close)
			}
		}
	}

	// 終了まで進め、2016 年の日足（close=200）が使われていることを確認する。
	clock.now = clock.now.Add(180 * time.Second)
	state, err := svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState(final): %v", err)
	}
	if state.Status != replayStatusFinished {
		t.Fatalf("status = %s, want finished", state.Status)
	}
	for _, pc := range state.Candles {
		for _, c := range pc.Candles {
			want := dec("100")
			if c.Day >= splitIdx {
				want = dec("200")
			}
			if !c.Close.Equal(want) {
				t.Errorf("final candle day %d close = %s, want %s", c.Day, c.Close, want)
			}
		}
	}
	// 区間が年をまたいでいる（開始 2015 → 終了 2016）。
	if state.IntervalStart == nil || state.IntervalStart.Year() != 2015 {
		t.Errorf("intervalStart = %v, want 2015", state.IntervalStart)
	}
	if state.IntervalEnd == nil || state.IntervalEnd.Year() != 2016 {
		t.Errorf("intervalEnd = %v, want 2016", state.IntervalEnd)
	}
}

// TestReplay_NewDatasetDoesNotChangeExistingGame は、同じ日付に新しいデータセットを追加しても、
// 既存のゲームの値（開始時に記録した日足）が変わらないことを確認する（#103 レビュー対応）。
func TestReplay_NewDatasetDoesNotChangeExistingGame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	dates := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)

	replaySetupCleanup(t, pool)
	pairs := seededPairs(t, ctx, pool)
	for _, p := range pairs {
		// 既存データセット（close=100）。
		insertDataset(t, ctx, pool, p, "old-"+p.Symbol,
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), dates, constantBar("100", "101", "99", "100"))
	}

	clock := &replayTestClock{now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	res, err := svc.StartGame(ctx, "", "tester")
	if err != nil {
		t.Fatalf("StartGame: %v", err)
	}
	// 開始直後の約定価格は 100。
	usd := pairBySymbol(t, pairs, "USD/JPY")
	ord, err := svc.PlaceOrder(ctx, res.Token, res.GameID, ReplayOrderParams{
		PairID: usd.ID, Side: SideLong, Margin: dec("10"), Leverage: dec("1"),
	})
	if err != nil {
		t.Fatalf("place order: %v", err)
	}
	if !ord.Position.EntryPrice.Equal(dec("100")) {
		t.Fatalf("entry price before = %s, want 100", ord.Position.EntryPrice)
	}

	// 同じ日付に新しいデータセット（close=200、imported_at が新しい）を追加。
	for _, p := range pairs {
		insertDataset(t, ctx, pool, p, "new-"+p.Symbol,
			time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), dates, constantBar("200", "201", "199", "200"))
	}

	// ゲームの値（開始時に記録した日足）は変わらず 100 のまま。
	clock.now = clock.now.Add(3 * time.Second)
	state, err := svc.GetState(ctx, res.Token, res.GameID)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	for _, pc := range state.Candles {
		for _, c := range pc.Candles {
			if !c.Close.Equal(dec("100")) {
				t.Errorf("candle day %d close = %s, want 100 (must not change)", c.Day, c.Close)
			}
		}
	}
}

// insertRankingUser はランキングのテスト用ユーザーを作る。avatar が空なら NULL。
func insertRankingUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, discordID, displayName, avatar string) {
	t.Helper()
	var avatarValue pgtype.Text
	if avatar != "" {
		avatarValue = pgtype.Text{String: avatar, Valid: true}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (discord_id, display_name, avatar)
		VALUES ($1, $2, $3)
		ON CONFLICT (discord_id) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			avatar = EXCLUDED.avatar`,
		discordID, displayName, avatarValue); err != nil {
		t.Fatalf("insert ranking user %s: %v", discordID, err)
	}
}

// insertFinishedReplayGame は確定済みのゲームを直接作る（ランキングのテストデータ）。
// userID が空ならゲスト（guestName）として作る。finalFunds は decimal 文字列。
func insertFinishedReplayGame(t *testing.T, ctx context.Context, pool *pgxpool.Pool, token, userID, guestName, finalFunds string, finishedAt time.Time) int64 {
	t.Helper()
	var userValue, guestValue pgtype.Text
	if userID != "" {
		userValue = pgtype.Text{String: userID, Valid: true}
	}
	if guestName != "" {
		guestValue = pgtype.Text{String: guestName, Valid: true}
	}
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status,
			funds, final_funds, finished_at
		) VALUES ($1, $2, $3, $4, 2015, 'finished', $5, $5, $6)
		RETURNING id`,
		token, userValue, guestValue, finishedAt.Add(-180*time.Second), finalFunds, finishedAt,
	).Scan(&id); err != nil {
		t.Fatalf("insert finished replay game %s: %v", token, err)
	}
	return id
}

// rankingEntryIndex は name と finalFunds が一致する行の位置を返す（無ければ -1）。
func rankingEntryIndex(entries []ReplayRankingEntry, name, finalFunds string) int {
	for i, e := range entries {
		if e.Name == name && e.FinalFunds.Equal(dec(finalFunds)) {
			return i
		}
	}
	return -1
}

// cleanupReplayRankingRows はランキングのテストで INSERT したゲーム・ユーザーだけを削除する。
// go test ./... はパッケージを並列に走らせるため、WHERE 無しの DELETE は使わない。
// 子テーブルから順に削除し、失敗は t.Errorf で検出する。
func cleanupReplayRankingRows(t *testing.T, pool *pgxpool.Pool, gameIDs []int64, userIDs []string) {
	t.Helper()
	cctx := context.Background()
	if len(gameIDs) > 0 {
		for _, table := range []string{
			"replay_positions",
			"replay_leverage_unlocks",
			"replay_speed_changes",
			"replay_game_rates",
			"replay_game_days",
		} {
			query := "DELETE FROM " + table + " WHERE game_id = ANY($1)"
			if _, err := pool.Exec(cctx, query, gameIDs); err != nil {
				t.Errorf("cleanup %s: %v", table, err)
			}
		}
		if _, err := pool.Exec(cctx, `DELETE FROM replay_games WHERE id = ANY($1)`, gameIDs); err != nil {
			t.Errorf("cleanup replay_games: %v", err)
		}
	}
	if len(userIDs) > 0 {
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE discord_id = ANY($1)`, userIDs); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	}
}

// insertPlayingReplayGame は未確定（status = playing）のゲームを直接作る。日足の記録は
// 作らないため、建玉が無いゲームを確定させるテストにだけ使う（確定処理は建玉が無ければ
// 日足を読まない）。
func insertPlayingReplayGame(t *testing.T, ctx context.Context, pool *pgxpool.Pool, token, guestName, funds string, startedAt time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO replay_games (
			token_hash, user_id, guest_name, started_at, start_year, status, funds
		) VALUES ($1, NULL, $2, $3, 2015, 'playing', $4)
		RETURNING id`,
		token, guestName, startedAt, funds,
	).Scan(&id); err != nil {
		t.Fatalf("insert playing replay game %s: %v", token, err)
	}
	return id
}

// TestReplay_Ranking はランキングの選び方・並び・アイコン・ゲスト印を確認する（#104）。
// テストデータは全体の上位 20 件に入るよう極端に大きいスコアを使い、自分の行どうしの
// 相対順序だけを検証する（他のテストのデータに左右されないため）。
func TestReplay_Ranking(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const (
		userA = "900000000000000001" // avatar あり
		userB = "999999999999999999" // avatar なし（デフォルト index 1）
	)
	gameIDs := make([]int64, 0, 9)
	userIDs := []string{userA, userB}
	// 挿入前から登録しておく（途中で失敗しても挿入済みの行は消す）。
	t.Cleanup(func() { cleanupReplayRankingRows(t, pool, gameIDs, userIDs) })
	insertRankingUser(t, ctx, pool, userA, "Alice", "avatar-a")
	insertRankingUser(t, ctx, pool, userB, "Bob", "")

	// 時計は他のテストのゲーム開始時刻（2026 年）より前の 2025-06-01 にする。ランキング取得は
	// status = playing のゲームをすべて advance するため、未来に開始した他テストのゲームは
	// 公開日数 0 のまま影響を受けない。
	base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clock := &replayTestClock{now: base}
	svc := newReplayServiceForTest(pool, clock)

	// 期限切れだが未確定のゲーム。テスト時計の 10 分前に開始し、取得時に確定されて
	// ランキングに載る（建玉が無いため日足は不要）。
	expiredStartedAt := base.Add(-10 * time.Minute)
	expiredID := insertPlayingReplayGame(t, ctx, pool, "rank-expired", "expired", "900000050", expiredStartedAt)
	gameIDs = append(gameIDs, expiredID)

	// ログインユーザーの自己最高と同点の再プレイ。先着（元の記録）が残ることを確認する。
	originalID := insertFinishedReplayGame(t, ctx, pool, "rank-a-original", userA, "",
		"900000030", base.Add(-20*time.Minute))
	gameIDs = append(gameIDs, originalID)
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-a-replay", userA, "",
		"900000030", base.Add(-10*time.Minute)))
	// 元の記録と再プレイの間に確定した同点のゲスト。元の記録が選ばれれば Alice が上になる。
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-between", "", "between",
		"900000030", base.Add(-15*time.Minute)))

	// ゲストは同名でも別の行。ログインユーザーは 1 人 1 行。
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-guest-1", "", "guest", "900000025", base.Add(-5*time.Minute)))
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-guest-2", "", "guest", "900000015", base.Add(-25*time.Minute)))
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-b", userB, "", "900000020", base.Add(-30*time.Minute)))
	// 同点は finished_at が早い方が上（取得時刻ではない）。
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-tie-late", "", "tie-late", "900000005", base.Add(-35*time.Minute)))
	gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool, "rank-tie-early", "", "tie-early", "900000005", base.Add(-40*time.Minute)))

	// 時計は動かさない（期限切れのゲームは開始時刻が過去なので既に 60 日目を過ぎている）。
	entries, err := svc.GetRanking(ctx)
	if err != nil {
		t.Fatalf("GetRanking: %v", err)
	}

	// 未確定だったゲームが確定し、論理的な終了時刻（開始 + 180 秒）で保存される。
	var status string
	var finishedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT status, finished_at FROM replay_games WHERE id = $1`, expiredID).
		Scan(&status, &finishedAt); err != nil {
		t.Fatalf("query expired game: %v", err)
	}
	if status != replayStatusFinished {
		t.Errorf("expired game status = %s, want finished", status)
	}
	if want := expiredStartedAt.Add(180 * time.Second); !finishedAt.Equal(want) {
		t.Errorf("expired game finished_at = %s, want %s (logical finish)", finishedAt, want)
	}
	if idx := rankingEntryIndex(entries, "expired", "900000050"); idx < 0 {
		t.Errorf("expired game is not in ranking: %+v", entries)
	}

	// ログインユーザーは 1 人 1 行で、同点の再プレイがあっても元の記録（先着）が残る。
	aliceCount := 0
	for _, e := range entries {
		if e.Name == "Alice" {
			aliceCount++
		}
	}
	if aliceCount != 1 {
		t.Errorf("Alice rows = %d, want 1", aliceCount)
	}
	if idx := rankingEntryIndex(entries, "Alice", "900000030"); idx < 0 {
		t.Errorf("Alice's best record is not in ranking: %+v", entries)
	}
	rows, err := db.New(pool).ListReplayRanking(ctx)
	if err != nil {
		t.Fatalf("ListReplayRanking: %v", err)
	}
	foundOriginal := false
	for _, r := range rows {
		if r.UserID.Valid && r.UserID.String == userA {
			foundOriginal = r.ID == originalID
			if !r.FinalFunds.Decimal.Equal(dec("900000030")) {
				t.Errorf("Alice chosen final = %s, want 900000030", r.FinalFunds.Decimal)
			}
		}
	}
	if !foundOriginal {
		t.Error("Alice's chosen record is not the earlier one (replay must not displace the original)")
	}

	// アイコン URL。avatar ありは hash、なしは Discord のデフォルト（ID から決まる）。
	for _, e := range entries {
		if e.Name == "Alice" {
			want := "https://cdn.discordapp.com/avatars/" + userA + "/avatar-a.png"
			if e.AvatarURL == nil || *e.AvatarURL != want {
				t.Errorf("Alice avatarUrl = %v, want %s", e.AvatarURL, want)
			}
			if e.IsGuest {
				t.Error("Alice must not be marked as guest")
			}
		}
		if e.Name == "Bob" {
			want := "https://cdn.discordapp.com/embed/avatars/1.png"
			if e.AvatarURL == nil || *e.AvatarURL != want {
				t.Errorf("Bob avatarUrl = %v, want %s", e.AvatarURL, want)
			}
		}
	}

	// ゲストは同名でも 1 プレイ 1 行で、ゲスト印が付き、アイコンは null。
	g1 := rankingEntryIndex(entries, "guest", "900000025")
	g2 := rankingEntryIndex(entries, "guest", "900000015")
	if g1 < 0 || g2 < 0 {
		t.Fatalf("guest rows not found: %+v", entries)
	}
	for _, idx := range []int{g1, g2} {
		if !entries[idx].IsGuest {
			t.Errorf("guest row %q must be marked as guest", entries[idx].Name)
		}
		if entries[idx].AvatarURL != nil {
			t.Errorf("guest row avatarUrl = %v, want nil", entries[idx].AvatarURL)
		}
	}

	// 自分の行どうしの相対順序（final_funds DESC, finished_at ASC, id ASC）。
	wantOrder := []struct {
		name  string
		funds string
	}{
		{"expired", "900000050"},
		{"Alice", "900000030"},
		{"between", "900000030"},
		{"guest", "900000025"},
		{"Bob", "900000020"},
		{"guest", "900000015"},
		{"tie-early", "900000005"},
		{"tie-late", "900000005"},
	}
	prev := -1
	for _, w := range wantOrder {
		idx := rankingEntryIndex(entries, w.name, w.funds)
		if idx < 0 {
			t.Fatalf("ranking entry %s/%s not found: %+v", w.name, w.funds, entries)
		}
		if idx <= prev {
			t.Errorf("ranking order wrong at %s/%s (index %d, prev %d): %+v", w.name, w.funds, idx, prev, entries)
		}
		prev = idx
	}

	// rank は 1 始まりの並び順の位置。
	for i, e := range entries {
		if e.Rank != i+1 {
			t.Errorf("entries[%d].Rank = %d, want %d", i, e.Rank, i+1)
		}
	}
}

// TestReplay_RankingLimit はランキングが上位 20 件までであることを確認する（#104）。
// 時計は他のテストのゲーム開始時刻より前の 2025-06-01 にする（並列実行される他パッケージの
// ゲームを確定させないため）。自分の 25 件だけで上位 20 件が埋まることを検証する。
func TestReplay_RankingLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	gameIDs := make([]int64, 0, 25)
	t.Cleanup(func() { cleanupReplayRankingRows(t, pool, gameIDs, nil) })

	clock := &replayTestClock{now: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)}
	svc := newReplayServiceForTest(pool, clock)

	// 25 件を投入する。他のテストのデータに左右されず自分の行だけで 20 件を超えるよう、
	// 900000000 台の大きな値を使う（ただし他のランキングテストよりは低い値にして、
	// 並列実行時に互いの行を押し出さないようにする）。
	const limitBase = 800000000
	for i := 1; i <= 25; i++ {
		funds := fmt.Sprintf("%d", limitBase+i)
		gameIDs = append(gameIDs, insertFinishedReplayGame(t, ctx, pool,
			fmt.Sprintf("rank-limit-%d", i), "", fmt.Sprintf("limit-%d", i), funds,
			clock.now.Add(time.Duration(i)*time.Second)))
	}

	entries, err := svc.GetRanking(ctx)
	if err != nil {
		t.Fatalf("GetRanking: %v", err)
	}
	if len(entries) != 20 {
		t.Fatalf("ranking length = %d, want 20", len(entries))
	}
	// 上位 20 件がスコアの降順で、rank が 1 始まりの位置になっていることだけを検証する
	// （並列実行される他パッケージのデータが混ざっても成立する）。
	for i, e := range entries {
		if e.Rank != i+1 {
			t.Errorf("entries[%d].Rank = %d, want %d", i, e.Rank, i+1)
		}
		if i > 0 && entries[i-1].FinalFunds.LessThan(e.FinalFunds) {
			t.Errorf("ranking is not sorted by finalFunds at %d: %s then %s",
				i, entries[i-1].FinalFunds, e.FinalFunds)
		}
	}
	// 自分の 25 件のうち、下位 5 件（801〜805）は必ず上位 20 件から外れる。上位側の
	// どの行が残るかは他パッケージの並列データに左右されるため検証しない。
	for i := 1; i <= 5; i++ {
		idx := rankingEntryIndex(entries, fmt.Sprintf("limit-%d", i), fmt.Sprintf("%d", limitBase+i))
		if idx >= 0 {
			t.Errorf("limit-%d should not be in the top 20", i)
		}
	}
}

// TestReplay_RankingContinuesAfterFailedGame は、あるゲームの確定処理が失敗しても、
// 残りの期限切れゲームが確定されることを確認する（CodeRabbit 指摘）。1 つの
// トランザクションで全ゲームを処理していた旧実装では、失敗した時点で先行分も
// ロールバックされていた。
func TestReplay_RankingContinuesAfterFailedGame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	gameIDs := make([]int64, 0, 2)
	t.Cleanup(func() { cleanupReplayRankingRows(t, pool, gameIDs, nil) })

	now := time.Date(2025, 6, 3, 12, 0, 0, 0, time.UTC)
	clock := &replayTestClock{now: now}
	svc := newReplayServiceForTest(pool, clock)

	pairs := seededPairs(t, ctx, pool)

	// 壊れたゲーム: 日足の記録が無いのに未決済建玉を持つ。時計が 60 日目を過ぎると
	// 最終日の終値で決済できず、advance がエラーになる。開始時刻は他のランキングテストの
	// 時計（2025-06-01 12:00）より後にして、他テストからは公開日数 0 のまま見えるようにする。
	brokenID := insertPlayingReplayGame(t, ctx, pool, "rank-fail-broken", "broken", "900000061",
		time.Date(2025, 6, 2, 12, 0, 0, 0, time.UTC))
	gameIDs = append(gameIDs, brokenID)
	if _, err := pool.Exec(ctx, `
		INSERT INTO replay_positions (
			game_id, pair_id, side, initial_margin, margin, leverage, entry_price, entry_day, status
		) VALUES ($1, $2, 'long', 10, 10, 1, 100, 20, 'open')`,
		brokenID, pairs[0].ID); err != nil {
		t.Fatalf("insert broken position: %v", err)
	}

	// 正常な期限切れゲーム。壊れたゲームの後に処理される（ID 順）。
	normalID := insertPlayingReplayGame(t, ctx, pool, "rank-fail-normal", "normal", "900000060",
		now.Add(-10*time.Minute))
	gameIDs = append(gameIDs, normalID)

	if _, err := svc.GetRanking(ctx); err == nil {
		t.Fatal("GetRanking = nil error, want error from the broken game")
	}

	// 壊れたゲームの失敗でロールバックされず、正常なゲームは確定している。
	var normalStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM replay_games WHERE id = $1`, normalID).Scan(&normalStatus); err != nil {
		t.Fatalf("query normal game: %v", err)
	}
	if normalStatus != replayStatusFinished {
		t.Errorf("normal game status = %s, want finished (other games must still be processed)", normalStatus)
	}

	// 壊れたゲームは確定できず playing のまま（そのゲームの変更だけがロールバックされる）。
	var brokenStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM replay_games WHERE id = $1`, brokenID).Scan(&brokenStatus); err != nil {
		t.Fatalf("query broken game: %v", err)
	}
	if brokenStatus != replayStatusPlaying {
		t.Errorf("broken game status = %s, want playing", brokenStatus)
	}
}
