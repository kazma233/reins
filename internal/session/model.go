// Package session：会话域基础设施层（纯逻辑与持久缓存）。7 个读取器、reader
// engine、catalog、sources 与 usage 统计的接缝见 timeline.go 的 Reader 接口与
// session.go 的注释。
//
// wire 契约经 frontend/bindings/ 暴露给前端（wails 生成、不手改）：字段
// camelCase、SourceApp 枚举值 snake_case、Option 字段区分「省略」（omitempty）
// 与「null」（指针类型）。
//
// 切片约定：wire DTO 的切片字段恒输出 []，构造时必须给 []T{} 而不是 nil——
// nil 会被 encoding/json 序列化成 null，破坏前端消费契约（wire 测试逐类型
// 钉住该行为）。map 字段同理（nil → null 与 JSON 的 {} 不一致），涉及跨
// 二进制共读的 UsageHourBucket 用 MarshalJSON 显式归一。
package session

import (
	"fmt"
	"sort"
	"strings"

	"reins/internal/agents"
)

// SourceApp 与 agents 清单共用同一键类型：session 域自身不再定义第二份枚举
// （GLOSSARY 工具清单词条；Rust SourceApp 的 Go 对应物寄居 agents，wire 拼写
// 即 snake_case 字符串）。合法值集合由 ParseSourceApp 守住。
type SourceApp = agents.SourceApp

// SourceApp 常量重导出：域内代码读 session.SourceCodex 而不越过包边界。
const (
	SourceCodex      = agents.SourceCodex
	SourceClaudeCode = agents.SourceClaudeCode
	SourceOpenCode   = agents.SourceOpenCode
	SourcePi         = agents.SourcePi
	SourceGrokBuild  = agents.SourceGrokBuild
	SourceZcode      = agents.SourceZcode
	SourceDsh        = agents.SourceDsh
)

// ParseSourceApp 对齐 Rust FromStr：未知值报错而不是静默成非法来源。
func ParseSourceApp(value string) (SourceApp, error) {
	switch value {
	case "codex":
		return SourceCodex, nil
	case "claude_code":
		return SourceClaudeCode, nil
	case "opencode":
		return SourceOpenCode, nil
	case "pi":
		return SourcePi, nil
	case "grokbuild":
		return SourceGrokBuild, nil
	case "zcode":
		return SourceZcode, nil
	case "dsh":
		return SourceDsh, nil
	default:
		return "", fmt.Errorf("Unsupported source app: %s", value)
	}
}

// ---------------------------------------------------------------------------
// 会话 wire DTO（sessions/generated）
// ---------------------------------------------------------------------------

// SourceStatus：来源探测结果。
type SourceStatus struct {
	App          SourceApp `json:"app"`
	Available    bool      `json:"available"`
	RootPath     *string   `json:"rootPath"`
	SessionCount int       `json:"sessionCount"`
	Note         *string   `json:"note"`
}

// ---------------------------------------------------------------------------
// wire 切片类型的命名封装
// ---------------------------------------------------------------------------

