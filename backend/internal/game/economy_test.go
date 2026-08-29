package game

import (
	"testing"

	"github.com/shopspring/decimal"
)

func decimals(values ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, len(values))
	for i, v := range values {
		out[i] = decimal.RequireFromString(v)
	}
	return out
}

// TestMedian_奇数偶数境界 は median() が要素数の偶奇で正しく振る舞うことを確認する
// （design.md §7.2「全登録者の総資産の中央値」の算出そのもの）。
func TestMedian_奇数偶数境界(t *testing.T) {
	tests := []struct {
		name   string
		values []decimal.Decimal
		want   decimal.Decimal
	}{
		{"要素0個はゼロ", decimals(), decimal.Zero},
		{"要素1個はその値", decimals("42"), decimal.RequireFromString("42")},
		{"奇数個は真ん中の値", decimals("30", "10", "20"), decimal.RequireFromString("20")},
		{"偶数個は真ん中2つの平均", decimals("10", "40", "20", "30"), decimal.RequireFromString("25")},
		{"同値が混在していても順序に依存しない", decimals("5", "5", "1", "9"), decimal.RequireFromString("5")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := median(tt.values)
			if !got.Equal(tt.want) {
				t.Errorf("median(%v) = %s, want %s", tt.values, got, tt.want)
			}
		})
	}
}

// TestMedian_引数のスライスを破壊しない は、median() が呼び出し元のスライスを
// ソートで書き換えないことを確認する（TotalAssetsByUserのmapから作った
// スライスをこの後も使う呼び出し側を想定した安全側の性質）。
func TestMedian_引数のスライスを破壊しない(t *testing.T) {
	values := decimals("30", "10", "20")
	original := append([]decimal.Decimal(nil), values...)

	median(values)

	for i := range values {
		if !values[i].Equal(original[i]) {
			t.Errorf("median()呼び出し後に values[%d] = %s, want %s（元の順序が変わっている）",
				i, values[i], original[i])
		}
	}
}
