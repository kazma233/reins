package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// session_readers.rs：各读取器的行为分区——标题提取、注入上下文、图片还原、
// 本地命令、family 聚合、模型归属、压缩与计数。

func TestOpenCodeRootSessionAggregatesSubagentSessions(t *testing.T) {
	root := t.TempDir()
	rootID := "ses_root_session"
	childID := "ses_child_session"
	seedOpenCodeFamilyAt(t, root, rootID, childID)

	reader := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, root, tempStore(t))
	summary, err := reader.ParseSummary(rootID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != rootID {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}
	if !strings.Contains(summary.Title, "+1 subagents") {
		t.Errorf("title = %q", summary.Title)
	}

	detail := readDetailOf(t, reader, rootID)
	assertAnyBlockTextContains(t, detail.messages, "Sub-agent session: Child task")
	if !anyEventKind(detail.events, "subagent_started") {
		t.Error("subagent_started event missing")
	}
	suffix := ":" + childID
	if !anyPathEndsWith(detail.sourcePaths, suffix) {
		t.Errorf("source paths %v missing suffix %s", detail.sourcePaths, suffix)
	}
}

func TestOpenCodeOverviewCountsMatchLoadedTimeline(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeFamilyAt(t, root, "ses_root_session", "ses_child_session")

	reader := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, root, tempStore(t))
	overview, err := reader.ParseOverview("ses_root_session")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	detail := readDetailOf(t, reader, "ses_root_session")

	if overview.MessageCount == nil || *overview.MessageCount != len(detail.messages) {
		t.Errorf("message count = %v, want %d", overview.MessageCount, len(detail.messages))
	}
	if overview.EventCount == nil || *overview.EventCount != len(detail.events) {
		t.Errorf("event count = %v, want %d", overview.EventCount, len(detail.events))
	}
}

func TestCodexIgnoresAgentsBannerWhenPickingTitle(t *testing.T) {
	root := t.TempDir()
	sessionID := "33333333-3333-4333-8333-333333333333"
	transcriptPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		`{"timestamp": "2026-04-21T12:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "# AGENTS.md instructions for /tmp/reins/workspace\n<system-reminder>\nYour operational mode has changed from plan to build.\nYou are no longer in read-only mode.\nYou are permitted to make file changes, run shell commands, and utilize your arsenal of tools as needed.\n</system-reminder>"}]}}`,
		`{"timestamp": "2026-04-21T12:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "How do I list the current files?"}]}}`,
	))

	summary, err := NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t)).ParseSummary(sessionID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != sessionID {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}
	if summary.Title != "How do I list the current files?" {
		t.Errorf("title = %q", summary.Title)
	}
}

func TestCodexSkipsAgentsInstructionsBlockWhenPickingTitle(t *testing.T) {
	root := t.TempDir()
	sessionID := "44444444-4444-4444-8444-444444444444"
	transcriptPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		`{"timestamp": "2026-04-21T12:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "# AGENTS.md instructions for /tmp/reins/workspace\n\n<INSTRUCTIONS>\n# 通用偏好\n\n- 使用中文回复，回复简洁直接。\n- 代码注释使用中文。\n</INSTRUCTIONS>"}, {"type": "text", "text": "<environment_context>\n  <cwd>/tmp/reins/workspace</cwd>\n  <shell>zsh</shell>\n</environment_context>"}]}}`,
		`{"timestamp": "2026-04-21T12:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "帮我看一下这个报错"}]}}`,
	))

	summary, err := NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t)).ParseSummary(sessionID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "帮我看一下这个报错" {
		t.Errorf("title = %q", summary.Title)
	}
}

func TestClaudeIgnoresAgentsBannerWhenPickingTitle(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "projects", "demo-project", "session-123.jsonl")
	writeJSONLFile(t, sessionFile, jsonlLines(t,
		`{"type": "user", "message": {"content": "# AGENTS.md instructions for /tmp/reins/workspace\n<system-reminder>\nYour operational mode has changed from plan to build.\nYou are no longer in read-only mode.\nYou are permitted to make file changes, run shell commands, and utilize your arsenal of tools as needed.\n</system-reminder>"}}`,
		`{"type": "user", "message": {"content": "How do I list the current files?"}}`,
	))

	summary, err := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t)).ParseSummary("session-123")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.Title != "How do I list the current files?" {
		t.Errorf("title = %q", summary.Title)
	}
}