// SessionModel：会话用过的模型。provider 是各工具自己配置里的 provider id，
// 不是统一命名；Codex/OpenCode/Pi/DSH/ZCode 有该字段，Claude Code 与 Grok
// Build 的转录里根本没有。一个会话先后换过模型时会有多项。
//
// provider 的 serde None 用空串承载：NewSessionModel 归一后 Some 必非空，
// None ≡ 空串，wire 形状（省略字段）与值语义两两一致，结构体也因此保持
// 可比较（== 即 Rust 的 PartialEq，ModelTally 去重依赖它）。
type SessionModel struct {
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

// NewSessionModel 对齐 Rust SessionModel::new：provider 去空白、空则视为无。
// model 原样保留（去空白是 Record 的职责）。
func NewSessionModel(provider string, model string) SessionModel {
	sm := SessionModel{Model: model}
	if trimmed := strings.TrimSpace(provider); trimmed != "" {
		sm.Provider = trimmed
	}
	return sm
}

// SessionTokenUsage：各来源转录的 usage 字段口径不一（Codex 的 input 含缓存
// 命中、OpenCode 把 reasoning 与 output 分开），读取器负责归一到这四项后再
// 进入统计。
type SessionTokenUsage struct {
	InputTokens      uint64 `json:"inputTokens"`
	OutputTokens     uint64 `json:"outputTokens"`
	CacheReadTokens  uint64 `json:"cacheReadTokens"`
	CacheWriteTokens uint64 `json:"cacheWriteTokens"`
}

// Accumulate：输出统一含 reasoning tokens，否则跨来源不可比。
func (u *SessionTokenUsage) Accumulate(next *SessionTokenUsage) {
	u.InputTokens += next.InputTokens
	u.OutputTokens += next.OutputTokens
	u.CacheReadTokens += next.CacheReadTokens
	u.CacheWriteTokens += next.CacheWriteTokens
}

// UsageDayPoint：用量统计的日聚合点。day 是本地时区 YYYY-MM-DD，由消耗事件
// （消息/turn/token_count 事件）的时间戳归桶而来，跨天会话天然按天拆分。
type UsageDayPoint struct {
	Day   string            `json:"day"`
	Usage SessionTokenUsage `json:"usage"`
}

type UsageSourceStats struct {
	SourceApp SourceApp       `json:"sourceApp"`
	Days      []UsageDayPoint `json:"days"`
	// 今日窗口的小时序列：本地时区今天有消耗的小时，前端补齐 0..23。
	TodayHours []UsageHourPoint `json:"todayHours"`
	// 有消耗记录的会话数；jsonl 来源按产出日桶的转录文件计（resume 段与
	// subagent 线程是独立文件，与列表页 family 求和口径一致）。
	SessionCount int `json:"sessionCount"`
	// 今日产生消耗的会话数，今日窗口的会话指标用它而不是全周期值。
	TodaySessionCount int `json:"todaySessionCount"`
	// 按模型的日切片：只有取得到模型信息的消耗事件进入，取不到的来源为
	// 空数组，模型筛选对其不可见，也不与 days 总量强行对齐。
	ModelDays []UsageModelDayPoint `json:"modelDays"`
	// 按模型的今日小时切片，口径同 modelDays。
	ModelTodayHours []UsageModelHourPoint `json:"modelTodayHours"`
}

// UsageModelDayPoint：模型维度的日聚合点，同一模型跨小时合并成天。
type UsageModelDayPoint struct {
	Model string            `json:"model"`
	Day   string            `json:"day"`
	Usage SessionTokenUsage `json:"usage"`
}

// UsageModelHourPoint：模型维度的今日小时点。
type UsageModelHourPoint struct {
	Model string            `json:"model"`
	Hour  uint8             `json:"hour"`
	Usage SessionTokenUsage `json:"usage"`
}

// UsageHourPoint：今日窗口的小时点；hour 是本地时区 0..23。
type UsageHourPoint struct {
	Hour  uint8             `json:"hour"`
	Usage SessionTokenUsage `json:"usage"`
}

// UsageStats：全量日序列是唯一事实源，时间窗裁剪与指标推导都在前端完成。
type UsageStats struct {
	// 只含当前可用且有数据的来源。
	Sources []UsageSourceStats `json:"sources"`
}

// UsageHourBucket：单个小时桶，usage 是该小时全量；models 是按模型的切片，
// 取不到模型信息的事件只进总量。持久缓存直接序列化整个结构，字段变更须
// bump 缓存版本（usage_day_cache 的 DBFileName）。
type UsageHourBucket struct {
	Usage  SessionTokenUsage            `json:"usage"`
	Models map[string]SessionTokenUsage `json:"models"`
}

// Models 由 accumulateModel 惰性建表，从不积累模型的空桶保持零值（nil
// map → wire 上是 null）。不设 MarshalJSON 是刻意的：自定义序列化器会让
// wails bindings 把整个类型渲染成 any；而本缓存的读写两端都是 Go，null
// 反序列化为 nil map 按空表处理，不会像 Rust serde 那样整行 miss，旧行
// （Rust 写入，恒为对象）也兼容。

// usageHourBucketAlias：绕开 MarshalJSON 自递归的同形别名。
type usageHourBucketAlias struct {
	Usage  SessionTokenUsage            `json:"usage"`
	Models map[string]SessionTokenUsage `json:"models"`
}

// Accumulate 合并另一个桶（缓存聚合用）。
func (b *UsageHourBucket) Accumulate(next *UsageHourBucket) {
	b.Usage.Accumulate(&next.Usage)
	for model, usage := range next.Models {
		b.accumulateModel(model, &usage)
	}
}

// AccumulateEvent 记入一条消耗事件；model 为空串表示取不到模型信息，
// 只进总量（Rust Option<&str> 的 None 由空串承载）。
func (b *UsageHourBucket) AccumulateEvent(model string, usage *SessionTokenUsage) {
	b.Usage.Accumulate(usage)
	if model != "" {
		b.accumulateModel(model, usage)
	}
}

// accumulateModel 惰性建 map：对齐 Rust 的 entry().or_default() 写入时建表，
// 空桶保持零值。
func (b *UsageHourBucket) accumulateModel(model string, usage *SessionTokenUsage) {
	if b.Models == nil {
		b.Models = make(map[string]SessionTokenUsage)
	}
	entry := b.Models[model]
	entry.Accumulate(usage)
	b.Models[model] = entry
}

// ---------------------------------------------------------------------------
// 会话 wire DTO（续）
// ---------------------------------------------------------------------------

// SessionSummary：列表页与会话身份的持久单元。
type SessionSummary struct {
	SourceApp       SourceApp `json:"sourceApp"`
	SourceSessionID string    `json:"sourceSessionId"`
	Title           string    `json:"title"`
	Cwd             *string   `json:"cwd"`
	GitBranch       *string   `json:"gitBranch"`
	TranscriptPath  string    `json:"transcriptPath"`
	// i64 时间戳经 invoke 以 JSON number 传输，与 Rust ts(type = "number | null") 一致。
	CreatedAt *int64 `json:"createdAt"`
	UpdatedAt *int64 `json:"updatedAt"`
	// 列表扫描时聚合；无 usage 数据的来源（GrokBuild）与旧缓存行为 nil。
	TokenUsage *SessionTokenUsage `json:"tokenUsage,omitempty"`
	// 会话用过的模型（family 含子代理线程），首项是主模型；取不到模型信息
	// 的来源为空。Claude Code 与 Grok Build 的 provider 恒为空串。
	// wire 契约：恒输出数组（空 → []），构造时不得给 nil。
	Models []SessionModel `json:"models"`
	// 会话在当前来源里的存储状态，由各读取器按自家契约填写：
	// archived = 来源已把会话移出默认集合（codex 移到 archived_sessions/、
	// dsh 写进 workspace.json 的归档集、zcode/opencode 的 time_archived）；
	// compressed = 转录在磁盘上不是该来源的明文常态（codex 的 .jsonl.zst）。
	// 无归档/无压缩契约的来源恒为 false（pi、grokbuild、claude code）。
	Archived   bool `json:"archived"`
	Compressed bool `json:"compressed"`
}

// ContentBlock：消息正文的一个展示块。
type ContentBlock struct {
	Kind       string  `json:"kind"`
	Text       *string `json:"text"`
	ToolName   *string `json:"toolName"`
	ToolCallID *string `json:"toolCallId"`
	// Pi toolResult 等来源携带工具失败标记，读取时原样透出，前端据此展示
	// 失败状态。serde skip → 省略字段。
	IsError *bool `json:"isError,omitempty"`
	// payload 保持 opaque（Rust 注释：递归 JsonValue 绑定会让 Vue 的
	// UnwrapRef 爆 TS2589），nil ≡ serde None ≡ JSON null。
	Payload any `json:"payload"`
}

// SessionMessage：会话详情的一条消息。
type SessionMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Timestamp *int64 `json:"timestamp"`
	// wire 契约：恒输出数组，构造时不得给 nil。
	Blocks []ContentBlock `json:"blocks"`
	// 这条消息所属轮次用的模型；过程消息与取不到模型信息的来源（Grok Build）
	// 为 nil，由各读取器按自家契约填写。
	Model     *SessionModel `json:"model,omitempty"`
	SessionID *string       `json:"sessionId,omitempty"`
}

