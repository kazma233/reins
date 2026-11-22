package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// zcode.rs：zcode 读取器的行为分区——family 聚合、overview 计数、
// semantics.kind 分类与 part 装配。

// zcodeEngineFixture：engine 直构的 root 就是 db 文件路径，不碰进程 env。
func zcodeEngineFixture(t *testing.T, dbDir string, rootID string, childID string) *ReaderEngine[ZcodeSessionRow] {
	t.Helper()
	dbPath := filepath.Join(dbDir, "db.sqlite")
	seedZcodeFamilyAt(t, dbPath, rootID, childID)
	return engineAt(t, ZcodeSpec{}, dbPath, tempStore(t))
}

func TestZcodeRootSessionAggregatesSubagentSessions(t *testing.T) {
	reader := zcodeEngineFixture(t, t.TempDir(), "sess_root", "sess_child")

	summary, err := reader.ParseSummary("sess_root")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != "sess_root" {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}
	if !containsStr(summary.Title, "Root task (+1 subagents)") {
		t.Errorf("title = %q", summary.Title)
	}
	if summary.Cwd == nil || *summary.Cwd != "/tmp/root" {
		t.Errorf("cwd = %v", summary.Cwd)
	}
	// turn_usage 按 family 求和：input 是全口径（含 cache_read），拆掉后为
	// (100+30)-(50+15)；output (20+5)+(8+2)；cache 直接求和
	usage := summary.TokenUsage
	if usage == nil {
		t.Fatal("token usage missing")
	}
	if usage.InputTokens != 65 || usage.OutputTokens != 35 || usage.CacheReadTokens != 65 || usage.CacheWriteTokens != 14 {
		t.Errorf("usage = %+v, want (65, 35, 65, 14)", usage)
	}

	detail := readDetailOf(t, reader, "sess_root")
	assertAnyBlockTextContains(t, detail.messages, "Sub-agent session: Child task")
	if !anyEventKind(detail.events, "subagent_started") {
		t.Error("subagent_started event missing")
	}
	if !anyPathEndsWith(detail.sourcePaths, ":sess_child") {
		t.Errorf("source paths %v missing :sess_child", detail.sourcePaths)
	}

	// 子代理入口按 agent session id 取子会话消息
	agentMessages, err := reader.ParseAgentMessages("sess_root", "sess_child")
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	found := false
	for _, message := range agentMessages {
		for _, block := range message.Blocks {
			if block.Text != nil && containsStr(*block.Text, "子任务完成") {
				found = true
			}
		}
	}
	if !found {
		t.Error("child answer missing in agent messages")
	}
}

func TestZcodeOverviewCountsMatchLoadedTimeline(t *testing.T) {
	reader := zcodeEngineFixture(t, t.TempDir(), "sess_root", "sess_child")
	overview, err := reader.ParseOverview("sess_root")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	detail := readDetailOf(t, reader, "sess_root")

	if overview.MessageCount == nil || *overview.MessageCount != len(detail.messages) {
		t.Errorf("message count = %v, want %d", overview.MessageCount, len(detail.messages))
	}
	if overview.EventCount == nil || *overview.EventCount != len(detail.events) {
		t.Errorf("event count = %v, want %d", overview.EventCount, len(detail.events))
	}
}

