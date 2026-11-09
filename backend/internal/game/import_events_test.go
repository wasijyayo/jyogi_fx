package game

import (
	"testing"
)

// TestParseEventFiles は import-events の入力（日付の精度・key・ファイル名の年）を確認する。
func TestParseEventFiles(t *testing.T) {
	files := []EventImportFile{
		{Name: "2015.json", Data: []byte(`[
			{"key":"2015-day","date":"2015-01-05","headline":"日まで分かる出来事","description":"説明","category":"分野","source":"出典"},
			{"key":"2015-month","date":"2015-02","headline":"月まで分かる出来事","description":"説明","category":"分野","source":"出典"},
			{"key":"2015-year","date":"2015","headline":"年まで分かる出来事","description":"説明","category":"分野","source":"出典"}
		]`)},
	}
	inputs, err := parseEventFiles(files)
	if err != nil {
		t.Fatalf("parseEventFiles: %v", err)
	}
	if len(inputs) != 3 {
		t.Fatalf("inputs = %d, want 3", len(inputs))
	}
	for i, want := range []string{"day", "month", "year"} {
		if inputs[i].datePrecision != want {
			t.Errorf("inputs[%d].datePrecision = %q, want %q", i, inputs[i].datePrecision, want)
		}
	}
	if inputs[0].key != "2015-day" {
		t.Errorf("inputs[0].key = %q, want 2015-day", inputs[0].key)
	}
	if inputs[0].year != 2015 || !inputs[0].eventDate.Valid {
		t.Errorf("日まで確定した出来事の年・日付が正しくない: %+v", inputs[0])
	}
	if inputs[1].year != 2015 || inputs[1].eventDate.Valid {
		t.Errorf("月までしか分からない出来事に日付が入っている: %+v", inputs[1])
	}
	if inputs[2].eventDate.Valid {
		t.Errorf("年までしか分からない出来事に日付が入っている: %+v", inputs[2])
	}
}

func TestParseEventFiles_Invalid(t *testing.T) {
	valid := `{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典"}`
	cases := map[string]struct {
		name string
		data string
	}{
		"key が空":          {"2015.json", `[{"key":"","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典"}]`},
		"key が大文字":        {"2015.json", `[{"key":"2015-Event","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典"}]`},
		"key にアンダースコア":    {"2015.json", `[{"key":"2015_event","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典"}]`},
		"key が重複":         {"2015.json", `[` + valid + `,` + valid + `]`},
		"ファイル名の年と不一致":     {"2016.json", `[` + valid + `]`},
		"ファイル名の形式":        {"events.json", `[` + valid + `]`},
		"日付の形式":           {"2015.json", `[{"key":"2015-event","date":"2015/01/05","headline":"見出し","description":"説明","category":"分野","source":"出典"}]`},
		"見出しが空":           {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":" ","description":"説明","category":"分野","source":"出典"}]`},
		"見出しに月日":          {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"6月23日の出来事","description":"説明","category":"分野","source":"出典"}]`},
		"見出しにスラッシュの月日":    {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"6/23 の出来事","description":"説明","category":"分野","source":"出典"}]`},
		"説明に月日":           {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"6月23日に起きた","category":"分野","source":"出典"}]`},
		"説明が空":            {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"","category":"分野","source":"出典"}]`},
		"分野が空":            {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"説明","category":"","source":"出典"}]`},
		"出典が空":            {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":""}]`},
		"知らないフィールド":       {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典","unknown":1}]`},
		"adopted は受け付けない": {"2015.json", `[{"key":"2015-event","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典","adopted":true}]`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseEventFiles([]EventImportFile{{Name: tc.name, Data: []byte(tc.data)}}); err == nil {
				t.Error("エラーにならなかった")
			}
		})
	}
}

// TestParseEventFiles_DuplicateKeyAcrossFiles は key が全ファイルを通じて一意であることを確認する。
func TestParseEventFiles_DuplicateKeyAcrossFiles(t *testing.T) {
	files := []EventImportFile{
		{Name: "2015.json", Data: []byte(`[
			{"key":"same-key","date":"2015-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典"}
		]`)},
		{Name: "2016.json", Data: []byte(`[
			{"key":"same-key","date":"2016-01-05","headline":"見出し","description":"説明","category":"分野","source":"出典"}
		]`)},
	}
	if _, err := parseEventFiles(files); err == nil {
		t.Error("ファイルをまたいだ key の重複がエラーにならなかった")
	}
}
