package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reins/internal/support"
	"reins/internal/testsupport"
)

// 2b 测试夹具（对齐 Rust 版 tests/mod.rs 与各来源测试文件的 helper）：
// 手写 jsonl/sqlite 夹具 + reader 级引擎直构 + 聚合视图读取。

// jsonlLines：夹具行按 JSON 字面量书写（可读性优先）；写入前逐行校验合法。
func jsonlLines(t *testing.T, lines ...string) []string {
	t.Helper()
	for i, line := range lines {
		if _, err := decodeJSONBytes([]byte(line)); err != nil {
			t.Fatalf("fixture line %d is not valid JSON: %v\n%s", i+1, err, line)
		}
	}
	return lines
}

// writeJSONLFile：把夹具行写入 path（自动建父目录）。
func writeJSONLFile(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	content := ""
	for _, line := range lines {
		content += line + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeJSONLRaw：不经校验的字节夹具。
func writeJSONLRaw(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// testsupportHomeDir：进程环境守卫用的临时 HOME（对齐 Rust env::temp_dir()/
// reins-test-<uuid>，由 t.TempDir 清理机制兜底）。
func testsupportHomeDir(t *testing.T) string {
	t.Helper()
	return testsupport.TestDir(t, "test")
}

// tempStore：engine 直构的持久缓存目录，每个测试独立，避免跨测试串扰。
func tempStore(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "reins-store")
}

// canonicalTempHome：macOS 的 TempDir 返回 /var 别名，canonicalize 后是
// /private/var——grokbuild 兄弟文件防线按 resolved != candidate 拦截符号
// 链接，HOME 必须先归一到 canonical 形态才与生产一致（真实 HOME 无此别名）。
func canonicalTempHome(t *testing.T) string {
	t.Helper()
	base, err := support.Canonicalize(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temp dir: %v", err)
	}
	return filepath.Join(base, "reins-home")
}

// homeGuard：进程级 home 守卫（对齐 Rust TestEnvGuard::set_home）；返回值
// 供 SetPiSessionDir 等追加设置。收尾关闭全局读取器槽位与 usage-day 进程级
// 连接——Windows 的 t.TempDir 清理删不掉仍被 SQLite 打开的库文件，连接必须
// 先于目录清理释放（Unix 允许删除打开中的文件，这层关闭在那里是防御性的）。
func homeGuard(t *testing.T, home string) *testsupport.EnvGuard {
	t.Helper()
	guard := testsupport.SetHome(t, home)
	t.Cleanup(closeReaderSlots)
	return guard
}

// closeReaderSlots：槽位清空后下一个测试按新根重建，与换根重建同一条路径。
func closeReaderSlots() {
	CodexReader.closeSlotForCleanup()
	ClaudeCodeReader.closeSlotForCleanup()
	OpenCodeReader.closeSlotForCleanup()
	ZcodeReader.closeSlotForCleanup()
	DshReader.closeSlotForCleanup()
	PiReaderInstance.closeSlotForCleanup()
	usageDayState.Lock()
	if usageDayState.db != nil {
		_ = usageDayState.db.Close()
		usageDayState.db = nil
		usageDayState.path = ""
	}
	usageDayState.Unlock()
}

// closeSlotForCleanup：仅测试夹具调用（生产路径经换根重建自行释放）。
func (r *FamilyReader[R]) closeSlotForCleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slotEngine != nil {
		r.slotEngine.Close()
		r.slotEngine = nil
		r.slotRoot = ""
	}
}

func (b *piReaderInstance) closeSlotForCleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.slot != nil {
		b.slot.reader.Close()
		b.slot = nil
	}
}

// engineAt：引擎直构夹具（root 与持久缓存目录显式注入，脱离进程 env），
// 收尾注册 Close——理由同 homeGuard 的收尾关闭。
func engineAt[R FamilyRow](t *testing.T, spec FamilySpec[R], root string, storeDir string) *ReaderEngine[R] {
	t.Helper()
	engine := NewReaderEngine[R](spec, root, storeDir)
	t.Cleanup(engine.Close)
	return engine
}

// setRedirect：设置来源重定向变量并在测试结束时还原。
func setRedirect(t *testing.T, name string, value string) {
	t.Helper()
	t.Setenv(name, value)
}

