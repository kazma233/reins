package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

// claude_code.go：Claude Code 读取器。真差异声明：
//   - 分组按转录内共享 sessionId；root 选取靠 is_root 行标志（非 agent-*
//     命名的子代理文件会回退 family key，id 比较无法区分）；
//   - 错误文案主语经 agents 清单派生（"Claude Code"），与 label() 的
//     "Claude"（锁前缀等引擎契约文本）刻意不同；
//   - 宽失效面：全部转录 mtime + 各自父目录 mtime + 非 root 的 meta.json
//     mtime；不含 projects 根目录本身（子代理改 label 只写 meta.json）；
//   - 坏文件静默跳过：一个损坏转录不能让整个列表失败，也不记日志；
//   - usage/models 在索引行解析时收集（行级直取，无需路径级缓存）；
//   - marker 注入按行标志——与 (+N) 计数（id ≠ root 的成员数）是两条口径。

// ClaudeSessionRow：索引行。
type ClaudeSessionRow struct {
	Path           string         `json:"path"`
	Summary        SessionSummary `json:"summary"`
	AgentSessionID string         `json:"agent_session_id"`
	AgentLabel     *string        `json:"agent_label"`
	IsRoot         bool           `json:"is_root"`
}

// ClaudeSessionFamily：claude 族。
type ClaudeSessionFamily = Family[ClaudeSessionRow]

func (r ClaudeSessionRow) MemberPath() string      { return r.Path }
func (r ClaudeSessionRow) FamilyRootID() string    { return r.Summary.SourceSessionID }
func (r ClaudeSessionRow) MemberID() string        { return r.AgentSessionID }
func (r ClaudeSessionRow) MemberCreatedAt() *int64 { return r.Summary.CreatedAt }
func (r ClaudeSessionRow) MemberUpdatedAt() *int64 { return r.Summary.UpdatedAt }

// member_tie_breaker：转录路径拼写，与引擎化之前的排序一致。
func (r ClaudeSessionRow) MemberTieBreaker() string { return r.Path }

var (
	_ FamilyRow = ClaudeSessionRow{}
	_ Reader    = (*FamilyReader[ClaudeSessionRow])(nil)
)

// ClaudeSpec：claude 的真差异声明。
type ClaudeSpec struct{}

func (ClaudeSpec) App() SourceApp                 { return SourceClaudeCode }
func (ClaudeSpec) Label() string                  { return "Claude" }
func (ClaudeSpec) SummaryKind() SummaryKind       { return SummaryRootTranscript }
func (ClaudeSpec) OverviewCounts() OverviewCounts { return OverviewTimeline }

// ScanRoot：root 下的 projects 子目录。
func (ClaudeSpec) ScanRoot(root string) string { return joinPath(root, "projects") }

func (ClaudeSpec) ListRows(scanRoot string, _ *RowsDb) ([]ClaudeSessionRow, error) {
	files := support.EnumerateJSONLFiles(scanRoot)
	return ScanFiles(files, parseClaudeSessionIndexRow, RowSkipSilently, "Claude")
}

func (ClaudeSpec) GroupFamilies(rows []ClaudeSessionRow) ([]ClaudeSessionFamily, error) {
	return claudeBuildSessionFamilies(rows), nil
}

func (ClaudeSpec) LoadMembers(_ string, members []ClaudeSessionRow) ([]MemberTimeline, error) {
	timelines := make([]MemberTimeline, 0, len(members))
	for i := range members {
		messages := make([]SessionMessage, 0)
		events := make([]SessionEvent, 0)

		lines, err := openPlainLines(members[i].Path)
		if err != nil {
			return nil, err
		}
		scanErr := forEachLine(lines, func(index int, line string) error {
			value, err := parseJSONLine(line)
			if err != nil {
				return err
			}
			if record := parseClaudeTimelineRecord(index, value, members[i].AgentSessionID); record != nil {
				if record.Message != nil {
					messages = append(messages, *record.Message)
				} else {
					events = append(events, *record.Event)
				}
			}
			return nil
		})
		closeErr := lines.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		timelines = append(timelines, MemberTimeline{Messages: messages, Events: events})
	}
	return timelines, nil
}

