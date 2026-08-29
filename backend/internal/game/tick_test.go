package game

import (
	"context"
	"testing"
	"time"
)

// TestTick_セッション外は何もしない は design.md §9.10/§9.12
// （常時起動しない構成のため、セッション外のtickは保存せず何もしない）を確認する。
//
// pool に nil を渡しても検証できる: セッション外なら DB に一切触れずに
// 早期returnするはず（もし触れれば nil pool の参照でエラーか panic になる）。
func TestTick_セッション外は何もしない(t *testing.T) {
	sessionSvc := NewSessionService(nil, RealClock{}, SessionConfig{})
	tradeSvc := NewTradeService(nil, RealClock{}, sessionSvc)
	liquidationSvc := NewLiquidationService(nil, RealClock{}, tradeSvc)
	tickSvc := NewTickService(nil, RealClock{}, sessionSvc, liquidationSvc)

	now := time.Date(2026, 1, 1, 3, 0, 0, 0, jst) // 深夜3時JST。セッション外。
	if err := tickSvc.Tick(context.Background(), now); err != nil {
		t.Errorf("Tick(セッション外) がエラーを返した: %v", err)
	}
}