// —— 聚合视图（对齐 tests/mod.rs 的 DetailView / read_detail）——

type detailView struct {
	summary     SessionSummary
	sourcePaths []string
	messages    []SessionMessage
	events      []SessionEvent
}

func readDetailOf(t *testing.T, reader Reader, sourceSessionID string) detailView {
	t.Helper()
	overview, err := reader.ParseOverview(sourceSessionID)
	if err != nil {
		t.Fatalf("parse overview %s: %v", sourceSessionID, err)
	}
	messages, err := reader.ParseMessages(sourceSessionID)
	if err != nil {
		t.Fatalf("parse messages %s: %v", sourceSessionID, err)
	}
	events, err := reader.ParseEvents(sourceSessionID)
	if err != nil {
		t.Fatalf("parse events %s: %v", sourceSessionID, err)
	}
	return detailView{
		summary:     overview.Summary,
		sourcePaths: overview.SourcePaths,
		messages:    messages,
		events:      events,
	}
}

// —— codex 夹具 ——

// seedCodexSession：最小 rollout 夹具（session_meta 即满足列表/索引读取）；
// 列表排序取文件 mtime，按调用顺序写文件即可获得预期顺序。
func seedCodexSession(t *testing.T, home string, sessionID string) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, path, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/workspace"}}`, sessionID),
	))
	return path
}

// writeCodexTranscriptAt：向传入的 codex 根目录（生产 root 的等价物）写转录。
func writeCodexTranscriptAt(t *testing.T, root string, sessionID string, question string) string {
	t.Helper()
	path := filepath.Join(root, "sessions", "2026", "09", "04",
		fmt.Sprintf("rollout-2026-09-04T09-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, path, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-09-04T09:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		fmt.Sprintf(`{"timestamp": "2026-09-04T09:00:01.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": %q}]}}`, question),
	))
	return path
}

// coldRolloutLines：冷会话 rollout 的明文内容，压缩夹具解出来就是它。
func coldRolloutLines(rolloutID string) []string {
	return []string{
		fmt.Sprintf(`{"timestamp": "2026-03-01T10:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/cold", "timestamp": "2026-03-01T10:00:00.000Z"}}`, rolloutID),
		`{"timestamp": "2026-03-01T10:00:01.000Z", "type": "turn_context", "payload": {"model": "gpt-5-codex"}}`,
		`{"timestamp": "2026-03-01T10:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "input_text", "text": "cold session question"}]}}`,
		`{"timestamp": "2026-03-01T10:00:03.000Z", "type": "response_item", "payload": {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "answer from compressed rollout"}]}}`,
		`{"timestamp": "2026-03-01T10:00:04.000Z", "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {"input_tokens": 1000, "cached_input_tokens": 200, "output_tokens": 50, "cache_write_input_tokens": 10}}}}`,
	}
}

// —— opencode 夹具（与真实库同构的最小 schema）——

