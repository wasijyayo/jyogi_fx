package game

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"fxgame/backend/internal/db"
)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

// TestReplayPublishedDays は公開済み営業日数が経過時間と速さから正しく計算されることを確認する。
func TestReplayPublishedDays(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		changes []db.ReplaySpeedChange
		now     time.Time
		want    int
	}{
		{
			name: "開始直後は0",
			now:  start,
			want: 0,
		},
		{
			name: "標準の速さで3秒ごとに1日",
			now:  start.Add(9 * time.Second),
			want: 3,
		},
		{
			name: "60日で打ち止め",
			now:  start.Add(181 * time.Second),
			want: 60,
		},
		{
			name: "×2にしたら2倍の速さ",
			changes: []db.ReplaySpeedChange{
				{ChangedAt: pgtype.Timestamptz{Time: start.Add(6 * time.Second), Valid: true}, Speed: 2},
			},
			now:  start.Add(12 * time.Second),
			want: 6, // 6秒を×1で2日 + 6秒を×2で4日 = 6日
		},
		{
			name: "×3→×1と戻す",
			changes: []db.ReplaySpeedChange{
				{ChangedAt: pgtype.Timestamptz{Time: start.Add(3 * time.Second), Valid: true}, Speed: 3},
				{ChangedAt: pgtype.Timestamptz{Time: start.Add(6 * time.Second), Valid: true}, Speed: 1},
			},
			now:  start.Add(9 * time.Second),
			want: 5, // 3秒×1=1日 + 3秒×3=3日 + 3秒×1=1日 = 5日
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replayPublishedDays(start, tt.changes, tt.now)
			if got != tt.want {
				t.Errorf("replayPublishedDays = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestReplayLiquidationLine は強制決済ラインの計算を確認する（design.md §13）。
func TestReplayLiquidationLine(t *testing.T) {
	// 証拠金100・10倍（取引規模1000）の買い → 10% 下落でライン。
	notional := dec("1000")
	line := replayLiquidationLine(SideLong, dec("100"), dec("100"), notional)
	if !line.Equal(dec("90")) {
		t.Errorf("long line = %s, want 90", line)
	}

	// 証拠金を 50 追加（証拠金150）→ 15% 下落でライン（ラインが遠ざかる）。
	line = replayLiquidationLine(SideLong, dec("100"), dec("150"), notional)
	if !line.Equal(dec("85")) {
		t.Errorf("long line (margin 150) = %s, want 85", line)
	}

	// 売りは上がると損。証拠金100・10倍 → 10% 上昇でライン。
	line = replayLiquidationLine(SideShort, dec("100"), dec("100"), notional)
	if !line.Equal(dec("110")) {
		t.Errorf("short line = %s, want 110", line)
	}
}

// TestReplayPnL は損益 = 取引規模 × 価格変化率 を確認する（証拠金追加で取引規模は変わらない）。
func TestReplayPnL(t *testing.T) {
	notional := dec("1000") // 当初証拠金100 × 10倍

	// 買い・1% 上昇 → +10。証拠金を追加しても損益は変わらない。
	pnl := replayPnL(SideLong, dec("100"), dec("101"), notional)
	if !pnl.Equal(dec("10")) {
		t.Errorf("long pnl = %s, want 10", pnl)
	}

	// 売り・1% 下落 → +10。
	pnl = replayPnL(SideShort, dec("100"), dec("99"), notional)
	if !pnl.Equal(dec("10")) {
		t.Errorf("short pnl = %s, want 10", pnl)
	}

	// 売り・1% 上昇 → -10。
	pnl = replayPnL(SideShort, dec("100"), dec("101"), notional)
	if !pnl.Equal(dec("-10")) {
		t.Errorf("short pnl (up) = %s, want -10", pnl)
	}
}

// TestReplayCloseReturn は決済で戻る額 = max(0, 証拠金 + 損益 − 手数料) を確認する。
func TestReplayCloseReturn(t *testing.T) {
	// 利益: 証拠金100 + 損益50 - 手数料0.5 = 149.5。
	got := replayCloseReturn(dec("100"), dec("50"), dec("0.5"))
	if !got.Equal(dec("149.5")) {
		t.Errorf("close return = %s, want 149.5", got)
	}

	// 損失が証拠金を超えても 0 で止まる（資金はマイナスにならない）。
	got = replayCloseReturn(dec("100"), dec("-150"), dec("0.5"))
	if !got.Equal(decimal.Zero) {
		t.Errorf("close return (big loss) = %s, want 0", got)
	}
}

// TestReplayUnlockCost は上限解放の費用が 250 → 500 → 1000 と倍々になることを確認する。
func TestReplayUnlockCost(t *testing.T) {
	want := []string{"250", "500", "1000", "2000"}
	for tier, w := range want {
		got := replayUnlockCost(tier)
		if !got.Equal(dec(w)) {
			t.Errorf("unlock cost tier %d = %s, want %s", tier, got, w)
		}
	}
}

// TestReplayNotional は取引規模 = 当初証拠金 × レバレッジを確認する。
func TestReplayNotional(t *testing.T) {
	got := replayNotional(dec("100"), 10)
	if !got.Equal(dec("1000")) {
		t.Errorf("notional = %s, want 1000", got)
	}
}

// TestBusinessDaysBefore は「今日から平日を数えて n 日さかのぼる」を確認する。
func TestBusinessDaysBefore(t *testing.T) {
	// 2026-10-30 は金曜。60 営業日さかのぼるとおよそ 2026-08-07（12 週前の金曜）。
	friday := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	got := businessDaysBefore(friday, 60)
	if got.Weekday() != time.Friday {
		t.Errorf("businessDaysBefore weekday = %s, want Friday", got.Weekday())
	}
	// 12 週 = 84 日で、土日を除くと 60 営業日。ちょうど 12 週前の金曜 = 2026-08-07。
	want := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("businessDaysBefore = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}

	// 月曜から 1 営業日さかのぼると前の金曜。
	mon := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	got = businessDaysBefore(mon, 1)
	if !got.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("businessDaysBefore(mon,1) = %s, want 2026-10-23", got.Format("2006-01-02"))
	}
}

// TestIntersectDates は共通営業日の交差を確認する。
func TestIntersectDates(t *testing.T) {
	d := func(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }
	a := []time.Time{d("2026-01-01"), d("2026-01-02"), d("2026-01-03")}
	b := []time.Time{d("2026-01-02"), d("2026-01-03"), d("2026-01-04")}
	c := []time.Time{d("2026-01-01"), d("2026-01-02"), d("2026-01-03"), d("2026-01-04")}

	got := intersectDates([][]time.Time{a, b, c})
	if len(got) != 2 || !got[0].Equal(d("2026-01-02")) || !got[1].Equal(d("2026-01-03")) {
		t.Errorf("intersectDates = %v, want [2026-01-02 2026-01-03]", got)
	}
}

// TestMinOrderCost は早期終了判定の最低注文費用（証拠金1 + 取引規模1 × 最小手数料率）を確認する。
func TestMinOrderCost(t *testing.T) {
	pairs := []db.CurrencyPair{
		{FeeRate: dec("0.000500")},
		{FeeRate: dec("0.001000")},
	}
	got := minOrderCost(pairs)
	if !got.Equal(dec("1.0005")) {
		t.Errorf("minOrderCost = %s, want 1.0005", got)
	}
}

// TestIsPositiveInteger は証拠金・レバレッジ・追加額の検証を確認する。
func TestIsPositiveInteger(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1", true},
		{"100", true},
		{"0", false},
		{"-1", false},
		{"1.5", false},
		{"0.5", false},
	}
	for _, tt := range tests {
		if got := isPositiveInteger(dec(tt.in)); got != tt.want {
			t.Errorf("isPositiveInteger(%s) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
