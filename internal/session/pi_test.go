package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pi.rs：pi 读取器的行为分区——header 校验与目录解析、活动分支链、子代理
// run 结构化块、角色与损坏链容错、v1/v2 迁移、缓存与归族。

func TestPiDetectsValidHeadersAndIgnoresInvalidFiles(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	older := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T09-00-00-000Z", "pi-old")
	newer := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-new")
	writeJSONLFile(t, older, jsonlLines(t,
		piHeaderLine("pi-old", "/tmp/pi-project", "2026-09-04T09:00:00.000Z"),
		piEntryLine("message", "old-msg", nil, "2026-09-04T09:00:01.000Z", `"message": {"role": "user", "content": "old"}`),
	))
	writeJSONLFile(t, newer, jsonlLines(t,
		piHeaderLine("pi-new", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "new-msg", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "new"}`),
	))
	// newer 的 mtime 显式设到固定时刻（排序按 family 最近活动）。
	touchMtime(t, newer, 1_800_000_000_000)
	writeJSONLFile(t, filepath.Join(tempHome, ".pi", "agent", "sessions", "broken.jsonl"), jsonlLines(t,
		`{"not": "a session"}`,
	))
	writeJSONLFile(t, filepath.Join(tempHome, ".pi", "agent", "sessions", "not-session.jsonl"), jsonlLines(t,
		`{"type": "other", "id": "x"}`,
	))
	writeJSONLFile(t, filepath.Join(tempHome, ".pi", "agent", "sessions", "missing-id.jsonl"), jsonlLines(t,
		`{"type": "session", "version": 3, "timestamp": "2026-09-04T10:00:00.000Z", "cwd": "/tmp/pi-project"}`,
	))

	r, err := reader(SourcePi)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Path != newer || entries[1].Path != older {
		t.Fatalf("entry order = [%s, %s], want newer first", entries[0].Path, entries[1].Path)
	}
	for _, entry := range entries {
		if entry.Summary != nil {
			t.Error("pi list entries carry no summary（按需懒加载）")
		}
	}
	sources, err := DetectSourcesInner(NewSessionIndexState())
	if err != nil {
		t.Fatalf("detect sources: %v", err)
	}
	found := false
	for _, source := range sources {
		if source.App == SourcePi && source.Available && source.SessionCount == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pi source status missing: %+v", sources)
	}
}

func TestPiHonorsConfiguredSessionDirectory(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	customSessions := filepath.Join(tempHome, "custom-pi-sessions")
	guard := homeGuard(t, tempHome)
	guard.SetPiSessionDir(customSessions)

	path := filepath.Join(customSessions, "2026-09-04T10-00-00-000Z_pi-custom.jsonl")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-custom", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "message-1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "custom directory"}`),
	))

	r, err := reader(SourcePi)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Path != path {
		t.Fatalf("entries = %+v, want [%s]", entries, path)
	}
	sources, err := DetectSourcesInner(NewSessionIndexState())
	if err != nil {
		t.Fatalf("detect sources: %v", err)
	}
	found := false
	for _, source := range sources {
		if source.App == SourcePi && source.Available && source.SessionCount == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("pi source status missing: %+v", sources)
	}
}

func TestPiActiveBranchAndEventsFollowTheLastEntryChain(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-tree")

	parentSession := "/tmp/parent.jsonl"
	writeJSONLFile(t, path, jsonlLines(t,
		fmt.Sprintf(`{"type": "session", "version": 3, "id": "pi-tree", "timestamp": "2026-09-04T10:00:00.000Z", "cwd": "/tmp/pi-project", "parentSession": %q}`, parentSession),
		piEntryLine("message", "user-1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "active question"}`),
		piEntryLine("message", "assistant-1", str("user-1"), "2026-09-04T10:00:02.000Z", `"message": {"role": "assistant", "content": [{"type": "text", "text": "active answer"}]}`),
		piEntryLine("message", "inactive-user", str("user-1"), "2026-09-04T10:00:03.000Z", `"message": {"role": "user", "content": "inactive branch"}`),
		piEntryLine("message", "active-user-2", str("assistant-1"), "2026-09-04T10:00:04.000Z", `"message": {"role": "user", "content": [{"type": "text", "text": "latest request"}, {"type": "image", "data": "AA==", "mimeType": "image/png"}]}`),
		piEntryLine("model_change", "model-1", str("active-user-2"), "2026-09-04T10:00:05.000Z", `"provider": "anthropic", "modelId": "sonnet"`),
		piEntryLine("thinking_level_change", "thinking-1", str("model-1"), "2026-09-04T10:00:06.000Z", `"thinkingLevel": "high"`),
		piEntryLine("compaction", "compact-1", str("thinking-1"), "2026-09-04T10:00:07.000Z", `"summary": "kept context", "retainedTail": [{"role": "user", "content": "tail"}]`),
		piEntryLine("branch_summary", "branch-1", str("compact-1"), "2026-09-04T10:00:08.000Z", `"fromId": "inactive-user", "summary": "inactive context"`),
		piEntryLine("custom", "custom-1", str("branch-1"), "2026-09-04T10:00:09.000Z", `"customType": "test", "data": {"value": 1}`),
		piEntryLine("label", "label-1", str("custom-1"), "2026-09-04T10:00:10.000Z", `"targetId": "user-1", "label": "checkpoint"`),
		piEntryLine("session_info", "info-1", str("label-1"), "2026-09-04T10:00:11.000Z", `"name": "Named Pi session"`),
	))

	r, err := reader(SourcePi)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	summary, err := r.ParseSummary("pi-tree")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "Named Pi session" {
		t.Errorf("title = %q", summary.Title)
	}
	detail := readDetailOf(t, r, "pi-tree")
	var texts []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil {
				texts = append(texts, *block.Text)
			}
		}
	}
	if !anyContains(texts, "active question") || !anyContains(texts, "latest request") {
		t.Fatalf("texts = %v", texts)
	}
	if anyContains(texts, "inactive branch") {
		t.Error("inactive branch leaked into the active chain")
	}
	for _, kind := range []string{"model_change", "thinking_level_change", "compaction", "branch_summary", "custom", "label", "session_info"} {
		if !anyEventKind(detail.events, kind) {
			t.Errorf("event kind %s missing", kind)
		}
	}
	hasRetainedTail := false
	for _, event := range detail.events {
		if event.Kind != "compaction" {
			continue
		}
		if payload, ok := event.Payload.(map[string]any); ok {
			if _, ok := getJSONObjectPath(payload, "retainedTail"); ok {
				hasRetainedTail = true
			}
		}
	}
	if !hasRetainedTail {
		t.Error("compaction retainedTail missing")
	}
	// source_paths 来自目录扫描，按路径语义比较。
	if len(detail.sourcePaths) != 1 || detail.sourcePaths[0] != path {
		t.Fatalf("source paths = %v, want [%s]", detail.sourcePaths, path)
	}
	if !anyEventKind(detail.events, "parent_session") {
		t.Error("parent_session event missing")
	}
	overview, err := r.ParseOverview("pi-tree")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if overview.MessageCount == nil || *overview.MessageCount != len(detail.messages) {
		t.Errorf("message count = %v, want %d", overview.MessageCount, len(detail.messages))
	}
	if overview.EventCount == nil || *overview.EventCount != len(detail.events) {
		t.Errorf("event count = %v, want %d", overview.EventCount, len(detail.events))
	}
	if len(overview.Agents) != 1 {
		t.Errorf("agents = %+v, want 1", overview.Agents)
	}
	// 详情正文全量契约：与 overview 计数一致，无分页元数据。
	messages, err := r.ParseMessages("pi-tree")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	if len(messages) != len(detail.messages) {
		t.Errorf("messages = %d, want %d", len(messages), len(detail.messages))
	}
	events, err := r.ParseEvents("pi-tree")
	if err != nil {
		t.Fatalf("parse events: %v", err)
	}
	if len(events) != len(detail.events) {
		t.Errorf("events = %d, want %d", len(events), len(detail.events))
	}
}

