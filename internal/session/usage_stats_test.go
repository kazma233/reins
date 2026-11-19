package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// session_usage_stats.rs：用量小时聚合的口径测试——跨天会话按消耗事件的
// 时间戳拆分，Codex 累计值差分，各来源归一后的四项与列表页 family 求和
// 一致；日序列与今日小时序列都由小时桶聚合而来。

func TestClaudeUsageHoursSplitsCrossDaySessionsByMessageTimestamps(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	sessionID := "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(home, ".claude", "projects", "demo", sessionID+".jsonl")

	writeJSONLFile(t, path, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "assistant", "message": {"content": "day one answer", "model": "glm-5.3", "usage": {"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 1000, "cache_creation_input_tokens": 50}}}`,
			rfc3339(localDayMillis(2026, 4, 21)), sessionID),
		// 同一会话两天后的续聊：按消息时间归到另一天，不并入首日；换模型后
		// 各自归属自己的模型。
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "assistant", "message": {"content": "day three answer", "model": "glm-5.3-flash", "usage": {"input_tokens": 10, "output_tokens": 5, "cache_read_input_tokens": 200}}}`,
			rfc3339(localDayMillis(2026, 4, 23)), sessionID),
	))

	buckets, err := ClaudeCodeUsageHours(path)
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	assertTotalView(t, buckets, [][2]any{
		{"2026-04-21T12", usageOf(100, 20, 1000, 50)},
		{"2026-04-23T12", usageOf(10, 5, 200, 0)},
	})
	// 模型切片与总量同桶同源，按 message.model 归属。
	assertModelBucket(t, buckets["2026-04-21T12"], map[string]SessionTokenUsage{
		"glm-5.3": usageOf(100, 20, 1000, 50),
	})
	assertModelBucket(t, buckets["2026-04-23T12"], map[string]SessionTokenUsage{
		"glm-5.3-flash": usageOf(10, 5, 200, 0),
	})
}

func TestCodexUsageHoursDiffsCumulativeTokenCountsAcrossDays(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	sessionID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	path := filepath.Join(home, ".codex", "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", sessionID))

	tokenCountEvent := func(timestampMillis int64, input uint64, cached uint64, cacheWrite uint64, output uint64) string {
		return fmt.Sprintf(`{"timestamp": %q, "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {"input_tokens": %d, "cached_input_tokens": %d, "cache_write_input_tokens": %d, "output_tokens": %d}}}}`,
			rfc3339(timestampMillis), input, cached, cacheWrite, output)
	}
	turnContextEvent := func(timestampMillis int64, model string) string {
		return fmt.Sprintf(`{"timestamp": %q, "type": "turn_context", "payload": {"model": %q}}`,
			rfc3339(timestampMillis), model)
	}

	writeJSONLFile(t, path, jsonlLines(t,
		// 首事件视作从 0 起步：全量归入首日（归一后 600/30/400/100）。
		turnContextEvent(localDayMillis(2026, 4, 21), "gpt-5.3"),
		tokenCountEvent(localDayMillis(2026, 4, 21), 1000, 400, 100, 30),
		// 次日累计到 2000/900/150/60：差分归入次日（500/30/500/50）；换了
		// 模型，差分归属新模型。
		turnContextEvent(localDayMillis(2026, 4, 22), "gpt-5.3-codex"),
		tokenCountEvent(localDayMillis(2026, 4, 22), 2000, 900, 150, 60),
	))

	buckets, err := CodexUsageHours(path)
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	assertTotalView(t, buckets, [][2]any{
		{"2026-04-21T12", usageOf(600, 30, 400, 100)},
		{"2026-04-22T12", usageOf(500, 30, 500, 50)},
	})
	// token_count 差分归属最近一次 turn_context 声明的模型。
	assertModelBucket(t, buckets["2026-04-21T12"], map[string]SessionTokenUsage{
		"gpt-5.3": usageOf(600, 30, 400, 100),
	})
	assertModelBucket(t, buckets["2026-04-22T12"], map[string]SessionTokenUsage{
		"gpt-5.3-codex": usageOf(500, 30, 500, 50),
	})

	// 差分求和必须回到最后一条累计值的归一口径，与列表页 summary 一致。
	var total SessionTokenUsage
	for _, key := range buckets.SortedKeys() {
		bucket := buckets[key]
		total.Accumulate(&bucket.Usage)
	}
	if total != usageOf(1100, 60, 900, 150) {
		t.Errorf("total = %+v, want (1100, 60, 900, 150)", total)
	}
}

