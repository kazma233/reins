// 方言（dialect）：归一化枚举（协议、思考档位）在某工具配置文件里的实际
// 拼写知识。每个适配器经 AppAdapter 声明一张，apply 写入、inspect 反读、
// empty_state 下发的映射文案全部从同一张表派生；正反互逆由 roundtrip 测试
// 守卫。收编前这些知识散在六份适配器的正反函数对里，dsh 曾因缺一份覆写把
// off 档的映射文案下发成 "none"（实际写 "off"）。
package providers

import "strings"

// ReasoningDialect：思考档位的正反拼写表。
type ReasoningDialect struct {
	// spells：档位 → 工具拼写。apply 只会拼写 supportedReasoningLevels 内的
	// 档位（validatePlan 前置校验），表按需覆盖；查表缺失属静态数据 bug，
	// panic 大声失败。
	Spells []ReasoningSpell
}

type ReasoningSpell struct {
	Level ReasoningLevel
	Spell string
}

// Spell：查表拼写。缺失属静态数据 bug（方言表与能力表脱钩），panic 暴露。
func (d *ReasoningDialect) Spell(level ReasoningLevel) string {
	for _, entry := range d.Spells {
		if entry.Level == level {
			return entry.Spell
		}
	}
	panic("方言表必须覆盖该档位")
}

// Parse 反读：先按工具拼写认档（方言表覆盖工具支持的全部拼写，含 Reins
// 不主动产出的历史取值），认不出回落 ReasoningLevel.Parse 的归一口径
// （trim + 小写，best-effort）；两处都认不出的取值留空展示、不报错。
func (d *ReasoningDialect) Parse(value string) (ReasoningLevel, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, entry := range d.Spells {
		if entry.Spell == normalized {
			return entry.Level, true
		}
	}
	return ParseReasoningLevel(value)
}

// ProtocolDialect：协议的正反拼写表。
type ProtocolDialect struct {
	// Spells：协议 → 工具拼写。
	Spells []ProtocolSpell
	// ParseAliases：只出现在反读里的别名（如 opencode 的 v1 npm 包名）：
	// 正向写入不产出。
	ParseAliases []ProtocolAlias
}

type ProtocolSpell struct {
	Protocol ProviderProtocol
	Spell    string
}

type ProtocolAlias struct {
	Alias    string
	Protocol ProviderProtocol
}

// Spell：查表拼写。缺失属静态数据 bug，panic 暴露。
func (d *ProtocolDialect) Spell(protocol ProviderProtocol) string {
	for _, entry := range d.Spells {
		if entry.Protocol == protocol {
			return entry.Spell
		}
	}
	panic("方言表必须覆盖该协议")
}

