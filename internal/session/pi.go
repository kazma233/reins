package session

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reins/internal/support"
)

// cryptoRandRead：crypto/rand.Read 的域内别名（按契约填满整个切片、永不
// 失败）。
func cryptoRandRead(b []byte) (int, error) { return rand.Read(b) }

// pi.go：Pi 读取器（bespoke：不走 ReaderEngine）。真差异：
//   - 列表摘要必须保持按需懒加载（条目不带 summary，身份来自扫描时的
//     header），引擎的 list_entries 会强制整族解析——所以索引是 family 归族
//     形态但列表不聚合；
//   - timeline 与 summary 复用引擎部件：DualHalfCache / SummaryMemCache，
//     索引用指纹门控（root 就是实例本身，键只剩指纹）；
//   - 会话根有官方 sessionDir 链（--session-dir > PI_CODING_AGENT_SESSION_DIR
//     > settings.json.sessionDir），目录可能被重定向出 agent 根；subagent-tasks
//     跟 agent 根走（tasks 目录不随 sessionDir）；
//   - subagent-tasks runner（task.json + output.jsonl 事件流）不是会话文件，
//     不能独立成条目——父缺失（已删）的 run 直接跳过；
//   - marker 注入条件：family 成员里 id ≠ root 的子代理会话（扩展派生，
//     「有 session_info 名」即判据）；fork 子会话（无名）不归族、保持独立；
//   - v1/v2 文件在内存里按官方迁移规则升级（只读浏览，不写回磁盘）。

// piReaderInstance：按 sessions 根构造的读取器——static 前置只存「根解析
// 函数」，实例按 root 缓存在 slot 里，换根即整实例重建（测试切 home 不需要
// 手工清缓存，也不再有进程级可变状态）。不叫 piReader：该名已被本文件的
// bespoke 读取器 struct PiReader 占用。
type piReaderInstance struct {
	mu   sync.Mutex
	slot *piSlot
}

type piSlot struct {
	root   string
	reader *PiReader
}

// PiReaderInstance 全局实例（注册表引用）。
var PiReaderInstance = &piReaderInstance{}

var _ Reader = (*piReaderInstance)(nil)

// piReader：每次调用重新解析根（与迁移前一致），根变了就重建实例。
func (b *piReaderInstance) current() (*PiReader, error) {
	root, err := PiSessionsRoot()
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.slot != nil && b.slot.root == root {
		return b.slot.reader, nil
	}
	// 换根重建：旧实例的持久缓存连接显式关闭（无 Drop 语义）。
	if b.slot != nil {
		b.slot.reader.Close()
	}

	// 持久缓存目录随实例解析（测试直构时经参数注入，脱离进程 env）。
	storeDir, err := reinsStoreDir()
	if err != nil {
		return nil, err
	}
	reader := newPiReader(root, storeDir)
	b.slot = &piSlot{root: root, reader: reader}
	return reader, nil
}

// PiReader：一个根下的缓存与读取逻辑。索引是 family 归族形态（bespoke）；
// timeline → DualHalfCache；index → 指纹门控；summary → SummaryMemCache
// （内存 → SQLite → 整文件解析三级回填）。
type PiReader struct {
	sessionsRoot string
	timeline     *DualHalfCache
	mu           sync.Mutex
	cachedIndex  *piCachedIndex
	summaries    *SummaryMemCache
}

type piCachedIndex struct {
	fingerprint string
	index       FamilyIndex[piRow]
}

// Close 释放 pi 实例持有的持久摘要连接。piReaderInstance 换根重建丢弃旧实例前
// 必须调用（无 Drop 语义，对齐 ReaderEngine.Close 的纪律）。
func (r *PiReader) Close() {
	if r.summaries != nil {
		r.summaries.Close()
	}
}

// piRow：索引行。
type piRow struct {
	path      string
	id        string
	createdAt *int64
	updatedAt int64
	// header.parentSession：pi 原生 fork/branch 与各代 subagent 扩展派生的
	// 子会话都写它，值是父会话文件的绝对路径。TaskRun 成员不走路径反查，
	// 用 parent_session_id 直连。
	parentPath      *string
	parentSessionID *string
	// 扩展派生的子代理会话创建后立即 setSessionName；两代扩展的命名不同
	// （新版 "<type>#<id8>"、旧版 "subagent-<agent>-<runId>-1"），但都有名，
	// 而 fork 子会话没有 session_info——「有名字」即扩展派生的通用判据。
	agentName *string
	kind      piMemberKind
}

// piMemberKind：成员数据形态。pi 会话文件走整文件解析；subagent-tasks
// runner 是事件流，时间线与摘要都从流合成。
type piMemberKind struct {
	isTaskRun  bool
	transcript string
}

func (r piRow) MemberPath() string      { return r.path }
func (r piRow) FamilyRootID() string    { return r.id }
func (r piRow) MemberID() string        { return r.id }
func (r piRow) MemberCreatedAt() *int64 { return r.createdAt }

func (r piRow) MemberUpdatedAt() *int64 { return &r.updatedAt }

// member_tie_breaker：路径拼写。
func (r piRow) MemberTieBreaker() string { return r.path }

var (
	_ FamilyRow = piRow{}
	_ Reader    = (*piReaderInstance)(nil)
)

// piHeader：会话文件首行。
type piHeader struct {
	id            string
	timestamp     string
	cwd           string
	parentSession *string
	version       int64
	value         any
}

// piEntry：会话文件的一条 entry。
type piEntry struct {
	id        string
	parentID  *string
	timestamp *int64
	value     any
}

// newPiReader：reader 级测试的直构入口（root 与持久缓存目录都由参数注入，
// 脱离进程 env）——与引擎系的 NewReaderEngine(spec, root, storeDir) 同待遇；
// 生产路径经 piReaderInstance.current 注入 HOME 解析的库目录。
func newPiReader(sessionsRoot string, storeDir string) *PiReader {
	return &PiReader{
		sessionsRoot: sessionsRoot,
		timeline:     NewDualHalfCache("Pi timeline"),
		summaries:    NewSummaryMemCache(SourcePi, "Pi summary", SummaryDbAt(storeDir)),
	}
}

// ClearCache：reader 级清缓存（timeline + summaries + index）。
func (r *PiReader) ClearCache() error {
	r.timeline.Clear()
	r.summaries.Clear()
	r.mu.Lock()
	r.cachedIndex = nil
	r.mu.Unlock()
	return nil
}

// familyIndex：索引——指纹就是新鲜度（root 由实例本身表达，不再另存
// source_key）。指纹覆盖文件集合（路径+mtime+大小），子会话文件被外部删除
// 时索引能失效；subagent-tasks 在 agent 根下而不在 sessions 根内，它的产物
// 单独进指纹。
func (r *PiReader) familyIndex() (FamilyIndex[piRow], error) {
	files := support.EnumerateJSONLFiles(r.sessionsRoot)
	fingerprintFiles := make([]string, len(files))
	copy(fingerprintFiles, files)
	taskRuns, err := piScanTaskRuns(piTaskRunsRoot(), &fingerprintFiles)
	if err != nil {
		return FamilyIndex[piRow]{}, err
	}
	fingerprint, err := SessionsFingerprint(fingerprintFiles)
	if err != nil {
		return FamilyIndex[piRow]{}, err
	}

	r.mu.Lock()
	if r.cachedIndex != nil && r.cachedIndex.fingerprint == fingerprint {
		index := r.cachedIndex.index
		r.mu.Unlock()
		return index, nil
	}
	r.mu.Unlock()

	rows := make([]piRow, 0)
	for _, path := range files {
		if row, ok := piScanRow(path); ok {
			rows = append(rows, row)
		}
	}
	rows = append(rows, taskRuns...)
	index := piBuildFamilyIndex(rows)

	r.mu.Lock()
	r.cachedIndex = &piCachedIndex{fingerprint: fingerprint, index: index}
	r.mu.Unlock()
	return index, nil
}

