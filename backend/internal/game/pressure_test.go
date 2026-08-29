package game

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// pressureCurrency はpressure関連のテスト専用に db.Currency を組み立てる。
func pressureCurrency(lambda, k, liquidity, pressure string, pressureAt time.Time) db.Currency {
	return db.Currency{
		BasePrice: decimal.NewFromInt(100),
		Lambda:    decimal.RequireFromString(lambda),
		K:         decimal.RequireFromString(k),
		Liquidity: decimal.RequireFromString(liquidity),
		Pressure:  decimal.RequireFromString(pressure),
		PressureAt: pgtype.Timestamptz{
			Time:  pressureAt,
			Valid: true,
		},
		// Volatility=0 / EpochAt=pressureAt にしておくと BasePrice(c, 0) は
		// base_price のまま動かないため、CurrentPrice のテストで pressure の
		// 影響だけを切り出して確認できる。
		Volatility: decimal.Zero,
		EpochAt:    pgtype.Timestamptz{Time: pressureAt, Valid: true},
	}
}

// TestPressure_半減期どおりに減衰する は #33 の完了条件の中核。
// design.md §2.12 で確定した3通貨それぞれの半減期（JOG=5分/WASI=4分/CHEBU=3分）どおりに
// 圧力が半分になることを確認する。
func TestPressure_半減期どおりに減衰する(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		lambda   string // design.md §2.12 の確定値
		halfLife time.Duration
	}{
		{"JOG(半減期5分)", "0.1386294361", 5 * time.Minute},
		{"WASI(半減期4分)", "0.1732867951", 4 * time.Minute},
		{"CHEBU(半減期3分)", "0.2310490602", 3 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := pressureCurrency(tt.lambda, "0.01", "10000", "0.08", t0)

			got := Pressure(c, t0.Add(tt.halfLife))
			want := c.Pressure.Div(decimal.NewFromInt(2))

			diff := got.Sub(want).Abs()
			tolerance := decimal.NewFromFloat(0.0001) // λが丸められている分の誤差を許容
			if diff.GreaterThan(tolerance) {
				t.Errorf("Pressure after 1 half-life = %s, want ≈ %s (diff=%s)", got, want, diff)
			}
		})
	}
}

// TestPressure_経過時間ゼロなら変化しない は基本的な境界条件。
func TestPressure_経過時間ゼロなら変化しない(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := pressureCurrency("0.1386294361", "0.01", "10000", "0.08", t0)

	got := Pressure(c, t0)
	if !got.Equal(c.Pressure) {
		t.Errorf("Pressure(elapsed=0) = %s, want %s", got, c.Pressure)
	}
}

// TestPressure_時刻が巻き戻っていたら減衰させない は、クロックのずれ等で
// now が pressure_at より前になった場合の安全側の挙動を確認する。
func TestPressure_時刻が巻き戻っていたら減衰させない(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := pressureCurrency("0.1386294361", "0.01", "10000", "0.08", t0)

	got := Pressure(c, t0.Add(-1*time.Minute))
	if !got.Equal(c.Pressure) {
		t.Errorf("Pressure(elapsed<0) = %s, want %s（変化させないはず）", got, c.Pressure)
	}
}

// TestUpdatePressure_買いは上げ売りは下げる は design.md §2.2 の符号を確認する。
func TestUpdatePressure_買いは上げ売りは下げる(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := pressureCurrency("0.1386294361", "0.01", "10000", "0", t0)

	buy := UpdatePressure(c, t0, decimal.NewFromInt(1000), c.Liquidity)
	if !buy.GreaterThan(c.Pressure) {
		t.Errorf("買い注文後の圧力 %s が元の圧力 %s 以下だった", buy, c.Pressure)
	}

	sell := UpdatePressure(c, t0, decimal.NewFromInt(-1000), c.Liquidity)
	if !sell.LessThan(c.Pressure) {
		t.Errorf("売り注文後の圧力 %s が元の圧力 %s 以上だった", sell, c.Pressure)
	}

	// k × (取引量/liquidity) = 0.01 × (1000/10000) = 0.001
	want := decimal.RequireFromString("0.001")
	if !buy.Equal(want) {
		t.Errorf("買い注文の圧力変化 = %s, want %s", buy, want)
	}
}

// TestUpdatePressure_liquidityゼロなら影響なし はゼロ除算を避けるガードを確認する。
func TestUpdatePressure_liquidityゼロなら影響なし(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := pressureCurrency("0.1386294361", "0.01", "10000", "0.05", t0)

	got := UpdatePressure(c, t0, decimal.NewFromInt(1000), decimal.Zero)
	if !got.Equal(c.Pressure) {
		t.Errorf("liquidity=0でも圧力が変化した: %s, want %s", got, c.Pressure)
	}
}

// TestCurrentPrice_大量購入で価格が跳ねてその後半減期どおりに戻る は
// #33 の完了条件そのもの。
func TestCurrentPrice_大量購入で価格が跳ねてその後半減期どおりに戻る(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := pressureCurrency("0.1386294361", "0.01", "10000", "0", t0) // JOG相当（半減期5分）

	before := CurrentPrice(c, 0, t0, nil)
	if !before.Equal(c.BasePrice) {
		t.Fatalf("取引前の価格 = %s, want %s（圧力0のはず）", before, c.BasePrice)
	}

	// 大量購入: 圧力を +0.08（8%）動かす取引を発生させる。
	// k × (volume/liquidity) = 0.08 になるよう volume を逆算する。
	volume := decimal.RequireFromString("0.08").Mul(c.Liquidity).Div(c.K)
	newPressure := UpdatePressure(c, t0, volume, c.Liquidity)
	c.Pressure = newPressure
	c.PressureAt = pgtype.Timestamptz{Time: t0, Valid: true}

	spiked := CurrentPrice(c, 0, t0, nil)
	if !spiked.GreaterThan(before) {
		t.Fatalf("大量購入後の価格 %s が購入前 %s 以下だった（跳ねていない）", spiked, before)
	}
	wantSpiked := c.BasePrice.Mul(decimal.RequireFromString("1.08"))
	if diff := spiked.Sub(wantSpiked).Abs(); diff.GreaterThan(decimal.NewFromFloat(0.000001)) {
		t.Errorf("跳ねた価格 = %s, want ≈ %s", spiked, wantSpiked)
	}

	// 半減期(5分)ちょうど後: 圧力が半分になり、価格もその分だけ戻っているはず。
	afterHalfLife := CurrentPrice(c, 0, t0.Add(5*time.Minute), nil)
	wantAfter := c.BasePrice.Mul(decimal.NewFromInt(1).Add(newPressure.Div(decimal.NewFromInt(2))))
	if diff := afterHalfLife.Sub(wantAfter).Abs(); diff.GreaterThan(decimal.NewFromFloat(0.0001)) {
		t.Errorf("半減期後の価格 = %s, want ≈ %s（圧力が半分に戻っていない）", afterHalfLife, wantAfter)
	}
	if !afterHalfLife.LessThan(spiked) {
		t.Errorf("半減期後の価格 %s が跳ねた直後の価格 %s 以上だった（戻っていない）", afterHalfLife, spiked)
	}
	if !afterHalfLife.GreaterThan(before) {
		t.Errorf("半減期後の価格 %s が取引前 %s 以下になった（戻りすぎ）", afterHalfLife, before)
	}
}
