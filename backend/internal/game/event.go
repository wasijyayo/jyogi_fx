package game

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/cespare/xxhash/v2"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// イベント種別（design.md §5.2）。events.type の値と一致させる。
const (
	EventTypeShock          = "shock"
	EventTypeVolUp          = "vol_up"
	EventTypeLiquidityDrain = "liquidity_drain"
)

const (
	// eventFireTickMin/Max はセッション内（相対）発火tickの抽選範囲（確定 #23。
	// design.md §5.1）。下限2は予兆（発火1tick前）を必ず出せるようにするため、
	// 上限55は発火後に最低5tick反応時間を残すため。
	eventFireTickMin = 2
	eventFireTickMax = 55

	// eventDurationMin/Max は vol_up・liquidity_drain の持続期間（tick）の抽選範囲。
	eventDurationMin = 3
	eventDurationMax = 5

	// volUpVolatilityMultiplier / liquidityDrainMultiplier は確定値（design.md §5.2）。
	volUpVolatilityMultiplier = 3.00
	liquidityDrainMultiplier  = 0.30

	// maxRerollAttempts は同一通貨・同一tick（持続期間重複含む）の衝突時に
	// 再抽選する上限回数（design.md §5.1）。1セッション最大3件・通貨3種×54tickの
	// 組み合わせ数に対して桁違いに大きく、実運用で使い切ることはまず無い安全弁。
	// 万一使い切った場合はその1件を諦める（drawEventsの呼び出し元は「その日は
	// 予定より少ないイベント数だった」として扱えばよく、エラーにする必要はない）。
	maxRerollAttempts = 1000
)

// drawnEvent は抽選結果1件分の中間表現（DB挿入前）。
//
// CurrencyIndex は呼び出し側が渡した []db.Currency のインデックスであり、
// 通貨名（symbol）にはこの抽選ロジックの中で一切依存しない（CLAUDE.md §5.3）。
// Direction は shock のみ意味を持つ（+1=上昇, -1=下落）。実際の magnitude
// （通貨ごとの shock_magnitude × Direction）は StoreSessionEvents が db.Currency
// を見て確定させる（この構造体は「どの通貨か」しか知らないため）。
type drawnEvent struct {
	CurrencyIndex    int
	Type             string
	RelativeFireTick int64 // セッション内tick番号（1〜60のうち2〜55）
	DurationTicks    int32
	Direction        int             // shockのみ使用（+1/-1）
	Magnitude        decimal.Decimal // vol_up/liquidity_drainのみ使用（確定値）
}

// occupiesRange は衝突判定用の占有tick範囲 [start, end) を返す。
// shock（duration=0）は「その1tickだけ」を占有するものとして扱う
// （design.md §5.1「同一通貨・同一tickに複数イベントが被った場合は再抽選」）。
func (e drawnEvent) occupiesRange() (start, end int64) {
	start = e.RelativeFireTick
	end = start + 1
	if e.DurationTicks > 0 {
		end = start + int64(e.DurationTicks)
	}
	return start, end
}

// lotteryU01 は (seed, salt) から [0,1) の一様乱数を返す純粋関数。
// zAt（pricing.go）と同じハッシュ手法（xxhash → 上位53bit）を、正規分布への
// Box-Muller変換はせず一様分布のまま使う（発生回数・種別・通貨・発火tickは
// いずれも離散選択で正規分布が不要なため）。
//
// game_sessions.seed だけに依存する純粋関数にすることで、寄り付き処理が
// 再実行されても同じ抽選結果になる（CLAUDE.md §5.5・§5.7と同じ理由。
// crypto/randのような非決定的な乱数源はここでは使わない）。
func lotteryU01(seed uint64, salt int64) float64 {
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], seed)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(salt))
	h := xxhash.Sum64(buf[:])
	return float64(h>>11) / float64(uint64(1)<<53)
}