func TestCodexUsageHoursClampsToZeroWhenCumulativeResets(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	path := filepath.Join(home, ".codex", "sessions", "2026", "04", "21",
		"rollout-2026-04-21T12-00-00-cccccccc-cccc-4ccc-8ccc-cccccccccccc.jsonl")

	event := func(timestampMillis int64, input uint64, output uint64) string {
		return fmt.Sprintf(`{"timestamp": %q, "type": "event_msg", "payload": {"type": "token_count", "info": {"total_token_usage": {"input_tokens": %d, "cached_input_tokens": 0, "cache_write_input_tokens": 0, "output_tokens": %d}}}}`,
			rfc3339(timestampMillis), input, output)
	}

	writeJSONLFile(t, path, jsonlLines(t,
		// compact 等重置会让累计值回退；差分钳 0，宁可少计不重复计。
		event(localDayMillis(2026, 4, 21), 1000, 100),
		event(localDayMillis(2026, 4, 22), 300, 20),
	))

	buckets, err := CodexUsageHours(path)
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	assertTotalView(t, buckets, [][2]any{
		{"2026-04-21T12", usageOf(1000, 100, 0, 0)},
		{"2026-04-22T12", usageOf(0, 0, 0, 0)},
	})
	// 无 turn_context 声明模型时，只有总量没有模型切片。
	for _, key := range buckets.SortedKeys() {
		assertModelsEmpty(t, buckets[key])
	}
}

func TestPiUsageHoursBucketsAssistantAndSubagentRuns(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	path := filepath.Join(home, ".pi", "agent", "sessions", "--tmp-pi-usage--", "2026-09-04T09-00-00-000Z_pi-usage.jsonl")

	writeJSONLFile(t, path, jsonlLines(t,
		fmt.Sprintf(`{"type": "session", "version": 3, "id": "pi-usage", "timestamp": %q, "cwd": "/tmp/pi-usage"}`,
			rfc3339(localDayMillis(2026, 9, 4))),
		fmt.Sprintf(`{"type": "message", "id": "m1", "parentId": null, "timestamp": %q, "message": {"role": "assistant", "content": "answer", "model": "deepseek-flash", "usage": {"input": 197, "output": 180, "cacheRead": 6656, "cacheWrite": 0}}}`,
			rfc3339(localDayMillis(2026, 9, 4))),
		// 子代理 run 的消耗按 toolResult entry 自身的时间戳归桶。
		fmt.Sprintf(`{"type": "message", "id": "m2", "parentId": "m1", "timestamp": %q, "message": {"role": "toolResult", "toolName": "subagent", "toolCallId": "call-1", "content": "subagent report", "details": {"results": [{"title": "run", "usage": {"input": 500, "output": 50, "cacheRead": 200, "cacheWrite": 10}}]}}}`,
			rfc3339(localDayMillis(2026, 9, 6))),
	))

	buckets, err := PiUsageHours(path)
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	assertTotalView(t, buckets, [][2]any{
		{"2026-09-04T12", usageOf(197, 180, 6656, 0)},
		{"2026-09-06T12", usageOf(500, 50, 200, 10)},
	})
	// 主线 assistant 按 message.model 归属；subagent run 转录没有模型字段。
	assertModelBucket(t, buckets["2026-09-04T12"], map[string]SessionTokenUsage{
		"deepseek-flash": usageOf(197, 180, 6656, 0),
	})
	assertModelsEmpty(t, buckets["2026-09-06T12"])
}

