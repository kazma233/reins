package session

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"reins/internal/support"
)

// session_cache.rs 里依赖 reader/catalog 的 8 个用例：持久摘要冷启动、mtime
// 失效、pi 的内存/时间线缓存、clear/refresh 的编排语义。（跨进程持久化的
// 子进程用例已在 2a 落地，见 session_cache_test.go。）

func cacheDBPathFor(t *testing.T, tempHome string) string {
	t.Helper()
	return filepath.Join(tempHome, ".reins", SummaryDBFileName)
}

func cacheRowCount(t *testing.T, tempHome string) int64 {
	t.Helper()
	db, err := sql.Open("sqlite", cacheDBPathFor(t, tempHome))
	if err != nil {
		t.Fatalf("open cache db: %v", err)
	}
	defer db.Close()
	return mustQueryInt(t, db, "SELECT COUNT(*) FROM session_summary_cache")
}

// tamperCachedTitle：直接改写缓存行里的标题——若后续列表读到篡改值，证明
// 摘要来自持久缓存而不是重新解析转录文件。
func tamperCachedTitle(t *testing.T, tempHome string, from string, to string) {
	t.Helper()
	db, err := sql.Open("sqlite", cacheDBPathFor(t, tempHome))
	if err != nil {
		t.Fatalf("open cache db: %v", err)
	}
	defer db.Close()
	result, err := db.Exec("UPDATE session_summary_cache SET summary = replace(summary, ?1, ?2)", from, to)
	if err != nil {
		t.Fatalf("tamper: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	if affected == 0 {
		t.Fatalf("no cache row matched %q", from)
	}
}

func codexTitles(t *testing.T) []string {
	t.Helper()
	r := readerMust(t, SourceCodex)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	return summaryTitles(t, entries)
}

// reader 级 env-free 版：engine 直构实例 + 共享 store 目录。断言各构造新实
// 例（内存缓存为空），模拟进程重启后的冷启动。
func TestCodexColdStartReadsTitleFromPersistentCache(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, ".reins")
	sessionID := "55555555-5555-4555-8555-555555555555"
	writeCodexTranscriptAt(t, root, sessionID, "First question")

	titles := func() []string {
		t.Helper()
		engine := NewReaderEngine[CodexSessionRow](CodexSpec{}, root, store)
		entries, err := engine.ListEntries()
		if err != nil {
			t.Fatalf("list entries: %v", err)
		}
		return summaryTitles(t, entries)
	}

	// 首次列表：全量解析并把摘要写入持久缓存。
	if got := titles(); len(got) != 1 || got[0] != "First question" {
		t.Fatalf("titles = %v", got)
	}
	if count := cacheRowCount(t, root); count != 1 {
		t.Fatalf("cache rows = %d, want 1", count)
	}

	tamperCachedTitle(t, root, "First question", "TAMPERED CACHE TITLE")

	// 冷启动：文件未变更，摘要必须命中持久缓存而不是重新解析。
	if got := titles(); len(got) != 1 || got[0] != "TAMPERED CACHE TITLE" {
		t.Fatalf("titles after tamper = %v", got)
	}
}

func TestCodexChangedFileReparsesAndRefreshesCache(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, ".reins")
	sessionID := "66666666-6666-4666-8666-666666666666"
	transcriptPath := writeCodexTranscriptAt(t, root, sessionID, "First question")
	originalMtime, err := support.FileModifiedTimestampMillis(transcriptPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	titles := func() []string {
		t.Helper()
		engine := NewReaderEngine[CodexSessionRow](CodexSpec{}, root, store)
		entries, err := engine.ListEntries()
		if err != nil {
			t.Fatalf("list entries: %v", err)
		}
		return summaryTitles(t, entries)
	}

	if got := titles(); len(got) != 1 || got[0] != "First question" {
		t.Fatalf("titles = %v", got)
	}

	// 追加内容会推进文件 mtime（水位线），缓存行随之失效。
	time.Sleep(25 * time.Millisecond)
	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		`{"timestamp": "2026-09-04T09:00:00.000Z", "type": "session_meta", "payload": {"id": "66666666-6666-4666-8666-666666666666", "cwd": "/tmp/reins/workspace"}}`,
		`{"timestamp": "2026-09-04T09:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "Second question"}]}}`,
	))
	changedMtime, err := support.FileModifiedTimestampMillis(transcriptPath)
	if err != nil {
		t.Fatalf("stat after change: %v", err)
	}
	if changedMtime == originalMtime {
		t.Fatal("mtime must advance after rewrite")
	}

	if got := titles(); len(got) != 1 || got[0] != "Second question" {
		t.Fatalf("titles after change = %v", got)
	}
}