// Parse 反读：先按工具拼写认协议，再查只读别名；都认不出返回 false。
func (d *ProtocolDialect) Parse(value string) (ProviderProtocol, bool) {
	for _, entry := range d.Spells {
		if entry.Spell == value {
			return entry.Protocol, true
		}
	}
	for _, entry := range d.ParseAliases {
		if entry.Alias == value {
			return entry.Protocol, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// 各工具方言表（包级常量：共享方言保证单一实例，pi 与 dsh 引用同一份，
// roundtrip 测试按同一性守卫）
// ---------------------------------------------------------------------------

// identityReasoning：恒等方言：拼写与归一化枚举一致（off 档写 "none"）。
var identityReasoning = &ReasoningDialect{
	Spells: []ReasoningSpell{
		{ReasoningOff, "none"},
		{ReasoningMinimal, "minimal"},
		{ReasoningLow, "low"},
		{ReasoningMedium, "medium"},
		{ReasoningHigh, "high"},
		{ReasoningXhigh, "xhigh"},
		{ReasoningMax, "max"},
	},
}

// piAIProtocol：pi-ai 方言：dsh 的 LLM 路由层就是 llm-pi-ai，与 Pi 共用
// 同一套 KnownApi 枚举与思考档位，两个适配器引用同一份实例，防止再长出
// 两份拷贝。api 取值按 pi 命名习惯写为 kebab-case（pi-ai 包 types.d.ts）；
// 思考档位首档是 "off"（pi.dev/docs/latest/settings），与归一化集合的
// "none" 写法不同。
var piAIProtocol = &ProtocolDialect{
	Spells: []ProtocolSpell{
		{ProtocolOpenaiResponses, "openai-responses"},
		{ProtocolOpenaiChatCompletions, "openai-completions"},
		{ProtocolAnthropicMessages, "anthropic-messages"},
	},
}

var piAIReasoning = &ReasoningDialect{
	Spells: []ReasoningSpell{
		{ReasoningOff, "off"},
		{ReasoningMinimal, "minimal"},
		{ReasoningLow, "low"},
		{ReasoningMedium, "medium"},
		{ReasoningHigh, "high"},
		{ReasoningXhigh, "xhigh"},
		{ReasoningMax, "max"},
	},
}

// grokProtocol：Grok 1.0.40 实测 + 官方 settings-reference 确认：
// api_backend 合法取值为 chat_completions / responses / messages。
var grokProtocol = &ProtocolDialect{
	Spells: []ProtocolSpell{
		{ProtocolOpenaiResponses, "responses"},
		{ProtocolOpenaiChatCompletions, "chat_completions"},
		{ProtocolAnthropicMessages, "messages"},
	},
}

// grokReasoning：Grok 的 config.toml / 模型菜单都只认
// none/minimal/low/medium/high/xhigh/max 七个值（1.0.92 实测：off、x-high、
// 带空格的写法都会让整个 reasoning_efforts 数组静默失效）。max 不再折叠成
// xhigh：模型菜单声明 max 时它就是独立档位；只有模型没有等级元数据、退回
// Grok 旧式默认菜单（low/medium/high/xhigh）时才由 apply 折算成 xhigh。
var grokReasoning = &ReasoningDialect{
	Spells: []ReasoningSpell{
		{ReasoningOff, "none"},
		{ReasoningMinimal, "minimal"},
		{ReasoningLow, "low"},
		{ReasoningMedium, "medium"},
		{ReasoningHigh, "high"},
		{ReasoningXhigh, "xhigh"},
		{ReasoningMax, "max"},
	},
}

// codexProtocol：codex 的 wire_api 目前只有 responses 一种取值
// （仅接受 openai_responses）。
var codexProtocol = &ProtocolDialect{
	Spells: []ProtocolSpell{
		{ProtocolOpenaiResponses, "responses"},
	},
}

// zcodeProtocol：ZCode provider_config.json 的 api.type 拼写（ZCode.app
// bundle 的 zod schema 实证，2026-10-13）：三种协议全支持，kebab-case。
var zcodeProtocol = &ProtocolDialect{
	Spells: []ProtocolSpell{
		{ProtocolOpenaiResponses, "openai-responses"},
		{ProtocolOpenaiChatCompletions, "openai-chat-completions"},
		{ProtocolAnthropicMessages, "anthropic-messages"},
	},
}

// zcodeReasoning：ZCode 的 reasoningLevel.values 档位拼写（开源仓库
// packages/shared/src/model-config.ts + 内置规则实证，2026-10-13）：关闭档是
// "disabled"，其余档位名原样透传成各家 effort（按 API 类型的兜底 map 负责
// 拼协议字段）。与恒等方言的差别只在 off：写 "none" 在 anthropic-messages /
// openai-responses 兜底里会被当成普通 effort 透传（只有 chat-completions
// 把它当关闭）。
var zcodeReasoning = &ReasoningDialect{
	Spells: []ReasoningSpell{
		{ReasoningOff, "disabled"},
		{ReasoningMinimal, "minimal"},
		{ReasoningLow, "low"},
		{ReasoningMedium, "medium"},
		{ReasoningHigh, "high"},
		{ReasoningXhigh, "xhigh"},
		{ReasoningMax, "max"},
	},
}

// opencodeProtocol：OpenCode v2 按协议选择原生运行时包写入：OpenAI 请求格式
// （chat completions）走 openai-compatible，Responses 协议走 openai/responses
// （compatible/responses 有包解析缺陷，不采用），Anthropic 协议走 anthropic。
// 反读额外识别 v1 遗留 npm 包名与 anthropic-compatible（面向第三方兼容端点，
// 仅反读）。
var opencodeProtocol = &ProtocolDialect{
	Spells: []ProtocolSpell{
		{ProtocolOpenaiChatCompletions, "@opencode/ai/providers/openai-compatible"},
		{ProtocolOpenaiResponses, "@opencode/ai/providers/openai/responses"},
		{ProtocolAnthropicMessages, "@opencode/ai/providers/anthropic"},
	},
	ParseAliases: []ProtocolAlias{
		{"@ai-sdk/openai-compatible", ProtocolOpenaiChatCompletions},
		{"@opencode/ai/providers/openai-compatible/responses", ProtocolOpenaiResponses},
		{"@opencode/ai/providers/anthropic-compatible", ProtocolAnthropicMessages},
		{"@ai-sdk/anthropic", ProtocolAnthropicMessages},
	},
}
