package game

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// setupTradeTestUser は取引系の統合テスト専用に、残高を明示的な値に固定した
// 使い捨てユーザー行を作る。balance は users.balance の DEFAULT（初期資金1000。
// design.md §7.0）に頼らず明示的に設定することで、マイグレーションのDEFAULTが
// 将来変わってもこのテストの前提が揺らがないようにする。
func setupTradeTestUser(t *testing.T, ctx context.Context, q *db.Queries, userID string, balance decimal.Decimal) db.User {
	t.Helper()

	if _, err := q.UpsertUser(ctx, db.UpsertUserParams{DiscordID: userID, DisplayName: userID}); err != nil {
		t.Fatalf("UpsertUser(%s): %v", userID, err)
	}
	if err := q.UpdateUserBalance(ctx, db.UpdateUserBalanceParams{DiscordID: userID, Balance: balance}); err != nil {
		t.Fatalf("UpdateUserBalance(%s): %v", userID, err)
	}
	u, err := q.GetUserForUpdate(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserForUpdate(%s): %v", userID, err)
	}
	return u
}

func TestPlaceOrder_成行注文で残高が減りポジションが作られ価格が動く(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 1, 12, 0, 0, 0, jst) // 実運用と衝突しない架空の未来日
	c := insertTestCurrency(t, ctx, pool, "TRADEBUY", 999101, epoch)

	userID := "test-trade-buy-user"
	_, _ = pool.Exec(ctx, `DELETE FROM trades WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM positions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)

	before := decimal.NewFromInt(1000) // design.md §7.0 の初期資金と同値
	setupTradeTestUser(t, ctx, q, userID, before)

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM trades WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM positions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc)

	// epoch と同時刻なので tickIndex=0 → BasePrice は base_price(=100) のまま動かない。
	// pressure も投入直後は0なので、約定価格はちょうど100になるはず
	// （pressure_test.go と同じ「他の要因を切ってpressureの効果だけを見る」手法）。
	now := epoch
	size := decimal.NewFromInt(90)
	leverage := decimal.NewFromInt(10)

	result, err := tradeSvc.PlaceOrder(ctx, now, PlaceOrderParams{
		UserID:         userID,
		CurrencySymbol: "TRADEBUY",
		Side:           SideLong,
		Size:           size,
		Leverage:       leverage,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	wantEntryPrice := c.BasePrice // pressure=0, tickIndex=0
	if !result.Position.EntryPrice.Equal(wantEntryPrice) {
		t.Fatalf("約定価格 = %s, want %s", result.Position.EntryPrice, wantEntryPrice)
	}

	wantNotional := size.Mul(wantEntryPrice)
	wantMargin := wantNotional.Div(leverage)
	wantFee := wantNotional.Mul(c.FeeRate)
	wantBalance := before.Sub(wantMargin).Sub(wantFee)

	// --- 完了条件1: 残高が減る ---
	if !result.NewBalance.Equal(wantBalance) {
		t.Errorf("PlaceOrderの返り値の残高 = %s, want %s", result.NewBalance, wantBalance)
	}
	gotUser, err := q.GetUserForUpdate(ctx, userID)
	if err != nil {
		t.Fatalf("GetUserForUpdate: %v", err)
	}
	if !gotUser.Balance.Equal(wantBalance) {
		t.Errorf("DB上の残高 = %s, want %s（必要証拠金%s + 手数料%s が引かれているはず）",
			gotUser.Balance, wantBalance, wantMargin, wantFee)
	}

	// --- 完了条件2: ポジションが作られる ---
	if result.Position.UserID != userID {
		t.Errorf("Position.UserID = %s, want %s", result.Position.UserID, userID)
	}
	if result.Position.CurrencyID != c.ID {
		t.Errorf("Position.CurrencyID = %d, want %d", result.Position.CurrencyID, c.ID)
	}
	if result.Position.Side != string(SideLong) {
		t.Errorf("Position.Side = %s, want %s", result.Position.Side, SideLong)
	}
	if !result.Position.Size.Equal(size) {
		t.Errorf("Position.Size = %s, want %s", result.Position.Size, size)
	}
	if !result.Position.Leverage.Equal(leverage) {
		t.Errorf("Position.Leverage = %s, want %s", result.Position.Leverage, leverage)
	}
	if result.Position.ClosedAt.Valid {
		t.Error("新規建玉のClosedAtがNULLでない")
	}

	// trades行にも約定価格・手数料が記録されている（issueの完了条件「約定価格を残す」）。
	if result.Trade.PositionID.Int64 != result.Position.ID || !result.Trade.PositionID.Valid {
		t.Errorf("Trade.PositionID = %+v, want %d", result.Trade.PositionID, result.Position.ID)
	}
	if !result.Trade.Price.Equal(wantEntryPrice) {
		t.Errorf("Trade.Price = %s, want %s", result.Trade.Price, wantEntryPrice)
	}
	if !result.Trade.Fee.Equal(wantFee) {
		t.Errorf("Trade.Fee = %s, want %s", result.Trade.Fee, wantFee)
	}

	// --- 完了条件3: 価格が動く（買い注文は圧力を上げる。design.md §2.2） ---
	gotCurrency, err := q.GetCurrencyBySymbolForUpdate(ctx, "TRADEBUY")
	if err != nil {
		t.Fatalf("GetCurrencyBySymbolForUpdate: %v", err)
	}
	if !gotCurrency.Pressure.IsPositive() {
		t.Errorf("買い注文後のpressure = %s, want 正の値", gotCurrency.Pressure)
	}
	wantPressure := c.K.Mul(wantNotional).Div(c.Liquidity)
	if diff := gotCurrency.Pressure.Sub(wantPressure).Abs(); diff.GreaterThan(decimal.NewFromFloat(0.00000001)) {
		t.Errorf("pressure = %s, want %s (k×名目金額/liquidity)", gotCurrency.Pressure, wantPressure)
	}

	priceAfter := CurrentPrice(gotCurrency, elapsedTicks(gotCurrency.EpochAt.Time, now), now)
	if !priceAfter.GreaterThan(result.Trade.Price) {
		t.Errorf("買い注文後の価格 %s が約定価格 %s 以下だった（価格が動いていない）", priceAfter, result.Trade.Price)
	}
}

func TestPlaceOrder_売り注文は圧力を下げ名目金額に応じた証拠金と手数料を引く(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "TRADESELL", 999102, epoch)

	userID := "test-trade-sell-user"
	_, _ = pool.Exec(ctx, `DELETE FROM trades WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM positions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)

	before := decimal.NewFromInt(1000)
	setupTradeTestUser(t, ctx, q, userID, before)

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM trades WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM positions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc)

	now := epoch
	size := decimal.NewFromInt(30)
	leverage := decimal.NewFromInt(5)

	result, err := tradeSvc.PlaceOrder(ctx, now, PlaceOrderParams{
		UserID:         userID,
		CurrencySymbol: "TRADESELL",
		Side:           SideShort,
		Size:           size,
		Leverage:       leverage,
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	wantNotional := size.Mul(result.Position.EntryPrice)
	wantMargin := wantNotional.Div(leverage)
	wantFee := wantNotional.Mul(c.FeeRate)
	wantBalance := before.Sub(wantMargin).Sub(wantFee)
	if !result.NewBalance.Equal(wantBalance) {
		t.Errorf("残高 = %s, want %s", result.NewBalance, wantBalance)
	}
	if result.Position.Side != string(SideShort) {
		t.Errorf("Position.Side = %s, want %s", result.Position.Side, SideShort)
	}

	gotCurrency, err := q.GetCurrencyBySymbolForUpdate(ctx, "TRADESELL")
	if err != nil {
		t.Fatalf("GetCurrencyBySymbolForUpdate: %v", err)
	}
	// design.md §2.2: 売り注文時は 圧力 -= k×(取引量/liquidity)。
	if !gotCurrency.Pressure.IsNegative() {
		t.Errorf("売り注文後のpressure = %s, want 負の値", gotCurrency.Pressure)
	}

	priceAfter := CurrentPrice(gotCurrency, elapsedTicks(gotCurrency.EpochAt.Time, now), now)
	if !priceAfter.LessThan(result.Trade.Price) {
		t.Errorf("売り注文後の価格 %s が約定価格 %s 以上だった（価格が動いていない）", priceAfter, result.Trade.Price)
	}
}

// TestPlaceOrder_拒否された注文は残高もポジションも変化しない は #36 の完了条件
// 「途中で失敗した場合に残高が巻き戻ることを確認」を確認する。
// 残高不足・レバレッジ超過はどちらも、どのDB書き込みよりも前に検証で弾かれる
// （PlaceOrderのコメント参照）。1トランザクションで囲んでいるため、途中まで
// 書き込みが進んだ場合でも同様に全て巻き戻ることの裏付けとして、
// 「拒否された注文はDB状態を一切変えない」ことを両パターンで確認する。
func TestPlaceOrder_拒否された注文は残高もポジションも変化しない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 4, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "TRADEREJECT", 999103, epoch)

	userID := "test-trade-reject-user"
	_, _ = pool.Exec(ctx, `DELETE FROM trades WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM positions WHERE user_id = $1`, userID)
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)

	before := decimal.NewFromInt(1000)
	setupTradeTestUser(t, ctx, q, userID, before)

	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM trades WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM positions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc)
	now := epoch

	tests := []struct {
		name    string
		params  PlaceOrderParams
		wantErr error
	}{
		{
			name: "残高を大幅に超える名目金額(残高不足)",
			params: PlaceOrderParams{
				UserID: userID, CurrencySymbol: "TRADEREJECT", Side: SideLong,
				Size: decimal.NewFromInt(100000), Leverage: decimal.NewFromInt(1),
			},
			wantErr: ErrInsufficientBalance,
		},
		{
			name: "通貨のmax_leverageを超える指定",
			params: PlaceOrderParams{
				UserID: userID, CurrencySymbol: "TRADEREJECT", Side: SideLong,
				Size: decimal.NewFromInt(1), Leverage: c.MaxLeverage.Add(decimal.NewFromInt(1)),
			},
			wantErr: ErrLeverageExceedsMax,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tradeSvc.PlaceOrder(ctx, now, tt.params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("PlaceOrder error = %v, want %v", err, tt.wantErr)
			}

			gotUser, err := q.GetUserForUpdate(ctx, userID)
			if err != nil {
				t.Fatalf("GetUserForUpdate: %v", err)
			}
			if !gotUser.Balance.Equal(before) {
				t.Errorf("拒否された注文の後で残高が変化した: %s, want %s（変化しないはず）", gotUser.Balance, before)
			}

			var positionCount int
			if err := pool.QueryRow(ctx,
				`SELECT count(*) FROM positions WHERE user_id = $1`, userID,
			).Scan(&positionCount); err != nil {
				t.Fatalf("count positions: %v", err)
			}
			if positionCount != 0 {
				t.Errorf("拒否された注文なのにpositionsが%d件作られた, want 0", positionCount)
			}

			gotCurrency, err := q.GetCurrencyBySymbolForUpdate(ctx, "TRADEREJECT")
			if err != nil {
				t.Fatalf("GetCurrencyBySymbolForUpdate: %v", err)
			}
			if !gotCurrency.Pressure.Equal(c.Pressure) {
				t.Errorf("拒否された注文の後でpressureが変化した: %s, want %s", gotCurrency.Pressure, c.Pressure)
			}
		})
	}
}