func TestPiParsesSubagentToolResultIntoStructuredRunBlock(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T12-00-00-000Z", "pi-subagent")

	subagentDetails := `{"mode": "single", "agentScope": "both", "results": [{
		"agent": "scout", "agentSource": "project", "task": "梳理代码库", "exitCode": 0, "stderr": "",
		"usage": {"input": 636783, "output": 31284, "cacheRead": 2916352, "turns": 18},
		"model": "acme/chat-b", "stopReason": "stop",
		"messages": [
			{"role": "user", "content": "Task: 梳理代码库", "timestamp": 1789091776409},
			{"role": "assistant", "content": [{"type": "thinking", "thinking": "先看目录结构"}, {"type": "text", "text": "结论：只读调查完成"}], "usage": {"input": 100, "output": 20}, "stopReason": "stop", "timestamp": 1789091776480}
		]
	}]}`
	subagentDetails = strings.ReplaceAll(subagentDetails, "\n", "")
	subagentDetails = strings.ReplaceAll(subagentDetails, "\t", "")

	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-subagent", "/tmp/pi-project", "2026-09-04T12:00:00.000Z"),
		piEntryLine("message", "sa-u1", nil, "2026-09-04T12:00:01.000Z", `"message": {"role": "user", "content": "派个 scout 调查"}`),
		piEntryLine("message", "sa-a1", str("sa-u1"), "2026-09-04T12:00:02.000Z",
			`"message": {"role": "assistant", "content": [{"type": "toolCall", "id": "call-sub-1", "name": "subagent", "arguments": {"agent": "scout", "task": "梳理代码库"}}]}`),
		piEntryLine("message", "sa-t1", str("sa-a1"), "2026-09-04T12:00:03.000Z",
			`"message": {"role": "toolResult", "toolCallId": "call-sub-1", "toolName": "subagent", "content": [{"type": "text", "text": "## 结论\n整体判断…"}], "details": `+subagentDetails+`, "isError": false}`),
	))

	r, err := reader(SourcePi)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	detail := readDetailOf(t, r, "pi-subagent")
	if len(detail.messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(detail.messages))
	}

	// subagent toolResult 解析为单个结构化块，报告文本保留。
	runMessage := &detail.messages[2]
	if len(runMessage.Blocks) != 1 {
		t.Fatalf("run blocks = %d, want 1", len(runMessage.Blocks))
	}
	block := &runMessage.Blocks[0]
	if block.Kind != "subagent_run" {
		t.Errorf("block kind = %s", block.Kind)
	}
	if derefString(block.ToolName) != "subagent" || derefString(block.ToolCallID) != "call-sub-1" {
		t.Errorf("block identity = %v %v", block.ToolName, block.ToolCallID)
	}
	if derefString(block.Text) != "## 结论\n整体判断…" {
		t.Errorf("block text = %q", derefString(block.Text))
	}

	// payload.runs 携带 run 元数据，原始 messages 数组被解析后的嵌套消息替代
	// （nestedMessages 的元素是 SessionMessage 值，先序列化成 wire 形状再看）。
	serialized, err := json.Marshal(block.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	payloadValue, err := decodeJSONBytes(serialized)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	payload, ok := payloadValue.(map[string]any)
	if !ok {
		t.Fatalf("block payload = %T", payloadValue)
	}
	runs, ok := getJSONObjectPath(payload, "runs")
	if !ok {
		t.Fatal("payload runs missing")
	}
	runsList := runs.([]any)
	first := runsList[0].(map[string]any)
	if agent, _ := jsonString(first, "agent"); agent != "scout" {
		t.Errorf("run agent = %q", agent)
	}
	if source, _ := jsonString(first, "agentSource"); source != "project" {
		t.Errorf("run agentSource = %q", source)
	}
	usage, _ := getJSONObjectPath(first, "usage")
	turns, _ := jsonU64(usage, "turns")
	if turns != 18 {
		t.Errorf("run turns = %d", turns)
	}
	if _, hasMessages := getJSONObjectPath(first, "messages"); hasMessages {
		t.Error("raw messages array must be replaced by nestedMessages")
	}
	nested, ok := getJSONObjectPath(first, "nestedMessages")
	if !ok {
		t.Fatal("nestedMessages missing")
	}
	nestedList := nested.([]any)
	if len(nestedList) != 2 {
		t.Fatalf("nested messages = %d, want 2", len(nestedList))
	}
	firstNested := nestedList[0].(map[string]any)
	secondNested := nestedList[1].(map[string]any)
	if role, _ := jsonString(firstNested, "role"); role != "user" {
		t.Errorf("nested[0] role = %q", role)
	}
	if role, _ := jsonString(secondNested, "role"); role != "assistant" {
		t.Errorf("nested[1] role = %q", role)
	}
	if ts, _ := jsonI64(secondNested, "timestamp"); ts != 1789091776480 {
		t.Errorf("nested[1] timestamp = %d", ts)
	}
	nestedBlocks := nestedList[1].(map[string]any)["blocks"].([]any)
	if kind, _ := jsonString(nestedBlocks[0], "kind"); kind != "thinking" {
		t.Errorf("nested block[0] kind = %q", kind)
	}
	if kind, _ := jsonString(nestedBlocks[1], "kind"); kind != "output_text" {
		t.Errorf("nested block[1] kind = %q", kind)
	}

	// 嵌套消息 id 全局唯一，可与主会话消息共存于同一列表。
	if id, _ := jsonString(firstNested, "id"); id != "sa-t1-r0-m0" {
		t.Errorf("nested[0] id = %q", id)
	}
	if id, _ := jsonString(secondNested, "id"); id != "sa-t1-r0-m1" {
		t.Errorf("nested[1] id = %q", id)
	}

	// 普通 toolResult 不受影响，仍是 tool_result 块。
	normalPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T12-10-00-000Z", "pi-normal-tool")
	writeJSONLFile(t, normalPath, jsonlLines(t,
		piHeaderLine("pi-normal-tool", "/tmp/pi-project", "2026-09-04T12:10:00.000Z"),
		piEntryLine("message", "nt-t1", nil, "2026-09-04T12:10:01.000Z",
			`"message": {"role": "toolResult", "toolCallId": "call-1", "toolName": "bash", "content": [{"type": "text", "text": "output"}], "isError": false}`),
	))
	normalDetail := readDetailOf(t, r, "pi-normal-tool")
	if normalDetail.messages[0].Blocks[0].Kind != "tool_result" {
		t.Errorf("normal tool result kind = %s", normalDetail.messages[0].Blocks[0].Kind)
	}

	// Pi 的 subagent 内嵌在 toolResult.details 里，没有子会话可查：必须报错
	// 而不是返回空，否则调用方无法区分「不支持」与「空子会话」。
	if _, err := r.ParseAgentMessages("pi-subagent", "any-agent-id"); err == nil {
		t.Error("parse agent messages must error for embedded subagents")
	}
}