// seedOpenCodeFamilyAt：向传入的 opencode 根目录写 opencode.db 夹具。
func seedOpenCodeFamilyAt(t *testing.T, opencodeRoot string, rootID string, childID string) {
	t.Helper()
	dbPath := filepath.Join(opencodeRoot, "opencode.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db := openSQLite(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
        CREATE TABLE session_v2 (
            id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL,
            parent_id TEXT,
            slug TEXT NOT NULL,
            directory TEXT NOT NULL,
            title TEXT,
            version TEXT NOT NULL,
            time_created INTEGER NOT NULL,
            time_updated INTEGER NOT NULL
        );
        CREATE TABLE session_message (
            id TEXT PRIMARY KEY,
            session_id TEXT NOT NULL,
            type TEXT NOT NULL,
            seq INTEGER NOT NULL,
            time_created INTEGER NOT NULL,
            time_updated INTEGER NOT NULL,
            data TEXT NOT NULL
        );`)

	mustExec(t, db,
		`INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated) VALUES (?1, ?2, NULL, ?3, ?4, ?5, ?6, ?7, ?8)`,
		rootID, "project-1", rootID, "/tmp/root", "Root task", "1", int64(1_744_366_400_000), int64(1_744_366_450_000))
	mustExec(t, db,
		`INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`,
		childID, "project-1", rootID, childID, "/tmp/root", "Child task", "1", int64(1_744_366_401_000), int64(1_744_366_460_000))

	seedOpenCodeMessageRow(t, db, rootID, "root-msg-1", 1_744_366_400_000, 1, "user",
		`{"time": {"created": 1744366400000}, "text": "Root question", "files": [], "agents": []}`)
	seedOpenCodeMessageRow(t, db, childID, "child-msg-1", 1_744_366_401_000, 1, "assistant",
		`{"time": {"created": 1744366401000}, "agent": "build", "content": [{"type": "text", "text": "Child answer"}]}`)
}

// seedOpenCodeMessageRow：单条消息行。
func seedOpenCodeMessageRow(t *testing.T, db *sql.DB, sessionID string, messageID string, messageTime int64, seq int64, kind string, data string) {
	t.Helper()
	mustExec(t, db,
		`INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`,
		messageID, sessionID, kind, seq, messageTime, messageTime, data)
}

// —— zcode 夹具（数据形态对应 ~/.zcode/cli/db/db.sqlite 的实测结构）——

// seedZcodeFamilyAt：向传入的 db 文件写夹具（zcode 的 scan_root 就是 db 文件
// 路径）。
func seedZcodeFamilyAt(t *testing.T, dbPath string, rootID string, childID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db := openSQLite(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
        CREATE TABLE session (
            id TEXT PRIMARY KEY,
            parent_id TEXT,
            directory TEXT NOT NULL,
            title TEXT,
            time_created INTEGER NOT NULL,
            time_updated INTEGER NOT NULL
        );
        CREATE TABLE message (
            id TEXT PRIMARY KEY,
            session_id TEXT NOT NULL,
            time_created INTEGER NOT NULL,
            sequence INTEGER,
            data TEXT NOT NULL
        );
        CREATE TABLE part (
            id TEXT PRIMARY KEY,
            message_id TEXT NOT NULL,
            session_id TEXT NOT NULL,
            time_created INTEGER NOT NULL,
            sequence INTEGER,
            data TEXT NOT NULL
        );
        CREATE TABLE turn_usage (
            session_id TEXT NOT NULL,
            input_tokens INTEGER NOT NULL DEFAULT 0,
            output_tokens INTEGER NOT NULL DEFAULT 0,
            reasoning_tokens INTEGER NOT NULL DEFAULT 0,
            cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
            cache_read_input_tokens INTEGER NOT NULL DEFAULT 0
        );`)

	mustExec(t, db, `INSERT INTO session (id, parent_id, directory, title, time_created, time_updated) VALUES (?1, NULL, ?2, ?3, 1000, 5000)`, rootID, "/tmp/root", "Root task")
	mustExec(t, db, `INSERT INTO session (id, parent_id, directory, title, time_created, time_updated) VALUES (?1, ?2, ?3, ?4, 2000, 3000)`, childID, rootID, "/tmp/root", "Child task")

	insertZcodeMessage := func(id, sessionID string, timeCreated int64, sequence int64, data string) {
		mustExec(t, db, `INSERT INTO message (id, session_id, time_created, sequence, data) VALUES (?1, ?2, ?3, ?4, ?5)`, id, sessionID, timeCreated, sequence, data)
	}
	insertZcodePart := func(id, messageID, sessionID string, timeCreated int64, sequence int64, data string) {
		mustExec(t, db, `INSERT INTO part (id, message_id, session_id, time_created, sequence, data) VALUES (?1, ?2, ?3, ?4, ?5, ?6)`, id, messageID, sessionID, timeCreated, sequence, data)
	}

	// root 的用户输入：text + file 附件。
	insertZcodeMessage("msg_user", rootID, 1100, 0, `{"role": "user", "time": {"created": 1100}, "semantics": {"kind": "user_prompt"}}`)
	insertZcodePart("part_user_text", "msg_user", rootID, 1100, 0, `{"type": "text", "text": "帮我看下这个项目"}`)
	insertZcodePart("part_user_file", "msg_user", rootID, 1100, 1, `{"type": "file", "mime": "image/png", "url": "zcode-artifact://sess/file-1"}`)

	// root 的助手回复：reasoning + text + tool。
	insertZcodeMessage("msg_assistant", rootID, 1200, 1, `{"role": "assistant", "time": {"created": 1200}, "semantics": {"kind": "assistant_response"}, "modelId": "GLM-5.3"}`)
	insertZcodePart("part_reasoning", "msg_assistant", rootID, 1201, 0, `{"type": "reasoning", "text": "先查看目录结构"}`)
	insertZcodePart("part_text", "msg_assistant", rootID, 1202, 1, `{"type": "text", "text": "目录里有 src 和 README"}`)
	insertZcodePart("part_tool", "msg_assistant", rootID, 1203, 2, `{"type": "tool", "callID": "call_1", "tool": "Bash", "state": {"status": "completed", "input": {"command": "ls"}, "output": "README.md\nsrc"}}`)
	// step-start/step-finish 是统计噪音，断言它们不进时间线。
	insertZcodePart("part_step_start", "msg_assistant", rootID, 1200, 3, `{"type": "step-start"}`)
	insertZcodePart("part_step_finish", "msg_assistant", rootID, 1299, 4, `{"type": "step-finish", "reason": "tool-calls"}`)

	// root 的系统注入消息：todo_reminder 归事件。
	insertZcodeMessage("msg_todo", rootID, 1300, 2, `{"role": "user", "time": {"created": 1300}, "semantics": {"kind": "todo_reminder"}}`)
	insertZcodePart("part_todo_text", "msg_todo", rootID, 1300, 0, `{"type": "text", "text": "- [ ] 分析存储\n- [ ] 写 reader"}`)

	// root 的 timeline_event 消息（如 model_change 分隔线）。
	insertZcodeMessage("msg_timeline", rootID, 1400, 3, `{"role": "assistant", "semantics": {"kind": "timeline_event"}}`)
	insertZcodePart("part_timeline", "msg_timeline", rootID, 1400, 0, `{"type": "timeline", "timelineType": "model_change"}`)

	// child（subagent）的助手回复。
	insertZcodeMessage("msg_child", childID, 2100, 0, `{"role": "assistant", "time": {"created": 2100}, "semantics": {"kind": "assistant_response"}}`)
	insertZcodePart("part_child_text", "msg_child", childID, 2100, 0, `{"type": "text", "text": "子任务完成"}`)

	// turn_usage：root 与 child 各一条，family 用量应为两者之和。
	mustExec(t, db, `INSERT INTO turn_usage (session_id, input_tokens, output_tokens, reasoning_tokens, cache_creation_input_tokens, cache_read_input_tokens) VALUES (?1, 100, 20, 5, 10, 50)`, rootID)
	mustExec(t, db, `INSERT INTO turn_usage (session_id, input_tokens, output_tokens, reasoning_tokens, cache_creation_input_tokens, cache_read_input_tokens) VALUES (?1, 30, 8, 2, 4, 15)`, childID)
}

