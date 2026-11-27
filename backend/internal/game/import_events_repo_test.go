package game

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRepositoryEventFilesAreValid はリポジトリに置いた出来事ファイル
// （backend/data/events/<年>.json）を、import-events と同じ規則で検証する。
//
// 出来事のデータそのものは別 PR #119 で追加する。この PR の時点ではファイルが
// 0 個でもよい（その場合は成功する）。ファイルが増えたら CI の通常の go test で
// key の形式・重複、ファイル名の年と出来事の年の一致、日付の形式、必須項目、
// 月日の表記を確認する。
func TestRepositoryEventFilesAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "data", "events", "*.json"))
	if err != nil {
		t.Fatalf("glob event files: %v", err)
	}
	if len(paths) == 0 {
		t.Log("backend/data/events/*.json が 0 個のため、検証をスキップする（#119 で追加する）")
		return
	}

	files := make([]EventImportFile, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		files = append(files, EventImportFile{Name: filepath.Base(path), Data: data})
	}

	inputs, err := parseEventFiles(files)
	if err != nil {
		t.Fatalf("backend/data/events/*.json の検証に失敗した: %v", err)
	}
	t.Logf("backend/data/events: %d ファイル / %d 件の出来事を検証した", len(files), len(inputs))
}
