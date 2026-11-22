package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"reins/internal/support"
)

// grokbuild.rs：grok build 读取器的行为分区——subagent 归族、缺失子会话、
// 嵌套与孤儿独立、冲突拒绝、消息装配与加密剥离、指纹刷新。

// 对已安装 Grok 会话库的显式只读验收（对齐 Rust #[ignore] 用例：默认跳过，
// 需要人工对本机真实会话库执行时去掉 Skip）。
func TestGrokbuildLocalStoreReadonlyAcceptance(t *testing.T) {
	t.Skip("Explicit read-only validation of the installed Grok session store")
	homeGuard(t, testsupportHomeDir(t))
	r := readerMust(t, SourceGrokBuild)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	messages := 0
	events := 0
	agents := 0
	subagentMessages := 0
	for _, entry := range entries {
		id := entry.SourceSessionID
		overview, err := r.ParseOverview(id)
		if err != nil {
			t.Fatalf("parse overview %s: %v", id, err)
		}
		if overview.Summary.SourceApp != SourceGrokBuild {
			t.Fatalf("overview source app = %s", overview.Summary.SourceApp)
		}
		loaded, err := r.ParseMessages(id)
		if err != nil {
			t.Fatalf("parse messages %s: %v", id, err)
		}
		messages += len(loaded)
		loadedEvents, err := r.ParseEvents(id)
		if err != nil {
			t.Fatalf("parse events %s: %v", id, err)
		}
		events += len(loadedEvents)
		agents += len(overview.Agents)
		for _, agent := range overview.Agents {
			if !agent.IsRoot {
				loadedSub, err := r.ParseAgentMessages(id, agent.SessionID)
				if err != nil {
					t.Fatalf("parse agent messages %s/%s: %v", id, agent.SessionID, err)
				}
				subagentMessages += len(loadedSub)
			}
		}
	}
	t.Logf("Grok read-only acceptance: sessions=%d, messages=%d, events=%d, agents=%d, subagent_messages=%d",
		len(entries), messages, events, agents, subagentMessages)
}

