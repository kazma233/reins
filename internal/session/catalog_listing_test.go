package session

import (
	"fmt"
	"path/filepath"
	"testing"

	"reins/internal/support"
)

// catalog_listing.rs：列表页过滤、排序、跨来源合并分页与刷新。

func TestListSessionsFiltersBySessionIDOnReader(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	targetID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	seedCodexSession(t, tempHome, targetID)
	seedCodexSession(t, tempHome, otherID)

	page, err := ListSessionsInner(NewSessionIndexState(), SourceSelection{App: SourceCodex}, 0, 20, "aaaa-4aaa", false, true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if page.TotalCount != 1 || len(page.Sessions) != 1 {
		t.Fatalf("page = total %d sessions %d, want 1/1", page.TotalCount, len(page.Sessions))
	}
	if page.Sessions[0].SourceSessionID != targetID {
		t.Errorf("session id = %s, want %s", page.Sessions[0].SourceSessionID, targetID)
	}
}

func TestListSessionsReverseOrderAppliesBeforePaging(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	oldestID := "11111111-1111-4111-8111-111111111111"
	middleID := "22222222-2222-4222-8222-222222222222"
	newestID := "33333333-3333-4333-8333-333333333333"

	seedCodexSession(t, tempHome, oldestID)
	seedCodexSession(t, tempHome, middleID)
	seedCodexSession(t, tempHome, newestID)

	defaultPage, err := ListSessionsInner(NewSessionIndexState(), SourceSelection{App: SourceCodex}, 0, 20, "", false, true)
	if err != nil {
		t.Fatalf("default page: %v", err)
	}
	reversedPage, err := ListSessionsInner(NewSessionIndexState(), SourceSelection{App: SourceCodex}, 0, 2, "", true, true)
	if err != nil {
		t.Fatalf("reversed page: %v", err)
	}

	// 期望 = 默认序的末两条（reverse 先于 paging 应用）。
	expected := make([]string, 0, 2)
	sessions := defaultPage.Sessions
	for i := len(sessions) - 1; i >= 0 && len(expected) < 2; i-- {
		expected = append(expected, sessions[i].SourceSessionID)
	}

	got := make([]string, 0, len(reversedPage.Sessions))
	for _, item := range reversedPage.Sessions {
		got = append(got, item.SourceSessionID)
	}
	if len(got) != len(expected) {
		t.Fatalf("reversed page = %v, want %v", got, expected)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Fatalf("reversed page = %v, want %v", got, expected)
		}
	}
}

func TestClaudeSessionListingUsesTranscriptTimestamps(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	olderID := "11111111-1111-4111-8111-111111111111"
	newerID := "22222222-2222-4222-8222-222222222222"
	projectDir := filepath.Join(tempHome, ".claude", "projects", "demo-project")
	olderPath := filepath.Join(projectDir, olderID+".jsonl")
	newerPath := filepath.Join(projectDir, newerID+".jsonl")
	olderUpdatedAt, _ := support.ParseTimestamp("2026-04-21T12:30:00.000Z")
	newerUpdatedAt, _ := support.ParseTimestamp("2026-04-22T09:45:00.000Z")

	writeJSONLFile(t, newerPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-22T09:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Newer task"}}`, newerID),
		fmt.Sprintf(`{"timestamp": "2026-04-22T09:45:00.000Z", "sessionId": %q, "type": "assistant", "message": {"content": "Newer answer"}}`, newerID),
	))
	writeJSONLFile(t, olderPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Older task"}}`, olderID),
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:30:00.000Z", "sessionId": %q, "type": "assistant", "message": {"content": "Older answer"}}`, olderID),
	))

	page, err := ListSessionsInner(NewSessionIndexState(), SourceSelection{App: SourceClaudeCode}, 0, 20, "", false, true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if page.TotalCount != 2 {
		t.Fatalf("total = %d, want 2", page.TotalCount)
	}
	if page.Sessions[0].SourceSessionID != newerID || *page.Sessions[0].UpdatedAt != newerUpdatedAt {
		t.Errorf("first = %s %v, want %s %v", page.Sessions[0].SourceSessionID, page.Sessions[0].UpdatedAt, newerID, newerUpdatedAt)
	}
	if page.Sessions[1].SourceSessionID != olderID || *page.Sessions[1].UpdatedAt != olderUpdatedAt {
		t.Errorf("second = %s %v, want %s %v", page.Sessions[1].SourceSessionID, page.Sessions[1].UpdatedAt, olderID, olderUpdatedAt)
	}
}

