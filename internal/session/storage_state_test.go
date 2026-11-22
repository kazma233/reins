package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// storage_state.rs：转录存储状态——压缩形态与归档状态的读取与角标来源。
// 每个用例对应一个上游真实契约：
//   - codex 冷会话压缩（.jsonl.zst）与 archived_sessions/ 归档目录；
//   - dsh 的 v0 文件名（session.jsonl[.zstd] 不带版本段）与
//     storages/workspace.json 的归档集；
//   - zcode/opencode 的 time_archived 列（老库可能没有该列）。

const codexColdID = "11111111-1111-4111-8111-111111111111"

func TestCodexReadsCompressedRolloutAndFlagsIt(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "sessions", "2026", "03", "01",
		fmt.Sprintf("rollout-2026-03-01T10-00-00-%s.jsonl.zst", codexColdID))
	writeJSONLRaw(t, path, string(codexColdRolloutZst))

	reader := engineAt(t, CodexSpec{}, home, tempStore(t))
	summary, err := reader.ParseSummary(codexColdID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}

	if summary.Title != "cold session question" {
		t.Errorf("title = %q", summary.Title)
	}
	if !summary.Compressed {
		t.Error("压缩形态必须置位")
	}
	if summary.Archived {
		t.Error("archived must be false")
	}
	// usage 归一口径：input 扣掉缓存命中，缓存读写单独计数
	usage := summary.TokenUsage
	if usage == nil {
		t.Fatal("compressed rollout usage missing")
	}
	if usage.InputTokens != 800 || usage.OutputTokens != 50 || usage.CacheReadTokens != 200 || usage.CacheWriteTokens != 10 {
		t.Errorf("usage = %+v, want (800, 50, 200, 10)", usage)
	}

	// 时间线也要能从同一份压缩转录里读出来
	detail := readDetailOf(t, reader, codexColdID)
	assertAnyBlockTextContains(t, detail.messages, "answer from compressed rollout")
}

func TestCodexListsArchivedRolloutsInTheSameList(t *testing.T) {
	home := t.TempDir()
	archivedID := "22222222-2222-4222-8222-222222222222"
	writeJSONLFile(t, filepath.Join(home, "archived_sessions",
		fmt.Sprintf("rollout-2026-03-01T11-00-00-%s.jsonl", archivedID)), coldRolloutLines(archivedID))

	reader := engineAt(t, CodexSpec{}, home, tempStore(t))
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1（归档会话仍属于用户的会话历史）", len(entries))
	}

	summary, err := reader.ParseSummary(archivedID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if !summary.Archived {
		t.Error("archived_sessions 下的会话必须置归档位")
	}
	if summary.Compressed {
		t.Error("compressed must be false")
	}
}

func TestCodexKeepsOneRowWhenPlainAndCompressedSiblingsCoexist(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "03", "01")
	plain := filepath.Join(dir, fmt.Sprintf("rollout-2026-03-01T12-00-00-%s.jsonl", codexColdID))
	writeJSONLFile(t, plain, coldRolloutLines(codexColdID))
	writeJSONLRaw(t, plain+".zst", string(codexColdRolloutZst))

	reader := engineAt(t, CodexSpec{}, home, tempStore(t))
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	// 上游读取侧明文优先（should_skip_compressed_sibling），不能出两条行。
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	summary, err := reader.ParseSummary(codexColdID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Compressed {
		t.Error("两份共存时读的是明文")
	}
}

