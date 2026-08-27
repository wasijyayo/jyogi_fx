package game

import "time"

// Clock は時刻を注入可能にするための抽象化（CLAUDE.md §5.1）。
// internal/game 配下では time.Now() を直接呼ばず、必ずこれ経由にする。
// テストでは固定時刻・倍速クロックを差し込む。
type Clock interface {
	Now() time.Time
}

// RealClock は本番用の実装。system-clock をそのまま返す。
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