func TestZcodeClassifiesTimelineBySemanticsAndParsesParts(t *testing.T) {
	reader := zcodeEngineFixture(t, t.TempDir(), "sess_root", "sess_child")
	detail := readDetailOf(t, reader, "sess_root")

	// user_prompt：text 与 file 附件都可见
	var userMessage *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].ID == "msg_user" {
			userMessage = &detail.messages[i]
		}
	}
	if userMessage == nil {
		t.Fatal("user prompt message is a timeline message")
	}
	if userMessage.Role != "user" {
		t.Errorf("role = %s", userMessage.Role)
	}
	hasText := false
	hasFile := false
	for _, block := range userMessage.Blocks {
		if block.Kind == "text" && derefString(block.Text) == "帮我看下这个项目" {
			hasText = true
		}
		if block.Kind == "file" && derefString(block.Text) == "文件类型:image/png" {
			hasFile = true
		}
	}
	if !hasText || !hasFile {
		t.Fatalf("user blocks = %+v", userMessage.Blocks)
	}

	// assistant_response：reasoning→thinking、text、tool 拆两条 UI 块；
	// step-start/step-finish 不产生任何块
	var assistant *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].ID == "msg_assistant" {
			assistant = &detail.messages[i]
		}
	}
	if assistant == nil {
		t.Fatal("assistant response message is a timeline message")
	}
	if assistant.Role != "assistant" {
		t.Errorf("role = %s", assistant.Role)
	}
	hasThinking := false
	var toolInput *ContentBlock
	var toolOutput *ContentBlock
	for i := range assistant.Blocks {
		block := &assistant.Blocks[i]
		if block.Kind == "thinking" && derefString(block.Text) == "先查看目录结构" {
			hasThinking = true
		}
		if block.Kind == "function_call" {
			toolInput = block
		}
		if block.Kind == "function_call_output" {
			toolOutput = block
		}
	}
	if !hasThinking {
		t.Fatalf("thinking block missing: %+v", assistant.Blocks)
	}
	if toolInput == nil {
		t.Fatal("tool call block missing")
	}
	if derefString(toolInput.ToolName) != "Bash" || derefString(toolInput.ToolCallID) != "call_1" {
		t.Errorf("tool input = %+v", toolInput)
	}
	payload, _ := toolInput.Payload.(map[string]any)
	input, _ := getJSONObjectPath(payload, "input")
	if command, _ := jsonString(input, "command"); command != "ls" {
		t.Errorf("input command = %q", command)
	}
	if toolOutput == nil {
		t.Fatal("tool output block missing")
	}
	if derefString(toolOutput.Text) != "README.md\nsrc" {
		t.Errorf("output text = %q", derefString(toolOutput.Text))
	}
	outputPayload, _ := toolOutput.Payload.(map[string]any)
	if output, _ := jsonString(outputPayload, "output"); output != "README.md\nsrc" {
		t.Errorf("payload output = %q", output)
	}

	// todo_reminder / timeline_event 不进消息，转事件
	for _, message := range detail.messages {
		if message.ID == "msg_todo" || message.ID == "msg_timeline" {
			t.Errorf("message %s leaked into timeline", message.ID)
		}
	}
	todoEvent, hasTodo := firstEventOfID(detail.events, "msg_todo")
	if !hasTodo {
		t.Fatal("todo reminder becomes an event")
	}
	if !containsStr(todoEvent.Summary, "todo reminder") {
		t.Errorf("todo summary = %q", todoEvent.Summary)
	}
	// raw payload 给整行 + part 表内容：正文在 parts 里，data 是 message 行原文
	todoPayload, _ := todoEvent.Payload.(map[string]any)
	parts, hasParts := getJSONObjectPath(todoPayload, "parts")
	if !hasParts {
		t.Fatal("todo payload parts missing")
	}
	partsList := parts.([]any)
	foundAnalysis := false
	for _, part := range partsList {
		if text, ok := jsonString(part, "text"); ok && containsStr(text, "分析存储") {
			foundAnalysis = true
		}
	}
	if !foundAnalysis {
		t.Error("todo parts text missing")
	}
	if data, hasData := getJSONObjectPath(todoPayload, "data"); !hasData {
		t.Fatal("todo payload data missing")
	} else if _, hasRole := getJSONObjectPath(data, "role"); !hasRole {
		t.Error("todo payload data.role missing")
	}
	timelineEvent, hasTimeline := firstEventOfID(detail.events, "msg_timeline")
	if !hasTimeline {
		t.Fatal("timeline event message becomes an event")
	}
	if !containsStr(timelineEvent.Summary, "model_change") {
		t.Errorf("timeline summary = %q", timelineEvent.Summary)
	}
}

func containsStr(haystack string, needle string) bool {
	return strings.Contains(haystack, needle)
}

func firstEventOfID(events []SessionEvent, id string) (SessionEvent, bool) {
	for _, event := range events {
		if event.ID == id {
			return event, true
		}
	}
	return SessionEvent{}, false
}

// session_models（zcode.rs）：model_usage 按 (session, provider, model) 聚合，
// 计数列只用于 ORDER BY（主模型排最前、同次数按模型名稳定排序）不进入结果——
// database/sql 的 Scan 必须为查询的每一列提供目标（rusqlite 按索引取值则无此
// 约束），移植时漏掉第 4 个 Scan 目标曾让整条会话页加载失败。
func TestZcodeSessionModelsReadsAggregatedModels(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "db.sqlite")
	db := openSQLite(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
        CREATE TABLE model_usage (
            id TEXT PRIMARY KEY,
            session_id TEXT NOT NULL,
            provider_id TEXT NOT NULL,
            model_id TEXT NOT NULL,
            started_at INTEGER NOT NULL,
            status TEXT NOT NULL,
            input_tokens INTEGER NOT NULL DEFAULT 0,
            output_tokens INTEGER NOT NULL DEFAULT 0,
            reasoning_tokens INTEGER NOT NULL DEFAULT 0,
            cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
            cache_read_input_tokens INTEGER NOT NULL DEFAULT 0
        );
        INSERT INTO model_usage (id, session_id, provider_id, model_id, started_at, status) VALUES
            ('u1', 's1', 'zai', 'glm-5.3', 1, 'completed'),
            ('u2', 's1', 'zai', 'glm-5.3', 2, 'completed'),
            ('u3', 's1', 'zai', 'glm-5.3', 3, 'completed'),
            ('u4', 's1', 'zai', 'glm-air',  4, 'completed');`)

	models, err := zcodeSessionModels(db)
	if err != nil {
		t.Fatalf("zcodeSessionModels: %v", err)
	}
	got := models["s1"]
	if len(got) != 2 {
		t.Fatalf("models = %+v, want 2 distinct models", got)
	}
	// calls 降序：3 次调用的 glm-5.3 在 1 次的 glm-air 之前。
	if got[0].Model != "glm-5.3" || got[0].Provider != "zai" {
		t.Errorf("models[0] = %+v, want glm-5.3/zai", got[0])
	}
	if got[1].Model != "glm-air" {
		t.Errorf("models[1] = %+v, want glm-air", got[1])
	}
}
