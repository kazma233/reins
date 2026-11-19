package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// dsh.rs：dsh 读取器的行为分区——多帧 zstd、代际选择、事件类型映射、
// 标题回退、surface 折叠、family 归族与孤儿隐藏。

// dshEngineFixture：env-free 夹具根，engine 直构的 root 就是 sessions 目录。
func dshEngineFixture(t *testing.T, sessionsRoot string) *ReaderEngine[DshFamilyRow] {
	t.Helper()
	return NewReaderEngine[DshFamilyRow](DshSpec{}, sessionsRoot, tempStore(t))
}

func TestDshDecodesMultiframeZstdAndPicksHighestGeneration(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "--D-projects-demo--", "session-multi")
	// 旧代 v3 明文共存，读取必须取最高代 v4 的多帧 zstd
	writeJSONLFile(t, filepath.Join(directory, "session.v3.jsonl"), jsonlLines(t,
		dshSessionHeaderLine("session-multi", 1791355600000, 0),
	))
	writeJSONLRaw(t, filepath.Join(directory, "session.v4.jsonl.zstd"), string(dshZstdMultiframe))

	reader := dshEngineFixture(t, home)
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}

	summary, err := reader.ParseSummary("session-multi")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceApp != SourceDsh || summary.SourceSessionID != "session-multi" {
		t.Fatalf("summary identity = %s/%s", summary.SourceApp, summary.SourceSessionID)
	}
	// 多帧里的第二条 user 消息是标题来源；created=header.createdAt，updated=最后事件 time
	if summary.Title != "frame two request" {
		t.Errorf("title = %q", summary.Title)
	}
	if summary.CreatedAt == nil || *summary.CreatedAt != 1791355700000 {
		t.Errorf("created = %v, want 1791355700000", summary.CreatedAt)
	}
	if summary.UpdatedAt == nil || *summary.UpdatedAt != 1791355700003 {
		t.Errorf("updated = %v, want 1791355700003", summary.UpdatedAt)
	}
	// assistant/message 的 usage 字段 1:1 映射
	usage := summary.TokenUsage
	if usage == nil {
		t.Fatal("usage missing")
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 || usage.CacheReadTokens != 3 || usage.CacheWriteTokens != 5 {
		t.Errorf("usage = %+v, want (11, 7, 3, 5)", usage)
	}

	detail := readDetailOf(t, reader, "session-multi")
	// 三个帧的内容全部解出：帧1 只有 header+permission（无消息），帧2/帧3 各一条消息
	assertAnyBlockTextContains(t, detail.messages, "frame two request")
	var assistant *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].Role == "assistant" {
			assistant = &detail.messages[i]
		}
	}
	if assistant == nil {
		t.Fatal("assistant message from frame three missing")
	}
	hasThinking := false
	hasAnswer := false
	for _, block := range assistant.Blocks {
		if block.Kind == "thinking" && derefString(block.Text) == "think" {
			hasThinking = true
		}
		if block.Kind == "text" && derefString(block.Text) == "frame three answer" {
			hasAnswer = true
		}
	}
	if !hasThinking || !hasAnswer {
		t.Fatalf("assistant blocks = %+v", assistant.Blocks)
	}
	// permission/preset 走事件时间线
	permission, ok := firstEventOfKind(detail.events, "permission/preset")
	if !ok {
		t.Fatal("permission event missing")
	}
	// raw payload 给整条记录（含 seq/type 信封），不是只给 data
	payload, ok := permission.Payload.(map[string]any)
	if !ok {
		t.Fatalf("permission payload = %T", permission.Payload)
	}
	if kind, _ := jsonString(payload, "type"); kind != "permission/preset" {
		t.Errorf("payload type = %q", kind)
	}
	if _, hasSeq := getJSONObjectPath(payload, "seq"); !hasSeq {
		t.Error("payload seq missing")
	}
	if _, hasData := getJSONObjectPath(payload, "data"); !hasData {
		t.Error("payload data missing")
	}
}