// 合并视图：codex 与 claude 两个来源按 updated_at 全局排序、跨来源分页。
func TestMergedListingInterleavesSourcesAndPaginatesAcrossSources(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	codexID := "11111111-1111-4111-8111-111111111111"
	claudeNewestID := "22222222-2222-4222-8222-222222222222"
	claudeOldestID := "33333333-3333-4333-8333-333333333333"

	// codex 会话夹在两个 claude 会话之间，验证合并排序真正跨来源交错。
	// codex 的 updated_at 语义是文件 mtime（产品既有行为），所以显式设置
	// mtime 到 claude 两个会话之间，而不是依赖写入时刻。
	codexTranscript := filepath.Join(tempHome, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T10-00-00-%s.jsonl", codexID))
	writeJSONLFile(t, codexTranscript, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T10:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, codexID),
		fmt.Sprintf(`{"timestamp": "2026-04-21T10:30:00.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "Middle codex task"}]}}`),
	))
	// 2026-04-21T17:10Z，落在 claude 两个会话（04-20、04-22）之间。
	touchMtime(t, codexTranscript, 1_776_767_400_000)
	projectDir := filepath.Join(tempHome, ".claude", "projects", "demo-project")
	writeJSONLFile(t, filepath.Join(projectDir, claudeNewestID+".jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-22T09:45:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Newest claude task"}}`, claudeNewestID),
	))
	writeJSONLFile(t, filepath.Join(projectDir, claudeOldestID+".jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-20T09:45:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Oldest claude task"}}`, claudeOldestID),
	))

	state := NewSessionIndexState()

	firstPage, err := ListSessionsInner(state, SourceSelection{All: true}, 0, 2, "", false, true)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if firstPage.SourceApp != nil {
		t.Errorf("sourceApp = %v, want nil (merged)", *firstPage.SourceApp)
	}
	if firstPage.TotalCount != 3 {
		t.Fatalf("total = %d, want 3", firstPage.TotalCount)
	}
	gotFirst := []string{firstPage.Sessions[0].SourceSessionID, firstPage.Sessions[1].SourceSessionID}
	if gotFirst[0] != claudeNewestID || gotFirst[1] != codexID {
		t.Fatalf("first page = %v, want [%s %s]", gotFirst, claudeNewestID, codexID)
	}
	if !firstPage.HasMore || firstPage.NextOffset == nil || *firstPage.NextOffset != 2 {
		t.Fatalf("hasMore/nextOffset = %v %v, want true/2", firstPage.HasMore, firstPage.NextOffset)
	}

	secondPage, err := ListSessionsInner(state, SourceSelection{All: true}, 2, 2, "", false, true)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(secondPage.Sessions) != 1 || secondPage.Sessions[0].SourceSessionID != claudeOldestID {
		t.Fatalf("second page = %+v, want [%s]", secondPage.Sessions, claudeOldestID)
	}
	if secondPage.HasMore {
		t.Errorf("second page hasMore = true, want false")
	}
}

// 合并视图只包含根目录存在的来源；搜索在合并结果上过滤。
func TestMergedListingSkipsUnavailableSourcesAndFiltersByQuery(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	// 只准备 claude，不创建 .codex 目录。
	claudeID := "44444444-4444-4444-8444-444444444444"
	projectDir := filepath.Join(tempHome, ".claude", "projects", "demo-project")
	writeJSONLFile(t, filepath.Join(projectDir, claudeID+".jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-22T09:45:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Claude only task"}}`, claudeID),
	))

	state := NewSessionIndexState()

	page, err := ListSessionsInner(state, SourceSelection{All: true}, 0, 20, "", false, true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if page.TotalCount != 1 {
		t.Fatalf("total = %d, want 1", page.TotalCount)
	}
	if page.Sessions[0].SourceApp != SourceClaudeCode {
		t.Errorf("sourceApp = %s, want claude_code", page.Sessions[0].SourceApp)
	}

	filtered, err := ListSessionsInner(state, SourceSelection{All: true}, 0, 20, "claude only", false, true)
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if filtered.TotalCount != 1 || filtered.Sessions[0].SourceSessionID != claudeID {
		t.Fatalf("filtered = %d %+v, want 1 %s", filtered.TotalCount, filtered.Sessions, claudeID)
	}
}

// 刷新合并视图时 selected_source 为 None（前端映射回"全部"）。
func TestRefreshWithAllSelectionReturnsMergedPage(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	codexID := "55555555-5555-4555-8555-555555555555"
	seedCodexSession(t, tempHome, codexID)

	result, err := RefreshSessionsInner(NewSessionIndexState(), SourceSelection{All: true}, 0, 20, "", false)
	if err != nil {
		t.Fatalf("refresh sessions: %v", err)
	}
	if result.SelectedSource != nil {
		t.Errorf("selectedSource = %v, want nil", *result.SelectedSource)
	}
	if result.Page.SourceApp != nil {
		t.Errorf("page.sourceApp = %v, want nil", *result.Page.SourceApp)
	}
	if result.Page.TotalCount != 1 {
		t.Errorf("total = %d, want 1", result.Page.TotalCount)
	}
}
