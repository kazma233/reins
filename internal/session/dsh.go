package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reins/internal/agents"
	"reins/internal/logger"
	"reins/internal/support"
)

// dsh.go：DSH（DeepSeek Harness）读取器。真差异声明：
//   - 分组走 catalog 声明的边集（subagent/catalog 事件的 childId），链断即
//     隐藏（孤儿子会话不展示），不经共享父链部件（GLOSSARY 孤儿策略词条）；
//   - index 失效用指纹（转录文件集合 + 归档账本时刻），timeline 失效用成员
//     mtime 最大值（引擎默认）——双口径是刻意差异；
//   - SummaryKind::Rows；row_usage/row_models 行级直取（单转录单会话）；
//   - marker 同 id 无 start/event 后缀，message/event payload 不对称；
//   - parse_agent_messages 有成员归属校验（check_agent_member）；
//   - index 锁文案无 "family" 字样（"DSH index"）；not-found 错误主语经
//     agents 清单派生（产品全名）。

// DshReader：static 前置。
var DshReader = NewFamilyReader[DshFamilyRow](DshSpec{}, DshSessionsRoot)

// DshRoot：env 解析的 dsh 根（DSH_HOME 重定向优先）。可用性看 ~/.dsh/sessions
// 而不是 ~/.dsh 本身；不读 .credentials.yaml 等其他内容。
func DshRoot() (string, error) {
	return SpecRoot(SourceDsh)
}

// DshSessionsRoot：会话根——root 下的 sessions 子目录。
func DshSessionsRoot() (string, error) {
	root, err := DshRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sessions"), nil
}

// DshHeader：转录首行的会话头。
type DshHeader struct {
	id              string
	createdAt       int64
	cwd             *string
	delegationDepth uint32
}

type catalogChild struct {
	ChildID string  `json:"child_id"`
	Label   *string `json:"label"`
}

// dshTranscript：单个转录解析出的全部展示数据；summary 与时间线共用这一份
// 扫描结果。
type dshTranscript struct {
	header          DshHeader
	messages        []SessionMessage
	events          []SessionEvent
	title           *string
	firstUserTitle  *string
	lastEventTime   int64
	usageTotal      *SessionTokenUsage
	models          []SessionModel
	usageBuckets    UsageHourBuckets
	catalogChildren []catalogChild
}

func dshReadTranscript(path string) (dshTranscript, error) {
	// 转录是逐帧 zstd（压缩可选），整份解出后统一按行解析。
	bytes, err := ReadFrames(path)
	if err != nil {
		return dshTranscript{}, err
	}
	// 尾帧截断可能切在多字节字符中间，损失地转成字符串而不是报错。
	text := string(bytes)

	values := make([]any, 0)
	for index, line := range strings.Split(text, "\n") {
		// Rust str::lines 语义：剥行尾 \r、空串整行按 trim 后跳过。
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		value, err := decodeJSONBytes([]byte(line))
		if err != nil {
			return dshTranscript{}, fmt.Errorf("Invalid DSH JSON at %s line %d: %w", path, index+1, err)
		}
		values = append(values, value)
	}

	if len(values) == 0 {
		return dshTranscript{}, fmt.Errorf("DSH transcript has no session header: %s", path)
	}
	if kind, _ := jsonType(values[0]); kind != "session" {
		return dshTranscript{}, fmt.Errorf("DSH transcript has no session header: %s", path)
	}
	header, err := dshParseHeader(values[0])
	if err != nil {
		return dshTranscript{}, err
	}
	timeline := dshBuildTimeline(header, values[1:])
	return timeline, nil
}

func dshParseHeader(value any) (DshHeader, error) {
	id := ""
	if text, ok := jsonString(value, "id"); ok && text != "" {
		id = text
	} else {
		return DshHeader{}, fmt.Errorf("DSH session header has no id")
	}

	createdAt, _ := jsonI64(value, "createdAt")
	cwd := (*string)(nil)
	if text, ok := jsonString(value, "cwd"); ok && text != "" {
		cwd = &text
	}
	depthRaw, _ := jsonU64(value, "delegationDepth")

	return DshHeader{id: id, createdAt: createdAt, cwd: cwd, delegationDepth: uint32(depthRaw)}, nil
}

// dshSurfaceRange：surface 节点携带的可折叠 seq 覆盖区间。
type dshSurfaceRange struct {
	low  int64
	high int64
}

// dshTimelineItems：装配中的时间线；replace 折叠 [startSeq,endSeq] 内的已有
// surface 节点（官方 compaction 语义），非 surface 事件不受影响。
type dshTimelineItems struct {
	messages []dshSurfaceItem[SessionMessage]
	events   []dshSurfaceItem[SessionEvent]
}

type dshSurfaceItem[T any] struct {
	rangeCovered *dshSurfaceRange
	item         T
}

