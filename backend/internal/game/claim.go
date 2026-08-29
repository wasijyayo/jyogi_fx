package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

var (
	// ErrClaimNotAvailable は本日のセッションがまだ寄り付いていない
	// （game_sessions行が無い、または claim_median がまだ算出されていない）場合に返る。
	ErrClaimNotAvailable = errors.New("claim is not available yet for today's session")
	// ErrAlreadyClaimed は同一セッション内で既に claim 済みの場合に返る
	// （#39完了条件「同一セッション内で2回claimできないこと」）。
	ErrAlreadyClaimed = errors.New("already claimed for this session")
)

// ClaimConfig は /claim の配布額パラメータ（design.md §7.2）。
// デプロイなしで調整できるようにする（issue #39完了条件）ため、値そのものは
// このstructに詰めて呼び出し側（cmd/app/main.go）で環境変数から組み立てる。
// バフ倍率1.5倍は確定値（#15）だが、issue側が「バフ倍率もデプロイなしで
// 調整可能にする」としているため定数化せずここに含める。
type ClaimConfig struct {
	// BaseAmount は1回あたりの基準配布額（バフ適用前）。
	BaseAmount decimal.Decimal
	// BuffMultiplier は中央値以下のユーザーに掛ける倍率（確定値1.5。design.md §7.0）。
	BuffMultiplier decimal.Decimal
}

// ClaimResult は claim 成功時の結果。
type ClaimResult struct {
	Amount     decimal.Decimal
	Buffed     bool
	NewBalance decimal.Decimal
}

// ClaimService は /claim 資金配布（#39 ECON-1。design.md §7.2）を担当する。
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」のclaim版の入口。
//
// 初回ログイン時の初期資金1000（design.md §7.0）はここでは扱わない。
// UpsertUser（internal/game/auth.go）が呼ぶ users.balance の DEFAULT 列
// （000002_game_schema.up.sql のコメント参照）で既に付与済みのため、
// claim とは別の仕組みのまま整理する（issue #39のTODO項目への回答）。
type ClaimService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（Claim/RecordMedian は
	// now を引数で受け取る。CLAUDE.md §5.1）。他のサービスと同じ理由で保持する。
	clock Clock
	cfg   ClaimConfig
}

func NewClaimService(pool *pgxpool.Pool, clock Clock, cfg ClaimConfig) *ClaimService {
	return &ClaimService{pool: pool, clock: clock, cfg: cfg}
}

// RecordMedian は寄り付き処理の手順8（design.md §2.7）を実行する。
//
// **呼び出し元（TickService.Tick）が手順6〜7（持ち越し建玉の含み損益再評価・
// 清算判定＝LiquidationService.LiquidateOpenPositions）を終えた「後」に
// 呼ぶこと。** 清算前に呼ぶと、これから清算されるポジションの含み損が
// まだ乗った状態の総資産で中央値を計算してしまい、design.md の手順順序
// （6→7→8）と食い違う。
//
// 全登録者が0人の場合は中央値を確定できないため何もしない
// （claim_median は NULL のままとなり、その日は誰も claim できない。
// 実運用ではまず起きないが、median() の空スライス安全側フォールバックとは
// 別に、ここでは「保存しない」という形で明示的に区別する）。
func (s *ClaimService) RecordMedian(ctx context.Context, sessionID int64, now time.Time) error {
	q := db.New(s.pool)
	assets, err := TotalAssetsByUser(ctx, q, now)
	if err != nil {
		return fmt.Errorf("compute total assets: %w", err)
	}
	if len(assets) == 0 {
		return nil
	}

	values := make([]decimal.Decimal, 0, len(assets))
	for _, v := range assets {
		values = append(values, v)
	}

	return q.UpdateGameSessionClaimMedian(ctx, db.UpdateGameSessionClaimMedianParams{
		ID:          sessionID,
		ClaimMedian: decimal.NullDecimal{Decimal: median(values), Valid: true},
	})
}

// Claim はユーザーが /claim を実行した際の資金配布を行う（design.md §7.2）。
//
// 手順:
//  1. 本日のセッション行を取得する（無い、または claim_median 未算出なら
//     ErrClaimNotAvailable）
//  2. 呼び出したユーザーの「今」の総資産を算出し、セッション開始時点の中央値
//     （手順1で取得した claim_median）と比較してバフの要否を決める
//  3. ユーザー行をロックし、claims テーブルへの INSERT を試みる
//     （PRIMARY KEY (session_id, user_id) の一意制約で二重claimを防ぐ。
//     0行で返ってきたら ErrAlreadyClaimed）
//  4. 残高を配布額分だけ増やす
//
// セッション時間そのもの（IsSessionOpen）では制限しない。PlaceOrder（新規注文）
// とは異なり、claim は「その日の枠を使い切ったか」だけが本質であり、
// ClosePosition と同様セッション外でも呼べて構わない（design.md §7.10は新規注文
// にのみ適用され、claimには言及がない）。
//
// 必ず1トランザクションで囲む（dev-guide.md §3）。残高だけ増えてclaims行が
// 無い（＝何度でも claim できてしまう）状態を絶対に作らない。
func (s *ClaimService) Claim(ctx context.Context, now time.Time, userID string) (ClaimResult, error) {
	q := db.New(s.pool)
	session, err := q.GetGameSessionByDate(ctx, sessionDateJST(now))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ClaimResult{}, ErrClaimNotAvailable
	case err != nil:
		return ClaimResult{}, fmt.Errorf("get game session: %w", err)
	}
	if !session.ClaimMedian.Valid {
		return ClaimResult{}, ErrClaimNotAvailable
	}

	// 総資産は「今」の価格で評価する（TotalAssetsByUserのコメント参照）。
	// 比較対象の中央値は寄り付き時点で確定した値（session.ClaimMedian）を使う。
	assets, err := TotalAssetsByUser(ctx, q, now)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("compute total assets: %w", err)
	}
	userAssets, ok := assets[userID]
	if !ok {
		return ClaimResult{}, ErrUserNotFound
	}

	amount := s.cfg.BaseAmount
	buffed := userAssets.LessThanOrEqual(session.ClaimMedian.Decimal)
	if buffed {
		amount = amount.Mul(s.cfg.BuffMultiplier)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClaimResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	txq := db.New(tx)

	user, err := txq.GetUserForUpdate(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClaimResult{}, ErrUserNotFound
		}
		return ClaimResult{}, fmt.Errorf("get user: %w", err)
	}

	if _, err := txq.CreateClaim(ctx, db.CreateClaimParams{
		SessionID: session.ID,
		UserID:    userID,
		Amount:    amount,
		ClaimedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// ON CONFLICT (session_id, user_id) DO NOTHING で0行（claims.sqlのコメント参照）。
			return ClaimResult{}, ErrAlreadyClaimed
		}
		return ClaimResult{}, fmt.Errorf("create claim: %w", err)
	}

	newBalance := user.Balance.Add(amount)
	if err := txq.UpdateUserBalance(ctx, db.UpdateUserBalanceParams{
		DiscordID: user.DiscordID,
		Balance:   newBalance,
	}); err != nil {
		return ClaimResult{}, fmt.Errorf("update user balance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ClaimResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return ClaimResult{Amount: amount, Buffed: buffed, NewBalance: newBalance}, nil
}
