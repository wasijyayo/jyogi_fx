package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// Balance は /balance の結果（design.md §6.6「自分の資金」）。
// CLAUDE.md §4の例そのままの命名・形（Service.GetBalance）に合わせている。
type Balance struct {
	UserID      string
	DisplayName string
	Balance     decimal.Decimal
}

// Profile は /profile の結果（design.md §6.6「他人のプロフィール」）。
// 総資産・ランキング内の順位まで含める（「他人の戦績が見える」ことが
// issue #41の狙いのため、残高だけでなく含み損益込みの総資産・相対順位を見せる）。
type Profile struct {
	UserID      string
	DisplayName string
	Balance     decimal.Decimal
	TotalAssets decimal.Decimal
	Rank        int // 1始まり。RankOutOf 中の順位
	RankOutOf   int
}

// ProfileService は /balance・/profile（#41 CMD-1）を担当する。
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」の版の入口。
type ProfileService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（Balance/Profileは
	// now を引数で受け取る。CLAUDE.md §5.1）。他のサービスと同じ理由で保持する。
	clock   Clock
	ranking *RankingService
}

func NewProfileService(pool *pgxpool.Pool, clock Clock, ranking *RankingService) *ProfileService {
	return &ProfileService{pool: pool, clock: clock, ranking: ranking}
}

// Balance はユーザーの現在の残高（資金）を返す（/balance）。
// 未決済ポジションの含み損益は含まない「今すぐ動かせる資金」の額そのもの
// （総資産を見せる /profile・/rank とはあえて分けている。design.md §6.6の
// 「資金」と§7.7の「総資産」の使い分けに合わせた）。
func (s *ProfileService) Balance(ctx context.Context, userID string) (Balance, error) {
	q := db.New(s.pool)
	u, err := q.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Balance{}, ErrUserNotFound
		}
		return Balance{}, fmt.Errorf("get user: %w", err)
	}
	return Balance{UserID: u.DiscordID, DisplayName: u.DisplayName, Balance: u.Balance}, nil
}

// Profile はユーザーの残高・総資産・総資産ランキング内の順位を返す（/profile）。
func (s *ProfileService) Profile(ctx context.Context, now time.Time, userID string) (Profile, error) {
	q := db.New(s.pool)
	u, err := q.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Profile{}, ErrUserNotFound
		}
		return Profile{}, fmt.Errorf("get user: %w", err)
	}

	entries, err := s.ranking.RankByTotalAssets(ctx, now)
	if err != nil {
		return Profile{}, fmt.Errorf("rank by total assets: %w", err)
	}

	var totalAssets decimal.Decimal
	rank := 0
	for i, e := range entries {
		if e.UserID == userID {
			totalAssets = e.TotalAssets
			rank = i + 1
			break
		}
	}

	return Profile{
		UserID:      u.DiscordID,
		DisplayName: u.DisplayName,
		Balance:     u.Balance,
		TotalAssets: totalAssets,
		Rank:        rank,
		RankOutOf:   len(entries),
	}, nil
}
