package game

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestDrawSessionEvents_同じseedなら同じ結果 は #40 完了条件
// 「イベント込みでもbase(n)が純粋関数のままであること」の前提となる、
// 抽選そのものの純粋性（CLAUDE.md §5.7）を確認する。
func TestDrawSessionEvents_同じseedなら同じ結果(t *testing.T) {
	for _, seed := range []int64{0, 1, 42, 123456789, -1} {
		a := DrawSessionEvents(seed, 3)
		b := DrawSessionEvents(seed, 3)
		if len(a) != len(b) {
			t.Fatalf("seed=%d: 件数が呼び出しごとに変わった (%d, %d)", seed, len(a), len(b))
		}
		for i := range a {
			if !drawnEventsEqual(a[i], b[i]) {
				t.Errorf("seed=%d: [%d]件目が呼び出しごとに変わった: %+v, %+v", seed, i, a[i], b[i])
			}
		}
	}
}

// TestDrawSessionEvents_発生回数は1から3の範囲 は確定分布（1回=10%/2回=80%/3回=10%。
// design.md §5.1）のうち「範囲」を大量のseedで確認する。分布の厳密な比率までは
// 統計的揺らぎがあるため、広めの許容範囲で「概ねそれらしい」ことだけ見る。
func TestDrawSessionEvents_発生回数は1から3の範囲(t *testing.T) {
	counts := map[int]int{}
	const trials = 10000
	for seed := int64(0); seed < trials; seed++ {
		n := len(DrawSessionEvents(seed, 3))
		if n < 1 || n > 3 {
			t.Fatalf("seed=%d: 発生回数 = %d, want 1〜3", seed, n)
		}
		counts[n]++
	}

	// 期待値: 1回≈10%, 2回≈80%, 3回≈10%。10000試行なら±3%も見れば十分安全。
	wantRatio := map[int]float64{1: 0.10, 2: 0.80, 3: 0.10}
	for n, want := range wantRatio {
		got := float64(counts[n]) / float64(trials)
		if diff := got - want; diff < -0.03 || diff > 0.03 {
			t.Errorf("発生回数%d回の割合 = %.3f, want ≈%.2f (±0.03)", n, got, want)
		}
	}
}

// TestDrawSessionEvents_同一通貨内で占有tick範囲が重ならない は確定ルール
// （design.md §5.1「同一通貨・同一tickに複数イベントが被った場合は再抽選する」）
// が実際に守られていることを大量のseedで確認する。
func TestDrawSessionEvents_同一通貨内で占有tick範囲が重ならない(t *testing.T) {
	for seed := int64(0); seed < 5000; seed++ {
		drawn := DrawSessionEvents(seed, 3)
		for i := 0; i < len(drawn); i++ {
			for j := i + 1; j < len(drawn); j++ {
				if drawn[i].CurrencyIndex != drawn[j].CurrencyIndex {
					continue
				}
				iStart, iEnd := drawn[i].occupiesRange()
				jStart, jEnd := drawn[j].occupiesRange()
				if iStart < jEnd && jStart < iEnd {
					t.Fatalf("seed=%d: 同一通貨(index=%d)で占有範囲が重なった: %+v, %+v",
						seed, drawn[i].CurrencyIndex, drawn[i], drawn[j])
				}
			}
		}
	}
}

// TestDrawSessionEvents_発火tickは範囲内 は確定範囲（2〜55。design.md §5.1）を確認する。
func TestDrawSessionEvents_発火tickは範囲内(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		for _, e := range DrawSessionEvents(seed, 3) {
			if e.RelativeFireTick < eventFireTickMin || e.RelativeFireTick > eventFireTickMax {
				t.Fatalf("seed=%d: RelativeFireTick = %d, want %d〜%d",
					seed, e.RelativeFireTick, eventFireTickMin, eventFireTickMax)
			}
			if e.Type == EventTypeVolUp || e.Type == EventTypeLiquidityDrain {
				if e.DurationTicks < eventDurationMin || e.DurationTicks > eventDurationMax {
					t.Fatalf("seed=%d type=%s: DurationTicks = %d, want %d〜%d",
						seed, e.Type, e.DurationTicks, eventDurationMin, eventDurationMax)
				}
			}
			if e.CurrencyIndex < 0 || e.CurrencyIndex >= 3 {
				t.Fatalf("seed=%d: CurrencyIndex = %d, want 0〜2", seed, e.CurrencyIndex)
			}
		}
	}
}

