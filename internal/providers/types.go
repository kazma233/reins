// Package providers：聚合平台模型管理域。与 session、workspace 互不依赖；
// 持久化在独立的 providers.yaml（含明文 API Key），工具配置写入由本包的
// 适配器层（apps*.go，对齐 Rust 版 providers/apps/）完成。
//
// wire 契约经 frontend/bindings/ 暴露给前端（wails 生成、不手改）：字段
// camelCase、枚举 snake_case、Option 字段省略（omitempty 指针）。
// providers.yaml 落盘键保持 Rust serde 拼写（snake_case；ProviderAppSetting
// 是唯一 camelCase 例外，与 Rust 侧一致）。
package providers

import (
	"fmt"
	"strings"

	"reins/internal/agents"
)

// ProviderApp 与 agents 清单共用同一键类型：providers 域自身不再定义第二份
// 枚举（GLOSSARY 工具清单词条；Rust ProviderAppId 的 Go 对应物寄居 agents）。
type ProviderApp = agents.ProviderApp

// ---------------------------------------------------------------------------
// 共享枚举
// ---------------------------------------------------------------------------

// ProviderProtocol：模型协议（serde snake_case 拼写即 wire 值）。
type ProviderProtocol string

const (
	ProtocolOpenaiResponses       ProviderProtocol = "openai_responses"
	ProtocolOpenaiChatCompletions ProviderProtocol = "openai_chat_completions"
	ProtocolAnthropicMessages     ProviderProtocol = "anthropic_messages"
)

// AllProtocols 对齐 Rust ProviderProtocol::all()。
func AllProtocols() []ProviderProtocol {
	return []ProviderProtocol{
		ProtocolOpenaiResponses,
		ProtocolOpenaiChatCompletions,
		ProtocolAnthropicMessages,
	}
}

// UsesAPIKeyHeader：各工具模型目录接口使用的鉴权头风格；聚合平台 /v1/models
// 通常兼容 OpenAI 形态，anthropic 协议按官方约定走 x-api-key。
func (p ProviderProtocol) UsesAPIKeyHeader() bool {
	return p == ProtocolAnthropicMessages
}

// ReasoningLevel：归一化思考等级（serde snake_case 拼写即 wire 值；参考
// models.dev reasoning_options 口径）。Rust 侧避免叫 None 的原因在 Go 不存在，
// 常量名沿用 Rust 变体拼写以保持一一对应。
type ReasoningLevel string

const (
	ReasoningOff     ReasoningLevel = "off"
	ReasoningMinimal ReasoningLevel = "minimal"
	ReasoningLow     ReasoningLevel = "low"
	ReasoningMedium  ReasoningLevel = "medium"
	ReasoningHigh    ReasoningLevel = "high"
	ReasoningXhigh   ReasoningLevel = "xhigh"
	ReasoningMax     ReasoningLevel = "max"
)

// AllReasoningLevels 按 Rust 枚举声明顺序排列。该顺序即 Ord 序：模型档位
// 列表的排序去重、codex 目录条目的档位插入位置都依赖它。
func AllReasoningLevels() []ReasoningLevel {
	return []ReasoningLevel{
		ReasoningOff,
		ReasoningMinimal,
		ReasoningLow,
		ReasoningMedium,
		ReasoningHigh,
		ReasoningXhigh,
		ReasoningMax,
	}
}

// reasoningRank 返回档位的 Ord 序号（枚举声明顺序）。
func reasoningRank(level ReasoningLevel) int {
	for i, candidate := range AllReasoningLevels() {
		if candidate == level {
			return i
		}
	}
	// 未登记档位排最后：只在程序构造出非法值时可能出现，排序仍保持稳定。
	return len(AllReasoningLevels())
}

// WriteForm 是写入工具配置文件时的小写字符串形式（Rust as_str）。off 档的
// 归一化拼写是 "off"（serde/wire），写入形态是 "none"——两套拼写并存是
// Rust 既有口径，方言表按需覆盖。
func (l ReasoningLevel) WriteForm() string {
	if l == ReasoningOff {
		return "none"
	}
	return string(l)
}