func (r *PiReader) familyForID(sourceSessionID string) (Family[piRow], error) {
	index, err := r.familyIndex()
	if err != nil {
		return Family[piRow]{}, err
	}
	family, ok := index.FamilyForID(sourceSessionID)
	if !ok {
		return Family[piRow]{}, fmt.Errorf("Could not find Pi session %s", sourceSessionID)
	}
	return family, nil
}

// memberSummary：三级摘要——内存 → SQLite 持久层 → 整文件解析，回填逐级
// 进行（引擎部件）。单成员口径（无 family 聚合），family 级由 familySummary
// 在其上组装。
func (r *PiReader) memberSummary(row piRow) (SessionSummary, error) {
	if !row.kind.isTaskRun {
		return r.summaries.Load(row.path, piParseFullSummary)
	}
	// run 转录不是 session 文件，持久层按 (路径, mtime) 键控同样适用。
	return r.summaries.Load(row.kind.transcript, func(path string) (SessionSummary, error) {
		return piParseRunSummary(row, path)
	})
}

// memberTimeline：单成员双半时间线。
func (r *PiReader) memberTimeline(row piRow) (MemberTimeline, error) {
	if !row.kind.isTaskRun {
		mtime, err := piFileMtime(row.path)
		if err != nil {
			return MemberTimeline{}, err
		}
		return r.timeline.Load(support.PathKey(row.path), mtime, func() ([]SessionMessage, []SessionEvent, error) {
			document, err := piReadDocument(row.path)
			if err != nil {
				return nil, nil, err
			}
			messages, events := piBuildTimeline(document.header, document.entries)
			return messages, events, nil
		})
	}
	mtime, err := piFileMtime(row.kind.transcript)
	if err != nil {
		return MemberTimeline{}, err
	}
	return r.timeline.Load(support.PathKey(row.kind.transcript), mtime, func() ([]SessionMessage, []SessionEvent, error) {
		return piBuildRunTimeline(row.id, row.kind.transcript)
	})
}

func (r *PiReader) familySummary(family Family[piRow]) (SessionSummary, error) {
	summary, err := r.memberSummary(family.Root)
	if err != nil {
		return SessionSummary{}, err
	}
	family.ApplySummaryAggregates(&summary)
	summary.TokenUsage = r.familyUsage(family)
	summary.Models = r.familyModels(family)
	return summary, nil
}

// familyUsage：family 总用量按成员求和；成员摘要走三级缓存，打开会话详情时
// 才解析。
func (r *PiReader) familyUsage(family Family[piRow]) *SessionTokenUsage {
	return family.SumTokenUsage(func(row piRow) *SessionTokenUsage {
		summary, err := r.memberSummary(row)
		if err != nil {
			return nil
		}
		return summary.TokenUsage
	})
}

func (r *PiReader) familyModels(family Family[piRow]) []SessionModel {
	tally := ModelTally{}
	for i := range family.Members {
		if summary, err := r.memberSummary(family.Members[i]); err == nil {
			tally.Absorb(summary.Models)
		}
	}
	return tally.IntoModels()
}

// familyTimeline：family 双半时间线——主会话 + 各子代理会话（子代理先注入
// marker 入口），全部按 (timestamp, id) 终排，marker 与成员消息按时间交错。
func (r *PiReader) familyTimeline(family Family[piRow]) ([]SessionMessage, []SessionEvent, error) {
	messages := make([]SessionMessage, 0)
	events := make([]SessionEvent, 0)
	for i := range family.Members {
		row := family.Members[i]
		agentMember := piIsAgentMember(family, row)
		if agentMember {
			messages = append(messages, r.markerMessage(row))
			events = append(events, r.markerEvent(row))
		}
		timeline, err := r.memberTimeline(row)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, timeline.Messages...)
		// 归族成员的血缘已由 family 表达；独立 root（fork、父缺失）保留
		// parent_session 事件，那是它唯一的血缘表达。
		for _, event := range timeline.Events {
			if agentMember && event.Kind == "parent_session" {
				continue
			}
			events = append(events, event)
		}
	}

	sort.SliceStable(messages, func(i, j int) bool {
		if c := compareOptionalInt64(messages[i].Timestamp, messages[j].Timestamp); c != 0 {
			return c < 0
		}
		return messages[i].ID < messages[j].ID
	})
	sort.SliceStable(events, func(i, j int) bool {
		if c := compareOptionalInt64(events[i].Timestamp, events[j].Timestamp); c != 0 {
			return c < 0
		}
		return events[i].ID < events[j].ID
	})
	return messages, events, nil
}

func (r *PiReader) markerMessage(row piRow) SessionMessage {
	title := piMemberTitle(row)
	text := fmt.Sprintf("Sub-agent session: %s\n%s", title, row.id)
	return SessionMessage{
		ID:        fmt.Sprintf("pi-subagent-start-%s", row.id),
		Role:      "assistant",
		Timestamp: row.createdAt,
		Blocks: []ContentBlock{{
			Kind: "output_text",
			Text: &text,
			Payload: map[string]any{
				"type":            "subagent_started",
				"session_id":      row.id,
				"title":           title,
				"transcript_path": row.path,
			},
		}},
		SessionID: someStr(row.id),
	}
}

func (r *PiReader) markerEvent(row piRow) SessionEvent {
	title := piMemberTitle(row)
	return SessionEvent{
		ID:        fmt.Sprintf("pi-subagent-event-%s", row.id),
		Kind:      "subagent_started",
		Timestamp: row.createdAt,
		Summary:   fmt.Sprintf("Sub-agent session started: %s", title),
		Payload: map[string]any{
			"session_id":      row.id,
			"title":           title,
			"transcript_path": row.path,
		},
		SessionID: someStr(row.id),
	}
}

// piIsAgentMember：marker 注入条件——family 成员里的子代理会话。members 只
// 收录扩展派生的子会话（fork 不归族），root 自身也在 members 里，按 id 排除。
func piIsAgentMember(family Family[piRow], row piRow) bool {
	return row.id != family.Root.id
}

func piMemberTitle(row piRow) string {
	if row.agentName != nil {
		return *row.agentName
	}
	return "子代理"
}

// piScanRow：索引行——首行 header 必读；带 parentSession 的文件再在开头少量
// 行内找 session_info（扩展派生的子代理会话创建后立即写名，实测在第 4 行）。
// 主会话与 fork 子会话只付首行成本。
func piScanRow(path string) (piRow, bool) {
	header, err := piReadHeader(path)
	if err != nil {
		return piRow{}, false
	}
	mtime, err := piFileMtime(path)
	if err != nil {
		return piRow{}, false
	}
	var agentName *string
	if header.parentSession != nil {
		if name, ok := piAgentSessionName(path); ok {
			agentName = &name
		}
	}
	createdAt := int64(0)
	if millis, ok := support.ParseTimestamp(header.timestamp); ok {
		createdAt = millis
	}
	return piRow{
		path:       path,
		id:         header.id,
		createdAt:  &createdAt,
		updatedAt:  mtime,
		parentPath: header.parentSession,
		agentName:  agentName,
	}, true
}