func (items *dshTimelineItems) applyReplace(start int64, end int64) {
	outside := func(r *dshSurfaceRange) bool {
		return r == nil || r.high < start || r.low > end
	}
	kept := items.messages[:0]
	for _, entry := range items.messages {
		if outside(entry.rangeCovered) {
			kept = append(kept, entry)
		}
	}
	items.messages = kept
	keptEvents := items.events[:0]
	for _, entry := range items.events {
		if outside(entry.rangeCovered) {
			keptEvents = append(keptEvents, entry)
		}
	}
	items.events = keptEvents
}

func dshBuildTimeline(header DshHeader, events []any) dshTranscript {
	sessionID := header.id
	items := dshTimelineItems{}
	var title *string
	var firstUserTitle *string
	lastEventTime := header.createdAt
	var usageTotal *SessionTokenUsage
	usageBuckets := UsageHourBuckets{}
	catalogChildren := make([]catalogChild, 0)
	models := ModelTally{}
	// 每次请求的 request/context 声明本次调用用的模型与供应商；更早的版本只
	// 写 model/selection（切换时声明），两种都认，以后出现的为准。
	var currentModel *SessionModel

	for index, value := range events {
		kind, ok := jsonType(value)
		if !ok {
			kind = "unknown"
		}
		seq, hasSeq := jsonI64(value, "seq")
		time := header.createdAt
		if raw, ok := jsonI64(value, "time"); ok {
			time = raw
		}
		if time > lastEventTime {
			lastEventTime = time
		}
		data, _ := getJSONObjectPath(value, "data")
		if kind == "model/selection" || kind == "request/context" {
			if model, ok := jsonString(data, "model"); ok {
				provider, _ := jsonString(data, "provider")
				sessionModel := NewSessionModel(provider, model)
				currentModel = &sessionModel
			}
		}
		seqSuffix := fmt.Sprintf("%d", index)
		if hasSeq {
			seqSuffix = fmt.Sprintf("%d", seq)
		}
		messageID := func(prefix string) string {
			return fmt.Sprintf("dsh-%s-%s-%s", sessionID, prefix, seqSuffix)
		}
		surfaceOp, _ := getJSONObjectPath(value, "surfaceOp")

		switch kind {
		case "user/message":
			sourceKind := "unknown"
			if text, ok := jsonString(data, "source", "kind"); ok {
				sourceKind = text
			}
			if sourceKind == "user" {
				blocks := SanitizeUserBlocks(dshContentTextBlocks(data, "text"))
				if firstUserTitle == nil {
					for _, block := range blocks {
						if block.Text == nil {
							continue
						}
						if candidate, ok := TitleCandidateFromText(*block.Text); ok {
							firstUserTitle = &candidate
							break
						}
					}
				}
				if len(blocks) > 0 {
					message := SessionMessage{
						ID:        messageID("message"),
						Role:      "user",
						Timestamp: &time,
						Blocks:    blocks,
						SessionID: someStr(sessionID),
					}
					dshPushSurfaceMessage(&items, hasSeq, seq, surfaceOp, &message)
				}
			} else {
				// 合成注入（AGENTS.md/runtime-context/skill-catalog 等）不是
				// 人类输入，转事件展示，不进消息时间线。
				summary := sourceKind
				if text, ok := dshFirstContentText(data); ok {
					normalized := TruncateSummary(NormalizeTitle(text))
					summary = normalized
				}
				event := dshSurfaceEvent(messageID("event"), sourceKind, time, summary, value, sessionID)
				dshPushSurfaceEvent(&items, hasSeq, seq, surfaceOp, &event)
			}
		case "assistant/message":
			if usageValue, ok := getJSONObjectPath(data, "usage"); ok {
				usage := dshParseUsage(usageValue)
				MergeTokenUsage(&usageTotal, usage)
				MergeUsageEvent(usageBuckets, HourKey(time), sessionModelName(currentModel), usage)
			}
			if currentModel != nil {
				models.Record(currentModel.Provider, currentModel.Model)
			}
			blocks := dshAssistantBlocks(data)
			if len(blocks) > 0 {
				message := SessionMessage{
					ID:        messageID("message"),
					Role:      "assistant",
					Timestamp: &time,
					Blocks:    blocks,
					Model:     cloneSessionModel(currentModel),
					SessionID: someStr(sessionID),
				}
				dshPushSurfaceMessage(&items, hasSeq, seq, surfaceOp, &message)
			}
		case "tool/result":
			message, _ := getJSONObjectPath(data, "message")
			text := ""
			if blocks, ok := jsonArray(message, "content"); ok {
				texts := make([]string, 0)
				for _, block := range blocks {
					if blockType, _ := jsonString(block, "type"); blockType != "text" {
						continue
					}
					if blockText, ok := jsonString(block, "text"); ok {
						texts = append(texts, blockText)
					}
				}
				text = strings.Join(texts, "\n")
			}
			callID := ""
			hasCallID := false
			if id, ok := jsonString(message, "toolCallId"); ok {
				callID, hasCallID = id, true
			} else if id, ok := jsonString(message, "source", "callId"); ok {
				callID, hasCallID = id, true
			}
			var textPtr *string
			if text != "" {
				textPtr = &text
			}
			block := ContentBlock{
				Kind:       "function_call_output",
				Text:       textPtr,
				ToolCallID: optionalString(callID, hasCallID),
				IsError:    jsonBoolPtr(message, "isError"),
				Payload:    message,
			}
			result := SessionMessage{
				ID:        messageID("message"),
				Role:      "tool",
				Timestamp: &time,
				Blocks:    []ContentBlock{block},
				SessionID: someStr(sessionID),
			}
			dshPushSurfaceMessage(&items, hasSeq, seq, surfaceOp, &result)
		case "system/message", "developer/message":
			summary := kind
			if text, ok := dshMessageFirstText(data); ok {
				normalized := TruncateSummary(NormalizeTitle(text))
				summary = normalized
			}
			event := dshSurfaceEvent(messageID("event"), kind, time, summary, value, sessionID)
			dshPushSurfaceEvent(&items, hasSeq, seq, surfaceOp, &event)
		case "session/title":
			if value, ok := jsonString(data, "title"); ok && strings.TrimSpace(value) != "" {
				title = &value
			}
		case "subagent/catalog":
			if childID, ok := jsonString(data, "childId"); ok {
				var labelPtr *string
				if label, hasLabel := jsonString(data, "label"); hasLabel {
					labelPtr = &label
				}
				catalogChildren = append(catalogChildren, catalogChild{ChildID: childID, Label: labelPtr})
			}
		default:
			if dshIsIgnoredType(kind) {
				continue
			}
			if dshIsGenericEventType(kind) {
				event := dshSurfaceEvent(messageID("event"), kind, time, SummarizeEvent(kind, data), value, sessionID)
				items.events = append(items.events, dshSurfaceItem[SessionEvent]{item: event})
				continue
			}
			// 官方语义：未知 type 无 ignorable 标记应拒读；Reins 降级为事件
			// 展示而不是让整个来源不可用。
			if ignorable, _ := jsonBool(value, "ignorable"); !ignorable {
				event := dshSurfaceEvent(messageID("event"), kind, time, SummarizeEvent(kind, data), value, sessionID)
				items.events = append(items.events, dshSurfaceItem[SessionEvent]{item: event})
			}
		}
	}

	// 用户输入出现在该轮的 request/context 之前，按「由这一轮的回答服务」补
	// 归属。
	messages := make([]SessionMessage, 0, len(items.messages))
	for _, entry := range items.messages {
		messages = append(messages, entry.item)
	}
	FillUserMessageModels(messages)

	eventsOut := make([]SessionEvent, 0, len(items.events))
	for _, entry := range items.events {
		eventsOut = append(eventsOut, entry.item)
	}

	return dshTranscript{
		header:          header,
		messages:        messages,
		events:          eventsOut,
		title:           title,
		firstUserTitle:  firstUserTitle,
		lastEventTime:   lastEventTime,
		usageTotal:      usageTotal,
		models:          models.IntoModels(),
		usageBuckets:    usageBuckets,
		catalogChildren: catalogChildren,
	}
}

