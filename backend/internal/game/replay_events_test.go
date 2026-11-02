package game

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"fxgame/backend/internal/db"
)

// historicalEventForTest は日まで確定した出来事を組み立てる。
func historicalEventForTest(id int64, date time.Time, adopted bool) db.HistoricalEvent {
	return db.HistoricalEvent{
		ID:            id,
		Year:          int32(date.Year()),
		EventDate:     pgtype.Date{Time: date, Valid: true},
		DatePrecision: "day",
		Headline:      "見出し",
		Description:   "説明",
		Adopted:       adopted,
	}
}

// TestReplayEventAssignments は出来事の区間への割り当て（#116 完了条件）を確認する。
// 取引のない日は次の共通営業日、区間の外・開始前・日の精度なし・未採用は対象外。
func TestReplayEventAssignments(t *testing.T) {
	// 2015-01-05（月）から 80 営業日。day_index 20 は 2015-02-02（月）。
	days := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	if got := days[20].Format("2006-01-02"); got != "2015-02-02" {
		t.Fatalf("day 20 = %s, want 2015-02-02", got)
	}

	monthPrecision := db.HistoricalEvent{
		ID:            30,
		Year:          2015,
		DatePrecision: "month",
		Headline:      "月までしか分からない出来事",
		Adopted:       true,
	}
	unadopted := historicalEventForTest(6, days[21], false)
	unadopted.Headline = "未採用の出来事"
	events := []db.HistoricalEvent{
		// 区間の 2 日目（day_index 21）。そのまま割り当てる。
		historicalEventForTest(1, days[21], true),
		// 取引のない土曜（2015-01-31）。次の共通営業日 = day_index 20。
		historicalEventForTest(2, time.Date(2015, 1, 31, 0, 0, 0, 0, time.UTC), true),
		// 日曜。同じく day_index 20。同じ日はイベント ID 順。
		historicalEventForTest(3, time.Date(2015, 2, 1, 0, 0, 0, 0, time.UTC), true),
		// 開始前の 20 日（day_index 5）に当たる。区間の外なので対象外。
		historicalEventForTest(4, days[5], true),
		// 区間の最後の共通営業日より後。割り当て先が無いので対象外。
		historicalEventForTest(5, time.Date(2015, 12, 31, 0, 0, 0, 0, time.UTC), true),
		// 未採用。ゲームは使わない。
		unadopted,
		// 月までしか分からない。新聞だけに載せる。
		monthPrecision,
	}

	got := replayEventAssignments(days, events)
	want := []replayEventAssignment{
		{EventID: 2, DayIndex: 20},
		{EventID: 3, DayIndex: 20},
		{EventID: 1, DayIndex: 21},
	}
	if len(got) != len(want) {
		t.Fatalf("assignments = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("assignments[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// 新聞は採用済みを日付の精度を問わず返し、未採用は返さない。日付は持たない。
	newspaper := replayNewspaperEvents(events)
	if len(newspaper) != 6 {
		t.Fatalf("newspaper = %d 件, want 6 件（未採用の 1 件を除く）", len(newspaper))
	}
	for _, n := range newspaper {
		if n.Headline == events[5].Headline {
			t.Error("未採用の出来事が新聞に含まれている")
		}
	}
}

// TestReplayEventAssignments_Empty は出来事が 0 件でも空の結果を返すことを確認する
// （出来事が 0 件の年でもゲームが動く。#116）。
func TestReplayEventAssignments_Empty(t *testing.T) {
	days := replayTestDates(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	if got := replayEventAssignments(days, nil); len(got) != 0 {
		t.Errorf("assignments = %+v, want empty", got)
	}
	if got := replayNewspaperEvents(nil); len(got) != 0 {
		t.Errorf("newspaper = %+v, want empty", got)
	}
}
