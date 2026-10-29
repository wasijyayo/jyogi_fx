package game

import (
	"testing"
	"time"
)

// TestSessionConfig_MarketStatus は共通ヘッダー（#99 WEB-4）用の取引時間状態の
// 計算を確認する。開催中なら残り時間、時間外なら次の開始時刻（JST）が返る。
func TestSessionConfig_MarketStatus(t *testing.T) {
	cfg := SessionConfig{}

	tests := []struct {
		name             string
		now              time.Time
		wantOpen         bool
		wantRemaining    *int
		wantNextStart    *time.Time
	}{
		{
			name:          "開始ちょうど(12:00)は残り60分",
			now:           time.Date(2026, 1, 1, 12, 0, 0, 0, jst),
			wantOpen:      true,
			wantRemaining: intPtr(60),
		},
		{
			name:          "セッション中盤(12:30)は残り30分",
			now:           time.Date(2026, 1, 1, 12, 30, 0, 0, jst),
			wantOpen:      true,
			wantRemaining: intPtr(30),
		},
		{
			name:          "終了直前(12:59)は残り1分",
			now:           time.Date(2026, 1, 1, 12, 59, 59, 0, jst),
			wantOpen:      true,
			wantRemaining: intPtr(1),
		},
		{
			name:          "開始前(11:30)は当日12:00開始",
			now:           time.Date(2026, 1, 1, 11, 30, 0, 0, jst),
			wantNextStart: timePtr(time.Date(2026, 1, 1, 12, 0, 0, 0, jst)),
		},
		{
			name:          "終了後(13:30)は翌日12:00開始",
			now:           time.Date(2026, 1, 1, 13, 30, 0, 0, jst),
			wantNextStart: timePtr(time.Date(2026, 1, 2, 12, 0, 0, 0, jst)),
		},
		{
			name:          "深夜(3:00)は当日12:00開始",
			now:           time.Date(2026, 1, 1, 3, 0, 0, 0, jst),
			wantNextStart: timePtr(time.Date(2026, 1, 1, 12, 0, 0, 0, jst)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cfg.MarketStatus(tt.now)
			if got.Open != tt.wantOpen {
				t.Errorf("Open = %v, want %v", got.Open, tt.wantOpen)
			}
			if tt.wantRemaining == nil {
				if got.RemainingMinutes != nil {
					t.Errorf("RemainingMinutes = %v, want nil", *got.RemainingMinutes)
				}
			} else if got.RemainingMinutes == nil || *got.RemainingMinutes != *tt.wantRemaining {
				t.Errorf("RemainingMinutes = %v, want %d", got.RemainingMinutes, *tt.wantRemaining)
			}
			if tt.wantNextStart == nil {
				if got.NextStartAt != nil {
					t.Errorf("NextStartAt = %v, want nil", got.NextStartAt)
				}
			} else if got.NextStartAt == nil || !got.NextStartAt.Equal(*tt.wantNextStart) {
				t.Errorf("NextStartAt = %v, want %v", got.NextStartAt, *tt.wantNextStart)
			}
		})
	}
}

// TestSessionConfig_MarketStatus_AlwaysOpen は GAME_ALWAYS_OPEN（開発モード）時の
// 表示を確認する。常に開催中で、残り時間・次の開始時刻はどちらも nil にする
// （「常時開催」の表示に使う）。
func TestSessionConfig_MarketStatus_AlwaysOpen(t *testing.T) {
	cfg := SessionConfig{AlwaysOpen: true}
	got := cfg.MarketStatus(time.Date(2026, 1, 1, 3, 0, 0, 0, jst))

	if !got.Open {
		t.Error("Open = false, want true（常時開催）")
	}
	if !got.AlwaysOpen {
		t.Error("AlwaysOpen = false, want true")
	}
	if got.RemainingMinutes != nil {
		t.Errorf("RemainingMinutes = %v, want nil（常時開催に残り時間は無い）", *got.RemainingMinutes)
	}
	if got.NextStartAt != nil {
		t.Errorf("NextStartAt = %v, want nil（常時開催に次の開始時刻は無い）", got.NextStartAt)
	}
}

func intPtr(v int) *int          { return &v }
func timePtr(v time.Time) *time.Time { return &v }