func (ClaudeSpec) AgentName(row ClaudeSessionRow) string {
	return claudeFamilyMemberDisplayName(row)
}

func (ClaudeSpec) AgentLabel(_ ClaudeSessionFamily, row ClaudeSessionRow) FamilyAgentLabel {
	if row.IsRoot {
		return FamilyAgentLabel{Kind: AgentLabelRoot}
	}
	return FamilyAgentLabel{Kind: AgentLabelChild, Name: claudeFamilyMemberDisplayName(row)}
}

func (ClaudeSpec) Hooks() *FamilyHooks[ClaudeSessionRow] {
	return &FamilyHooks[ClaudeSessionRow]{
		// 错误文案主语经 agents 清单派生，与界面产品名保持同一份拼写；
		// label() 的 "Claude" 是锁前缀等引擎契约文本，两者刻意不同。
		DisplayLabel: func(string) string { return claudeDisplayLabel() },
		IndexFreshness: func(scanRoot string) (Freshness, error) {
			stamp, err := claudeProjectsTimestamp(scanRoot)
			if err != nil {
				return Freshness{}, err
			}
			return StampFreshness(stamp), nil
		},
		// 同上：family 失效把非 root 成员的父目录 mtime 计入。
		FamilyFreshness: func(_ string, family ClaudeSessionFamily) (int64, error) {
			return claudeFamilyTimestamp(family)
		},
		ParseFullSummary: parseClaudeFullSummary,
		// 路径级缓存只含 root 文件；子代理文件的消耗在各自索引行里，行级直取。
		RowUsage: func(row ClaudeSessionRow) *SessionTokenUsage { return row.Summary.TokenUsage },
		// 模型同样在索引行解析时收集：索引行是整文件扫出来的，行里已有结果。
		RowModels: func(row ClaudeSessionRow) []SessionModel { return row.Summary.Models },
		// marker 与名册的 root 判定都用行标志。
		IsSubagentMember: func(_ ClaudeSessionFamily, row ClaudeSessionRow) bool { return !row.IsRoot },
		Marker: func(memberID string, row ClaudeSessionRow) MarkerShape {
			extras := map[string]any{
				"transcript_path": row.Path,
				"is_sidechain":    true,
			}
			return LabeledMarker("Claude", memberID).WithExtras(extras, extras)
		},
	}
}

// claudeDisplayLabel：not-found 错误主语，与界面产品名同源。
func claudeDisplayLabel() string {
	spec := agents.SpecBySourceApp(SourceClaudeCode)
	if spec == nil {
		panic("claude registered in AgentSpecs")
	}
	return spec.Label
}

// ClaudeCodeRoot：env 解析的 claude 根（CLAUDE_CONFIG_DIR 重定向优先）。
func ClaudeCodeRoot() (string, error) {
	return SpecRoot(SourceClaudeCode)
}

// ClaudeCodeReader：static 前置。
var ClaudeCodeReader = NewFamilyReader[ClaudeSessionRow](ClaudeSpec{}, ClaudeCodeRoot)

// claudeBuildSessionFamilies：按 transcript 内共享 sessionId 分组（root 与
// 子代理转录共享该 id），再挑各族 root——优先 is_root 行标志（非 agent-*
// 命名的子代理文件回退 family key，id 比较无法区分），缺失时回退按 id 的
// 登记、再回退首成员。成员排序与 id 表归引擎。
func claudeBuildSessionFamilies(rows []ClaudeSessionRow) []ClaudeSessionFamily {
	byID := make(map[string]ClaudeSessionRow, len(rows))
	for i := range rows {
		byID[rows[i].Summary.SourceSessionID] = rows[i]
	}
	grouped := map[string][]ClaudeSessionRow{}

	for i := range rows {
		row := rows[i]
		grouped[row.Summary.SourceSessionID] = append(grouped[row.Summary.SourceSessionID], row)
	}

	families := make([]ClaudeSessionFamily, 0, len(grouped))
	for rootID, members := range grouped {
		var root ClaudeSessionRow
		found := false
		for i := range members {
			if members[i].IsRoot {
				root, found = members[i], true
				break
			}
		}
		if !found {
			if byIDRow, ok := byID[rootID]; ok {
				root, found = byIDRow, true
			}
		}
		if !found && len(members) > 0 {
			root, found = members[0], true
		}
		if !found {
			continue
		}
		families = append(families, ClaudeSessionFamily{Root: root, Members: members})
	}
	return families
}