func TestGrokbuildFoldsConfirmedSubagentsIntoParentFamily(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	parentPath := grokFixture(t, home, "parent")
	grokHistory(t, parentPath)
	grokSubagentFixture(t, home, "parent", "child-a", "at1.a", "Child A answer")
	grokSubagentFixture(t, home, "parent", "child-b", "at1.b", "Child B answer")

	r := readerMust(t, SourceGrokBuild)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	summary := entries[0].Summary
	if summary.SourceSessionID != "parent" {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}
	if !strings.HasSuffix(summary.Title, "(+2 subagents)") {
		t.Errorf("title = %q", summary.Title)
	}
	canonicalParent, err := supportCanonical(parentPath)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if entries[0].Path != canonicalParent {
		t.Errorf("entry path = %s, want %s", entries[0].Path, canonicalParent)
	}

	overview, err := r.ParseOverview("parent")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if len(overview.Agents) != 3 {
		t.Fatalf("agents = %+v, want 3", overview.Agents)
	}
	if !overview.Agents[0].IsRoot || overview.Agents[0].Label != "主 Agent" {
		t.Errorf("root agent = %+v", overview.Agents[0])
	}
	labels := make([]string, 0)
	for _, agent := range overview.Agents {
		if !agent.IsRoot {
			labels = append(labels, agent.Label)
		}
	}
	if !stringsContain(labels, "task child-a(子)") || !stringsContain(labels, "task child-b(子)") {
		t.Fatalf("child labels = %v", labels)
	}

	// 主时间线只有父会话消息 + 子代理入口，不扫描子会话正文。
	messages, err := r.ParseMessages("parent")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	if len(messages) != 6+2 {
		t.Fatalf("messages = %d, want 8", len(messages))
	}
	var markerIDs []string
	for _, message := range messages {
		if id, ok := grokMarkerSessionID(message); ok {
			markerIDs = append(markerIDs, id)
		}
	}
	if len(markerIDs) != 2 || markerIDs[0] != "child-a" || markerIDs[1] != "child-b" {
		t.Fatalf("marker ids = %v, want [child-a child-b]", markerIDs)
	}

	// 子代理弹窗数据：marker + 子会话正文，消息按子会话命名空间隔离。
	child, err := r.ParseAgentMessages("parent", "child-a")
	if err != nil {
		t.Fatalf("agent messages: %v", err)
	}
	if len(child) != 3 {
		t.Fatalf("child messages = %d, want 3", len(child))
	}
	if id, ok := grokMarkerSessionID(child[0]); !ok || id != "child-a" {
		t.Fatalf("child[0] marker = %q %v", id, ok)
	}
	for _, message := range child {
		if message.SessionID == nil || *message.SessionID != "child-a" {
			t.Fatalf("child message session id = %v, want child-a", message.SessionID)
		}
	}
	if derefString(child[2].Blocks[0].Text) != "Child A answer" {
		t.Errorf("child[2] text = %q", derefString(child[2].Blocks[0].Text))
	}
	if _, err := r.ParseAgentMessages("parent", "not-a-child"); err == nil {
		t.Error("unknown agent must error")
	}

	// 折叠后的子会话按自身 id 直查仍归属同一个 family（root 摘要）。
	childSummary, err := r.ParseSummary("child-a")
	if err != nil {
		t.Fatalf("parse child summary: %v", err)
	}
	if childSummary.SourceSessionID != "parent" {
		t.Errorf("child resolves to %s, want parent", childSummary.SourceSessionID)
	}

	// 新的 meta 与子会话在刷新后立即可见。
	grokSubagentFixture(t, home, "parent", "child-c", "at1.c", "Child C answer")
	entries, err = r.ListEntries()
	if err != nil {
		t.Fatalf("list entries after add: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries after add = %d, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Summary.Title, "(+3 subagents)") {
		t.Errorf("title after add = %q", entries[0].Summary.Title)
	}
}

func TestGrokbuildMissingChildKeepsParentReadable(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	parentPath := grokFixture(t, home, "parent")
	grokHistory(t, parentPath)
	grokSubagentMetaFixture(t, home, "parent", "missing", "at1.m", "task missing")

	r := readerMust(t, SourceGrokBuild)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Summary.Title, "(+1 subagents)") {
		t.Errorf("title = %q", entries[0].Summary.Title)
	}
	overview, err := r.ParseOverview("parent")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if len(overview.Agents) != 2 {
		t.Fatalf("agents = %+v, want 2", overview.Agents)
	}
	if overview.Agents[1].SessionID != "missing" {
		t.Errorf("placeholder agent = %+v", overview.Agents[1])
	}

	// 父会话照常可读，缺失子会话保留入口但打开时明确报错。
	messages, err := r.ParseMessages("parent")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	hasMarker := false
	for _, message := range messages {
		if id, ok := grokMarkerSessionID(message); ok && id == "missing" {
			hasMarker = true
		}
	}
	if !hasMarker {
		t.Error("missing child marker missing")
	}
	_, err = r.ParseAgentMessages("parent", "missing")
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("agent messages error = %v, want contains not readable", err)
	}
}

func TestGrokbuildOrphanAndNestedChildrenStayStandalone(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	grokFixture(t, home, "root")
	grokChildSessionFixture(t, home, "nested-child", "at1.n", "Nested child answer")
	grokSubagentMetaFixture(t, home, "root", "nested-child", "at1.n", "task nested-child")
	// 嵌套子代理（父自身也是子代理）尚未验证：保留为独立条目。
	grokChildSessionFixture(t, home, "grand-child", "at1.g", "Grand child answer")
	grokSubagentMetaFixture(t, home, "nested-child", "grand-child", "at1.g", "task grand-child")
	// 没有任何 meta 的普通会话不应被折叠。
	grokChildSessionFixture(t, home, "orphan", "at1.o", "Orphan answer")

	r := readerMust(t, SourceGrokBuild)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	foundRoot := false
	for _, entry := range entries {
		id := entry.Summary.SourceSessionID
		switch id {
		case "root":
			foundRoot = true
			if !strings.HasSuffix(entry.Summary.Title, "(+1 subagents)") {
				t.Errorf("root title = %q", entry.Summary.Title)
			}
		case "grand-child", "orphan":
			if strings.Contains(entry.Summary.Title, "subagents") {
				t.Errorf("%s title = %q mentions subagents", id, entry.Summary.Title)
			}
		}
	}
	if !foundRoot {
		t.Fatal("root entry missing")
	}

	overview, err := r.ParseOverview("root")
	if err != nil {
		t.Fatalf("parse overview: %v", err)
	}
	if len(overview.Agents) != 2 {
		t.Fatalf("agents = %+v, want 2", overview.Agents)
	}
	if _, err := r.ParseAgentMessages("root", "nested-child"); err != nil {
		t.Errorf("nested-child agent messages: %v", err)
	}
	// 未折叠的嵌套/孤立子会话不属于父会话的成员。
	if _, err := r.ParseAgentMessages("root", "grand-child"); err == nil {
		t.Error("grand-child must not belong to root")
	}
	if _, err := r.ParseAgentMessages("root", "orphan"); err == nil {
		t.Error("orphan must not belong to root")
	}
}