func TestClaudeKeepsInjectedContextAsReadableBlock(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "projects", "demo-project", "session-context.jsonl")
	writeJSONLFile(t, sessionFile, jsonlLines(t,
		// 字符串正文的注入上下文（Claude Code 的典型形态）：清洗后没有对话内容
		`{"type": "user", "message": {"content": "# AGENTS.md instructions for /tmp/reins/workspace\n<system-reminder>\nYour operational mode has changed from plan to build.\n</system-reminder>"}}`,
	))

	reader := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t))
	detail := readDetailOf(t, reader, "session-context")
	block, ok := firstBlockOfKind(detail.messages, "context_injection")
	if !ok {
		t.Fatal("context injection block missing")
	}
	text := derefString(block.Text)
	if !strings.Contains(text, "AGENTS.md instructions for /tmp/reins/workspace") || !strings.Contains(text, "<system-reminder>") {
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

func TestClaudeTurnsImageBlocksIntoRenderableImageBlocks(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "projects", "demo-project", "session-image.jsonl")
	writeJSONLFile(t, sessionFile, jsonlLines(t,
		`{"type": "user", "message": {"content": [{"type": "text", "text": "看下这两张图"}, {"type": "image", "source": {"type": "base64", "media_type": "image/jpeg", "data": "/9j/4AAQ"}}, {"type": "image", "source": {"type": "url", "url": "https://example.invalid/a.png"}}]}}`,
	))

	reader := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t))
	detail := readDetailOf(t, reader, "session-image")
	var references []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Kind == "image" && block.Text != nil {
				references = append(references, *block.Text)
			}
		}
	}
	want := []string{"data:image/jpeg;base64,/9j/4AAQ", "https://example.invalid/a.png"}
	if len(references) != 2 || references[0] != want[0] || references[1] != want[1] {
		t.Fatalf("references = %v, want %v", references, want)
	}
}

func TestClaudeRendersLocalCommandRecordsAsReadableBlocks(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "projects", "demo-project", "session-local-command.jsonl")
	writeJSONLFile(t, sessionFile, jsonlLines(t,
		`{"type": "user", "message": {"role": "user", "content": "<local-command-caveat>The command below was run directly in Claude Code, not sent to you as a request, and its output goes straight to the user. It's recorded here as context for later messages.</local-command-caveat>"}}`,
		`{"type": "user", "message": {"role": "user", "content": "<command-name>/exit</command-name>\n            <command-message>exit</command-message>\n            <command-args></command-args>"}}`,
		`{"type": "user", "message": {"role": "user", "content": "<local-command-stdout>See ya!</local-command-stdout>"}}`,
	))

	reader := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t))
	detail := readDetailOf(t, reader, "session-local-command")

	type blockTuple struct {
		kind string
		text *string
	}
	var blocks []blockTuple
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			blocks = append(blocks, blockTuple{kind: block.Kind, text: block.Text})
		}
	}

	foundCommand, foundOutput, foundCaveat, foundEmpty := false, false, false, false
	for _, block := range blocks {
		if block.kind == "local_command" && block.text != nil && *block.text == "/exit" {
			foundCommand = true
		}
		if block.kind == "local_command_output" && block.text != nil && *block.text == "See ya!" {
			foundOutput = true
		}
		if block.kind == "context_injection" && block.text != nil && strings.HasPrefix(*block.text, "<local-command-caveat>") {
			// caveat 是记录用上下文，归到上下文块。
			foundCaveat = true
		}
		if block.kind == "empty_message" {
			foundEmpty = true
		}
	}
	if !foundCommand || !foundOutput || !foundCaveat {
		t.Fatalf("blocks = %+v", blocks)
	}
	// 三条都不是对话内容，但都不再退化成原始报文兜底块。
	if foundEmpty {
		t.Error("empty_message fallback leaked")
	}
}

func TestClaudeSkipsUnreadableFilesWhenIndexing(t *testing.T) {
	root := t.TempDir()
	validPath := filepath.Join(root, "projects", "demo-project", "session-123.jsonl")
	invalidPath := filepath.Join(root, "projects", "demo-project", "broken.jsonl")

	writeJSONLFile(t, validPath, jsonlLines(t,
		`{"type": "user", "message": {"content": "Valid task"}}`,
	))
	writeJSONLRaw(t, invalidPath, "{ not valid json }\n")

	reader := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t))
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Path != validPath {
		t.Errorf("path = %s, want %s", entries[0].Path, validPath)
	}
}