func TestDshToleratesTruncatedZstdTail(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "--D-projects-demo--", "session-live")
	// 截 420 字节：帧1+帧2 完整，帧3 从中间截断（live 写入中的文件）
	writeJSONLRaw(t, filepath.Join(directory, "session.v4.jsonl.zstd"), string(dshZstdMultiframe[:420]))

	reader := dshEngineFixture(t, home)
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	detail := readDetailOf(t, reader, entries[0].SourceSessionID)
	assertAnyBlockTextContains(t, detail.messages, "frame two request")
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil && strings.Contains(*block.Text, "frame three") {
				t.Error("frame three content must be truncated away")
			}
		}
	}
}

// dshMappingFixture：事件类型全覆盖的映射夹具。
func dshMappingFixture(id string) []string {
	lines := []string{dshSessionHeaderLine(id, 1000, 0)}
	seq := int64(0)
	for _, kind := range []string{"permission/preset", "sandbox/mode", "approval/policy"} {
		lines = append(lines, dshEventLine(seq, 1001, kind, `{"value": "workspace-write"}`))
		seq++
	}
	lines = append(lines, dshWithSurfaceAppend(dshEventLine(seq, 1002, "system/message",
		`{"message": {"role": "system", "content": [{"type": "text", "text": "You are an AI agent powered by DeepSeek Harness."}]}}`)))
	seq++
	lines = append(lines, dshUserMessageLine(seq, 1003, "帮我分析这个项目", "user"))
	seq++
	lines = append(lines, dshUserMessageLine(seq, 1004, "AGENTS.md instructions for this workspace, keep it short.", "agent-instructions"))
	seq++
	lines = append(lines, dshUserMessageLine(seq, 1005, "Current runtime context. Snapshot of sandbox policy.", "runtime-context"))
	seq++
	// request/header 带全量工具定义，量最大且在忽略列表
	lines = append(lines, dshEventLine(seq, 1006, "request/header",
		`{"header": {"config": {"provider": "deepseek-account", "model": "deepseek-flash"}, "tools": [{"name": "pwsh", "parameters": {"type": "object"}}]}}`))
	seq++
	lines = append(lines, dshEventLine(seq, 1007, "request/context",
		`{"provider": "deepseek-account", "model": "deepseek-flash", "contextWindow": 1000000}`))
	seq++
	lines = append(lines, dshEventLine(seq, 1008, "tool/call",
		`{"callId": "call_1", "name": "pwsh", "arguments": "{\"command\": \"ls\"}"}`))
	seq++
	lines = append(lines, dshAssistantMessageLine(seq, 1009,
		`[{"type": "reasoning", "text": ""}, {"type": "reasoning", "text": "先想想目录结构"}, {"type": "text", "text": "开始分析"}, {"type": "tool-call", "id": "call_1", "name": "pwsh", "arguments": "{\"command\": \"ls\"}"}]`,
		`{"inputTokens": 100, "outputTokens": 10, "cacheReadTokens": 20, "cacheWriteTokens": 5, "totalTokens": 135}`))
	seq++
	lines = append(lines, dshToolResultLine(seq, 1010, "call_1", "file-a\nfile-b", false))
	seq++
	lines = append(lines, dshToolResultLine(seq, 1011, "call_2", "Error: boom", true))
	seq++
	// 纯噪音/重复：全部忽略
	for _, kind := range []string{"step/start", "step/end", "turn/start", "agent/inbox/spliced", "session/title-llm-request"} {
		lines = append(lines, dshEventLine(seq, 1012, kind, `{"turn": 1, "step": 1}`))
		seq++
	}
	lines = append(lines, dshEventLine(seq, 1013, "session-log-deepseek/delivery-accepted",
		fmt.Sprintf(`{"sessionId": %q, "throughSeq": %d}`, id, seq)))
	seq++
	lines = append(lines, dshEventLine(seq, 1014, "llm/retry-attempt", `{"attempt": 1}`))
	seq++
	lines = append(lines, dshEventLine(seq, 1015, "turn/end", `{"turn": 1, "reason": {"kind": "completed"}}`))
	seq++
	// 未知 type：ignorable 才跳过，否则降级为事件
	lines = append(lines, strings.TrimSuffix(dshEventLine(seq, 1016, "future/experimental", `{"detail": "skip me"}`), "}")+`, "ignorable": true}`)
	seq++
	lines = append(lines, dshEventLine(seq, 1017, "future/unknown", `{"detail": "show me"}`))
	seq++
	lines = append(lines, dshEventLine(seq, 1018, "session/title", `{"title": "Initial title", "source": {"kind": "fallback"}}`))
	seq++
	lines = append(lines, dshEventLine(seq, 1019, "session/title", `{"title": "Final title", "source": {"kind": "provider"}}`))
	seq++
	lines = append(lines, dshAssistantMessageLine(seq, 1020,
		`[{"type": "reasoning", "text": "再想想"}, {"type": "text", "text": "总结完成"}]`,
		`{"inputTokens": 50, "outputTokens": 5, "cacheReadTokens": 0, "cacheWriteTokens": 0, "totalTokens": 55}`))
	return lines
}