func TestPiPreservesToolCustomUnknownAndBrokenChainContent(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-roles")

	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-roles", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "u", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "question"}`),
		piEntryLine("message", "a", str("u"), "2026-09-04T10:00:02.000Z",
			`"message": {"role": "assistant", "content": [{"type": "text", "text": "answer"}, {"type": "thinking", "thinking": "reason"}, {"type": "toolCall", "id": "call-1", "name": "bash", "arguments": {"command": "pwd"}}, {"type": "futureBlock", "value": 42}]}`),
		piEntryLine("message", "tool", str("a"), "2026-09-04T10:00:03.000Z",
			`"message": {"role": "toolResult", "toolCallId": "call-1", "toolName": "bash", "content": [{"type": "text", "text": "/tmp"}]}`),
		piEntryLine("message", "unknown", str("tool"), "2026-09-04T10:00:04.000Z",
			`"message": {"role": "futureRole", "content": "must survive"}`),
		piEntryLine("custom_message", "custom", str("unknown"), "2026-09-04T10:00:05.000Z",
			`"customType": "extension", "content": "custom context", "display": true`),
		piEntryLine("message", "orphan", str("missing"), "2026-09-04T10:00:06.000Z",
			`"message": {"role": "user", "content": "orphan"}`),
		piEntryLine("label", "final", str("custom"), "2026-09-04T10:00:07.000Z",
			`"targetId": "custom", "label": "final"`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-roles")
	hasAnswer := false
	hasToolResultRole := false
	hasToolResultBlock := false
	hasCustomRole := false
	hasUnsupported := false
	hasMustSurvive := false
	hasOrphan := false
	for _, message := range detail.messages {
		if message.ID == "a" {
			for _, block := range message.Blocks {
				if block.Kind == "output_text" && derefString(block.Text) == "answer" {
					hasAnswer = true
				}
			}
		}
		if message.Role == "toolResult" {
			hasToolResultRole = true
			for _, block := range message.Blocks {
				if block.Kind == "tool_result" {
					hasToolResultBlock = true
				}
			}
		}
		if message.Role == "custom" {
			hasCustomRole = true
		}
		for _, block := range message.Blocks {
			if block.Kind == "unsupported_content" {
				hasUnsupported = true
			}
			if block.Text != nil && strings.Contains(*block.Text, "must survive") {
				hasMustSurvive = true
			}
			if block.Text != nil && *block.Text == "orphan" {
				hasOrphan = true
			}
		}
	}
	if !hasAnswer {
		t.Error("answer block missing")
	}
	if !hasToolResultRole || !hasToolResultBlock {
		t.Error("toolResult role/block missing")
	}
	if !hasCustomRole {
		t.Error("custom role missing")
	}
	if !hasUnsupported {
		t.Error("unsupported_content missing")
	}
	if !hasMustSurvive {
		t.Error("unknown role content must survive")
	}
	if hasOrphan {
		t.Error("broken chain entry (orphan) leaked into timeline")
	}
}

func TestPiSkipsZeroWidthTextPlaceholdersWithoutLosingToolContext(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-zero-width-placeholder")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-zero-width-placeholder", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "assistant-1", nil, "2026-09-04T10:00:01.000Z",
			`"message": {"role": "assistant", "content": [{"type": "text", "text": "​"}, {"type": "thinking", "thinking": "reason"}, {"type": "toolCall", "id": "call-1", "name": "bash", "arguments": {"command": "pwd"}}]}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-zero-width-placeholder")
	var message *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].ID == "assistant-1" {
			message = &detail.messages[i]
		}
	}
	if message == nil {
		t.Fatal("assistant message missing")
	}
	for _, block := range message.Blocks {
		if block.Kind == "output_text" {
			t.Error("zero-width placeholder must be skipped")
		}
	}
	hasThinking := false
	hasTool := false
	for _, block := range message.Blocks {
		if block.Kind == "thinking" && derefString(block.Text) == "reason" {
			hasThinking = true
		}
		if block.Kind == "tool_use" && derefString(block.ToolName) == "bash" && derefString(block.ToolCallID) == "call-1" {
			hasTool = true
		}
	}
	if !hasThinking || !hasTool {
		t.Fatalf("blocks = %+v", message.Blocks)
	}
}

func TestPiPreservesMessageEntriesWithoutMessagePayloads(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-missing-message")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-missing-message", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "missing-message", nil, "2026-09-04T10:00:01.000Z", `"futureField": {"value": 42}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-missing-message")
	if len(detail.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(detail.messages))
	}
	if detail.messages[0].Role != "unknown" {
		t.Errorf("role = %s", detail.messages[0].Role)
	}
	block := detail.messages[0].Blocks[0]
	if block.Kind != "unsupported_content" {
		t.Errorf("block kind = %s", block.Kind)
	}
	if !strings.Contains(derefString(block.Text), "futureField") {
		t.Errorf("block text = %q", derefString(block.Text))
	}
}

