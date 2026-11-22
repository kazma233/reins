package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reins/internal/testsupport"
)

// 对齐 Rust 版 session/summary_cache.rs 内联测试。

func cachedSummary(title string) *SessionSummary {
	return &SessionSummary{
		SourceApp:       SourceCodex,
		SourceSessionID: "test-session",
		Title:           title,
		TranscriptPath:  "/tmp/does-not-exist.jsonl",
		CreatedAt:       int64Ptr(1),
		UpdatedAt:       int64Ptr(2),
		Models:          []SessionModel{},
	}
}

func int64Ptr(value int64) *int64 { return &value }

func TestSummaryStoreThenLoadRoundTripsAndMtimeGatesHits(t *testing.T) {
	tempHome := testsupport.TestDir(t, "summary-roundtrip")
	testsupport.SetHome(t, tempHome)

	db, err := SummaryDbAtStoreDir()
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	defer db.Close()
	path := "/tmp/some-transcript.jsonl"

	// mtime 未变时命中，变化后失效。
	db.Store(SourceCodex, path, 100, cachedSummary("cached title"))
	loaded := db.Load(SourceCodex, path, 100)
	if loaded == nil || loaded.Title != "cached title" {
		t.Fatalf("cache hit expected, got %+v", loaded)
	}
	if db.Load(SourceCodex, path, 101) != nil {
		t.Error("changed mtime should miss")
	}

	// 覆盖写入后按新 mtime 命中。
	db.Store(SourceCodex, path, 200, cachedSummary("updated title"))
	reloaded := db.Load(SourceCodex, path, 200)
	if reloaded == nil || reloaded.Title != "updated title" {
		t.Fatalf("cache hit after update expected, got %+v", reloaded)
	}
	if db.Load(SourceCodex, path, 100) != nil {
		t.Error("old mtime should miss after overwrite")
	}
}

func TestSummaryRowsAreIsolatedPerSourceApp(t *testing.T) {
	tempHome := testsupport.TestDir(t, "summary-isolation")
	testsupport.SetHome(t, tempHome)

	db, err := SummaryDbAtStoreDir()
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	defer db.Close()
	path := "/tmp/some-transcript.jsonl"
	db.Store(SourceCodex, path, 100, cachedSummary("codex title"))

	if db.Load(SourceClaudeCode, path, 100) != nil {
		t.Error("other source app should not see codex rows")
	}
}

func TestSummaryClearAllInvalidatesEveryRow(t *testing.T) {
	tempHome := testsupport.TestDir(t, "summary-clear")
	testsupport.SetHome(t, tempHome)

	db, err := SummaryDbAtStoreDir()
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	defer db.Close()
	path := "/tmp/some-transcript.jsonl"
	db.Store(SourceCodex, path, 100, cachedSummary("codex title"))
	db.ClearAll()

	if db.Load(SourceCodex, path, 100) != nil {
		t.Error("clear_all must invalidate every row")
	}
}

func TestOpeningCurrentDBRemovesStaleVersionFiles(t *testing.T) {
	tempHome := testsupport.TestDir(t, "summary-stale")
	cacheDir := filepath.Join(tempHome, ".reins")
	if err := os.MkdirAll(cacheDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	testsupport.SetHome(t, tempHome)

	// 版本化之前的无后缀命名和旧版本号文件都应在打开当前库时被清掉。
	stale := []string{
		"session-summary-cache.db",
		"session-summary-cache.db-wal",
		"session-summary-cache_v1.db",
		"session-summary-cache_v1.db-shm",
	}
	for _, name := range stale {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte("stale"), 0o644); err != nil {
			t.Fatalf("write stale fixture: %v", err)
		}
	}

	db, err := SummaryDbAtStoreDir()
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	defer db.Close()
	path := "/tmp/some-transcript.jsonl"
	db.Store(SourceCodex, path, 100, cachedSummary("codex title"))

	remaining, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, entry := range remaining {
		if !strings.HasPrefix(entry.Name(), SummaryDBFileName) {
			t.Errorf("stale cache db file survived: %s", entry.Name())
		}
	}
}
