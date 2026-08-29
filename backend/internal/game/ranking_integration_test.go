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

// indexOf は entries の中から userID に一致する行のインデックスを返す（無ければ-1）。
// ランキング系の統合テストは、他のテスト・過去の実行の残留行によって全体の順位
// そのものは変動しうるため、「用意した数人の相対的な前後関係」だけを確認する
// （claim_integration_test.goのTestClaim_中央値以下のユーザーは1点5倍を受け取ると
// 同じ考え方）。
func indexOfRankEntry(entries []RankEntry, userID string) int {
	for i, e := range entries {
		if e.UserID == userID {
			return i
		}
	}
	return -1
}

func indexOfTodayRankEntry(entries []TodayRankEntry, userID string) int {
	for i, e := range entries {
		if e.UserID == userID {
			return i
		}
	}
	return -1
}

// TestRankByTotalAssets_総資産降順で並ぶ は #41完了条件（正しい値が返ること）の
// 核心である「並び順」を、他の残留行に依存しない極端な値の2人で確認する。
func TestRankByTotalAssets_総資産降順で並ぶ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	richID := "test-rank-rich-user"
	poorID := "test-rank-poor-user"
	for _, id := range []string{richID, poorID} {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, id)
	}
	setupTradeTestUser(t, ctx, q, richID, decimal.NewFromInt(10_000_000))
	setupTradeTestUser(t, ctx, q, poorID, decimal.Zero)
	t.Cleanup(func() {
		cctx := context.Background()
		for _, id := range []string{richID, poorID} {
			_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, id)
		}
	})

	svc := NewRankingService(pool, RealClock{})
	entries, err := svc.RankByTotalAssets(ctx, time.Now())
	if err != nil {
		t.Fatalf("RankByTotalAssets: %v", err)
	}

	richIdx := indexOfRankEntry(entries, richID)
	poorIdx := indexOfRankEntry(entries, poorID)
	if richIdx == -1 || poorIdx == -1 {
		t.Fatalf("結果にrichID/poorIDが含まれていない: richIdx=%d, poorIdx=%d", richIdx, poorIdx)
	}
	if richIdx >= poorIdx {
		t.Errorf("richUser(index=%d)がpoorUser(index=%d)より後ろにいる（降順になっていない）", richIdx, poorIdx)
	}
	if !entries[richIdx].TotalAssets.Equal(decimal.NewFromInt(10_000_000)) {
		t.Errorf("richUserのTotalAssets = %s, want 10000000", entries[richIdx].TotalAssets)
	}
}