func TestGrokbuildUsageHoursReadsIncrementalTurnsByEndedAt(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	dir := filepath.Join(home, ".grok", "sessions", "bucket", "session-turns")
	writeJSONLRaw(t, filepath.Join(dir, "summary.json"),
		`{"info": {"id": "session-turns", "cwd": "/synthetic"}, "chat_format_version": 1, "session_summary": "turns", "created_at": "2026-09-17T08:00:00Z", "updated_at": "2026-09-18T08:00:00Z"}`)
	writeJSONLRaw(t, filepath.Join(dir, "usage.json"), fmt.Sprintf(`{
		"sessionId": "session-turns",
		"session": {"inputTokens": 3000, "outputTokens": 90, "cachedReadTokens": 1300, "cacheCreationTokens": 150},
		"turns": [
			{
				"turnNumber": 1,
				"endedAt": %q,
				"inputTokens": 2000, "outputTokens": 60, "cachedReadTokens": 900, "cacheCreationTokens": 100,
				"reasoningTokens": 30, "modelCalls": 1, "turnCount": 1,
				"modelUsage": {
					"deepseek-flash": {"inputTokens": 1500, "outputTokens": 40, "cachedReadTokens": 600, "cacheCreationTokens": 100, "reasoningTokens": 20, "totalTokens": 2140, "modelCalls": 1},
					"glm-5.3": {"inputTokens": 500, "outputTokens": 20, "cachedReadTokens": 300, "cacheCreationTokens": 0, "reasoningTokens": 10, "totalTokens": 820, "modelCalls": 1}
				}
			},
			{
				"turnNumber": 2,
				"endedAt": %q,
				"inputTokens": 1000, "outputTokens": 30, "cachedReadTokens": 400, "cacheCreationTokens": 50,
				"reasoningTokens": 15, "modelCalls": 1, "turnCount": 2
			}
		]
	}`,
		rfc3339(localHourMillis(2026, 9, 17, 10)),
		rfc3339(localDayMillis(2026, 9, 18)),
	))

	summaryPath := filepath.Join(dir, "summary.json")
	buckets, err := GrokUsageHours(summaryPath)
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	// input 扣掉缓存命中，与 session 汇总口径一致。
	assertTotalView(t, buckets, [][2]any{
		{"2026-09-17T10", usageOf(1100, 60, 900, 100)},
		{"2026-09-18T12", usageOf(600, 30, 400, 50)},
	})
	// 有 modelUsage 的轮按模型切片（切片同样扣缓存命中），没有的轮只进总量。
	assertModelBucket(t, buckets["2026-09-17T10"], map[string]SessionTokenUsage{
		"deepseek-flash": usageOf(900, 40, 600, 100),
		"glm-5.3":        usageOf(200, 20, 300, 0),
	})
	assertModelsEmpty(t, buckets["2026-09-18T12"])

	// 旧版本 usage.json 没有 turns 数组：空桶而不是报错。
	writeJSONLRaw(t, filepath.Join(dir, "usage.json"),
		`{"sessionId": "session-turns", "session": {"inputTokens": 3000, "outputTokens": 90, "cachedReadTokens": 1300, "cacheCreationTokens": 150}}`)
	buckets, err = GrokUsageHours(summaryPath)
	if err != nil {
		t.Fatalf("usage hours without turns: %v", err)
	}
	if !buckets.IsEmpty() {
		t.Fatalf("buckets = %+v, want empty", buckets)
	}
}

