package game

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// setupLiquidationTestPosition は、指定した pressure 上書き後の実勢価格が想定通りに
// なるロングポジションを1つ作る（trade_integration_test.go の
// TestClosePosition_損益が計算され残高と圧力に反映される と同じ手法:
// 建玉直後にpressureを直接上書きし、self-impactに頼らず任意の含み損益を再現する）。
//
// 通貨は entry_price = 100（base_price）で建つよう epoch と同時刻で発注するため、
// 建玉直後の pressureOverride がそのまま「エントリー価格からの変化率」になる。
func setupLiquidationTestPosition(
	t *testing.T, ctx context.Context, pool *db.Queries, tradeSvc *TradeService,
	userID, symbol string, epoch time.Time, leverage, pressureOverride decimal.Decimal,
) db.Position {
	t.Helper()

	openResult, err := tradeSvc.PlaceOrder(ctx, epoch, PlaceOrderParams{
		UserID:         userID,
		CurrencySymbol: symbol,
		Side:           SideLong,
		Size:           decimal.NewFromInt(10),
		Leverage:       leverage,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	c, err := pool.GetCurrencyBySymbolForUpdate(ctx, symbol)
	if err != nil {
		t.Fatalf("GetCurrencyBySymbolForUpdate: %v", err)
	}
	if err := pool.UpdateCurrencyPressure(ctx, db.UpdateCurrencyPressureParams{
		ID: c.ID, Pressure: pressureOverride, PressureAt: pgtype.Timestamptz{Time: epoch, Valid: true},
	}); err != nil {
		t.Fatalf("UpdateCurrencyPressure: %v", err)
	}

	return openResult.Position
}

// TestLiquidateOpenPositions_清算基準を割ったポジションが強制決済される は #38 の完了条件
// 「セッション中に清算基準を割った建玉が強制決済されること」を確認する。
// レバレッジ10倍・価格が5.1%下落（清算距離5%の外側）という、ShouldLiquidateの
// 境界値テスト（liquidation_test.go）と対応する状況を実データで再現する。
func TestLiquidateOpenPositions_清算基準を割ったポジションが強制決済される(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "LIQFIRE", 999301, epoch)

	userID := "test-liquidation-fire-user"
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

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero)
	liquidationSvc := NewLiquidationService(pool, RealClock{}, tradeSvc)

	leverage := decimal.NewFromInt(10) // 清算距離5%（design.md §2.8/§5.2）
	position := setupLiquidationTestPosition(
		t, ctx, q, tradeSvc, userID, "LIQFIRE", epoch, leverage, decimal.NewFromFloat(-0.051))

	liquidated, err := liquidationSvc.LiquidateOpenPositions(ctx, epoch)
	if err != nil {
		t.Fatalf("LiquidateOpenPositions: %v", err)
	}

	if len(liquidated) != 1 || liquidated[0].Position.ID != position.ID {
		t.Fatalf("liquidated = %+v, want 1件（PositionID=%d）", liquidated, position.ID)
	}

	gotPosition, err := q.GetPositionForUpdate(ctx, db.GetPositionForUpdateParams{ID: position.ID, UserID: userID})
	if err != nil {
		t.Fatalf("GetPositionForUpdate: %v", err)
	}
	if !gotPosition.ClosedAt.Valid {
		t.Error("清算基準を割ったのに ClosedAt が設定されていない")
	}
	if !gotPosition.Pnl.Valid || !gotPosition.Pnl.Decimal.IsNegative() {
		t.Errorf("pnl = %+v, want 負の値（含み損での強制決済）", gotPosition.Pnl)
	}
}

