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

// Side は注文・ポジションの売買方向。positions.side / trades.side の値と一致させる
// （design.md §8: side TEXT NOT NULL, -- long / short）。
// long = 買い注文、short = 売り注文（design.md §2.2）。
type Side string

const (
	SideLong  Side = "long"
	SideShort Side = "short"
)

var (
	// ErrInvalidSide は Side が long/short のどちらでもない場合に返る。
	ErrInvalidSide = errors.New("side must be \"long\" or \"short\"")
	// ErrInvalidSize は size が0以下の場合に返る。
	ErrInvalidSize = errors.New("size must be positive")
	// ErrInvalidLeverage は leverage が0以下の場合に返る。
	ErrInvalidLeverage = errors.New("leverage must be positive")
	// ErrLeverageExceedsMax は指定レバレッジが通貨の max_leverage を超える場合に返る
	// （design.md §7.0「レバレッジ上限: 10倍」・§2.9 通貨別上書き）。
	ErrLeverageExceedsMax = errors.New("leverage exceeds currency max_leverage")
	// ErrNewOrdersClosed はセッション外・終了1分前で新規注文が拒否される場合に返る
	// （確定 #14/#48。design.md §7.9）。
	ErrNewOrdersClosed = errors.New("new orders are not accepted outside the trading window")
	// ErrUserNotFound は該当ユーザーが存在しない場合に返る。
	ErrUserNotFound = errors.New("user not found")
	// ErrCurrencyNotFound は該当通貨が存在しない場合に返る。
	ErrCurrencyNotFound = errors.New("currency not found")
	// ErrInsufficientBalance は必要証拠金+手数料に対して残高が不足している場合に返る。
	ErrInsufficientBalance = errors.New("insufficient balance for required margin and fee")
)

// PlaceOrderParams は成行注文（新規建玉）の入力。
type PlaceOrderParams struct {
	UserID         string
	CurrencySymbol string
	Side           Side
	// Size は建玉数量（通貨単位。例: "JOGを10単位買う"なら10）。
	// 想定名目金額（＝需給圧力・証拠金・手数料の計算に使う額）は Size × 約定価格。
	Size decimal.Decimal
	// Leverage はこの注文に適用するレバレッジ。currency.max_leverage 以下でなければならない。
	Leverage decimal.Decimal
}

// PlaceOrderResult は成行注文成功時の結果。
type PlaceOrderResult struct {
	Position   db.Position
	Trade      db.Trade
	NewBalance decimal.Decimal
}

// TradeService は売買処理（#36 TRADE-1〜）を担当する。
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」の取引版の入口。
type TradeService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（PlaceOrder は now を
	// 引数で受け取る。CLAUDE.md §5.1）。SessionService/TickService と同じ理由で、
	// 将来 now を引数に取らない便利メソッドを追加する時のために保持する。
	clock   Clock
	session *SessionService
}

func NewTradeService(pool *pgxpool.Pool, clock Clock, session *SessionService) *TradeService {
	return &TradeService{pool: pool, clock: clock, session: session}
}