func TestClaudeExposesUnsupportedMessageContent(t *testing.T) {
	root := t.TempDir()
	sessionFile := filepath.Join(root, "projects", "demo-project", "session-unsupported.jsonl")
	writeJSONLFile(t, sessionFile, jsonlLines(t,
		`{"timestamp": "2026-04-21T12:00:00.000Z", "sessionId": "session-unsupported", "type": "assistant", "message": {"content": {"unexpected": true}}}`,
	))

	reader := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t))
	detail := readDetailOf(t, reader, "session-unsupported")
	block, ok := firstBlockOfKind(detail.messages, "unsupported_content")
	if !ok {
		t.Fatal("unsupported_content block missing")
	}
	if !strings.Contains(derefString(block.Text), "\"unexpected\": true") {
		t.Errorf("text = %q", derefString(block.Text))
	}
}

func TestCodexTurnsInputImageIntoRenderableImageBlock(t *testing.T) {
	root := t.TempDir()
	sessionID := "88888888-8888-4888-8888-888888888888"
	transcriptPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		// 粘贴的图片：没有 text 字段，引用要提成正文而不是当成未支持块
		`{"timestamp": "2026-04-21T12:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "看下这张图"}, {"type": "input_image", "detail": "high", "image_url": "data:image/png;base64,iVBORw0KGgo="}]}}`,
		`{"timestamp": "2026-04-21T12:00:02.000Z", "type": "response_item", "payload": {"type": "reasoning", "content": null, "encrypted_content": "SYNTHETIC_BLOB"}}`,
	))

	detail := readDetailOf(t, NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t)), sessionID)
	block, ok := firstBlockOfKind(detail.messages, "image")
	if !ok {
		t.Fatal("image block missing")
	}
	if derefString(block.Text) != "data:image/png;base64,iVBORw0KGgo=" {
		t.Errorf("image text = %q", derefString(block.Text))
	}
	for _, message := range detail.messages {
		for _, b := range message.Blocks {
			if b.Kind == "unsupported_block" {
				t.Error("unsupported_block leaked")
			}
		}
	}
	// raw 事件给整条记录：reasoning 的 payload 也带 timestamp/type 信封
	event, ok := firstEventOfKind(detail.events, "reasoning")
	if !ok {
		t.Fatal("reasoning event missing")
	}
	payload, ok := event.Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload = %T", event.Payload)
	}
	if kind, _ := jsonString(payload, "type"); kind != "response_item" {
		t.Errorf("payload type = %q", kind)
	}
	if ts, _ := jsonString(payload, "timestamp"); ts != "2026-04-21T12:00:02.000Z" {
		t.Errorf("payload timestamp = %q", ts)
	}
	// 加密字段是唯一例外
	serialized, _ := json.Marshal(payload)
	if strings.Contains(string(serialized), "SYNTHETIC_BLOB") {
		t.Error("encrypted content leaked into payload")
	}
}

func TestCodexKeepsInjectedContextAsReadableBlock(t *testing.T) {
	root := t.TempDir()
	sessionID := "77777777-7777-4777-8777-777777777777"
	transcriptPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		// 宿主注入的上下文消息：清洗后没有对话内容，但整段原文要能看
		`{"timestamp": "2026-04-21T12:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "# AGENTS.md instructions for /tmp/reins/workspace\n\n<INSTRUCTIONS>\n使用中文回复。\n</INSTRUCTIONS>"}, {"type": "input_text", "text": "<environment_context>\n  <cwd>/tmp/reins/workspace</cwd>\n</environment_context>"}], "internal_chat_message_metadata_passthrough": {"content_item_kinds": ["agents_md.instructions", "environments.environment_context"]}}}`,
	))

	detail := readDetailOf(t, NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t)), sessionID)
	block, ok := firstBlockOfKind(detail.messages, "context_injection")
	if !ok {
		t.Fatal("context injection block missing")
	}
	text := derefString(block.Text)
	if !strings.Contains(text, "AGENTS.md instructions for /tmp/reins/workspace") || !strings.Contains(text, "<environment_context>") {
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

func TestCodexExposesUnsupportedMessageContentAndBlocks(t *testing.T) {
	root := t.TempDir()
	sessionID := "66666666-6666-4666-8666-666666666666"
	transcriptPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		`{"timestamp": "2026-04-21T12:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": {"unexpected": true}}}`,
		`{"timestamp": "2026-04-21T12:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "new_block_shape", "value": 42}]}}`,
	))

	detail := readDetailOf(t, NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t)), sessionID)
	contentBlock, hasContent := firstBlockOfKind(detail.messages, "unsupported_content")
	blockBlock, hasBlock := firstBlockOfKind(detail.messages, "unsupported_block")
	if !hasContent || !strings.Contains(derefString(contentBlock.Text), "\"unexpected\": true") {
		t.Fatalf("unsupported_content missing: %v", hasContent)
	}
	if !hasBlock || !strings.Contains(derefString(blockBlock.Text), "\"new_block_shape\"") {
		t.Fatalf("unsupported_block missing: %v", hasBlock)
	}
}

