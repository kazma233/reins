package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reins/internal/agents"
	"reins/internal/testsupport"
)

// session_engine.rs：读取器引擎按根直接构造（脱离进程 env、不持全局锁）；
// SQL 来源的数据查询从实例 scan_root 派生 db 路径；缺失形态按「还没有会话」
// 处理。

func TestCodexEngineReadsWithoutTouchingProcessEnv(t *testing.T) {
	dir := t.TempDir()
	sessions := filepath.Join(dir, "sessions")
	sessionID := "33333333-3333-4333-8333-333333333333"
	transcript := filepath.Join(sessions, fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcript, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, sessionID),
		`{"timestamp": "2026-04-21T12:00:02.000Z", "type": "response_item", "payload": {"type": "message", "role": "user", "content": [{"type": "text", "text": "How do I list the current files?"}]}}`,
	))

	engine := NewReaderEngine[CodexSessionRow](CodexSpec{}, dir, filepath.Join(dir, "store"))

	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Summary.Title != "How do I list the current files?" {
		t.Fatalf("entries = %+v", entries)
	}

	summary, err := engine.ParseSummary(sessionID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != sessionID {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}

	messages, err := engine.ParseMessages(sessionID)
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	if len(messages) != 1 {
		t.Errorf("messages = %d, want 1", len(messages))
	}

	family, err := engine.FamilyForID(sessionID)
	if err != nil {
		t.Fatalf("family for id: %v", err)
	}
	if family.Root.MemberPath() != transcript {
		t.Errorf("root path = %s, want %s", family.Root.MemberPath(), transcript)
	}
}

func TestClaudeEngineReadsWithoutTouchingProcessEnv(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "reins")
	sessionID := "33333333-3333-4333-8333-333333333333"
	transcript := filepath.Join(projects, sessionID+".jsonl")
	writeJSONLFile(t, transcript, jsonlLines(t,
		fmt.Sprintf(`{"type": "user", "timestamp": "2026-04-21T12:00:00.000Z", "sessionId": %q, "cwd": "/tmp/reins/workspace", "message": {"role": "user", "content": [{"type": "text", "text": "How do I list the current files?"}]}}`, sessionID),
	))

	engine := NewReaderEngine[ClaudeSessionRow](ClaudeSpec{}, dir, filepath.Join(dir, "store"))

	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Summary.Title != "How do I list the current files?" {
		t.Fatalf("entries = %+v", entries)
	}

	summary, err := engine.ParseSummary(sessionID)
	if err != nil {
		t.Fatalf("parse summary: %v", err)
	}
	if summary.SourceSessionID != sessionID {
		t.Errorf("session id = %s", summary.SourceSessionID)
	}

	family, err := engine.FamilyForID(sessionID)
	if err != nil {
		t.Fatalf("family for id: %v", err)
	}
	if family.Root.MemberPath() != transcript {
		t.Errorf("root path = %s, want %s", family.Root.MemberPath(), transcript)
	}
}

// not-found 错误主语（display_label）与 AgentSpec.label（产品名单一来源）
// 的一致性：五家引擎 reader 的错误文案拼写必须与界面其他位置同源，新增
// 覆写时在这里暴露拼写分叉。
func TestReaderDisplayLabelsMatchAgentSpecLabels(t *testing.T) {
	check := func(app SourceApp, display string) {
		t.Helper()
		spec := agents.SpecBySourceApp(app)
		if spec == nil {
			t.Fatalf("%s 未登记 agents 清单", app)
		}
		if display != spec.Label {
			t.Errorf("%s display label = %q, want %q", app, display, spec.Label)
		}
	}
	check(SourceCodex, (CodexSpec{}).Label())
	check(SourceClaudeCode, claudeDisplayLabel())
	check(SourceOpenCode, (OpenCodeSpec{}).Label())
	check(SourceZcode, (ZcodeSpec{}).Label())
	check(SourceDsh, dshDisplayLabel())
}

// —— SQL 来源的 env-free 直构 ——

func TestOpenCodeEngineReadsCustomRootWithoutEnv(t *testing.T) {
	dir := t.TempDir()
	seedOpenCodeFamilyAt(t, dir, "ses_engine_root", "ses_engine_child")

	engine := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, dir, filepath.Join(dir, "store"))

	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Summary.Title != "Root task (+1 subagents)" {
		t.Fatalf("entries = %+v", entries)
	}

	messages, err := engine.ParseMessages("ses_engine_root")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	found := false
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Text != nil && *block.Text == "Root question" {
				found = true
			}
		}
	}
	if !found {
		t.Error("Root question message not found")
	}
}

func TestZcodeEngineReadsCustomRootWithoutEnv(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	seedZcodeFamilyAt(t, dbPath, "ses_engine_root", "ses_engine_child")

	engine := NewReaderEngine[ZcodeSessionRow](ZcodeSpec{}, dbPath, filepath.Join(dir, "store"))

	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 1 || entries[0].Summary.Title != "Root task (+1 subagents)" {
		t.Fatalf("entries = %+v", entries)
	}

	messages, err := engine.ParseMessages("ses_engine_root")
	if err != nil {
		t.Fatalf("parse messages: %v", err)
	}
	found := false
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Text != nil && *block.Text == "帮我看下这个项目" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected user prompt text not found")
	}
}

