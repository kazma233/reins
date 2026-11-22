package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reins/internal/logger"
	"reins/internal/support"
)

// reader_engine.go：family 形态读取器共享的机械底座（GLOSSARY 读取器引擎
// 词条）。分组与 root 选取按 family_index 的既定边界留在读取器；本文件拥有
// 其余全部机械部分——目录索引与双半时间线的 load-through 缓存、目录行持久
// 缓存（rows_cache）、三级摘要缓存、marker 外壳与 (timestamp, id) 终排、
// 分页五元组、clear 编排。读取器经 FamilySpec + FamilyHooks 声明真差异，
// hook 为 nil 时吃引擎默认，典型来源大部分方法吃默认值。锁与错误文案是
// 前端可见契约，一律由 label 数据逐字还原。

// MemberTimeline：单成员双半时间线，读取器一次扫描产出，engine 负责缓存、
// marker 注入与终排。
type MemberTimeline struct {
	Messages []SessionMessage
	Events   []SessionEvent
}

// assembleMemberTimelines：LoadMembers 的尾部装配（opencode/zcode 共用）——
// 按成员 id 顺序取出各会话的双半并补空集合（nil 集合 JSON 序列化成 null，
// 前端契约要空数组）；消费过的键从 map 删除，让残余引用尽早可回收。
func assembleMemberTimelines(
	memberIDs []string,
	messagesBySession map[string][]SessionMessage,
	eventsBySession map[string][]SessionEvent,
) []MemberTimeline {
	timelines := make([]MemberTimeline, 0, len(memberIDs))
	for _, id := range memberIDs {
		messages := messagesBySession[id]
		events := eventsBySession[id]
		if messages == nil {
			messages = []SessionMessage{}
		}
		if events == nil {
			events = []SessionEvent{}
		}
		delete(messagesBySession, id)
		delete(eventsBySession, id)
		timelines = append(timelines, MemberTimeline{Messages: messages, Events: events})
	}
	return timelines
}

// Freshness：目录索引的失效口径。engine 只做相等比较、不假设单调性：指纹串
// 把文件集合变化也计入失效，与 mtime 是两种语义。
type Freshness struct {
	isStamp bool
	stamp   int64
	print   string
}

// StampFreshness：毫秒级时间戳口径。
func StampFreshness(value int64) Freshness {
	return Freshness{isStamp: true, stamp: value}
}

// FingerprintFreshness：指纹串口径。
func FingerprintFreshness(value string) Freshness {
	return Freshness{print: value}
}

// CacheStamp：持久行缓存的失效戳——同一口径的字符串化，两种语义靠前缀区分。
func (f Freshness) CacheStamp() string {
	if f.isStamp {
		return fmt.Sprintf("stamp:%d", f.stamp)
	}
	return "fingerprint:" + f.print
}

func (f Freshness) equal(other Freshness) bool {
	return f.isStamp == other.isStamp && f.stamp == other.stamp && f.print == other.print
}

// SummaryKind：family 摘要的合成方式。
type SummaryKind uint8

const (
	// SummaryRootTranscript：root 转录整文件解析，经三级缓存（内存 → 持久层
	// → 解析）。
	SummaryRootTranscript SummaryKind = iota
	// SummaryRows：直接从 root 行合成，无路径级缓存。
	SummaryRows
)

// OverviewCounts：overview 计数三派。
type OverviewCounts uint8

const (
	// OverviewTimeline：加载双半时间线取 len（有填缓存副作用，时序由 engine
	// 保持）。
	OverviewTimeline OverviewCounts = iota
	// OverviewOmitted：不计数。
	OverviewOmitted
	// OverviewDeclared：读取器给原始计数，engine 补 marker 数。
	OverviewDeclared
)

// RowErrorPolicy：行装载的坏文件策略。
type RowErrorPolicy uint8

const (
	// RowSkipSilently：静默跳过。
	RowSkipSilently RowErrorPolicy = iota
	// RowSkipLogged：记日志跳过（文案 "{label} session skipped {path}: {error}"）。
	RowSkipLogged
	// RowAbort：中止整个列表。
	RowAbort
)

// MarkerShape：marker 的读取器供给面。engine 组装外壳（正文/摘要/kind/
// session_id 与 type/title 基底），读取器给 id 方案与 payload 附加字段；
// dsh 的 message/event payload 不对称由两个 extras 分开表达。
type MarkerShape struct {
	MessageID     string
	EventID       string
	MessageExtras any // JSON 对象（map[string]any）
	EventExtras   any
}

// LabeledMarker：默认 id 方案 "{label小写}-subagent-start-{id}" /
// "-subagent-event-{id}"。
func LabeledMarker(label string, memberID string) MarkerShape {
	prefix := strings.ToLower(label)
	return MarkerShape{
		MessageID:     fmt.Sprintf("%s-subagent-start-%s", prefix, memberID),
		EventID:       fmt.Sprintf("%s-subagent-event-%s", prefix, memberID),
		MessageExtras: map[string]any{},
		EventExtras:   map[string]any{},
	}
}