// PlaceOrder は成行注文で新規建玉を作成する（#36 TRADE-1。design.md §7.0/§7.3/§2.2）。
//
// 手順:
//  1. セッション時間・入力値を検証する（DBに触る前に弾けるものは弾く）
//  2. ユーザー・通貨の行をロックして取得する（同時注文によるlost updateを防ぐ。
//     常にユーザー→通貨の順でロックし、デッドロックを避ける）
//  3. レバレッジ上限を検証する
//  4. 約定価格（ロック取得前の需給圧力を反映した「今の」価格）を算出する
//  5. 名目金額 = Size × 約定価格 から必要証拠金・手数料を計算し、残高を検証する
//  6. 残高から 必要証拠金+手数料 を減算する（手数料はインフレのシンクとして消滅させる。
//     design.md §7.3。証拠金は決済時（#37）まで残高に戻らない）
//  7. positions 行・trades 行を作成する
//  8. この注文自身の需給圧力インパクトを反映する（#33 UpdatePressure。design.md §2.2）
//
// 必ず1トランザクションで囲む（dev-guide.md §3）。残高だけ減ってポジションが
// 作られない、という状態を絶対に作らない。
func (s *TradeService) PlaceOrder(ctx context.Context, now time.Time, p PlaceOrderParams) (PlaceOrderResult, error) {
	if p.Side != SideLong && p.Side != SideShort {
		return PlaceOrderResult{}, ErrInvalidSide
	}
	if !p.Size.IsPositive() {
		return PlaceOrderResult{}, ErrInvalidSize
	}
	if !p.Leverage.IsPositive() {
		return PlaceOrderResult{}, ErrInvalidLeverage
	}
	// セッション外・終了1分前は新規注文を拒否する（確定#14/#48。#34の判定を使う）。
	// 決済（#37）にはこの判定を使い回さないこと（design.md §7.9）。
	if !s.session.IsNewOrderAllowed(now) {
		return PlaceOrderResult{}, ErrNewOrdersClosed
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlaceOrderResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	// ユーザー→通貨の順で行ロックを取得する。全呼び出しでこの順序を固定することで、
	// 「AがユーザーX→通貨Yの順で待ち、BがユーザーY→通貨Xの順で待つ」ようなデッドロックを防ぐ。
	user, err := q.GetUserForUpdate(ctx, p.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlaceOrderResult{}, ErrUserNotFound
		}
		return PlaceOrderResult{}, fmt.Errorf("get user: %w", err)
	}

	currency, err := q.GetCurrencyBySymbolForUpdate(ctx, p.CurrencySymbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlaceOrderResult{}, ErrCurrencyNotFound
		}
		return PlaceOrderResult{}, fmt.Errorf("get currency: %w", err)
	}

	if p.Leverage.GreaterThan(currency.MaxLeverage) {
		return PlaceOrderResult{}, ErrLeverageExceedsMax
	}

	// 約定価格 = この注文が入る直前の表示価格（design.md §2.1）。
	// 自分自身の注文による圧力インパクトは、約定後に別途反映する（手順8）ため
	// ここでは含めない（＝自分の注文で自分の約定価格が動くことはない）。
	tickIndex := elapsedTicks(currency.EpochAt.Time, now)
	entryPrice := CurrentPrice(currency, tickIndex, now)

	// 名目金額（＝この注文が動かす経済的な大きさ）= 数量 × 約定価格。
	// 必要証拠金・手数料・需給圧力インパクト（手順8）は全てこれを基準に計算する。
	notional := p.Size.Mul(entryPrice)
	requiredMargin := notional.Div(p.Leverage)
	fee := notional.Mul(currency.FeeRate)
	totalCost := requiredMargin.Add(fee)

	if user.Balance.LessThan(totalCost) {
		return PlaceOrderResult{}, ErrInsufficientBalance
	}

	newBalance := user.Balance.Sub(totalCost)
	if err := q.UpdateUserBalance(ctx, db.UpdateUserBalanceParams{
		DiscordID: user.DiscordID,
		Balance:   newBalance,
	}); err != nil {
		return PlaceOrderResult{}, fmt.Errorf("update user balance: %w", err)
	}

	position, err := q.CreatePosition(ctx, db.CreatePositionParams{
		UserID:     user.DiscordID,
		CurrencyID: currency.ID,
		Side:       string(p.Side),
		Size:       p.Size,
		EntryPrice: entryPrice,
		Leverage:   p.Leverage,
		OpenedAt:   pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return PlaceOrderResult{}, fmt.Errorf("create position: %w", err)
	}

	trade, err := q.CreateTrade(ctx, db.CreateTradeParams{
		UserID:     user.DiscordID,
		CurrencyID: currency.ID,
		PositionID: pgtype.Int8{Int64: position.ID, Valid: true},
		Side:       string(p.Side),
		Size:       p.Size,
		Price:      entryPrice,
		Fee:        fee,
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return PlaceOrderResult{}, fmt.Errorf("create trade: %w", err)
	}

	// この注文自身の需給圧力インパクトを反映する（design.md §2.2）。
	// 買い注文は正、売り注文は負の符号付き名目金額として渡す
	// （UpdatePressure の呼び出し規約。pressure.go の #33 コメント参照）。
	signedVolume := notional
	if p.Side == SideShort {
		signedVolume = notional.Neg()
	}
	newPressure := UpdatePressure(currency, now, signedVolume, currency.Liquidity)
	if err := q.UpdateCurrencyPressure(ctx, db.UpdateCurrencyPressureParams{
		ID:         currency.ID,
		Pressure:   newPressure,
		PressureAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		return PlaceOrderResult{}, fmt.Errorf("update currency pressure: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return PlaceOrderResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return PlaceOrderResult{Position: position, Trade: trade, NewBalance: newBalance}, nil
}