func TestCodexKeepsReadingARolloutCompressedAfterTheCatalogWasBuilt(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "03", "01")
	plain := filepath.Join(dir, fmt.Sprintf("rollout-2026-03-01T13-00-00-%s.jsonl", codexColdID))
	writeJSONLFile(t, plain, coldRolloutLines(codexColdID))

	reader := engineAt(t, CodexSpec{}, home, tempStore(t))
	before, err := reader.ParseSummary(codexColdID)
	if err != nil {
		t.Fatalf("parse summary before: %v", err)
	}
	if before.Compressed {
		t.Error("before must be plain")
	}

	// 模拟 codex 后台 worker：原地压成 .jsonl.zst 并删除明文；索引必须重建，
	// 旧明文路径经 Resolve 归一后仍可读。
	if err := removeFile(plain); err != nil {
		t.Fatalf("remove plain: %v", err)
	}
	compressed := plain + ".zst"
	writeJSONLRaw(t, compressed, string(codexColdRolloutZst))
	pushMtimeForward(t, compressed)

	after, err := reader.ParseSummary(codexColdID)
	if err != nil {
		t.Fatalf("parse summary after: %v", err)
	}
	if !after.Compressed {
		t.Error("压缩后索引与角标都要跟上")
	}
	if after.Title != "cold session question" {
		t.Errorf("title = %q", after.Title)
	}
}

func TestDshReadsV0TranscriptsWithoutVersionSegment(t *testing.T) {
	sessions := t.TempDir()
	project := filepath.Join(sessions, "--D-projects-demo--")

	// v0 的规范文件名是 session.jsonl[.zstd]：既没有 .v0，也不能因为缺版本段
	// 被当成非转录文件。
	compressed := filepath.Join(project, "session-demo-v0", "session.jsonl.zstd")
	writeJSONLRaw(t, compressed, string(dshV0Zst))
	plain := filepath.Join(project, "session-demo-v0-plain", "session.jsonl")
	writeJSONLFile(t, plain, jsonlLines(t,
		`{"type": "session", "version": 0, "id": "session-demo-v0-plain", "createdAt": 1791355600000, "cwd": "/tmp/dsh/legacy"}`,
		`{"type": "user/message", "seq": 1, "time": 1791355600001, "surfaceOp": "append", "data": {"content": [{"type": "text", "text": "plain v0 question"}], "source": {"kind": "user"}, "role": "user", "id": "user-1"}}`,
	))

	reader := engineAt(t, DshSpec{}, sessions, tempStore(t))
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2（v0 的两个形态都要出现）", len(entries))
	}

	compressedSummary, err := reader.ParseSummary("session-demo-v0")
	if err != nil {
		t.Fatalf("parse compressed: %v", err)
	}
	if compressedSummary.Title != "legacy v0 question" {
		t.Errorf("title = %q", compressedSummary.Title)
	}
	usage := compressedSummary.TokenUsage
	if usage == nil {
		t.Fatal("v0 usage missing")
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 || usage.CacheReadTokens != 3 || usage.CacheWriteTokens != 5 {
		t.Errorf("usage = %+v, want (11, 7, 3, 5)", usage)
	}
	plainSummary, err := reader.ParseSummary("session-demo-v0-plain")
	if err != nil {
		t.Fatalf("parse plain: %v", err)
	}
	if plainSummary.Title != "plain v0 question" {
		t.Errorf("plain title = %q", plainSummary.Title)
	}
}

