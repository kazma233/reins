package session

import (
	"fmt"
	"path/filepath"
	"testing"
)

// session_token_usage.rs：列表页 token 用量的归一口径——各来源读取器先把
// 自家 usage 字段归一，family 聚合再相加。

func listFirstSession(t *testing.T, source SourceApp) SessionSummary {
	t.Helper()
	page, err := ListSessionsInner(NewSessionIndexState(), SourceSelection{App: source}, 0, 20, "", false, true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if page.TotalCount != 1 {
		t.Fatalf("夹具应恰好产生一个会话条目: total = %d", page.TotalCount)
	}
	return page.Sessions[0]
}

func TestClaudeListingUsageSumsAssistantLinesAcrossFamilyMembers(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	sessionID := "11111111-1111-4111-8111-111111111111"
	projectDir := filepath.Join(tempHome, ".claude", "projects", "demo-project")

	writeJSONLFile(t, filepath.Join(projectDir, sessionID+".jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Root task"}}`, sessionID),
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:01:00.000Z", "sessionId": %q, "type": "assistant", "message": {"content": "answer", "usage": {"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 1000, "cache_creation_input_tokens": 50}}}`, sessionID),
		// 同文件内嵌 sidechain 回复也按 assistant 行计入。
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:02:00.000Z", "sessionId": %q, "type": "assistant", "isSidechain": true, "message": {"content": "sidechain answer", "usage": {"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 200}}}`, sessionID),
	))
	writeJSONLFile(t, filepath.Join(projectDir, "subagents", "agent-sub-1.jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:03:00.000Z", "sessionId": %q, "type": "assistant", "message": {"content": "subagent answer", "usage": {"input_tokens": 500, "output_tokens": 50, "cache_read_input_tokens": 5000}}}`, sessionID),
	))

	summary := listFirstSession(t, SourceClaudeCode)
	want := SessionTokenUsage{InputTokens: 610, OutputTokens: 75, CacheReadTokens: 6200, CacheWriteTokens: 50}
	if summary.TokenUsage == nil || *summary.TokenUsage != want {
		t.Fatalf("token usage = %+v, want %+v", summary.TokenUsage, want)
	}
}

