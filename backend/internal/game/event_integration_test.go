package game

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"fxgame/backend/internal/db"
)

// TestStoreSessionEvents_抽選結果がDBに保存され再抽選しない は #40 完了条件
// 「抽選結果がDBに保存され、tick処理が再抽選しないこと」を確認する。
//
// insertTestCurrency で作った使い捨て通貨に対して直接 StoreSessionEvents を呼ぶ
// （SessionService.OpenSession経由だと実通貨JOG/WASI/CHEBUも巻き込みBasePriceの
// コストが乗るため、tick_integration_test.goと同じ理由で避ける）。
func TestStoreSessionEvents_抽選結果がDBに保存され再抽選しない(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	epoch := time.Date(2099, 7, 1, 12, 0, 0, 0, jst)
	c := insertTestCurrency(t, ctx, pool, "EVENTSTORE", 999501, epoch)

	sessionDate := pgtype.Date{Time: time.Date(2099, 7, 1, 0, 0, 0, 0, jst), Valid: true}
	session, err := q.CreateGameSession(ctx, db.CreateGameSessionParams{
		Date:     sessionDate,
		Seed:     123456789,
		OpenedAt: pgtype.Timestamptz{Time: epoch, Valid: true},
		ClosedAt: pgtype.Timestamptz{Time: epoch.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateGameSession: %v", err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM events WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE currency_id = $1`, c.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM currencies WHERE id = $1`, c.ID)
	})

	currencies := []db.Currency{c}
	want := DrawSessionEvents(session.Seed, len(currencies))
	if len(want) == 0 {
		t.Fatal("DrawSessionEvents が0件を返した（発生回数は必ず1〜3のはず）")
	}

	if err := StoreSessionEvents(ctx, q, session, currencies); err != nil {
		t.Fatalf("StoreSessionEvents (1回目): %v", err)
	}

	firstRun, err := q.ListEventsBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListEventsBySession: %v", err)
	}
	if len(firstRun) != len(want) {
		t.Fatalf("1回目後のevents件数 = %d, want %d（DrawSessionEventsの結果と一致するはず）",
			len(firstRun), len(want))
	}

	// ListEventsBySession は fire_tick 昇順で返る一方 DrawSessionEvents は抽選順
	// （必ずしもfire_tick順ではない）なので、インデックス対応ではなく
	// fire_tickをキーに一致を確認する。
	sessionOffset := elapsedTicks(c.EpochAt.Time, session.OpenedAt.Time)
	byFireTick := make(map[int64]db.Event, len(firstRun))
	for _, e := range firstRun {
		byFireTick[e.FireTick] = e
	}
	for _, w := range want {
		wantFireTick := sessionOffset + w.RelativeFireTick
		e, ok := byFireTick[wantFireTick]
		if !ok {
			t.Errorf("fire_tick=%d（RelativeFireTick=%d）の行が保存されていない", wantFireTick, w.RelativeFireTick)
			continue
		}
		if e.CurrencyID != c.ID {
			t.Errorf("fire_tick=%dのCurrencyID = %d, want %d", wantFireTick, e.CurrencyID, c.ID)
		}
	}

	// --- #40完了条件: tick処理（の再実行）が再抽選しない ---
	if err := StoreSessionEvents(ctx, q, session, currencies); err != nil {
		t.Fatalf("StoreSessionEvents (2回目・再実行): %v", err)
	}
	secondRun, err := q.ListEventsBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListEventsBySession (2回目後): %v", err)
	}
	if len(secondRun) != len(firstRun) {
		t.Errorf("2回目のStoreSessionEvents後にevents件数が変わった: %d → %d（再抽選されてしまった）",
			len(firstRun), len(secondRun))
	}
	for i := range firstRun {
		if firstRun[i].ID != secondRun[i].ID {
			t.Errorf("events[%d]のIDが変わった: %d → %d（再抽選で別行が作られた）",
				i, firstRun[i].ID, secondRun[i].ID)
		}
	}
}

// TestOpenSession_その日のイベントが抽選される は、SessionService.OpenSession
// （寄り付き処理）が実際にイベント抽選を行うところまで通しで確認する
// （design.md §5.1「セッション開始時にその日のイベントを抽選してDBに書き込む」）。
func TestOpenSession_その日のイベントが抽選される(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)
	q := db.New(pool)

	before, err := q.ListCurrencies(ctx)
	if err != nil {
		t.Fatalf("ListCurrencies (snapshot): %v", err)
	}
	if len(before) == 0 {
		t.Skip("currencies が投入されていないためスキップ（#31 のマイグレーション未適用）")
	}

	// 実運用と衝突しない明確に架空の未来日（TestOpenSessionと同じ2099年だが
	// 日付を変えて競合を避ける）。
	now := time.Date(2099, 2, 1, 12, 0, 0, 0, jst)

	svc := NewSessionService(pool, RealClock{}, SessionConfig{})
	session, err := svc.OpenSession(ctx, now)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() {
		cctx := context.Background()
		_, _ = pool.Exec(cctx, `DELETE FROM events WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM price_ticks WHERE session_id = $1`, session.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM game_sessions WHERE id = $1`, session.ID)
		for _, c := range before {
			_, _ = pool.Exec(cctx,
				`UPDATE currencies SET pressure = $2, pressure_at = $3 WHERE id = $1`,
				c.ID, c.Pressure, c.PressureAt)
		}
	})

	events, err := q.ListEventsBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("ListEventsBySession: %v", err)
	}
	// 発生回数は必ず1〜3件（design.md §5.1の確定分布）。
	if len(events) < 1 || len(events) > 3 {
		t.Errorf("OpenSession後のevents件数 = %d, want 1〜3", len(events))
	}
	for _, e := range events {
		found := false
		for _, c := range before {
			if c.ID == e.CurrencyID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("event(id=%d)のCurrencyID=%dが投入済み通貨のいずれとも一致しない", e.ID, e.CurrencyID)
		}
	}
}
