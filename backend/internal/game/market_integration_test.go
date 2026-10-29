package game

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestGetCurrencySummary_前日比を算出する は #99 WEB-4 の通貨一覧・通貨ページ用の
// 表示情報（現在価格と前日比）を確認する。
//
// 前日比 = (現在価格 - 基準close) / 基準close × 100。基準close は「今日より前の
// 直近セッションの最後の price_tick の close」。
//
// 実通貨（JOG/WASI/CHEBU）もループする ListCurrencySummaries を架空の未来日で呼ぶと
// 実通貨の BasePrice が極端に遅くなる（AGENTS.md）ため、単一通貨だけを対象にする
// GetCurrencySummary で検証する。
func TestGetCurrencySummary_前日比を算出する(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 1, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "MKTPCT", 999801, epoch)

	// 前日（2099-01-01）のセッションと、その最終 close（100）を用意する。
	prevDate := pgtype.Date{Time: time.Date(2099, 1, 1, 0, 0, 0, 0, jst), Valid: true}
	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     prevDate,
		Seed:     1,
		OpenedAt: pgtype.Timestamptz{Time: epoch, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: epoch.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}

	prevClose := decimal.RequireFromString("100")
	if err := q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
		CurrencyID: c.ID,
		SessionID:  session.ID,
		TickIndex:  1,
		TickedAt:   pgtype.Timestamptz{Time: epoch.Add(time.Minute), Valid: true},
		BasePrice:  prevClose,
		Pressure:   decimal.Zero,
		NetVolume:  decimal.Zero,
		Open:       prevClose,
		High:       prevClose,
		Low:        prevClose,
		Close:      prevClose,
		IsOpening:  true,
	}); err != nil {
		t.Fatalf("UpsertPriceTick: %v", err)
	}

	// price_ticks → game_sessions → currencies の順で削除する（外部キー依存）。
	t.Cleanup(func() {
		cctx := context.Background()
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

	now := time.Date(2099, 1, 2, 12, 0, 0, 0, jst) // 「今日」＝前日の翌日
	svc := NewQuoteService(pool, RealClock{})
	got, err := svc.GetCurrencySummary(ctx, now, "MKTPCT")
	if err != nil {
		t.Fatalf("GetCurrencySummary: %v", err)
	}

	wantCurrent := CurrentPrice(c, elapsedTicks(c.EpochAt.Time, now), now, nil)
	if !got.CurrentPrice.Equal(wantCurrent) {
		t.Errorf("CurrentPrice = %s, want %s", got.CurrentPrice, wantCurrent)
	}
	if got.DayChangePercent == nil {
		t.Fatal("DayChangePercent = nil, want 前日比が入る")
	}
	wantPct := wantCurrent.Sub(prevClose).Div(prevClose).Mul(decimal.NewFromInt(100)).Round(2)
	if !got.DayChangePercent.Equal(wantPct) {
		t.Errorf("DayChangePercent = %s, want %s", got.DayChangePercent, wantPct)
	}
}

// TestGetCurrencySummary_前日セッションが無ければ前日比はnil は、基準が無い場合に
// 前日比が null（未定義）になることを確認する（#99 指示）。
func TestGetCurrencySummary_前日セッションが無ければ前日比はnil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	epoch := time.Date(2099, 1, 3, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "MKTPCTNONE", 999802, epoch)
	t.Cleanup(func() {
		cctx := context.Background()
		if _, err := pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, c.ID); err != nil {
			t.Errorf("cleanup: delete price_ticks: %v", err)
		}
		if _, err := pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID); err != nil {
			t.Errorf("cleanup: delete currencies: %v", err)
		}
	})

	svc := NewQuoteService(pool, RealClock{})
	got, err := svc.GetCurrencySummary(ctx, epoch, "MKTPCTNONE")
	if err != nil {
		t.Fatalf("GetCurrencySummary: %v", err)
	}
	if got.DayChangePercent != nil {
		t.Errorf("DayChangePercent = %s, want nil", *got.DayChangePercent)
	}
	// elapsedTicks = 0 なので現在価格は base_price（pressure 0）に一致する。
	if !got.CurrentPrice.Equal(c.BasePrice) {
		t.Errorf("CurrentPrice = %s, want %s", got.CurrentPrice, c.BasePrice)
	}
}

// TestGetCurrencySummary_存在しない通貨はErrCurrencyNotFound を確認する。
func TestGetCurrencySummary_存在しない通貨(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	svc := NewQuoteService(pool, RealClock{})
	if _, err := svc.GetCurrencySummary(ctx, time.Now(), "NOSUCHCURRENCY"); !errors.Is(err, ErrCurrencyNotFound) {
		t.Errorf("GetCurrencySummary(存在しない通貨) = %v, want ErrCurrencyNotFound", err)
	}
}