// —— grokbuild 夹具 ——

func grokFixture(t *testing.T, home string, id string) string {
	t.Helper()
	path := filepath.Join(home, ".grok", "sessions", "not-a-cwd", id, "summary.json")
	writeJSONLRaw(t, path, fmt.Sprintf(`{"info": {"id": %q, "cwd": "/synthetic/project's folder"}, "chat_format_version": 1, "session_summary": "Synthetic summary", "generated_title": "Synthetic title", "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-02T00:00:00Z", "num_messages": 0}`, id))
	return path
}

func grokChildSessionFixture(t *testing.T, home string, child string, attempt string, answer string) string {
	t.Helper()
	dir := filepath.Join(home, ".grok", "sessions", "not-a-cwd", child)
	path := filepath.Join(dir, "summary.json")
	writeJSONLRaw(t, path, fmt.Sprintf(`{"info": {"id": %q, "cwd": "/synthetic/project"}, "chat_format_version": 1, "session_summary": "Synthetic child summary", "attempt_id": %q, "session_kind": "subagent", "created_at": "2026-01-01T00:00:10Z", "updated_at": "2026-01-01T00:00:20Z"}`, child, attempt))
	writeJSONLFile(t, filepath.Join(dir, "chat_history.jsonl"), jsonlLines(t,
		`{"type":"user","content":[{"type":"text","text":"Synthetic child request"}]}`,
		fmt.Sprintf(`{"type":"assistant","content":%q}`, answer),
	))
	return path
}