func TestOpenCodeExposesMessagesWithoutVisibleParts(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "opencode.db")

	db := openSQLite(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
        CREATE TABLE session_v2 (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT,
            slug TEXT NOT NULL, directory TEXT NOT NULL, title TEXT,
            version TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL
        );
        CREATE TABLE session_message (
            id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
            seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
            data TEXT NOT NULL
        );`)

	sessionID := "ses_empty_parts"
	mustExec(t, db, `INSERT INTO session_v2 (id, project_id, slug, directory, title, version, time_created, time_updated) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`,
		sessionID, "project-1", sessionID, "/tmp/root", "Empty parts", "1", int64(1_744_366_400_000), int64(1_744_366_400_000))
	// assistant 行的 data 没有 content 数组：不产生可见块，应回退为
	// empty_message 诊断块并携带原始 data 作为 payload
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`,
		"msg-empty", sessionID, "assistant", 1, int64(1_744_366_400_000), int64(1_744_366_400_000),
		`{"time": {"created": 1744366400000}, "agent": "build"}`)

	detail := readDetailOf(t, NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, root, tempStore(t)), sessionID)
	block, ok := firstBlockOfKind(detail.messages, "empty_message")
	if !ok {
		t.Fatal("empty_message block missing")
	}
	if !strings.Contains(derefString(block.Text), "\"agent\": \"build\"") {
		t.Errorf("text = %q", derefString(block.Text))
	}
}

func TestOpenCodeV2ParsesToolContentAndNonMessageEvents(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "opencode.db")

	db := openSQLite(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
        CREATE TABLE session_v2 (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT,
            slug TEXT NOT NULL, directory TEXT NOT NULL, title TEXT,
            version TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL
        );
        CREATE TABLE session_message (
            id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
            seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
            data TEXT NOT NULL
        );`)

	sessionID := "ses_v2_content"
	mustExec(t, db, `INSERT INTO session_v2 (id, project_id, slug, directory, title, version, time_created, time_updated) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`,
		sessionID, "project-1", sessionID, "/tmp/root", "V2 content", "1", int64(1_744_366_400_000), int64(1_744_366_400_000))

	// v2 assistant 消息：工具输入在 state.input、输出在 state.content 块数组
	messageTime := int64(1_744_366_400_000)
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`,
		"msg-assistant", sessionID, "assistant", 1, messageTime, messageTime,
		`{"time": {"created": 1744366400000}, "agent": "build", "content": [{"type": "reasoning", "text": "thinking out loud"}, {"type": "tool", "id": "call_v2_1", "name": "grep", "state": {"status": "completed", "input": {"pattern": "session_v2"}, "content": [{"type": "text", "text": "match line 1"}, {"type": "text", "text": "match line 2"}], "metadata": {}}}, {"type": "text", "text": "done"}]}`)

	// 非 user/assistant 行归入事件，kind 用 type 列
	idleTime := int64(1_744_366_401_000)
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`,
		"msg-idle", sessionID, "idle", 2, idleTime, idleTime,
		`{"time": {"created": 1744366401000}, "outcome": "succeeded"}`)
	compactionTime := int64(1_744_366_402_000)
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`,
		"msg-compaction", sessionID, "compaction", 3, compactionTime, compactionTime,
		`{"status": "completed", "reason": "auto", "summary": "## 总结：v2 读取改造"}`)

	detail := readDetailOf(t, NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, root, tempStore(t)), sessionID)

	// 消息时间线只有 user/assistant 行
	if len(detail.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(detail.messages))
	}
	blocks := detail.messages[0].Blocks
	if blocks[0].Kind != "thinking" || derefString(blocks[0].Text) != "thinking out loud" {
		t.Fatalf("blocks[0] = %s %v", blocks[0].Kind, blocks[0].Text)
	}

	call := blocks[1]
	if call.Kind != "function_call" || derefString(call.ToolName) != "grep" || derefString(call.ToolCallID) != "call_v2_1" {
		t.Fatalf("call = %+v", call)
	}
	if !strings.Contains(derefString(call.Text), "session_v2") {
		t.Errorf("call text = %q", derefString(call.Text))
	}

	output := blocks[2]
	if output.Kind != "function_call_output" || derefString(output.ToolCallID) != "call_v2_1" {
		t.Fatalf("output = %+v", output)
	}
	if derefString(output.Text) != "match line 1\nmatch line 2" {
		t.Errorf("output text = %q", derefString(output.Text))
	}

	if blocks[3].Kind != "text" || derefString(blocks[3].Text) != "done" {
		t.Fatalf("blocks[3] = %s %v", blocks[3].Kind, blocks[3].Text)
	}

	// idle/compaction 进事件面板，摘要带最有信息量的字段
	if len(detail.events) != 2 {
		t.Fatalf("events = %d, want 2", len(detail.events))
	}
	idle, ok := firstEventOfKind(detail.events, "idle")
	if !ok || idle.Summary != "idle: succeeded" {
		t.Fatalf("idle event = %+v", idle)
	}
	// raw payload 给整行：id/type/time_created + data 列原文
	payload, ok := idle.Payload.(map[string]any)
	if !ok {
		t.Fatalf("idle payload = %T", idle.Payload)
	}
	if kind, _ := jsonString(payload, "type"); kind != "idle" {
		t.Errorf("payload type = %q", kind)
	}
	if id, _ := jsonString(payload, "id"); id != "msg-idle" {
		t.Errorf("payload id = %q", id)
	}
	if _, hasTime := getJSONObjectPath(payload, "time_created"); !hasTime {
		t.Error("payload time_created missing")
	}
	if _, hasData := getJSONObjectPath(payload, "data"); !hasData {
		t.Error("payload data missing")
	}
	compaction, ok := firstEventOfKind(detail.events, "compaction")
	if !ok || !strings.Contains(compaction.Summary, "v2 读取改造") {
		t.Fatalf("compaction event = %+v", compaction)
	}
}

