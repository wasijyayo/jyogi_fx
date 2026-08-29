package game

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// insertTestCurrency は tick 系の統合テスト専用に、実データ（JOG/WASI/CHEBU）を
// 一切使わない使い捨ての通貨行を作る。
//
// 実通貨は epoch_at が実際の投入時刻（およそ現在時刻）に固定されている。
// 実運用と衝突しない架空の未来日（他の integration test と同じ理由で2099年）を
// 対象にテストすると、epoch_at からの経過tick数が数千万に達し、BasePrice の
// 計算コスト（tickIndexに比例。pricing.go）が跳ね上がって60回のTickが
// 数分かかってしまう。epoch_at 自体をテストの起点に合わせることで、
// 「架空の未来日で安全」と「経過tick数が小さく高速」を両立させる。
func insertTestCurrency(t *testing.T, ctx context.Context, pool *pgxpool.Pool, symbol string, seed int64, epochAt time.Time) db.Currency {
	t.Helper()

	// 前回のテスト失敗等で同名の行が残っていた場合に備え、先に掃除する
	// （currencies.symbol は UNIQUE 制約があるため、残っていると INSERT が失敗する）。
	_, _ = pool.Exec(ctx,
		`DELETE FROM price_ticks WHERE currency_id IN (SELECT id FROM currencies WHERE symbol = $1)`, symbol)
	_, _ = pool.Exec(ctx, `DELETE FROM currencies WHERE symbol = $1`, symbol)

	var c db.Currency
	row := pool.QueryRow(ctx, `
		INSERT INTO currencies (
			symbol, name, base_price, drift, volatility, lambda, k, liquidity,
			pressure, pressure_at, off_session_scale, seed, epoch_at, max_leverage, fee_rate
		) VALUES (
			$1, $1, 100, 0, 0.0020, 0.1732867951, 0.0100, 40000,
			0, $2, 0.0500, $3, $2, 10.00, 0.000500
		) RETURNING id, symbol, name, base_price, drift, volatility, lambda, k, liquidity,
			pressure, pressure_at, off_session_scale, seed, epoch_at, max_leverage, fee_rate,
			created_by, created_at`,
		symbol, pgtype.Timestamptz{Time: epochAt, Valid: true}, seed)
	if err := row.Scan(
		&c.ID, &c.Symbol, &c.Name, &c.BasePrice, &c.Drift, &c.Volatility, &c.Lambda, &c.K, &c.Liquidity,
		&c.Pressure, &c.PressureAt, &c.OffSessionScale, &c.Seed, &c.EpochAt, &c.MaxLeverage, &c.FeeRate,
		&c.CreatedBy, &c.CreatedAt,
	); err != nil {
		t.Fatalf("insert test currency %s: %v", symbol, err)
	}

	// 後始末は呼び出し側で行う。price_ticks は currency_id だけでなく
	// game_sessions への外部キーも持つため、ここで先に price_ticks・currencies
	// だけ消してしまうと、後から呼び出し側が game_sessions を消すタイミングで
	// 「まだ削除していないgame_sessionsを参照するprice_ticksが残っている」
	// 場合に外部キー制約違反になりうる。呼び出し側で
	// price_ticks → game_sessions → currencies の順に1つのt.Cleanupにまとめること。

	return c
}

// connectTestDB はローカル Postgres への接続を用意する。繋がらなければテストを
// スキップする（session_integration_test.go と同じ作法）。
func connectTestDB(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://app:app@localhost:5432/fxgame?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	// pool.Close は t.Cleanup に先に登録し、後始末のDELETEを後から登録することで
	// LIFOで後始末が先に走るようにする（#55の教訓。session_integration_test.goと同じ）。
	t.Cleanup(func() { pool.Close() })

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("ローカル Postgres に接続できないためスキップ（docker compose up -d を実行してください）: %v", err)
	}
	return pool
}