// WithExtras：附加 payload 字段（message 与 event 可各不相同）。
func (m MarkerShape) WithExtras(message any, event any) MarkerShape {
	m.MessageExtras = message
	m.EventExtras = event
	return m
}

// subagentMarkerShape：opencode/zcode 共用的子代理 marker——id 沿用历史拼写
// "{prefix}-subagent_started-{id}"（引擎默认是 "-start-"）；payload 差异字段
// directory/parent_id 全放 extras，基底由引擎组装。
func subagentMarkerShape(prefix string, memberID string, directory string, parentID *string) MarkerShape {
	extras := map[string]any{
		"directory": directory,
		"parent_id": parentID,
	}
	return MarkerShape{
		MessageID:     fmt.Sprintf("%s-subagent_started-%s", prefix, memberID),
		EventID:       fmt.Sprintf("%s-subagent_started-%s", prefix, memberID),
		MessageExtras: extras,
		EventExtras:   extras,
	}
}

// FamilyHooks：读取器可选差异的声明面，nil 字段吃引擎默认。每个 hook 对应
// 一个真实存在的来源差异；必填项在 FamilySpec 接口上。
type FamilyHooks[R FamilyRow] struct {
	// DisplayLabel：family 未找到错误的显示名；nil 等于 Label()，产品名与
	// 锁前缀拼写不同时覆写（claude/dsh 经 agents 清单派生）。参数是 Label()
	// 的取值。
	DisplayLabel func(label string) string
	// IndexLockLabel：index 锁文案；nil 等于 "{label} family index"。dsh
	// 覆写为 "DSH index"（无 "family" 字样）。
	IndexLockLabel func(label string) string
	// IndexFreshness：目录失效；nil 等于「扫描根目录 mtime 与其下 jsonl 文件
	// mtime 的最大值」。
	IndexFreshness func(scanRoot string) (Freshness, error)
	// ParseFullSummary：SummaryRootTranscript 时整文件解析（三级缓存的 miss
	// loader）；nil 时调用报「未声明 SummaryKind::RootTranscript」。
	ParseFullSummary func(path string) (SessionSummary, error)
	// SummaryFromRows：SummaryRows 时行直构；nil 时 panic（Rust unreachable）。
	SummaryFromRows func(root R) SessionSummary
	// RowUsage：行级 usage（索引行自带）；nil 无。
	RowUsage func(row R) *SessionTokenUsage
	// RowModels：索引行自带的模型（行解析时收集）；nil 空。
	RowModels func(row R) []SessionModel
	// FamilyUsage：family 总 usage；nil 等于成员 RowUsage 求和。codex 覆写为
	// 逐成员经 path_summary 整文件解析（索引行只读首行拿不到 usage）。
	FamilyUsage func(engine *ReaderEngine[R], family Family[R]) (*SessionTokenUsage, error)
	// FamilyModels：family 用过的模型；nil 等于按成员 Absorb 合并（root 主模型
	// 排最前）。codex 与 FamilyUsage 同因覆写。
	FamilyModels func(engine *ReaderEngine[R], family Family[R]) ([]SessionModel, error)
	// FamilyFreshness：timeline 失效（毫秒口径）；nil 等于成员文件 mtime 最大
	// 值。SQL 来源覆写为 max(db mtime, family updated_at)。
	FamilyFreshness func(scanRoot string, family Family[R]) (int64, error)
	// IsSubagentMember：marker 注入条件；nil 等于 member_id ≠ root。注意与
	// (+N) 计数是两条独立口径，不得合并（claude 有「有 marker、不计数」的成员）。
	IsSubagentMember func(family Family[R], row R) bool
	// Marker：marker 形状；nil 等于 LabeledMarker(label, memberID)。
	Marker func(memberID string, row R) MarkerShape
	// RootExtraEvents：root 转录的前置事件（codex 的 subagent 生命周期事件）；
	// nil 空。
	RootExtraEvents func(family Family[R]) ([]SessionEvent, error)
	// CountFamilyRecords：OverviewDeclared 时的原始计数（不含 marker）；nil
	// 时调用报「未声明 OverviewCounts::Declared」。
	CountFamilyRecords func(scanRoot string, family Family[R]) (int, int, error)
	// FamilySourcePaths：overview.source_paths；nil 等于成员路径。SQL 来源
	// 覆写以 db 文件打头。
	FamilySourcePaths func(scanRoot string, family Family[R]) []string
	// CheckAgentMember：parse_agent_messages 的成员归属校验（dsh 专用）；
	// nil 即通过。
	CheckAgentMember func(family Family[R], agentSessionID string) error
}

