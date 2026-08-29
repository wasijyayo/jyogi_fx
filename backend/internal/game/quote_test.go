package game

import (
	"testing"

	"github.com/shopspring/decimal"
)

func closesFrom(values ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, len(values))
	for i, v := range values {
		out[i] = decimal.RequireFromString(v)
	}
	return out
}

// TestSparkline_最小値と最大値が両端の段になる は design.md §6.4
// 「直近8本のキャンドルのcloseを8段階に写像する」の基本動作を確認する。
func TestSparkline_最小値と最大値が両端の段になる(t *testing.T) {
	got := sparkline(closesFrom("100", "110", "90", "120", "80"))
	want := []rune(got)
	if len(want) != 5 {
		t.Fatalf("len(sparkline) = %d, want 5", len(want))
	}
	// 最小値(80, index4)は最初の段、最大値(120, index3)は最後の段になるはず。
	if want[4] != sparklineLevels[0] {
		t.Errorf("最小値の段 = %q, want %q", string(want[4]), string(sparklineLevels[0]))
	}
	if want[3] != sparklineLevels[len(sparklineLevels)-1] {
		t.Errorf("最大値の段 = %q, want %q", string(want[3]), string(sparklineLevels[len(sparklineLevels)-1]))
	}
}

// TestSparkline_全て同値なら真ん中の段 は、レンジ0（ゼロ除算になりうるケース）の
// 安全な扱いを確認する。
func TestSparkline_全て同値なら真ん中の段(t *testing.T) {
	got := sparkline(closesFrom("100", "100", "100"))
	want := string(sparklineLevels[len(sparklineLevels)/2])
	for _, r := range got {
		if string(r) != want {
			t.Errorf("sparkline(全て同値) の各段 = %q, want %q", string(r), want)
		}
	}
	if len([]rune(got)) != 3 {
		t.Errorf("len(sparkline) = %d, want 3", len([]rune(got)))
	}
}

// TestSparkline_空スライスは空文字 を確認する（保存済みtickが無い通貨向け）。
func TestSparkline_空スライスは空文字(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("sparkline(nil) = %q, want \"\"", got)
	}
}

// TestSparkline_単調増加なら段も単調非減少 を確認する（見た目の一貫性）。
func TestSparkline_単調増加なら段も単調非減少(t *testing.T) {
	closes := closesFrom("100", "105", "110", "115", "120", "125", "130", "135")
	got := []rune(sparkline(closes))
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Errorf("段が減少した: index%d(%q) < index%d(%q)", i, string(got[i]), i-1, string(got[i-1]))
		}
	}
}