// TestTick_60回呼んでセッションをシミュレートできる は #35 の完了条件
// 「Tick を1分刻みに60回呼んで1日のセッションを数秒でシミュレートできること
// （design.md §11.1）」を確認する。
//
// 使い捨て通貨1つに対して、Tick() が内部で呼ぶのと同じ手順（寄り付き
// openCurrency → 通常tick writePriceTick × 59）を直接実行する。全通貨をループする
// Tick() 自体をそのまま使わないのは、実通貨（JOG/WASI/CHEBU）を巻き込むと
// insertTestCurrency のコメントの通りBasePriceのコストで極端に遅くなるため。
func TestTick_60回呼んでセッションをシミュレートできる(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 1, 15, 12, 0, 0, 0, jst) // 実運用と衝突しない架空の未来日
	c := insertTestCurrency(t, ctx, pool, "TICKSIM", 999001, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 1, 15, 0, 0, 0, 0, jst), Valid: true}
	// price_ticks → game_sessions → currencies の順で削除する
	// （price_ticksが両方に外部キーを持つため、この順を崩すと制約違反になる）。
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, c.ID)
		if s, err := q.GetGameSessionByDate(cctx, sessionDate); err == nil {
			_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, s.ID)
		}
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
	})

	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     sessionDate,
		Seed:     1,
		OpenedAt: pgtype.Timestamptz{Time: epoch, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: epoch.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}

	simStart := time.Now()

	if err := openCurrency(ctx, q, session, c, epoch); err != nil {
		t.Fatalf("openCurrency(寄り付き): %v", err)
	}
	for i := 1; i < 60; i++ {
		now := epoch.Add(time.Duration(i) * time.Minute)
		if err := writePriceTick(ctx, q, session, c, now); err != nil {
			t.Fatalf("writePriceTick(#%d, %s): %v", i, now, err)
		}
	}

	if elapsed := time.Since(simStart); elapsed > 5*time.Second {
		t.Errorf("60tick分のシミュレートに%sかかった。数秒で終わるはず（design.md §11.1）", elapsed)
	}

	rows, err := pool.Query(ctx, `SELECT tick_index, open, close, high, low, is_opening
		FROM price_ticks WHERE session_id = $1 AND currency_id = $2 ORDER BY tick_index`,
		session.ID, c.ID)
	if err != nil {
		t.Fatalf("query price_ticks: %v", err)
	}
	defer rows.Close()

	type tickRow struct {
		tickIndex   int64
		open, close decimal.Decimal
		high, low   decimal.Decimal
		isOpening   bool
	}
	var got []tickRow
	for rows.Next() {
		var r tickRow
		if err := rows.Scan(&r.tickIndex, &r.open, &r.close, &r.high, &r.low, &r.isOpening); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// 寄り付き1本 + 通常tick59本 = 60本（openCurrencyが最初のtick_indexを兼ねるため）。
	if len(got) != 60 {
		t.Fatalf("price_ticks行数 = %d, want 60", len(got))
	}
	if !got[0].isOpening {
		t.Error("先頭行の is_opening = false, want true")
	}

	openingCount := 0
	for i, r := range got {
		if r.isOpening {
			openingCount++
		}
		if i == 0 {
			continue
		}
		// design.md §2.8「OHLCの定義」: open = 前tickのclose。
		prev := got[i-1]
		if !r.open.Equal(prev.close) {
			t.Errorf("tick_index=%d の open(%s) != 前tickのclose(%s)", r.tickIndex, r.open, prev.close)
		}
		// 1分足にヒゲは生えない（§2.8/§9.15）ので high/low は open・close の範囲そのもの。
		wantHigh := decimal.Max(r.open, r.close)
		wantLow := decimal.Min(r.open, r.close)
		if !r.high.Equal(wantHigh) {
			t.Errorf("tick_index=%d の high = %s, want %s", r.tickIndex, r.high, wantHigh)
		}
		if !r.low.Equal(wantLow) {
			t.Errorf("tick_index=%d の low = %s, want %s", r.tickIndex, r.low, wantLow)
		}
	}
	if openingCount != 1 {
		t.Errorf("is_opening行数 = %d, want 1", openingCount)
	}
}