func TestPiCycleAtLeafStopsWithoutLooping(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-cycle")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-cycle", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "cycle-a", str("cycle-b"), "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "cycle a"}`),
		piEntryLine("message", "cycle-b", str("cycle-a"), "2026-09-04T10:00:02.000Z", `"message": {"role": "assistant", "content": "cycle b"}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-cycle")
	if len(detail.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(detail.messages))
	}
	if detail.messages[0].ID != "cycle-a" || detail.messages[1].ID != "cycle-b" {
		t.Fatalf("message ids = [%s, %s]", detail.messages[0].ID, detail.messages[1].ID)
	}
}

func TestPiReadsSettingsJSONSessionDirAndEnvTakesPrecedence(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	if err := mkDirAll(filepath.Join(tempHome, ".pi", "agent")); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settingsDir := filepath.Join(tempHome, "settings-sessions")
	if err := mkDirAll(settingsDir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeJSONLRaw(t, filepath.Join(tempHome, ".pi", "agent", "settings.json"),
		fmt.Sprintf(`{"sessionDir": %q}`, settingsDir))
	guard := homeGuard(t, tempHome)
	writeJSONLFile(t, filepath.Join(settingsDir, "2026-09-04T10-00-00-000Z_pi-settings.jsonl"), jsonlLines(t,
		piHeaderLine("pi-settings", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "message-1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "settings directory"}`),
	))

	r, err := reader(SourcePi)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || filepath.Dir(entries[0].Path) != settingsDir {
		t.Fatalf("entries = %+v, want in %s", entries, settingsDir)
	}

	// env 优先级高于 settings.json
	envDir := filepath.Join(tempHome, "env-sessions")
	if err := mkDirAll(envDir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	guard.SetPiSessionDir(envDir)
	writeJSONLFile(t, filepath.Join(envDir, "2026-09-04T10-00-00-000Z_pi-env.jsonl"), jsonlLines(t,
		piHeaderLine("pi-env", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "message-1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "env directory"}`),
	))

	entries, err = r.ListEntries()
	if err != nil {
		t.Fatalf("list entries after env: %v", err)
	}
	if len(entries) != 1 || filepath.Dir(entries[0].Path) != envDir {
		t.Fatalf("entries = %+v, want in %s", entries, envDir)
	}
}

func TestPiParsesV1LinearSessionWithFullHistory(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := filepath.Join(tempHome, ".pi", "agent", "sessions", "--tmp-pi-v1--", "2026-09-04T10-00-00-000Z_pi-v1.jsonl")
	// v1 线性文件：entry 无 id/parentId（header 仍有 id），compaction 用
	// firstKeptEntryIndex
	writeJSONLFile(t, path, jsonlLines(t,
		`{"type": "session", "version": 1, "id": "pi-v1", "timestamp": "2026-09-04T10:00:00.000Z", "cwd": "/tmp/pi-v1"}`,
		`{"type": "message", "timestamp": "2026-09-04T10:00:01.000Z", "message": {"role": "user", "content": "v1 first question"}}`,
		`{"type": "message", "timestamp": "2026-09-04T10:00:02.000Z", "message": {"role": "assistant", "content": [{"type": "text", "text": "v1 answer"}]}}`,
		`{"type": "compaction", "timestamp": "2026-09-04T10:00:03.000Z", "summary": "v1 compacted", "firstKeptEntryIndex": 2, "tokensBefore": 100}`,
		`{"type": "message", "timestamp": "2026-09-04T10:00:04.000Z", "message": {"role": "user", "content": "v1 after compaction"}}`,
	))

	r := readerMust(t, SourcePi)
	detail := readDetailOf(t, r, "pi-v1")
	var texts []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil {
				texts = append(texts, *block.Text)
			}
		}
	}
	if !anyContains(texts, "v1 first question") || !anyContains(texts, "v1 answer") || !anyContains(texts, "v1 after compaction") {
		t.Fatalf("texts = %v", texts)
	}
	hasCompactionSummary := false
	for _, message := range detail.messages {
		if message.Role == "compactionSummary" {
			hasCompactionSummary = true
		}
	}
	if !hasCompactionSummary {
		t.Error("compactionSummary message missing")
	}

	// compaction 的行索引迁移成 entry id
	compaction, ok := firstEventOfKind(detail.events, "compaction")
	if !ok {
		t.Fatal("compaction event missing")
	}
	payload, ok := compaction.Payload.(map[string]any)
	if !ok {
		t.Fatalf("compaction payload = %T", compaction.Payload)
	}
	if _, hasIndex := getJSONObjectPath(payload, "firstKeptEntryIndex"); hasIndex {
		t.Error("firstKeptEntryIndex must be migrated away")
	}
	keptID, hasKept := jsonString(payload, "firstKeptEntryId")
	if !hasKept || keptID == "" {
		t.Error("firstKeptEntryId missing")
	}

	// 标题取自第一条用户消息，而不是退化成 session id
	summary, err := r.ParseSummary("pi-v1")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "v1 first question" {
		t.Errorf("title = %q", summary.Title)
	}
}