func TestDshMapsEventTypesToMessagesEventsAndUsage(t *testing.T) {
	home := t.TempDir()
	dshWriteSession(t, home, "session-map", 4, false, dshMappingFixture("session-map"))

	reader := dshEngineFixture(t, home)
	summary, err := reader.ParseSummary("session-map")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	// session/title latest-wins
	if summary.Title != "Final title" {
		t.Errorf("title = %q", summary.Title)
	}
	// usage 两轮累计，字段 1:1 无换算
	usage := summary.TokenUsage
	if usage == nil {
		t.Fatal("usage missing")
	}
	if usage.InputTokens != 150 || usage.OutputTokens != 15 || usage.CacheReadTokens != 20 || usage.CacheWriteTokens != 5 {
		t.Errorf("usage = %+v, want (150, 15, 20, 5)", usage)
	}

	detail := readDetailOf(t, reader, "session-map")

	// 人类 user 消息进消息时间线；合成注入不进
	var user *SessionMessage
	userCount := 0
	for i := range detail.messages {
		if detail.messages[i].Role == "user" {
			user = &detail.messages[i]
			userCount++
		}
	}
	if user == nil {
		t.Fatal("human user message missing")
	}
	if userCount != 1 {
		t.Fatalf("user messages = %d, want 1", userCount)
	}
	hasQuestion := false
	for _, block := range user.Blocks {
		if block.Text != nil && *block.Text == "帮我分析这个项目" {
			hasQuestion = true
		}
	}
	if !hasQuestion {
		t.Fatalf("user blocks = %+v", user.Blocks)
	}

	// assistant：空 reasoning 跳过、非空→thinking、text→text、tool-call→function_call
	var firstAssistant *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].Role == "assistant" {
			firstAssistant = &detail.messages[i]
			break
		}
	}
	if firstAssistant == nil {
		t.Fatal("assistant message missing")
	}
	thinkingCount := 0
	hasThought := false
	var call *ContentBlock
	for i := range firstAssistant.Blocks {
		block := &firstAssistant.Blocks[i]
		if block.Kind == "thinking" {
			thinkingCount++
			if derefString(block.Text) == "先想想目录结构" {
				hasThought = true
			}
		}
		if block.Kind == "function_call" {
			call = block
		}
	}
	if !hasThought || thinkingCount != 1 {
		t.Fatalf("thinking blocks = %+v", firstAssistant.Blocks)
	}
	if call == nil {
		t.Fatal("tool call block missing")
	}
	if derefString(call.ToolName) != "pwsh" || derefString(call.ToolCallID) != "call_1" || derefString(call.Text) != `{"command": "ls"}` {
		t.Errorf("call block = %+v", call)
	}
	callPayload, _ := call.Payload.(map[string]any)
	input, _ := getJSONObjectPath(callPayload, "input")
	if command, _ := jsonString(input, "command"); command != "ls" {
		t.Errorf("call input = %+v", input)
	}

	// tool/result：两条 role=tool 消息，错误标记透出
	var outputs []*SessionMessage
	for i := range detail.messages {
		if detail.messages[i].Role == "tool" {
			outputs = append(outputs, &detail.messages[i])
		}
	}
	if len(outputs) != 2 {
		t.Fatalf("tool outputs = %d, want 2", len(outputs))
	}
	var failed *ContentBlock
	for _, message := range outputs {
		for i := range message.Blocks {
			if derefString(message.Blocks[i].ToolCallID) == "call_2" {
				failed = &message.Blocks[i]
			}
		}
	}
	if failed == nil {
		t.Fatal("failed tool result missing")
	}
	if failed.IsError == nil || !*failed.IsError {
		t.Errorf("failed is_error = %v, want true", failed.IsError)
	}
	if derefString(failed.Text) != "Error: boom" {
		t.Errorf("failed text = %q", derefString(failed.Text))
	}
	failedPayload, _ := failed.Payload.(map[string]any)
	if callID, _ := jsonString(failedPayload, "toolCallId"); callID != "call_2" {
		t.Errorf("failed payload toolCallId = %q", callID)
	}

	// 合成注入/系统消息转事件，summary 取首个 text 块截断
	instructions, ok := firstEventOfKind(detail.events, "agent-instructions")
	if !ok || !strings.Contains(instructions.Summary, "AGENTS.md instructions") {
		t.Fatalf("instructions event = %+v", instructions)
	}
	if !anyEventKind(detail.events, "runtime-context") {
		t.Error("runtime-context event missing")
	}
	system, ok := firstEventOfKind(detail.events, "system/message")
	if !ok || !strings.Contains(system.Summary, "AI agent powered") {
		t.Fatalf("system event = %+v", system)
	}

	// 通用事件在时间线；忽略项与 ignorable 未知项不在任何时间线
	for _, expected := range []string{"request/context", "turn/end", "future/unknown", "permission/preset"} {
		if !anyEventKind(detail.events, expected) {
			t.Errorf("missing event %s", expected)
		}
	}
	for _, ignored := range []string{
		"request/header", "tool/call", "step/start", "step/end", "turn/start",
		"agent/inbox/spliced", "session/title-llm-request",
		"session-log-deepseek/delivery-accepted", "llm/retry-attempt", "future/experimental",
	} {
		if anyEventKind(detail.events, ignored) {
			t.Errorf("ignored type %s leaked into timeline", ignored)
		}
		for _, message := range detail.messages {
			for _, block := range message.Blocks {
				if block.Kind == ignored {
					t.Errorf("ignored type %s leaked into messages", ignored)
				}
			}
		}
	}
}

