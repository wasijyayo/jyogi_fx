package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// maintenanceMarginRatio は必要証拠金に対する維持証拠金の割合（design.md §7.4
// 「清算判定」で確定。維持証拠金率50%・ポジションごとに独立判定する分離マージン方式）。
//
// この値からレバレッジLの清算距離を逆算すると maintenanceMarginRatio/L となり、
// L=10 で 0.5/10 = 5.0%（design.md §7.4「清算距離の導出」の表と一致。
// ShouldLiquidate のコメントに式の詳細）。
var maintenanceMarginRatio = decimal.NewFromFloat(0.5)

// LiquidationService はロスカット（強制決済）判定を担当する（#38 TRADE-3。
// design.md §7.1「持ち越し可・セッション外は判定しない」・§7.9「約定は即時ではない」）。
//
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」に従い、決済処理自体は
// 二重実装せず #37 の TradeService.ClosePosition をそのまま呼び出す。
type LiquidationService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（LiquidateOpenPositions は
	// now を引数で受け取る。CLAUDE.md §5.1）。他のサービスと同じ理由で、将来 now を
	// 引数に取らない便利メソッドを追加する時のために保持する。
	clock Clock
	trade *TradeService
}

func NewLiquidationService(pool *pgxpool.Pool, clock Clock, trade *TradeService) *LiquidationService {
	return &LiquidationService{pool: pool, clock: clock, trade: trade}
}

// LiquidatedPosition は強制決済されたポジション1件の結果（#44 NOTIFY-2の
// ロスカット通知が、この結果を使って通知文を組み立てる想定）。
type LiquidatedPosition struct {
	Position db.Position
	Trade    db.Trade
}

// LiquidateOpenPositions は now 時点で清算基準を割っている未決済ポジションをすべて
// 強制決済する（design.md §4 tickの手順3「ロスカット判定」）。
//
// 呼び出し元（TickService）がセッション外は呼ばない前提のため、ここでは
// セッション判定を行わない（design.md §7.1 B案「セッション外は判定しない」）。
// 呼び出しタイミングは2箇所ある。どちらも同じこの関数を呼ぶだけでよい:
//
//   - 通常tick（セッション中、毎分）: pressureを反映した「今の」価格で判定する
//   - 寄り付きtick（SessionService.OpenSession 直後）: OpenSession が pressure を
//     0 にリセットした直後に呼ばれるため、結果的に寄り付き価格（base(n) そのもの）
//     で持ち越し建玉をまとめて判定することになる（design.md §2.7 寄り付き処理順序
//     6〜7・§7.1「翌日開始時にまとめて判定」）
//
// 通貨ごとに全件ループする（CLAUDE.md §5.3。通貨が増えてもtickの実行回数は変わらない）。
//
// 決済自体は #37 の ClosePosition を再利用するため、1ポジションごとに独立した
// トランザクションになる（全ポジションをまとめて1つの巨大トランザクションにはしない）。
// これにより、途中のポジションで失敗しても既に清算済みのポジションは closed_at が
// 立っているため、tick の再実行（Cloud Scheduler の重複実行・CLAUDE.md §5.5）で
// 二重決済されずに安全に再開できる。
func (s *LiquidationService) LiquidateOpenPositions(ctx context.Context, now time.Time) ([]LiquidatedPosition, error) {
	q := db.New(s.pool)
	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}

	var liquidated []LiquidatedPosition
	for _, c := range currencies {
		positions, err := q.ListOpenPositionsByCurrency(ctx, c.ID)
		if err != nil {
			return liquidated, fmt.Errorf("list open positions for %s: %w", c.Symbol, err)
		}
		if len(positions) == 0 {
			// BasePrice は経過tick数に比例したコストがかかる（pricing.goのコメント参照）。
			// 建玉が無い通貨のために毎tick計算するのは無駄なので、該当ポジションが
			// 無ければ価格計算自体をスキップする。
			continue
		}

		// 全ポジション共通の「今の」価格を1通貨につき1回だけ求める。この後の
		// ClosePosition 呼び出しで実際の決済価格が個別に再計算されるため
		// （反対売買のpressureインパクトで通貨内の後続ポジションの実勢価格は
		// 微妙にずれていく）、ここでの currentPrice は「このtickでどのポジションを
		// 清算対象とするか」を判定するためのスナップショットに過ぎない。
		events, err := q.ListEventsByCurrency(ctx, c.ID)
		if err != nil {
			return liquidated, fmt.Errorf("list events for %s: %w", c.Symbol, err)
		}
		tickIndex := elapsedTicks(c.EpochAt.Time, now)
		currentPrice := CurrentPrice(c, tickIndex, now, events)

		for _, p := range positions {
			if !ShouldLiquidate(p, currentPrice) {
				continue
			}

			result, err := s.trade.ClosePosition(ctx, now, ClosePositionParams{
				UserID:     p.UserID,
				PositionID: p.ID,
			})
			if err != nil {
				if errors.Is(err, ErrPositionAlreadyClosed) {
					// ユーザー自身の決済など、別経路ですでに閉じられていた
					// （tick の重複実行・ユーザーとの競合）。二重決済にはならず
					// 実害もないため、tick全体を失敗させずスキップする。
					continue
				}
				return liquidated, fmt.Errorf("liquidate position %d: %w", p.ID, err)
			}
			liquidated = append(liquidated, LiquidatedPosition{Position: result.Position, Trade: result.Trade})
		}
	}
	return liquidated, nil
}

// ShouldLiquidate は position が currentPrice の下で清算基準を割っているかを返す
// 純粋関数（design.md §7.4「清算判定」）。他の建玉・口座残高は見ない
// ポジションごとに独立の判定（分離マージン。§7.4で確定）。
//
// 必要証拠金（PlaceOrderで拘束した額。design.md §7.0）:
//
//	requiredMargin = size × entry_price / leverage
//
// 維持証拠金をその50%とし、含み損益を反映した
// equity（＝今決済したら手元に返る額）がこれを下回ったら清算する:
//
//	equity = requiredMargin + pnl
//	清算条件: equity <= requiredMargin × 0.5
//
// この式をレバレッジ L・清算距離（価格が何%逆行したら清算されるか）で書き直すと
// 0.5/L になる。L=10 なら 5.0%（design.md §7.4「清算距離の導出」の表と一致）。
func ShouldLiquidate(p db.Position, currentPrice decimal.Decimal) bool {
	requiredMargin := p.Size.Mul(p.EntryPrice).Div(p.Leverage)

	priceDiff := currentPrice.Sub(p.EntryPrice)
	if Side(p.Side) == SideShort {
		priceDiff = priceDiff.Neg()
	}
	pnl := priceDiff.Mul(p.Size)

	equity := requiredMargin.Add(pnl)
	maintenanceMargin := requiredMargin.Mul(maintenanceMarginRatio)

	return equity.LessThanOrEqual(maintenanceMargin)
}