func grokSubagentMetaFixture(t *testing.T, home string, parent string, child string, attempt string, description string) {
	t.Helper()
	dir := filepath.Join(home, ".grok", "sessions", "not-a-cwd", parent, "subagents", child)
	writeJSONLRaw(t, filepath.Join(dir, "meta.json"), fmt.Sprintf(`{"subagent_id": %q, "attempt_id": %q, "parent_session_id": %q, "child_session_id": %q, "subagent_type": "general-purpose", "description": %q, "status": "completed", "started_at": "2026-01-01T00:00:10Z", "completed_at": "2026-01-01T00:00:20Z"}`,
		child, attempt, parent, child, description))
}

func grokSubagentFixture(t *testing.T, home string, parent string, child string, attempt string, answer string) {
	t.Helper()
	grokChildSessionFixture(t, home, child, attempt, answer)
	grokSubagentMetaFixture(t, home, parent, child, attempt, fmt.Sprintf("task %s", child))
}

// grokHistory：真实夹具内容（history 的写入目标是与 summary.json 同目录
// 的 chat_history.jsonl）。
func grokHistory(t *testing.T, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	writeJSONLFile(t, filepath.Join(dir, "chat_history.jsonl"), jsonlLines(t,
		`{"type":"system", "content":"Synthetic system"}`,
		`{"type":"user", "synthetic_reason":"system_reminder", "content":[{"type":"text","text":"Do not display"}]}`,
		`{"type":"user", "content":[{"type":"text","text":"Synthetic request"},{"type":"image","url":"https://example.invalid/image.png"}]}`,
		`{"type":"reasoning", "summary":[{"type":"summary_text","text":"Synthetic thought"}], "encrypted_content":"NEVER_EXPOSE_SYNTHETIC"}`,
		`{"type":"assistant", "content":"Synthetic answer", "tool_calls":[{"id":"ok","name":"read","arguments":"{\"path\":\"fixture.txt\"}"},{"id":"bad","name":"run","arguments":"{}"}]}`,
		`{"type":"tool_result", "tool_call_id":"ok", "content":"Synthetic success"}`,
		`{"type":"tool_result", "tool_call_id":"bad", "content":"Synthetic failure"}`,
	))
	writeJSONLFile(t, filepath.Join(dir, "updates.jsonl"), jsonlLines(t,
		`{"params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"text":"Synthetic answer"}}}}`,
		`{"params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"bad","status":"failed"}}}`,
	))
	writeJSONLFile(t, filepath.Join(dir, "events.jsonl"), jsonlLines(t,
		`{"type":"turn_started","ts":"2026-01-01T00:00:00Z"}`,
		`{"type":"tool_completed","tool_call_id":"ok","outcome":"success"}`,
		`{"type":"turn_ended","outcome":"completed"}`,
	))
}

// grokMarkerSessionID：marker 消息 payload 里的子代理 session id。
func grokMarkerSessionID(message SessionMessage) (string, bool) {
	for _, block := range message.Blocks {
		payload, ok := block.Payload.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := jsonString(payload, "type"); kind != "subagent_started" {
			continue
		}
		if id, ok := jsonString(payload, "session_id"); ok {
			return id, true
		}
	}
	return "", false
}

// —— pi 夹具 ——

// piSessionPath：pi 会话文件的编码路径。
func piSessionPath(home string, cwd string, timestamp string, id string) string {
	encoded := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(cwd, "/")) + "--"
	fileStamp := strings.NewReplacer(":", "-", ".", "-").Replace(timestamp)
	return filepath.Join(home, ".pi", "agent", "sessions", encoded, fmt.Sprintf("%s_%s.jsonl", fileStamp, id))
}

func piHeaderLine(id string, cwd string, timestamp string) string {
	return fmt.Sprintf(`{"type": "session", "version": 3, "id": %q, "timestamp": %q, "cwd": %q}`, id, timestamp, cwd)
}