func TestPiRestoresCompactionRetainedTailAsMessages(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-compaction")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-compaction", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "user-1", nil, "2026-09-04T10:00:01.000Z", `"message": {"role": "user", "content": "old question"}`),
		piEntryLine("compaction", "compact-1", str("user-1"), "2026-09-04T10:00:02.000Z",
			`"summary": "compacted context", "retainedTail": [{"role": "user", "content": "kept question", "timestamp": 1744366401000}, {"role": "assistant", "content": [{"type": "text", "text": "kept answer"}], "timestamp": 1744366402000}]`),
		piEntryLine("message", "user-2", str("compact-1"), "2026-09-04T10:00:03.000Z", `"message": {"role": "user", "content": "after compaction"}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-compaction")
	var texts []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil {
				texts = append(texts, *block.Text)
			}
		}
	}
	if !anyContains(texts, "kept question") || !anyContains(texts, "kept answer") {
		t.Fatalf("texts = %v", texts)
	}
	// 浏览器展示完整链，compaction 之前的旧消息保留（官方只在重建上下文时裁剪）
	if !anyContains(texts, "old question") {
		t.Error("old question must be retained in browser view")
	}

	// retainedTail 恢复出的消息带有自己的时间
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil && *block.Text == "kept question" {
				if message.Timestamp == nil || *message.Timestamp != 1_744_366_401_000 {
					t.Errorf("kept question timestamp = %v, want 1744366401000", message.Timestamp)
				}
				return
			}
		}
	}
	t.Fatal("kept question message missing")
}

func TestPiParsesToolFailureState(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T10-00-00-000Z", "pi-failure")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-failure", "/tmp/pi-project", "2026-09-04T10:00:00.000Z"),
		piEntryLine("message", "bash-1", nil, "2026-09-04T10:00:01.000Z",
			`"message": {"role": "bashExecution", "command": "exit 1", "output": "boom", "exitCode": 1, "cancelled": true, "truncated": true, "timestamp": 1744366401000}`),
		piEntryLine("message", "assistant-1", str("bash-1"), "2026-09-04T10:00:02.000Z",
			`"message": {"role": "assistant", "content": [{"type": "toolCall", "id": "call-x", "name": "bash", "arguments": {"command": "ls"}}], "timestamp": 1744366402000}`),
		piEntryLine("message", "tool-1", str("assistant-1"), "2026-09-04T10:00:03.000Z",
			`"message": {"role": "toolResult", "toolCallId": "call-x", "toolName": "bash", "content": [{"type": "text", "text": "err"}], "isError": true, "timestamp": 1744366403000}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-failure")
	for _, message := range detail.messages {
		if message.Role != "toolResult" {
			continue
		}
		for _, block := range message.Blocks {
			if block.IsError != nil && *block.IsError {
				return
			}
		}
	}
	t.Fatal("tool failure state (is_error) missing")
}

func TestPiShowsMessageErrorTextInsteadOfRawEmptyMessageRow(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T11-00-00-000Z", "pi-aborted")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-aborted", "/tmp/pi-project", "2026-09-04T11:00:00.000Z"),
		piEntryLine("message", "assistant-aborted", nil, "2026-09-04T11:00:01.000Z",
			`"message": {"role": "assistant", "content": [], "api": "openai-responses", "model": "deepseek-flash", "provider": "reins-custom-gateway", "stopReason": "error", "errorMessage": "This operation was aborted", "timestamp": 1744366401000}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-aborted")
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Kind != "message_error" {
				continue
			}
			if derefString(block.Text) != "This operation was aborted" {
				t.Errorf("error text = %q", derefString(block.Text))
			}
			if block.IsError == nil || !*block.IsError {
				t.Error("message_error must carry is_error")
			}
			// 原始报文保留在 payload 里，界面仍可展开查看
			serialized, _ := jsonMarshal(block.Payload)
			if !strings.Contains(string(serialized), "deepseek-flash") {
				t.Error("error payload lost the raw message")
			}
			return
		}
	}
	t.Fatal("message_error block missing")
}

func TestPiKeepsInjectedContextAsReadableBlock(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T12-00-00-000Z", "pi-context")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-context", "/tmp/pi-project", "2026-09-04T12:00:00.000Z"),
		piEntryLine("message", "user-context", nil, "2026-09-04T12:00:01.000Z",
			`"message": {"role": "user", "content": [{"type": "text", "text": "<environment_context>\n  <cwd>/tmp/pi-project</cwd>\n</environment_context>"}, {"type": "text", "text": "<system-reminder>\n读代码前先看 AGENTS.md\n</system-reminder>"}], "timestamp": 1744366401000}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-context")
	block, ok := firstBlockOfKind(detail.messages, "context_injection")
	if !ok {
		t.Fatal("context injection block missing")
	}
	text := derefString(block.Text)
	if !strings.Contains(text, "<environment_context>") || !strings.Contains(text, "<system-reminder>") {
		t.Errorf("context text = %q", text)
	}
	for _, message := range detail.messages {
		for _, b := range message.Blocks {
			if b.Kind == "empty_message" {
				t.Error("empty_message fallback leaked")
			}
		}
	}
}

func TestPiKeepsRawDiagnosticBlockWhenEmptyMessageHasNoSemantics(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T13-00-00-000Z", "pi-blank")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-blank", "/tmp/pi-project", "2026-09-04T13:00:00.000Z"),
		piEntryLine("message", "assistant-blank", nil, "2026-09-04T13:00:01.000Z",
			`"message": {"role": "assistant", "content": [], "stopReason": "stop", "timestamp": 1744366401000}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-blank")
	// 既没有错误也没有注入上下文：保持原始报文兜底，便于排查来源侧异常
	hasEmpty := false
	hasContextInjection := false
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Kind == "empty_message" {
				hasEmpty = true
			}
			if block.Kind == "context_injection" {
				hasContextInjection = true
			}
		}
	}
	if !hasEmpty {
		t.Error("empty_message fallback missing")
	}
	if hasContextInjection {
		t.Error("context_injection must not appear for plain empty messages")
	}
}

func TestPiTurnsImageItemIntoRenderableImageBlock(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T14-00-00-000Z", "pi-image")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-image", "/tmp/pi-project", "2026-09-04T14:00:00.000Z"),
		piEntryLine("message", "user-image", nil, "2026-09-04T14:00:01.000Z",
			`"message": {"role": "user", "content": [{"type": "text", "text": "这张图是什么"}, {"type": "image", "data": "iVBORw0KGgo=", "mimeType": "image/png"}], "timestamp": 1744366401000}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-image")
	block, ok := firstBlockOfKind(detail.messages, "image")
	if !ok {
		t.Fatal("image block missing")
	}
	// data + mimeType 还原成 data URL，前端才能直接渲染
	if derefString(block.Text) != "data:image/png;base64,iVBORw0KGgo=" {
		t.Errorf("image text = %q", derefString(block.Text))
	}
}