func TestClaudeColdStartReadsTitleFromPersistentCache(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, ".reins")
	sessionFile := filepath.Join(root, "projects", "demo-project", "session-123.jsonl")
	writeJSONLFile(t, sessionFile, jsonlLines(t,
		`{"type": "user", "message": {"content": "First question"}}`,
	))

	titles := func() []string {
		t.Helper()
		engine := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, store)
		entries, err := engine.ListEntries()
		if err != nil {
			t.Fatalf("list entries: %v", err)
		}
		return summaryTitles(t, entries)
	}

	if got := titles(); len(got) != 1 || got[0] != "First question" {
		t.Fatalf("titles = %v", got)
	}
	tamperCachedTitle(t, root, "First question", "TAMPERED CACHE TITLE")

	if got := titles(); len(got) != 1 || got[0] != "TAMPERED CACHE TITLE" {
		t.Fatalf("titles after tamper = %v", got)
	}
}

func TestPiSummaryCacheSurvivesMemoryClearAndInvalidatesOnMtimeChange(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := filepath.Join(tempHome, ".pi", "agent", "sessions", "--tmp-pi-project--", "2026-09-04T10-00-00-000Z_pi-cache.jsonl")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-cache", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "m1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "First Pi question"}`),
	))

	r := readerMust(t, SourcePi)
	summary, err := r.ParseSummary("pi-cache")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "First Pi question" {
		t.Fatalf("title = %q", summary.Title)
	}
	if count := cacheRowCount(t, tempHome); count != 1 {
		t.Fatalf("cache rows = %d, want 1", count)
	}
	tamperCachedTitle(t, tempHome, "First Pi question", "Cached Pi title")
	if err := r.ClearCache(); err != nil {
		t.Fatalf("clear cache: %v", err)
	}
	summary, err = r.ParseSummary("pi-cache")
	if err != nil {
		t.Fatalf("parse summary after clear: %v", err)
	}
	if summary.Title != "Cached Pi title" {
		t.Fatalf("title after clear = %q, want cached value", summary.Title)
	}

	oldMtime, err := support.FileModifiedTimestampMillis(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-cache", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "m1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "Second Pi question"}`),
	))
	newMtime, err := support.FileModifiedTimestampMillis(path)
	if err != nil {
		t.Fatalf("stat after change: %v", err)
	}
	if newMtime == oldMtime {
		t.Fatal("mtime must advance after rewrite")
	}
	if err := r.ClearCache(); err != nil {
		t.Fatalf("clear cache again: %v", err)
	}
	summary, err = r.ParseSummary("pi-cache")
	if err != nil {
		t.Fatalf("parse summary after change: %v", err)
	}
	if summary.Title != "Second Pi question" {
		t.Fatalf("title after change = %q, want Second Pi question", summary.Title)
	}
}

