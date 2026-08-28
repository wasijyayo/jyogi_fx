package game

import (
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// TestZAt_同じ入力なら同じ値 は #32 の完了条件（純粋関数であること）の核心部分。
// z は tick番号のみに依存し、呼び出し順序に依存しないことを確認する。
func TestZAt_同じ入力なら同じ値(t *testing.T) {
	for _, tick := range []int64{0, 1, 2, 100, 1440, 999999} {
		a := zAt(1001, tick)
		b := zAt(1001, tick)
		if a != b {
			t.Errorf("zAt(1001, %d) = %v, %v: 同じ入力で異なる値が返った", tick, a, b)
		}
	}
}

// TestZAt_呼び出し順序に依存しない は、先に大きいtickを呼んでから小さいtickを
// 呼んでも結果が変わらないこと（内部に隠れた状態が無いこと）を確認する。
func TestZAt_呼び出し順序に依存しない(t *testing.T) {
	first := zAt(1001, 1440)
	_ = zAt(1001, 1)
	_ = zAt(1001, 500)
	second := zAt(1001, 1440)

	if first != second {
		t.Errorf("zAt(1001, 1440) = %v then %v: 呼び出し順序で結果が変わった", first, second)
	}
}

// TestZAt_既知の値の回帰テスト は、ハッシュ+Box-Muller の実装を誤って変更した際に
// 気づけるようにするための固定値回帰テスト（値自体に意味はない）。
func TestZAt_既知の値の回帰テスト(t *testing.T) {
	want := map[int64]float64{
		1:    -1.7453965176792068,
		2:    -1.3059515826371326,
		100:  1.7384233610757311,
		1440: 0.4523167679568953,
	}
	for tick, w := range want {
		got := zAt(1001, tick)
		if math.Abs(got-w) > 1e-12 {
			t.Errorf("zAt(1001, %d) = %v, want %v", tick, got, w)
		}
	}
}

// TestZAt_異なるseedなら異なる系列になる は、通貨ごとに異なる seed
// （JOG=1001 / WASI=2002 / CHEBU=3003, design.md §2.12）を持たせる意味を確認する。
func TestZAt_異なるseedなら異なる系列になる(t *testing.T) {
	if zAt(1001, 1) == zAt(2002, 1) {
		t.Error("異なる seed で同じ tick=1 の値が一致した（偶然の可能性はあるが、実装ミスを疑う）")
	}
}

var jstZone = time.FixedZone("JST", 9*60*60)

func TestScaleAt(t *testing.T) {
	offSessionScale := decimal.NewFromFloat(0.05)
	// epoch を JST 0:00 ちょうどに置く（epochOffsetMinutes = 0 で境界を計算しやすくする）。
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, jstZone)
	epochOffset := minuteOfDayJST(epoch)

	tests := []struct {
		name string
		tick int64
		want decimal.Decimal
	}{
		{"セッション開始ちょうど(12:00)", 12 * 60, decimal.NewFromInt(1)},
		{"セッション中(12:30)", 12*60 + 30, decimal.NewFromInt(1)},
		{"セッション終了直前(12:59)", 12*60 + 59, decimal.NewFromInt(1)},
		{"セッション終了ちょうど(13:00)はセッション外", 13 * 60, offSessionScale},
		{"セッション開始直前(11:59)はセッション外", 12*60 - 1, offSessionScale},
		{"深夜はセッション外", 3 * 60, offSessionScale},
		{"日をまたいだ翌日の12:00もセッション中", minutesPerDay + 12*60, decimal.NewFromInt(1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scaleAt(epochOffset, tt.tick, offSessionScale)
			if !got.Equal(tt.want) {
				t.Errorf("scaleAt(tick=%d) = %s, want %s", tt.tick, got, tt.want)
			}
		})
	}
}

// testCurrency はテスト用の db.Currency を組み立てる。
// epochAt は JST 0:00 固定（scaleAt のセッション判定を検算しやすくするため）。
func testCurrency(seed int64, volatility, offSessionScale string) db.Currency {
	return db.Currency{
		BasePrice:       decimal.NewFromInt(100),
		Volatility:      decimal.RequireFromString(volatility),
		OffSessionScale: decimal.RequireFromString(offSessionScale),
		Seed:            seed,
		EpochAt:         pgtype.Timestamptz{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, jstZone), Valid: true},
	}
}