func TestPiRendersSystemPromptSectionsAsReadableBlock(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	path := piSessionPath(tempHome, "/tmp/pi-project", "2026-09-04T15-00-00-000Z", "pi-system")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-system", "/tmp/pi-project", "2026-09-04T15:00:00.000Z"),
		piEntryLine("message", "system-1", nil, "2026-09-04T15:00:01.000Z",
			`"message": {"role": "system", "content": "", "sections": {"cwd": "<cwd>\n/tmp/pi-project\n</cwd>", "preamble": "You are an expert coding assistant.", "rules": "<rules>\n- be concise\n</rules>"}, "toolsAdded": [{"name": "read", "description": "Read the contents of a file.", "parameters": {"type": "object"}, "constrainedSampling": {"type": "json_schema"}}], "toolsRemoved": [{"name": "bash"}], "timestamp": 1744366401000}`),
	))

	detail := readDetailOf(t, readerMust(t, SourcePi), "pi-system")
	block, ok := firstBlockOfKind(detail.messages, "system_prompt")
	if !ok {
		t.Fatal("system prompt block missing")
	}
	text := derefString(block.Text)
	if !strings.Contains(text, "3 段：cwd、preamble、rules") {
		t.Errorf("sections header missing: %q", text)
	}
	if !strings.Contains(text, "## preamble\nYou are an expert coding assistant.") {
		t.Errorf("preamble section missing: %q", text)
	}
	if !strings.Contains(text, "## rules\n<rules>\n- be concise\n</rules>") {
		t.Errorf("rules section missing: %q", text)
	}
	if !strings.Contains(text, "- read: Read the contents of a file.") {
		t.Errorf("added tools missing: %q", text)
	}
	if !strings.Contains(text, "## 本次移除的工具\n- bash") {
		t.Errorf("removed tools missing: %q", text)
	}
	// 工具的 parameters schema 不进正文
	if strings.Contains(text, "json_schema") {
		t.Error("parameters schema leaked into readable text")
	}
}

// pi-subagents 扩展派生的子代理会话（带 parentSession + "#<8位hex>" 会话名）
// 折叠进主会话：列表只出主会话一条，时间线注入 marker 入口，名册列出子代理，
// 弹窗按子会话 id 取完整消息。fork 子会话（有 parentSession、无会话名）不归
// 族，保持独立条目——fork 复制了父会话历史，折叠会整段重复。
func TestPiFoldsSubagentChildSessionsIntoTheParentFamily(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	mainPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-10-29T02-00-00-000Z", "pi-main")
	childAPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-10-29T02-01-00-000Z", "pi-child-a")
	childBPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-10-29T02-02-00-000Z", "pi-child-b")
	forkPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-10-29T02-03-00-000Z", "pi-fork")

	writeJSONLFile(t, mainPath, jsonlLines(t,
		piHeaderLine("pi-main", "/tmp/pi-project", "2026-10-29T02:00:00.000Z"),
		piEntryLine("message", "main-user", nil, "2026-10-29T02:00:01.000Z", `"message": {"role": "user", "content": "main question"}`),
	))

	// 子代理子会话：header 带 parentSession，session_info 名形如 <type>#<8位hex>，
	// 消息含带 usage 的 assistant（家族用量与模型合并的数据来源）。
	childHeader := func(id string, timestamp string) string {
		return fmt.Sprintf(`{"type": "session", "version": 3, "id": %q, "timestamp": %q, "cwd": "/tmp/pi-project", "parentSession": %q}`, id, timestamp, mainPath)
	}
	childAssistant := func(text string) string {
		return fmt.Sprintf(`"message": {"role": "assistant", "content": [{"type": "text", "text": %q}], "provider": "reins", "model": "deepseek-flash", "usage": {"input": 100, "output": 50, "cacheRead": 10, "cacheWrite": 0}}`, text)
	}
	writeJSONLFile(t, childAPath, jsonlLines(t,
		childHeader("pi-child-a", "2026-10-29T02:01:00.000Z"),
		piEntryLine("session_info", "info-a", nil, "2026-10-29T02:01:00.000Z", `"name": "Explore#aaaaaaaa"`),
		piEntryLine("message", "child-a-user", nil, "2026-10-29T02:01:01.000Z", `"message": {"role": "user", "content": "child a task"}`),
		piEntryLine("message", "child-a-assistant", str("child-a-user"), "2026-10-29T02:01:02.000Z", childAssistant("child a answer")),
	))
	writeJSONLFile(t, childBPath, jsonlLines(t,
		childHeader("pi-child-b", "2026-10-29T02:02:00.000Z"),
		piEntryLine("session_info", "info-b", nil, "2026-10-29T02:02:00.000Z", `"name": "Explore#bbbbbbbb"`),
		piEntryLine("message", "child-b-user", nil, "2026-10-29T02:02:01.000Z", `"message": {"role": "user", "content": "child b task"}`),
		piEntryLine("message", "child-b-assistant", str("child-b-user"), "2026-10-29T02:02:02.000Z", childAssistant("child b answer")),
	))
	// fork 子会话：有血缘、无会话名，不归族。
	writeJSONLFile(t, forkPath, jsonlLines(t,
		childHeader("pi-fork", "2026-10-29T02:03:00.000Z"),
		piEntryLine("message", "fork-user", nil, "2026-10-29T02:03:01.000Z", `"message": {"role": "user", "content": "fork question"}`),
	))

	r := readerMust(t, SourcePi)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	var entryIDs []string
	for _, entry := range entries {
		entryIDs = append(entryIDs, entry.SourceSessionID)
	}
	sortStrings(entryIDs)
	if len(entryIDs) != 2 || entryIDs[0] != "pi-fork" || entryIDs[1] != "pi-main" {
		t.Fatalf("entry ids = %v, want [pi-fork pi-main]", entryIDs)
	}

	summary, err := r.ParseSummary("pi-main")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "main question (+2 subagents)" {
		t.Errorf("title = %q", summary.Title)
	}
	// 家族用量 = 全部成员求和：两个子会话各 input=100/output=50/cacheRead=10。
	usage := summary.TokenUsage
	if usage == nil {
		t.Fatal("family usage missing")
	}
	if usage.InputTokens != 200 || usage.OutputTokens != 100 || usage.CacheReadTokens != 20 {
		t.Errorf("family usage = %+v", usage)
	}
	hasModel := false
	for _, model := range summary.Models {
		if model.Model == "deepseek-flash" {
			hasModel = true
		}
	}
	if !hasModel {
		t.Errorf("models = %+v", summary.Models)
	}

	detail := readDetailOf(t, r, "pi-main")
	markerCount := 0
	hasChildA := false
	for _, message := range detail.messages {
		isMarker := false
		for _, block := range message.Blocks {
			if payload, ok := block.Payload.(map[string]any); ok {
				if kind, _ := jsonString(payload, "type"); kind == "subagent_started" {
					isMarker = true
				}
			}
		}
		if isMarker {
			markerCount++
			if message.SessionID != nil && *message.SessionID == "pi-child-a" {
				hasChildA = true
			}
		}
	}
	if markerCount != 2 {
		t.Fatalf("markers = %d, want 2", markerCount)
	}
	if !hasChildA {
		t.Error("child-a marker session id missing")
	}
	var texts []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil {
				texts = append(texts, *block.Text)
			}
		}
	}
	if !anyContains(texts, "child a answer") || !anyContains(texts, "child b answer") {
		t.Fatalf("texts = %v", texts)
	}

	overview, err := r.ParseOverview("pi-main")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if len(overview.Agents) != 3 {
		t.Fatalf("agents = %+v, want 3", overview.Agents)
	}
	rootCount := 0
	hasChildLabel := false
	for _, agent := range overview.Agents {
		if agent.IsRoot {
			rootCount++
		}
		if agent.SessionID == "pi-child-a" && agent.Label == "Explore#aaaaaaaa(子)" {
			hasChildLabel = true
		}
	}
	if rootCount != 1 || !hasChildLabel {
		t.Fatalf("agents roster = %+v", overview.Agents)
	}
	if len(overview.SourcePaths) != 3 {
		t.Errorf("source paths = %d, want 3", len(overview.SourcePaths))
	}

	// 弹窗口径：marker + 该子代理会话的完整消息；fork 不是子代理，不可寻址。
	agentMessages, err := GetSessionAgentMessagesInner(SourcePi, "pi-main", "pi-child-a")
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	if len(agentMessages) != 3 {
		t.Fatalf("agent messages = %d, want 3", len(agentMessages))
	}
	if agentMessages[0].SessionID == nil || *agentMessages[0].SessionID != "pi-child-a" {
		t.Errorf("first agent message session id = %v", agentMessages[0].SessionID)
	}
	if _, err := GetSessionAgentMessagesInner(SourcePi, "pi-main", "pi-fork"); err == nil {
		t.Error("fork agent messages must error")
	}
}