// 来源根目录存在但数据还没落地时按「还没有会话」处理，不把 no such table
// 或 metadata 读取错误抛给用户。db 文件、数据表、sessions 子目录分属三种
// 缺失形态。

func TestOpenCodeEngineWithoutSessionV2TableListsEmpty(t *testing.T) {
	dir := t.TempDir()
	// db 已存在但建表还没发生：session_v2 随 opencode 首次会话写入才创建。
	db := openSQLite(t, filepath.Join(dir, "opencode.db"))
	mustExec(t, db, "CREATE TABLE other (id TEXT);")
	db.Close()

	engine := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, dir, filepath.Join(dir, "store"))
	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want empty", len(entries))
	}
}

func TestOpenCodeEngineWithoutDBListsEmptyWithoutCreatingIt(t *testing.T) {
	dir := t.TempDir()

	engine := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, dir, filepath.Join(dir, "store"))
	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want empty", len(entries))
	}
	// 读取路径用读写模式打开 sqlite，不先判存在就会凭空建出空库。
	if _, err := os.Stat(filepath.Join(dir, "opencode.db")); !os.IsNotExist(err) {
		t.Error("opencode.db must not be created by a read path")
	}
}

func TestCodexEngineWithoutSessionsDirListsEmpty(t *testing.T) {
	dir := t.TempDir()
	// 只建 codex 根目录（~/.codex 在登录/配置阶段就有），sessions 子目录还没有。

	engine := NewReaderEngine[CodexSessionRow](CodexSpec{}, dir, filepath.Join(dir, "store"))
	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want empty", len(entries))
	}
}

func TestZcodeEngineWithoutSessionTableListsEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	db := openSQLite(t, dbPath)
	mustExec(t, db, "CREATE TABLE other (id TEXT);")
	db.Close()

	engine := NewReaderEngine[ZcodeSessionRow](ZcodeSpec{}, dbPath, filepath.Join(dir, "store"))
	entries, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want empty", len(entries))
	}
}

// 贡献表增量：opencode 活跃写入（回写旧消息 tokens、追加新消息、删除消息）
// 后，下一次扫描按行级 (id, time_updated) 幂等更新聚合；增量结果必须与
// 全新 store 的全量提取一致。
func TestOpenCodeContribUpdatesIncrementallyOnDBWrites(t *testing.T) {
	dir := t.TempDir()
	seedOpenCodeFamilyAt(t, dir, "ses_contrib", "ses_contrib_child")
	dbPath := filepath.Join(dir, "opencode.db")

	db := openSQLite(t, dbPath)
	seedOpenCodeMessageRow(t, db, "ses_contrib", "msg-a1", 1_744_366_402_000, 2, "assistant",
		`{"time": {"created": 1744366402000}, "model": {"providerID": "p1", "id": "model-x"}, "tokens": {"input": 100, "output": 40, "reasoning": 10, "cache": {"read": 5, "write": 2}}}`)
	db.Close()

	engine := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, dir, filepath.Join(dir, "store"))

	// 首次扫描全量提取；usage 口径：input 不含缓存读，output 并 reasoning。
	first, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first entries = %d, want 1", len(first))
	}
	firstUsage := first[0].Summary.TokenUsage
	if firstUsage == nil || firstUsage.InputTokens != 100 || firstUsage.OutputTokens != 50 {
		t.Fatalf("first usage = %+v, want input 100 output 50", firstUsage)
	}
	models := modelNames(first[0].Summary.Models)
	if len(models) != 1 || models[0] != "model-x" {
		t.Fatalf("first models = %v, want [model-x]", models)
	}

	// 跨毫秒确保 db mtime（行缓存 freshness）与行级 stamp 都能观察到变化。
	time.Sleep(20 * time.Millisecond)
	db = openSQLite(t, dbPath)
	mustExec(t, db, `UPDATE session_message SET time_updated = time_updated + 1000, data = json_set(data, '$.tokens.input', 300) WHERE id = 'msg-a1'`)
	seedOpenCodeMessageRow(t, db, "ses_contrib", "msg-a2", 1_744_366_403_000, 3, "assistant",
		`{"time": {"created": 1744366403000}, "model": {"providerID": "p1", "id": "model-y"}, "tokens": {"input": 50, "output": 20, "reasoning": 0, "cache": {"read": 0, "write": 0}}}`)
	// 删除子会话的 assistant 消息：贡献随之消失。
	mustExec(t, db, `DELETE FROM session_message WHERE session_id = 'ses_contrib_child' AND type = 'assistant'`)
	db.Close()

	// 增量扫描（共享 store 的贡献表 diff）与全新 store 的全量提取对照。
	incremental, err := engine.ListEntries()
	if err != nil {
		t.Fatalf("incremental list: %v", err)
	}
	fresh := NewReaderEngine[OpenCodeSessionRow](OpenCodeSpec{}, dir, filepath.Join(dir, "store_fresh"))
	full, err := fresh.ListEntries()
	if err != nil {
		t.Fatalf("fresh list: %v", err)
	}

	if len(incremental) != len(full) {
		t.Fatalf("incremental %d vs full %d", len(incremental), len(full))
	}
	incrementalUsage := incremental[0].Summary.TokenUsage
	fullUsage := full[0].Summary.TokenUsage
	if incrementalUsage == nil || fullUsage == nil || *incrementalUsage != *fullUsage {
		t.Fatalf("usage mismatch: %+v vs %+v", incrementalUsage, fullUsage)
	}
	// 回写后的期望值：300 + 50，旧值不得重复累计。
	if incrementalUsage.InputTokens != 350 {
		t.Errorf("incremental input = %d, want 350", incrementalUsage.InputTokens)
	}
	incrementalModels := modelNames(incremental[0].Summary.Models)
	fullModels := modelNames(full[0].Summary.Models)
	if len(incrementalModels) != len(fullModels) {
		t.Fatalf("models mismatch: %v vs %v", incrementalModels, fullModels)
	}
	for i := range incrementalModels {
		if incrementalModels[i] != fullModels[i] {
			t.Fatalf("models mismatch: %v vs %v", incrementalModels, fullModels)
		}
	}
}