func TestDshTitleFallsBackToFirstUserMessageThenSessionID(t *testing.T) {
	home := t.TempDir()
	dshWriteSession(t, home, "session-titled", 4, false, []string{
		dshSessionHeaderLine("session-titled", 1000, 0),
		dshUserMessageLine(0, 1100, "看看这个项目的结构", "user"),
		dshAssistantMessageLine(1, 1200, `[{"type": "text", "text": "done"}]`,
			`{"inputTokens": 1, "outputTokens": 1, "cacheReadTokens": 0, "cacheWriteTokens": 0}`),
	})
	dshWriteSession(t, home, "session-empty", 4, false, []string{
		dshSessionHeaderLine("session-empty", 2000, 0),
	})

	reader := dshEngineFixture(t, home)
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	titled := ""
	empty := ""
	for _, entry := range entries {
		summary := entry.Summary
		switch summary.SourceSessionID {
		case "session-titled":
			titled = summary.Title
		case "session-empty":
			empty = summary.Title
			if summary.CreatedAt == nil || *summary.CreatedAt != 2000 {
				t.Errorf("empty created = %v, want 2000", summary.CreatedAt)
			}
		}
	}
	if titled != "看看这个项目的结构" {
		t.Errorf("titled = %q", titled)
	}
	if empty != "session-empty" {
		t.Errorf("empty = %q", empty)
	}
}

