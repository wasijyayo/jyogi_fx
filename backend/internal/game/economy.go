package game

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TotalAssetsByUser は全登録者の総資産（残高＋未決済ポジションの含み損益）を
// now 時点の価格で評価して返す（design.md §7.2「資産下位バフ」・§2.7 寄り付き
// 処理の手順8）。
//
// ClaimService が2箇所で使う:
//   - RecordMedian（寄り付き直後）: 全員分をまとめて中央値算出に使う
//   - Claim（呼び出された時点）: 呼び出したユーザー1人分を中央値と比較する
//
// LiquidationService と同じく「全通貨をループし、その内側で該当通貨の未決済
// ポジションだけを見る」形にする（CLAUDE.md §5.3: user×currencyのループにせず、
// 通貨が増えてもこの関数の呼び出し回数自体は変わらない設計を保つ）。
func TotalAssetsByUser(ctx context.Context, q *db.Queries, now time.Time) (map[string]decimal.Decimal, error) {
	users, err := q.ListAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all users: %w", err)
	}

	assets := make(map[string]decimal.Decimal, len(users))
	for _, u := range users {
		assets[u.DiscordID] = u.Balance
	}

	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}

	for _, c := range currencies {
		positions, err := q.ListOpenPositionsByCurrency(ctx, c.ID)
		if err != nil {
			return nil, fmt.Errorf("list open positions for %s: %w", c.Symbol, err)
		}
		if len(positions) == 0 {
			// BasePrice は経過tick数に比例したコストがかかる（pricing.goのコメント参照）。
			// 建玉が無い通貨のために毎回計算するのは無駄なのでスキップする
			// （liquidation.go の同じ判断を踏襲）。
			continue
		}

		tickIndex := elapsedTicks(c.EpochAt.Time, now)
		price := CurrentPrice(c, tickIndex, now)

		for _, p := range positions {
			assets[p.UserID] = assets[p.UserID].Add(PositionPnL(p, price))
		}
	}

	return assets, nil
}

// median は値の中央値を返す純粋関数。要素数が偶数なら中央2つの平均を取る。
// 空スライスに対しては decimal.Zero を返す（呼び出し側が「登録者0人」を
// 特別扱いする必要をなくすための安全側のフォールバック）。
func median(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}

	sorted := make([]decimal.Decimal, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].LessThan(sorted[j]) })

	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return sorted[mid-1].Add(sorted[mid]).Div(decimal.NewFromInt(2))
}