func sessionModelName(model *SessionModel) string {
	if model == nil {
		return ""
	}
	return model.Model
}

// dshSurfaceCoverage：surfaceOp 为 replace 时先折叠 [startSeq,endSeq] 内已有
// surface 节点（官方 compaction 语义）并返回新区间；append/缺省按自身 seq
// 入列，缺 seq 的节点无法参与折叠（返回 nil 区间）。
func dshSurfaceCoverage(items *dshTimelineItems, hasSeq bool, seq int64, surfaceOp any) *dshSurfaceRange {
	var covered *dshSurfaceRange
	if hasSeq {
		covered = &dshSurfaceRange{low: seq, high: seq}
	}
	if surfaceOp != nil {
		if op, ok := jsonString(surfaceOp, "op"); ok && op == "replace" {
			start, hasStart := jsonI64(surfaceOp, "startSeq")
			end, hasEnd := jsonI64(surfaceOp, "endSeq")
			if hasStart && hasEnd {
				folded := dshSurfaceRange{low: minInt64(start, end), high: maxInt64(start, end)}
				items.applyReplace(folded.low, folded.high)
				covered = &folded
			}
		}
	}
	return covered
}

// dshPushSurfaceMessage / dshPushSurfaceEvent：按折叠区间入列。
func dshPushSurfaceMessage(items *dshTimelineItems, hasSeq bool, seq int64, surfaceOp any, message *SessionMessage) {
	if message == nil {
		return
	}
	covered := dshSurfaceCoverage(items, hasSeq, seq, surfaceOp)
	items.messages = append(items.messages, dshSurfaceItem[SessionMessage]{rangeCovered: covered, item: *message})
}