func TestDshSurfaceReplaceFoldsCoveredRange(t *testing.T) {
	home := t.TempDir()
	lines := []string{
		dshSessionHeaderLine("session-fold", 1000, 0),
		dshUserMessageLine(1, 1100, "原始问题", "user"),
		dshAssistantMessageLine(2, 1200, `[{"type": "text", "text": "原始回答"}]`,
			`{"inputTokens": 10, "outputTokens": 2, "cacheReadTokens": 0, "cacheWriteTokens": 0}`),
		dshToolResultLine(3, 1250, "call_1", "原始工具输出", false),
		dshSurfaceReplace(4, 1300, "user/message",
			`{"content": [{"type": "text", "text": "压缩后的摘要"}], "source": {"kind": "user"}, "role": "user", "id": "user-fold"}`,
			1, 3),
	}
	dshWriteSession(t, home, "session-fold", 4, false, lines)

	reader := dshEngineFixture(t, home)
	detail := readDetailOf(t, reader, "session-fold")
	var messageTexts []string
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil {
				messageTexts = append(messageTexts, *block.Text)
			}
		}
	}
	if !stringsContain(messageTexts, "压缩后的摘要") {
		t.Fatalf("texts = %v", messageTexts)
	}
	for _, folded := range []string{"原始问题", "原始回答", "原始工具输出"} {
		if stringsContain(messageTexts, folded) {
			t.Errorf("replaced node %s must be folded away", folded)
		}
	}
	// 折叠不影响 usage 统计（消耗是计费事实）
	summary, err := reader.ParseSummary("session-fold")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	usage := summary.TokenUsage
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 2 {
		t.Fatalf("usage = %+v, want (10, 2)", usage)
	}
}

func TestDshFamilyFoldsSubagentsAndHidesOrphans(t *testing.T) {
	home := t.TempDir()
	dshWriteSession(t, home, "session-root", 4, false, []string{
		dshSessionHeaderLine("session-root", 1000, 0),
		dshUserMessageLine(0, 1100, "主任务", "user"),
		dshEventLine(1, 1150, "subagent/catalog",
			`{"childId": "session-child", "childCreatedAt": 1200, "version": 1, "mode": "one-shot", "label": "Research task"}`),
		dshAssistantMessageLine(2, 1300, `[{"type": "text", "text": "主会话回答"}]`,
			`{"inputTokens": 100, "outputTokens": 20, "cacheReadTokens": 0, "cacheWriteTokens": 0}`),
	})
	dshWriteSession(t, home, "session-child", 4, false, []string{
		dshSessionHeaderLine("session-child", 1200, 1),
		dshUserMessageLine(0, 1250, "子任务请求", "user"),
		dshAssistantMessageLine(1, 1350, `[{"type": "text", "text": "子任务完成"}]`,
			`{"inputTokens": 30, "outputTokens": 8, "cacheReadTokens": 5, "cacheWriteTokens": 0}`),
	})
	// 孤儿子会话：无 catalog 声明，隐藏
	dshWriteSession(t, home, "session-orphan", 4, false, []string{
		dshSessionHeaderLine("session-orphan", 1500, 1),
		dshUserMessageLine(0, 1550, "孤儿消息", "user"),
	})

	reader := dshEngineFixture(t, home)
	entries, err := reader.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1（root family is the only entry）", len(entries))
	}

	summary, err := reader.ParseSummary("session-root")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != "session-root" {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}
	if !containsStr(summary.Title, "主任务 (+1 subagents)") {
		t.Errorf("title = %q", summary.Title)
	}
	// family 用量 = root + child 之和
	usage := summary.TokenUsage
	if usage == nil {
		t.Fatal("usage missing")
	}
	if usage.InputTokens != 130 || usage.OutputTokens != 28 || usage.CacheReadTokens != 5 || usage.CacheWriteTokens != 0 {
		t.Errorf("usage = %+v, want (130, 28, 5, 0)", usage)
	}

	overview, err := reader.ParseOverview("session-root")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if len(overview.Agents) != 2 {
		t.Fatalf("agents = %+v, want 2", overview.Agents)
	}
	hasChildAgent := false
	for _, agent := range overview.Agents {
		if agent.SessionID == "session-child" && containsStr(agent.Label, "Research task") {
			hasChildAgent = true
		}
	}
	if !hasChildAgent {
		t.Errorf("agents roster = %+v", overview.Agents)
	}
	// catalog 的 label 优先作为 marker 标题
	if overview.Summary.Title != summary.Title {
		t.Errorf("overview title = %q, summary title = %q", overview.Summary.Title, summary.Title)
	}

	detail := readDetailOf(t, reader, "session-root")
	var marker *SessionMessage
	for i := range detail.messages {
		if detail.messages[i].ID == "dsh-subagent-session-child" {
			marker = &detail.messages[i]
		}
	}
	if marker == nil {
		t.Fatal("subagent marker message missing")
	}
	markerPayloadOK := false
	for _, block := range marker.Blocks {
		if payload, ok := block.Payload.(map[string]any); ok {
			if kind, _ := jsonString(payload, "type"); kind == "subagent_started" {
				markerPayloadOK = true
			}
		}
	}
	if !markerPayloadOK {
		t.Error("marker payload type=subagent_started missing")
	}
	assertAnyBlockTextContains(t, detail.messages, "子任务完成")
	for _, message := range detail.messages {
		for _, block := range message.Blocks {
			if block.Text != nil && containsStr(*block.Text, "孤儿消息") {
				t.Error("orphan message leaked into family timeline")
			}
		}
	}

	// 子代理入口按 agent session id 取消息；孤儿不属于任何 family
	agentMessages, err := reader.ParseAgentMessages("session-root", "session-child")
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	foundChild := false
	for _, message := range agentMessages {
		for _, block := range message.Blocks {
			if block.Text != nil && containsStr(*block.Text, "子任务") {
				foundChild = true
			}
		}
	}
	if !foundChild {
		t.Error("child task text missing in agent messages")
	}
	if _, err := reader.ParseAgentMessages("session-root", "session-orphan"); err == nil {
		t.Error("orphan agent messages must error")
	}

	// 子会话按自身 id 直查仍归属父 family；孤儿不入索引，直查报错。
	childSummary, err := reader.ParseSummary("session-child")
	if err != nil {
		t.Fatalf("parse child: %v", err)
	}
	if childSummary.SourceSessionID != "session-root" {
		t.Errorf("child resolves to %s, want session-root", childSummary.SourceSessionID)
	}
	if _, err := reader.ParseSummary("session-orphan"); err == nil {
		t.Error("orphan must not resolve")
	}
}