// 旧版扩展（workflow/async run）的子代理会话落在 <主会话stem>/<runId>/run-0/
// 嵌套目录，session_info 名是 "subagent-<agent>-<runId>-1"（无 # 后缀）。
// 通用判据（parentSession + session_info 有名）应同样归族。
func TestPiFoldsLegacyWorkflowRunSessionsIntoTheParentFamily(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	mainPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-10-16T09-32-05-247Z", "pi-legacy-main")
	writeJSONLFile(t, mainPath, jsonlLines(t,
		piHeaderLine("pi-legacy-main", "/tmp/pi-project", "2026-10-16T09:32:05.247Z"),
		piEntryLine("message", "legacy-user", nil, "2026-10-16T09:32:06.000Z", `"message": {"role": "user", "content": "legacy main"}`),
	))

	runDir := filepath.Join(tempHome, ".pi", "agent", "sessions", "--tmp-pi-project--",
		"2026-10-16T09-32-05-247Z_pi-legacy-main", "d460ee11-run", "run-0")
	runHeader := fmt.Sprintf(`{"type": "session", "version": 3, "id": "pi-legacy-run", "timestamp": "2026-10-23T10:21:33.902Z", "cwd": "/tmp/pi-project", "parentSession": %q}`, mainPath)
	writeJSONLFile(t, filepath.Join(runDir, "session.jsonl"), jsonlLines(t,
		runHeader,
		piEntryLine("session_info", "run-info", nil, "2026-10-23T10:21:33.904Z", `"name": "subagent-scout-d460ee11-d658-4bde-854f-2220be32933b-1"`),
		piEntryLine("message", "run-user", nil, "2026-10-23T10:21:34.000Z", `"message": {"role": "user", "content": "run task"}`),
		piEntryLine("message", "run-assistant", str("run-user"), "2026-10-23T10:21:35.000Z",
			`"message": {"role": "assistant", "content": [{"type": "text", "text": "run answer"}], "provider": "reins", "model": "deepseek-flash", "usage": {"input": 10, "output": 5, "cacheRead": 0, "cacheWrite": 0}}`),
	))

	r := readerMust(t, SourcePi)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	summary, err := r.ParseSummary("pi-legacy-main")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "legacy main (+1 subagents)" {
		t.Errorf("title = %q", summary.Title)
	}
	usage := summary.TokenUsage
	if usage == nil || usage.InputTokens != 10 {
		t.Fatalf("usage = %+v, want input 10", usage)
	}

	detail := readDetailOf(t, r, "pi-legacy-main")
	hasRun := false
	for _, message := range detail.messages {
		if message.SessionID != nil && *message.SessionID == "pi-legacy-run" {
			hasRun = true
		}
	}
	if !hasRun {
		t.Error("run member messages missing in family timeline")
	}
	agentMessages, err := GetSessionAgentMessagesInner(SourcePi, "pi-legacy-main", "pi-legacy-run")
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	if len(agentMessages) != 3 {
		t.Fatalf("agent messages = %d, want 3", len(agentMessages))
	}
}