// ParseReasoningLevel 沿归一口径（trim + 小写）按写入形态识别；认不出返回
// false。注意 "off" 不是任何档位的写入形态，识别不出（方言表另行映射）。
func ParseReasoningLevel(value string) (ReasoningLevel, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, level := range AllReasoningLevels() {
		if level.WriteForm() == normalized {
			return level, true
		}
	}
	return "", false
}

// UnmarshalText 让 encoding/json 与 gopkg.in/yaml.v3 共用同一份枚举校验：
// 未知取值在反序列化期报错，与 Rust serde 的 unknown variant 行为同向。
func (l *ReasoningLevel) UnmarshalText(text []byte) error {
	value := ReasoningLevel(text)
	for _, known := range AllReasoningLevels() {
		if value == known {
			*l = value
			return nil
		}
	}
	return fmt.Errorf("unknown reasoning level %q", string(text))
}

// ProviderProtocol 的反序列化校验同 ReasoningLevel。
func (p *ProviderProtocol) UnmarshalText(text []byte) error {
	value := ProviderProtocol(text)
	for _, known := range AllProtocols() {
		if value == known {
			*p = value
			return nil
		}
	}
	return fmt.Errorf("unknown provider protocol %q", string(text))
}

// ---------------------------------------------------------------------------
// Raw 类型：providers.yaml 落盘文档（serde 拼写键）
// ---------------------------------------------------------------------------

const documentVersion = 1

// providerAppSetting：单个工具的 Reins 侧设置。键名跟随 Rust serde
// rename_all = "camelCase"（providers.yaml 里的唯一 camelCase 键）。
type ProviderAppSetting struct {
	DefaultReasoningLevel *ReasoningLevel `yaml:"defaultReasoningLevel,omitempty" json:"defaultReasoningLevel,omitempty"`
}

type rawProvider struct {
	Label    string                `yaml:"label"`
	Protocol ProviderProtocol      `yaml:"protocol"`
	BaseURL  string                `yaml:"base_url"`
	APIKey   *string               `yaml:"api_key,omitempty"`
	Models   []ProviderModelRecord `yaml:"models"`
}

// ProviderModelRecord：providers.yaml 单个模型记录。
type ProviderModelRecord struct {
	ID              string           `yaml:"id"`
	Label           string           `yaml:"label"`
	ContextWindow   *int64           `yaml:"context_window,omitempty"`
	MaxOutputTokens *int64           `yaml:"max_output_tokens,omitempty"`
	SupportsImages  *bool            `yaml:"supports_images,omitempty"`
	Reasoning       *bool            `yaml:"reasoning,omitempty"`
	ReasoningLevels []ReasoningLevel `yaml:"reasoning_levels,omitempty"`
}

type rawProvidersDocument struct {
	Version     int64                              `yaml:"version"`
	Providers   map[string]rawProvider             `yaml:"providers"`
	AppSettings map[ProviderApp]ProviderAppSetting `yaml:"app_settings"`
}

// ResolvedProvider：交给适配器与状态构建的解析后平台数据。
type ResolvedProvider struct {
	ID       string
	Label    string
	Protocol ProviderProtocol
	BaseURL  string
	APIKey   *string
	Models   []ProviderModelRecord
}

