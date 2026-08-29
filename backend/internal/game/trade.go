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
	// （確定 #14/#48。design.md §7.10）。
	ErrNewOrdersClosed = errors.New("new orders are not accepted outside the trading window")
	// ErrUserNotFound は該当ユーザーが存在しない場合に返る。
	ErrUserNotFound = errors.New("user not found")
	// ErrCurrencyNotFound は該当通貨が存在しない場合に返る。
	ErrCurrencyNotFound = errors.New("currency not found")
	// ErrInsufficientBalance は必要証拠金+手数料に対して残高が不足している場合に返る。
	ErrInsufficientBalance = errors.New("insufficient balance for required margin and fee")
	// ErrPositionNotFound は該当ポジションが存在しない、または指定したユーザーの
	// ものでない場合に返る（他人のポジションIDの存在有無を漏らさないため、
	// この2つを区別しない。GetPositionForUpdate のコメント参照）。
	ErrPositionNotFound = errors.New("position not found")
	// ErrPositionAlreadyClosed はすでに決済済み（closed_at が設定済み）のポジションを
	// 再度決済しようとした場合に返る。
	ErrPositionAlreadyClosed = errors.New("position already closed")
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
	// 決済（#37）にはこの判定を使い回さないこと（design.md §7.10）。
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
	events, err := q.ListEventsByCurrency(ctx, currency.ID)
	if err != nil {
		return PlaceOrderResult{}, fmt.Errorf("list events: %w", err)
	}
	tickIndex := elapsedTicks(currency.EpochAt.Time, now)
	entryPrice := CurrentPrice(currency, tickIndex, now, events)

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
	// liquidity_drainイベント中は流動性が枯渇し、同じ取引量でも価格インパクトが
	// 約3.3倍（1/0.3）になる（design.md §5.4）。base(n)には触れず、ここで
	// UpdatePressureに渡すliquidityだけを差し替える。
	liquidity := currency.Liquidity.Mul(liquidityMultiplierAt(events, tickIndex))
	newPressure := UpdatePressure(currency, now, signedVolume, liquidity)
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

// ClosePositionParams は決済（クローズ）の入力。
type ClosePositionParams struct {
	UserID     string
	PositionID int64
}

// ClosePositionResult は決済成功時の結果。
type ClosePositionResult struct {
	Position   db.Position
	Trade      db.Trade
	NewBalance decimal.Decimal
}