func TestClaudeRootSessionAggregatesSubagentSessions(t *testing.T) {
	root := t.TempDir()
	rootID := "root-session"
	childID := "child-session"
	projectDir := filepath.Join(root, "projects", "demo-project")
	rootPath := filepath.Join(projectDir, rootID+".jsonl")
	childPath := filepath.Join(projectDir, "subagents", "agent-"+childID+".jsonl")
	childMetaPath := filepath.Join(projectDir, "subagents", "agent-"+childID+".meta.json")

	writeJSONLFile(t, rootPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Main task"}}`, rootID),
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:01.000Z", "sessionId": %q, "type": "assistant", "message": {"content": "Root answer"}}`, rootID),
	))
	writeJSONLFile(t, childPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T15:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Child task"}}`, rootID),
		fmt.Sprintf(`{"timestamp": "2026-04-21T15:00:01.000Z", "sessionId": %q, "type": "assistant", "message": {"content": "Child answer"}}`, rootID),
	))
	writeJSONLRaw(t, childMetaPath, `{"agentType":"Explore","description":"Find fetch_rss scheduling code"}`)

	reader := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, root, tempStore(t))
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Path != rootPath {
		t.Errorf("entry path = %s, want %s", entries[0].Path, rootPath)
	}
	if !strings.Contains(entries[0].Summary.Title, "+1 subagents") {
		t.Errorf("entry title = %q", entries[0].Summary.Title)
	}

	summary, err := reader.ParseSummary(rootID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if !strings.Contains(summary.Title, "+1 subagents") {
		t.Errorf("title = %q", summary.Title)
	}

	overview, err := reader.ParseOverview(rootID)
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if overview.Summary.SourceSessionID != rootID {
		t.Errorf("overview session id = %s", overview.Summary.SourceSessionID)
	}
	if len(overview.Agents) != 2 {
		t.Fatalf("agents = %+v, want 2", overview.Agents)
	}
	ids := map[string]bool{}
	hasRoot := false
	hasChild := false
	for _, agent := range overview.Agents {
		ids[agent.SessionID] = true
		if agent.IsRoot && agent.SessionID == rootID && agent.Label == "主 Agent" {
			hasRoot = true
		}
		if !agent.IsRoot && agent.SessionID == childID && agent.Label == "Find fetch_rss scheduling code(子)" {
			hasChild = true
		}
	}
	if len(ids) != 2 || !hasRoot || !hasChild {
		t.Fatalf("agents roster = %+v", overview.Agents)
	}

	detail := readDetailOf(t, reader, rootID)
	if len(detail.sourcePaths) != 2 {
		t.Fatalf("source paths = %v, want 2", detail.sourcePaths)
	}
	if !anyPathEndsWith(detail.sourcePaths, rootPath) || !anyPathEndsWith(detail.sourcePaths, childPath) {
		t.Fatalf("source paths = %v, want %s and %s", detail.sourcePaths, rootPath, childPath)
	}
	assertAnyBlockTextContains(t, detail.messages, "Sub-agent session: Find fetch_rss scheduling code")
	if !anyEventKind(detail.events, "subagent_started") {
		t.Error("subagent_started event missing")
	}
}