func TestDshFlagsArchivedSessionsFromWorkspaceRegistry(t *testing.T) {
	root := t.TempDir()
	sessions := filepath.Join(root, "sessions")
	project := filepath.Join(sessions, "--D-projects-demo--")

	archivedID := "session-archived-one"
	activeID := "session-active-one"
	for _, id := range []string{archivedID, activeID} {
		writeJSONLFile(t, filepath.Join(project, id, "session.v4.jsonl"), jsonlLines(t,
			fmt.Sprintf(`{"type": "session", "version": 4, "id": %q, "createdAt": 1791355600000, "cwd": "/tmp/dsh/workspace"}`, id),
			`{"type": "user/message", "seq": 1, "time": 1791355600001, "surfaceOp": "append", "data": {"content": [{"type": "text", "text": "task"}], "role": "user", "id": "user-1"}}`,
		))
	}

	registry := filepath.Join(root, "storages", "workspace.json")
	writeJSONLFile(t, registry, jsonlLines(t,
		fmt.Sprintf(`{"global": {"archivedSessionIds": [%q]}}`, archivedID),
	))

	reader := engineAt(t, DshSpec{}, sessions, tempStore(t))
	archivedSummary, err := reader.ParseSummary(archivedID)
	if err != nil {
		t.Fatalf("parse archived: %v", err)
	}
	if !archivedSummary.Archived {
		t.Error("archived session must be flagged")
	}
	activeSummary, err := reader.ParseSummary(activeID)
	if err != nil {
		t.Fatalf("parse active: %v", err)
	}
	if activeSummary.Archived {
		t.Error("active session must not be flagged")
	}

	// 归档只改账本不改转录，索引指纹必须带上账本时刻，否则角标不会更新。
	writeJSONLFile(t, registry, jsonlLines(t,
		fmt.Sprintf(`{"global": {"archivedSessionIds": [%q, %q]}}`, archivedID, activeID),
	))
	pushMtimeForward(t, registry)
	reread, err := reader.ParseSummary(activeID)
	if err != nil {
		t.Fatalf("parse active after archive: %v", err)
	}
	if !reread.Archived {
		t.Error("archive registry change must refresh the flag")
	}
}

func TestZcodeFlagsArchivedSessionsFromTimeArchived(t *testing.T) {
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "db.sqlite")

	seedZcodeFamilyAt(t, dbPath, "root-archived", "child-active")
	db := openSQLite(t, dbPath)
	mustExec(t, db, "ALTER TABLE session ADD COLUMN time_archived INTEGER")
	mustExec(t, db, "UPDATE session SET time_archived = 5000 WHERE id = 'root-archived'")
	// 对照组必须是独立会话（非归档会话的子会话会被并进同一 family，角标取自
	// family root，断言不出未归档态）。
	mustExec(t, db, "INSERT INTO session (id, parent_id, directory, title, time_created, time_updated, time_archived) VALUES ('standalone', NULL, '/tmp/root', 'Standalone task', 1000, 2000, NULL)")
	db.Close()

	reader := engineAt(t, ZcodeSpec{}, dbPath, tempStore(t))
	archived, err := reader.ParseSummary("root-archived")
	if err != nil {
		t.Fatalf("parse archived: %v", err)
	}
	if !archived.Archived {
		t.Error("time_archived session must be flagged")
	}
	standalone, err := reader.ParseSummary("standalone")
	if err != nil {
		t.Fatalf("parse standalone: %v", err)
	}
	if standalone.Archived {
		t.Error("standalone session must not be flagged")
	}
}

func TestOpenCodeFlagsArchivedSessionsFromTimeArchived(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeFamilyAt(t, root, "ses_archived_root", "ses_active_child")

	dbPath := filepath.Join(root, "opencode.db")
	db := openSQLite(t, dbPath)
	mustExec(t, db, "ALTER TABLE session_v2 ADD COLUMN time_archived INTEGER")
	mustExec(t, db, "UPDATE session_v2 SET time_archived = 5000 WHERE id = 'ses_archived_root'")
	mustExec(t, db, "INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated, time_archived) VALUES ('ses_standalone', 'project-1', NULL, 'ses_standalone', '/tmp/root', 'Standalone task', '1', 1744366400000, 1744366500000, NULL)")
	db.Close()

	reader := engineAt(t, OpenCodeSpec{}, root, tempStore(t))
	archived, err := reader.ParseSummary("ses_archived_root")
	if err != nil {
		t.Fatalf("parse archived: %v", err)
	}
	if !archived.Archived {
		t.Error("time_archived session must be flagged")
	}
	standalone, err := reader.ParseSummary("ses_standalone")
	if err != nil {
		t.Fatalf("parse standalone: %v", err)
	}
	if standalone.Archived {
		t.Error("standalone session must not be flagged")
	}
}

// removeFile：测试内删除。
func removeFile(path string) error {
	return os.Remove(path)
}