// ClosePosition は既存ポジションを決済し、損益を確定する（#37 TRADE-2。
// design.md §7.3/§2.2/§7.10）。
//
// 手順:
//  1. ポジションをユーザーIDで絞ってロック取得する（他人のポジションは
//     ErrPositionNotFound。すでに決済済みなら ErrPositionAlreadyClosed）
//  2. ユーザー・通貨の行をロックして取得する（PlaceOrderと同じくユーザー→通貨の順。
//     デッドロック回避のため全呼び出しでこの順序を固定する）
//  3. 決済価格（今の表示価格）を算出する
//  4. side に応じた損益を計算する（long: (決済価格-建値)×size、short: (建値-決済価格)×size）
//  5. 手数料を計算する（建玉時と同じ片道0.05%。名目金額は決済価格基準。往復で計2回徴収される
//     ことになる。design.md §7.3）
//  6. 残高 += 証拠金（建玉時にロックした分）+ 損益 - 手数料
//  7. positions.closed_at/pnl を更新し、trades 行を作成する（反対売買として記録する。
//     side は建玉と逆にする）
//  8. 反対売買としての需給圧力インパクトを反映する（#33 UpdatePressure。design.md §2.2）
//
// セッション時間による制限はしない。既存ポジションの決済はセッション外・
// 終了1分前（12:59台）を含め常に許可する（確定#48。design.md §7.10）。
// PlaceOrder の IsNewOrderAllowed をここで使い回さないこと。
//
// 必ず1トランザクションで囲む（dev-guide.md §3）。
func (s *TradeService) ClosePosition(ctx context.Context, now time.Time, p ClosePositionParams) (ClosePositionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ClosePositionResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)

	// ポジション→ユーザー→通貨の順でロックを取得する。position は既存行のIDで
	// 一意に決まるためこの順序自体が循環を生むことはなく、user→currencyの部分は
	// PlaceOrderと同じ順序を保っている（デッドロック回避）。
	position, err := q.GetPositionForUpdate(ctx, db.GetPositionForUpdateParams{
		ID:     p.PositionID,
		UserID: p.UserID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClosePositionResult{}, ErrPositionNotFound
		}
		return ClosePositionResult{}, fmt.Errorf("get position: %w", err)
	}
	if position.ClosedAt.Valid {
		return ClosePositionResult{}, ErrPositionAlreadyClosed
	}

	user, err := q.GetUserForUpdate(ctx, p.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClosePositionResult{}, ErrUserNotFound
		}
		return ClosePositionResult{}, fmt.Errorf("get user: %w", err)
	}

	currency, err := q.GetCurrencyByIDForUpdate(ctx, position.CurrencyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClosePositionResult{}, ErrCurrencyNotFound
		}
		return ClosePositionResult{}, fmt.Errorf("get currency: %w", err)
	}

	// 決済価格 = この決済が入る直前の表示価格。エントリー時（PlaceOrder）と同じ考え方で、
	// この決済自身の需給圧力インパクトは含めない（手順8で別途反映する）。
	events, err := q.ListEventsByCurrency(ctx, currency.ID)
	if err != nil {
		return ClosePositionResult{}, fmt.Errorf("list events: %w", err)
	}
	tickIndex := elapsedTicks(currency.EpochAt.Time, now)
	exitPrice := CurrentPrice(currency, tickIndex, now, events)

	side := Side(position.Side)
	priceDiff := exitPrice.Sub(position.EntryPrice)
	if side == SideShort {
		priceDiff = priceDiff.Neg()
	}
	pnl := priceDiff.Mul(position.Size)

	// 建玉時にロックした証拠金 = size × entry_price / leverage（PlaceOrderの計算そのまま）。
	// 決済では損益とは独立にこの分を残高へ戻す。
	requiredMargin := position.Size.Mul(position.EntryPrice).Div(position.Leverage)

	// 手数料は決済価格基準の名目金額に対して片道0.05%（PlaceOrderと同じ計算。design.md §7.3）。
	notional := position.Size.Mul(exitPrice)
	fee := notional.Mul(currency.FeeRate)

	newBalance := user.Balance.Add(requiredMargin).Add(pnl).Sub(fee)
	if err := q.UpdateUserBalance(ctx, db.UpdateUserBalanceParams{
		DiscordID: user.DiscordID,
		Balance:   newBalance,
	}); err != nil {
		return ClosePositionResult{}, fmt.Errorf("update user balance: %w", err)
	}

	closedPosition, err := q.ClosePosition(ctx, db.ClosePositionParams{
		ID:       position.ID,
		ClosedAt: pgtype.Timestamptz{Time: now, Valid: true},
		Pnl:      decimal.NullDecimal{Decimal: pnl, Valid: true},
	})
	if err != nil {
		return ClosePositionResult{}, fmt.Errorf("close position: %w", err)
	}

	// 決済は反対売買として記録する（long建玉の決済 = 売り、short建玉の決済 = 買い。
	// issueの「決済も需給圧力を動かす（反対売買のため）」に対応）。
	closeSide := SideShort
	if side == SideShort {
		closeSide = SideLong
	}
	trade, err := q.CreateTrade(ctx, db.CreateTradeParams{
		UserID:     user.DiscordID,
		CurrencyID: currency.ID,
		PositionID: pgtype.Int8{Int64: position.ID, Valid: true},
		Side:       string(closeSide),
		Size:       position.Size,
		Price:      exitPrice,
		Fee:        fee,
		CreatedAt:  pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return ClosePositionResult{}, fmt.Errorf("create trade: %w", err)
	}

	// 反対売買としての需給圧力インパクトを反映する（design.md §2.2）。
	// 符号の向きはPlaceOrderと同じ規約（買い=正、売り=負）で、この決済自体の
	// side（closeSide）を基準にする。
	signedVolume := notional
	if closeSide == SideShort {
		signedVolume = notional.Neg()
	}
	// liquidity_drain中は流動性差し替え（PlaceOrderと同じ考え方。design.md §5.4）。
	liquidity := currency.Liquidity.Mul(liquidityMultiplierAt(events, tickIndex))
	newPressure := UpdatePressure(currency, now, signedVolume, liquidity)
	if err := q.UpdateCurrencyPressure(ctx, db.UpdateCurrencyPressureParams{
		ID:         currency.ID,
		Pressure:   newPressure,
		PressureAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		return ClosePositionResult{}, fmt.Errorf("update currency pressure: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ClosePositionResult{}, fmt.Errorf("commit tx: %w", err)
	}

	return ClosePositionResult{Position: closedPosition, Trade: trade, NewBalance: newBalance}, nil
}