// 末段断言走 GetSessionAgentMessagesInner（命令层入口，经注册表读 env
// BACKEND），因此整个用例保持 env 根，不迁移 engine 直构。
func TestCodexRootSessionAggregatesSubagentSessions(t *testing.T) {
	tempHome := testsupportHomeDir(t)
	homeGuard(t, tempHome)

	rootID := "44444444-4444-4444-8444-444444444444"
	childID := "55555555-5555-4555-8555-555555555555"
	rootPath := filepath.Join(tempHome, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", rootID))
	childPath := filepath.Join(tempHome, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T15-00-00-%s.jsonl", childID))

	writeJSONLFile(t, rootPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, rootID),
		`{"timestamp": "2026-04-21T12:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Main task"}]}}`,
		`{"timestamp": "2026-04-21T12:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Main answer"}]}}`,
	))
	writeJSONLFile(t, childPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T15:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace", "source": {"subagent": {"thread_spawn": {"parent_thread_id": %q, "agent_nickname": "Plato", "agent_role": "worker"}}}, "agent_nickname": "Plato", "agent_role": "worker"}}`, childID, rootID),
		`{"timestamp": "2026-04-21T15:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Child task"}]}}`,
		`{"timestamp": "2026-04-21T15:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Child answer"}]}}`,
	))

	r, err := reader(SourceCodex)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	summary, err := r.ParseSummary(rootID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != rootID {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}
	if !strings.Contains(summary.Title, "+1 subagents") {
		t.Errorf("title = %q", summary.Title)
	}

	detail := readDetailOf(t, r, rootID)
	assertAnyBlockTextContains(t, detail.messages, "Sub-agent session: Plato")
	if !anyEventKind(detail.events, "subagent_started") {
		t.Error("subagent_started event missing")
	}
	if !anyPathEndsWith(detail.sourcePaths, childID+".jsonl") {
		t.Errorf("source paths %v missing %s", detail.sourcePaths, childID+".jsonl")
	}

	// 子代理弹窗取数：只返回该 agent 的消息（marker + 子会话消息），与分页
	// 进度无关。走命令层入口而非 reader，连 resolve_session_path 一起覆盖。
	childMessages, err := GetSessionAgentMessagesInner(SourceCodex, rootID, childID)
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	if len(childMessages) != 3 {
		t.Fatalf("child messages = %d, want 3", len(childMessages))
	}
	hasChildAnswer := false
	hasMainAnswer := false
	for _, message := range childMessages {
		if message.SessionID == nil || *message.SessionID != childID {
			t.Fatalf("message session id = %v, want %s", message.SessionID, childID)
		}
		for _, block := range message.Blocks {
			if block.Text != nil && *block.Text == "Child answer" {
				hasChildAnswer = true
			}
			if block.Text != nil && *block.Text == "Main answer" {
				hasMainAnswer = true
			}
		}
	}
	if !hasChildAnswer {
		t.Error("Child answer missing")
	}
	if hasMainAnswer {
		t.Error("Main answer leaked into child messages")
	}
}

