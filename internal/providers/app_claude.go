// Claude Code：$CLAUDE_CONFIG_DIR/settings.json（默认 ~/.claude/settings.json）。
// 单活动 Provider：应用即替换；仅接受 anthropic_messages。写入覆盖
// env.ANTHROPIC_BASE_URL / env.ANTHROPIC_AUTH_TOKEN / model / effortLevel，
// 保留文件中其他键。
package providers

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"reins/internal/agents"
	"reins/internal/support"
)

type ClaudeAdapter struct{}

const (
	claudeBaseURLKey = "ANTHROPIC_BASE_URL"
	// 认证键用 ANTHROPIC_AUTH_TOKEN（Authorization: Bearer 头）：官方键，且
	// 只认 Bearer 的网关也能通（用户决策 2026-09-28，方案 c）。
	claudeAuthTokenKey = "ANTHROPIC_AUTH_TOKEN"
	// 旧版 Reins 写的认证键（X-Api-Key 头）。两个凭据头并存会被部分网关
	// 拒绝，应用态下认证由 Reins 管理，apply 时清掉这个遗留键。
	claudeLegacyAPIKeyKey = "ANTHROPIC_API_KEY"
	// 聚合模型对 Claude Code 是「不认识的模型 ID」：窗口按内置同名 ID 推断、
	// 输出默认 32000（code.claude.com/docs/en/env-vars）。这两个 env 键正是
	// 官方为此场景提供的纠正入口，随活动 provider 由 Reins 管理。
	claudeMaxContextTokensKey = "CLAUDE_CODE_MAX_CONTEXT_TOKENS"
	claudeMaxOutputTokensKey  = "CLAUDE_CODE_MAX_OUTPUT_TOKENS"
)

// claudeSupportedLevels：官方 effortLevel 值域为 low/medium/high/xhigh/max
// （code.claude.com/docs/en/settings-reference），off/minimal 不在枚举内，
// 不放行避免写出无效值。
var claudeSupportedLevels = []ReasoningLevel{
	ReasoningLow,
	ReasoningMedium,
	ReasoningHigh,
	ReasoningXhigh,
	ReasoningMax,
}

func (a *ClaudeAdapter) ID() ProviderApp {
	return agents.ProviderClaude
}

func (a *ClaudeAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	path, err := claudeSettingsPath(env)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func (a *ClaudeAdapter) RestrictedPaths(env *support.Environment) []string {
	return nil
}

func (a *ClaudeAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols:   []ProviderProtocol{ProtocolAnthropicMessages},
		Additive:             false,
		RequiredModelFields:  nil,
		UnwrittenModelFields: []ModelField{FieldSupportsImages, FieldReasoning},
		// 窗口与最大输出已写 env；图像输入与 per-model 推理能力在
		// Claude Code 配置里没有对应键。
		SupportedReasoningLevels: claudeSupportedLevels,
		BehaviorNotes: []string{
			"单活动提供商：应用即替换 settings.json 里的 base URL、认证、模型与 effortLevel，文件中其他键保留。",
			"聚合模型对 Claude Code 是「不认识的模型 ID」，上下文窗口按内置同名模型推断。",
		},
	}
}

func (a *ClaudeAdapter) ReasoningDialect() *ReasoningDialect {
	return identityReasoning
}

// Claude 的 env 写法语义固定为 Anthropic Messages，配置里没有协议字段
// 可写可反推，无方言可声明。
func (a *ClaudeAdapter) ProtocolDialect() *ProtocolDialect {
	return nil
}

func (a *ClaudeAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	path, err := claudeSettingsPath(env)
	if err != nil {
		return nil, err
	}
	state := EmptyState(agents.ProviderClaude, []string{path})
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return state, nil
	}
	state.ConfigExists = true
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	// 应用弹窗据此预选；反读值只用于回显，不参与漂移判定。
	if effort, ok := asString(root["effortLevel"]); ok {
		if level, ok := identityReasoning.Parse(effort); ok {
			levelCopy := level
			state.DefaultReasoningLevel = &levelCopy
		}
	}
	envTable, ok := asObject(root["env"])
	if !ok {
		return state, nil
	}
	baseURL, ok := asString(envTable[claudeBaseURLKey])
	if !ok {
		return state, nil
	}

	// Claude 没有 reins- 注册键可认，只能靠 base_url 归属：
	// 与某平台一致 → 已应用；不一致 → 外部配置（只展示不纳管）。
	var matched *ResolvedProvider
	for _, provider := range providers {
		if provider.BaseURL == baseURL {
			matched = provider
			break
		}
	}
	model, _ := asString(root["model"])
	var entry ProviderAppEntry
	if matched != nil {
		entry = appliedEntry("env", matched, baseURL, true, modelListOrEmpty(model), stringPtrOrNil(model),
			// Claude 的 env 写法语义固定为 Anthropic Messages，无字段可反推。
			protocolPtr(ProtocolAnthropicMessages))
		if _, has := envTable[claudeAuthTokenKey]; !has {
			entry.Notes = append(entry.Notes, fmt.Sprintf("缺少 %s，应用后才能生效。", claudeAuthTokenKey))
		}
		if _, has := envTable[claudeLegacyAPIKeyKey]; has {
			entry.Notes = append(entry.Notes, fmt.Sprintf("检测到遗留的 %s，重新应用可清理。", claudeLegacyAPIKeyKey))
		}
	} else {
		entry = externalEntry("env", baseURL, true,
			[]string{"外部 ANTHROPIC_BASE_URL，非 Reins 纳管。"},
			protocolPtr(ProtocolAnthropicMessages))
	}
	state.Entries = append(state.Entries, entry)
	return state, nil
}