// FillUserMessageModels：用户输入自身不带模型，它的归属由这一轮的回答决定
// ——同一次扫描里其后第一条带模型的消息。助手消息在读取器里已按来源直接
// 标注（Codex/DSH 是整轮继承），这里只补 user 角色，已经标注过的不动。
func FillUserMessageModels(messages []SessionMessage) {
	var following *SessionModel
	for i := len(messages) - 1; i >= 0; i-- {
		message := &messages[i]
		if message.Model != nil {
			following = message.Model
			continue
		}
		if message.Role == "user" && following != nil {
			// 逐消息复制：Rust 是 following.clone()，共享指针会让下游对单条
			// 消息的局部改动波及其余 user 消息。
			copied := *following
			message.Model = &copied
		}
	}
}

// SessionEvent：时间线的一条过程事件。
type SessionEvent struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Timestamp *int64  `json:"timestamp"`
	Summary   string  `json:"summary"`
	Payload   any     `json:"payload"`
	SessionID *string `json:"sessionId,omitempty"`
}

// SessionAgent：会话详情里的子代理入口。
type SessionAgent struct {
	SessionID string `json:"sessionId"`
	Label     string `json:"label"`
	IsRoot    bool   `json:"isRoot"`
}

// SessionOverview：单会话详情页载荷。
type SessionOverview struct {
	Summary      SessionSummary `json:"summary"`
	SourcePaths  []string       `json:"sourcePaths"`
	MessageCount *int           `json:"messageCount"`
	EventCount   *int           `json:"eventCount"`
	Agents       []SessionAgent `json:"agents"`
}

