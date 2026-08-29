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

// TestQuote_変動率とスパークラインを算出する は #42完了条件の前提となる /price の
// 表示情報（現在価格・変動率・スパークライン）を確認する。
func TestQuote_変動率とスパークラインを算出する(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 8, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "QUOTETEST", 999601, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 8, 1, 0, 0, 0, 0, jst), Valid: true}
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
		_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
	})

	closes := []string{"90", "95", "100", "105", "110"}
	for i, closeStr := range closes {
		price := decimal.RequireFromString(closeStr)
		if err := q.UpsertPriceTick(ctx, db.UpsertPriceTickParams{
			CurrencyID: c.ID,
			SessionID:  session.ID,
			TickIndex:  int64(i + 1),
			TickedAt:   pgtype.Timestamptz{Time: epoch.Add(time.Duration(i+1) * time.Minute), Valid: true},
			BasePrice:  price,
			Pressure:   decimal.Zero,
			NetVolume:  decimal.Zero,
			Open:       price,
			High:       price,
			Low:        price,
			Close:      price,
			IsOpening:  i == 0,
		}); err != nil {
			t.Fatalf("UpsertPriceTick(tick=%d): %v", i+1, err)
		}
	}

	now := epoch.Add(5 * time.Minute) // tick_index = 5（保存済み最終tickと同じ時点）
	quoteSvc := NewQuoteService(pool, RealClock{})
	quote, err := quoteSvc.Quote(ctx, now, "QUOTETEST")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}

	gotCurrency, err := q.GetCurrencyBySymbol(ctx, "QUOTETEST")
	if err != nil {
		t.Fatalf("GetCurrencyBySymbol: %v", err)
	}
	wantCurrent := BasePrice(gotCurrency, elapsedTicks(gotCurrency.EpochAt.Time, now), nil)
	if !quote.CurrentPrice.Equal(wantCurrent) {
		t.Errorf("CurrentPrice = %s, want %s", quote.CurrentPrice, wantCurrent)
	}

	previous := decimal.RequireFromString("110") // 直近tickのclose
	wantChangePercent := wantCurrent.Sub(previous).Div(previous).Mul(decimal.NewFromInt(100))
	if !quote.ChangePercent.Equal(wantChangePercent) {
		t.Errorf("ChangePercent = %s, want %s", quote.ChangePercent, wantChangePercent)
	}

	wantSparkline := sparkline(closesFrom("90", "95", "100", "105", "110"))
	if quote.Sparkline != wantSparkline {
		t.Errorf("Sparkline = %q, want %q", quote.Sparkline, wantSparkline)
	}
}

// TestQuote_存在しない通貨はErrCurrencyNotFound を確認する。
func TestQuote_存在しない通貨(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	quoteSvc := NewQuoteService(pool, RealClock{})
	if _, err := quoteSvc.Quote(ctx, time.Now(), "NOSUCHCURRENCY"); !errors.Is(err, ErrCurrencyNotFound) {
		t.Errorf("Quote(存在しない通貨) = %v, want ErrCurrencyNotFound", err)
	}
}

// TestQuote_保存済みtickが無ければ変化率0でスパークライン空 を確認する
// （寄り付き前・新規通貨などのエッジケース）。
func TestQuote_保存済みtickが無ければ変化率0でスパークライン空(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	epoch := time.Date(2099, 8, 2, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "QUOTENOTICKS", 999602, epoch)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM currencies WHERE id = $1`, c.ID)
	})

	quoteSvc := NewQuoteService(pool, RealClock{})
	quote, err := quoteSvc.Quote(ctx, epoch, "QUOTENOTICKS")
	if err != nil {
		t.Fatalf("Quote: %v", err)
	}
	if !quote.ChangePercent.IsZero() {
		t.Errorf("ChangePercent = %s, want 0", quote.ChangePercent)
	}
	if quote.Sparkline != "" {
		t.Errorf("Sparkline = %q, want \"\"", quote.Sparkline)
	}
}
