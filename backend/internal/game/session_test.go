package game

import (
	"testing"
	"time"
)

func TestSessionConfig_IsSessionOpen(t *testing.T) {
	cfg := SessionConfig{}

	tests := []struct {
		name string
		time time.Time
		want bool
	}{
		{"開始ちょうど(12:00 JST)", time.Date(2026, 1, 1, 12, 0, 0, 0, jst), true},
		{"セッション中(12:30 JST)", time.Date(2026, 1, 1, 12, 30, 0, 0, jst), true},
		{"終了直前(12:59:59 JST)", time.Date(2026, 1, 1, 12, 59, 59, 0, jst), true},
		{"終了ちょうど(13:00 JST)はセッション外", time.Date(2026, 1, 1, 13, 0, 0, 0, jst), false},
		{"開始直前(11:59 JST)はセッション外", time.Date(2026, 1, 1, 11, 59, 0, 0, jst), false},
		{"深夜はセッション外", time.Date(2026, 1, 1, 3, 0, 0, 0, jst), false},
		{"UTCで渡されても内部でJST変換して判定する(UTC 3:00=JST 12:00)", time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.IsSessionOpen(tt.time); got != tt.want {
				t.Errorf("IsSessionOpen(%s) = %v, want %v", tt.time, got, tt.want)
			}
		})
	}
}

func TestSessionConfig_AlwaysOpen(t *testing.T) {
	cfg := SessionConfig{AlwaysOpen: true}

	// 深夜・終了1分前どちらもtrueになること（開発モードの目的そのもの）。
	midnight := time.Date(2026, 1, 1, 3, 0, 0, 0, jst)
	if !cfg.IsSessionOpen(midnight) {
		t.Error("AlwaysOpen=true なのに深夜がセッション外と判定された")
	}
	if !cfg.IsNewOrderAllowed(midnight) {
		t.Error("AlwaysOpen=true なのに深夜の新規注文が拒否された")
	}
}

func TestSessionConfig_IsNewOrderAllowed(t *testing.T) {
	cfg := SessionConfig{}

	tests := []struct {
		name string
		time time.Time
		want bool
	}{
		{"セッション中盤(12:30)は許可", time.Date(2026, 1, 1, 12, 30, 0, 0, jst), true},
		{"終了1分前の境目直前(12:58:59)は許可", time.Date(2026, 1, 1, 12, 58, 59, 0, jst), true},
		{"終了1分前(12:59:00)は拒否（確定 #14/#48）", time.Date(2026, 1, 1, 12, 59, 0, 0, jst), false},
		{"終了1分前台の途中(12:59:30)も拒否", time.Date(2026, 1, 1, 12, 59, 30, 0, jst), false},
		{"セッション外(13:00)は拒否", time.Date(2026, 1, 1, 13, 0, 0, 0, jst), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.IsNewOrderAllowed(tt.time); got != tt.want {
				t.Errorf("IsNewOrderAllowed(%s) = %v, want %v", tt.time, got, tt.want)
			}
		})
	}
}

func TestElapsedTicks(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		now  time.Time
		want int64
	}{
		{"同時刻なら0", epoch, 0},
		{"1分後なら1", epoch.Add(1 * time.Minute), 1},
		{"1分未満は切り捨てて0", epoch.Add(59 * time.Second), 0},
		{"epochより前なら0（時刻の巻き戻り対策）", epoch.Add(-1 * time.Minute), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := elapsedTicks(epoch, tt.now); got != tt.want {
				t.Errorf("elapsedTicks = %d, want %d", got, tt.want)
			}
		})
	}
}