// 冷缓存 + assistant 行数超过 SQLite 绑定变量上限时，贡献提取必须分批工作：
// 一次性全量 IN (...) 会让 prepare 直接失败且缓存永远写不进去，用量统计与
// 列表页陷入不可恢复的报错循环。
func TestOpenCodeUsageHoursColdCacheBeyondSQLParameterLimit(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	dbPath, err := OpenCodeDBPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db := openSQLite(t, dbPath)
	mustExec(t, db, `
        CREATE TABLE session_message (
            id TEXT PRIMARY KEY,
            session_id TEXT NOT NULL,
            type TEXT NOT NULL,
            seq INTEGER NOT NULL,
            time_created INTEGER NOT NULL,
            time_updated INTEGER NOT NULL,
            data TEXT NOT NULL
        );`)
	transaction, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i <= 32766; i++ {
		if _, err := transaction.Exec(
			"INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, 'ses-cold', 'assistant', ?2, 1, 1, '{}')",
			fmt.Sprintf("msg-%06d", i), i); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db.Close()

	hours, err := OpenCodeUsageHours()
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	if hours == nil {
		t.Fatal("db exists in fixture home")
	}
	if hours.SessionCount != 1 {
		t.Errorf("session count = %d, want 1", hours.SessionCount)
	}
}

func TestOpenCodeUsageHoursGroupsByMessageTime(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	dbPath, err := OpenCodeDBPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
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
	mustExec(t, db, `INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated) VALUES (?1, ?2, NULL, ?3, ?4, ?5, ?6, ?7, ?8)`,
		"ses-days", "project-1", "ses-days", "/tmp/root", "Days", "1", 0, 0)

	// reasoning 并入输出；data.time.created 优先于 time_created 列
	// （两天的时间都在 data 里，列时间故意错开验证优先级）。
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, 'assistant', 1, ?3, ?3, ?4)`,
		"msg-1", "ses-days", localDayMillis(2026, 4, 21),
		fmt.Sprintf(`{"time": {"created": %d}, "model": {"id": "MiniMax-M3", "providerID": "minimax"}, "tokens": {"input": 1000, "output": 100, "reasoning": 50, "cache": {"read": 500, "write": 20}}}`,
			localDayMillis(2026, 4, 21)))
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, 'assistant', 1, ?3, ?3, ?4)`,
		"msg-2", "ses-days", localDayMillis(2026, 4, 23),
		fmt.Sprintf(`{"time": {"created": %d}, "tokens": {"input": 200, "output": 20, "cache": {"read": 0, "write": 0}}}`,
			localDayMillis(2026, 4, 23)))

	hours, err := OpenCodeUsageHours()
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	if hours == nil {
		t.Fatal("db exists in fixture home")
	}
	if hours.SessionCount != 1 {
		t.Errorf("session count = %d, want 1", hours.SessionCount)
	}
	// 夹具日期不是今天，今日会话数为 0。
	if hours.TodaySessionCount != 0 {
		t.Errorf("today session count = %d, want 0", hours.TodaySessionCount)
	}
	assertTotalView(t, hours.Buckets, [][2]any{
		{"2026-04-21T12", usageOf(1000, 150, 500, 20)},
		{"2026-04-23T12", usageOf(200, 20, 0, 0)},
	})
	// 模型切片取 data.model.id；没有 model 节点的消息只进总量。
	assertModelBucket(t, hours.Buckets["2026-04-21T12"], map[string]SessionTokenUsage{
		"MiniMax-M3": usageOf(1000, 150, 500, 20),
	})
	assertModelsEmpty(t, hours.Buckets["2026-04-23T12"])
}

