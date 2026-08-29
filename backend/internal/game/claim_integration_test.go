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

// testClaimConfig は #39 の完了条件で確認した確定値と同じ配布パラメータ
// （基準額100・バフ倍率1.5）を使う（issue #39完了条件を参照）。
func testClaimConfig() ClaimConfig {
	return ClaimConfig{
		BaseAmount:     decimal.NewFromInt(100),
		BuffMultiplier: decimal.NewFromFloat(1.5),
	}
}

// setupClaimTestSession は claim系の統合テスト専用に、当日(date)分の
// game_sessions 行を1つ作る。opened_at/closed_at はJST 12-13時固定でよい
// （Claim/RecordMedianはどちらもopened_at/closed_atを見ず、dateだけを
// sessionDateJSTで引く）。
func setupClaimTestSession(t *testing.T, ctx context.Context, q *db.Queries, date time.Time) db.GameSession {
	t.Helper()

	jstDate := date.In(jst)
	dayStart := time.Date(jstDate.Year(), jstDate.Month(), jstDate.Day(), 0, 0, 0, 0, jst)
	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     pgtype.Date{Time: dayStart, Valid: true},
		Seed:     1,
		OpenedAt: pgtype.Timestamptz{Time: dayStart.Add(12 * time.Hour), Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: dayStart.Add(13 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}
	return session
}

// TestClaim_中央値以下のユーザーは1点5倍を受け取る は #39完了条件
// 「中央値以下のユーザーが1.5倍を受け取れること」を確認する。
//
// 他のテスト・過去のテスト実行の残留行によって users テーブルの中央値そのものは
// 変動しうるため、中央値の具体値には依存しない。代わりに残高を極端な値
// （0 と 10,000,000）にしたユーザーを2人用意することで、他にどんな行が
// 残っていてもこの2人の大小関係（貧しい方が中央値以下・裕福な方が中央値超）
// が覆らないようにしている。
func TestClaim_中央値以下のユーザーは1点5倍を受け取る(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	poorID := "test-claim-poor-user"
	richID := "test-claim-rich-user"
	for _, id := range []string{poorID, richID} {
		_, _ = pool.Exec(ctx, `DELETE FROM claims WHERE user_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, id)
	}
	setupTradeTestUser(t, ctx, q, poorID, decimal.Zero)
	setupTradeTestUser(t, ctx, q, richID, decimal.NewFromInt(10_000_000))

	now := time.Date(2099, 5, 1, 12, 30, 0, 0, jst)
	session := setupClaimTestSession(t, ctx, q, now)
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM claims WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		for _, id := range []string{poorID, richID} {
			_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, id)
		}
	})

	claimSvc := NewClaimService(pool, RealClock{}, testClaimConfig())
	if err := claimSvc.RecordMedian(ctx, session.ID, now); err != nil {
		t.Fatalf("RecordMedian: %v", err)
	}

	poorResult, err := claimSvc.Claim(ctx, now, poorID)
	if err != nil {
		t.Fatalf("Claim(poor): %v", err)
	}
	if !poorResult.Buffed {
		t.Errorf("poor user の Buffed = false, want true（残高0は必ず中央値以下のはず）")
	}
	wantPoorAmount := decimal.NewFromInt(150) // 100 × 1.5
	if !poorResult.Amount.Equal(wantPoorAmount) {
		t.Errorf("poor user の Amount = %s, want %s", poorResult.Amount, wantPoorAmount)
	}
	if !poorResult.NewBalance.Equal(wantPoorAmount) {
		t.Errorf("poor user の NewBalance = %s, want %s（残高0 + %s）", poorResult.NewBalance, wantPoorAmount, wantPoorAmount)
	}

	richResult, err := claimSvc.Claim(ctx, now, richID)
	if err != nil {
		t.Fatalf("Claim(rich): %v", err)
	}
	if richResult.Buffed {
		t.Errorf("rich user の Buffed = true, want false（残高1000万は必ず中央値超のはず）")
	}
	wantRichAmount := decimal.NewFromInt(100)
	if !richResult.Amount.Equal(wantRichAmount) {
		t.Errorf("rich user の Amount = %s, want %s", richResult.Amount, wantRichAmount)
	}

	// --- #39完了条件: 同一セッション内で2回claimできない ---
	if _, err := claimSvc.Claim(ctx, now, poorID); !errors.Is(err, ErrAlreadyClaimed) {
		t.Errorf("2回目のClaim(poor) = %v, want ErrAlreadyClaimed", err)
	}
	// 2回目の失敗で残高が変化していないことも確認する。
	gotUser, err := q.GetUserForUpdate(ctx, poorID)
	if err != nil {
		t.Fatalf("GetUserForUpdate(poor): %v", err)
	}
	if !gotUser.Balance.Equal(wantPoorAmount) {
		t.Errorf("2回目失敗後のDB上の残高 = %s, want %s（変化していないはず）", gotUser.Balance, wantPoorAmount)
	}
}

// TestClaim_セッションが寄り付いていなければ利用できない は、その日の
// game_sessions 行自体が無い場合の挙動を確認する。
func TestClaim_セッションが寄り付いていなければ利用できない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	// 2098-05-01 という他のテストと衝突しない架空の日付。game_sessions 行を作らない。
	now := time.Date(2098, 5, 1, 12, 30, 0, 0, jst)

	claimSvc := NewClaimService(pool, RealClock{}, testClaimConfig())
	if _, err := claimSvc.Claim(ctx, now, "test-claim-no-session-user"); !errors.Is(err, ErrClaimNotAvailable) {
		t.Errorf("Claim(session無し) = %v, want ErrClaimNotAvailable", err)
	}
}

// TestClaim_中央値未算出なら利用できない は、寄り付き処理の手順8
// （RecordMedian）がまだ走っていない game_sessions 行（claim_medianがNULL）
// に対する挙動を確認する。
func TestClaim_中央値未算出なら利用できない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	now := time.Date(2097, 5, 1, 12, 30, 0, 0, jst)
	session := setupClaimTestSession(t, ctx, q, now)
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
	})

	claimSvc := NewClaimService(pool, RealClock{}, testClaimConfig())
	if _, err := claimSvc.Claim(ctx, now, "test-claim-no-median-user"); !errors.Is(err, ErrClaimNotAvailable) {
		t.Errorf("Claim(median未算出) = %v, want ErrClaimNotAvailable", err)
	}
}

// TestTotalAssetsByUser_未決済ポジションの含み損益を含む は、残高だけでなく
// 未決済ポジションの含み損益も合算されること（design.md §2.7手順6「含み損益・
// equityを再計算」の考え方を総資産算出に反映したもの）を確認する。
func TestTotalAssetsByUser_未決済ポジションの含み損益を含む(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 6, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "CLAIMPNL", 999401, epoch)

	userID := "test-claim-pnl-user"
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

	sessionSvc := NewSessionService(pool, RealClock{}, SessionConfig{AlwaysOpen: true})
	tradeSvc := NewTradeService(pool, RealClock{}, sessionSvc)
	leverage := decimal.NewFromInt(10)
	// +10%含み益（liquidation_integration_test.goのsetupLiquidationTestPositionと
	// 同じ手法: entry_price=base_price=100固定・pressureOverrideで含み損益を再現）。
	position := setupLiquidationTestPosition(
		t, ctx, q, tradeSvc, userID, "CLAIMPNL", epoch, leverage, decimal.NewFromFloat(0.10))

	notional := position.Size.Mul(position.EntryPrice) // 10 × 100 = 1000
	fee := notional.Mul(c.FeeRate)
	margin := notional.Div(leverage)
	balanceAfterOrder := before.Sub(margin).Sub(fee)
	wantPnL := decimal.NewFromInt(100) // (110-100)×10
	wantTotal := balanceAfterOrder.Add(wantPnL)

	assets, err := TotalAssetsByUser(ctx, q, epoch)
	if err != nil {
		t.Fatalf("TotalAssetsByUser: %v", err)
	}
	got, ok := assets[userID]
	if !ok {
		t.Fatalf("TotalAssetsByUser に %s が含まれていない", userID)
	}
	if !got.Equal(wantTotal) {
		t.Errorf("TotalAssetsByUser[%s] = %s, want %s（残高%s + 含み益%s）",
			userID, got, wantTotal, balanceAfterOrder, wantPnL)
	}
}