// piAgentSessionName：各代 subagent 扩展的子代理会话都在创建后立即
// setSessionName（新版 "<type>#<id8>"、旧版 "subagent-<agent>-<runId>-1"），
// 而 pi 原生 fork 的子会话没有 session_info——「有名字」即扩展派生的通用
// 判据，不逐家认名字格式。只扫文件开头的少量行：名字写在创建后（实测
// @4），更深的 session_info（用户对旧会话 /rename）不改变归族判定，不值得
// 全文读。
func piAgentSessionName(path string) (string, bool) {
	lines, err := openPlainLines(path)
	if err != nil {
		return "", false
	}
	defer lines.Close()

	name := ""
	found := false
	skipped := 0
	scanErr := forEachLine(lines, func(index int, line string) error {
		// skip(1).take(32)：跳过 header 后最多读 32 行。
		if index == 0 {
			skipped++
			return nil
		}
		if index-skipped >= 32 {
			return errStopIteration
		}
		value, err := decodeJSONBytes([]byte(line))
		if err != nil {
			return nil
		}
		if kind, _ := jsonType(value); kind == "session_info" {
			if text, ok := jsonString(value, "name"); ok && text != "" {
				name = text
				found = true
			}
			return errStopIteration
		}
		return nil
	})
	if scanErr != nil && scanErr != errStopIteration {
		return "", false
	}
	return name, found
}

// piTaskRunsRoot：subagent-tasks 跟 agent 根走——sessions 可能被 sessionDir
// 配置重定向，tasks 目录不会。
func piTaskRunsRoot() string {
	root, err := PiRoot()
	if err != nil {
		return ""
	}
	return root
}

// piScanTaskRuns：subagent-tasks runner（旧版扩展的后台 run）——task.json
// 携带父会话 id 与 agent 名，过程在 output.jsonl（pi --mode json 事件流）。
// 它不是会话文件，不能独立成条目——父缺失（已删）的 run 直接跳过，数据仍
// 在磁盘。
func piScanTaskRuns(agentRoot string, fingerprintFiles *[]string) ([]piRow, error) {
	tasksDir := filepath.Join(agentRoot, "subagent-tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		return []piRow{}, nil
	}

	rows := make([]piRow, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(tasksDir, entry.Name())
		taskPath := filepath.Join(dirPath, "task.json")
		transcript := filepath.Join(dirPath, "output.jsonl")
		contents, err := os.ReadFile(taskPath)
		if err != nil {
			continue
		}
		value, err := decodeJSONBytes(contents)
		if err != nil {
			continue
		}
		id, hasID := jsonString(value, "id")
		parentID, hasParent := jsonString(value, "sessionId")
		if !hasID || !hasParent {
			continue
		}
		if _, err := os.Stat(transcript); err != nil {
			continue
		}
		*fingerprintFiles = append(*fingerprintFiles, taskPath)
		*fingerprintFiles = append(*fingerprintFiles, transcript)
		mtime, err := piFileMtime(transcript)
		if err != nil {
			mtime = 0
		}
		createdAt := int64(0)
		hasCreatedAt := false
		if raw, ok := jsonI64(value, "createdAt"); ok {
			createdAt, hasCreatedAt = raw, true
		}
		var createdAtPtr *int64
		if hasCreatedAt {
			createdAtPtr = &createdAt
		}
		var agentNamePtr *string
		if agentName, hasName := jsonString(value, "agent"); hasName {
			agentNamePtr = &agentName
		}
		rows = append(rows, piRow{
			path:            taskPath,
			id:              id,
			createdAt:       createdAtPtr,
			updatedAt:       mtime,
			parentPath:      nil,
			parentSessionID: &parentID,
			agentName:       agentNamePtr,
			kind:            piMemberKind{isTaskRun: true, transcript: transcript},
		})
	}
	return rows, nil
}

// piBuildFamilyIndex：归族——parentSession 指向本根内会话的扩展派生子代理
// 挂到父；fork 子会话（无 session_info 名）保持独立条目——fork 复制了父会话
// 的历史，折叠进时间线会整段重复。父缺失（已删）的子会话同样保持独立。
func piBuildFamilyIndex(rows []piRow) FamilyIndex[piRow] {
	// parentSession 是 pi 写下的绝对路径，与文件枚举可能隔着 symlink，两边
	// 都规范后再对表。
	byPath := map[string]string{}
	for i := range rows {
		byPath[piCanonicalPath(rows[i].path)] = rows[i].id
	}
	byID := map[string]bool{}
	for i := range rows {
		byID[rows[i].id] = true
	}

	// 直接归属：子代理 id -> 父会话 id。只收扩展派生的子代理（有 agent 名）；
	// fork 子会话保持独立。自指忽略。
	owner := map[string]string{}
	for i := range rows {
		row := rows[i]
		// TaskRun 的父关联在 task.json 里，按 id 直连；父缺失的 run 已被
		// 扫描过滤，不会出现在这里。
		if row.parentSessionID != nil {
			if byID[*row.parentSessionID] && *row.parentSessionID != row.id {
				owner[row.id] = *row.parentSessionID
			}
			continue
		}
		if row.parentPath == nil || row.agentName == nil {
			continue
		}
		if parentID, ok := byPath[piCanonicalPath(*row.parentPath)]; ok && parentID != row.id {
			owner[row.id] = parentID
		}
	}

	// 嵌套 spawn 的子代理沿链上溯到最近的顶层主会话；环按到达者收束。
	topOwner := func(id string) (string, bool) {
		current := id
		visited := map[string]bool{current: true}
		for {
			parent, ok := owner[current]
			if !ok {
				break
			}
			if visited[parent] {
				break
			}
			visited[parent] = true
			current = parent
		}
		if current != id {
			return current, true
		}
		return "", false
	}

	childrenOf := map[string][]piRow{}
	families := make([]Family[piRow], 0)
	for i := range rows {
		row := rows[i]
		if rootID, ok := topOwner(row.id); ok {
			childrenOf[rootID] = append(childrenOf[rootID], row)
			continue
		}
		families = append(families, Family[piRow]{Members: []piRow{row}, Root: row})
	}
	for fi := range families {
		if children, ok := childrenOf[families[fi].Root.id]; ok {
			families[fi].Members = append(families[fi].Members, children...)
			delete(childrenOf, families[fi].Root.id)
		}
	}

	return BuildFamilyIndex(families)
}

func piCanonicalPath(path string) string {
	canonical, err := support.Canonicalize(path)
	if err != nil {
		return path
	}
	return canonical
}

// piBuildRunTimeline：subagent-tasks 的事件流转录（pi --mode json）不是
// session 文件——只取 message_end 的最终消息合成线性时间线，
// message_start/update 是流式增量。
func piBuildRunTimeline(runID string, transcript string) ([]SessionMessage, []SessionEvent, error) {
	lines, err := openPlainLines(transcript)
	if err != nil {
		return nil, nil, err
	}
	defer lines.Close()

	messages := make([]SessionMessage, 0)
	scanErr := forEachLine(lines, func(index int, line string) error {
		value, err := decodeJSONBytes([]byte(line))
		if err != nil {
			return nil
		}
		if kind, _ := jsonType(value); kind != "message_end" {
			return nil
		}
		message, ok := getJSONObjectPath(value, "message")
		if !ok {
			return nil
		}
		entry := piEntry{
			id:        fmt.Sprintf("pi-run-%d", index),
			timestamp: jsonI64Ptr(message, "timestamp"),
			value:     map[string]any{"type": "message", "message": message},
		}
		messages = append(messages, piParseMessageEntry(entry, runID))
		return nil
	})
	if scanErr != nil {
		return nil, nil, scanErr
	}
	FillUserMessageModels(messages)
	return messages, []SessionEvent{}, nil
}