// piEntryLine：带 parentId 的 entry 行。
func piEntryLine(kind string, id string, parentID *string, timestamp string, extra string) string {
	parent := "null"
	if parentID != nil {
		parent = fmt.Sprintf("%q", *parentID)
	}
	if extra == "" {
		return fmt.Sprintf(`{"type": %q, "id": %q, "parentId": %s, "timestamp": %q}`, kind, id, parent, timestamp)
	}
	// extra 以 { 开头：并入对象。
	trimmed := strings.TrimPrefix(strings.TrimSpace(extra), "{")
	return fmt.Sprintf(`{"type": %q, "id": %q, "parentId": %s, "timestamp": %q, %s}`, kind, id, parent, timestamp, trimmed)
}

// —— dsh 夹具 ——

func dshSessionHeaderLine(id string, createdAt int64, delegationDepth int) string {
	return fmt.Sprintf(`{"type": "session", "version": 4, "id": %q, "createdAt": %d, "cwd": "D:\\projects\\demo", "isSeeded": false, "delegationDepth": %d, "agentPreset": "standard"}`,
		id, createdAt, delegationDepth)
}

func dshEventLine(seq int64, time int64, kind string, data string) string {
	return fmt.Sprintf(`{"type": %q, "seq": %d, "time": %d, "data": %s}`, kind, seq, time, data)
}

func dshUserMessageLine(seq int64, time int64, text string, sourceKind string) string {
	return dshWithSurfaceAppend(dshEventLine(seq, time, "user/message", fmt.Sprintf(
		`{"content": [{"type": "text", "text": %q}], "source": {"kind": %q, "rpcId": "rpc-1"}, "role": "user", "id": "user-%d"}`,
		text, sourceKind, seq)))
}

func dshAssistantMessageLine(seq int64, time int64, content string, usage string) string {
	return dshWithSurfaceAppend(dshEventLine(seq, time, "assistant/message", fmt.Sprintf(
		`{"turn": 1, "step": 1, "message": {"role": "assistant", "content": %s, "source": {"kind": "model", "provider": "deepseek-account", "model": "deepseek-flash"}, "id": "assistant-%d"}, "usage": %s}`,
		content, seq, usage)))
}

func dshToolResultLine(seq int64, time int64, callID string, text string, isError bool) string {
	return dshWithSurfaceAppend(dshEventLine(seq, time, "tool/result", fmt.Sprintf(
		`{"turn": 1, "step": 1, "message": {"role": "tool", "source": {"kind": "tool", "callId": %q}, "toolCallId": %q, "content": [{"type": "text", "text": %q}], "isError": %t, "id": "tool-result-%d"}}`,
		callID, callID, text, isError, seq)))
}

// dshWithSurfaceAppend：surface 事件统一补 surfaceOp:"append"。
func dshWithSurfaceAppend(line string) string {
	return strings.TrimSuffix(line, "}") + `, "surfaceOp": "append"}`
}

// dshSurfaceReplace：带 replace 折叠区间的 surface 事件。
func dshSurfaceReplace(seq int64, time int64, kind string, data string, start int64, end int64) string {
	return strings.TrimSuffix(dshEventLine(seq, time, kind, data), "}") +
		fmt.Sprintf(`, "surfaceOp": {"op": "replace", "startSeq": %d, "endSeq": %d}}`, start, end)
}

// dshWriteSession：夹具直接写 sessions 根目录（engine 直构的 root 就是
// sessions 目录，等价于生产 sessions_root 的返回值）。
func dshWriteSession(t *testing.T, sessionsRoot string, sessionID string, generation int, compressed bool, lines []string) string {
	t.Helper()
	directory := filepath.Join(sessionsRoot, "--D-projects-demo--", sessionID)
	fileName := fmt.Sprintf("session.v%d.jsonl", generation)
	if compressed {
		fileName = fmt.Sprintf("session.v%d.jsonl.zstd", generation)
	}
	path := filepath.Join(directory, fileName)
	writeJSONLFile(t, path, lines)
	return path
}

// —— 时间与用量断言助手 ——