// StoredAPIKey 返回落库与下发共用的空值口径：trim 后为空一律视为「没有
// Key」；found=false 表示没有可用 Key。
func (p *ResolvedProvider) StoredAPIKey() (string, bool) {
	if p.APIKey == nil {
		return "", false
	}
	trimmed := strings.TrimSpace(*p.APIKey)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// ---------------------------------------------------------------------------
// View 类型：前后端边界（camelCase）
// ---------------------------------------------------------------------------

// ProviderView 对齐 generated ProviderView.ts。i64 经 JSON 序列化为 number，
// TS 侧固定 number（Rust 注释同口径）。
type ProviderView struct {
	ID       string              `json:"id"`
	Label    string              `json:"label"`
	Protocol ProviderProtocol    `json:"protocol"`
	BaseURL  string              `json:"baseUrl"`
	APIKey   *string             `json:"apiKey,omitempty"`
	Models   []ProviderModelView `json:"models"`
}

type ProviderModelView struct {
	ID              string           `json:"id"`
	Label           string           `json:"label"`
	ContextWindow   *int64           `json:"contextWindow,omitempty"`
	MaxOutputTokens *int64           `json:"maxOutputTokens,omitempty"`
	SupportsImages  *bool            `json:"supportsImages,omitempty"`
	Reasoning       *bool            `json:"reasoning,omitempty"`
	ReasoningLevels []ReasoningLevel `json:"reasoningLevels,omitempty"`
}

type ProviderAppEntryStatus string

const (
	EntryStatusApplied  ProviderAppEntryStatus = "applied"
	EntryStatusDrifted  ProviderAppEntryStatus = "drifted"
	EntryStatusExternal ProviderAppEntryStatus = "external"
)

type ProviderAppEntry struct {
	// Key：注册键。Reins 写入的条目为 reins-<平台ID>；外部条目为原样键名。
	Key            string                 `json:"key"`
	Status         ProviderAppEntryStatus `json:"status"`
	ProviderID     *string                `json:"providerId,omitempty"`
	Label          *string                `json:"label,omitempty"`
	BaseURL        *string                `json:"baseUrl,omitempty"`
	ModelIDs       []string               `json:"modelIds"`
	DefaultModelID *string                `json:"defaultModelId,omitempty"`
	Notes          []string               `json:"notes"`
	// Protocol：条目实际使用的协议，按工具配置文件里的协议字段反推；
	// 识别不出（手工改过或未知取值）时为 nil 不展示。
	Protocol *ProviderProtocol `json:"protocol,omitempty"`
}

type ProviderAppState struct {
	App                      agents.ProviderApp     `json:"app"`
	ConfigPaths              []string               `json:"configPaths"`
	ConfigExists             bool                   `json:"configExists"`
	SupportedProtocols       []ProviderProtocol     `json:"supportedProtocols"`
	SupportedReasoningLevels []ReasoningLevel       `json:"supportedReasoningLevels"`
	ReasoningLevelWrites     []ReasoningEffortWrite `json:"reasoningLevelWrites"`
	Additive                 bool                   `json:"additive"`
	UnwrittenModelFields     []string               `json:"unwrittenModelFields"`
	DefaultReasoningLevel    *ReasoningLevel        `json:"defaultReasoningLevel,omitempty"`
	AppReasoningLevel        *ReasoningLevel        `json:"appReasoningLevel,omitempty"`
	BehaviorNotes            []string               `json:"behaviorNotes"`
	Entries                  []ProviderAppEntry     `json:"entries"`
	LoadError                *string                `json:"loadError,omitempty"`
}

type ReasoningEffortWrite struct {
	Level  ReasoningLevel `json:"level"`
	Writes string         `json:"writes"`
}

type ProvidersState struct {
	ConfigPath string             `json:"configPath"`
	Providers  []ProviderView     `json:"providers"`
	Apps       []ProviderAppState `json:"apps"`
}

// ---------------------------------------------------------------------------
// 输入
// ---------------------------------------------------------------------------

type ProviderModelInput struct {
	ID              string           `json:"id"`
	Label           string           `json:"label"`
	ContextWindow   *int64           `json:"contextWindow"`
	MaxOutputTokens *int64           `json:"maxOutputTokens"`
	SupportsImages  *bool            `json:"supportsImages"`
	Reasoning       *bool            `json:"reasoning"`
	ReasoningLevels []ReasoningLevel `json:"reasoningLevels"`
}

// ProviderWriteMode：调用方的写入意图。缺省（空串，JSON 未传）按 upsert
// 处理——Rust #[serde(default)] + #[default] 的同款语义。
type ProviderWriteMode string

const (
	WriteModeUpsert ProviderWriteMode = "upsert"
	WriteModeCreate ProviderWriteMode = "create"
	WriteModeUpdate ProviderWriteMode = "update"
)

type ProviderUpsertInput struct {
	ProviderID string            `json:"providerId"`
	Mode       ProviderWriteMode `json:"mode"`
	Label      string            `json:"label"`
	Protocol   ProviderProtocol  `json:"protocol"`
	BaseURL    string            `json:"baseUrl"`
	// 所见即所得：表单里的值就是落盘值，留空即清除已存 Key。
	APIKey string               `json:"apiKey"`
	Models []ProviderModelInput `json:"models"`
}

// ApplyProviderInput：一次应用动作选择的模型与默认值；预览与实际写入共用。
type ApplyProviderInput struct {
	ProviderID            string             `json:"providerId"`
	App                   agents.ProviderApp `json:"app"`
	ModelIDs              []string           `json:"modelIds"`
	DefaultModelID        string             `json:"defaultModelId"`
	DefaultReasoningLevel *ReasoningLevel    `json:"defaultReasoningLevel"`
}

type ProviderMutationResult struct {
	// App 指针且无 omitempty：Rust 该字段没有 skip_serializing_if，None
	// 序列化为 null（TS 侧 ProviderAppId | null 非可选）。
	App        *agents.ProviderApp `json:"app"`
	ProviderID *string             `json:"providerId,omitempty"`
	Action     string              `json:"action"`
	Detail     string              `json:"detail"`
}

// ---------------------------------------------------------------------------
// 预览：文件路径 + 行级 diff + 能力警告（密钥脱敏）
// ---------------------------------------------------------------------------

type DiffLineKind string

const (
	DiffContext DiffLineKind = "context"
	DiffAdd     DiffLineKind = "add"
	DiffRemove  DiffLineKind = "remove"
)

type DiffLine struct {
	Kind DiffLineKind `json:"kind"`
	Text string       `json:"text"`
}

type ProviderFilePreview struct {
	Path   string     `json:"path"`
	Exists bool       `json:"exists"`
	Diff   []DiffLine `json:"diff"`
}

type ProviderApplyPreview struct {
	App      agents.ProviderApp    `json:"app"`
	Files    []ProviderFilePreview `json:"files"`
	Warnings []string              `json:"warnings"`
}

// ---------------------------------------------------------------------------
// 模型目录拉取 + models.dev 补全
// ---------------------------------------------------------------------------

type FetchedModel struct {
	ID   string  `json:"id"`
	Name *string `json:"name,omitempty"`
}

// FetchedModelsResult.URL 是最终成功的那一个候选路径，前端弹窗展示它以便
// 确认实际请求打到哪里。
type FetchedModelsResult struct {
	URL    string         `json:"url"`
	Models []FetchedModel `json:"models"`
}

type ModelsDevMeta struct {
	ContextWindow   *int64           `json:"contextWindow,omitempty"`
	MaxOutputTokens *int64           `json:"maxOutputTokens,omitempty"`
	SupportsImages  *bool            `json:"supportsImages,omitempty"`
	Reasoning       *bool            `json:"reasoning,omitempty"`
	ReasoningLevels []ReasoningLevel `json:"reasoningLevels,omitempty"`
}

type ModelsDevCandidate struct {
	Provider string        `json:"provider"`
	ModelID  string        `json:"modelId"`
	Label    *string       `json:"label,omitempty"`
	Meta     ModelsDevMeta `json:"meta"`
}

type ModelsDevMatchStatus string

const (
	MatchExact      ModelsDevMatchStatus = "exact"
	MatchCandidates ModelsDevMatchStatus = "candidates"
	MatchNotFound   ModelsDevMatchStatus = "not_found"
)

type ModelsDevMatchResult struct {
	Status     ModelsDevMatchStatus `json:"status"`
	Meta       *ModelsDevMeta       `json:"meta,omitempty"`
	Candidates []ModelsDevCandidate `json:"candidates"`
}

// ProviderAppLabel：产品名唯一来源是 agents 清单（Rust label() 派生同源）。
func ProviderAppLabel(app ProviderApp) string {
	spec := agents.SpecByProviderApp(app)
	if spec == nil {
		panic("providers 工具未登记 agents 清单")
	}
	return spec.Label
}