func parseClaudeSessionIndexRow(path string) (ClaudeSessionRow, error) {
	var summary SummaryAccumulator
	isRoot := claudeIsRootTranscript(path)

	lines, err := openPlainLines(path)
	if err != nil {
		return ClaudeSessionRow{}, err
	}
	scanErr := forEachLine(lines, func(_ int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return err
		}
		UpdateSummaryTimestamp(&summary, value)

		if kind, _ := jsonType(value); kind == "assistant" {
			message, _ := getJSONObjectPath(value, "message")
			claudeAccumulateUsage(&summary.TokenUsage, message)
			if model, ok := jsonString(value, "message", "model"); ok {
				summary.Models.Record("", model)
			}
		}

		if summary.SessionID == nil {
			summary.SessionID = strField(jsonString(value, "sessionId"))
		}
		return nil
	})
	closeErr := lines.Close()
	if scanErr != nil {
		return ClaudeSessionRow{}, scanErr
	}
	if closeErr != nil {
		return ClaudeSessionRow{}, closeErr
	}

	built, err := BuildSummary(SourceClaudeCode, path, summary)
	if err != nil {
		return ClaudeSessionRow{}, err
	}
	agentSessionID := built.SourceSessionID
	if !isRoot {
		if stem, ok := claudeSubagentSessionIDFromPath(path); ok {
			agentSessionID = stem
		}
	}
	var agentLabel *string
	if !isRoot {
		agentLabel = claudeSubagentLabelFromPath(path)
	}

	return ClaudeSessionRow{
		Path:           path,
		Summary:        built,
		AgentSessionID: agentSessionID,
		AgentLabel:     agentLabel,
		IsRoot:         isRoot,
	}, nil
}

func parseClaudeFullSummary(path string) (SessionSummary, error) {
	var summary SummaryAccumulator

	lines, err := openPlainLines(path)
	if err != nil {
		return SessionSummary{}, err
	}
	scanErr := forEachLine(lines, func(_ int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return err
		}
		UpdateSummaryTimestamp(&summary, value)

		if kind, _ := jsonType(value); kind == "assistant" {
			message, _ := getJSONObjectPath(value, "message")
			claudeAccumulateUsage(&summary.TokenUsage, message)
			if model, ok := jsonString(value, "message", "model"); ok {
				summary.Models.Record("", model)
			}
		}

		if summary.SessionID == nil {
			summary.SessionID = strField(jsonString(value, "sessionId"))
		}
		if summary.Cwd == nil {
			summary.Cwd = strField(jsonString(value, "cwd"))
		}
		if summary.GitBranch == nil {
			summary.GitBranch = strField(jsonString(value, "gitBranch"))
		}

		if kind, _ := jsonType(value); kind == "user" && summary.Title == nil {
			message, hasMessage := getJSONObjectPath(value, "message")
			if hasMessage {
				summary.Title = claudeExtractTitle(message)
			}
		}
		return nil
	})
	closeErr := lines.Close()
	if scanErr != nil {
		return SessionSummary{}, scanErr
	}
	if closeErr != nil {
		return SessionSummary{}, closeErr
	}

	return BuildSummary(SourceClaudeCode, path, summary)
}

// claudeAccumulateUsage：每条 assistant 行携带该次 API 调用的增量 usage，
// 逐条相加；同文件内嵌的 sidechain 消息与 subagents/ 目录下的子代理文件都
// 是 assistant 行，按「会话总消耗」口径一并计入。
func claudeAccumulateUsage(total **SessionTokenUsage, message any) {
	if usage := claudeUsage(message); usage != nil {
		MergeTokenUsage(total, *usage)
	}
}

