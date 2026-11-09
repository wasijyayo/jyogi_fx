package game

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// eventImportTestCleanup はテスト専用の source の行を先に消し、後始末を登録する。
// key は全ファイルを通じて一意なので、テストごとに別の key を使う。
func eventImportTestCleanup(t *testing.T, ctx context.Context, pool *pgxpool.Pool, source string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM historical_events WHERE source = $1`, source); err != nil {
		t.Fatalf("pre-cleanup: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM historical_events WHERE source = $1`, source); err != nil {
			t.Errorf("cleanup historical_events: %v", err)
		}
	})
}

// eventImportTestFile はテスト用の 1 ファイル分の JSON を組み立てる。
func eventImportTestFile(year string, records ...string) EventImportFile {
	return EventImportFile{
		Name: year + ".json",
		Data: []byte("[" + strings.Join(records, ",") + "]"),
	}
}

// eventImportTestRecord はテスト用の出来事 1 件分の JSON を組み立てる。
func eventImportTestRecord(key, date, headline, description, source string) string {
	return `{"key":"` + key + `","date":"` + date + `","headline":"` + headline +
		`","description":"` + description + `","category":"テスト","source":"` + source + `"}`
}

// queryImportedEvent は key で出来事の見出し・説明・採用状態を読む。
func queryImportedEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string) (headline, description string, adopted bool) {
	t.Helper()
	if err := pool.QueryRow(ctx, `
		SELECT headline, description, adopted FROM historical_events WHERE key = $1`, key).
		Scan(&headline, &description, &adopted); err != nil {
		t.Fatalf("get event %q: %v", key, err)
	}
	return headline, description, adopted
}

// TestEventImport_SyncsFilesAsSourceOfTruth は、再実行で既存の行を全項目上書きし、
// ファイルから消した key の採用を外す（削除はしない）ことを確認する（#116 完了条件）。
func TestEventImport_SyncsFilesAsSourceOfTruth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const source = "import-events-test-116-sync"
	const keyA = "test-116-sync-a"
	const keyB = "test-116-sync-b"
	eventImportTestCleanup(t, ctx, pool, source)

	svc := NewEventImportService(pool)

	first := []EventImportFile{eventImportTestFile("2015",
		eventImportTestRecord(keyA, "2015-01-05", "最初の見出し", "最初の説明", source),
		eventImportTestRecord(keyB, "2015-02", "残す見出し", "残す説明", source),
	)}
	res, err := svc.Import(ctx, first)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if res.Skipped || res.Upserted != 2 {
		t.Fatalf("first import = %+v, want upserted 2", res)
	}
	if _, _, adopted := queryImportedEvent(t, ctx, pool, keyA); !adopted {
		t.Error("取り込んだ行が採用済みになっていない")
	}

	// keyA は見出し・説明を直し、keyB はファイルから消す。
	second := []EventImportFile{eventImportTestFile("2015",
		eventImportTestRecord(keyA, "2015-01-06", "直した見出し", "直した説明", source),
	)}
	res, err = svc.Import(ctx, second)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res.Skipped || res.Upserted != 1 || res.Unadopted < 1 {
		t.Fatalf("second import = %+v, want upserted 1 / unadopted >= 1", res)
	}

	headline, description, adopted := queryImportedEvent(t, ctx, pool, keyA)
	if headline != "直した見出し" || description != "直した説明" || !adopted {
		t.Errorf("既存の行が上書きされていない: headline=%q description=%q adopted=%v",
			headline, description, adopted)
	}

	// ファイルから消した行は削除せず、採用だけ外す（replay_game_events が参照するため）。
	_, _, adoptedB := queryImportedEvent(t, ctx, pool, keyB)
	if adoptedB {
		t.Error("ファイルから消した行の採用が外れていない")
	}
}

