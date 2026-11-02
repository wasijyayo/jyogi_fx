package server_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"fxgame/backend/internal/db"
)

// mutableClock はテストの途中で時刻を進められるクロック（CLAUDE.md §5.1）。
type mutableClock struct{ now time.Time }

func (c *mutableClock) Now() time.Time { return c.now }

// TestReplayAPI_EventsHiddenUntilPublished は、状態取得が未公開の日の出来事を返さず、
// 公開の境界の前後で切り替わることを確認する（#116 完了条件）。併せて、開始の応答と
// 新聞の一覧に日付・day_index が含まれないこと、未採用・区間外・日の精度なしの出来事が
// カットインに出ないことを確認する。
func TestReplayAPI_EventsHiddenUntilPublished(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	const source = "server-events-test-116"

	// 出来事の後始末は seedReplayRates の後始末（replay_game_events・replay_games の削除）
	// より先に登録する（LIFO なので後から登録した seedReplayRates 側が先に走る）。
	if _, err := pool.Exec(ctx, `DELETE FROM historical_events WHERE source = $1`, source); err != nil {
		t.Fatalf("pre-cleanup historical_events: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM historical_events WHERE source = $1`, source); err != nil {
			t.Errorf("cleanup historical_events: %v", err)
		}
	})

	pairs := seedReplayRates(t, pool)
	_ = pairs

	dates := replayWeekdays(time.Date(2015, 1, 5, 0, 0, 0, 0, time.UTC), 80)
	q := db.New(pool)

	insertEvent := func(headline string, precision string, eventDate time.Time, dateValid, adopted bool) int64 {
		t.Helper()
		event, err := q.CreateHistoricalEvent(ctx, db.CreateHistoricalEventParams{
			Year:          2015,
			EventDate:     pgtype.Date{Time: eventDate, Valid: dateValid},
			DatePrecision: precision,
			Headline:      headline,
			Description:   "テスト用の説明",
			Category:      "テスト",
			Source:        source,
			Adopted:       adopted,
		})
		if err != nil {
			t.Fatalf("insert event %q: %v", headline, err)
		}
		return event.ID
	}

	// 新聞に載る採用済みの出来事（id と見出し）。並びの期待値は md5(id::text) 順で作る。
	type newspaperEvent struct {
		id       int64
		headline string
	}
	adopted := make([]newspaperEvent, 0, 5)
	insertAdopted := func(headline string, precision string, eventDate time.Time, dateValid bool) {
		t.Helper()
		id := insertEvent(headline, precision, eventDate, dateValid, true)
		adopted = append(adopted, newspaperEvent{id: id, headline: headline})
	}

	// 取引のない土曜。次の共通営業日（day_index 20）に割り当てる。
	insertAdopted("土曜日の出来事", "day", time.Date(2015, 1, 31, 0, 0, 0, 0, time.UTC), true)
	// 区間の 2 日目（day_index 21）。
	insertAdopted("区間2日目の出来事", "day", dates[21], true)
	// 月までしか分からない。新聞だけに載せる。
	insertAdopted("月までの出来事", "month", time.Time{}, false)
	// 未採用。新聞にもカットインにも出さない。
	insertEvent("未採用の出来事", "day", dates[21], true, false)
	// 区間の外（最後の共通営業日より後）。新聞だけに載せる。
	insertAdopted("区間外の出来事", "day", time.Date(2015, 12, 31, 0, 0, 0, 0, time.UTC), true)
	// 開始前の 20 日（day_index 5）。新聞だけに載せる。
	insertAdopted("開始前の出来事", "day", dates[5], true)

	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: now}
	mux := newReplayMux(pool, clock)

	// 開始。新聞に開始年の採用済みの出来事が、日付なしで全件入る。
	start := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/replay/games", strings.NewReader(`{"guestName":"tester"}`))
	mux.ServeHTTP(start, req)
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d, body=%s", start.Code, start.Body.String())
	}
	if datePattern.MatchString(start.Body.String()) {
		t.Errorf("start response contains a date string: %s", start.Body.String())
	}
	if strings.Contains(start.Body.String(), "dayIndex") || strings.Contains(start.Body.String(), "day_index") {
		t.Errorf("start response contains day_index: %s", start.Body.String())
	}

	var started struct {
		GameId    int64  `json:"gameId"`
		Token     string `json:"token"`
		Newspaper []struct {
			Headline    string `json:"headline"`
			Description string `json:"description"`
		} `json:"newspaper"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	wantNewspaper := map[string]bool{
		"土曜日の出来事":   true,
		"区間2日目の出来事": true,
		"月までの出来事":   true,
		"区間外の出来事":   true,
		"開始前の出来事":   true,
	}
	if len(started.Newspaper) != len(wantNewspaper) {
		t.Fatalf("newspaper = %+v, want %d 件", started.Newspaper, len(wantNewspaper))
	}
	for _, event := range started.Newspaper {
		if !wantNewspaper[event.Headline] {
			t.Errorf("新聞に想定外の出来事が入っている: %+v", event)
		}
		if event.Description == "" {
			t.Errorf("新聞の説明が空: %+v", event)
		}
	}

	// 並びは md5(id::text) による再現可能な固定順で、日付順でも取り込み順でもない
	// （日付の前後関係を推測させない）。DB と同じ md5 を Go 側でも計算して期待値にする。
	sort.Slice(adopted, func(i, j int) bool {
		return newspaperOrderKey(adopted[i].id) < newspaperOrderKey(adopted[j].id)
	})
	for i, want := range adopted {
		if started.Newspaper[i].Headline != want.headline {
			t.Errorf("newspaper[%d] = %q, want %q（md5(id::text) 順で返す）",
				i, started.Newspaper[i].Headline, want.headline)
		}
	}

	auth := "Bearer " + started.Token
	getState := func() struct {
		PublishedDays int `json:"publishedDays"`
		Events        []struct {
			Id       int64  `json:"id"`
			Headline string `json:"headline"`
		} `json:"events"`
	} {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/replay/games/"+itoa(started.GameId), nil)
		req.Header.Set("Authorization", auth)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("state status = %d, body=%s", rec.Code, rec.Body.String())
		}
		if datePattern.MatchString(rec.Body.String()) {
			t.Errorf("state response contains a date string: %s", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "dayIndex") || strings.Contains(rec.Body.String(), "day_index") {
			t.Errorf("state response contains day_index: %s", rec.Body.String())
		}
		var state struct {
			PublishedDays int `json:"publishedDays"`
			Events        []struct {
				Id       int64  `json:"id"`
				Headline string `json:"headline"`
			} `json:"events"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
			t.Fatalf("decode state: %v", err)
		}
		return state
	}

	headlines := func(events []struct {
		Id       int64  `json:"id"`
		Headline string `json:"headline"`
	}) []string {
		out := make([]string, 0, len(events))
		for _, e := range events {
			out = append(out, e.Headline)
		}
		return out
	}

	if got := getState(); len(got.Events) != 0 {
		t.Fatalf("ready state events = %+v, want empty", got.Events)
	}

	begin := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/replay/games/"+itoa(started.GameId)+"/begin", nil)
	req.Header.Set("Authorization", auth)
	mux.ServeHTTP(begin, req)
	if begin.Code != http.StatusOK {
		t.Fatalf("begin status = %d, body=%s", begin.Code, begin.Body.String())
	}

	// 公開 0 日（currentDay 19）: 出来事はまだ無い。
	if got := getState(); len(got.Events) != 0 {
		t.Fatalf("published 0 events = %+v, want empty", got.Events)
	}

	// 公開 1 日（currentDay 20）: 土曜日の出来事だけが出る。翌日の出来事はまだ。
	clock.now = now.Add(5*time.Second + 3*time.Second)
	got := getState()
	if got.PublishedDays != 1 {
		t.Fatalf("publishedDays = %d, want 1", got.PublishedDays)
	}
	if h := headlines(got.Events); len(h) != 1 || h[0] != "土曜日の出来事" {
		t.Errorf("published 1 events = %v, want [土曜日の出来事]", h)
	}

	// 公開 2 日（currentDay 21）: 区間 2 日目の出来事も出る。順番は公開日の順。
	clock.now = now.Add(5*time.Second + 6*time.Second)
	got = getState()
	if got.PublishedDays != 2 {
		t.Fatalf("publishedDays = %d, want 2", got.PublishedDays)
	}
	if h := headlines(got.Events); len(h) != 2 || h[0] != "土曜日の出来事" || h[1] != "区間2日目の出来事" {
		t.Errorf("published 2 events = %v, want [土曜日の出来事 区間2日目の出来事]", h)
	}
}

// newspaperOrderKey は Postgres の md5(id::text) と同じ並びを作るためのキー。
// 新聞の並びが日付順・取り込み順になっていないことを、DB と同じ計算で確かめる。
func newspaperOrderKey(id int64) string {
	sum := md5.Sum([]byte(strconv.FormatInt(id, 10)))
	return hex.EncodeToString(sum[:])
}