func dshPushSurfaceEvent(items *dshTimelineItems, hasSeq bool, seq int64, surfaceOp any, event *SessionEvent) {
	if event == nil {
		return
	}
	covered := dshSurfaceCoverage(items, hasSeq, seq, surfaceOp)
	items.events = append(items.events, dshSurfaceItem[SessionEvent]{rangeCovered: covered, item: *event})
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// dshSurfaceEvent：record 是原始记录本身（含 seq/type 信封）——raw 页要能看
// 全，摘要由调用方从 record.data 里提取后传入。
func dshSurfaceEvent(id string, kind string, time int64, summary string, record any, sessionID string) SessionEvent {
	return SessionEvent{
		ID:        id,
		Kind:      kind,
		Timestamp: &time,
		Summary:   summary,
		Payload:   RecordPayload(record),
		SessionID: someStr(sessionID),
	}
}

// dshIsIgnoredType：纯边界/噪音/重复内容——tool/call 与 assistant/message 内
// 的 tool-call 重复，step 是步边界计数，delivery-accepted 是云同步投递确认。
func dshIsIgnoredType(kind string) bool {
	switch kind {
	case "step/start", "step/end", "turn/start", "tool/call",
		"agent/inbox/spliced", "session/title-llm-request", "request/header":
		return true
	}
	return strings.HasPrefix(kind, "session-log-deepseek/") ||
		strings.HasPrefix(kind, "llm/retry") ||
		strings.HasPrefix(kind, "subagent/")
}

func dshIsGenericEventType(kind string) bool {
	switch kind {
	case "turn/end", "permission/preset", "sandbox/mode", "approval/policy",
		"model/selection", "request/context":
		return true
	}
	return strings.HasPrefix(kind, "plan/") ||
		strings.HasPrefix(kind, "todo/") ||
		strings.HasPrefix(kind, "compaction/")
}

// dshParseUsage：usage 字段与 SessionTokenUsage 官方口径 1:1（input=未命中
// 输入），无换算。
func dshParseUsage(usage any) SessionTokenUsage {
	input, _ := jsonU64(usage, "inputTokens")
	output, _ := jsonU64(usage, "outputTokens")
	cacheRead, _ := jsonU64(usage, "cacheReadTokens")
	cacheWrite, _ := jsonU64(usage, "cacheWriteTokens")
	return SessionTokenUsage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}

// dshContentTextBlocks：user/message 的 content 块——本机只见 text；其余类型
// 走 unsupported 降级。
func dshContentTextBlocks(data any, kind string) []ContentBlock {
	blocks, ok := jsonArray(data, "content")
	if !ok {
		return []ContentBlock{}
	}
	collection := make([]ContentBlock, 0, len(blocks))
	for _, block := range blocks {
		blockType, _ := jsonString(block, "type")
		if blockType == "text" {
			text, hasText := jsonString(block, "text")
			collection = append(collection, ContentBlock{
				Kind:    kind,
				Text:    optionalString(text, hasText),
				Payload: nil,
			})
		} else {
			collection = append(collection, UnsupportedBlock("DSH", block))
		}
	}
	return collection
}

func dshFirstContentText(data any) (string, bool) {
	blocks, ok := jsonArray(data, "content")
	if !ok {
		return "", false
	}
	for _, block := range blocks {
		if blockType, _ := jsonString(block, "type"); blockType != "text" {
			continue
		}
		if text, ok := jsonString(block, "text"); ok {
			return text, true
		}
	}
	return "", false
}

func dshMessageFirstText(data any) (string, bool) {
	blocks, ok := jsonArray(data, "message", "content")
	if !ok {
		return "", false
	}
	for _, block := range blocks {
		if blockType, _ := jsonString(block, "type"); blockType != "text" {
			continue
		}
		if text, ok := jsonString(block, "text"); ok {
			return text, true
		}
	}
	return "", false
}

// dshAssistantBlocks：assistant 消息——reasoning→thinking、text→text、
// tool-call→function_call（arguments 是 JSON 字符串，原样作 text，
// payload.input 放解析结果）；空 reasoning 是流式占位，没有展示价值，跳过。
func dshAssistantBlocks(data any) []ContentBlock {
	blocks, ok := jsonArray(data, "message", "content")
	if !ok {
		return []ContentBlock{}
	}
	result := make([]ContentBlock, 0)
	for _, block := range blocks {
		blockType, _ := jsonString(block, "type")
		switch blockType {
		case "reasoning":
			if text, ok := jsonString(block, "text"); ok && text != "" {
				result = append(result, ContentBlock{Kind: "thinking", Text: &text})
			}
		case "text":
			if text, ok := jsonString(block, "text"); ok && text != "" {
				result = append(result, ContentBlock{Kind: "text", Text: &text})
			}
		case "tool-call":
			raw, _ := jsonString(block, "arguments")
			// 模型中断会留下截断的 arguments；按 {raw} 降级，不让单条坏数据
			// 毁掉整个会话。
			input, err := decodeJSONBytes([]byte(raw))
			if err != nil {
				input = map[string]any{"raw": raw}
			}
			var textPtr *string
			if raw != "" {
				textPtr = &raw
			}
			result = append(result, ContentBlock{
				Kind:       "function_call",
				Text:       textPtr,
				ToolName:   strField(jsonString(block, "name")),
				ToolCallID: strField(jsonString(block, "id")),
				Payload:    map[string]any{"input": input},
			})
		default:
			result = append(result, UnsupportedBlock("DSH", block))
		}
	}
	return result
}

// --- 目录扫描 ---

// dshTranscriptGeneration：会话文件名 session[.v{N}].jsonl[.zstd]——v0 不带
// 版本段（session.jsonl），压缩可选；同目录多代共存取最高代，先比版本号再
// 让压缩形态优先。
func dshTranscriptGeneration(fileName string) (uint32, bool, bool) {
	rest, found := strings.CutPrefix(fileName, "session")
	if !found {
		return 0, false, false
	}

	version := uint32(0)
	if versionRest, hasVersion := strings.CutPrefix(rest, ".v"); hasVersion {
		digits, _, found := strings.Cut(versionRest, ".")
		parsed, ok := parseUint32(digits)
		if !found || !ok {
			return 0, false, false
		}
		version = parsed
		rest = versionRest[len(digits):]
	}

	// 版本段之后必须正好是后缀，不多不少。
	switch rest {
	case ".jsonl":
		return version, false, true
	case DshCompressedSuffix:
		return version, true, true
	default:
		return 0, false, false
	}
}

func parseUint32(text string) (uint32, bool) {
	if text == "" {
		return 0, false
	}
	value := uint32(0)
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		digit := uint32(ch - '0')
		next := value*10 + digit
		if next < value {
			return 0, false
		}
		value = next
	}
	return value, true
}

