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

// ローソク足の種類（resolution）。openapi.yaml の enum（1m / 5m）と一致させる。
const (
	Resolution1m = "1m"
	Resolution5m = "5m"
)

// Candle はローソク足1本の表示情報。金額・価格は decimal で持ち、JSON に出す際は
// 文字列に変換する（CLAUDE.md §5.2）。
type Candle struct {
	Time      time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	// BasePrice は「人がいなかった場合の価格」（design.md §3.4 の比較線）。
	BasePrice decimal.Decimal
	// IsOpening は寄り付きキャンドルか（design.md §2.8）。
	IsOpening bool
}

// ChartEvent はチャートに注釈として重ねるイベント（design.md §3.1）。
// 発火済みのイベントだけを含める（未来のイベントは返さない。GetCandles 参照）。
type ChartEvent struct {
	Time      time.Time
	Type      string
	Magnitude decimal.Decimal
}

// CandlesResult は GET /api/currencies/{symbol}/candles の応答。
type CandlesResult struct {
	Symbol     string
	Resolution string
	Candles    []Candle
	Events     []ChartEvent
}

// GetCandles はその通貨の「当日セッション」のローソク足（1分足 or 5分足）と、
// 発火済みイベントを返す（design.md §2.8・§3.1・§8「過去チャートの取得」）。
//
// ログイン不要（公開。issue #45 決定事項）。
//
// 再現ロジックは書かない（design.md §8: price_ticks を SELECT するだけ）。
// 当日のセッション行がまだ無い（＝寄り付いていない）場合は空の candles/events を返す。
//
// イベント注釈は「すでに発火したもの」（resolved 済み、または fire_tick が現在の
// tick 以下）だけを返し、まだ起きていないイベントの存在・種類・時刻は返さない
// （events は寄り付き時に先に抽選され、fire_tick が未来の行もあるため）。
func (s *QuoteService) GetCandles(ctx context.Context, now time.Time, symbol, resolution string) (CandlesResult, error) {
	q := db.New(s.pool)
	c, err := q.GetCurrencyBySymbol(ctx, symbol)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CandlesResult{}, ErrCurrencyNotFound
		}
		return CandlesResult{}, fmt.Errorf("get currency: %w", err)
	}

	result := CandlesResult{
		Symbol:     c.Symbol,
		Resolution: resolution,
		Candles:    []Candle{},
		Events:     []ChartEvent{},
	}

	session, err := q.GetGameSessionByDate(ctx, sessionDateJST(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return CandlesResult{}, fmt.Errorf("get game session: %w", err)
	}

	candles, err := s.loadCandles(ctx, q, c.ID, session.ID, resolution)
	if err != nil {
		return CandlesResult{}, err
	}
	result.Candles = candles

	events, err := q.ListEventsBySession(ctx, session.ID)
	if err != nil {
		return CandlesResult{}, fmt.Errorf("list events by session: %w", err)
	}
	currentTick := elapsedTicks(c.EpochAt.Time, now)
	for _, e := range firedEvents(events, c.ID, currentTick) {
		result.Events = append(result.Events, ChartEvent{
			Time:      c.EpochAt.Time.Add(time.Duration(e.FireTick) * time.Minute),
			Type:      e.Type,
			Magnitude: e.Magnitude,
		})
	}

	return result, nil
}

// loadCandles は resolution に応じて price_ticks の生値（1分足）か、5分足への
// 集約クエリ（design.md §2.8）を引いて []Candle に変換する。
func (s *QuoteService) loadCandles(ctx context.Context, q *db.Queries, currencyID, sessionID int64, resolution string) ([]Candle, error) {
	switch resolution {
	case Resolution1m:
		ticks, err := q.ListPriceTicksBySession(ctx, db.ListPriceTicksBySessionParams{
			CurrencyID: currencyID,
			SessionID:  sessionID,
		})
		if err != nil {
			return nil, fmt.Errorf("list price ticks by session: %w", err)
		}
		candles := make([]Candle, 0, len(ticks))
		for _, t := range ticks {
			candles = append(candles, Candle{
				Time:      t.TickedAt.Time,
				Open:      t.Open,
				High:      t.High,
				Low:       t.Low,
				Close:     t.Close,
				BasePrice: t.BasePrice,
				IsOpening: t.IsOpening,
			})
		}
		return candles, nil

	case Resolution5m:
		rows, err := q.ListFiveMinCandlesBySession(ctx, db.ListFiveMinCandlesBySessionParams{
			CurrencyID: currencyID,
			SessionID:  sessionID,
		})
		if err != nil {
			return nil, fmt.Errorf("list five min candles by session: %w", err)
		}
		candles := make([]Candle, 0, len(rows))
		for _, r := range rows {
			candles = append(candles, Candle{
				Time:      r.Time.Time,
				Open:      r.Open,
				High:      r.High,
				Low:       r.Low,
				Close:     r.Close,
				BasePrice: r.BasePrice,
				IsOpening: r.IsOpening,
			})
		}
		return candles, nil

	default:
		return nil, fmt.Errorf("unknown resolution %q", resolution)
	}
}

// firedEvents は events のうち「その通貨に属し、かつ発火済み」の行だけを返す純粋関数。
//
// 発火済みの判定は「resolved 済み、または fire_tick が現在の tick（currentTick）以下」
// （issue #45 指示）。これにより、寄り付き時に先に抽選された未来のイベントの
// 存在・種類・時刻が API 応答に漏れない。
//
// fire_tick は「通貨の epoch_at からの通算tick番号」（グローバル座標。event.go の
// StoreSessionEvents 参照）なので、currentTick も elapsedTicks(epoch_at, now) で求める。
func firedEvents(events []db.Event, currencyID, currentTick int64) []db.Event {
	out := make([]db.Event, 0, len(events))
	for _, e := range events {
		if e.CurrencyID != currencyID {
			continue
		}
		if e.Resolved || e.FireTick <= currentTick {
			out = append(out, e)
		}
	}
	return out
}