func TestCodexListingUsageTakesLastCumulativeAndSumsFamilyMembers(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	rootID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	subagentID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	tokenCountEvent := func(input uint64, cached uint64, cacheWrite uint64, output uint64) string {
		return fmt.Sprintf(`{"timestamp": "2026-04-21T12:05:00.000Z", "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {"input_tokens": %d, "cached_input_tokens": %d, "cache_write_input_tokens": %d, "output_tokens": %d, "reasoning_output_tokens": %d, "total_tokens": %d}, "last_token_usage": {"input_tokens": 1, "output_tokens": 1}, "model_context_window": 200000}}}`,
			input, cached, cacheWrite, output, output/2, input+output)
	}

	writeJSONLFile(t, filepath.Join(tempHome, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", rootID)), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/codex", "timestamp": "2026-04-21T12:00:00.000Z"}}`, rootID),
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:01:00.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Root task"}]}}`),
		// 两条累计值事件：第二条覆盖第一条，不能按增量求和。
		tokenCountEvent(1000, 400, 100, 30),
		tokenCountEvent(2000, 900, 150, 60),
	))
	writeJSONLFile(t, filepath.Join(tempHome, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-06-00-%s.jsonl", subagentID)), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:06:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/codex", "timestamp": "2026-04-21T12:06:00.000Z", "source": {"subagent": {"thread_spawn": {"parent_thread_id": %q}}}}}`, subagentID, rootID),
		tokenCountEvent(300, 100, 0, 10),
	))

	summary := listFirstSession(t, SourceCodex)
	// root 取最后一条累计值并扣除缓存命中（input 2000-900），再与子代理线程相加。
	want := SessionTokenUsage{InputTokens: 1100 + 200, OutputTokens: 60 + 10, CacheReadTokens: 900 + 100, CacheWriteTokens: 150}
	if summary.TokenUsage == nil || *summary.TokenUsage != want {
		t.Fatalf("token usage = %+v, want %+v", summary.TokenUsage, want)
	}
}

func TestPiListingUsageSumsAssistantAndSubagentRunUsage(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	sessionPath := filepath.Join(tempHome, ".pi", "agent", "sessions", "--tmp-pi-usage--", "2026-09-04T09-00-00-000Z_pi-usage.jsonl")

	writeJSONLFile(t, sessionPath, jsonlLines(t,
		`{"type": "session", "version": 3, "id": "pi-usage", "timestamp": "2026-09-04T09:00:00.000Z", "cwd": "/tmp/pi-usage"}`,
		piEntryLine("message", "m1", nil, "2026-09-04T09:00:01.000Z", `"message": {"role": "user", "content": "pi task"}`),
		piEntryLine("message", "m2", str("m1"), "2026-09-04T09:00:02.000Z",
			`"message": {"role": "assistant", "content": "answer", "usage": {"input": 197, "output": 180, "cacheRead": 6656, "cacheWrite": 0, "reasoning": 37, "totalTokens": 7033}}`),
		piEntryLine("message", "m3", str("m2"), "2026-09-04T09:00:03.000Z",
			`"message": {"role": "toolResult", "toolName": "subagent", "toolCallId": "call-1", "content": "subagent report", "details": {"results": [{"title": "run", "usage": {"input": 636783, "output": 31284, "cacheRead": 2916352, "cacheWrite": 0}}]}}`),
	))

	summary := listFirstSession(t, SourcePi)
	want := SessionTokenUsage{InputTokens: 197 + 636783, OutputTokens: 180 + 31284, CacheReadTokens: 6656 + 2916352, CacheWriteTokens: 0}
	if summary.TokenUsage == nil || *summary.TokenUsage != want {
		t.Fatalf("token usage = %+v, want %+v", summary.TokenUsage, want)
	}
}

func TestOpenCodeListingUsageSumsAssistantTokensWithReasoning(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	// seed 在 env 根下（seedOpenCodeFamily 走生产 root）。
	root, err := OpenCodeRoot()
	if err != nil {
		t.Fatalf("opencode root: %v", err)
	}
	seedOpenCodeFamilyAt(t, root, "ses_usage_root", "ses_usage_child")

	db := openSQLite(t, filepath.Join(root, "opencode.db"))
	defer db.Close()
	seedOpenCodeMessageRow(t, db, "ses_usage_root", "root-assistant", 1_744_366_402_000, 2, "assistant",
		`{"time": {"created": 1744366402000}, "agent": "build", "content": [{"type": "text", "text": "Root answer"}], "tokens": {"input": 1000, "output": 100, "reasoning": 50, "cache": {"read": 500, "write": 20}}}`)
	seedOpenCodeMessageRow(t, db, "ses_usage_child", "child-assistant", 1_744_366_403_000, 2, "assistant",
		`{"time": {"created": 1744366403000}, "agent": "build", "content": [{"type": "text", "text": "Child answer"}], "tokens": {"input": 200, "output": 20, "cache": {"read": 0, "write": 0}}}`)

	summary := listFirstSession(t, SourceOpenCode)
	// reasoning 并入输出；child-msg-1 没有 tokens 字段，SUM 视为 0。
	want := SessionTokenUsage{InputTokens: 1200, OutputTokens: (100 + 50) + 20, CacheReadTokens: 500, CacheWriteTokens: 20}
	if summary.TokenUsage == nil || *summary.TokenUsage != want {
		t.Fatalf("token usage = %+v, want %+v", summary.TokenUsage, want)
	}
}

func grokbuildUsageSessionFixture(t *testing.T, home string, sessionID string, attempt *string, kind *string, usage *string) {
	t.Helper()
	dir := filepath.Join(home, ".grok", "sessions", "not-a-cwd", sessionID)
	attemptJSON := "null"
	if attempt != nil {
		attemptJSON = fmt.Sprintf("%q", *attempt)
	}
	kindJSON := "null"
	if kind != nil {
		kindJSON = fmt.Sprintf("%q", *kind)
	}
	summaryJSON := fmt.Sprintf(`{"info": {"id": %q, "cwd": "/synthetic/project"}, "chat_format_version": 1, "session_summary": "Synthetic summary", "attempt_id": %s, "session_kind": %s, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:01:00Z"}`,
		sessionID, attemptJSON, kindJSON)
	writeJSONLRaw(t, filepath.Join(dir, "summary.json"), summaryJSON)
	writeJSONLFile(t, filepath.Join(dir, "chat_history.jsonl"), jsonlLines(t,
		`{"type":"user","content":[{"type":"text","text":"Grok task"}]}`,
		`{"type":"assistant","content":"answer"}`,
	))
	if usage != nil {
		writeJSONLRaw(t, filepath.Join(dir, "usage.json"), *usage)
	}
}

func TestGrokbuildListingUsageReadsSessionTotalsAndSumsSubagents(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	rootID := "grok-usage-root"
	childID := "grok-usage-child"
	attempt := "attempt-1"
	kind := "subagent"
	rootUsage := `{"sessionId": "grok-usage-root", "session": {"inputTokens": 2000, "outputTokens": 60, "cachedReadTokens": 900, "cacheCreationTokens": 150, "reasoningTokens": 25, "totalTokens": 2060, "modelCalls": 3, "turnCount": 1}}`
	// 子会话不带 usage.json：旧版本/中断场景下保持 None 而不是报错。
	grokbuildUsageSessionFixture(t, tempHome, rootID, nil, nil, &rootUsage)
	grokbuildUsageSessionFixture(t, tempHome, childID, &attempt, &kind, nil)
	metaDir := filepath.Join(tempHome, ".grok", "sessions", "not-a-cwd", rootID, "subagents", childID)
	writeJSONLRaw(t, filepath.Join(metaDir, "meta.json"),
		`{"subagent_id": "grok-usage-child", "attempt_id": "attempt-1", "parent_session_id": "grok-usage-root", "child_session_id": "grok-usage-child", "subagent_type": "general-purpose", "description": "usage child", "status": "completed", "started_at": "2026-01-01T00:00:10Z", "completed_at": "2026-01-01T00:00:20Z"}`)

	summary := listFirstSession(t, SourceGrokBuild)
	// inputTokens 含缓存命中（input+output==totalTokens），扣除后口径对齐；
	// 子会话无 usage.json，按 0 计入。
	want := SessionTokenUsage{InputTokens: 2000 - 900, OutputTokens: 60, CacheReadTokens: 900, CacheWriteTokens: 150}
	if summary.TokenUsage == nil || *summary.TokenUsage != want {
		t.Fatalf("token usage = %+v, want %+v", summary.TokenUsage, want)
	}
}
