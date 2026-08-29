package game

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestProfileService_Balance は /balance が呼び出しユーザーの残高をそのまま
// 返すこと（design.md §6.6「自分の資金」）を確認する。
func TestProfileService_Balance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	userID := "test-profile-balance-user"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	setupTradeTestUser(t, ctx, q, userID, decimal.NewFromInt(4242))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	rankingSvc := NewRankingService(pool, RealClock{})
	profileSvc := NewProfileService(pool, RealClock{}, rankingSvc)

	got, err := profileSvc.Balance(ctx, userID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if !got.Balance.Equal(decimal.NewFromInt(4242)) {
		t.Errorf("Balance.Balance = %s, want 4242", got.Balance)
	}
	if got.UserID != userID {
		t.Errorf("Balance.UserID = %s, want %s", got.UserID, userID)
	}
}

// TestProfileService_Balance_未登録ユーザーはErrUserNotFound を確認する。
func TestProfileService_Balance_未登録ユーザー(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	rankingSvc := NewRankingService(pool, RealClock{})
	profileSvc := NewProfileService(pool, RealClock{}, rankingSvc)

	if _, err := profileSvc.Balance(ctx, "test-profile-nonexistent-user"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Balance(未登録) = %v, want ErrUserNotFound", err)
	}
}

// TestProfileService_Profile_最高額のユーザーは1位になる は、他の残留行に依存しない
// 極端な値で Rank/RankOutOf/TotalAssets の整合性を確認する
// （ranking_integration_test.goのTestRankByTotalAssetsと同じ考え方）。
func TestProfileService_Profile_最高額のユーザーは1位になる(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	userID := "test-profile-topuser"
	_, _ = pool.Exec(ctx, `DELETE FROM users WHERE discord_id = $1`, userID)
	setupTradeTestUser(t, ctx, q, userID, decimal.NewFromInt(999_999_999))
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE discord_id = $1`, userID)
	})

	var wantRankOutOf int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&wantRankOutOf); err != nil {
		t.Fatalf("count users: %v", err)
	}

	rankingSvc := NewRankingService(pool, RealClock{})
	profileSvc := NewProfileService(pool, RealClock{}, rankingSvc)

	got, err := profileSvc.Profile(ctx, time.Now(), userID)
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}
	if got.Rank != 1 {
		t.Errorf("Rank = %d, want 1（誰よりも総資産が多いはず）", got.Rank)
	}
	if got.RankOutOf != wantRankOutOf {
		t.Errorf("RankOutOf = %d, want %d", got.RankOutOf, wantRankOutOf)
	}
	if !got.TotalAssets.Equal(decimal.NewFromInt(999_999_999)) {
		t.Errorf("TotalAssets = %s, want 999999999", got.TotalAssets)
	}
	if !got.Balance.Equal(decimal.NewFromInt(999_999_999)) {
		t.Errorf("Balance = %s, want 999999999", got.Balance)
	}
}

// TestProfileService_Profile_未登録ユーザー はErrUserNotFoundを確認する。
func TestProfileService_Profile_未登録ユーザー(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	rankingSvc := NewRankingService(pool, RealClock{})
	profileSvc := NewProfileService(pool, RealClock{}, rankingSvc)

	if _, err := profileSvc.Profile(ctx, time.Now(), "test-profile-nonexistent-user-2"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Profile(未登録) = %v, want ErrUserNotFound", err)
	}
}
