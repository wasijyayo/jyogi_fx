package game

import (
	"testing"

	"fxgame/backend/internal/db"
)

// TestFiredEvents_未来のイベントを漏らさない は #45 の最重要仕様
// 「events は寄り付き時に先に抽選され fire_tick が未来の行もある。API が返すのは
// 発火済み（resolved 済み、または fire_tick <= 現在tick）だけ」を純粋関数レベルで検証する。
func TestFiredEvents_未来のイベントを漏らさない(t *testing.T) {
	const (
		targetCurrency  = int64(1)
		otherCurrency   = int64(2)
		currentTick     = int64(10)
	)

	events := []db.Event{
		{ID: 1, CurrencyID: targetCurrency, Type: EventTypeShock, FireTick: 3, Resolved: false},   // 発火済み（fire_tick<=current）
		{ID: 2, CurrencyID: targetCurrency, Type: EventTypeShock, FireTick: 50, Resolved: false},  // 未来・未解決 → 漏らす
		{ID: 3, CurrencyID: targetCurrency, Type: EventTypeVolUp, FireTick: 60, Resolved: true},   // 未来でも resolved → 含める
		{ID: 4, CurrencyID: otherCurrency, Type: EventTypeShock, FireTick: 3, Resolved: false},    // 別通貨 → 含めない
	}

	got := firedEvents(events, targetCurrency, currentTick)

	if len(got) != 2 {
		t.Fatalf("firedEvents の件数 = %d, want 2", len(got))
	}
	ids := []int64{got[0].ID, got[1].ID}
	if ids[0] != 1 || ids[1] != 3 {
		t.Errorf("返ったID = %v, want [1 3]（未来の未解決イベントID=2と別通貨ID=4は含めない）", ids)
	}
}

// TestFiredEvents_現在tickちょうどは含める は「fire_tick <= currentTick」の境界を確認する。
func TestFiredEvents_現在tickちょうどは含める(t *testing.T) {
	events := []db.Event{
		{ID: 1, CurrencyID: 1, Type: EventTypeShock, FireTick: 10, Resolved: false},
		{ID: 2, CurrencyID: 1, Type: EventTypeShock, FireTick: 11, Resolved: false},
	}

	got := firedEvents(events, 1, 10)

	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("firedEvents = %+v, want fire_tick=10 の1件だけ", got)
	}
}

// TestFiredEvents_空でも壊れない は空スライス入力で空を返すことを確認する（非nilを保証）。
func TestFiredEvents_空でも壊れない(t *testing.T) {
	got := firedEvents(nil, 1, 0)
	if got == nil {
		t.Error("firedEvents(nil) = nil, want 空スライス（JSON で [] になるため）")
	}
	if len(got) != 0 {
		t.Errorf("firedEvents(nil) の件数 = %d, want 0", len(got))
	}
}
