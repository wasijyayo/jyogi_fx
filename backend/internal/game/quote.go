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

// sparklineLevels は design.md §6.4「スパークラインは▁▂▃▄▅▆▇█のUnicode文字を
// 並べるだけ」の8段階そのもの。
var sparklineLevels = [...]rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparklineTickCount は「直近8本のキャンドル」（design.md §6.4）。
const sparklineTickCount = 8

// PriceQuote は /price（#42 CMD-2）の表示情報。
type PriceQuote struct {
	Symbol string
	// CurrentPrice は今この瞬間の表示価格（TradeService.PlaceOrder等と同じ
	// CurrentPriceによるライブ計算。design.md §2.1）。
	CurrentPrice decimal.Decimal
	// ChangePercent は直近に保存されているprice_tickのcloseからの変化率(%)。
	// 保存済みtickが無ければ0。
	ChangePercent decimal.Decimal
	// Sparkline は直近8本のcloseを8段階に写像したもの（design.md §6.4）。
	// 保存済みtickが無ければ空文字。
	Sparkline string
}

// QuoteService は /price（#42 CMD-2）を担当する。
// CLAUDE.md §4「4つの入口はすべて同じサービス層を呼ぶ」の版の入口。
type QuoteService struct {
	pool *pgxpool.Pool
	// clock は現時点ではどのメソッドからも使われていない（Quoteは now を
	// 引数で受け取る。CLAUDE.md §5.1）。他のサービスと同じ理由で保持する。
	clock Clock
}

func NewQuoteService(pool *pgxpool.Pool, clock Clock) *QuoteService {
	return &QuoteService{pool: pool, clock: clock}
}

// Quote は通貨シンボルから現在価格・変動率・スパークラインを返す（/price）。
//
// 変化率の基準点はdesign.mdに明記が無かったため、「直近に保存されている
// price_tick（毎分tickが書き込む1分足）のclose」からの変化率とした
// （§6.4のティッカーが毎分編集され続けるのと同じ考え方を、コマンド実行時点の
// ライブ価格と直前の1分足との比較に読み替えたもの）。
func (s *QuoteService) Quote(ctx context.Context, now time.Time, symbol string) (PriceQuote, error) {
	q := db.New(s.pool)
	c, err := q.GetCurrencyBySymbol(ctx, symbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceQuote{}, ErrCurrencyNotFound
		}
		return PriceQuote{}, fmt.Errorf("get currency: %w", err)
	}

	events, err := q.ListEventsByCurrency(ctx, c.ID)
	if err != nil {
		return PriceQuote{}, fmt.Errorf("list events: %w", err)
	}
	tickIndex := elapsedTicks(c.EpochAt.Time, now)
	current := CurrentPrice(c, tickIndex, now, events)

	recent, err := q.ListRecentPriceTicks(ctx, db.ListRecentPriceTicksParams{
		CurrencyID: c.ID,
		Limit:      sparklineTickCount,
	})
	if err != nil {
		return PriceQuote{}, fmt.Errorf("list recent price ticks: %w", err)
	}

	quote := PriceQuote{Symbol: c.Symbol, CurrentPrice: current}
	if len(recent) == 0 {
		return quote, nil
	}

	// recentはtick_index降順（最新が先頭）。時系列順（古い→新しい）に並べ替える。
	closes := make([]decimal.Decimal, len(recent))
	for i, tick := range recent {
		closes[len(recent)-1-i] = tick.Close
	}

	previous := recent[0].Close
	if previous.IsPositive() {
		quote.ChangePercent = current.Sub(previous).Div(previous).Mul(decimal.NewFromInt(100))
	}
	quote.Sparkline = sparkline(closes)

	return quote, nil
}

// sparkline は design.md §6.4 のとおり、closeの列を8段階のUnicode文字に写像する
// 純粋関数。最小値をsparklineLevels[0]、最大値をsparklineLevels[len-1]に線形写像する。
// 全て同値（レンジ0）の場合は真ん中の段を並べる。
func sparkline(closes []decimal.Decimal) string {
	if len(closes) == 0 {
		return ""
	}

	min, max := closes[0], closes[0]
	for _, c := range closes {
		if c.LessThan(min) {
			min = c
		}
		if c.GreaterThan(max) {
			max = c
		}
	}

	rangeVal := max.Sub(min)
	runes := make([]rune, len(closes))
	for i, c := range closes {
		if rangeVal.IsZero() {
			runes[i] = sparklineLevels[len(sparklineLevels)/2]
			continue
		}
		// (c-min)/range を [0, len-1] の段に写像する。
		ratio := c.Sub(min).Div(rangeVal)
		level := int(ratio.Mul(decimal.NewFromInt(int64(len(sparklineLevels) - 1))).Round(0).IntPart())
		if level < 0 {
			level = 0
		}
		if level >= len(sparklineLevels) {
			level = len(sparklineLevels) - 1
		}
		runes[i] = sparklineLevels[level]
	}
	return string(runes)
}