func TestGrokbuildRejectsDuplicateChildOwnership(t *testing.T) {
	// 同一个子会话被两个父会话声明。
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	grokFixture(t, home, "parent-a")
	grokFixture(t, home, "parent-b")
	grokChildSessionFixture(t, home, "shared", "at1.s", "Shared answer")
	grokSubagentMetaFixture(t, home, "parent-a", "shared", "at1.s", "task a")
	grokSubagentMetaFixture(t, home, "parent-b", "shared", "at1.s", "task b")
	_, err := readerMust(t, SourceGrokBuild).ListEntries()
	if err == nil || !strings.Contains(err.Error(), "Conflicting") {
		t.Fatalf("error = %v, want contains Conflicting", err)
	}
}

func TestGrokbuildRejectsAttemptMismatch(t *testing.T) {
	// 子会话的 attempt 与 meta 不一致。
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	grokFixture(t, home, "parent")
	grokChildSessionFixture(t, home, "child", "at1.real", "Child answer")
	grokSubagentMetaFixture(t, home, "parent", "child", "at1.other", "task child")
	_, err := readerMust(t, SourceGrokBuild).ListEntries()
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v, want contains does not match", err)
	}
}

func TestGrokbuildRejectsSelfClaimedSubagent(t *testing.T) {
	// 自己声明为自己的子代理。
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	grokFixture(t, home, "self")
	grokSubagentMetaFixture(t, home, "self", "self", "at1.self", "task self")
	if _, err := readerMust(t, SourceGrokBuild).ListEntries(); err == nil {
		t.Error("self claimed subagent must error")
	}
}

func TestGrokbuildTranscriptPathHasNoVerbatimPrefix(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	grokFixture(t, home, "empty")
	summary, err := readerMust(t, SourceGrokBuild).ParseSummary("empty")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	// 转录文件路径直接展示给用户，不能暴露规范化工具链的前缀痕迹。
	if strings.Contains(summary.TranscriptPath, "\\\\?\\") {
		t.Errorf("transcript path = %s", summary.TranscriptPath)
	}
}

func TestGrokbuildSummaryOnlyAndFormatValidation(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	path := grokFixture(t, home, "empty")
	r := readerMust(t, SourceGrokBuild)
	detail := readDetailOf(t, r, "empty")
	if len(detail.messages) != 0 || len(detail.events) != 0 {
		t.Fatalf("summary-only session has messages/events: %+v", detail)
	}
	if detail.summary.Title != "Synthetic title" {
		t.Errorf("title = %q", detail.summary.Title)
	}
	if detail.summary.Cwd == nil || *detail.summary.Cwd != "/synthetic/project's folder" {
		t.Errorf("cwd = %v", detail.summary.Cwd)
	}
	canonical, err := supportCanonical(path)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	summary, err := r.ParseSummary("empty")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.TranscriptPath != canonical {
		t.Errorf("transcript path = %s, want %s", summary.TranscriptPath, canonical)
	}
	if _, err := r.ParseSummary("../../etc/passwd"); err == nil {
		t.Error("path traversal must error")
	}

	// generated_title 空串 → 回退 session_summary；再空 → 回退 id。
	writeJSONLRaw(t, path, `{"info": {"id": "empty", "cwd": "/synthetic/project's folder"}, "chat_format_version": 1, "session_summary": "Synthetic summary", "generated_title": "", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "num_messages": 0}`)
	summary, err = r.ParseSummary("empty")
	if err != nil {
		t.Fatalf("parse after title clear: %v", err)
	}
	if summary.Title != "Synthetic summary" {
		t.Errorf("title = %q, want Synthetic summary", summary.Title)
	}
	writeJSONLRaw(t, path, `{"info": {"id": "empty", "cwd": "/synthetic/project's folder"}, "chat_format_version": 1, "session_summary": "", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "num_messages": 0}`)
	summary, err = r.ParseSummary("empty")
	if err != nil {
		t.Fatalf("parse after summary clear: %v", err)
	}
	if summary.Title != "empty" {
		t.Errorf("title = %q, want empty", summary.Title)
	}
	writeJSONLRaw(t, path, `{"info": {"id": "empty", "cwd": "/synthetic/project's folder"}, "chat_format_version": 2, "session_summary": "", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "num_messages": 0}`)
	_, err = r.ParseEvents("empty")
	if err == nil || !strings.Contains(err.Error(), "chat_format_version") {
		t.Fatalf("events error = %v, want contains chat_format_version", err)
	}
}