// DrawSessionEvents はその日のイベントを抽選する純粋関数（確定 #23。design.md §5.1）。
// seed（=game_sessions.seed）と通貨数だけに依存し、同じ引数には常に同じ結果を返す。
//
// 呼び出し元（StoreSessionEvents）が currencies[drawnEvent.CurrencyIndex] で
// 実際の db.Currency を引き、shock_magnitude 等の通貨固有パラメータと
// セッションのグローバルtickオフセットを解決する。
func DrawSessionEvents(seed int64, numCurrencies int) []drawnEvent {
	if numCurrencies <= 0 {
		return nil
	}
	u := uint64(seed)

	n := drawEventCount(u)
	drawn := make([]drawnEvent, 0, n)

	for k := 0; k < n; k++ {
		for attempt := 0; attempt < maxRerollAttempts; attempt++ {
			// salt=0 は発生回数の抽選が専有しているため、+1 して重複を避ける。
			base := int64(k)*int64(maxRerollAttempts) + int64(attempt) + 1
			candidate := drawOneEvent(u, base, numCurrencies)
			if !conflictsWithAny(drawn, candidate) {
				drawn = append(drawn, candidate)
				break
			}
		}
	}
	return drawn
}

// drawEventCount は1日の発生回数を抽選する（確定分布: 1回=10%/2回=80%/3回=10%）。
func drawEventCount(seed uint64) int {
	u := lotteryU01(seed, 0)
	switch {
	case u < 0.10:
		return 1
	case u < 0.90:
		return 2
	default:
		return 3
	}
}

// drawOneEvent はイベント1件分の種別・対象通貨・発火tick・持続期間・方向を抽選する。
func drawOneEvent(seed uint64, base int64, numCurrencies int) drawnEvent {
	eventType := pickEventType(lotteryU01(seed, base*10+1))
	currencyIndex := pickIndex(lotteryU01(seed, base*10+2), numCurrencies)
	relativeFireTick := eventFireTickMin +
		pickIndex(lotteryU01(seed, base*10+3), eventFireTickMax-eventFireTickMin+1)

	e := drawnEvent{
		CurrencyIndex:    currencyIndex,
		Type:             eventType,
		RelativeFireTick: int64(relativeFireTick),
	}

	switch eventType {
	case EventTypeShock:
		// 方向は50/50（design.md §5.2: drift=0のため偏りを持たせる理由が無い）。
		if lotteryU01(seed, base*10+4) < 0.5 {
			e.Direction = 1
		} else {
			e.Direction = -1
		}
	case EventTypeVolUp:
		e.DurationTicks = drawDuration(seed, base*10+5)
		e.Magnitude = decimal.NewFromFloat(volUpVolatilityMultiplier)
	case EventTypeLiquidityDrain:
		e.DurationTicks = drawDuration(seed, base*10+5)
		e.Magnitude = decimal.NewFromFloat(liquidityDrainMultiplier)
	}
	return e
}

// pickEventType は shock/vol_up/liquidity_drain から一様に1つ選ぶ。
func pickEventType(u float64) string {
	types := [...]string{EventTypeShock, EventTypeVolUp, EventTypeLiquidityDrain}
	return types[pickIndex(u, len(types))]
}

// pickIndex は [0,1) の一様乱数 u を [0, n) の整数インデックスに写す。
// u はほぼ確実に1未満だが、浮動小数の丸めで n に達した場合に備えクランプする。
func pickIndex(u float64, n int) int {
	idx := int(u * float64(n))
	if idx >= n {
		idx = n - 1
	}
	return idx
}

// drawDuration は vol_up/liquidity_drain の持続期間（3〜5tick）を抽選する。
func drawDuration(seed uint64, salt int64) int32 {
	span := eventDurationMax - eventDurationMin + 1
	return int32(eventDurationMin + pickIndex(lotteryU01(seed, salt), span))
}

// conflictsWithAny は candidate が drawn 内の同一通貨イベントと
// 占有tick範囲で重なるかを返す（design.md §5.1「持続期間の重なり含む」）。
func conflictsWithAny(drawn []drawnEvent, candidate drawnEvent) bool {
	cStart, cEnd := candidate.occupiesRange()
	for _, e := range drawn {
		if e.CurrencyIndex != candidate.CurrencyIndex {
			continue
		}
		eStart, eEnd := e.occupiesRange()
		if cStart < eEnd && eStart < cEnd {
			return true
		}
	}
	return false
}