// TestDrawSessionEvents_通貨数に応じて変わる は CLAUDE.md §5.3
// 「通貨をハードコードしない」の裏付け: 通貨数を変えても CurrencyIndex が
// その範囲内に収まることを確認する（3種決め打ちであれば起きえない5種でも確認）。
func TestDrawSessionEvents_通貨数に応じて変わる(t *testing.T) {
	for seed := int64(0); seed < 500; seed++ {
		for _, e := range DrawSessionEvents(seed, 5) {
			if e.CurrencyIndex < 0 || e.CurrencyIndex >= 5 {
				t.Fatalf("seed=%d: CurrencyIndex = %d, want 0〜4", seed, e.CurrencyIndex)
			}
		}
	}
}

// drawnEventsEqual は drawnEvent 同士を比較する。Magnitude が decimal.Decimal
// （内部に *big.Int を持つ）を含むため、そのまま `==` で比較すると呼び出しごとに
// 新しく確保されるポインタの違いで「値は同じなのに不一致」と誤判定してしまう。
// 数値としての一致は decimal.Decimal.Equal() で見る必要がある。
func drawnEventsEqual(a, b drawnEvent) bool {
	return a.CurrencyIndex == b.CurrencyIndex &&
		a.Type == b.Type &&
		a.RelativeFireTick == b.RelativeFireTick &&
		a.DurationTicks == b.DurationTicks &&
		a.Direction == b.Direction &&
		a.Magnitude.Equal(b.Magnitude)
}

func testEvent(eventType string, fireTick int64, durationTicks int32, magnitude string) db.Event {
	return db.Event{
		Type:          eventType,
		FireTick:      fireTick,
		DurationTicks: durationTicks,
		Magnitude:     decimal.RequireFromString(magnitude),
	}
}

// TestShockMultiplier_発火済みのイベントだけが恒久的に効く は design.md §5.4の
// Π項（「発火後は恒久的にbase(n)へ効き続ける」）を確認する。
func TestShockMultiplier_発火済みのイベントだけが恒久的に効く(t *testing.T) {
	events := []db.Event{
		testEvent(EventTypeShock, 10, 0, "0.14"),  // +14%
		testEvent(EventTypeShock, 20, 0, "-0.04"), // -4%（別通貨のtickだが同じ関数に渡すテストなのでそのまま）
		testEvent(EventTypeVolUp, 5, 3, "3.0"),    // shock以外は無視されるはず
	}

	tests := []struct {
		tick int64
		want decimal.Decimal
	}{
		{9, decimal.NewFromInt(1)},                                                          // どちらも未発火
		{10, decimal.RequireFromString("1.14")},                                              // 1件目のみ発火
		{19, decimal.RequireFromString("1.14")},                                              // 2件目はまだ
		{20, decimal.RequireFromString("1.14").Mul(decimal.RequireFromString("0.96"))},       // 両方発火
		{1000, decimal.RequireFromString("1.14").Mul(decimal.RequireFromString("0.96"))},     // 発火後は恒久的に効く
	}

	for _, tt := range tests {
		got := shockMultiplier(events, tt.tick)
		if !got.Equal(tt.want) {
			t.Errorf("shockMultiplier(tick=%d) = %s, want %s", tt.tick, got, tt.want)
		}
	}
}

// TestVolatilityMultiplierAt_持続期間の内側だけ倍率がかかる は design.md §5.2の
// vol_up（一定期間の増幅・duration_ticks後は平常に戻る）を確認する。
func TestVolatilityMultiplierAt_持続期間の内側だけ倍率がかかる(t *testing.T) {
	events := []db.Event{testEvent(EventTypeVolUp, 10, 3, "3.0")} // 有効tick: 10,11,12

	tests := []struct {
		tick int64
		want float64
	}{
		{9, 1}, {10, 3}, {11, 3}, {12, 3}, {13, 1},
	}
	for _, tt := range tests {
		if got := volatilityMultiplierAt(events, tt.tick); got != tt.want {
			t.Errorf("volatilityMultiplierAt(tick=%d) = %v, want %v", tt.tick, got, tt.want)
		}
	}
}

