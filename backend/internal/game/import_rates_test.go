package game

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// dayUnix は UTC 00:00 の Unix 秒を返す（テスト用ヘルパー）。
func dayUnix(t *testing.T, y int, m time.Month, d int) int64 {
	t.Helper()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
}

// rateRec はテスト用に 1 行分の JSON を組み立てる。
func rateRec(ts int64, o, h, l, c string) string {
	return `{"ts":` + strconv.FormatInt(ts, 10) + `,"o":` + o + `,"h":` + h + `,"l":` + l + `,"c":` + c + `}`
}

func array(records ...string) []byte {
	return []byte("[" + strings.Join(records, ",") + "]")
}

// TestParseRates_桁が落ちない は、UseNumber + decimal.NewFromString によって
// float を経由せず、小数が丸まらないことを確認する（CLAUDE.md §5.2）。
// float64 を経由すると 119.746 が 119.745999... になるため、文字列一致で検出できる。
func TestParseRates_桁が落ちない(t *testing.T) {
	ts := dayUnix(t, 2015, 1, 1) // 2015-01-01（木）
	bars, err := parseRates(array(rateRec(ts, "119.746", "119.967", "119.701", "119.885")))
	if err != nil {
		t.Fatalf("parseRates: %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("len(bars) = %d, want 1", len(bars))
	}
	b := bars[0]
	for name, got := range map[string]decimal.Decimal{
		"o": b.open, "h": b.high, "l": b.low, "c": b.close,
	} {
		if got.String() != map[string]string{
			"o": "119.746", "h": "119.967", "l": "119.701", "c": "119.885",
		}[name] {
			t.Errorf("%s = %s, want exact decimal（float 経由で桁が落ちている）", name, got)
		}
	}
}

// TestParseRates_週末の判定 は、土曜・日曜の行に weekend フラグが立つことを確認する。
func TestParseRates_週末の判定(t *testing.T) {
	thu := dayUnix(t, 2015, 1, 1) // 木
	sat := dayUnix(t, 2015, 1, 3) // 土
	sun := dayUnix(t, 2015, 1, 4) // 日

	bars, err := parseRates(array(
		rateRec(thu, "1.0", "1.1", "0.9", "1.05"),
		rateRec(sat, "2.0", "2.1", "1.9", "2.05"),
		rateRec(sun, "3.0", "3.1", "2.9", "3.05"),
	))
	if err != nil {
		t.Fatalf("parseRates: %v", err)
	}
	want := []bool{false, true, true}
	for i, b := range bars {
		if b.weekend != want[i] {
			t.Errorf("bars[%d].weekend = %v, want %v", i, b.weekend, want[i])
		}
	}
}

// TestSplitWeekdays は、平日だけを残し、週末の件数を返すことを確認する。
func TestSplitWeekdays(t *testing.T) {
	bars := []dailyBar{
		{weekend: false},
		{weekend: true},
		{weekend: true},
		{weekend: false},
		{weekend: false},
	}
	weekdays, skipped := splitWeekdays(bars)
	if len(weekdays) != 3 {
		t.Errorf("len(weekdays) = %d, want 3", len(weekdays))
	}
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
}

// TestParseRates_検証エラー は各検証の違反が「どの行か」付きのエラーになることを確認する。
func TestParseRates_検証エラー(t *testing.T) {
	ts := dayUnix(t, 2015, 1, 1) // 木

	tests := []struct {
		name string
		rec  string
		want string // エラーメッセージの部分一致
	}{
		{"ts が整数でない", `{"ts":1420070400.5,"o":1,"h":1,"l":1,"c":1}`, "ts is not an integer"},
		{"ts が 00:00 UTC でない", rateRec(ts+3600, "1", "1", "1", "1"), "not 00:00 UTC"},
		{"o が正でない", rateRec(ts, "0", "1", "1", "1"), "o must be positive"},
		{"high < open", rateRec(ts, "1.5", "1.4", "1.0", "1.0"), "high"},
		{"high < close", rateRec(ts, "1.0", "1.4", "1.0", "1.5"), "high"},
		{"high < low", rateRec(ts, "1.0", "1.4", "1.5", "1.0"), "high"},
		{"low > open", rateRec(ts, "1.0", "1.5", "1.1", "1.0"), "low"},
		{"low > close", rateRec(ts, "1.0", "1.5", "0.9", "0.8"), "low"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRates(array(tt.rec))
			if err == nil {
				t.Fatal("parseRates = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("parseRates error = %q, want to contain %q", err, tt.want)
			}
		})
	}
}

// TestParseRates_日付重複 は、1 ファイル内で同じ日付が 2 回現れるとエラーになることを確認する。
func TestParseRates_日付重複(t *testing.T) {
	ts := dayUnix(t, 2015, 1, 1) // 木
	_, err := parseRates(array(
		rateRec(ts, "1.0", "1.1", "0.9", "1.0"),
		rateRec(ts, "2.0", "2.1", "1.9", "2.0"),
	))
	if err == nil {
		t.Fatal("parseRates = nil, want duplicate date error")
	}
	if !strings.Contains(err.Error(), "duplicate date") {
		t.Errorf("parseRates error = %q, want to contain %q", err, "duplicate date")
	}
}
