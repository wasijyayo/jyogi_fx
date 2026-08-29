package game

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestListOpenPositions_含み損益込みで返す は #42完了条件の前提となる /positions の
// 表示情報（保有ポジションと含み損益）を確認する。
func TestListOpenPositions_含み損益込みで返す(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 9, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "POSLIST", 999701, epoch)

	userID := "test-positions-list-user"
	_, _ = pool.Exec(ctx, `DELETE FROM trades WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM positions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	setupTradeTestUser(t, ctx, q, userID, decimal.NewFromInt(1000))
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM trades WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM positions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{AlwaysOpen: true})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero)
	leverage := decimal.NewFromInt(10)
	// +10%含み益（liquidation_integration_test.goのsetupLiquidationTestPositionと同じ手法）。
	position := setupLiquidationTestPosition(
		t, ctx, q, tradeSvc, userID, "POSLIST", epoch, leverage, decimal.NewFromFloat(0.10))

	got, err := tradeSvc.ListOpenPositions(ctx, epoch, userID)
	if err != nil {
		t.Fatalf("ListOpenPositions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(ListOpenPositions) = %d, want 1", len(got))
	}

	entry := got[0]
	if entry.Position.ID != position.ID {
		t.Errorf("Position.ID = %d, want %d", entry.Position.ID, position.ID)
	}
	if entry.CurrencySymbol != "POSLIST" {
		t.Errorf("CurrencySymbol = %s, want POSLIST", entry.CurrencySymbol)
	}
	wantPrice := decimal.NewFromInt(110) // base_price=100 × (1+0.10)
	if !entry.CurrentPrice.Equal(wantPrice) {
		t.Errorf("CurrentPrice = %s, want %s", entry.CurrentPrice, wantPrice)
	}
	wantPnL := decimal.NewFromInt(100) // (110-100)×10
	if !entry.UnrealizedPnL.Equal(wantPnL) {
		t.Errorf("UnrealizedPnL = %s, want %s", entry.UnrealizedPnL, wantPnL)
	}
}

// TestListOpenPositions_ポジションが無ければ空 を確認する。
func TestListOpenPositions_ポジションが無ければ空(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	tradeSvc := NewTradeService(pool, RealClock{}, NewSessionService(pool, RealClock{}, SessionConfig{}), nil, decimal.Zero, decimal.Zero)
	got, err := tradeSvc.ListOpenPositions(ctx, time.Now(), "test-positions-nonexistent-user")
	if err != nil {
		t.Fatalf("ListOpenPositions: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(ListOpenPositions) = %d, want 0", len(got))
	}
}