func TestGrokbuildNormalizesMessagesWithoutStreamDuplicatesOrEncryptedContent(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	path := grokFixture(t, home, "messages")
	grokHistory(t, path)
	r := readerMust(t, SourceGrokBuild)
	detail := readDetailOf(t, r, "messages")
	if len(detail.messages) != 6 {
		t.Fatalf("messages = %d, want 6", len(detail.messages))
	}
	if detail.messages[1].Blocks[1].Kind != "image" {
		t.Errorf("messages[1].blocks[1] = %s", detail.messages[1].Blocks[1].Kind)
	}
	// 图片块正文是引用：前端据此渲染，而不是回退成一行 JSON
	if derefString(detail.messages[1].Blocks[1].Text) != "https://example.invalid/image.png" {
		t.Errorf("image text = %q", derefString(detail.messages[1].Blocks[1].Text))
	}
	if derefString(detail.messages[2].Blocks[0].Text) != "Synthetic thought" {
		t.Errorf("thought text = %q", derefString(detail.messages[2].Blocks[0].Text))
	}
	toolPayload, _ := detail.messages[3].Blocks[1].Payload.(map[string]any)
	input, _ := getJSONObjectPath(toolPayload, "input")
	if command, _ := jsonString(input, "path"); command != "fixture.txt" {
		t.Errorf("tool input path = %q", command)
	}
	if detail.messages[4].Blocks[0].IsError == nil || *detail.messages[4].Blocks[0].IsError {
		t.Errorf("ok result is_error = %v", detail.messages[4].Blocks[0].IsError)
	}
	if detail.messages[5].Blocks[0].IsError == nil || !*detail.messages[5].Blocks[0].IsError {
		t.Errorf("bad result is_error = %v", detail.messages[5].Blocks[0].IsError)
	}
	serialized, _ := jsonMarshal(detail.messages)
	if strings.Contains(string(serialized), "NEVER_EXPOSE") ||
		strings.Contains(string(serialized), "encrypted_content") ||
		strings.Contains(string(serialized), "Do not display") {
		t.Error("encrypted/synthetic-reminder content leaked")
	}
	if countOccurrences(string(serialized), "Synthetic answer") != 1 {
		t.Errorf("Synthetic answer occurrences = %d, want 1", countOccurrences(string(serialized), "Synthetic answer"))
	}
	messages, err := r.ParseMessages("messages")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	if len(messages) != 6 {
		t.Errorf("parse messages = %d, want 6", len(messages))
	}
	events, err := r.ParseEvents("messages")
	if err != nil {
		t.Fatalf("parse events: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("events = %d, want 3", len(events))
	}
}

func TestGrokbuildTruncatedToolArgumentsStayReadable(t *testing.T) {
	// 模型输出中断会落盘截断的 arguments，会话必须照常可读并保留原文。
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	path := grokFixture(t, home, "truncated")
	writeJSONLFile(t, filepath.Join(filepath.Dir(path), "chat_history.jsonl"), jsonlLines(t,
		`{"type":"user","content":[{"type":"text","text":"Synthetic request"}]}`,
		`{"type":"assistant","content":"","tool_calls":[{"id":"trunc","name":"use_tool","arguments":"{\"tool_name\": \"create_comment\", \"description\": \"**问题原因"}]}`,
		`{"type":"tool_result","tool_call_id":"trunc","content":"Failed to parse arguments for tool `+"`use_tool`"+`"}`,
	))
	detail := readDetailOf(t, readerMust(t, SourceGrokBuild), "truncated")
	tool := &detail.messages[1].Blocks[0]
	if tool.Kind != "tool_use" {
		t.Errorf("kind = %s", tool.Kind)
	}
	if derefString(tool.ToolName) != "use_tool" {
		t.Errorf("tool name = %q", derefString(tool.ToolName))
	}
	payload, _ := tool.Payload.(map[string]any)
	input, _ := getJSONObjectPath(payload, "input")
	if raw, _ := jsonString(input, "raw"); raw != `{"tool_name": "create_comment", "description": "**问题原因` {
		t.Errorf("raw arguments = %q", raw)
	}
}

func TestGrokbuildListingReadsOnlySummaryAndRefreshesIndex(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	path := grokFixture(t, home, "listed")
	writeJSONLRaw(t, filepath.Join(filepath.Dir(path), "chat_history.jsonl"), "{invalid")
	writeJSONLRaw(t, filepath.Join(filepath.Dir(path), "events.jsonl"), "{invalid")
	writeJSONLRaw(t, filepath.Join(filepath.Dir(path), "updates.jsonl"), "{invalid")
	state := NewSessionIndexState()
	page, err := ListSessionsInner(state, SourceSelection{App: SourceGrokBuild}, 0, 20, "", false, true)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if page.TotalCount != 1 || page.Sessions[0].Title != "Synthetic title" {
		t.Fatalf("page = %+v", page)
	}
	writeJSONLRaw(t, path, `{"info": {"id": "listed", "cwd": "/synthetic/project's folder"}, "chat_format_version": 1, "session_summary": "Synthetic summary", "generated_title": "Updated title", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "num_messages": 0}`)
	grokFixture(t, home, "second")
	page, err = ListSessionsInner(state, SourceSelection{All: true}, 0, 20, "", false, true)
	if err != nil {
		t.Fatalf("list sessions all: %v", err)
	}
	if page.TotalCount != 2 {
		t.Fatalf("total = %d, want 2", page.TotalCount)
	}
	hasUpdated := false
	for _, session := range page.Sessions {
		if session.Title == "Updated title" {
			hasUpdated = true
		}
	}
	if !hasUpdated {
		t.Fatalf("sessions = %+v", page.Sessions)
	}
	sources, err := DetectSourcesInner(state)
	if err != nil {
		t.Fatalf("detect sources: %v", err)
	}
	found := false
	for _, source := range sources {
		if source.App == SourceGrokBuild && source.SessionCount == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("grok source status missing: %+v", sources)
	}
}

func TestGrokbuildRejectsUnknownIDsAndSymlinkedAuxFiles(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	path := grokFixture(t, home, "safe")
	outside := filepath.Join(home, "summary.json")
	contents, err := osReadFile(path)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	writeJSONLRaw(t, outside, string(contents))
	// 会话根目录之外的伪造入口不进索引，id 直查无法触达。
	if _, err := readerMust(t, SourceGrokBuild).ParseMessages("unknown"); err == nil {
		t.Error("unknown id must error")
	}
	if _, err := GetSessionMessagesInner(SourceGrokBuild, "other"); err == nil {
		t.Error("unknown id via command entry must error")
	}
	// chat_history.jsonl 指向根外的符号链接被兄弟文件防线拦截。
	symlinkFile(t, outside, filepath.Join(filepath.Dir(path), "chat_history.jsonl"))
	if _, err := readerMust(t, SourceGrokBuild).ParseMessages("safe"); err == nil {
		t.Error("symlinked aux file must error")
	}
}

func TestGrokbuildCustomHomeIsUsedForListingAndResolution(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	grokFixture(t, home, "custom")
	custom := filepath.Join(home, "custom-grok")
	if err := osRename(filepath.Join(home, ".grok"), custom); err != nil {
		t.Fatalf("rename: %v", err)
	}
	setRedirect(t, "GROK_HOME", custom)
	r := readerMust(t, SourceGrokBuild)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	expected, err := supportCanonical(filepath.Join(custom, "sessions", "not-a-cwd", "custom", "summary.json"))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	summary, err := r.ParseSummary("custom")
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.TranscriptPath != expected {
		t.Errorf("transcript path = %s, want %s", summary.TranscriptPath, expected)
	}
}

func TestGrokbuildLargeEventsPageAndAuxiliaryChangesAreFresh(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	path := grokFixture(t, home, "large")
	r := readerMust(t, SourceGrokBuild)
	var lines []string
	for index := 0; index < 2_000; index++ {
		lines = append(lines, fmt.Sprintf(`{"type":"phase_changed","ts":"2026-01-01T00:00:00Z","index":%d}`, index))
	}
	writeJSONLFile(t, filepath.Join(filepath.Dir(path), "events.jsonl"), lines)
	events, err := r.ParseEvents("large")
	if err != nil {
		t.Fatalf("parse events: %v", err)
	}
	if len(events) != 2_000 {
		t.Fatalf("events = %d, want 2000", len(events))
	}
	if events[0].ID != "grok-event-0" {
		t.Errorf("event id = %s", events[0].ID)
	}
	writeJSONLFile(t, filepath.Join(filepath.Dir(path), "events.jsonl"), jsonlLines(t,
		`{"type":"future_event", "outcome":"completed", "encrypted_content":"NEVER_EXPOSE_SYNTHETIC"}`,
	))
	events, err = r.ParseEvents("large")
	if err != nil {
		t.Fatalf("parse events after change: %v", err)
	}
	if len(events) != 1 || events[0].Kind != "future_event" {
		t.Fatalf("events = %+v", events)
	}
	// raw 页给整条记录：白名单外的字段也要在
	payload, ok := events[0].Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload = %T", events[0].Payload)
	}
	if outcome, _ := jsonString(payload, "outcome"); outcome != "completed" {
		t.Errorf("outcome = %q", outcome)
	}
	// 加密字段是唯一例外，不下发
	serialized, _ := jsonMarshal(events)
	if strings.Contains(string(serialized), "encrypted_content") {
		t.Error("encrypted_content leaked")
	}
	writeJSONLFile(t, filepath.Join(filepath.Dir(path), "chat_history.jsonl"), jsonlLines(t,
		`{"type":"assistant", "content":"First"}`,
	))
	messages, err := r.ParseMessages("large")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	writeJSONLFile(t, filepath.Join(filepath.Dir(path), "chat_history.jsonl"), jsonlLines(t,
		`{"type":"assistant", "content":"Second"}`,
	))
	messages, err = r.ParseMessages("large")
	if err != nil {
		t.Fatalf("parse messages after change: %v", err)
	}
	if derefString(messages[0].Blocks[0].Text) != "Second" {
		t.Errorf("message text = %q", derefString(messages[0].Blocks[0].Text))
	}
}

