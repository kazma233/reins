package session

import (
	"encoding/json"
	"testing"
)

// wire 形状契约：以 frontend/bindings/（wails 生成的前端唯一类型来源）为准，
// 逐类型钉住序列化形状——Option 的「省略」与「null」两种语义、空切片恒为
// 数组、枚举 snake_case。任何漂移都会打到前端类型。

func marshalCompact(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func assertJSON(t *testing.T, got string, want string) {
	t.Helper()
	if got != want {
		t.Errorf("json =\n%s\nwant\n%s", got, want)
	}
}

func TestWireShapeSessionTokenUsage(t *testing.T) {
	assertJSON(t, marshalCompact(t, SessionTokenUsage{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
	}), `{"inputTokens":1,"outputTokens":2,"cacheReadTokens":3,"cacheWriteTokens":4}`)
}

func TestWireShapeSessionModelOmitsEmptyProvider(t *testing.T) {
	assertJSON(t, marshalCompact(t, NewSessionModel("", "m")), `{"model":"m"}`)
	assertJSON(t, marshalCompact(t, NewSessionModel("p", "m")), `{"model":"m","provider":"p"}`)
}

func TestWireShapeSessionSummaryNullVsOmitted(t *testing.T) {
	summary := SessionSummary{
		SourceApp:       SourceCodex,
		SourceSessionID: "s1",
		Title:           "t",
		TranscriptPath:  "/p",
		Models:          []SessionModel{},
	}
	// Option 无 skip 的字段恒在（cwd/gitBranch/createdAt/updatedAt 为 null），
	// tokenUsage 带 skip 序列化时省略。
	// 切片字段无 nil→[] 包装：显式 [] 输出 []，nil 输出 null（见
	// TestWireShapeSourceStatusOverviewPageRefresh 的零值分支）。
	assertJSON(t, marshalCompact(t, summary),
		`{"sourceApp":"codex","sourceSessionId":"s1","title":"t","cwd":null,"gitBranch":null,`+
			`"transcriptPath":"/p","createdAt":null,"updatedAt":null,"models":[],`+
			`"archived":false,"compressed":false}`)

	usage := SessionTokenUsage{InputTokens: 9}
	summary.TokenUsage = &usage
	assertJSON(t, marshalCompact(t, summary),
		`{"sourceApp":"codex","sourceSessionId":"s1","title":"t","cwd":null,"gitBranch":null,`+
			`"transcriptPath":"/p","createdAt":null,"updatedAt":null,"tokenUsage":`+
			`{"inputTokens":9,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0},`+
			`"models":[],"archived":false,"compressed":false}`)
}

func TestWireShapeContentBlock(t *testing.T) {
	assertJSON(t, marshalCompact(t, ContentBlock{Kind: "x"}),
		`{"kind":"x","text":null,"toolName":null,"toolCallId":null,"payload":null}`)

	isError := true
	text := "body"
	assertJSON(t, marshalCompact(t, ContentBlock{
		Kind: "x", Text: &text, IsError: &isError,
	}), `{"kind":"x","text":"body","toolName":null,"toolCallId":null,"isError":true,"payload":null}`)
}

func TestWireShapeSessionMessageAndEvent(t *testing.T) {
	assertJSON(t, marshalCompact(t, SessionMessage{ID: "m1", Role: "user", Blocks: []ContentBlock{}}),
		`{"id":"m1","role":"user","timestamp":null,"blocks":[]}`)
	assertJSON(t, marshalCompact(t, SessionEvent{ID: "e1", Kind: "k", Summary: "s"}),
		`{"id":"e1","kind":"k","timestamp":null,"summary":"s","payload":null}`)

	model := NewSessionModel("p", "m")
	sessionID := "root"
	timestamp := int64(1234)
	message := SessionMessage{
		ID: "m2", Role: "assistant", Timestamp: &timestamp,
		Blocks: []ContentBlock{}, Model: &model, SessionID: &sessionID,
	}
	assertJSON(t, marshalCompact(t, message),
		`{"id":"m2","role":"assistant","timestamp":1234,"blocks":[],`+
			`"model":{"model":"m","provider":"p"},"sessionId":"root"}`)
}

func TestWireShapeSourceStatusOverviewPageRefresh(t *testing.T) {
	assertJSON(t, marshalCompact(t, SourceStatus{App: SourcePi, Available: true}),
		`{"app":"pi","available":true,"rootPath":null,"sessionCount":0,"note":null}`)

	overview := SessionOverview{SourcePaths: []string{}, Agents: []SessionAgent{}}
	assertJSON(t, marshalCompact(t, overview),
		`{"summary":{"sourceApp":"","sourceSessionId":"","title":"","cwd":null,"gitBranch":null,`+
			`"transcriptPath":"","createdAt":null,"updatedAt":null,"models":null,`+
			`"archived":false,"compressed":false},"sourcePaths":[],"messageCount":null,`+
			`"eventCount":null,"agents":[]}`)

	page := SessionPage{Sessions: []SessionSummary{}, Limit: 10}
	assertJSON(t, marshalCompact(t, page),
		`{"sourceApp":null,"sessions":[],"offset":0,"limit":10,"nextOffset":null,`+
			`"totalCount":0,"hasMore":false}`)

	app := SourceDsh
	page.SourceApp = &app
	next := 10
	page.NextOffset = &next
	page.TotalCount = 1
	page.HasMore = true
	refresh := SessionRefreshResult{SelectedSource: &app, Sources: []SourceStatus{}, Page: page}
	assertJSON(t, marshalCompact(t, refresh),
		`{"selectedSource":"dsh","sources":[],"page":{"sourceApp":"dsh","sessions":[],`+
			`"offset":0,"limit":10,"nextOffset":10,"totalCount":1,"hasMore":true}}`)
}

func TestWireShapeUsageStats(t *testing.T) {
	assertJSON(t, marshalCompact(t, UsageStats{Sources: []UsageSourceStats{}}), `{"sources":[]}`)
	assertJSON(t, marshalCompact(t, UsageHourPoint{Hour: 9}),
		`{"hour":9,"usage":{"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}}`)
	assertJSON(t, marshalCompact(t, UsageDayPoint{Day: "2026-09-04"}),
		`{"day":"2026-09-04","usage":{"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}}`)
	assertJSON(t, marshalCompact(t, UsageModelDayPoint{Model: "m", Day: "d"}),
		`{"model":"m","day":"d","usage":{"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}}`)
	assertJSON(t, marshalCompact(t, UsageModelHourPoint{Model: "m", Hour: 1}),
		`{"model":"m","hour":1,"usage":{"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}}`)

	source := UsageSourceStats{
		Days: []UsageDayPoint{}, TodayHours: []UsageHourPoint{},
		ModelDays: []UsageModelDayPoint{}, ModelTodayHours: []UsageModelHourPoint{},
	}
	assertJSON(t, marshalCompact(t, source),
		`{"sourceApp":"","days":[],"todayHours":[],"sessionCount":0,"todaySessionCount":0,`+
			`"modelDays":[],"modelTodayHours":[]}`)
}

// SourceApp 作为 wire 枚举：值就是 snake_case 字符串本身。
func TestWireShapeSourceAppEnum(t *testing.T) {
	for app, wire := range map[SourceApp]string{
		SourceCodex:      "codex",
		SourceClaudeCode: "claude_code",
		SourceOpenCode:   "opencode",
		SourcePi:         "pi",
		SourceGrokBuild:  "grokbuild",
		SourceZcode:      "zcode",
		SourceDsh:        "dsh",
	} {
		assertJSON(t, marshalCompact(t, app), `"`+wire+`"`)
	}
}