// piParseRunSummary：run 摘要从事件流合成——usage 与模型取全部 assistant
// 消息（与子会话文件的 assistant 求和口径一致），标题用 task.json 的
// agent 名。
func piParseRunSummary(row piRow, transcript string) (SessionSummary, error) {
	lines, err := openPlainLines(transcript)
	if err != nil {
		return SessionSummary{}, err
	}
	var tokenUsage *SessionTokenUsage
	models := ModelTally{}
	createdAt := row.createdAt
	updatedAt := row.updatedAt
	scanErr := forEachLine(lines, func(_ int, line string) error {
		value, err := decodeJSONBytes([]byte(line))
		if err != nil {
			return nil
		}
		if kind, _ := jsonType(value); kind != "message_end" {
			return nil
		}
		message, ok := getJSONObjectPath(value, "message")
		if !ok {
			return nil
		}
		if role, _ := jsonString(message, "role"); role != "assistant" {
			return nil
		}
		if usage, ok := getJSONObjectPath(message, "usage"); ok {
			MergeTokenUsage(&tokenUsage, piUsage(usage))
		}
		if model, ok := jsonString(message, "model"); ok {
			provider, _ := jsonString(message, "provider")
			models.Record(provider, model)
		}
		if timestamp, ok := jsonI64(message, "timestamp"); ok {
			if createdAt == nil || timestamp < *createdAt {
				copied := timestamp
				createdAt = &copied
			}
			if timestamp > updatedAt {
				updatedAt = timestamp
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

	return BuildSummary(SourcePi, transcript, SummaryAccumulator{
		SessionID:  someStr(row.id),
		Title:      someStr(piMemberTitle(row)),
		CreatedAt:  createdAt,
		UpdatedAt:  &updatedAt,
		TokenUsage: tokenUsage,
		Models:     models,
	})
}

// —— Reader 契约 ——

// ListEntries 实现 Reader：列表条目不带 summary（按需懒加载是 pi 的列表
// 契约），排序按 family 最近活动（全部成员 mtime 最大值）。
func (b *piReaderInstance) ListEntries() ([]SessionFileEntry, error) {
	current, err := b.current()
	if err != nil {
		return nil, err
	}
	index, err := current.familyIndex()
	if err != nil {
		return nil, err
	}
	entries := make([]SessionFileEntry, 0, len(index.Families))
	for i := range index.Families {
		family := index.Families[i]
		entries = append(entries, SessionFileEntry{
			Path:            family.Root.path,
			SourceSessionID: family.Root.id,
			SortTimestamp:   derefDefault(family.UpdatedAt()),
			Summary:         nil,
		})
	}
	sortEntries(entries)
	return entries, nil
}

// ClearCache 实现 Reader。
func (b *piReaderInstance) ClearCache() error {
	current, err := b.current()
	if err != nil {
		return err
	}
	return current.ClearCache()
}

// ParseSummary 实现 Reader。
func (b *piReaderInstance) ParseSummary(sourceSessionID string) (SessionSummary, error) {
	current, err := b.current()
	if err != nil {
		return SessionSummary{}, err
	}
	family, err := current.familyForID(sourceSessionID)
	if err != nil {
		return SessionSummary{}, err
	}
	return current.familySummary(family)
}

// ParseOverview 实现 Reader。
func (b *piReaderInstance) ParseOverview(sourceSessionID string) (SessionOverview, error) {
	current, err := b.current()
	if err != nil {
		return SessionOverview{}, err
	}
	family, err := current.familyForID(sourceSessionID)
	if err != nil {
		return SessionOverview{}, err
	}
	summary, err := current.familySummary(family)
	if err != nil {
		return SessionOverview{}, err
	}
	messages, events, err := current.familyTimeline(family)
	if err != nil {
		return SessionOverview{}, err
	}

	return SessionOverview{
		Summary:      summary,
		SourcePaths:  family.SourcePaths(),
		MessageCount: intPointer(len(messages)),
		EventCount:   intPointer(len(events)),
		Agents: FamilyAgents(family, func(row piRow) FamilyAgentLabel {
			if piIsAgentMember(family, row) {
				return FamilyAgentLabel{Kind: AgentLabelChild, Name: piMemberTitle(row)}
			}
			return FamilyAgentLabel{Kind: AgentLabelRoot}
		}),
	}, nil
}

// ParseMessages 实现 Reader。
func (b *piReaderInstance) ParseMessages(sourceSessionID string) ([]SessionMessage, error) {
	current, err := b.current()
	if err != nil {
		return nil, err
	}
	family, err := current.familyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	messages, _, err := current.familyTimeline(family)
	return messages, err
}

// ParseEvents 实现 Reader。
func (b *piReaderInstance) ParseEvents(sourceSessionID string) ([]SessionEvent, error) {
	current, err := b.current()
	if err != nil {
		return nil, err
	}
	family, err := current.familyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	_, events, err := current.familyTimeline(family)
	return events, err
}

// ParseAgentMessages 实现 Reader：子代理过程弹窗——marker（分组锚点）+ 该
// 子代理会话的完整消息。
func (b *piReaderInstance) ParseAgentMessages(sourceSessionID string, agentSessionID string) ([]SessionMessage, error) {
	current, err := b.current()
	if err != nil {
		return nil, err
	}
	family, err := current.familyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	var row *piRow
	for i := range family.Members {
		if piIsAgentMember(family, family.Members[i]) && family.Members[i].id == agentSessionID {
			row = &family.Members[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("Pi subagent %s does not belong to this session", agentSessionID)
	}
	messages := []SessionMessage{current.markerMessage(*row)}
	timeline, err := current.memberTimeline(*row)
	if err != nil {
		return nil, err
	}
	messages = append(messages, timeline.Messages...)
	return messages, nil
}

// —— 会话根解析 ——

// PiRoot：agent 根——PI_CODING_AGENT_DIR 重定向优先、~/.pi/agent 兜底。
func PiRoot() (string, error) {
	if path, ok := os.LookupEnv("PI_CODING_AGENT_DIR"); ok && path != "" {
		return piExpandConfiguredPath(path)
	}
	home, ok := support.UserHomeDir()
	if !ok {
		return "", fmt.Errorf("Unable to determine home directory")
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// PiSessionsRoot：官方契约（--session-dir > PI_CODING_AGENT_SESSION_DIR >
// settings.json.sessionDir）：显式配置的 sessionDir 就是最终会话目录；只有
// 未配置时才用 <agentDir>/sessions/<encoded-cwd>/ 默认布局。
func PiSessionsRoot() (string, error) {
	dir, err := piConfiguredSessionDir()
	if err != nil {
		return "", err
	}
	if dir != nil {
		return *dir, nil
	}
	root, err := PiRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sessions"), nil
}

func piConfiguredSessionDir() (*string, error) {
	if path, ok := os.LookupEnv("PI_CODING_AGENT_SESSION_DIR"); ok && path != "" {
		expanded, err := piExpandConfiguredPath(path)
		if err != nil {
			return nil, err
		}
		return &expanded, nil
	}
	return piSettingsSessionDir()
}

func piSettingsSessionDir() (*string, error) {
	root, err := PiRoot()
	if err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		return nil, nil
	}
	// settings.json 归 Pi 所有，损坏或字段缺失时回退默认目录，不阻断会话
	// 浏览。
	value, err := decodeJSONBytes(contents)
	if err != nil {
		return nil, nil
	}
	dir, ok := jsonString(value, "sessionDir")
	if !ok || dir == "" {
		return nil, nil
	}
	expanded, err := piExpandConfiguredPath(dir)
	if err != nil {
		return nil, err
	}
	return &expanded, nil
}

func piExpandConfiguredPath(value string) (string, error) {
	home, ok := support.UserHomeDir()
	if !ok {
		return "", fmt.Errorf("Unable to determine home directory")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("Unable to determine current directory")
	}
	return piResolveConfiguredPath(value, cwd, home), nil
}

func piResolveConfiguredPath(value string, cwd string, home string) string {
	expanded := value
	if value == "~" {
		expanded = home
	} else if relative, found := strings.CutPrefix(value, "~/"); found {
		expanded = filepath.Join(home, relative)
	}

	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(cwd, expanded)
}

// —— 文件读取与时间线 ——

func piFileMtime(path string) (int64, error) {
	return support.FileModifiedTimestampMillis(path)
}

// piReadHeader：首行 header 必读；type/id/timestamp 有硬契约。
func piReadHeader(path string) (piHeader, error) {
	lines, err := openPlainLines(path)
	if err != nil {
		return piHeader{}, err
	}
	defer lines.Close()

	line := ""
	hasLine := false
	readErr := forEachLine(lines, func(_ int, content string) error {
		line = content
		hasLine = true
		return errStopIteration
	})
	if readErr != nil && readErr != errStopIteration {
		return piHeader{}, readErr
	}
	if !hasLine {
		return piHeader{}, fmt.Errorf("Pi session has no header: %s", path)
	}

	value, err := parseJSONLine(line)
	if err != nil {
		return piHeader{}, err
	}
	if kind, _ := jsonType(value); kind != "session" {
		return piHeader{}, fmt.Errorf("Pi session header has invalid type: %s", path)
	}

	id, ok := jsonString(value, "id")
	if !ok || id == "" {
		return piHeader{}, fmt.Errorf("Pi session header has no id: %s", path)
	}
	timestamp, ok := jsonString(value, "timestamp")
	if !ok {
		timestamp = ""
	}
	if _, valid := support.ParseTimestamp(timestamp); !valid {
		return piHeader{}, fmt.Errorf("Pi session header has invalid timestamp: %s", path)
	}
	cwd, _ := jsonString(value, "cwd")
	// 官方把缺失/非法的 version 当作 v1 触发迁移；非数字 version 视为已最新。
	version := int64(1)
	if rawVersion, ok := jsonI64(value, "version"); ok {
		version = rawVersion
	} else if _, exists := getJSONObjectPath(value, "version"); exists {
		version = piCurrentSessionVersion
	}

	var parentSession *string
	if text, ok := jsonString(value, "parentSession"); ok {
		parentSession = &text
	}

	return piHeader{id: id, timestamp: timestamp, cwd: cwd, parentSession: parentSession, version: version, value: value}, nil
}

// piParseFullSummary：会话文件整文件解析（三级摘要缓存的 miss loader）。
func piParseFullSummary(path string) (SessionSummary, error) {
	document, err := piReadDocument(path)
	if err != nil {
		return SessionSummary{}, err
	}
	messages, _ := piBuildTimeline(document.header, document.entries)

	latestName := ""
	hasName := false
	for i := len(document.entries) - 1; i >= 0; i-- {
		if kind, _ := jsonType(document.entries[i].value); kind == "session_info" {
			if name, ok := jsonString(document.entries[i].value, "name"); ok && strings.TrimSpace(name) != "" {
				latestName = name
				hasName = true
			}
			break
		}
	}

	title := document.header.id
	if hasName {
		title = latestName
	} else if candidate, ok := piFirstUserTitle(messages); ok {
		title = candidate
	}

	createdAt := int64(0)
	hasCreated := false
	if millis, ok := support.ParseTimestamp(document.header.timestamp); ok {
		createdAt, hasCreated = millis, true
	}
	updatedAt, err := piFileMtime(path)
	if err != nil {
		return SessionSummary{}, err
	}

	summary := SummaryAccumulator{
		SessionID: someStr(document.header.id),
		Title:     someStr(NormalizeTitle(title)),
		CreatedAt: nil,
		UpdatedAt: &updatedAt,
	}
	if hasCreated {
		summary.CreatedAt = &createdAt
	}
	if document.header.cwd != "" {
		summary.Cwd = someStr(document.header.cwd)
	}
	// pi 无归档也无压缩：会话列表就是 sessions 根下的全部 .jsonl，上下文压
	// 缩只追加 compaction 条目，不动文件。
	for i := range document.entries {
		piAccumulateUsage(&summary.TokenUsage, document.entries[i].value)
		if message, ok := getJSONObjectPath(document.entries[i].value, "message"); ok {
			if role, _ := jsonString(message, "role"); role == "assistant" {
				if model, ok := jsonString(message, "model"); ok {
					provider, _ := jsonString(message, "provider")
					summary.Models.Record(provider, model)
				}
			}
		}
	}

	// header 决定创建时间，文件 mtime 只负责失效判断，避免旧 entry 时间变化
	// 导致列表漂移。
	summary.CreatedAt = nil
	if hasCreated {
		summary.CreatedAt = &createdAt
	}
	return BuildSummary(SourcePi, path, summary)
}

// piAccumulateUsage：主线程 assistant 消息带每次调用的增量 usage；子代理
// run 的消耗记录在 toolResult 的扩展元数据里，按「会话总消耗」口径一并计
// 入。官方 usage 的 output 已含 reasoning（totalTokens = input+output+
// cacheRead+cacheWrite）。
func piAccumulateUsage(total **SessionTokenUsage, entry any) {
	for _, pair := range piEntryUsages(entry) {
		MergeTokenUsage(total, pair.usage)
	}
}

type piUsageEntry struct {
	model string
	usage SessionTokenUsage
}

// piEntryUsages：单个 entry 产生的全部增量 usage（主线程回复 0 或 1 条，
// subagent toolResult 可能带多个 run），附带模型名：主线 assistant 消息带
// message.model，subagent run 的转录没有模型字段，为空串。
func piEntryUsages(entry any) []piUsageEntry {
	message, ok := getJSONObjectPath(entry, "message")
	if !ok {
		return []piUsageEntry{}
	}

	role, _ := jsonString(message, "role")
	switch role {
	case "assistant":
		usage, ok := getJSONObjectPath(message, "usage")
		if !ok {
			return []piUsageEntry{}
		}
		model, _ := jsonString(message, "model")
		return []piUsageEntry{{model: model, usage: piUsage(usage)}}
	case "toolResult":
		if toolName, _ := jsonString(message, "toolName"); toolName == "subagent" {
			results, ok := jsonArray(message, "details", "results")
			if !ok {
				return []piUsageEntry{}
			}
			entries := make([]piUsageEntry, 0)
			for _, run := range results {
				if usage, ok := getJSONObjectPath(run, "usage"); ok {
					entries = append(entries, piUsageEntry{usage: piUsage(usage)})
				}
			}
			return entries
		}
	}
	return []piUsageEntry{}
}

// PiUsageHours：用量曲线的日桶——与 piAccumulateUsage 同源，按
// entry.timestamp 归到本地时区的小时，跨天会话由此拆开。
func PiUsageHours(path string) (UsageHourBuckets, error) {
	document, err := piReadDocument(path)
	if err != nil {
		return nil, err
	}
	buckets := UsageHourBuckets{}

	for i := range document.entries {
		for _, pair := range piEntryUsages(document.entries[i].value) {
			MergeUsageEvent(buckets, hourKeyOpt(document.entries[i].timestamp), pair.model, pair.usage)
		}
	}
	return buckets, nil
}

func piUsage(usage any) SessionTokenUsage {
	input, _ := jsonU64(usage, "input")
	output, _ := jsonU64(usage, "output")
	cacheRead, _ := jsonU64(usage, "cacheRead")
	cacheWrite, _ := jsonU64(usage, "cacheWrite")
	return SessionTokenUsage{
		InputTokens:      input,
		OutputTokens:     output,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}

func piFirstUserTitle(messages []SessionMessage) (string, bool) {
	for i := range messages {
		if messages[i].Role != "user" {
			continue
		}
		for _, block := range messages[i].Blocks {
			if block.Text == nil {
				continue
			}
			if candidate, ok := TitleCandidateFromText(*block.Text); ok {
				return candidate, true
			}
		}
	}
	return "", false
}

const piCurrentSessionVersion int64 = 3

// piDocument：会话文件的 header 与 entries。
type piDocument struct {
	header  piHeader
	entries []piEntry
}

func piReadDocument(path string) (piDocument, error) {
	header, err := piReadHeader(path)
	if err != nil {
		return piDocument{}, err
	}
	lines, err := openPlainLines(path)
	if err != nil {
		return piDocument{}, err
	}
	defer lines.Close()

	entries := make([]piEntry, 0)
	scanErr := forEachLine(lines, func(index int, line string) error {
		if index == 0 {
			return nil // header 已读。
		}
		value, err := decodeJSONBytes([]byte(line))
		if err != nil {
			return nil
		}
		id := fmt.Sprintf("pi-entry-%d", index)
		if text, ok := jsonString(value, "id"); ok {
			id = text
		}
		var parentID *string
		if text, ok := jsonString(value, "parentId"); ok {
			parentID = &text
		}
		var timestamp *int64
		if text, ok := jsonString(value, "timestamp"); ok {
			if millis, ok := support.ParseTimestamp(text); ok {
				timestamp = &millis
			}
		}
		entries = append(entries, piEntry{id: id, parentID: parentID, timestamp: timestamp, value: value})
		return nil
	})
	closeErr := lines.Close()
	if scanErr != nil {
		return piDocument{}, scanErr
	}
	if closeErr != nil {
		return piDocument{}, closeErr
	}

	if header.version < piCurrentSessionVersion {
		piMigrateLegacyEntries(header.version, entries)
	}

	return piDocument{header: header, entries: entries}, nil
}

// piMigrateLegacyEntries：对齐官方 session-manager 的加载迁移；Reins 只读
// 浏览，仅在内存迁移，不写回磁盘。
func piMigrateLegacyEntries(version int64, entries []piEntry) {
	if version < 2 {
		piMigrateV1Entries(entries)
	}
	if version < 3 {
		piMigrateV2Entries(entries)
	}
}

// piMigrateV1Entries：v1 文件没有 id/parentId，按官方 migrateV1ToV2 补成线
// 性链，否则 buildTimeline 从叶子回溯时整段历史只剩最后一条。
func piMigrateV1Entries(entries []piEntry) {
	usedIDs := map[string]bool{}
	var previousID *string
	for i := range entries {
		id := piNewEntryID(usedIDs)
		if object, ok := entries[i].value.(map[string]any); ok {
			object["id"] = id
			if previousID != nil {
				object["parentId"] = *previousID
			} else {
				object["parentId"] = nil
			}
		}
		entries[i].id = id
		entries[i].parentID = previousID
		copied := id
		previousID = &copied
	}
	piMigrateCompactionIndices(entries)
}

// piMigrateCompactionIndices：v1 的 compaction 用文件行索引（含 header 行，
// 故减一）指向保留 entry，迁移成 entry id。
func piMigrateCompactionIndices(entries []piEntry) {
	type fixup struct {
		position int
		keptID   *string
	}
	fixes := make([]fixup, 0)
	for position, entry := range entries {
		object, ok := entry.value.(map[string]any)
		if !ok {
			continue
		}
		if kind, _ := jsonString(object, "type"); kind != "compaction" {
			continue
		}
		keptIndex, ok := jsonI64(object, "firstKeptEntryIndex")
		if !ok {
			continue
		}
		var keptID *string
		if idx := keptIndex - 1; idx >= 0 && int(idx) < len(entries) {
			id := entries[idx].id
			keptID = &id
		}
		fixes = append(fixes, fixup{position: position, keptID: keptID})
	}

	for _, fix := range fixes {
		object, ok := entries[fix.position].value.(map[string]any)
		if !ok {
			continue
		}
		delete(object, "firstKeptEntryIndex")
		if fix.keptID != nil {
			object["firstKeptEntryId"] = *fix.keptID
		}
	}
}

// piMigrateV2Entries：v2 → v3：hookMessage 角色改名 custom。
func piMigrateV2Entries(entries []piEntry) {
	for i := range entries {
		message, ok := getJSONObjectPath(entries[i].value, "message")
		if !ok {
			continue
		}
		object, ok := message.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := jsonString(object, "role"); role == "hookMessage" {
			object["role"] = "custom"
		}
	}
}

// piBuildTimeline：从叶子回溯 parentId 链得到活动分支，再按链序装配。
func piBuildTimeline(header piHeader, entries []piEntry) ([]SessionMessage, []SessionEvent) {
	byID := map[string]piEntry{}
	for _, entry := range entries {
		byID[entry.id] = entry
	}
	if len(entries) == 0 {
		return []SessionMessage{}, piParentSessionEvents(header)
	}
	current := entries[len(entries)-1]
	chain := make([]piEntry, 0)
	visited := map[string]bool{}

	for {
		if visited[current.id] {
			break
		}
		visited[current.id] = true
		chain = append(chain, current)

		if current.parentID == nil {
			break
		}
		parent, ok := byID[*current.parentID]
		if !ok {
			break
		}
		current = parent
	}

	// 反转链（叶子在最前 → 根在最前）。
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	messages := make([]SessionMessage, 0)
	events := piParentSessionEvents(header)

	for _, entry := range chain {
		kind, hasKind := jsonType(entry.value)
		switch {
		case hasKind && kind == "message":
			messages = append(messages, piParseMessageEntry(entry, header.id))
		case hasKind && kind == "custom_message":
			messages = append(messages, piParseCustomMessageEntry(entry, header.id))
		case hasKind && kind == "compaction":
			events = append(events, piParseEvent(entry, "compaction", header.id))
			piExpandCompactionMessages(entry, header.id, &messages)
		case hasKind:
			events = append(events, piParseEvent(entry, kind, header.id))
		default:
			events = append(events, piParseEvent(entry, "unknown", header.id))
		}
	}

	// 用户输入自身不带模型，由这一轮的回答补归属。
	FillUserMessageModels(messages)

	return messages, events
}

// piExpandCompactionMessages：官方把 compaction entry 还原为 compactionSummary
// 消息 + retainedTail 中的保留消息；这里是浏览器口径，全量展示，不过滤
// stopReason 为 error/aborted/deferred 的助手消息。
func piExpandCompactionMessages(entry piEntry, sessionID string, messages *[]SessionMessage) {
	summary, _ := jsonString(entry.value, "summary")
	*messages = append(*messages, SessionMessage{
		ID:        entry.id + "-summary",
		Role:      "compactionSummary",
		Timestamp: entry.timestamp,
		Blocks: []ContentBlock{{
			Kind:    "compactionSummary",
			Text:    &summary,
			Payload: entry.value,
		}},
		SessionID: someStr(sessionID),
	})

	tail, ok := jsonArray(entry.value, "retainedTail")
	if !ok {
		return
	}
	for index, message := range tail {
		var timestamp *int64
		if raw := jsonI64Ptr(message, "timestamp"); raw != nil {
			timestamp = raw
		} else {
			timestamp = entry.timestamp
		}
		tailEntry := piEntry{
			id:        fmt.Sprintf("%s-tail-%d", entry.id, index),
			timestamp: timestamp,
			value:     map[string]any{"type": "message", "message": message},
		}
		*messages = append(*messages, piParseMessageEntry(tailEntry, sessionID))
	}
}

func piParentSessionEvents(header piHeader) []SessionEvent {
	if header.parentSession == nil {
		return []SessionEvent{}
	}
	timestamp := int64(0)
	if millis, ok := support.ParseTimestamp(header.timestamp); ok {
		timestamp = millis
	}
	return []SessionEvent{{
		ID:        "pi-parent-session",
		Kind:      "parent_session",
		Timestamp: &timestamp,
		Summary:   "Pi session has a parent session",
		Payload: map[string]any{
			"type":          "session",
			"id":            header.id,
			"parentSession": *header.parentSession,
			"header":        header.value,
		},
		SessionID: someStr(header.id),
	}}
}

func piParseEvent(entry piEntry, kind string, sessionID string) SessionEvent {
	return SessionEvent{
		ID:        entry.id,
		Kind:      kind,
		Timestamp: entry.timestamp,
		Summary:   SummarizeEvent(kind, entry.value),
		Payload:   entry.value,
		SessionID: someStr(sessionID),
	}
}

// piParseMessageEntry：message entry 的消息装配。
func piParseMessageEntry(entry piEntry, sessionID string) SessionMessage {
	message, ok := getJSONObjectPath(entry.value, "message")
	if !ok {
		return SessionMessage{
			ID:        entry.id,
			Role:      "unknown",
			Timestamp: entry.timestamp,
			Blocks:    []ContentBlock{UnsupportedContentBlock("Pi", entry.value)},
			SessionID: someStr(sessionID),
		}
	}
	role := "unknown"
	if text, hasRole := jsonString(message, "role"); hasRole {
		role = text
	}
	var blocks []ContentBlock
	switch role {
	case "system":
		// 系统提示：正文为空，提示词按 sections 分块存。
		if block := piSystemPromptBlock(message); block != nil {
			blocks = []ContentBlock{*block}
		} else {
			blocks = []ContentBlock{UnsupportedContentBlock("Pi", message)}
		}
	case "bashExecution":
		texts := make([]string, 0)
		if command, ok := jsonString(message, "command"); ok {
			texts = append(texts, command)
		}
		if output, ok := jsonString(message, "output"); ok {
			texts = append(texts, output)
		}
		toolName := "bash"
		blocks = []ContentBlock{{
			Kind:     "bash_execution",
			Text:     someStr(strings.Join(texts, "\n")),
			ToolName: &toolName,
			Payload:  message,
		}}
	case "branchSummary", "compactionSummary":
		summary, _ := jsonString(message, "summary")
		blocks = []ContentBlock{{
			Kind:    role,
			Text:    &summary,
			Payload: message,
		}}
	case "user", "assistant", "toolResult", "custom":
		content, hasContent := getJSONObjectPath(message, "content")
		blocks = piParseMessageContent(role, content, hasContent)
	default:
		blocks = []ContentBlock{UnsupportedContentBlock("Pi", message)}
	}

	if role == "toolResult" {
		isError := jsonBoolPtr(message, "isError")
		// subagent 扩展（pi --no-session）把子代理完整对话内嵌在
		// toolResult.details 里，这是官方留给扩展的元数据通道；存在时用
		// 结构化块替代普通 tool_result 块。
		if toolName, _ := jsonString(message, "toolName"); toolName == "subagent" {
			if block := piParseSubagentRunBlock(entry.id, message, sessionID); block != nil {
				blocks = []ContentBlock{*block}
			}
		}
		for i := range blocks {
			if blocks[i].ToolName == nil {
				if toolName, ok := jsonString(message, "toolName"); ok {
					blocks[i].ToolName = &toolName
				}
			}
			if blocks[i].ToolCallID == nil {
				if callID, ok := jsonString(message, "toolCallId"); ok {
					blocks[i].ToolCallID = &callID
				}
			}
			if blocks[i].IsError == nil {
				blocks[i].IsError = isError
			}
		}
	}

	if role == "user" {
		blocks = piSanitizeUserBlocks(blocks)
	}

	if len(blocks) == 0 {
		// 无可见内容的消息按语义分流：助手侧错误显示错误文案，整条都是宿主
		// 注入的上下文则保留原文，其余才退化成一行原始报文诊断块。
		errorMessage := ""
		if text, ok := jsonString(message, "errorMessage"); ok && strings.TrimSpace(text) != "" {
			errorMessage = text
		}
		if errorMessage != "" {
			blocks = append(blocks, MessageErrorBlock(errorMessage, message))
		} else if injected := InjectedContextBlock(mustJSONValue(message, "content"), message); injected != nil {
			blocks = append(blocks, *injected)
		} else {
			blocks = append(blocks, EmptyMessageBlock("Pi", "content was empty after sanitization", message))
		}
	}

	timestamp := entry.timestamp
	if raw := jsonI64Ptr(message, "timestamp"); raw != nil {
		timestamp = raw
	}

	var model *SessionModel
	if modelID, ok := jsonString(message, "model"); ok {
		provider, _ := jsonString(message, "provider")
		// Pi 的 assistant 消息直接带这次调用的模型与 provider。
		sessionModel := NewSessionModel(provider, modelID)
		model = &sessionModel
	}

	return SessionMessage{
		ID:        entry.id,
		Role:      role,
		Timestamp: timestamp,
		Blocks:    blocks,
		Model:     model,
		SessionID: someStr(sessionID),
	}
}

// mustJSONValue：message 的 content 取值（Rust message.get("content") 的
// Option 语义：缺失 → nil）。
func mustJSONValue(value any, key string) any {
	child, _ := getJSONObjectPath(value, key)
	return child
}

// piParseSubagentRunBlock：子代理运行结果的结构化块——details.results[]
// .messages 是扩展捕获的标准 AgentMessage 线性流，复用 piParseMessageEntry
// 解析成嵌套消息；原始 messages 数组不进 payload，由解析结果替代，其余 run
// 元数据原样保留。
func piParseSubagentRunBlock(entryID string, message any, sessionID string) *ContentBlock {
	results, ok := jsonArray(message, "details", "results")
	if !ok || len(results) == 0 {
		return nil
	}

	runs := make([]any, 0, len(results))
	for runIndex, run := range results {
		runObject := deepCloneJSON(run)
		nestedMessages := []SessionMessage{}
		if items, ok := jsonArray(run, "messages"); ok {
			for messageIndex, nested := range items {
				nestedEntry := piEntry{
					id:    fmt.Sprintf("%s-r%d-m%d", entryID, runIndex, messageIndex),
					value: map[string]any{"type": "message", "message": nested},
				}
				nestedMessages = append(nestedMessages, piParseMessageEntry(nestedEntry, sessionID))
			}
		}
		if object, ok := runObject.(map[string]any); ok {
			delete(object, "messages")
			object["nestedMessages"] = nestedMessages
		}
		runs = append(runs, runObject)
	}

	reportTexts := make([]string, 0)
	for _, block := range piParseMessageContent("toolResult", mustJSONValue(message, "content"), true) {
		if block.Text != nil {
			reportTexts = append(reportTexts, *block.Text)
		}
	}
	report := strings.Join(reportTexts, "\n")

	var textPtr *string
	if report != "" {
		textPtr = &report
	}
	toolName := "subagent"
	return &ContentBlock{
		Kind:       "subagent_run",
		Text:       textPtr,
		ToolName:   &toolName,
		ToolCallID: strField(jsonString(message, "toolCallId")),
		Payload:    map[string]any{"runs": runs},
	}
}

func piParseCustomMessageEntry(entry piEntry, sessionID string) SessionMessage {
	content, hasContent := getJSONObjectPath(entry.value, "content")
	blocks := piParseMessageContent("custom", content, hasContent)
	if len(blocks) == 0 {
		blocks = append(blocks, EmptyMessageBlock("Pi", "custom message content was empty", entry.value))
	}

	return SessionMessage{
		ID:        entry.id,
		Role:      "custom",
		Timestamp: entry.timestamp,
		Blocks:    blocks,
		SessionID: someStr(sessionID),
	}
}

func piParseMessageContent(role string, content any, present bool) []ContentBlock {
	if present {
		if text, ok := content.(string); ok {
			kind := "text"
			if role == "assistant" {
				kind = "output_text"
			} else if role == "toolResult" {
				kind = "tool_result"
			}
			return []ContentBlock{{Kind: kind, Text: &text}}
		}
		if items, ok := content.([]any); ok {
			blocks := make([]ContentBlock, 0, len(items))
			for _, item := range items {
				if block := piParseContentBlock(role, item); block != nil {
					blocks = append(blocks, *block)
				}
			}
			return blocks
		}
		return []ContentBlock{UnsupportedContentBlock("Pi", content)}
	}
	return []ContentBlock{}
}

// piSystemPromptBlock：系统提示消息——正文为空，提示词按 sections 分块存，
// 另带本次新增/移除的工具。拼回一段可读文本（段名 + 正文 + 工具名与说明）；
// 工具的 parameters 是机械的 JSON schema，不进正文，完整报文仍留在 payload
// 里。
func piSystemPromptBlock(message any) *ContentBlock {
	sections, hasSections := message.(map[string]any)["sections"].(map[string]any)
	added := piToolEntries(getJSONObject(message, "toolsAdded"))
	removed := piToolNames(getJSONObject(message, "toolsRemoved"))

	if !hasSections && len(added) == 0 && len(removed) == 0 {
		return nil
	}

	parts := make([]string, 0)

	if hasSections {
		// 首行当折叠行的摘要：这段提示词包含哪些段（serde_json 的 Map 默认
		// 按键排序，Go map 无序，这里排序对齐）。
		names := make([]string, 0, len(sections))
		for name := range sections {
			names = append(names, name)
		}
		sort.Strings(names)
		parts = append(parts, fmt.Sprintf("%d 段：%s", len(sections), strings.Join(names, "、")))
		for _, name := range names {
			value, _ := sections[name].(string)
			parts = append(parts, fmt.Sprintf("## %s\n%s", name, value))
		}
	}

	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("## 本次加入的工具\n%s", strings.Join(added, "\n")))
	}

	if len(removed) > 0 {
		lines := make([]string, 0, len(removed))
		for _, name := range removed {
			lines = append(lines, "- "+name)
		}
		parts = append(parts, fmt.Sprintf("## 本次移除的工具\n%s", strings.Join(lines, "\n")))
	}

	joined := strings.Join(parts, "\n\n")
	block := diagnosticBlock("system_prompt", joined, message)
	return &block
}

func getJSONObject(value any, key string) any {
	child, _ := getJSONObjectPath(value, key)
	return child
}

func piToolEntries(tools any) []string {
	items, ok := tools.([]any)
	if !ok {
		return []string{}
	}
	entries := make([]string, 0)
	for _, item := range items {
		name, ok := jsonString(item, "name")
		if !ok {
			continue
		}
		description, _ := jsonString(item, "description")
		entries = append(entries, fmt.Sprintf("- %s: %s", name, description))
	}
	return entries
}

func piToolNames(tools any) []string {
	items, ok := tools.([]any)
	if !ok {
		return []string{}
	}
	names := make([]string, 0)
	for _, item := range items {
		if name, ok := jsonString(item, "name"); ok {
			names = append(names, name)
		}
	}
	return names
}

// piParseContentBlock：content 数组单项 → UI 块。
func piParseContentBlock(role string, value any) *ContentBlock {
	rawKind := "unknown"
	if text, ok := jsonString(value, "type"); ok {
		rawKind = text
	}
	kind := rawKind
	switch {
	case role == "assistant" && rawKind == "text":
		kind = "output_text"
	case role == "toolResult" && rawKind == "text":
		kind = "tool_result"
	case rawKind == "text":
		kind = "text"
	case rawKind == "toolCall":
		kind = "tool_use"
	case rawKind == "toolResult":
		kind = "tool_result"
	case rawKind == "image":
		kind = "image"
	}

	text := ""
	hasText := false
	if textValue, ok := jsonString(value, "text"); ok {
		text, hasText = textValue, true
	} else if thinkingValue, ok := jsonString(value, "thinking"); ok {
		text, hasText = thinkingValue, true
	} else if contentValue, ok := jsonString(value, "content"); ok {
		text, hasText = contentValue, true
	}

	if role == "assistant" && rawKind == "text" && hasText && piIsZeroWidthTextPlaceholder(text) {
		return nil
	}
	switch kind {
	case "text", "output_text", "thinking", "image", "tool_use", "tool_result":
	default:
		kind = "unsupported_block"
	}

	// 图片项没有 text 字段：把 data + mimeType 还原成 data URL 当正文，前端
	// 才能直接渲染，而不是退成一行 JSON。
	if !hasText && kind == "image" {
		if reference, ok := ImageReference(value); ok {
			text = reference
			hasText = true
		}
	}

	toolName := ""
	hasToolName := false
	if name, ok := jsonString(value, "name"); ok {
		toolName, hasToolName = name, true
	} else if name, ok := jsonString(value, "toolName"); ok {
		toolName, hasToolName = name, true
	}
	callID := ""
	hasCallID := false
	if id, ok := jsonString(value, "id"); ok {
		callID, hasCallID = id, true
	} else if id, ok := jsonString(value, "toolCallId"); ok {
		callID, hasCallID = id, true
	}

	return &ContentBlock{
		Kind:       kind,
		Text:       optionalString(text, hasText),
		ToolName:   optionalString(toolName, hasToolName),
		ToolCallID: optionalString(callID, hasCallID),
		Payload:    value,
	}
}

func piIsZeroWidthTextPlaceholder(text string) bool {
	if text == "" {
		return false
	}
	for _, character := range text {
		if character != '​' {
			return false
		}
	}
	return true
}

// piSanitizeUserBlocks：与 SanitizeUserBlocks 同口径，但 pi 还保留 image 类
// 块（Rust matches! 列表里含 image）。
func piSanitizeUserBlocks(blocks []ContentBlock) []ContentBlock {
	out := make([]ContentBlock, 0)
	for _, block := range blocks {
		if block.Text != nil {
			if sanitized, ok := SanitizeUserMessageText(*block.Text); ok {
				block.Text = &sanitized
			} else {
				block.Text = nil
			}
		}
		switch block.Kind {
		case "image", "tool_use", "tool_result", "function_call", "function_call_output", "unsupported_block":
			out = append(out, block)
		default:
			if block.Text == nil {
				continue
			}
			out = append(out, block)
		}
	}
	return out
}

// piNewEntryID：v1 迁移的短 id（uuid simple 前 8 位等价的随机 8 hex，冲突
// 重试）。
func piNewEntryID(usedIDs map[string]bool) string {
	for {
		id := piRandomHex8()
		if !usedIDs[id] {
			usedIDs[id] = true
			return id
		}
	}
}

// jsonI64Ptr：message.timestamp 的指针承载。
func jsonI64Ptr(value any, key string) *int64 {
	if millis, ok := jsonI64(value, key); ok {
		return &millis
	}
	return nil
}

// piRandomHex8：随机 8 位 hex（uuid v4 simple 前 8 位的唯一性等价物；support
// 的临时文件名随机串不导出，域内自带一份）。
func piRandomHex8() string {
	var b [4]byte
	_, _ = cryptoRandRead(b[:])
	return fmt.Sprintf("%02x%02x%02x%02x", b[0], b[1], b[2], b[3])
}