func TestCodexResumeSegmentsKeepOriginalSegmentAsRoot(t *testing.T) {
	root := t.TempDir()
	sessionID := "88888888-8888-4888-8888-888888888888"
	sessionsDir := filepath.Join(root, "sessions", "2026", "09", "02")
	originalPath := filepath.Join(sessionsDir, fmt.Sprintf("rollout-2026-09-02T16-00-15-%s.jsonl", sessionID))
	resumePath := filepath.Join(sessionsDir, fmt.Sprintf("rollout-2026-09-02T17-32-03-%s_99999999-9999-4999-8999-999999999999.jsonl", sessionID))

	writeJSONLFile(t, originalPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-09-02T08:00:15.000Z", "type": "session_meta", "payload": {"id": %q, "timestamp": "2026-09-02T08:00:15.000Z", "cwd": "/tmp/reins/workspace"}}`, sessionID),
		`{"timestamp": "2026-09-02T08:00:16.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Original task"}]}}`,
	))

	// resume 续跑段：与原始段共享 session id，靠 history_base 串链。
	writeJSONLFile(t, resumePath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-09-02T09:32:03.000Z", "type": "session_meta", "payload": {"id": %q, "timestamp": "2026-09-02T09:32:03.000Z", "cwd": "/tmp/reins/workspace", "history_base": {"thread_id": %q, "end_ordinal_exclusive": 834, "end_byte_offset": 3018401}}}`, sessionID, sessionID),
		`{"timestamp": "2026-09-02T09:32:04.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "继续"}]}}`,
	))

	reader := NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t))
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	// root 必须是原始段：标题取自原始段首条消息，而不是续跑段的"继续"。
	if entries[0].Path != originalPath {
		t.Errorf("entry path = %s, want original %s", entries[0].Path, originalPath)
	}
	summary := entries[0].Summary
	if summary.Title != "Original task" {
		t.Errorf("title = %q, want Original task", summary.Title)
	}
	// 续跑段与原始段共享 session id，是同一条线程而不是 subagent。
	if strings.Contains(summary.Title, "subagents") {
		t.Errorf("title = %q mentions subagents", summary.Title)
	}
}

func TestCodexTitleStripsMarkdownLinkSyntax(t *testing.T) {
	root := t.TempDir()
	sessionID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	transcriptPath := filepath.Join(root, "sessions", "2026", "09", "02",
		fmt.Sprintf("rollout-2026-09-02T16-00-15-%s.jsonl", sessionID))

	writeJSONLFile(t, transcriptPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-09-02T08:00:15.000Z", "type": "session_meta", "payload": {"id": %q, "timestamp": "2026-09-02T08:00:15.000Z", "cwd": "/tmp/reins/workspace"}}`, sessionID),
		`{"timestamp": "2026-09-02T08:00:16.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "[$mongo-slow-log-report]($HOME/Desktop/skills/mongo-slow-log-report/SKILL.md) 帮我看下这份skill有没有问题"}]}}`,
	))

	entries, err := NewReaderEngine[CodexSessionRow](CodexSpec{}, root, tempStore(t)).ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	// 链接语法必须剥掉只留 label，否则标题全是 [label](url)、看不到实际文本。
	if entries[0].Summary.Title != "$mongo-slow-log-report 帮我看下这份skill有没有问题" {
		t.Errorf("title = %q", entries[0].Summary.Title)
	}
}