// SessionPage：列表页分页。
type SessionPage struct {
	// nil 表示合并视图（跨来源分页），非 nil 表示单一来源。
	SourceApp  *SourceApp       `json:"sourceApp"`
	Sessions   []SessionSummary `json:"sessions"`
	Offset     int              `json:"offset"`
	Limit      int              `json:"limit"`
	NextOffset *int             `json:"nextOffset"`
	TotalCount int              `json:"totalCount"`
	HasMore    bool             `json:"hasMore"`
}

// SessionRefreshResult：列表刷新载荷。
type SessionRefreshResult struct {
	// nil 表示合并视图（跨来源分页）。
	SelectedSource *SourceApp     `json:"selectedSource"`
	Sources        []SourceStatus `json:"sources"`
	Page           SessionPage    `json:"page"`
}

// ---------------------------------------------------------------------------
// 非 wire 类型（域内数据结构）
// ---------------------------------------------------------------------------

// SessionFileEntry：一个转录文件（或 SQL 来源）的目录条目。
type SessionFileEntry struct {
	Path string
	// 条目自身的会话身份；列表层用它按 id 取 summary（pi 的条目不带
	// summary，身份只能来自扫描时的 header）。
	SourceSessionID string
	SortTimestamp   int64
	Summary         *SessionSummary
}

// TimelineRecord：时间线记录，Message/Event 恰有一个非 nil（Rust 枚举的两
// 个变体）。family_timeline 与 reader_engine 按 nil 区分变体。
type TimelineRecord struct {
	Message *SessionMessage
	Event   *SessionEvent
}

// SummaryAccumulator：扫描期逐步聚出一份 SessionSummary 的中间态（零值即可用，
// 对齐 Rust derive(Default)）。
type SummaryAccumulator struct {
	SessionID  *string
	Title      *string
	Cwd        *string
	GitBranch  *string
	CreatedAt  *int64
	UpdatedAt  *int64
	TokenUsage *SessionTokenUsage
	Models     ModelTally
	Archived   bool
	Compressed bool
}

// ---------------------------------------------------------------------------
// ModelTally
// ---------------------------------------------------------------------------

// modelEntry：同一 (provider, model) 的计数项。count 为 0 表示 absorb 保序
// 并入（family 合并），与 record 计数项两种语义各自成对使用、不混用。
type modelEntry struct {
	model SessionModel
	count int
}

// ModelTally：会话内模型计数。同一 (provider, model) 归一项，输出按使用次数
// 降序、同次数按首次出现顺序，首项即用于列表展示的主模型。会话级与 family
// 级合并都走这里，避免两处各写一份排序口径。
type ModelTally struct {
	entries []modelEntry
}

// Record 记一次模型使用。provider 空串 ≡ Rust None；model 去空白后为空则忽略。
func (t *ModelTally) Record(provider string, model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	candidate := NewSessionModel(provider, model)
	for i := range t.entries {
		if t.entries[i].model == candidate {
			t.entries[i].count++
			return
		}
	}
	t.entries = append(t.entries, modelEntry{model: candidate, count: 1})
}

// Absorb：family 级合并。成员各自的列表已按用量排好序，这里只做保序去重
// ——跨成员的调用次数不可比，若让"出现在更多成员里"的模型顶上去，列表
// 角标会指到子代理偶尔一用的模型上。会话内计数仍走 Record。
func (t *ModelTally) Absorb(models []SessionModel) {
	for _, entry := range models {
		found := false
		for i := range t.entries {
			if t.entries[i].model == entry {
				found = true
				break
			}
		}
		if !found {
			t.entries = append(t.entries, modelEntry{model: entry, count: 0})
		}
	}
}

// IntoModels：计数为 0 的是 absorb 保序并入的项，排在计数项之后。稳定排序
// 保证同次数的项保持首次出现顺序。
func (t *ModelTally) IntoModels() []SessionModel {
	sorted := make([]modelEntry, len(t.entries))
	copy(sorted, t.entries)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})
	models := make([]SessionModel, 0, len(sorted))
	for _, entry := range sorted {
		models = append(models, entry.model)
	}
	return models
}