// DshSessionTranscriptPathsAt：usage 统计的 CachedJsonl 采集按实例 scan_root
// 发现转录，与列表同一规则。
func DshSessionTranscriptPathsAt(root string) ([]string, error) {
	if _, err := os.Stat(root); err != nil {
		return []string{}, nil
	}
	type latestEntry struct {
		generation uint32
		compressed bool
		path       string
	}
	latest := map[string]latestEntry{}

	// min_depth(3).max_depth(3)：root/<bucket>/<session>/session 文件。
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		depth := dshDepthBelow(root, path)
		if depth != 3 || !entry.Type().IsRegular() {
			return nil
		}
		name := entry.Name()
		generation, compressed, ok := dshTranscriptGeneration(name)
		if !ok {
			return nil
		}
		directory := filepath.Dir(path)
		current, seen := latest[directory]
		if seen {
			// 新条目必须严格大于当前才替换（(generation, compressed) <= 当前
			// 则保持）。
			if generation < current.generation || (generation == current.generation && !compressed && current.compressed) {
				return nil
			}
			if generation == current.generation && compressed == current.compressed {
				return nil
			}
		}
		latest[directory] = latestEntry{generation: generation, compressed: compressed, path: path}
		return nil
	})
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(latest))
	for _, entry := range latest {
		paths = append(paths, entry.path)
	}
	sort.Strings(paths)
	return paths, nil
}

// dshDepthBelow：path 相对 root 的层级（root 本身为 0）。
func dshDepthBelow(root string, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// --- 归档集 ---

// dshArchiveRegistryPath：归档是 storages/workspace.json 里的
// global.archivedSessionIds——归档只写这份账本，转录文件原样留在 sessions
// 下，所以列表必须显式读它，否则归档会话与未归档会话在磁盘上完全同形。
func dshArchiveRegistryPath(sessionsRoot string) string {
	return filepath.Join(filepath.Dir(sessionsRoot), "storages", "workspace.json")
}

// dshArchivedSessionIDs：账本缺失或字段缺失都是合法状态（该来源还没有归档
// 动作，老数据也可能没有这个后加的字段），此时归档集为空；读取失败只降级
// 归档角标，不影响列表本身。
func dshArchivedSessionIDs(sessionsRoot string) (map[string]bool, error) {
	path := dshArchiveRegistryPath(sessionsRoot)
	contents, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		supportLogf("dsh archive registry unreadable at %s: %v", path, err)
		return map[string]bool{}, nil
	}

	value, err := decodeJSONBytes(contents)
	if err != nil {
		supportLogf("dsh archive registry unparsable at %s: %v", path, err)
		return map[string]bool{}, nil
	}

	ids := map[string]bool{}
	if list, ok := jsonArray(value, "global", "archivedSessionIds"); ok {
		for _, item := range list {
			if id, ok := item.(string); ok {
				ids[id] = true
			}
		}
	}
	return ids, nil
}