// setupRankingTestSession は ranking 系の統合テスト専用に game_sessions 行を1つ作る
// （claim_integration_test.goのsetupClaimTestSessionと同じ手法）。
func setupRankingTestSession(t *testing.T, ctx context.Context, q *db.Queries, date time.Time) db.GameSession {
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

// TestRankByTodayChange_セッションが無ければエラー は、寄り付き前（当日分の
// daily_asset_snapshotsがまだ無い）状態でのエラー返却を確認する。
func TestRankByTodayChange_セッションが無ければエラー(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	now := time.Date(2096, 5, 1, 12, 30, 0, 0, jst) // 他のテストと衝突しない架空の日付
	svc := NewRankingService(pool, RealClock{})
	if _, err := svc.RankByTodayChange(ctx, now); !errors.Is(err, ErrNoSessionToday) {
		t.Errorf("RankByTodayChange(セッション無し) = %v, want ErrNoSessionToday", err)
	}
}

// TestRankByTodayChange_変化率降順で並ぶ は、資産が増えたユーザーと減ったユーザーの
// 相対順序（＋変化率の実際の値）を確認する。
func TestRankByTodayChange_変化率降順で並ぶ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	gainerID := "test-today-gainer-user"
	loserID := "test-today-loser-user"
	for _, id := range []string{gainerID, loserID} {
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, id)
	}
	// 基準値1000。gainerは2000（+100%）、loserは500（-50%）まで変化させる。
	setupTradeTestUser(t, ctx, q, gainerID, decimal.NewFromInt(2000))
	setupTradeTestUser(t, ctx, q, loserID, decimal.NewFromInt(500))

	now := time.Date(2095, 5, 1, 12, 30, 0, 0, jst)
	session := setupRankingTestSession(t, ctx, q, now)
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM daily_asset_snapshots WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		for _, id := range []string{gainerID, loserID} {
			_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, id)
		}
	})

	for _, e := range []struct {
		userID  string
		balance decimal.Decimal
	}{
		{gainerID, decimal.NewFromInt(1000)},
		{loserID, decimal.NewFromInt(1000)},
	} {
		if _, err := q.CreateDailyAssetSnapshot(ctx, db.CreateDailyAssetSnapshotParams{
			SessionID:   session.ID,
			UserID:      e.userID,
			TotalAssets: e.balance,
		}); err != nil {
			t.Fatalf("CreateDailyAssetSnapshot(%s): %v", e.userID, err)
		}
	}

	svc := NewRankingService(pool, RealClock{})
	entries, err := svc.RankByTodayChange(ctx, now)
	if err != nil {
		t.Fatalf("RankByTodayChange: %v", err)
	}

	gainerIdx := indexOfTodayRankEntry(entries, gainerID)
	loserIdx := indexOfTodayRankEntry(entries, loserID)
	if gainerIdx == -1 || loserIdx == -1 {
		t.Fatalf("結果にgainerID/loserIDが含まれていない: gainerIdx=%d, loserIdx=%d", gainerIdx, loserIdx)
	}
	if gainerIdx >= loserIdx {
		t.Errorf("gainer(index=%d)がloser(index=%d)より後ろにいる（降順になっていない）", gainerIdx, loserIdx)
	}

	wantGainerPercent := decimal.NewFromInt(100)
	if !entries[gainerIdx].ChangePercent.Equal(wantGainerPercent) {
		t.Errorf("gainerのChangePercent = %s, want %s", entries[gainerIdx].ChangePercent, wantGainerPercent)
	}
	wantLoserPercent := decimal.NewFromInt(-50)
	if !entries[loserIdx].ChangePercent.Equal(wantLoserPercent) {
		t.Errorf("loserのChangePercent = %s, want %s", entries[loserIdx].ChangePercent, wantLoserPercent)
	}
}

// TestRecordDailySnapshots_冪等 は、寄り付き処理の重複実行（Cloud Schedulerの
// リトライ。CLAUDE.md §5.5）に対する安全性を確認する。
func TestRecordDailySnapshots_冪等(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	userID := "test-snapshot-idempotent-user"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	setupTradeTestUser(t, ctx, q, userID, decimal.NewFromInt(1234))

	now := time.Date(2094, 5, 1, 12, 30, 0, 0, jst)
	session := setupRankingTestSession(t, ctx, q, now)
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM daily_asset_snapshots WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	if err := RecordDailySnapshots(ctx, q, session.ID, now); err != nil {
		t.Fatalf("RecordDailySnapshots (1回目): %v", err)
	}
	first, err := q.ListDailyAssetSnapshotsBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListDailyAssetSnapshotsBySession: %v", err)
	}

	// 1回目の後で残高を変えても、2回目のRecordDailySnapshotsは
	// 「既にあるなら何もしない」ため基準値は上書きされないはず。
	if err := q.UpdateUserBalance(ctx, db.UpdateUserBalanceParams{
		DiscordID: userID, Balance: decimal.NewFromInt(9999),
	}); err != nil {
		t.Fatalf("UpdateUserBalance: %v", err)
	}

	if err := RecordDailySnapshots(ctx, q, session.ID, now); err != nil {
		t.Fatalf("RecordDailySnapshots (2回目): %v", err)
	}
	second, err := q.ListDailyAssetSnapshotsBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListDailyAssetSnapshotsBySession (2回目後): %v", err)
	}

	if len(second) != len(first) {
		t.Errorf("件数が変わった: %d → %d", len(first), len(second))
	}
	for _, snap := range second {
		if snap.UserID != userID {
			continue
		}
		if !snap.TotalAssets.Equal(decimal.NewFromInt(1234)) {
			t.Errorf("2回目実行後のtotal_assets = %s, want 1234（上書きされてしまった）", snap.TotalAssets)
		}
	}
}