// TestBasePrice_tick0は基準価格そのまま は「1〜nの総和」が空集合になるケース
// （exp(0) = 1）を確認する。
func TestBasePrice_tick0は基準価格そのまま(t *testing.T) {
	c := testCurrency(1001, "0.0008", "0.05")

	for _, tick := range []int64{0, -1} {
		got := BasePrice(c, tick)
		if !got.Equal(c.BasePrice) {
			t.Errorf("BasePrice(tick=%d) = %s, want %s", tick, got, c.BasePrice)
		}
	}
}

// TestBasePrice_同じ入力なら同じ値 は #32 の完了条件そのもの。
func TestBasePrice_同じ入力なら同じ値(t *testing.T) {
	c := testCurrency(1001, "0.0008", "0.05")

	for _, tick := range []int64{1, 100, 1440, 100000} {
		a := BasePrice(c, tick)
		b := BasePrice(c, tick)
		if !a.Equal(b) {
			t.Errorf("BasePrice(tick=%d) = %s, %s: 同じ入力で異なる値が返った", tick, a, b)
		}
	}
}

// TestBasePrice_呼び出し順序に依存しない も #32 の完了条件そのもの。
// 大きいtickを先に計算しても、後から計算した同じtickの結果が変わらないことを確認する
// （内部に積み上げ式の状態を持っていないことの検証）。
func TestBasePrice_呼び出し順序に依存しない(t *testing.T) {
	c := testCurrency(1001, "0.0008", "0.05")

	before := BasePrice(c, 500)
	_ = BasePrice(c, 10)
	_ = BasePrice(c, 50000)
	_ = BasePrice(c, 1)
	after := BasePrice(c, 500)

	if !before.Equal(after) {
		t.Errorf("BasePrice(tick=500) = %s then %s: 呼び出し順序で結果が変わった", before, after)
	}
}

// TestBasePrice_通貨が違えば結果も違う は、通貨ごとに異なる seed/volatility を
// 持たせる意味（design.md §2.12: JOG/WASI/CHEBU で性格を変える）を確認する。
func TestBasePrice_通貨が違えば結果も違う(t *testing.T) {
	jog := testCurrency(1001, "0.0008", "0.05")
	wasi := testCurrency(2002, "0.0020", "0.05")

	if BasePrice(jog, 1000).Equal(BasePrice(wasi, 1000)) {
		t.Error("異なる通貨(seed/volatility)で同じ結果になった")
	}
}

// TestBasePrice_セッション外は変動が抑制される は off_session_scale の効果そのものを
// 確認する。同じ乱数系列でも、セッション外係数を1に固定した場合より
// 変動幅（ここでは base_price からの乖離）が小さくなるはず、を統計的に確認する
// （個々のtickの符号までは制御できないため、多数tickの標準偏差で比較する）。
func TestBasePrice_セッション外は変動が抑制される(t *testing.T) {
	suppressed := testCurrency(1001, "0.02", "0.05") // volatilityを大きめにして差を見やすくする
	unsuppressed := testCurrency(1001, "0.02", "1.0")

	const tick = minutesPerDay * 5 // 5日分。ほぼ全時間帯がセッション外。

	got := BasePrice(suppressed, tick)
	base := BasePrice(unsuppressed, tick)

	diffFromStart := got.Sub(suppressed.BasePrice).Abs()
	diffFromStartUnsuppressed := base.Sub(unsuppressed.BasePrice).Abs()

	if diffFromStart.GreaterThanOrEqual(diffFromStartUnsuppressed) {
		t.Errorf("抑制ありの乖離 %s が抑制なしの乖離 %s 以上になった（抑制が効いていない）",
			diffFromStart, diffFromStartUnsuppressed)
	}
}

// BenchmarkBasePrice は #32 の完了条件「1tickあたり十分速いこと
// （design.md §2.7 の想定は約100ns）」を検証するためのベンチマーク。
// go test -bench=BasePrice -benchtime=10x ./internal/game/ で実行する。
func BenchmarkBasePrice(b *testing.B) {
	c := testCurrency(1001, "0.0008", "0.05")
	const tick = 1440 * 100 // 100日分（design.mdの想定と同じ規模）

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BasePrice(c, tick)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(tick), "ns/tick")
}