// claudeUsage：Claude 的 usage 字段直映射；nil 表示该消息没有 usage 记录。
func claudeUsage(message any) *SessionTokenUsage {
	usage, ok := getJSONObjectPath(message, "usage")
	if !ok {
		return nil
	}
	input, _ := jsonU64(usage, "input_tokens")
	output, _ := jsonU64(usage, "output_tokens")
	cacheRead, _ := jsonU64(usage, "cache_read_input_tokens")
	cacheWrite, _ := jsonU64(usage, "cache_creation_input_tokens")
	return &SessionTokenUsage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}

// ClaudeCodeUsageHours：用量曲线的小时桶——与 claudeAccumulateUsage 同源，
// 但按行级 timestamp 归到本地时区的小时，跨天会话由此拆开。
func ClaudeCodeUsageHours(path string) (UsageHourBuckets, error) {
	buckets := UsageHourBuckets{}

	lines, err := openPlainLines(path)
	if err != nil {
		return nil, err
	}
	scanErr := forEachLine(lines, func(_ int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return err
		}
		if kind, _ := jsonType(value); kind != "assistant" {
			return nil
		}

		var timestamp *int64
		if raw, ok := jsonString(value, "timestamp"); ok {
			if millis, ok := support.ParseTimestamp(raw); ok {
				timestamp = &millis
			}
		}

		message, _ := getJSONObjectPath(value, "message")
		if usage := claudeUsage(message); usage != nil {
			model, _ := jsonString(value, "message", "model")
			MergeUsageEvent(buckets, hourKeyOpt(timestamp), model, *usage)
		}
		return nil
	})
	closeErr := lines.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return buckets, nil
}

// claudeIsRootTranscript：subagents/ 目录下的文件是子代理转录。
func claudeIsRootTranscript(path string) bool {
	return filepath.Base(filepath.Dir(path)) != "subagents"
}

func claudeSubagentSessionIDFromPath(path string) (string, bool) {
	stem := fileStem(path)
	return strings.CutPrefix(stem, "agent-")
}

// claudeSubagentLabelFromPath：子代理显示名来自同名 meta.json（描述、类型
// 或名字），缺失即 nil。
func claudeSubagentLabelFromPath(path string) *string {
	metadataPath := withExtension(path, "meta.json")
	contents, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil
	}
	value, err := decodeJSONBytes(contents)
	if err != nil {
		return nil
	}
	if label := claudeSubagentLabelFromMetadata(value); label != "" {
		return &label
	}
	return nil
}

// claudeSubagentLabelFromMetadata：description 优先、其次 agentType、最后
// name；归一后为空视同缺失。
func claudeSubagentLabelFromMetadata(value any) string {
	label := ""
	hasLabel := false
	if text, ok := jsonString(value, "description"); ok {
		label, hasLabel = text, true
	} else if text, ok := jsonString(value, "agentType"); ok {
		label, hasLabel = text, true
	} else if text, ok := jsonString(value, "name"); ok {
		label, hasLabel = text, true
	}
	if !hasLabel {
		return ""
	}
	normalized := NormalizeTitle(label)
	if normalized == "" {
		return ""
	}
	return normalized
}

func claudeFamilyMemberDisplayName(row ClaudeSessionRow) string {
	if row.AgentLabel != nil {
		return *row.AgentLabel
	}
	if stem, ok := claudeSubagentSessionIDFromPath(row.Path); ok {
		return stem
	}
	return row.Summary.Title
}

// claudePathTimestamp：转录文件与其父目录 mtime 的最大值（改名/移动只动
// 父目录）。
func claudePathTimestamp(path string) (int64, error) {
	latest, err := support.FileModifiedTimestampMillis(path)
	if err != nil {
		return 0, err
	}
	parent := filepath.Dir(path)
	if parent != "" {
		if stamp, err := support.FileModifiedTimestampMillis(parent); err == nil && stamp > latest {
			latest = stamp
		} else if err != nil {
			return 0, err
		}
	}
	return latest, nil
}

