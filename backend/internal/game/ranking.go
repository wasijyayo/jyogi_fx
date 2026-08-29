package game

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// ErrNoSessionToday は本日のセッションがまだ寄り付いていない（RecordDailySnapshots
// がまだ走っていない）場合に /today ランキングが返すエラー（#41 CMD-1）。
var ErrNoSessionToday = errors.New("today's session has not opened yet")

// RankEntry は /rank（総資産ランキング）1行分。
type RankEntry struct {
	UserID      string
	DisplayName string
	TotalAssets decimal.Decimal
}

// TodayRankEntry は /today（当日の増減ランキング）1行分。
type TodayRankEntry struct {
	UserID        string
	DisplayName   string
	TotalAssets   decimal.Decimal
	ChangePercent decimal.Decimal // セッション開始時からの総資産変化率（%）
	// ChangeAmount はセッション開始時からの総資産変化額（絶対額）。
	// /todayコマンド自体には使わないが、日次まとめ（design.md §6.9、#44 NOTIFY-2）の
	// 「+142,300 (+38.2%)」のような絶対額表示に使う。
	ChangeAmount decimal.Decimal
}

// RankingService はランキング集計（#41 CMD-1）を担当する。
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」のランキング版の入口。
type RankingService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（RankByTotalAssets等は
	// now を引数で受け取る。CLAUDE.md §5.1）。他のサービスと同じ理由で保持する。
	clock Clock
}

func NewRankingService(pool *pgxpool.Pool, clock Clock) *RankingService {
	return &RankingService{pool: pool, clock: clock}
}