// 重定向后的会话根：读取器走 agents 清单的 GlobalRoot（重定向优先、HOME
// 兜底），与 workspace/providers 同一条路径来源。codex 侧的行为证据：
// CODEX_HOME 重定向时官方 CLI 把 sessions/年/月/日 树建在重定向目录下。
func TestCodexReaderFollowsCodexHomeRedirect(t *testing.T) {
	home := testsupport.TestDir(t, "codex-redirect-home")
	redirect := testsupport.TestDir(t, "codex-redirect-root")
	homeGuard(t, home)
	setRedirect(t, "CODEX_HOME", redirect)

	// 会话种在重定向根下；旧实现读 HOME/.codex，列表会是空。
	sessionID := "44444444-4444-4444-8444-444444444444"
	transcript := filepath.Join(redirect, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))
	writeJSONLFile(t, transcript, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/workspace"}}`, sessionID),
	))

	// 重定向变量即整个根：官方 CLI 实测把 sessions/年/月/日 树建在变量目录
	// 下，.codex 只是 HOME 兜底时的缺省名。
	root, err := CodexRoot()
	if err != nil || root != redirect {
		t.Fatalf("codex root = %s %v, want %s", root, err, redirect)
	}
	entries, err := reader(SourceCodex)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	list, err := entries.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("entries = %d, want 1（CODEX_HOME 下的会话应被列出）", len(list))
	}
	if list[0].SourceSessionID != sessionID {
		t.Errorf("session id = %s", list[0].SourceSessionID)
	}
}

func TestClaudeReaderFollowsClaudeConfigDirRedirect(t *testing.T) {
	home := testsupport.TestDir(t, "claude-redirect-home")
	redirect := testsupport.TestDir(t, "claude-redirect-root")
	homeGuard(t, home)
	setRedirect(t, "CLAUDE_CONFIG_DIR", redirect)

	transcript := filepath.Join(redirect, "projects", "demo-project", "redirected-session.jsonl")
	writeJSONLFile(t, transcript, jsonlLines(t,
		`{"timestamp": "2026-04-21T12:00:00.000Z", "sessionId": "redirected-session", "type": "user", "message": {"content": "Main task"}}`,
	))

	r, err := reader(SourceClaudeCode)
	if err != nil {
		t.Fatalf("reader: %v", err)
	}
	list, err := r.ListEntries()
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("entries = %d, want 1（CLAUDE_CONFIG_DIR 下的会话应被列出）", len(list))
	}
	if list[0].SourceSessionID != "redirected-session" {
		t.Errorf("session id = %s", list[0].SourceSessionID)
	}
}

func TestDshRootResolvesDshHomeRedirect(t *testing.T) {
	home := testsupport.TestDir(t, "dsh-redirect-home")
	redirect := testsupport.TestDir(t, "dsh-redirect-root")
	homeGuard(t, home)
	setRedirect(t, "DSH_HOME", redirect)

	// 重定向变量即整个根，与 workspace/providers 的 DSH_HOME 语义同一份。
	// dsh 侧官方行为已实测（0.2.0-rc.2）：DSH_HOME 重定向时 boot profile
	// 数秒内把 sessions/ 建在变量目录下，会话存储整体随迁。
	root, err := DshRoot()
	if err != nil || root != redirect {
		t.Fatalf("dsh root = %s %v, want %s", root, err, redirect)
	}
	sessions, err := DshSessionsRoot()
	if err != nil || sessions != filepath.Join(redirect, "sessions") {
		t.Fatalf("dsh sessions root = %s %v", sessions, err)
	}
}

// modelNames：模型列表的 model 名切片。
func modelNames(models []SessionModel) []string {
	names := make([]string, 0, len(models))
	for _, model := range models {
		names = append(names, model.Model)
	}
	return names
}