// TestEventImport_ValidationFailureChangesNothing は、1 件でも検証に失敗したら
// 何も変えないことを確認する。
func TestEventImport_ValidationFailureChangesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const source = "import-events-test-116-validation"
	const keyA = "test-116-validation-a"
	const keyB = "test-116-validation-b"
	eventImportTestCleanup(t, ctx, pool, source)

	svc := NewEventImportService(pool)

	initial := []EventImportFile{eventImportTestFile("2015",
		eventImportTestRecord(keyA, "2015-01-05", "元の見出し", "元の説明", source),
		eventImportTestRecord(keyB, "2015-02", "残す見出し", "残す説明", source),
	)}
	if _, err := svc.Import(ctx, initial); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	// keyA の有効な修正の後ろに、日付が不正な行を置く。
	invalid := []EventImportFile{eventImportTestFile("2015",
		eventImportTestRecord(keyA, "2015-01-06", "直した見出し", "直した説明", source),
		eventImportTestRecord("test-116-validation-c", "2015/01/07", "不正な日付", "説明", source),
	)}
	if _, err := svc.Import(ctx, invalid); err == nil {
		t.Fatal("検証に失敗するはずの取り込みが成功した")
	}

	headline, description, adopted := queryImportedEvent(t, ctx, pool, keyA)
	if headline != "元の見出し" || description != "元の説明" || !adopted {
		t.Errorf("検証失敗時に既存の行が変わった: headline=%q description=%q adopted=%v",
			headline, description, adopted)
	}
	if _, _, adoptedB := queryImportedEvent(t, ctx, pool, keyB); !adoptedB {
		t.Error("検証失敗時に既存の行の採用が外れた")
	}
}

// TestEventImport_ZeroEventsChangesNothing は、ファイルが 0 個・出来事が 0 件のときに
// 全件の採用を外さないことを確認する。
func TestEventImport_ZeroEventsChangesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const source = "import-events-test-116-zero"
	const key = "test-116-zero-a"
	eventImportTestCleanup(t, ctx, pool, source)

	svc := NewEventImportService(pool)

	if _, err := svc.Import(ctx, []EventImportFile{eventImportTestFile("2015",
		eventImportTestRecord(key, "2015-01-05", "見出し", "説明", source),
	)}); err != nil {
		t.Fatalf("initial import: %v", err)
	}

	// 出来事が 0 件のファイル。
	res, err := svc.Import(ctx, []EventImportFile{eventImportTestFile("2015")})
	if err != nil {
		t.Fatalf("empty file import: %v", err)
	}
	if !res.Skipped {
		t.Errorf("empty file import = %+v, want skipped", res)
	}
	if _, _, adopted := queryImportedEvent(t, ctx, pool, key); !adopted {
		t.Error("出来事が 0 件のときに既存の行の採用が外れた")
	}

	// ファイルが 0 個。
	res, err = svc.Import(ctx, nil)
	if err != nil {
		t.Fatalf("no files import: %v", err)
	}
	if !res.Skipped {
		t.Errorf("no files import = %+v, want skipped", res)
	}
	if _, _, adopted := queryImportedEvent(t, ctx, pool, key); !adopted {
		t.Error("ファイルが 0 個のときに既存の行の採用が外れた")
	}
}

// TestEventImport_DuplicateKeyFails は、同じ key が重複したらエラーになり、
// 何も入らないことを確認する。
func TestEventImport_DuplicateKeyFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool := connectTestDB(t, ctx)

	const source = "import-events-test-116-duplicate"
	const key = "test-116-duplicate-a"
	eventImportTestCleanup(t, ctx, pool, source)

	svc := NewEventImportService(pool)
	file := eventImportTestFile("2015",
		eventImportTestRecord(key, "2015-01-05", "見出し", "説明", source),
		eventImportTestRecord(key, "2015-01-06", "別の見出し", "別の説明", source),
	)
	if _, err := svc.Import(ctx, []EventImportFile{file}); err == nil {
		t.Fatal("key の重複がエラーにならなかった")
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM historical_events WHERE source = $1`, source).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 0 {
		t.Errorf("key の重複で %d 行入った, want 0 行", count)
	}
}