// TestLiquidityMultiplierAt_持続期間の内側だけ倍率がかかる は design.md §5.2の
// liquidity_drain（liquidityを平常の30%に）を確認する。
func TestLiquidityMultiplierAt_持続期間の内側だけ倍率がかかる(t *testing.T) {
	events := []db.Event{testEvent(EventTypeLiquidityDrain, 10, 3, "0.30")}

	tests := []struct {
		tick int64
		want decimal.Decimal
	}{
		{9, decimal.NewFromInt(1)},
		{10, decimal.RequireFromString("0.30")},
		{12, decimal.RequireFromString("0.30")},
		{13, decimal.NewFromInt(1)},
	}
	for _, tt := range tests {
		got := liquidityMultiplierAt(events, tt.tick)
		if !got.Equal(tt.want) {
			t.Errorf("liquidityMultiplierAt(tick=%d) = %s, want %s", tt.tick, got, tt.want)
		}
	}
}

// TestBasePrice_shockは発火tick以降に一撃で反映される は BasePrice への
// events 統合（#40）を確認する。ボラティリティ0の通貨を使い、乱数由来の変動を
// 完全に排除することで shock の効果だけを検算できるようにする。
func TestBasePrice_shockは発火tick以降に一撃で反映される(t *testing.T) {
	c := db.Currency{
		BasePrice:       decimal.NewFromInt(100),
		Volatility:      decimal.Zero, // 乱数項を消す
		OffSessionScale: decimal.NewFromInt(1),
		Seed:            1001,
		EpochAt:         pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, jstZone), Valid: true},
	}
	events := []db.Event{testEvent(EventTypeShock, 30, 0, "0.14")}

	before := BasePrice(c, 29, events)
	if !before.Equal(c.BasePrice) {
		t.Errorf("発火前 BasePrice = %s, want %s（volatility=0なので不変のはず）", before, c.BasePrice)
	}

	after := BasePrice(c, 30, events)
	want := c.BasePrice.Mul(decimal.RequireFromString("1.14"))
	if !after.Equal(want) {
		t.Errorf("発火tick BasePrice = %s, want %s（+14%%）", after, want)
	}

	// 純粋関数性: 同じ(c, tick, events)を後から呼んでも同じ結果。
	again := BasePrice(c, 30, events)
	if !again.Equal(after) {
		t.Errorf("BasePrice(tick=30)を2回呼んで結果が変わった: %s, %s", after, again)
	}
}

// TestBasePrice_vol_upで純粋関数性が保たれる は #40完了条件
// 「イベント込みでもbase(n)が純粋関数のままであること（同一入力→同一出力）」の
// 中心的な確認。乱数項が残る（volatility>0）状態でもeventsが同じなら結果が
// 安定していることを見る。
func TestBasePrice_vol_upで純粋関数性が保たれる(t *testing.T) {
	c := testCurrency(1001, "0.02", "1.0")
	events := []db.Event{testEvent(EventTypeVolUp, 100, 5, "3.0")}

	for _, tick := range []int64{50, 100, 103, 104, 200, 1000} {
		a := BasePrice(c, tick, events)
		b := BasePrice(c, tick, events)
		if !a.Equal(b) {
			t.Errorf("BasePrice(tick=%d, events)を2回呼んで結果が変わった: %s, %s", tick, a, b)
		}
	}

	// vol_up区間の内外で結果が異なるはず（増幅が効いていることの間接確認）。
	withoutEvents := BasePrice(c, 104, nil)
	withEvents := BasePrice(c, 104, events)
	if withoutEvents.Equal(withEvents) {
		t.Error("vol_up区間内でeventsの有無によりBasePriceが変わらなかった（増幅が効いていない）")
	}
}