func TestZcodeUsageHoursGroupsByModelCallStartedAt(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	dbPath, err := ZcodeDBPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db := openSQLite(t, dbPath)
	defer db.Close()
	mustExec(t, db, `
        CREATE TABLE model_usage (
            id TEXT PRIMARY KEY,
            session_id TEXT NOT NULL,
            started_at INTEGER NOT NULL,
            model_id TEXT NOT NULL,
            status TEXT NOT NULL,
            input_tokens INTEGER NOT NULL DEFAULT 0,
            output_tokens INTEGER NOT NULL DEFAULT 0,
            reasoning_tokens INTEGER NOT NULL DEFAULT 0,
            cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
            cache_read_input_tokens INTEGER NOT NULL DEFAULT 0
        );`)

	insertCall := func(id string, startedAt int64, modelID string, status string, input, output, reasoning, creation, cacheRead int64) {
		mustExec(t, db,
			"INSERT INTO model_usage (id, session_id, started_at, model_id, status, input_tokens, output_tokens, reasoning_tokens, cache_creation_input_tokens, cache_read_input_tokens) VALUES (?1, 'ses-days', ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)",
			id, startedAt, modelID, status, input, output, reasoning, creation, cacheRead)
	}

	// input_tokens 是全口径，拆掉 cache_read；output 并入 reasoning；error
	// 调用的部分用量不计。
	insertCall("call-1", localDayMillis(2026, 4, 21), "GLM-5.3", "completed", 1000, 30, 10, 100, 400)
	insertCall("call-2", localDayMillis(2026, 4, 23), "GLM-5.3-Flash", "completed", 200, 5, 0, 0, 0)
	insertCall("call-error", localDayMillis(2026, 4, 23), "GLM-5.3", "error", 999, 99, 0, 0, 0)

	hours, err := ZcodeUsageHours()
	if err != nil {
		t.Fatalf("usage hours: %v", err)
	}
	if hours == nil {
		t.Fatal("db exists in fixture home")
	}
	if hours.SessionCount != 1 {
		t.Errorf("session count = %d, want 1", hours.SessionCount)
	}
	if hours.TodaySessionCount != 0 {
		t.Errorf("today session count = %d, want 0", hours.TodaySessionCount)
	}
	assertTotalView(t, hours.Buckets, [][2]any{
		{"2026-04-21T12", usageOf(600, 40, 400, 100)},
		{"2026-04-23T12", usageOf(200, 5, 0, 0)},
	})
	assertModelBucket(t, hours.Buckets["2026-04-21T12"], map[string]SessionTokenUsage{
		"GLM-5.3": usageOf(600, 40, 400, 100),
	})
	assertModelBucket(t, hours.Buckets["2026-04-23T12"], map[string]SessionTokenUsage{
		"GLM-5.3-Flash": usageOf(200, 5, 0, 0),
	})
}