func TestDshUsageHoursBucketedByAssistantEventTime(t *testing.T) {
	home := t.TempDir()
	morning := localHourMillis(2026, 4, 21, 10)
	afternoon := localHourMillis(2026, 4, 21, 14)
	expectedKey := func(ts int64) string {
		key := HourKey(ts)
		return *key
	}
	modelSelection := func(seq int64, time int64, model string) string {
		return dshEventLine(seq, time, "model/selection", fmt.Sprintf(`{"provider": "custom-gateway", "model": %q}`, model))
	}
	path := dshWriteSession(t, home, "session-hours", 4, false, []string{
		dshSessionHeaderLine("session-hours", 1000, 0),
		modelSelection(0, morning-10, "deepseek-flash"),
		dshAssistantMessageLine(1, morning, `[{"type": "text", "text": "morning"}]`,
			`{"inputTokens": 100, "outputTokens": 10, "cacheReadTokens": 20, "cacheWriteTokens": 0}`),
		// selection 变更后，后续消耗归属新模型。
		modelSelection(2, afternoon-10, "glm-5.3"),
		dshAssistantMessageLine(3, afternoon, `[{"type": "text", "text": "afternoon"}]`,
			`{"inputTokens": 50, "outputTokens": 5, "cacheReadTokens": 0, "cacheWriteTokens": 0}`),
	})

	buckets, err := DshUsageHours(path)
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	assertTotalView(t, buckets, [][2]any{
		{expectedKey(morning), usageOf(100, 10, 20, 0)},
		{expectedKey(afternoon), usageOf(50, 5, 0, 0)},
	})
	// 早上桶带模型切片，下午桶归属换掉的模型。
	assertModelBucket(t, buckets[expectedKey(morning)], map[string]SessionTokenUsage{
		"deepseek-flash": usageOf(100, 10, 20, 0),
	})
	assertModelBucket(t, buckets[expectedKey(afternoon)], map[string]SessionTokenUsage{
		"glm-5.3": usageOf(50, 5, 0, 0),
	})
}