// RankByTotalAssets は全登録者を総資産（残高＋未決済ポジションの含み損益）降順で
// 並べたランキングを返す（/rank。design.md §6.6・§7.7の「総資産」軸）。
func (s *RankingService) RankByTotalAssets(ctx context.Context, now time.Time) ([]RankEntry, error) {
	q := db.New(s.pool)

	users, err := q.ListAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	assets := make(map[string]decimal.Decimal, len(users))
	for _, u := range users {
		assets[u.DiscordID] = u.Balance
	}
	if err := addOpenPositionPnL(ctx, q, now, assets); err != nil {
		return nil, fmt.Errorf("add open position pnl: %w", err)
	}

	entries := make([]RankEntry, 0, len(users))
	for _, u := range users {
		entries = append(entries, RankEntry{
			UserID:      u.DiscordID,
			DisplayName: u.DisplayName,
			TotalAssets: assets[u.DiscordID],
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TotalAssets.GreaterThan(entries[j].TotalAssets)
	})
	return entries, nil
}

// RankByTodayChange は本日のセッション開始時点からの総資産変化率（%）降順の
// ランキングを返す（/today。design.md §6.6・§7.7の「当日増減」軸）。
//
// design.md には算出式が書かれていなかったため確認のうえ「総資産の変化率」に
// 決定した（資産下位バフ §7.2 と同じく、絶対額ではなく率にすることで
// 新規参入者にも勝ち目を残す§7.7の狙いに合わせるため。ranking.goのコメント末尾参照）。
//
// 基準値は RecordDailySnapshots が寄り付き処理内で保存した
// daily_asset_snapshots を使う。セッション開始後に初回ログインしたなど
// 基準値が無いユーザー、または基準値が0以下（変化率が定義できない）だった
// ユーザーはランキングから除外する。
func (s *RankingService) RankByTodayChange(ctx context.Context, now time.Time) ([]TodayRankEntry, error) {
	q := db.New(s.pool)

	session, err := q.GetGameSessionByDate(ctx, sessionDateJST(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoSessionToday
	}
	if err != nil {
		return nil, fmt.Errorf("get game session: %w", err)
	}

	snapshots, err := q.ListDailyAssetSnapshotsBySession(ctx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("list daily asset snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		// 寄り付き処理は完了しているがRecordDailySnapshotsだけ未実行、という
		// 通常運用では起きないはずの状態。/claimのErrClaimNotAvailableと同じ発想で、
		// 「今はまだ使えない」として扱う。
		return nil, ErrNoSessionToday
	}
	baseline := make(map[string]decimal.Decimal, len(snapshots))
	for _, snap := range snapshots {
		baseline[snap.UserID] = snap.TotalAssets
	}

	users, err := q.ListAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}
	current := make(map[string]decimal.Decimal, len(users))
	for _, u := range users {
		current[u.DiscordID] = u.Balance
	}
	if err := addOpenPositionPnL(ctx, q, now, current); err != nil {
		return nil, fmt.Errorf("add open position pnl: %w", err)
	}

	entries := make([]TodayRankEntry, 0, len(users))
	for _, u := range users {
		base, ok := baseline[u.DiscordID]
		if !ok || !base.IsPositive() {
			// 基準値が無い、または0以下（変化率が定義できない）ユーザーは
			// このランキングの対象外とする。
			continue
		}
		cur := current[u.DiscordID]
		changeAmount := cur.Sub(base)
		changePercent := changeAmount.Div(base).Mul(decimal.NewFromInt(100))
		entries = append(entries, TodayRankEntry{
			UserID:        u.DiscordID,
			DisplayName:   u.DisplayName,
			TotalAssets:   cur,
			ChangePercent: changePercent,
			ChangeAmount:  changeAmount,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ChangePercent.GreaterThan(entries[j].ChangePercent)
	})
	return entries, nil
}

// LifetimePipsRankEntry は生涯獲得pipsランキング（/pips、#84）1行分。
type LifetimePipsRankEntry struct {
	UserID       string
	DisplayName  string
	LifetimePips decimal.Decimal
}

// RankByLifetimePips は全登録者を生涯累計pips（ネット。TradeService.ClosePositionが
// 決済のたびに積み上げる値。#84「人生の勝者」ロールと同じ値）降順で並べた
// ランキングを返す（design.md §7.7の軸候補に追加、ユーザーからの追加要望）。
//
// RankByTotalAssetsと違い未決済ポジションの含み損益は含めない（決済して確定した
// pipsのみを積み上げる値のため、nowを引数に取る必要が無い）。
func (s *RankingService) RankByLifetimePips(ctx context.Context) ([]LifetimePipsRankEntry, error) {
	q := db.New(s.pool)

	users, err := q.ListAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	entries := make([]LifetimePipsRankEntry, 0, len(users))
	for _, u := range users {
		entries = append(entries, LifetimePipsRankEntry{
			UserID:       u.DiscordID,
			DisplayName:  u.DisplayName,
			LifetimePips: u.LifetimePips,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LifetimePips.GreaterThan(entries[j].LifetimePips)
	})
	return entries, nil
}

// RecordDailySnapshots は /today の基準値（セッション開始時点の全登録者の総資産）を
// 保存する。design.md §2.7「寄り付き処理の順序」には明示されていないが、
// ClaimService.RecordMedian（#39）と同じく手順6〜7（含み損益再評価・清算判定）の
// 「後」に呼ぶ必要がある（清算前だと、これから清算されるポジションの含み損が
// まだ乗った状態の総資産を基準値にしてしまうため）。TickService.Tickから
// RecordMedianと並べて呼ぶ想定。
//
// 既にこのセッション分のスナップショットが1件でも存在するなら何もしない
// （寄り付き処理の再実行で基準値が上書きされないようにする。CLAUDE.md §5.5）。
func RecordDailySnapshots(ctx context.Context, q *db.Queries, sessionID int64, now time.Time) error {
	existing, err := q.ListDailyAssetSnapshotsBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("list daily asset snapshots: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}

	assets, err := TotalAssetsByUser(ctx, q, now)
	if err != nil {
		return fmt.Errorf("compute total assets: %w", err)
	}

	for userID, total := range assets {
		if _, err := q.CreateDailyAssetSnapshot(ctx, db.CreateDailyAssetSnapshotParams{
			SessionID:   sessionID,
			UserID:      userID,
			TotalAssets: total,
		}); err != nil {
			return fmt.Errorf("create daily asset snapshot (user=%s): %w", userID, err)
		}
	}
	return nil
}