func claudeProjectsTimestamp(projectsRoot string) (int64, error) {
	latest := int64(0)
	for _, path := range support.EnumerateJSONLFiles(projectsRoot) {
		stamp, err := claudePathTimestamp(path)
		if err != nil {
			return 0, err
		}
		if stamp > latest {
			latest = stamp
		}

		if !claudeIsRootTranscript(path) {
			metadataPath := withExtension(path, "meta.json")
			if _, err := os.Stat(metadataPath); err == nil {
				stamp, err := claudePathTimestamp(metadataPath)
				if err != nil {
					return 0, err
				}
				if stamp > latest {
					latest = stamp
				}
			}
		}
	}
	return latest, nil
}

// claudeFamilyTimestamp：family 失效——成员文件 mtime 最大值，非 root 成员
// 再并入其父目录。
func claudeFamilyTimestamp(family ClaudeSessionFamily) (int64, error) {
	latest := int64(0)
	for i := range family.Members {
		row := family.Members[i]
		rowLatest, err := support.FileModifiedTimestampMillis(row.Path)
		if err != nil {
			return 0, err
		}
		if !row.IsRoot {
			parent := filepath.Dir(row.Path)
			if parent != "" {
				if stamp, err := support.FileModifiedTimestampMillis(parent); err != nil {
					return 0, err
				} else if stamp > rowLatest {
					rowLatest = stamp
				}
			}
		}
		if rowLatest > latest {
			latest = rowLatest
		}
	}
	return latest, nil
}

// parseClaudeTimelineRecord：user/assistant 进消息时间线，其余进事件。
func parseClaudeTimelineRecord(index int, value any, sessionID string) *TimelineRecord {
	var timestamp *int64
	if raw, ok := jsonString(value, "timestamp"); ok {
		if millis, ok := support.ParseTimestamp(raw); ok {
			timestamp = &millis
		}
	}

	kind, _ := jsonType(value)
	switch kind {
	case "user", "assistant":
		message, hasMessage := getJSONObjectPath(value, "message")
		role := "unknown"
		if text, ok := jsonString(message, "role"); ok {
			role = text
		} else if hasMessage {
			role = kind
		}
		content, hasContent := getJSONObjectPath(message, "content")
		blocks := parseClaudeMessageBlocks(content, hasContent, role)

		if role == "user" {
			blocks = SanitizeUserBlocks(blocks)
		}

		if len(blocks) == 0 {
			// 整条都是宿主注入的上下文时保留原文，其余空消息仍退化成原始
			// 报文诊断块。
			if injected := InjectedContextBlock(content, message); injected != nil {
				blocks = append(blocks, *injected)
			} else {
				blocks = append(blocks, EmptyMessageBlock("Claude Code", "content was empty after sanitization", message))
			}
		}

		id := fmt.Sprintf("claude-message-%d", index)
		if uuid, ok := jsonString(value, "uuid"); ok {
			id = uuid
		}

		var model *SessionModel
		if modelID, ok := jsonString(message, "model"); ok {
			// Claude 的转录只有模型名，没有 provider 字段。
			sessionModel := NewSessionModel("", modelID)
			model = &sessionModel
		}

		return &TimelineRecord{Message: &SessionMessage{
			ID:        id,
			Role:      role,
			Timestamp: timestamp,
			Blocks:    blocks,
			Model:     model,
			SessionID: someStr(sessionID),
		}}
	default:
		return &TimelineRecord{Event: &SessionEvent{
			ID:        fmt.Sprintf("claude-event-%d", index),
			Kind:      kind,
			Timestamp: timestamp,
			Summary:   SummarizeEvent(kind, value),
			Payload:   value,
			SessionID: someStr(sessionID),
		}}
	}
}

// claudeLocalCommandBlock：Claude Code 的本地命令记录——斜杠命令自身与它的
// 输出分别成块，提取可读文本给前端渲染（原始报文在会话文件里，界面不需要
// 再看 JSON）。
func claudeLocalCommandBlock(text string) *ContentBlock {
	if name, ok := claudeTagText(text, "command-name"); ok {
		args, _ := claudeTagText(text, "command-args")
		summary := name
		if args != "" {
			summary = name + " " + args
		}
		block := diagnosticBlock("local_command", summary, nil)
		return &block
	}

	for _, tag := range []string{"local-command-stdout", "local-command-stderr"} {
		if output, ok := claudeTagText(text, tag); ok && strings.TrimSpace(output) != "" {
			block := diagnosticBlock("local_command_output", output, nil)
			return &block
		}
	}

	return nil
}

