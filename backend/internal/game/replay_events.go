package game

import (
	"sort"
	"time"

	"fxgame/backend/internal/db"
)

// 過去チャートモード（design.md §13）の出来事（#116 REPLAY-6）の純粋関数。
// 出来事そのものは historical_events の行として持ち、コードに見出しや日付を書かない。

// ReplayNewspaperEvent は開始時の新聞に載せる出来事 1 件（見出しと説明のみ）。
// 日付・月日・day_index は持たない（開始年だけを公開する。design.md §13「先読み対策」）。
type ReplayNewspaperEvent struct {
	Headline    string
	Description string
}

// ReplayCutInEvent はプレイ中にカットインで出す出来事 1 件。
// ID はゲーム内の対応（replay_game_events.id）で、同じ出来事を 1 回だけ出すために使う。
type ReplayCutInEvent struct {
	ID       int64
	Headline string
}

// replayEventAssignment は採用済みの出来事を区間の営業日（day_index）に割り当てた結果。
type replayEventAssignment struct {
	EventID  int64
	DayIndex int32
}

// replayEventAssignments は日まで確定した採用済みの出来事を、区間の営業日へ割り当てる。
//
// 取引のない日（週末・祝日など）の出来事は、その日以降で最初に取引がある共通営業日
// （days の要素）に割り当てる。割り当て先が開始前の 20 日（day_index 0..19）や区間の
// 外になる場合は割り当てない（カットインの対象は day_index 20..79 の出来事だけ）。
// 月・年までしか分からない出来事、未採用の出来事も対象外（新聞だけに載せる）。
//
// events は日付順である必要はない。戻り値は day_index 昇順、同じ日はイベント ID 順。
func replayEventAssignments(days []time.Time, events []db.HistoricalEvent) []replayEventAssignment {
	if len(days) == 0 || len(events) == 0 {
		return nil
	}

	// days は日付順（replay_game_days の通し番号順）で渡される。
	assignments := make([]replayEventAssignment, 0, len(events))
	for _, event := range events {
		if !event.Adopted || event.DatePrecision != "day" || !event.EventDate.Valid {
			continue
		}
		eventDate := dateOnlyUTC(event.EventDate.Time)

		// その日以降で最初に取引がある共通営業日を探す。
		index := sort.Search(len(days), func(i int) bool {
			return !dateOnlyUTC(days[i]).Before(eventDate)
		})
		if index >= len(days) {
			// 区間の最後の共通営業日より後の出来事。割り当てない。
			continue
		}
		if index < replayPreDays {
			// 開始前の 20 日に当たる出来事。区間の外なので割り当てない。
			continue
		}
		assignments = append(assignments, replayEventAssignment{
			EventID:  event.ID,
			DayIndex: int32(index),
		})
	}

	sort.SliceStable(assignments, func(i, j int) bool {
		if assignments[i].DayIndex != assignments[j].DayIndex {
			return assignments[i].DayIndex < assignments[j].DayIndex
		}
		return assignments[i].EventID < assignments[j].EventID
	})
	return assignments
}

// replayNewspaperEvents は開始年の採用済みの出来事を、新聞に載せる形（見出し・説明のみ）に
// 変換する。日付の精度は問わない（月・年までしか分からない出来事も載せる）。
// 日付は返さない。並びは呼び出し側が決めた順（新聞は取り込み順 = id 順）を保つ。
func replayNewspaperEvents(events []db.HistoricalEvent) []ReplayNewspaperEvent {
	newspaper := make([]ReplayNewspaperEvent, 0, len(events))
	for _, event := range events {
		if !event.Adopted {
			continue
		}
		newspaper = append(newspaper, ReplayNewspaperEvent{
			Headline:    event.Headline,
			Description: event.Description,
		})
	}
	return newspaper
}