// StoreSessionEvents は DrawSessionEvents の結果をDBに書き込む（design.md §5.1
// 「セッション開始時にその日のイベントを抽選し尽くしてDBに書き込む」）。
// SessionService.OpenSession の寄り付き処理から1回呼ぶ想定。
//
// 既にこのセッション分のイベントが1件でも存在するなら何もしない
// （#40完了条件「tick処理が再抽選しないこと」。DrawSessionEvents自体はseedの
// 純粋関数で再実行しても同じ結果になるが、無駄な計算・INSERT試行を避ける）。
//
// events.fire_tick は price_ticks.tick_index と同じ「通貨のepoch_atからの
// 通算tick番号」（グローバル座標）で保存する。DrawSessionEvents が返す
// RelativeFireTick はセッション内の相対値（2〜55）なので、該当通貨の
// セッション開始時点のグローバルtick（elapsedTicks(currency.EpochAt, session.OpenedAt)）
// を足して変換する。通貨ごとにepoch_atが異なるため、この変換は通貨ごとに個別に行う
// （そうしないとBasePriceのΣ/Π計算で使う tickIndex と座標系が食い違ってしまう）。
func StoreSessionEvents(ctx context.Context, q *db.Queries, session db.GameSession, currencies []db.Currency) error {
	existing, err := q.ListEventsBySession(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("list events by session: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}

	for _, e := range DrawSessionEvents(session.Seed, len(currencies)) {
		c := currencies[e.CurrencyIndex]
		globalFireTick := elapsedTicks(c.EpochAt.Time, session.OpenedAt.Time) + e.RelativeFireTick

		magnitude := e.Magnitude
		if e.Type == EventTypeShock {
			magnitude = c.ShockMagnitude.Mul(decimal.NewFromInt(int64(e.Direction)))
		}

		if _, err := q.CreateEvent(ctx, db.CreateEventParams{
			SessionID:     session.ID,
			CurrencyID:    c.ID,
			Type:          e.Type,
			FireTick:      globalFireTick,
			DurationTicks: e.DurationTicks,
			Magnitude:     magnitude,
		}); err != nil {
			return fmt.Errorf("create event (currency=%s, fire_tick=%d): %w", c.Symbol, globalFireTick, err)
		}
	}
	return nil
}

// shockMultiplier は design.md §5.4 の Π 項を返す純粋関数:
// 発火済み（fire_tick <= tick）の shock イベントすべての (1+magnitude) の総乗。
// shockは瞬間的だが効果は恒久的（以後ずっとbase(n)に効き続ける）ため、
// 「今アクティブか」ではなく「発火済みかどうか」だけを見る。
//
// events は呼び出し側があらかじめ対象通貨1件分に絞り込んだものを渡すこと
// （ListEventsByCurrencyの結果をそのまま渡す想定）。
func shockMultiplier(events []db.Event, tick int64) decimal.Decimal {
	result := decimal.NewFromInt(1)
	for _, e := range events {
		if e.Type != EventTypeShock || e.FireTick > tick {
			continue
		}
		result = result.Mul(decimal.NewFromInt(1).Add(e.Magnitude))
	}
	return result
}

// volatilityMultiplierAt は tick 時点で有効な vol_up イベントの倍率を返す
// （無ければ1）。design.md §5.1 の再抽選ルールにより同一通貨で持続期間が重なる
// vol_up は存在しない前提のため、最初に見つかった1件を返せば十分。
//
// BasePrice のホットループ（tickIndex回の総和計算）から呼ばれるため float64 を返す
// （pricing.goのBasePriceコメント: 内部の総和計算はfloat64で行う。ここで
// decimal.Decimalを経由すると同じコストがtickIndex回積み重なってしまう）。
func volatilityMultiplierAt(events []db.Event, tick int64) float64 {
	for _, e := range events {
		if e.Type != EventTypeVolUp {
			continue
		}
		if tick >= e.FireTick && tick < e.FireTick+int64(e.DurationTicks) {
			return e.Magnitude.InexactFloat64()
		}
	}
	return 1
}

// liquidityMultiplierAt は tick 時点で有効な liquidity_drain イベントの倍率を返す
// （無ければ1）。design.md §5.4: liquidity_drain は base(n) には触れず、
// pressure更新側（UpdatePressureに渡すliquidity引数）を差し替えるためだけに使う
// （TradeService.PlaceOrder/ClosePosition）。呼び出し頻度は取引の都度でBasePriceの
// ホットループとは無関係なため decimal.Decimal のままでよい。
func liquidityMultiplierAt(events []db.Event, tick int64) decimal.Decimal {
	for _, e := range events {
		if e.Type != EventTypeLiquidityDrain {
			continue
		}
		if tick >= e.FireTick && tick < e.FireTick+int64(e.DurationTicks) {
			return e.Magnitude
		}
	}
	return decimal.NewFromInt(1)
}