// claudeTagText：取 <tag>…</tag> 之间的文本；标签不成对时不猜，返回 false。
func claudeTagText(text string, tag string) (string, bool) {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(text, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	offset := strings.Index(text[start:], close)
	if offset < 0 {
		return "", false
	}
	end := start + offset
	return strings.TrimSpace(text[start:end]), true
}

func parseClaudeMessageBlocks(content any, contentPresent bool, role string) []ContentBlock {
	if contentPresent {
		if text, ok := content.(string); ok {
			if role == "user" {
				// 本地命令记录（/exit 这类斜杠命令与其输出）既不是对话，
				// 也不该当噪音丢掉。
				if block := claudeLocalCommandBlock(text); block != nil {
					return []ContentBlock{*block}
				}

				if IsTransportMessage(text) {
					return []ContentBlock{}
				}
			}

			return []ContentBlock{{
				Kind:    "text",
				Text:    &text,
				Payload: nil,
			}}
		}
		if items, ok := content.([]any); ok {
			blocks := make([]ContentBlock, 0, len(items))
			for _, item := range items {
				kind, _ := jsonString(item, "type")
				if kind == "" {
					kind = "unknown"
				}
				// 图片块没有文本：把 source.data / media_type 还原成 data URL
				// 当正文，前端才能直接渲染。
				text := ""
				hasText := false
				if value, ok := jsonString(item, "text"); ok {
					text, hasText = value, true
				} else if value, ok := jsonString(item, "thinking"); ok {
					text, hasText = value, true
				} else if value, ok := jsonString(item, "content"); ok {
					text, hasText = value, true
				} else if kind == "image" || kind == "input_image" {
					if reference, ok := ImageReference(item); ok {
						text, hasText = reference, true
					}
				}

				callID := ""
				hasCallID := false
				if id, ok := jsonString(item, "id"); ok {
					callID, hasCallID = id, true
				} else if id, ok := jsonString(item, "tool_use_id"); ok {
					callID, hasCallID = id, true
				}

				blocks = append(blocks, ContentBlock{
					Kind:       kind,
					Text:       optionalString(text, hasText),
					ToolName:   strField(jsonString(item, "name")),
					ToolCallID: optionalString(callID, hasCallID),
					IsError:    jsonBoolPtr(item, "is_error"),
					Payload:    item,
				})
			}
			return blocks
		}
	}
	return []ContentBlock{UnsupportedContentBlock("Claude Code", content)}
}

// jsonBoolPtr：value[key].as_bool 的指针承载（缺失/非布尔 → nil）。
func jsonBoolPtr(value any, key string) *bool {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	flag, ok := object[key].(bool)
	if !ok {
		return nil
	}
	return &flag
}

// withExtension：对齐 Rust Path::with_extension——替换现有扩展名（首字符
// 的 '.' 属于文件名本身，不算分隔点）。"a.jsonl" → "a.meta.json"。
func withExtension(path string, ext string) string {
	name := filepath.Base(path)
	stem := name
	if index := strings.LastIndexByte(name, '.'); index > 0 {
		stem = name[:index]
	}
	return filepath.Join(filepath.Dir(path), stem+"."+ext)
}

// claudeExtractTitle：user 消息 content 里第一个可当标题的 text 块。
func claudeExtractTitle(message any) *string {
	content, hasContent := getJSONObjectPath(message, "content")
	if !hasContent {
		return nil
	}
	for _, block := range parseClaudeMessageBlocks(content, true, "user") {
		if block.Kind != "text" {
			continue
		}
		if block.Text == nil {
			continue
		}
		if candidate, ok := TitleCandidateFromText(*block.Text); ok {
			return &candidate
		}
	}
	return nil
}