// TestTick_同じtickを2回実行しても壊れない は #35 の完了条件
// 「同じtickを2回実行してもprice_ticksが壊れないこと（high/lowは広がる方に更新される）」を、
// TickService.Tick そのもの（全通貨ループ・寄り付きへの委譲を含む本番の入口）に対して確認する。
//
// 通常運用でこの状況が起きるのは Cloud Scheduler のリトライだが、現時点ではまだ
// 取引（#36）が無いため pressure は tick の間で自然には変化しない。ここでは
// pressure を直接書き換えることで「2回目の呼び出しで異なる価格が計算される」
// 状況を意図的に再現し、UpsertPriceTick の ON CONFLICT（design.md §8）が
// open/is_openingを保持しつつhigh/lowを正しく広げることを検証する。
//
// TickService.Tick は実通貨（JOG/WASI/CHEBU）を全件ループするため、他のテストと
// 同じ2099年を使うと epoch_at（≒現在時刻）からの経過tick数が数千万に達し、
// BasePrice のコストで1回の呼び出しだけで数秒かかる（実測: 3通貨で約5秒）。
// このテストは3回 Tick を呼ぶため2099年のままだと十数秒〜数十秒かかってしまう。
// 「実運用より明確に先」であれば足りるため、代わりに現在の開発時期からは
// 十分先だが経過tick数が小さく済む2032年を使う。
func TestTick_同じtickを2回実行しても壊れない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies: %v", err)
	}
	if len(currencies) == 0 {
		t.Skip("currencies が投入されていないためスキップ（#31 のマイグレーション未適用）")
	}
	c := currencies[0]

	sessionDate := pgtype.Date{Time: time.Date(2032, 6, 15, 0, 0, 0, 0, jst), Valid: true}
	t.Cleanup(func() {
		cctx := context.Background()
		if s, err := q.GetGameSessionByDate(cctx, sessionDate); err == nil {
			// events は game_sessions への外部キーを持つため（#40 EVENT-1で追加）、
			// game_sessions を消す前に先に消す（#55と同じ理由）。
			_, _ = pool.Exec(cctx, `DELETE FROM events WHERE session_id = $1`, s.ID)
			_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE session_id = $1`, s.ID)
			_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, s.ID)
		}
		for _, cur := range currencies {
			_, _ = pool.Exec(cctx,
				`UPDATE currencies SET pressure = $2, pressure_at = $3 WHERE id = $1`,
				cur.ID, cur.Pressure, cur.PressureAt)
		}
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc)
	liquidationSvc := NewLiquidationService(pool, RealClock{}, tradeSvc)
	claimSvc := NewClaimService(pool, RealClock{}, ClaimConfig{
		BaseAmount:     decimal.NewFromInt(100),
		BuffMultiplier: decimal.NewFromFloat(1.5),
	})
	tickSvc := NewTickService(pool, RealClock{}, sessionSvc, liquidationSvc, claimSvc)

	start := time.Date(2032, 6, 15, 12, 0, 0, 0, jst)
	if err := tickSvc.Tick(ctx, start); err != nil {
		t.Fatalf("Tick(寄り付き): %v", err)
	}

	now := start.Add(time.Minute)
	if err := tickSvc.Tick(ctx, now); err != nil {
		t.Fatalf("Tick(1回目): %v", err)
	}

	first, err := q.GetLastPriceTick(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetLastPriceTick(1回目後): %v", err)
	}

	// 2回目の呼び出し前に pressure を書き換え、同じ tick_index に対して
	// 異なる価格が計算される状況を作る（コメントの通りシミュレーション）。
	bumpedPressure := decimal.NewFromFloat(0.05)
	if _, err := pool.Exec(ctx,
		`UPDATE currencies SET pressure = $2, pressure_at = $3 WHERE id = $1`,
		c.ID, bumpedPressure, now); err != nil {
		t.Fatalf("pressureの書き換え: %v", err)
	}

	if err := tickSvc.Tick(ctx, now); err != nil {
		t.Fatalf("Tick(2回目・同じtick): %v", err)
	}

	second, err := q.GetLastPriceTick(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetLastPriceTick(2回目後): %v", err)
	}

	if second.TickIndex != first.TickIndex {
		t.Fatalf("2回目で別のtick_indexの行が増えた: %d -> %d（新しい行を作らず同じ行を更新するはず）",
			first.TickIndex, second.TickIndex)
	}
	if !second.Open.Equal(first.Open) {
		t.Errorf("open が2回目の実行で変わった: %s -> %s（ON CONFLICTでopenは更新されないはず）",
			first.Open, second.Open)
	}
	if second.IsOpening != first.IsOpening {
		t.Errorf("is_opening が2回目の実行で変わった: %v -> %v", first.IsOpening, second.IsOpening)
	}
	if second.Close.Equal(first.Close) {
		t.Fatal("2回目で価格が変化するようpressureを書き換えたのに close が変わっていない。テストの前提が崩れている")
	}

	wantHigh := decimal.Max(first.High, second.Close)
	wantLow := decimal.Min(first.Low, second.Close)
	if !second.High.Equal(wantHigh) {
		t.Errorf("high = %s, want %s（広がる方に更新されるはず）", second.High, wantHigh)
	}
	if !second.Low.Equal(wantLow) {
		t.Errorf("low = %s, want %s（広がる方に更新されるはず）", second.Low, wantLow)
	}
}