func TestPiTimelineCacheUsesFileMtimeAsItsFreshnessBoundary(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := filepath.Join(tempHome, ".pi", "agent", "sessions", "--tmp-pi-project--", "2026-09-04T10-00-00-000Z_pi-timeline-cache.jsonl")
	header := piHeaderLine("pi-timeline-cache", "/tmp/pi-project", "2026-09-04T10:00:00.000Z")
	writeSession := func(text string) {
		t.Helper()
		writeJSONLFile(t, path, jsonlLines(t,
			header,
			piEntryLine("message", "message-1", nil, "2026-09-04T10:00:01.000Z",
				fmt.Sprintf(`"message": {"role": "user", "content": %q}`, text)),
		))
	}

	writeSession("first timeline")
	// 固定 mtime，让后续重写落在同一新鲜度上。
	const originalMtime = int64(1_800_000_000_000)
	touchMtime(t, path, originalMtime)

	first := readDetailOf(t, readerMust(t, SourcePi), "pi-timeline-cache")
	assertAnyBlockTextContains(t, first.messages, "first timeline")

	writeSession("second timeline")
	touchMtime(t, path, originalMtime)

	cached := readDetailOf(t, readerMust(t, SourcePi), "pi-timeline-cache")
	assertAnyBlockTextContains(t, cached.messages, "first timeline")
	for _, message := range cached.messages {
		for _, block := range message.Blocks {
			if block.Text != nil && *block.Text == "second timeline" {
				t.Error("second timeline leaked despite unchanged mtime")
			}
		}
	}

	touchMtime(t, path, originalMtime+1)

	refreshed := readDetailOf(t, readerMust(t, SourcePi), "pi-timeline-cache")
	assertAnyBlockTextContains(t, refreshed.messages, "second timeline")
}

func TestClearSessionCachesEmptiesPersistentCache(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	writeCodexTranscriptAt(t, filepath.Join(tempHome, ".codex"), "77777777-7777-4777-8777-777777777777", "First question")
	if got := codexTitles(t); len(got) != 1 || got[0] != "First question" {
		t.Fatalf("titles = %v", got)
	}
	if count := cacheRowCount(t, tempHome); count != 1 {
		t.Fatalf("cache rows = %d, want 1", count)
	}

	// 显式清缓存是全量重建的逃生通道：持久缓存必须一并清空。
	if err := ClearSessionCachesInner(NewSessionIndexState()); err != nil {
		t.Fatalf("clear session caches: %v", err)
	}
	if count := cacheRowCount(t, tempHome); count != 0 {
		t.Fatalf("cache rows after clear = %d, want 0", count)
	}
}

// 刷新要重新扫盘（新文件必须出现），但不该顺手丢掉仍然有效的持久摘要——
// 那份摘要按 (路径, mtime) 自失效，清一次等于把全部转录重解一遍。
func TestRefreshRescansDiskAndKeepsPersistentRows(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	writeCodexTranscriptAt(t, filepath.Join(tempHome, ".codex"), "88888888-8888-4888-8888-888888888888", "Cached question")
	if got := codexTitles(t); len(got) != 1 || got[0] != "Cached question" {
		t.Fatalf("titles = %v", got)
	}
	// 篡改缓存行：刷新后仍读到篡改值 == 走的是原持久行，没有被清掉重解。
	tamperCachedTitle(t, tempHome, "Cached question", "Tampered title")

	// 刷新期间出现的新会话必须进列表。
	writeCodexTranscriptAt(t, filepath.Join(tempHome, ".codex"), "99999999-9999-4999-8999-999999999999", "Fresh question")

	result, err := RefreshSessionsInner(NewSessionIndexState(), SourceSelection{App: SourceCodex}, 0, 10, "", false)
	if err != nil {
		t.Fatalf("refresh sessions: %v", err)
	}
	var titles []string
	for _, session := range result.Page.Sessions {
		titles = append(titles, session.Title)
	}
	if len(titles) != 2 || !stringsContain(titles, "Fresh question") || !stringsContain(titles, "Tampered title") {
		t.Fatalf("titles = %v, want fresh + tampered", titles)
	}
}

func TestClearSessionCachesEmptiesPiPersistentCache(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := filepath.Join(tempHome, ".pi", "agent", "sessions", "--tmp-pi-project--", "2026-09-04T10-00-00-000Z_pi-global-clear.jsonl")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-global-clear", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "m1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "clear Pi cache"}`),
	))
	if _, err := readerMust(t, SourcePi).ParseSummary("pi-global-clear"); err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if count := cacheRowCount(t, tempHome); count != 1 {
		t.Fatalf("cache rows = %d, want 1", count)
	}
	if err := ClearSessionCachesInner(NewSessionIndexState()); err != nil {
		t.Fatalf("clear session caches: %v", err)
	}
	if count := cacheRowCount(t, tempHome); count != 0 {
		t.Fatalf("cache rows after clear = %d, want 0", count)
	}
}