// TestLiquidateOpenPositions_清算距離内のポジションは維持される は、境界のすぐ内側
// （4.9%下落。ShouldLiquidateの境界値テストと対応）では清算されないことを確認する。
// 「毎tickで再計算するが、基準を割るまでは何もしない」ことの裏取り。
func TestLiquidateOpenPositions_清算距離内のポジションは維持される(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "LIQSAFE", 999302, epoch)

	userID := "test-liquidation-safe-user"
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

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero)
	liquidationSvc := NewLiquidationService(pool, RealClock{}, tradeSvc)

	leverage := decimal.NewFromInt(10) // 清算距離5%（design.md §2.8/§5.2）
	position := setupLiquidationTestPosition(
		t, ctx, q, tradeSvc, userID, "LIQSAFE", epoch, leverage, decimal.NewFromFloat(-0.049))

	liquidated, err := liquidationSvc.LiquidateOpenPositions(ctx, epoch)
	if err != nil {
		t.Fatalf("LiquidateOpenPositions: %v", err)
	}
	if len(liquidated) != 0 {
		t.Errorf("liquidated = %+v, want 0件（清算距離の内側のはず）", liquidated)
	}

	gotPosition, err := q.GetPositionForUpdate(ctx, db.GetPositionForUpdateParams{ID: position.ID, UserID: userID})
	if err != nil {
		t.Fatalf("GetPositionForUpdate: %v", err)
	}
	if gotPosition.ClosedAt.Valid {
		t.Error("清算距離の内側なのに ClosedAt が設定され強制決済されてしまった")
	}
}

// TestTick_セッション外の時刻を注入しても清算は実行されない は #38 の完了条件
// 「セッション外の時刻を注入しても清算が走らないことをテストで確認」そのもの。
//
// 本来なら確実に清算される状況（清算距離を大きく超える含み損）を用意した上で、
// TickService.Tick にセッション外の時刻を渡し、ポジションが維持されたままであることを
// 確認する。design.md §7.1 B案「セッション外では判定しない。持ち越し建玉は翌日の
// 寄り付きでまとめて判定する」の裏取り。
func TestTick_セッション外の時刻を注入しても清算は実行されない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 1, 12, 0, 0, 0, jst) // JST 12:00 = セッション開始時刻
	c := insertTestCurrency(t, ctx, pool, "LIQOFFSESSION", 999303, epoch)

	userID := "test-liquidation-offsession-user"
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

	// 発注そのものはセッション判定に関わらず通す必要があるため、setup専用に
	// AlwaysOpen のセッションで PlaceOrder する（epoch自体はセッション開始時刻なので
	// 本来は不要だが、他のテストとの対称性のため明示する）。
	setupSessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{AlwaysOpen: true})
	setupTradeSvc := NewTradeService(pool, RealClock{}, setupSessionSvc, nil, decimal.Zero, decimal.Zero)

	leverage := decimal.NewFromInt(10)
	// 清算距離5%を大きく超える含み損（-50%）を用意し、「判定さえされれば確実に
	// 清算される」状況を作る。それでもセッション外なら維持されるはず、というのが
	// このテストの主眼。
	position := setupLiquidationTestPosition(
		t, ctx, q, setupTradeSvc, userID, "LIQOFFSESSION", epoch, leverage, decimal.NewFromFloat(-0.5))

	// 本番相当の非AlwaysOpenセッションでTickServiceを組み立てる。
	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc, nil, decimal.Zero, decimal.Zero)
	liquidationSvc := NewLiquidationService(pool, RealClock{}, tradeSvc)
	claimSvc := NewClaimService(pool, RealClock{}, ClaimConfig{
		BaseAmount:     decimal.NewFromInt(100),
		BuffMultiplier: decimal.NewFromFloat(1.5),
	})
	tickSvc := NewTickService(pool, RealClock{}, sessionSvc, liquidationSvc, claimSvc, nil, nil, nil)

	offSession := time.Date(2099, 4, 1, 15, 0, 0, 0, jst) // JST 15:00。セッション（12-13時）外。
	if err := tickSvc.Tick(ctx, offSession); err != nil {
		t.Fatalf("Tick(セッション外): %v", err)
	}

	gotPosition, err := q.GetPositionForUpdate(ctx, db.GetPositionForUpdateParams{ID: position.ID, UserID: userID})
	if err != nil {
		t.Fatalf("GetPositionForUpdate: %v", err)
	}
	if gotPosition.ClosedAt.Valid {
		t.Error("セッション外のTickでポジションが清算されてしまった（design.md §7.1 B案違反）")
	}
}