// 会话级模型来自 session_meta 的 provider + turn_context 的逐轮模型；消息级
// 按轮归属，用户输入落在该轮的回答模型上。
func TestCodexModelsFollowProviderAndTurnContext(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")

	sessionID := "55555555-5555-4555-8555-555555555555"
	transcript := filepath.Join(sessions, fmt.Sprintf("rollout-2026-05-01T09-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcript, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-05-01T09:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace", "model_provider": "custom"}}`, sessionID),
		// 首轮：turn_context 声明模型，之后的用户输入与回答都归它。
		`{"timestamp": "2026-05-01T09:00:01.000Z", "type": "turn_context", "payload": {"cwd": "/tmp/reins/workspace", "model": "gpt-5.6-terra"}}`,
		`{"timestamp": "2026-05-01T09:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "First question"}]}}`,
		`{"timestamp": "2026-05-01T09:00:03.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "First answer"}]}}`,
		// 次轮换成另一个模型。
		`{"timestamp": "2026-05-01T09:00:04.000Z", "type": "turn_context", "payload": {"cwd": "/tmp/reins/workspace", "model": "deepseek-flash"}}`,
		`{"timestamp": "2026-05-01T09:00:05.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Second question"}]}}`,
		`{"timestamp": "2026-05-01T09:00:06.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Second answer"}]}}`,
	))

	reader := NewReaderEngine[CodexSessionRow](CodexSpec{}, dir, tempStore(t))
	summary, err := reader.ParseSummary(sessionID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	wantModels := []SessionModel{
		NewSessionModel("custom", "gpt-5.6-terra"),
		NewSessionModel("custom", "deepseek-flash"),
	}
	if len(summary.Models) != 2 || summary.Models[0] != wantModels[0] || summary.Models[1] != wantModels[1] {
		t.Fatalf("models = %+v, want %+v", summary.Models, wantModels)
	}

	detail := readDetailOf(t, reader, sessionID)
	var gotModels []SessionModel
	for _, message := range detail.messages {
		if message.Role != "user" && message.Role != "assistant" {
			continue
		}
		if message.Model == nil {
			t.Fatalf("message %s has no model（每轮都有声明）", message.ID)
		}
		gotModels = append(gotModels, *message.Model)
	}
	wantMessageModels := []SessionModel{
		NewSessionModel("custom", "gpt-5.6-terra"),
		NewSessionModel("custom", "gpt-5.6-terra"),
		NewSessionModel("custom", "deepseek-flash"),
		NewSessionModel("custom", "deepseek-flash"),
	}
	if len(gotModels) != 4 {
		t.Fatalf("message models = %+v, want 4", gotModels)
	}
	for i := range gotModels {
		if gotModels[i] != wantMessageModels[i] {
			t.Fatalf("message models = %+v, want %+v", gotModels, wantMessageModels)
		}
	}
}

// SQL 来源的会话级模型按 assistant 消息预聚合，消息级取该次调用的
// data.model；用户输入没有模型，落到这一轮回答的模型上。
func TestOpenCodeModelsComeFromAssistantMessageData(t *testing.T) {
	root := t.TempDir()
	sessionID := "ses_models_root"
	seedOpenCodeFamilyAt(t, root, sessionID, "ses_models_child")

	db := openSQLite(t, filepath.Join(root, "opencode.db"))
	defer db.Close()
	seedOpenCodeMessageRow(t, db, sessionID, "root-msg-2", 1_744_366_402_000, 2, "assistant",
		`{"time": {"created": 1744366402000}, "model": {"providerID": "platform-a", "id": "gpt-5.5"}, "content": [{"type": "text", "text": "Root answer"}]}`)
	seedOpenCodeMessageRow(t, db, "ses_models_child", "child-msg-2", 1_744_366_403_000, 2, "assistant",
		`{"time": {"created": 1744366403000}, "model": {"providerID": "platform-a", "id": "gpt-5.6-terra"}, "content": [{"type": "text", "text": "Child follow-up"}]}`)

	reader := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, root, tempStore(t))
	summary, err := reader.ParseSummary(sessionID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	// family 级：子会话自己的 assistant 模型也计入，root 的排在最前。
	want := []SessionModel{
		NewSessionModel("platform-a", "gpt-5.5"),
		NewSessionModel("platform-a", "gpt-5.6-terra"),
	}
	if len(summary.Models) != 2 || summary.Models[0] != want[0] || summary.Models[1] != want[1] {
		t.Fatalf("models = %+v, want %+v", summary.Models, want)
	}

	detail := readDetailOf(t, reader, sessionID)
	var question *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].ID == "root-msg-1" {
			question = &detail.messages[i]
			break
		}
	}
	if question == nil {
		t.Fatal("user message root-msg-1 missing")
	}
	if question.Model == nil || *question.Model != (SessionModel{Model: "gpt-5.5", Provider: "platform-a"}) {
		t.Fatalf("question model = %+v", question.Model)
	}
}

// —— 断言小件 ——

func assertAnyBlockTextContains(t *testing.T, messages []SessionMessage, needle string) {
	t.Helper()
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Text != nil && strings.Contains(*block.Text, needle) {
				return
			}
		}
	}
	t.Fatalf("no block text contains %q", needle)
}

func anyEventKind(events []SessionEvent, kind string) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func anyPathEndsWith(paths []string, suffix string) bool {
	for _, path := range paths {
		if path == suffix || strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

func firstBlockOfKind(messages []SessionMessage, kind string) (ContentBlock, bool) {
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Kind == kind {
				return block, true
			}
		}
	}
	return ContentBlock{}, false
}

func firstEventOfKind(events []SessionEvent, kind string) (SessionEvent, bool) {
	for _, event := range events {
		if event.Kind == kind {
			return event, true
		}
	}
	return SessionEvent{}, false
}