// supportLogf：reader 内部的记日志文案（与 Rust log_error 等价）。
func supportLogf(format string, args ...any) {
	logger.Error(fmt.Sprintf(format, args...))
}

// dshArchiveRegistryStamp：归档集的变更时刻，参与索引指纹（归档不改转录
// 文件，mtime 指纹看不见它）。
func dshArchiveRegistryStamp(sessionsRoot string) int64 {
	stamp, err := support.FileModifiedTimestampMillis(dshArchiveRegistryPath(sessionsRoot))
	if err != nil {
		return 0
	}
	return stamp
}

// --- family 索引 ---

// DshSessionRow：单转录行。
type DshSessionRow struct {
	SessionID       string             `json:"session_id"`
	DelegationDepth uint32             `json:"delegation_depth"`
	Title           string             `json:"title"`
	Cwd             *string            `json:"cwd"`
	Path            string             `json:"path"`
	CreatedAt       int64              `json:"created_at"`
	UpdatedAt       int64              `json:"updated_at"`
	TokenUsage      *SessionTokenUsage `json:"token_usage"`
	Models          []SessionModel     `json:"models"`
	CatalogChildren []catalogChild     `json:"catalog_children"`
	Archived        bool               `json:"archived"`
}

// DshFamilyRow：父链行——父日志 catalog 声明的关系；root 自身 parent 为空。
type DshFamilyRow struct {
	Row DshSessionRow `json:"row"`
	// ChainRootID：族根 id（json 键 family_root_id）。
	ChainRootID string  `json:"family_root_id"`
	ParentID    *string `json:"parent_id"`
	Label       *string `json:"label"`
}

// DshFamily：dsh 族。
type DshFamily = Family[DshFamilyRow]

func (r DshFamilyRow) MemberPath() string       { return r.Row.Path }
func (r DshFamilyRow) FamilyRootID() string     { return r.ChainRootID }
func (r DshFamilyRow) MemberID() string         { return r.Row.SessionID }
func (r DshFamilyRow) MemberCreatedAt() *int64  { return &r.Row.CreatedAt }
func (r DshFamilyRow) MemberUpdatedAt() *int64  { return &r.Row.UpdatedAt }
func (r DshFamilyRow) MemberTieBreaker() string { return r.Row.SessionID }

var (
	_ FamilyRow = DshFamilyRow{}
	_ Reader    = (*FamilyReader[DshFamilyRow])(nil)
)

// DshSpec：dsh 的真差异声明。
type DshSpec struct{}

func (DshSpec) App() SourceApp                 { return SourceDsh }
func (DshSpec) Label() string                  { return "DSH" }
func (DshSpec) SummaryKind() SummaryKind       { return SummaryRows }
func (DshSpec) OverviewCounts() OverviewCounts { return OverviewTimeline }

// ScanRoot：传入的 root 就是 sessions 目录，不再下钻子目录。
func (DshSpec) ScanRoot(root string) string { return root }

func (DshSpec) ListRows(scanRoot string, _ *RowsDb) ([]DshFamilyRow, error) {
	paths, err := DshSessionTranscriptPathsAt(scanRoot)
	if err != nil {
		return nil, err
	}
	archived, err := dshArchivedSessionIDs(scanRoot)
	if err != nil {
		return nil, err
	}
	rows, err := ScanFiles(paths, func(path string) (DshSessionRow, error) {
		transcript, err := dshReadTranscript(path)
		if err != nil {
			return DshSessionRow{}, err
		}
		// 归档集按 header 里的会话 id 匹配（目录名只是提示）。
		return dshSessionRow(path, transcript, archived[transcript.header.id]), nil
	}, RowSkipLogged, "DSH")
	if err != nil {
		return nil, err
	}

	// scan_files 只表达单文件解析失败策略；同 id 双文件无法确定 family
	// 归属，在枚举后自行检查。
	seen := map[string]bool{}
	familyRows := make([]DshFamilyRow, 0, len(rows))
	for i := range rows {
		row := rows[i]
		if seen[row.SessionID] {
			return nil, fmt.Errorf("Conflicting DSH session ids for %s", row.Path)
		}
		seen[row.SessionID] = true
		familyRows = append(familyRows, DshFamilyRow{
			ChainRootID: row.SessionID,
			ParentID:    nil,
			Label:       nil,
			Row:         row,
		})
	}
	return familyRows, nil
}