func TestCodexUsageCountsCompressedAndArchivedRollouts(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	// 压缩（冷会话）与归档只是转录的存储形态，消耗记录仍属于用户用量，会话
	// 数与 token 都要计入；夹具两份内容一致，期望值即单份的两倍。
	sessions := filepath.Join(home, ".codex", "sessions", "2026", "03", "01")
	archived := filepath.Join(home, ".codex", "archived_sessions")
	if err := os.MkdirAll(sessions, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(archived, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeJSONLRaw(t, filepath.Join(sessions,
		fmt.Sprintf("rollout-2026-03-01T10-00-00-%s.jsonl.zst", codexColdID)), string(codexColdRolloutZst))
	archivedID := "22222222-2222-4222-8222-222222222222"
	writeJSONLFile(t, filepath.Join(archived,
		fmt.Sprintf("rollout-2026-03-01T11-00-00-%s.jsonl", archivedID)), coldRolloutLines(archivedID))

	spec := Spec(SourceCodex)
	stats, err := spec.UsageDays()
	if err != nil {
		t.Fatalf("usage days: %v", err)
	}
	if stats == nil {
		t.Fatal("codex usage stats missing")
	}
	if stats.SessionCount != 2 {
		t.Fatalf("session count = %d, want 2", stats.SessionCount)
	}
	var total SessionTokenUsage
	for _, day := range stats.Days {
		total.Accumulate(&day.Usage)
	}
	if total != usageOf(1600, 100, 400, 20) {
		t.Errorf("total = %+v, want (1600, 100, 400, 20)", total)
	}
}

func TestUsageStatsInnerAggregatesAvailableSourcesAndSurvivesCacheHits(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	sessionID := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	writeJSONLFile(t, filepath.Join(home, ".claude", "projects", "demo", sessionID+".jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "user", "message": {"content": "task"}}`,
			rfc3339(localDayMillis(2026, 4, 21)), sessionID),
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "assistant", "message": {"content": "answer", "usage": {"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 1000}}}`,
			rfc3339(localDayMillis(2026, 4, 21)), sessionID),
	))

	// 首次为解析结果，第二次走持久缓存；两次结果必须一致。
	first, err := UsageStatsInner()
	if err != nil {
		t.Fatalf("first stats: %v", err)
	}
	second, err := UsageStatsInner()
	if err != nil {
		t.Fatalf("second stats: %v", err)
	}
	if marshalForCompare(first) != marshalForCompare(second) {
		t.Fatalf("stats differ across cache hits:\n%s\n%s", marshalForCompare(first), marshalForCompare(second))
	}

	hasClaude := false
	hasCodex := false
	for _, source := range first.Sources {
		switch source.SourceApp {
		case SourceClaudeCode:
			hasClaude = true
		case SourceCodex:
			hasCodex = true
		}
	}
	if !hasClaude {
		t.Fatalf("claude source missing: %+v", first.Sources)
	}
	if hasCodex {
		t.Error("unavailable source leaked")
	}

	for _, source := range first.Sources {
		if source.SourceApp != SourceClaudeCode {
			continue
		}
		if source.SessionCount != 1 {
			t.Errorf("session count = %d, want 1", source.SessionCount)
		}
		if len(source.Days) != 1 || source.Days[0].Day != "2026-04-21" || source.Days[0].Usage != usageOf(100, 20, 1000, 0) {
			t.Errorf("days = %+v", source.Days)
		}
		// 夹具日期不是今天，今日序列为空。
		if len(source.TodayHours) != 0 {
			t.Errorf("today hours = %+v, want empty", source.TodayHours)
		}
		if source.TodaySessionCount != 0 {
			t.Errorf("today session count = %d, want 0", source.TodaySessionCount)
		}
	}
}

func TestUsageStatsInnerReportsTodayHoursAndTodaySessions(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	now := timeNowLocal()
	year, month, day := now.Year(), now.Month(), now.Day()
	sessionID := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	writeJSONLFile(t, filepath.Join(home, ".claude", "projects", "demo", sessionID+".jsonl"), jsonlLines(t,
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "assistant", "message": {"content": "morning", "usage": {"input_tokens": 30, "output_tokens": 3}}}`,
			rfc3339(localHourMillis(year, month, day, 10)), sessionID),
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "assistant", "message": {"content": "afternoon", "usage": {"input_tokens": 5, "output_tokens": 7}}}`,
			rfc3339(localHourMillis(year, month, day, 14)), sessionID),
		// 昨天的消耗不进今日序列。
		fmt.Sprintf(`{"timestamp": %q, "sessionId": %q, "type": "assistant", "message": {"content": "yesterday", "usage": {"input_tokens": 999, "output_tokens": 999}}}`,
			rfc3339(localDayMillis(year, month, day)-86_400_000), sessionID),
	))

	stats, err := UsageStatsInner()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	var claude *UsageSourceStats
	for i := range stats.Sources {
		if stats.Sources[i].SourceApp == SourceClaudeCode {
			claude = &stats.Sources[i]
		}
	}
	if claude == nil {
		t.Fatal("claude stats missing")
	}

	// 同一天的两个小时各自成点，昨天的量只进日序列。
	if len(claude.TodayHours) != 2 ||
		claude.TodayHours[0].Hour != 10 || claude.TodayHours[0].Usage != usageOf(30, 3, 0, 0) ||
		claude.TodayHours[1].Hour != 14 || claude.TodayHours[1].Usage != usageOf(5, 7, 0, 0) {
		t.Fatalf("today hours = %+v", claude.TodayHours)
	}
	if claude.TodaySessionCount != 1 {
		t.Errorf("today session count = %d, want 1", claude.TodaySessionCount)
	}

	todayKey := fmt.Sprintf("%04d-%02d-%02d", year, int(month), day)
	var todayPoint *UsageDayPoint
	for i := range claude.Days {
		if claude.Days[i].Day == todayKey {
			todayPoint = &claude.Days[i]
		}
	}
	if todayPoint == nil {
		t.Fatalf("today in day series missing: %+v", claude.Days)
	}
	if todayPoint.Usage != usageOf(35, 10, 0, 0) {
		t.Errorf("today usage = %+v, want (35, 10)", todayPoint.Usage)
	}
}

