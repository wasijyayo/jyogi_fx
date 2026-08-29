package game

import (
	"testing"

	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

// testPosition は ShouldLiquidate のテスト専用に最小限のフィールドを埋めた
// db.Position を作る。
func testPosition(side Side, size, entryPrice, leverage string) db.Position {
	return db.Position{
		Side:       string(side),
		Size:       decimal.RequireFromString(size),
		EntryPrice: decimal.RequireFromString(entryPrice),
		Leverage:   decimal.RequireFromString(leverage),
	}
}

// TestShouldLiquidate_レバレッジ10倍で清算距離が厳密に5パーセント は #38 の完了条件の
// 前提「清算距離はレバレッジ10倍で約5%（design.md §2.8/§5.2）」を、確認済みの
// 維持証拠金50%方式（liquidation.go のコメント参照）で厳密に検証する。
func TestShouldLiquidate_レバレッジ10倍で清算距離が厳密に5パーセント(t *testing.T) {
	tests := []struct {
		name  string
		side  Side
		price string // currentPrice
		want  bool
	}{
		{"ロング: 4.9%下落は清算されない", SideLong, "95.10", false},
		{"ロング: ちょうど5%下落で清算される（境界）", SideLong, "95.00", true},
		{"ロング: 5.1%下落は清算される", SideLong, "94.90", true},
		{"ロング: 上昇では清算されない", SideLong, "110.00", false},
		{"ショート: 4.9%上昇は清算されない", SideShort, "104.90", false},
		{"ショート: ちょうど5%上昇で清算される（境界）", SideShort, "105.00", true},
		{"ショート: 5.1%上昇は清算される", SideShort, "105.10", true},
		{"ショート: 下落では清算されない", SideShort, "90.00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// entry_price=100, size=10, leverage=10
			// → requiredMargin = 10×100/10 = 100、維持証拠金 = 50。
			p := testPosition(tt.side, "10", "100", "10")
			got := ShouldLiquidate(p, decimal.RequireFromString(tt.price))
			if got != tt.want {
				t.Errorf("ShouldLiquidate(%s, price=%s) = %v, want %v", tt.side, tt.price, got, tt.want)
			}
		})
	}
}

// TestShouldLiquidate_清算距離はレバレッジに反比例する は、レバレッジLでの清算距離が
// 0.5/L になること（design.md「レバレッジ10倍で約5%」の一般化）を、
// レバレッジ5倍（清算距離10%）・レバレッジ2倍（清算距離25%）で確認する。
func TestShouldLiquidate_清算距離はレバレッジに反比例する(t *testing.T) {
	tests := []struct {
		name     string
		leverage string
		// 境界のわずかに内側・外側の価格（ロング・entry_price=100想定）
		justInside  string
		justOutside string
	}{
		{"レバレッジ5倍→清算距離10%", "5", "90.10", "89.90"},
		{"レバレッジ2倍→清算距離25%", "2", "75.10", "74.90"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := testPosition(SideLong, "10", "100", tt.leverage)
			if ShouldLiquidate(p, decimal.RequireFromString(tt.justInside)) {
				t.Errorf("justInside=%s で清算されてしまった（清算距離の内側のはず）", tt.justInside)
			}
			if !ShouldLiquidate(p, decimal.RequireFromString(tt.justOutside)) {
				t.Errorf("justOutside=%s で清算されなかった（清算距離の外側のはず）", tt.justOutside)
			}
		})
	}
}