// subagent-tasks runner（task.json + output.jsonl 事件流）按 task.json 的
// sessionId 归族；时间线取 message_end 的最终消息，摘要从 assistant 消息
// 合成。
func TestPiFoldsSubagentTaskRunsIntoTheParentFamily(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)
	mainPath := piSessionPath(tempHome, "/tmp/pi-project", "2026-10-23T02-44-12-987Z", "pi-task-main")
	writeJSONLFile(t, mainPath, jsonlLines(t,
		piHeaderLine("pi-task-main", "/tmp/pi-project", "2026-10-23T02:44:12.987Z"),
		piEntryLine("message", "task-main-user", nil, "2026-10-23T02:44:13.000Z", `"message": {"role": "user", "content": "main question"}`),
	))

	taskDir := filepath.Join(tempHome, ".pi", "agent", "subagent-tasks", "e29ae30b-task")
	writeJSONLFile(t, filepath.Join(taskDir, "output.jsonl"), jsonlLines(t,
		`{"type": "message_start", "message": {"role": "user", "content": "task prompt (streaming)"}}`,
		`{"type": "message_end", "message": {"role": "user", "content": "task prompt", "timestamp": 1792724376000}}`,
		`{"type": "message_end", "message": {"role": "assistant", "content": [{"type": "text", "text": "task answer"}], "provider": "reins", "model": "deepseek-flash", "usage": {"input": 100, "output": 50, "cacheRead": 10, "cacheWrite": 0}, "timestamp": 1792724595685}}`,
	))
	writeJSONLRaw(t, filepath.Join(taskDir, "task.json"),
		`{"version": 1, "id": "e29ae30b-task", "status": "completed", "agent": "scout", "sessionId": "pi-task-main", "createdAt": 1792724375849}`)

	r := readerMust(t, SourcePi)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	summary, err := r.ParseSummary("pi-task-main")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "main question (+1 subagents)" {
		t.Errorf("title = %q", summary.Title)
	}
	usage := summary.TokenUsage
	if usage == nil || usage.InputTokens != 100 || usage.OutputTokens != 50 {
		t.Fatalf("usage = %+v", usage)
	}
	hasModel := false
	for _, model := range summary.Models {
		if model.Model == "deepseek-flash" {
			hasModel = true
		}
	}
	if !hasModel {
		t.Errorf("models = %+v", summary.Models)
	}

	detail := readDetailOf(t, r, "pi-task-main")
	// marker + 流式增量（message_start）不入时间线，只有 message_end 的最终消息
	runMessages := 0
	for _, message := range detail.messages {
		if message.SessionID == nil || *message.SessionID != "e29ae30b-task" {
			continue
		}
		isMarker := false
		for _, block := range message.Blocks {
			if payload, ok := block.Payload.(map[string]any); ok {
				if kind, _ := jsonString(payload, "type"); kind == "subagent_started" {
					isMarker = true
				}
			}
		}
		if !isMarker {
			runMessages++
		}
	}
	if runMessages != 2 {
		t.Fatalf("run messages = %d, want 2", runMessages)
	}
	var texts []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil {
				texts = append(texts, *block.Text)
			}
		}
	}
	if anyContains(texts, "streaming") {
		t.Error("message_start increment leaked into timeline")
	}
	if !anyContains(texts, "task answer") {
		t.Error("task answer missing")
	}

	agentMessages, err := GetSessionAgentMessagesInner(SourcePi, "pi-task-main", "e29ae30b-task")
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	if len(agentMessages) != 3 {
		t.Fatalf("agent messages = %d, want 3", len(agentMessages))
	}
}

// 父会话已删（或不在当前根）的 subagent-tasks run 不独立成条目；数据仍在
// 磁盘，父会话重新出现时自动归族。
func TestPiSkipsOrphanSubagentTaskRuns(t *testing.T) {
	root := t.TempDir()
	agentRoot := t.TempDir()
	store := tempStore(t)
	path := filepath.Join(root, "2026-10-23T02-44-12-987Z_pi-orphan-main.jsonl")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-orphan-main", "/tmp/pi-project", "2026-10-23T02:44:12.987Z"),
		piEntryLine("message", "orphan-user", nil, "2026-10-23T02:44:13.000Z", `"message": {"role": "user", "content": "main question"}`),
	))

	taskDir := filepath.Join(agentRoot, "subagent-tasks", "orphan-run")
	writeJSONLFile(t, filepath.Join(taskDir, "output.jsonl"), jsonlLines(t,
		`{"type": "message_end", "message": {"role": "assistant", "content": [{"type": "text", "text": "orphan answer"}], "timestamp": 1792724595685}}`,
	))
	writeJSONLRaw(t, filepath.Join(taskDir, "task.json"),
		`{"version": 1, "id": "orphan-run", "status": "completed", "agent": "scout", "sessionId": "pi-deleted-parent", "createdAt": 1792724375849}`)

	r := newPiReader(root, agentRoot, store)
	defer r.Close()
	index, err := r.familyIndex()
	if err != nil {
		t.Fatalf("family index: %v", err)
	}
	if len(index.Families) != 1 || index.Families[0].Root.id != "pi-orphan-main" {
		t.Fatalf("families = %+v, want one pi-orphan-main", index.Families)
	}
}

// —— 小件 ——

// 直构接缝：sessions 根、subagent-tasks 根与持久缓存目录都由参数注入，
// 不经 env 重定向即可测试——与引擎系的 NewReaderEngine(spec, root, storeDir)
// 同待遇；生产路径仍经 piReaderInstance.current 解析 HOME（由 readerMust
// 系测试覆盖）。
func TestPiReaderDirectConstructionInjectsRootAndStoreDir(t *testing.T) {
	root := t.TempDir()
	store := tempStore(t)
	path := filepath.Join(root, "2026-11-22T10-00-00-000Z_pi-direct.jsonl")
	writeJSONLFile(t, path, jsonlLines(t,
		piHeaderLine("pi-direct", "/tmp/pi-project", "2026-11-22T10:00:00.000Z"),
		piEntryLine("message", "user-1", nil, "2026-11-22T10:00:01.000Z", `"message": {"role": "user", "content": "direct question"}`),
		piEntryLine("message", "assistant-1", str("user-1"), "2026-11-22T10:00:02.000Z", `"message": {"role": "assistant", "content": [{"type": "text", "text": "direct answer"}]}`),
	))

	r := newPiReader(root, t.TempDir(), store)
	defer r.Close()

	// 与 piReaderInstance.ListEntries/ParseSummary 同一条部件链（实例层只是
	// current() 外的薄转发）。
	index, err := r.familyIndex()
	if err != nil {
		t.Fatalf("family index: %v", err)
	}
	if len(index.Families) != 1 || index.Families[0].Root.id != "pi-direct" {
		t.Fatalf("families = %+v, want one pi-direct", index.Families)
	}
	family, err := r.familyForID("pi-direct")
	if err != nil {
		t.Fatalf("family for id: %v", err)
	}
	summary, err := r.familySummary(family)
	if err != nil {
		t.Fatalf("family summary: %v", err)
	}
	if summary.Title != "direct question" {
		t.Errorf("title = %q, want direct question", summary.Title)
	}
	// storeDir 注入生效：familySummary 触发持久层连接，库文件落在注入目录。
	if !pathExists(filepath.Join(store, SummaryDBFileName)) {
		t.Errorf("summary db missing under injected storeDir %s", store)
	}
}

func str(value string) *string { return &value }

func readerMust(t *testing.T, app SourceApp) Reader {
	t.Helper()
	r, err := reader(app)
	if err != nil {
		t.Fatalf("reader %s: %v", app, err)
	}
	return r
}

func mkDirAll(path string) error { return os.MkdirAll(path, 0o777) }

func anyContains(list []string, needle string) bool {
	for _, item := range list {
		if strings.Contains(item, needle) {
			return true
		}
	}
	return false
}

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