// FamilySpec：读取器真差异的必填声明面。可序列化（json tag）由各 Row 自行
// 保证：引擎的目录行持久缓存（rows_cache）存取行集合。
type FamilySpec[R FamilyRow] interface {
	App() SourceApp
	// Label：锁文案与默认 marker id 前缀（如 "Codex"）。
	Label() string
	// SummaryKind：family 摘要的合成方式。
	SummaryKind() SummaryKind
	// OverviewCounts：overview 计数派别。
	OverviewCounts() OverviewCounts
	// ScanRoot：实例 root 下的行扫描根；目录索引的 source_key。
	ScanRoot(root string) string
	// ListRows：物理行扫描。cache 是引擎实例的持久缓存句柄：聚合型来源可以
	// 在行级增量里用它存单行贡献（见 rows_cache 的贡献表），目录型来源忽略。
	ListRows(scanRoot string, cache *RowsDb) ([]R, error)
	// GroupFamilies：分组与 root 选取（孤儿语义等最危险的差异全部活在这里）。
	GroupFamilies(rows []R) ([]Family[R], error)
	// LoadMembers：逐成员一次扫描产出双半；返回与 members 等长同序。SQL 来源
	// 经 scanRoot 打开实例 db。
	LoadMembers(scanRoot string, members []R) ([]MemberTimeline, error)
	// AgentName：agent 名册显示名，与 marker 标题共用。
	AgentName(row R) string
	AgentLabel(family Family[R], row R) FamilyAgentLabel
	// Hooks：可选差异声明；典型来源大部分字段保持 nil。
	Hooks() *FamilyHooks[R]
}

// ---------------------------------------------------------------------------
// 双半时间线缓存
// ---------------------------------------------------------------------------

type dualHalfEntry struct {
	updatedAt int64
	// complete：两半齐全标志。Rust 以 Option 区分；Go 的 nil 切片也可能是
	// 合法空结果，用显式标志承载「已完整装载」。
	complete bool
	messages []SessionMessage
	events   []SessionEvent
}

// DualHalfCache：双半 load-through 缓存。命中要求 stamp 相等且两半齐全（pi
// 的既有语义）；loader 在锁外执行，miss 时单 loader 一次产出双半。
type DualHalfCache struct {
	label   string
	mu      sync.Mutex
	entries map[string]dualHalfEntry
}

// NewDualHalfCache 以锁文案构造。
func NewDualHalfCache(label string) *DualHalfCache {
	return &DualHalfCache{label: label, entries: map[string]dualHalfEntry{}}
}

// Load：命中返回缓存；miss 在锁外执行 loader 并回填。
func (c *DualHalfCache) Load(cacheKey string, updatedAt int64, load func() ([]SessionMessage, []SessionEvent, error)) (MemberTimeline, error) {
	c.mu.Lock()
	if entry, ok := c.entries[cacheKey]; ok && entry.updatedAt == updatedAt && entry.complete {
		timeline := MemberTimeline{Messages: entry.messages, Events: entry.events}
		c.mu.Unlock()
		return timeline, nil
	}
	c.mu.Unlock()

	messages, events, err := load()
	if err != nil {
		return MemberTimeline{}, err
	}
	c.mu.Lock()
	c.entries[cacheKey] = dualHalfEntry{updatedAt: updatedAt, complete: true, messages: messages, events: events}
	c.mu.Unlock()
	return MemberTimeline{Messages: messages, Events: events}, nil
}

