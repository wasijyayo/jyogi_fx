package game

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// CurrencySummary は通貨一覧・通貨ページ（#99 WEB-4）に出す表示情報。
// 金額・価格は decimal で持ち、JSON に出す際は文字列に変換する（CLAUDE.md §5.2）。
type CurrencySummary struct {
	Symbol       string
	Name         string
	CurrentPrice decimal.Decimal
	// DayChangePercent は前日比（%）。「今日より前の直近セッションの最後の
	// price_tick の close」を基準とし、基準が無ければ nil（未定義）を表す。
	DayChangePercent *decimal.Decimal
}

// ListCurrencySummaries は全通貨の現在価格と前日比を返す（GET /api/currencies）。
// 通貨は currencies テーブルの全行から作る（CLAUDE.md §5.3 ハードコードしない）。
func (s *QuoteService) ListCurrencySummaries(ctx context.Context, now time.Time) ([]CurrencySummary, error) {
	q := db.New(s.pool)
	currencies, err := q.ListCurrencies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list currencies: %w", err)
	}

	summaries := make([]CurrencySummary, 0, len(currencies))
	for _, c := range currencies {
		summary, err := s.summarize(ctx, q, now, c)
		if err != nil {
			return nil, fmt.Errorf("summarize %s: %w", c.Symbol, err)
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// GetCurrencySummary は通貨1件の現在価格と前日比を返す（GET /api/currencies/{symbol}）。
func (s *QuoteService) GetCurrencySummary(ctx context.Context, now time.Time, symbol string) (CurrencySummary, error) {
	q := db.New(s.pool)
	c, err := q.GetCurrencyBySymbol(ctx, symbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CurrencySummary{}, ErrCurrencyNotFound
		}
		return CurrencySummary{}, fmt.Errorf("get currency: %w", err)
	}
	return s.summarize(ctx, q, now, c)
}

// summarize は1通貨分の表示情報を組み立てる。List/Get で共通の内部処理
// （CLAUDE.md §4: 同じロジックを二重実装しない）。
//
// 前日比の定義（#99 でマネージャーと確定）:
//
//	dayChangePercent = (現在価格 - 基準close) / 基準close × 100
//	基準close = 今日（JST）より前の直近セッションの最後の price_tick の close
//
// 既存の QuoteService.Quote の ChangePercent は「直近 tick の close からの変化率」
// であり前日比ではないため、ここでは別の定義を新たに実装する。基準が無ければ
// nil（未定義）にする。
func (s *QuoteService) summarize(ctx context.Context, q *db.Queries, now time.Time, c db.Currency) (CurrencySummary, error) {
	events, err := q.ListEventsByCurrency(ctx, c.ID)
	if err != nil {
		return CurrencySummary{}, fmt.Errorf("list events: %w", err)
	}
	tickIndex := elapsedTicks(c.EpochAt.Time, now)
	current := CurrentPrice(c, tickIndex, now, events)

	summary := CurrencySummary{
		Symbol:       c.Symbol,
		Name:         c.Name,
		CurrentPrice: current,
	}

	prev, err := q.GetLastCloseBeforeDate(ctx, db.GetLastCloseBeforeDateParams{
		CurrencyID: c.ID,
		Date:       sessionDateJST(now),
	})
	switch {
	case err == nil:
		if prev.IsPositive() {
			pct := current.Sub(prev).Div(prev).Mul(decimal.NewFromInt(100))
			summary.DayChangePercent = &pct
		}
	case errors.Is(err, pgx.ErrNoRows):
		// 過去のセッションが無い（新規通貨など）。前日比は未定義のまま。
	default:
		return CurrencySummary{}, fmt.Errorf("get last close before date: %w", err)
	}

	return summary, nil
}
