package game

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestGetCandles_1分足と5分足と発火済みイベント は #45 のチャート API の本体を検証する。
//
//   - 1分足: price_ticks を tick_index 昇順でそのまま返す（再現ロジック不要。§8）
//   - 5分足: design.md §2.8 の集約クエリで OHLC をまとめる
//   - イベント: 発火済み（fire_tick <= 現在tick または resolved）だけを返し、未来は漏らさない
func TestGetCandles_1分足と5分足と発火済みイベント(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 5, 10, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "CHRTTICKS", 999201, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 5, 10, 0, 0, 0, 0, jst), Valid: true}
	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     sessionDate,
		Seed:     1,
		OpenedAt: pgtype.Timestamptz{Time: epoch, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: epoch.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM events WHERE session_id = $1`, session.ID); err != nil {
			t.Errorf("cleanup: delete events: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, c.ID); err != nil {
			t.Errorf("cleanup: delete price_ticks: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID); err != nil {
			t.Errorf("cleanup: delete game_sessions: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID); err != nil {
			t.Errorf("cleanup: delete currencies: %v", err)
		}
	})

	// price_ticks を tick_index 0..10 で投入（1分足11本、5分足は 0-4 / 5-9 / 10 の3本）。
	// open/close は単調増加にして 5分足の集約結果が手で計算できるようにする。
	for i := int64(0); i <= 10; i++ {
		closeVal := decimal.NewFromInt(100 + i)
		openVal := closeVal.Sub(decimal.NewFromInt(1))
		base := decimal.NewFromInt(90 + i)
		if err := q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
			CurrencyID: c.ID,
			SessionID:  session.ID,
			TickIndex:  i,
			TickedAt:   pgtype.Timestamptz{Time: epoch.Add(time.Duration(i) * time.Minute), Valid: true},
			BasePrice:  base,
			Pressure:   decimal.Zero,
			NetVolume:  decimal.Zero,
			Open:       openVal,
			High:       closeVal, // 単調増加なので high = close
			Low:        openVal,  // 単調増加なので low = open
			Close:      closeVal,
			IsOpening:  i == 0,
		}); err != nil {
			t.Fatalf("UpsertPriceTick(%d): %v", i, err)
		}
	}

	// イベント: fire_tick=3（発火済み・未解決）、50（未来・未解決）、60（未来・解決済み）。
	mustCreateEvent := func(fireTick int64, typ string, magnitude string, resolved bool) {
		t.Helper()
		if _, err := q.CreateEvent(ctx, db.CreateEventParams{
			SessionID:     session.ID,
			CurrencyID:    c.ID,
			Type:          typ,
			FireTick:      fireTick,
			DurationTicks: 0,
			Magnitude:     decimal.RequireFromString(magnitude),
		}); err != nil {
			t.Fatalf("CreateEvent(%d): %v", fireTick, err)
		}
	}
	mustCreateEvent(3, EventTypeShock, "0.04", false)
	mustCreateEvent(50, EventTypeShock, "-0.04", false)
	mustCreateEvent(60, EventTypeVolUp, "3", true)
	// 60 の行を resolved=TRUE にする（CreateEvent は resolved を立てないため）。
	if _, err := pool.Exec(ctx, `UPDATE events SET resolved = TRUE WHERE fire_tick = 60 AND session_id = $1`, session.ID); err != nil {
		t.Fatalf("mark resolved: %v", err)
	}

	now := time.Date(2099, 5, 10, 12, 10, 0, 0, jst) // currentTick = 10
	svc := NewQuoteService(pool, RealClock{})

	t.Run("1分足", func(t *testing.T) {
		got, err := svc.GetCandles(ctx, now, "CHRTTICKS", Resolution1m)
		if err != nil {
			t.Fatalf("GetCandles(1m): %v", err)
		}
		if len(got.Candles) != 11 {
			t.Fatalf("1分足の本数 = %d, want 11", len(got.Candles))
		}
		if !got.Candles[0].IsOpening {
			t.Error("先頭（寄り付き）キャンドルの isOpening = false, want true")
		}
		if got.Candles[0].Close.String() != "100" || got.Candles[10].Close.String() != "110" {
			t.Errorf("close の並びが不正: 先頭=%s 末尾=%s, want 100 / 110",
				got.Candles[0].Close, got.Candles[10].Close)
		}
		// 時刻が ticked_at（epoch + i分）と一致すること。
		if !got.Candles[0].Time.Equal(epoch) {
			t.Errorf("先頭の time = %s, want %s", got.Candles[0].Time, epoch)
		}
	})

	t.Run("5分足", func(t *testing.T) {
		got, err := svc.GetCandles(ctx, now, "CHRTTICKS", Resolution5m)
		if err != nil {
			t.Fatalf("GetCandles(5m): %v", err)
		}
		if len(got.Candles) != 3 {
			t.Fatalf("5分足の本数 = %d, want 3（tick 0-4 / 5-9 / 10）", len(got.Candles))
		}
		first := got.Candles[0]
		// bucket0 = tick 0..4: open=99, high=104, low=99, close=104, base=94, is_opening=true
		if first.Open.String() != "99" || first.High.String() != "104" ||
			first.Low.String() != "99" || first.Close.String() != "104" {
			t.Errorf("5分足[0] = o%s h%s l%s c%s, want o99 h104 l99 c104",
				first.Open, first.High, first.Low, first.Close)
		}
		if first.BasePrice.String() != "94" {
			t.Errorf("5分足[0] base_price = %s, want 94（close に合わせた最新tickの値）", first.BasePrice)
		}
		if !first.IsOpening {
			t.Error("5分足[0]（寄り付きを含むバケット）の isOpening = false, want true")
		}
		if last := got.Candles[2]; last.Close.String() != "110" {
			t.Errorf("5分足[2] close = %s, want 110", last.Close)
		}
	})

	t.Run("イベントは発火済みのみ", func(t *testing.T) {
		got, err := svc.GetCandles(ctx, now, "CHRTTICKS", Resolution1m)
		if err != nil {
			t.Fatalf("GetCandles: %v", err)
		}
		if len(got.Events) != 2 {
			t.Fatalf("イベント件数 = %d, want 2（fire_tick=3 と resolved 済み fire_tick=60 のみ）", len(got.Events))
		}
		if got.Events[0].Type != EventTypeShock || got.Events[1].Type != EventTypeVolUp {
			t.Errorf("イベント種別 = [%s %s], want [shock vol_up]",
				got.Events[0].Type, got.Events[1].Type)
		}
		// fire_tick は epoch からの通算tickなので、時刻は epoch + fire_tick 分。
		wantFirst := epoch.Add(3 * time.Minute)
		if !got.Events[0].Time.Equal(wantFirst) {
			t.Errorf("イベント[0]の時刻 = %s, want %s", got.Events[0].Time, wantFirst)
		}
		wantSecond := epoch.Add(60 * time.Minute)
		if !got.Events[1].Time.Equal(wantSecond) {
			t.Errorf("イベント[1]の時刻 = %s, want %s", got.Events[1].Time, wantSecond)
		}
	})
}

// TestGetCandles_セッションが無ければ空 は、当日のセッション行がまだ無い（＝寄り付いて
// いない）場合に空の candles/events を返すことを確認する（未来のイベントが居る余地も無い）。
func TestGetCandles_セッションが無ければ空(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	epoch := time.Date(2099, 6, 20, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "CHRTEMPTY", 999202, epoch)
	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, c.ID); err != nil {
			t.Errorf("cleanup: delete price_ticks: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID); err != nil {
			t.Errorf("cleanup: delete currencies: %v", err)
		}
	})

	// セッション行を作らないまま、当日（2099-06-20）を指す now で呼ぶ。
	now := time.Date(2099, 6, 20, 12, 30, 0, 0, jst)
	svc := NewQuoteService(pool, RealClock{})
	got, err := svc.GetCandles(ctx, now, "CHRTEMPTY", Resolution1m)
	if err != nil {
		t.Fatalf("GetCandles: %v", err)
	}
	if len(got.Candles) != 0 {
		t.Errorf("candles = %d, want 0（セッション未寄り付きは空）", len(got.Candles))
	}
	if len(got.Events) != 0 {
		t.Errorf("events = %d, want 0", len(got.Events))
	}
}

// TestGetCandles_存在しない通貨はErrCurrencyNotFound を確認する。
func TestGetCandles_存在しない通貨はErrCurrencyNotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	svc := NewQuoteService(pool, RealClock{})
	if _, err := svc.GetCandles(ctx, time.Date(2099, 6, 20, 12, 0, 0, 0, jst), "NOSUCH", Resolution1m); err == nil {
		t.Fatal("GetCandles(存在しない通貨) = nil, want ErrCurrencyNotFound")
	} else if err != ErrCurrencyNotFound {
		t.Errorf("GetCandles(存在しない通貨) = %v, want ErrCurrencyNotFound", err)
	}
}