func (DshSpec) GroupFamilies(rows []DshFamilyRow) ([]DshFamily, error) {
	byID := make(map[string]DshSessionRow, len(rows))
	for i := range rows {
		byID[rows[i].Row.SessionID] = rows[i].Row
	}
	return dshBuildFamilies(byID)
}

// IndexFreshness：dsh 的双口径——index 失效用指纹（文件集合增删也触发重建），
// timeline 失效用成员 mtime 最大值（引擎默认）。这是现状的刻意差异，不是
// 遗漏。归档只改 workspace.json 不改转录，指纹必须把这份账本的时刻一起计入，
// 否则归档/取消归档后角标要等到转录文件自己变化才更新。
func dshIndexFingerprint(scanRoot string) (Freshness, error) {
	paths, err := DshSessionTranscriptPathsAt(scanRoot)
	if err != nil {
		return Freshness{}, err
	}
	fingerprint, err := SessionsFingerprint(paths)
	if err != nil {
		return Freshness{}, err
	}
	return FingerprintFreshness(fmt.Sprintf("%s\narchive:%d", fingerprint, dshArchiveRegistryStamp(scanRoot))), nil
}

func (DshSpec) AgentName(row DshFamilyRow) string {
	if row.Label != nil && strings.TrimSpace(*row.Label) != "" {
		return *row.Label
	}
	return row.Row.Title
}

// markerShape：message/event 同 id（无 start/event 后缀），payload 不对称：
// event 不带 transcript_path。payload.type=subagent_started 是前端
// subagent-group 分组契约。
func (DshSpec) markerShape(memberID string, row DshFamilyRow) MarkerShape {
	return MarkerShape{
		MessageID: fmt.Sprintf("dsh-subagent-%s", memberID),
		EventID:   fmt.Sprintf("dsh-subagent-%s", memberID),
		MessageExtras: map[string]any{
			"transcript_path":   row.Row.Path,
			"parent_session_id": row.ParentID,
		},
		EventExtras: map[string]any{
			"parent_session_id": row.ParentID,
		},
	}
}

func (DshSpec) AgentLabel(family DshFamily, row DshFamilyRow) FamilyAgentLabel {
	if row.Row.SessionID == family.Root.Row.SessionID {
		return FamilyAgentLabel{Kind: AgentLabelRoot}
	}
	name := row.Row.Title
	if row.Label != nil {
		name = *row.Label
	}
	return FamilyAgentLabel{Kind: AgentLabelChild, Name: name}
}

func (DshSpec) Hooks() *FamilyHooks[DshFamilyRow] {
	return &FamilyHooks[DshFamilyRow]{
		// 现状 index 锁文案无 "family" 字样，逐字保留。
		IndexLockLabel: func(string) string { return "DSH index" },
		// not-found 错误主语经 agents 清单派生（产品全名）。
		DisplayLabel:   func(string) string { return dshDisplayLabel() },
		IndexFreshness: dshIndexFingerprint,
		SummaryFromRows: func(root DshFamilyRow) SessionSummary {
			return dshSessionSummary(root.Row)
		},
		RowUsage:  func(row DshFamilyRow) *SessionTokenUsage { return row.Row.TokenUsage },
		RowModels: func(row DshFamilyRow) []SessionModel { return row.Row.Models },
		Marker:    func(memberID string, row DshFamilyRow) MarkerShape { return (DshSpec{}).markerShape(memberID, row) },
		// parse_agent_messages 的成员归属校验（dsh 专用）。
		CheckAgentMember: func(family DshFamily, agentSessionID string) error {
			for i := range family.Members {
				if family.Members[i].Row.SessionID == agentSessionID {
					return nil
				}
			}
			return fmt.Errorf("DSH subagent does not belong to this session")
		},
	}
}

// LoadMembers：双半共用一次解码——转录是多帧 zstd，原先 messages/events 两
// 个 loader 对同一文件各完整解码一遍。
func (DshSpec) LoadMembers(_ string, members []DshFamilyRow) ([]MemberTimeline, error) {
	timelines := make([]MemberTimeline, 0, len(members))
	for i := range members {
		transcript, err := dshReadTranscript(members[i].Row.Path)
		if err != nil {
			return nil, err
		}
		timelines = append(timelines, MemberTimeline{Messages: transcript.messages, Events: transcript.events})
	}
	return timelines, nil
}

// dshDisplayLabel：not-found 错误主语（产品全名）。
func dshDisplayLabel() string {
	spec := agents.SpecBySourceApp(SourceDsh)
	if spec == nil {
		panic("dsh registered in AgentSpecs")
	}
	return spec.Label
}

