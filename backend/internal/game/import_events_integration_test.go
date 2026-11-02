package game

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestEventImport_ReimportIsIdempotent は import-events を同じファイルで 2 回実行しても
// 行が増えず、既存の行（採用状態・説明）を上書きしないことを確認する（#116 完了条件）。
func TestEventImport_ReimportIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const source = "import-events-test-116"
	// 前回の失敗で残った行を先に消す（このテスト専用の source だけ）。
	if _, err := pool.Exec(ctx, `DELETE FROM historical_events WHERE source = $1`, source); err != nil {
		t.Fatalf("pre-cleanup: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM historical_events WHERE source = $1`, source); err != nil {
			t.Errorf("cleanup historical_events: %v", err)
		}
	})

	file := `[
		{
			"date": "1999-01-05",
			"headline": "日まで分かる出来事",
			"description": "テスト用の説明",
			"category": "テスト",
			"source": "` + source + `",
			"sourceUrl": "https://example.invalid/event",
			"adopted": true
		},
		{
			"date": "1999-02",
			"headline": "月まで分かる出来事",
			"description": "テスト用の説明",
			"category": "テスト",
			"source": "` + source + `"
		}
	]`

	svc := NewEventImportService(pool)
	first, err := svc.ImportFile(ctx, strings.NewReader(file))
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.Inserted != 2 || first.AlreadyExisted != 0 {
		t.Fatalf("first import = %+v, want inserted 2 / existed 0", first)
	}

	// 既存の行を直接変えて、再実行で上書きされないことを確かめる。
	if _, err := pool.Exec(ctx, `
		UPDATE historical_events SET description = '手で直した説明', adopted = false
		WHERE source = $1 AND headline = '日まで分かる出来事'`, source); err != nil {
		t.Fatalf("update existing event: %v", err)
	}

	second, err := svc.ImportFile(ctx, strings.NewReader(file))
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Inserted != 0 || second.AlreadyExisted != 2 {
		t.Fatalf("second import = %+v, want inserted 0 / existed 2", second)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM historical_events WHERE source = $1`, source).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 2 {
		t.Errorf("historical_events = %d 行, want 2 行（再実行で増えない）", count)
	}

	var description string
	var adopted bool
	if err := pool.QueryRow(ctx, `
		SELECT description, adopted FROM historical_events
		WHERE source = $1 AND headline = '日まで分かる出来事'`, source).Scan(&description, &adopted); err != nil {
		t.Fatalf("get event: %v", err)
	}
	if description != "手で直した説明" || adopted {
		t.Errorf("既存の行が上書きされた: description=%q adopted=%v", description, adopted)
	}
}
