package game

import (
	"strings"
	"testing"
)

// TestParseHistoricalEvents は import-events の入力（日付の精度の判定）を確認する。
func TestParseHistoricalEvents(t *testing.T) {
	file := `[
		{"date":"2015-01-05","headline":"日","description":"説明","category":"分野","source":"出典"},
		{"date":"2015-02","headline":"月","description":"説明","category":"分野","source":"出典","adopted":true},
		{"date":"2015","headline":"年","description":"説明","category":"分野","source":"出典"}
	]`
	inputs, err := parseHistoricalEvents(strings.NewReader(file))
	if err != nil {
		t.Fatalf("parseHistoricalEvents: %v", err)
	}
	if len(inputs) != 3 {
		t.Fatalf("inputs = %d, want 3", len(inputs))
	}
	for i, want := range []string{"day", "month", "year"} {
		if inputs[i].datePrecision != want {
			t.Errorf("inputs[%d].datePrecision = %q, want %q", i, inputs[i].datePrecision, want)
		}
	}
	if inputs[0].year != 2015 || !inputs[0].eventDate.Valid {
		t.Errorf("日まで確定した出来事の年・日付が正しくない: %+v", inputs[0])
	}
	if inputs[1].year != 2015 || inputs[1].eventDate.Valid {
		t.Errorf("月までしか分からない出来事に日付が入っている: %+v", inputs[1])
	}
	if !inputs[1].adopted {
		t.Error("adopted が反映されていない")
	}
	if inputs[2].eventDate.Valid {
		t.Errorf("年までしか分からない出来事に日付が入っている: %+v", inputs[2])
	}
}

func TestParseHistoricalEvents_Invalid(t *testing.T) {
	cases := map[string]string{
		"日付の形式":     `[{"date":"2015/01/05","headline":"見出し","description":"説明","category":"分野","source":"出典"}]`,
		"見出しが空":     `[{"date":"2015-01-05","headline":" ","description":"説明","category":"分野","source":"出典"}]`,
		"説明が空":      `[{"date":"2015-01-05","headline":"見出し","description":"","category":"分野","source":"出典"}]`,
		"分野が空":      `[{"date":"2015-01-05","headline":"見出し","description":"説明","category":"","source":"出典"}]`,
		"出典が空":      `[{"date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":""}]`,
		"知らないフィールド": `[{"date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典","unknown":1}]`,
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseHistoricalEvents(strings.NewReader(file)); err == nil {
				t.Error("エラーにならなかった")
			}
		})
	}
}