// 指纹门控：外部（grok CLI 或手工）写入新会话后，下一次列表必须出现它
// ——索引缓存靠文件集合指纹失配失效。
func TestGrokbuildIndexRebuildsWhenNewSessionAppears(t *testing.T) {
	home := testsupportHomeDir(t)
	homeGuard(t, home)
	first := grokFixture(t, home, "first")
	grokHistory(t, first)

	r := readerMust(t, SourceGrokBuild)
	entries, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}

	// 外部新增：summary + history 直接落盘，不经过 reins。
	second := grokFixture(t, home, "second")
	grokHistory(t, second)

	entries, err = r.ListEntries()
	if err != nil {
		t.Fatalf("list entries after add: %v", err)
	}
	var ids []string
	for _, entry := range entries {
		ids = append(ids, entry.SourceSessionID)
	}
	if !stringsContain(ids, "second") {
		t.Fatalf("ids = %v, want contains second", ids)
	}
}

// —— 小件 ——

func countOccurrences(haystack string, needle string) int {
	count := 0
	for i := 0; i+len(needle) <= len(haystack); {
		index := indexOfStr(haystack[i:], needle)
		if index < 0 {
			break
		}
		count++
		i += index + len(needle)
	}
	return count
}

func indexOfStr(haystack string, needle string) int {
	return strings.Index(haystack, needle)
}

func supportCanonical(path string) (string, error) {
	return support.Canonicalize(path)
}

func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func osRename(oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}

// symlinkFile：unix 的符号链接夹具。
// symlinkFile：创建符号链接夹具。Windows 未开开发者模式且非管理员时
// ERROR_PRIVILEGE_NOT_HELD（0x522=1314，数值字面量以避开平台专属常量）
// ——该前提无法在夹具内满足，跳过并说明原因，而不是让套件在本机常红。
func symlinkFile(t *testing.T, target string, link string) {
	t.Helper()
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skipf("需要开发者模式或管理员权限才能创建符号链接: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}
}