// dshSessionRow：扫描行装配。
func dshSessionRow(path string, transcript dshTranscript, archived bool) DshSessionRow {
	title := transcript.header.id
	if transcript.title != nil {
		title = *transcript.title
	} else if transcript.firstUserTitle != nil {
		title = *transcript.firstUserTitle
	}
	normalized := NormalizeTitle(title)
	if normalized != "" {
		title = normalized
	}

	return DshSessionRow{
		SessionID:       transcript.header.id,
		DelegationDepth: transcript.header.delegationDepth,
		Title:           title,
		Cwd:             transcript.header.cwd,
		CreatedAt:       transcript.header.createdAt,
		UpdatedAt:       maxInt64(transcript.header.createdAt, transcript.lastEventTime),
		Path:            path,
		TokenUsage:      transcript.usageTotal,
		Models:          transcript.models,
		CatalogChildren: transcript.catalogChildren,
		Archived:        archived,
	}
}

// dshFamilyRootIDOf：从 catalog 建立的父子链向上找 family 根；链断（父文件
// 缺失）或成环时返回空串，对应孤儿子会话——v1 隐藏，无处挂靠的子会话不单独
// 展示。
func dshFamilyRootIDOf(id string, owners map[string]dshOwner) string {
	owner, ok := owners[id]
	if !ok {
		return ""
	}
	current := owner.parentID
	visited := map[string]bool{id: true}
	for {
		if visited[current] {
			return ""
		}
		visited[current] = true
		next, ok := owners[current]
		if !ok {
			return current
		}
		current = next.parentID
	}
}

// dshOwner：父日志 catalog 对子会话的声明：父 id + 可选 label。
type dshOwner struct {
	parentID string
	label    *string
}

func dshBuildFamilies(rows map[string]DshSessionRow) ([]DshFamily, error) {
	owners := map[string]dshOwner{}
	for sessionID, row := range rows {
		for _, child := range row.CatalogChildren {
			owner := dshOwner{parentID: sessionID, label: child.Label}
			previous, exists := owners[child.ChildID]
			if exists {
				// 同一父会话重复声明同一子会话（continuable 模式）是正常的。
				if previous.parentID != owner.parentID {
					return nil, fmt.Errorf("Conflicting DSH subagent ownership for %s", child.ChildID)
				}
				continue
			}
			owners[child.ChildID] = owner
		}
	}

	roots := map[string]DshFamilyRow{}
	children := map[string][]DshFamilyRow{}
	for _, row := range rows {
		rootID := dshFamilyRootIDOf(row.SessionID, owners)
		// 有 catalog 链时以链为准；无链时 delegationDepth==0 才是根，其余为
		// 孤儿子会话，不展示。
		isRoot := false
		if rootID != "" {
			isRoot = rootID == row.SessionID
		} else {
			_, owned := owners[row.SessionID]
			isRoot = row.DelegationDepth == 0 && !owned
		}
		owner := owners[row.SessionID]
		familyRow := DshFamilyRow{
			ChainRootID: row.SessionID,
			ParentID:    nil,
			Label:       nil,
			Row:         row,
		}
		if owner != (dshOwner{}) {
			parentID := owner.parentID
			familyRow.ParentID = &parentID
			if owner.label != nil {
				label := *owner.label
				familyRow.Label = &label
			}
		}
		if isRoot {
			roots[row.SessionID] = familyRow
		} else if rootID != "" {
			child := familyRow
			child.ChainRootID = rootID
			children[rootID] = append(children[rootID], child)
		}
	}

	// 与 Rust HashMap 一样无序；下游 BuildFamilyIndex 的排序把它归一。
	families := make([]DshFamily, 0, len(roots))
	for rootID, root := range roots {
		members := []DshFamilyRow{root}
		members = append(members, children[rootID]...)
		delete(children, rootID)
		families = append(families, DshFamily{Root: root, Members: members})
	}
	return families, nil
}

// dshSessionSummary：行直构摘要。
func dshSessionSummary(row DshSessionRow) SessionSummary {
	return SessionSummary{
		SourceApp:       SourceDsh,
		SourceSessionID: row.SessionID,
		Title:           row.Title,
		Cwd:             row.Cwd,
		GitBranch:       nil,
		TranscriptPath:  row.Path,
		CreatedAt:       someI64(row.CreatedAt),
		UpdatedAt:       someI64(row.UpdatedAt),
		TokenUsage:      row.TokenUsage,
		Models:          row.Models,
		Archived:        row.Archived,
		// dsh 的 zstd 是该来源的默认存储形态（每个转录都压缩），标注成角标
		// 没有区分度，所以压缩位恒假；只有归档是「偏离常态」的状态。
		Compressed: false,
	}
}

// DshUsageHours：用量曲线的小时桶——assistant/message.usage 按事件 time 归
// 本地时区小时。
func DshUsageHours(path string) (UsageHourBuckets, error) {
	transcript, err := dshReadTranscript(path)
	if err != nil {
		return nil, err
	}
	return transcript.usageBuckets, nil
}