// localDayMillis：用本地时区的正午构造时间戳，任何时区下都不跨午夜。
func localDayMillis(year int, month time.Month, day int) int64 {
	return localHourMillis(year, month, day, 12)
}

func localHourMillis(year int, month time.Month, day int, hour int) int64 {
	return time.Date(year, month, day, hour, 0, 0, 0, time.Local).UnixMilli()
}

func rfc3339(timestampMillis int64) string {
	return time.UnixMilli(timestampMillis).Format(time.RFC3339)
}

func usageOf(input uint64, output uint64, cacheRead uint64, cacheWrite uint64) SessionTokenUsage {
	return SessionTokenUsage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}

// totalView：小时桶的总量视图（键排序后比较）。
func totalView(buckets UsageHourBuckets) []struct {
	Key   string
	Usage SessionTokenUsage
} {
	view := make([]struct {
		Key   string
		Usage SessionTokenUsage
	}, 0, len(buckets))
	for _, key := range buckets.SortedKeys() {
		view = append(view, struct {
			Key   string
			Usage SessionTokenUsage
		}{Key: key, Usage: buckets[key].Usage})
	}
	return view
}

func assertTotalView(t *testing.T, buckets UsageHourBuckets, want [][2]any) {
	t.Helper()
	got := totalView(buckets)
	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i, entry := range want {
		key := entry[0].(string)
		usage := entry[1].(SessionTokenUsage)
		if got[i].Key != key || got[i].Usage != usage {
			t.Errorf("bucket[%d] = (%s, %+v), want (%s, %+v)", i, got[i].Key, got[i].Usage, key, usage)
		}
	}
}

func assertModelBucket(t *testing.T, bucket UsageHourBucket, want map[string]SessionTokenUsage) {
	t.Helper()
	if len(bucket.Models) != len(want) {
		t.Fatalf("models = %+v, want %+v", bucket.Models, want)
	}
	for model, usage := range want {
		if bucket.Models[model] != usage {
			t.Errorf("model %s = %+v, want %+v", model, bucket.Models[model], usage)
		}
	}
}

func assertModelsEmpty(t *testing.T, bucket UsageHourBucket) {
	t.Helper()
	if len(bucket.Models) != 0 {
		t.Fatalf("models = %+v, want empty", bucket.Models)
	}
}

// touchMtime：显式把文件时刻拨到指定毫秒（索引失效用毫秒精度，夹具写入可能
// 发生在同一毫秒内）。
func touchMtime(t *testing.T, path string, at int64) {
	t.Helper()
	stamp := time.UnixMilli(at)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// pushMtimeForward：把文件时刻往前推 1 秒，模拟「这件事发生在之后的某一刻」。
func pushMtimeForward(t *testing.T, path string) {
	t.Helper()
	ahead := time.Now().Add(time.Second)
	if err := os.Chtimes(path, ahead, ahead); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// summaryTitles：列表条目标题（条目必须带摘要）。
func summaryTitles(t *testing.T, entries []SessionFileEntry) []string {
	t.Helper()
	titles := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Summary == nil {
			t.Fatalf("list entries carry summaries: %+v", entry)
		}
		titles = append(titles, entry.Summary.Title)
	}
	return titles
}

func assertContains(t *testing.T, haystack []string, needle string) {
	t.Helper()
	for _, item := range haystack {
		if item == needle {
			return
		}
	}
	t.Fatalf("%q not found in %v", needle, haystack)
}

func stringsContain(list []string, needle string) bool {
	for _, item := range list {
		if item == needle {
			return true
		}
	}
	return false
}

// —— sqlite 夹具助手 ——

// openSQLite：打开（必要时创建）测试 sqlite 库。
func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return db
}

// mustExec：执行并要求成功。
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// mustQueryInt：单值查询。
func mustQueryInt(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var value int64
	if err := db.QueryRow(query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

// marshalForCompare：结构体 → 字符串（统计一致性断言用）。
func marshalForCompare(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("marshal error: %v", err)
	}
	return string(raw)
}

// mustExecRowsAffected：执行并返回受影响行数。
func mustExecRowsAffected(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("rows affected: %v", err)
	}
	return affected
}

// timeNowLocal：本地当前时间（测试里取年月日）。
func timeNowLocal() time.Time { return time.Now() }