// Clear：清空内存层。
func (c *DualHalfCache) Clear() {
	c.mu.Lock()
	c.entries = map[string]dualHalfEntry{}
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 三级摘要缓存（内存 → 持久层 → 整文件解析）
// ---------------------------------------------------------------------------

type summaryMemEntry struct {
	updatedAt int64
	summary   SessionSummary
}

// SummaryMemCache：路径级三级摘要缓存——内存 (canonical path_key, mtime) →
// summary_cache 持久层 → spec 的整文件解析，逐级回填。持久层句柄随引擎
// 实例注入。
type SummaryMemCache struct {
	app     SourceApp
	label   string
	mu      sync.Mutex
	entries map[string]summaryMemEntry
	db      *SummaryDb
}

// NewSummaryMemCache 构造（label 仅用于默认 loader 的报错文案）。
func NewSummaryMemCache(app SourceApp, label string, db *SummaryDb) *SummaryMemCache {
	return &SummaryMemCache{app: app, label: label, entries: map[string]summaryMemEntry{}, db: db}
}

// Close 释放底层持久连接（转发给 SummaryDb；db 可为 nil——SummaryKind 不
// 是 RootTranscript 的引擎没有摘要缓存）。
func (c *SummaryMemCache) Close() {
	if c.db != nil {
		_ = c.db.Close()
	}
}

// Load：内存命中 → 持久命中 → parse 解析并回填两级。
func (c *SummaryMemCache) Load(path string, parse func(path string) (SessionSummary, error)) (SessionSummary, error) {
	key := support.PathKey(path)
	updatedAt, err := support.FileModifiedTimestampMillis(path)
	if err != nil {
		return SessionSummary{}, err
	}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && entry.updatedAt == updatedAt {
		c.mu.Unlock()
		return entry.summary, nil
	}
	c.mu.Unlock()

	// 跨进程的持久缓存：冷启动时未变更的文件跳过整文件解析。
	if cached := c.db.Load(c.app, path, updatedAt); cached != nil {
		c.mu.Lock()
		c.entries[key] = summaryMemEntry{updatedAt: updatedAt, summary: *cached}
		c.mu.Unlock()
		return *cached, nil
	}

	summary, err := parse(path)
	if err != nil {
		return SessionSummary{}, err
	}
	c.mu.Lock()
	c.entries[key] = summaryMemEntry{updatedAt: updatedAt, summary: summary}
	c.mu.Unlock()
	c.db.Store(c.app, path, updatedAt, &summary)

	return summary, nil
}

// Clear：清空内存层（持久层不动——刷新语义只丢内存）。
func (c *SummaryMemCache) Clear() {
	c.mu.Lock()
	c.entries = map[string]summaryMemEntry{}
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 引擎本体
// ---------------------------------------------------------------------------

type cachedIndex[R FamilyRow] struct {
	freshness Freshness
	index     FamilyIndex[R]
}

// ReaderEngine：按 root 构造的引擎实例——持有该来源在该根下的全部缓存。
// 生产经 FamilyReader（env 根）；测试直接构造，完全脱离进程环境。
type ReaderEngine[R FamilyRow] struct {
	spec      FamilySpec[R]
	scanRoot  string
	mu        sync.Mutex
	index     *cachedIndex[R]
	timeline  *DualHalfCache
	summaries *SummaryMemCache
	rowsCache *RowsDb
}

// NewReaderEngine 构造实例。
func NewReaderEngine[R FamilyRow](spec FamilySpec[R], root string, storeDir string) *ReaderEngine[R] {
	scanRoot := spec.ScanRoot(root)
	label := spec.Label()
	engine := &ReaderEngine[R]{
		spec:      spec,
		scanRoot:  scanRoot,
		timeline:  NewDualHalfCache(label + " timeline"),
		rowsCache: RowsDbAt(storeDir),
	}
	if spec.SummaryKind() == SummaryRootTranscript {
		engine.summaries = NewSummaryMemCache(spec.App(), label+" summary", SummaryDbAt(storeDir))
	}
	return engine
}

// Close 释放引擎持有的持久缓存连接。引擎被丢弃（换根重建）前必须调用：
// *sql.DB 不会随 GC 关闭，Rust 侧靠 Connection 的 Drop 释放，Go 侧丢弃点
// 显式负责。
func (e *ReaderEngine[R]) Close() {
	if e.summaries != nil {
		e.summaries.Close()
	}
	e.rowsCache.Close()
}

func (e *ReaderEngine[R]) hooks() *FamilyHooks[R] {
	return e.spec.Hooks()
}

func (e *ReaderEngine[R]) displayLabel() string {
	if hook := e.hooks().DisplayLabel; hook != nil {
		return hook(e.spec.Label())
	}
	return e.spec.Label()
}

func (e *ReaderEngine[R]) indexLockLabel() string {
	if hook := e.hooks().IndexLockLabel; hook != nil {
		return hook(e.spec.Label())
	}
	return fmt.Sprintf("%s family index", e.spec.Label())
}

func (e *ReaderEngine[R]) indexFreshness() (Freshness, error) {
	if hook := e.hooks().IndexFreshness; hook != nil {
		return hook(e.scanRoot)
	}
	stamp, err := subtreeMtimeMax(e.scanRoot)
	if err != nil {
		return Freshness{}, err
	}
	return StampFreshness(stamp), nil
}

func (e *ReaderEngine[R]) rowUsage(row R) *SessionTokenUsage {
	if hook := e.hooks().RowUsage; hook != nil {
		return hook(row)
	}
	return nil
}

func (e *ReaderEngine[R]) rowModels(row R) []SessionModel {
	if hook := e.hooks().RowModels; hook != nil {
		return hook(row)
	}
	return nil
}

func (e *ReaderEngine[R]) familyUsage(family Family[R]) (*SessionTokenUsage, error) {
	if hook := e.hooks().FamilyUsage; hook != nil {
		return hook(e, family)
	}
	return family.SumTokenUsage(func(row R) *SessionTokenUsage { return e.rowUsage(row) }), nil
}

func (e *ReaderEngine[R]) familyModels(family Family[R]) ([]SessionModel, error) {
	if hook := e.hooks().FamilyModels; hook != nil {
		return hook(e, family)
	}
	tally := ModelTally{}
	for i := range family.Members {
		tally.Absorb(e.rowModels(family.Members[i]))
	}
	return tally.IntoModels(), nil
}

func (e *ReaderEngine[R]) familyFreshness(family Family[R]) (int64, error) {
	if hook := e.hooks().FamilyFreshness; hook != nil {
		return hook(e.scanRoot, family)
	}
	return memberMtimeMax(family)
}

func (e *ReaderEngine[R]) isSubagentMember(family Family[R], row R) bool {
	if hook := e.hooks().IsSubagentMember; hook != nil {
		return hook(family, row)
	}
	return row.MemberID() != family.Root.MemberID()
}

func (e *ReaderEngine[R]) marker(memberID string, row R) MarkerShape {
	if hook := e.hooks().Marker; hook != nil {
		return hook(memberID, row)
	}
	return LabeledMarker(e.spec.Label(), memberID)
}

func (e *ReaderEngine[R]) rootExtraEvents(family Family[R]) ([]SessionEvent, error) {
	if hook := e.hooks().RootExtraEvents; hook != nil {
		return hook(family)
	}
	return nil, nil
}

func (e *ReaderEngine[R]) countFamilyRecords(family Family[R]) (int, int, error) {
	if hook := e.hooks().CountFamilyRecords; hook != nil {
		return hook(e.scanRoot, family)
	}
	return 0, 0, fmt.Errorf("%s 未声明 OverviewCounts::Declared", e.spec.Label())
}

func (e *ReaderEngine[R]) familySourcePaths(family Family[R]) []string {
	if hook := e.hooks().FamilySourcePaths; hook != nil {
		return hook(e.scanRoot, family)
	}
	return family.SourcePaths()
}

func (e *ReaderEngine[R]) checkAgentMember(family Family[R], agentSessionID string) error {
	if hook := e.hooks().CheckAgentMember; hook != nil {
		return hook(family, agentSessionID)
	}
	return nil
}

// FamilyIndex：目录索引的 load-through 缓存入口——内存索引 → 持久行缓存 →
// 物理行扫描。
func (e *ReaderEngine[R]) FamilyIndex() (FamilyIndex[R], error) {
	freshness, err := e.indexFreshness()
	if err != nil {
		return FamilyIndex[R]{}, err
	}

	e.mu.Lock()
	if e.index != nil && e.index.freshness.equal(freshness) {
		index := e.index.index
		e.mu.Unlock()
		return index, nil
	}
	e.mu.Unlock()

	// 持久行缓存：进程重启后 freshness 未变时跳过物理行扫描（OpenCode 的
	// 全表聚合、dsh 的整份解压、codex 的逐文件首行）。失效口径与内存索引
	// 同一份 freshness，信任窗口只是延伸到进程之外。
	rootKey := support.PathKey(e.scanRoot)
	stamp := freshness.CacheStamp()
	if rows, ok := LoadRows[R](e.rowsCache, e.spec.App(), rootKey, stamp); ok {
		return e.rebuildIndex(freshness, rows)
	}

	rows, err := e.spec.ListRows(e.scanRoot, e.rowsCache)
	if err != nil {
		return FamilyIndex[R]{}, err
	}
	StoreRows[R](e.rowsCache, e.spec.App(), rootKey, stamp, rows)
	return e.rebuildIndex(freshness, rows)
}

func (e *ReaderEngine[R]) rebuildIndex(freshness Freshness, rows []R) (FamilyIndex[R], error) {
	families, err := e.spec.GroupFamilies(rows)
	if err != nil {
		return FamilyIndex[R]{}, err
	}
	index := BuildFamilyIndex(families)

	e.mu.Lock()
	e.index = &cachedIndex[R]{freshness: freshness, index: index}
	e.mu.Unlock()

	return index, nil
}

// FamilyForID：会话身份的唯一寻址入口——id → family 直查，未命中文案逐字
// 保留各家 not-found 契约（dsh 的无 "for" 变体由本公式原样还原）。
func (e *ReaderEngine[R]) FamilyForID(sourceSessionID string) (Family[R], error) {
	index, err := e.FamilyIndex()
	if err != nil {
		return Family[R]{}, err
	}
	family, ok := index.FamilyForID(sourceSessionID)
	if !ok {
		return Family[R]{}, fmt.Errorf("Could not find %s session %s", e.displayLabel(), sourceSessionID)
	}
	return family, nil
}

// PathSummary：路径级三级摘要；仅 SummaryRootTranscript 的来源可用。
func (e *ReaderEngine[R]) PathSummary(path string) (SessionSummary, error) {
	if e.summaries == nil {
		return SessionSummary{}, fmt.Errorf("%s 未声明 SummaryKind::RootTranscript", e.spec.Label())
	}
	return e.summaries.Load(path, e.parseFullSummary)
}

func (e *ReaderEngine[R]) parseFullSummary(path string) (SessionSummary, error) {
	if hook := e.hooks().ParseFullSummary; hook != nil {
		return hook(path)
	}
	return SessionSummary{}, fmt.Errorf("%s 未声明 SummaryKind::RootTranscript", e.spec.Label())
}

func (e *ReaderEngine[R]) summaryFromRows(root R) SessionSummary {
	if hook := e.hooks().SummaryFromRows; hook != nil {
		return hook(root)
	}
	panic(fmt.Sprintf("%s 未声明 SummaryKind::Rows", e.spec.Label()))
}

func (e *ReaderEngine[R]) familySummary(family Family[R]) (SessionSummary, error) {
	var summary SessionSummary
	var err error
	switch e.spec.SummaryKind() {
	case SummaryRootTranscript:
		summary, err = e.PathSummary(family.Root.MemberPath())
	case SummaryRows:
		summary = e.summaryFromRows(family.Root)
	}
	if err != nil {
		return SessionSummary{}, err
	}

	family.ApplySummaryAggregates(&summary)

	usage, err := e.familyUsage(family)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.TokenUsage = usage

	models, err := e.familyModels(family)
	if err != nil {
		return SessionSummary{}, err
	}
	summary.Models = models

	return summary, nil
}

func (e *ReaderEngine[R]) familyTimeline(family Family[R]) (MemberTimeline, error) {
	cacheKey := family.Root.MemberID()
	freshness, err := e.familyFreshness(family)
	if err != nil {
		return MemberTimeline{}, err
	}
	return e.timeline.Load(cacheKey, freshness, func() ([]SessionMessage, []SessionEvent, error) {
		return e.loadFamilyTimeline(family)
	})
}

// loadFamilyTimeline：装配管线——前置事件 → markers → 各成员双半（session_id
// 兜底）→ 各半终排。marker 与成员项的入列顺序不影响结果（终排键
// (timestamp, id) 唯一）。
func (e *ReaderEngine[R]) loadFamilyTimeline(family Family[R]) ([]SessionMessage, []SessionEvent, error) {
	messages := make([]SessionMessage, 0)
	events, err := e.rootExtraEvents(family)
	if err != nil {
		return nil, nil, err
	}

	timelines, err := e.spec.LoadMembers(e.scanRoot, family.Members)
	if err != nil {
		return nil, nil, err
	}
	for i, timeline := range timelines {
		row := family.Members[i]
		if e.isSubagentMember(family, row) {
			messages = append(messages, e.markerMessage(row))
			events = append(events, e.markerEvent(row))
		}

		memberID := row.MemberID()
		// 用户输入的模型归属按成员内部推导：子代理线程有自己的轮次，跨成员
		// 的相邻关系没有意义。复制一份再改，不污染缓存里的原消息。
		memberMessages := make([]SessionMessage, len(timeline.Messages))
		copy(memberMessages, timeline.Messages)
		FillUserMessageModels(memberMessages)
		for j := range memberMessages {
			message := memberMessages[j]
			if message.SessionID == nil {
				id := memberID
				message.SessionID = &id
			}
			messages = append(messages, message)
		}
		for _, event := range timeline.Events {
			event := event
			if event.SessionID == nil {
				id := memberID
				event.SessionID = &id
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

func (e *ReaderEngine[R]) markerMessage(row R) SessionMessage {
	memberID := row.MemberID()
	shape := e.marker(memberID, row)
	title := e.spec.AgentName(row)

	payload := map[string]any{
		"type":       "subagent_started",
		"session_id": memberID,
		"title":      title,
	}
	mergePayload(payload, shape.MessageExtras)

	text := fmt.Sprintf("Sub-agent session: %s\n%s", title, memberID)
	return SessionMessage{
		ID:        shape.MessageID,
		Role:      "assistant",
		Timestamp: row.MemberCreatedAt(),
		Blocks:    []ContentBlock{{Kind: "output_text", Text: &text, Payload: payload}},
		SessionID: &memberID,
	}
}

func (e *ReaderEngine[R]) markerEvent(row R) SessionEvent {
	memberID := row.MemberID()
	shape := e.marker(memberID, row)
	title := e.spec.AgentName(row)

	payload := map[string]any{
		"session_id": memberID,
		"title":      title,
	}
	mergePayload(payload, shape.EventExtras)

	return SessionEvent{
		ID:        shape.EventID,
		Kind:      "subagent_started",
		Timestamp: row.MemberCreatedAt(),
		Summary:   fmt.Sprintf("Sub-agent session started: %s", title),
		Payload:   payload,
		SessionID: &memberID,
	}
}

// Clear：clear_cache 是 rescan 语义——只丢内存层（目录索引、时间线、内存
// 摘要），持久层不动。
func (e *ReaderEngine[R]) Clear() {
	e.timeline.Clear()
	if e.summaries != nil {
		e.summaries.Clear()
	}
	e.mu.Lock()
	e.index = nil
	e.mu.Unlock()
}

// —— SessionReader 契约 ——

// ListEntries：目录条目（列表分页的数据源），按时间倒序。
func (e *ReaderEngine[R]) ListEntries() ([]SessionFileEntry, error) {
	index, err := e.FamilyIndex()
	if err != nil {
		return nil, err
	}
	entries := make([]SessionFileEntry, 0, len(index.Families))
	for i := range index.Families {
		family := index.Families[i]
		summary, err := e.familySummary(family)
		if err != nil {
			return nil, err
		}
		entries = append(entries, SessionFileEntry{
			Path:            family.Root.MemberPath(),
			SourceSessionID: family.Root.FamilyRootID(),
			SortTimestamp:   derefDefault(family.UpdatedAt()),
			Summary:         &summary,
		})
	}
	sortEntries(entries)
	return entries, nil
}

// ClearCache：rescan 入口。
func (e *ReaderEngine[R]) ClearCache() error {
	e.Clear()
	return nil
}

// ParseSummary：会话身份 → family 摘要。
func (e *ReaderEngine[R]) ParseSummary(sourceSessionID string) (SessionSummary, error) {
	family, err := e.FamilyForID(sourceSessionID)
	if err != nil {
		return SessionSummary{}, err
	}
	return e.familySummary(family)
}

// ParseOverview：单会话详情概要。
func (e *ReaderEngine[R]) ParseOverview(sourceSessionID string) (SessionOverview, error) {
	family, err := e.FamilyForID(sourceSessionID)
	if err != nil {
		return SessionOverview{}, err
	}
	summary, err := e.familySummary(family)
	if err != nil {
		return SessionOverview{}, err
	}

	var messageCount, eventCount *int
	switch e.spec.OverviewCounts() {
	case OverviewOmitted:
	case OverviewTimeline:
		timeline, err := e.familyTimeline(family)
		if err != nil {
			return SessionOverview{}, err
		}
		messageCount = intPointer(len(timeline.Messages))
		eventCount = intPointer(len(timeline.Events))
	case OverviewDeclared:
		rawMessages, rawEvents, err := e.countFamilyRecords(family)
		if err != nil {
			return SessionOverview{}, err
		}
		markers := len(family.Members) - 1
		if markers < 0 {
			markers = 0
		}
		messageCount = intPointer(rawMessages + markers)
		eventCount = intPointer(rawEvents + markers)
	}

	return SessionOverview{
		Summary:      summary,
		SourcePaths:  e.familySourcePaths(family),
		MessageCount: messageCount,
		EventCount:   eventCount,
		Agents:       FamilyAgents(family, func(row R) FamilyAgentLabel { return e.spec.AgentLabel(family, row) }),
	}, nil
}

// ParseMessages：详情正文全量。
func (e *ReaderEngine[R]) ParseMessages(sourceSessionID string) ([]SessionMessage, error) {
	family, err := e.FamilyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	timeline, err := e.familyTimeline(family)
	if err != nil {
		return nil, err
	}
	return timeline.Messages, nil
}

// ParseEvents：时间线事件流。
func (e *ReaderEngine[R]) ParseEvents(sourceSessionID string) ([]SessionEvent, error) {
	family, err := e.FamilyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	timeline, err := e.familyTimeline(family)
	if err != nil {
		return nil, err
	}
	return timeline.Events, nil
}

// ParseAgentMessages：子代理入口弹窗——marker 归属校验后按 agent session id
// 过滤 family 消息。
func (e *ReaderEngine[R]) ParseAgentMessages(sourceSessionID string, agentSessionID string) ([]SessionMessage, error) {
	family, err := e.FamilyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	if err := e.checkAgentMember(family, agentSessionID); err != nil {
		return nil, err
	}
	timeline, err := e.familyTimeline(family)
	if err != nil {
		return nil, err
	}
	return AgentMessages(timeline.Messages, agentSessionID), nil
}

// ---------------------------------------------------------------------------
// static 前置（env 根 → 实例）
// ---------------------------------------------------------------------------

// FamilyReader：每 reader 一个 static 前置——按 root() 解析结果缓存引擎实例，
// 换根即整实例重建（旧实例释放）；对外就是 Reader。
type FamilyReader[R FamilyRow] struct {
	spec FamilySpec[R]
	root func() (string, error)

	mu         sync.Mutex
	slotRoot   string
	slotEngine *ReaderEngine[R]
}

// NewFamilyReader 构造 static 前置。
func NewFamilyReader[R FamilyRow](spec FamilySpec[R], root func() (string, error)) *FamilyReader[R] {
	return &FamilyReader[R]{spec: spec, root: root}
}

// Engine：同根复用实例，换根重建；持久缓存目录随实例解析（测试切根即换库）。
func (r *FamilyReader[R]) Engine() (*ReaderEngine[R], error) {
	root, err := r.root()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.slotEngine != nil && r.slotRoot == root {
		return r.slotEngine, nil
	}
	// 换根重建：旧引擎的持久缓存连接显式关闭（无 Drop 语义，见 Engine.Close）。
	if r.slotEngine != nil {
		r.slotEngine.Close()
	}

	// 持久缓存目录随实例解析（测试直构时经参数注入，脱离进程 env）。
	storeDir, err := reinsStoreDir()
	if err != nil {
		return nil, err
	}
	engine := NewReaderEngine[R](r.spec, root, storeDir)
	r.slotRoot = root
	r.slotEngine = engine
	return engine, nil
}

// ListEntries 实现 Reader。
func (r *FamilyReader[R]) ListEntries() ([]SessionFileEntry, error) {
	engine, err := r.Engine()
	if err != nil {
		return nil, err
	}
	return engine.ListEntries()
}

// ClearCache 实现 Reader。
func (r *FamilyReader[R]) ClearCache() error {
	engine, err := r.Engine()
	if err != nil {
		return err
	}
	return engine.ClearCache()
}

// ParseSummary 实现 Reader。
func (r *FamilyReader[R]) ParseSummary(sourceSessionID string) (SessionSummary, error) {
	engine, err := r.Engine()
	if err != nil {
		return SessionSummary{}, err
	}
	return engine.ParseSummary(sourceSessionID)
}

// ParseOverview 实现 Reader。
func (r *FamilyReader[R]) ParseOverview(sourceSessionID string) (SessionOverview, error) {
	engine, err := r.Engine()
	if err != nil {
		return SessionOverview{}, err
	}
	return engine.ParseOverview(sourceSessionID)
}

// ParseMessages 实现 Reader。
func (r *FamilyReader[R]) ParseMessages(sourceSessionID string) ([]SessionMessage, error) {
	engine, err := r.Engine()
	if err != nil {
		return nil, err
	}
	return engine.ParseMessages(sourceSessionID)
}

// ParseEvents 实现 Reader。
func (r *FamilyReader[R]) ParseEvents(sourceSessionID string) ([]SessionEvent, error) {
	engine, err := r.Engine()
	if err != nil {
		return nil, err
	}
	return engine.ParseEvents(sourceSessionID)
}

// ParseAgentMessages 实现 Reader。
func (r *FamilyReader[R]) ParseAgentMessages(sourceSessionID string, agentSessionID string) ([]SessionMessage, error) {
	engine, err := r.Engine()
	if err != nil {
		return nil, err
	}
	return engine.ParseAgentMessages(sourceSessionID, agentSessionID)
}

// ---------------------------------------------------------------------------
// 共享小件
// ---------------------------------------------------------------------------

// ScanFiles：枚举后的行装载，按来源声明的坏文件策略处理解析失败。
func ScanFiles[R any](files []string, parse func(path string) (R, error), policy RowErrorPolicy, label string) ([]R, error) {
	rows := make([]R, 0, len(files))
	for _, path := range files {
		row, err := parse(path)
		if err != nil {
			switch policy {
			case RowAbort:
				return nil, err
			case RowSkipSilently:
			case RowSkipLogged:
				// 单个损坏文件跳过，不让一个坏文件隐藏其余会话。
				logger.Error(fmt.Sprintf("%s session skipped %s: %v", label, path, err))
			}
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// subtreeMtimeMax：扫描根与其下 jsonl 文件 mtime 的最大值。扫描根可能还没
// 落地（codex 的 sessions 子目录随首次会话才创建），缺失按空目录处理，让
// 索引走到空列表分支而不是在 metadata 读取上失败。
func subtreeMtimeMax(root string) (int64, error) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return 0, nil
	}

	latest, err := support.FileModifiedTimestampMillis(root)
	if err != nil {
		return 0, err
	}
	for _, path := range support.EnumerateJSONLFiles(root) {
		stamp, err := support.FileModifiedTimestampMillis(path)
		if err != nil {
			return 0, err
		}
		if stamp > latest {
			latest = stamp
		}
	}
	return latest, nil
}

// memberMtimeMax：timeline 默认失效口径——成员文件 mtime 最大值。
func memberMtimeMax[R FamilyRow](family Family[R]) (int64, error) {
	latest := int64(0)
	for i := range family.Members {
		stamp, err := support.FileModifiedTimestampMillis(family.Members[i].MemberPath())
		if err != nil {
			return 0, err
		}
		if stamp > latest {
			latest = stamp
		}
	}
	return latest, nil
}

// mergePayload：把 extras 的对象键并入基底（extras 不是对象时忽略——对齐
// Rust 的 Value::Object 守卫）。
func mergePayload(base map[string]any, extras any) {
	extraObject, ok := extras.(map[string]any)
	if !ok {
		return
	}
	for key, value := range extraObject {
		base[key] = value
	}
}

func derefDefault(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func intPointer(value int) *int {
	return &value
}

func joinPath(base string, part string) string {
	return filepath.Join(base, part)
}
