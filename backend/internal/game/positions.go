package game

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// OpenPosition は /positions（#42 CMD-2）1行分。含み損益込みの表示情報。
type OpenPosition struct {
	Position       db.Position
	CurrencySymbol string
	CurrentPrice   decimal.Decimal
	UnrealizedPnL  decimal.Decimal
}

// ListOpenPositions はユーザーの未決済ポジション一覧を、現在価格での含み損益込みで
// 返す（/positions。design.md §6.6「保有ポジションと含み損益」）。
//
// 通貨は ListCurrencies で一括取得してマップ化する。ユーザー1人分の少数ポジション
// （最大でも通貨種類数と同程度）に対して通貨ごとの個別クエリを繰り返すより
// シンプルで、CLAUDE.md §5.3「全通貨ループ」の考え方（通貨が増えても呼び出し
// 回数を増やさない）にも沿う。
func (s *TradeService) ListOpenPositions(ctx context.Context, now time.Time, userID string) ([]OpenPosition, error) {
	q := db.New(s.pool)

	positions, err := q.ListOpenPositionsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list open positions: %w", err)
	}
	if len(positions) == 0 {
		return nil, nil
	}

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}
	byID := make(map[int64]db.Currency, len(currencies))
	for _, c := range currencies {
		byID[c.ID] = c
	}

	// 通貨ごとのevents取得を使い回すためのキャッシュ（ユーザーが同じ通貨に
	// 複数ポジションを持つ場合に同じクエリを繰り返さないため）。
	eventsByCurrency := make(map[int64][]db.Event, len(currencies))

	result := make([]OpenPosition, 0, len(positions))
	for _, p := range positions {
		c, ok := byID[p.CurrencyID]
		if !ok {
			// 通貨行が消えることは通常運用では起きないが、表示だけの機能で
			// 全体を失敗させる理由も無いため、このポジションだけスキップする。
			continue
		}

		events, ok := eventsByCurrency[c.ID]
		if !ok {
			events, err = q.ListEventsByCurrency(ctx, c.ID)
			if err != nil {
				return nil, fmt.Errorf("list events for %s: %w", c.Symbol, err)
			}
			eventsByCurrency[c.ID] = events
		}

		tickIndex := elapsedTicks(c.EpochAt.Time, now)
		price := CurrentPrice(c, tickIndex, now, events)

		result = append(result, OpenPosition{
			Position:       p,
			CurrencySymbol: c.Symbol,
			CurrentPrice:   price,
			UnrealizedPnL:  PositionPnL(p, price),
		})
	}
	return result, nil
}