func (a *ClaudeAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	path, err := claudeSettingsPath(env)
	if err != nil {
		return nil, err
	}
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}

	envAny, exists := root["env"]
	if !exists {
		envTable := map[string]any{}
		root["env"] = envTable
		envAny = envTable
	}
	envTable, ok := asObject(envAny)
	if !ok {
		return nil, fmt.Errorf("env 段不是对象：%s", path)
	}
	envTable[claudeBaseURLKey] = provider.BaseURL
	envTable[claudeAuthTokenKey] = apiKey
	delete(envTable, claudeLegacyAPIKeyKey)
	// 窗口与输出上限取自默认模型元数据；缺失时清空，避免上一个 provider
	// 的值残留在唯一的活动配置上。
	var defaultModel *ProviderModelRecord
	for i := range provider.Models {
		if provider.Models[i].ID == plan.DefaultModelID {
			defaultModel = &provider.Models[i]
			break
		}
	}
	for key, value := range map[string]*int64{
		claudeMaxContextTokensKey: defaultModelInt64(defaultModel, func(m *ProviderModelRecord) *int64 { return m.ContextWindow }),
		claudeMaxOutputTokensKey:  defaultModelInt64(defaultModel, func(m *ProviderModelRecord) *int64 { return m.MaxOutputTokens }),
	} {
		if value != nil {
			// env 值一律字符串（Rust JsonValue::String(value.to_string())）。
			envTable[key] = strconv.FormatInt(*value, 10)
		} else {
			delete(envTable, key)
		}
	}
	root["model"] = plan.DefaultModelID
	if plan.DefaultReasoningLevel != nil {
		root["effortLevel"] = identityReasoning.Spell(*plan.DefaultReasoningLevel)
	}

	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func defaultModelInt64(model *ProviderModelRecord, pick func(*ProviderModelRecord) *int64) *int64 {
	if model == nil {
		return nil
	}
	return pick(model)
}

func (a *ClaudeAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	// 没有 base_url 比对就无法证明这条配置属于该平台，宁可拒绝也
	// 不能误删用户自己的官方配置。
	if provider == nil {
		return nil, fmt.Errorf(
			"平台 %s 的元数据已删除，无法安全移除 Claude Code 配置；请手动清理 settings.json 中的 %s 与 %s。",
			providerID, claudeBaseURLKey, claudeAuthTokenKey)
	}
	path, err := claudeSettingsPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("Claude Code 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}

	envTable, _ := asObject(root["env"])
	currentBaseURL, _ := asString(envTable[claudeBaseURLKey])
	if currentBaseURL != provider.BaseURL {
		return nil, fmt.Errorf("settings.json 的 %s 与平台 %s 不一致，未做任何修改。",
			claudeBaseURLKey, providerID)
	}

	if envTable != nil {
		delete(envTable, claudeBaseURLKey)
		delete(envTable, claudeAuthTokenKey)
		delete(envTable, claudeLegacyAPIKeyKey)
		delete(envTable, claudeMaxContextTokensKey)
		delete(envTable, claudeMaxOutputTokensKey)
		if len(envTable) == 0 {
			delete(root, "env")
		}
	}
	// model 与 effortLevel 只有与平台写入值一致才清，避免误删
	// 用户自己设置的值。
	model, _ := asString(root["model"])
	modelIsOurs := false
	if model != "" && findModel(provider, model) != nil {
		modelIsOurs = true
	}
	if modelIsOurs {
		delete(root, "model")
		if level, ok := asString(root["effortLevel"]); ok {
			if _, parsed := identityReasoning.Parse(level); parsed {
				delete(root, "effortLevel")
			}
		}
	}

	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func (a *ClaudeAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	_ = env
	_ = entryKey
	// Claude 的外部配置是 env 内嵌值，没有可结构化删除的独立条目。
	return nil, errors.New("Claude 的外部配置不支持按条目删除。")
}

// modelListOrEmpty：Rust `model.map(|m| vec![m]).unwrap_or_default()`。
func modelListOrEmpty(model string) []string {
	if model == "" {
		return []string{}
	}
	return []string{model}
}

// protocolPtr：可选协议值的取址辅助。
func protocolPtr(protocol ProviderProtocol) *ProviderProtocol {
	value := protocol
	return &value
}