// 口径单点：output 并入 reasoning、负值钳 0；输入列语义按来源分两个入口
// （zcode 全口径要拆缓存读，opencode 已是不含命中的口径）。
func TestFullScopeUsageSplitsCacheReadAndMergesReasoning(t *testing.T) {
	usage := FullScopeUsage(UsageColumns{Input: 1100, Output: 100, Reasoning: 50, CacheRead: 300, CacheWrite: 20})
	if usage.InputTokens != 800 || usage.OutputTokens != 150 || usage.CacheReadTokens != 300 || usage.CacheWriteTokens != 20 {
		t.Errorf("usage = %+v", usage)
	}

	// 缓存读大于 input 时钳 0，不出现负数。
	clamped := FullScopeUsage(UsageColumns{Input: 100, Output: 0, Reasoning: 0, CacheRead: 300, CacheWrite: 0})
	if clamped.InputTokens != 0 {
		t.Errorf("clamped input = %d, want 0", clamped.InputTokens)
	}
}

func TestSplitReasoningUsageKeepsInputAsIs(t *testing.T) {
	usage := SplitReasoningUsage(UsageColumns{Input: 1200, Output: 100, Reasoning: 50, CacheRead: 500, CacheWrite: 20})
	// input 已不含缓存读，不重复拆。
	if usage.InputTokens != 1200 || usage.OutputTokens != 150 || usage.CacheReadTokens != 500 {
		t.Errorf("usage = %+v", usage)
	}
}

// SQL 来源的持久桶缓存：db mtime 未变时复用缓存（篡改缓存行能读到篡改值），
// 采集日期过期（跨午夜）后强制重查 db。
func TestDaysFromSQLReusesCacheUntilDayChanges(t *testing.T) {
	home := canonicalTempHome(t)
	homeGuard(t, home)

	dbPath, err := OpenCodeDBPath()
	if err != nil {
		t.Fatalf("db path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db := openSQLite(t, dbPath)
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
	mustExec(t, db, `INSERT INTO session_v2 (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated) VALUES (?1, ?2, NULL, ?3, ?4, ?5, ?6, ?7, ?8)`,
		"ses-cache", "project-1", "ses-cache", "/tmp/root", "Cache", "1", 0, 0)
	mustExec(t, db, `INSERT INTO session_message (id, session_id, type, seq, time_created, time_updated, data) VALUES (?1, ?2, 'assistant', 1, ?3, ?3, ?4)`,
		"msg-1", "ses-cache", localDayMillis(2026, 4, 21),
		`{"tokens": {"input": 1000, "output": 100}}`)
	db.Close()

	collect := func() *UsageSourceStats {
		t.Helper()
		stats, err := DaysFromSQL(SourceOpenCode, OpenCodeDBPath, OpenCodeUsageHours)
		if err != nil {
			t.Fatalf("days from sql: %v", err)
		}
		if stats == nil {
			t.Fatal("source must be available")
		}
		return stats
	}

	first := collect()
	if first.SessionCount != 1 || first.Days[0].Usage.InputTokens != 1000 {
		t.Fatalf("first = %+v", first)
	}

	// 篡改持久缓存里的 input 总量：再采集读到篡改值，证明没有重查 db。
	// 缓存 JSON 走 camelCase（与前端契约同形）。
	cacheDB := openSQLite(t, filepath.Join(home, ".reins", UsageDayDBFileName))
	updated := mustExecRowsAffected(t, cacheDB,
		`UPDATE usage_day_cache SET days = replace(days, '"inputTokens":1000', '"inputTokens":9999')`)
	if updated == 0 {
		t.Fatal("no cache row matched")
	}
	cacheDB.Close()

	second := collect()
	if second.Days[0].Usage.InputTokens != 9999 {
		t.Errorf("second input = %d, want tampered 9999", second.Days[0].Usage.InputTokens)
	}

	// 采集日期改成过去：今日口径过期，必须重查 db，篡改值失效。
	today := TodayPrefix()
	cacheDB = openSQLite(t, filepath.Join(home, ".reins", UsageDayDBFileName))
	mustExec(t, cacheDB, `UPDATE usage_day_cache SET days = replace(days, ?1, '"day":"2000-01-01"')`,
		fmt.Sprintf(`"day":"%s"`, today))
	cacheDB.Close()

	third := collect()
	if third.Days[0].Usage.InputTokens != 1000 {
		t.Errorf("third input = %d, want 1000", third.Days[0].Usage.InputTokens)
	}
}
