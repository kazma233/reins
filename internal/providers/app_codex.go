// Codex：$CODEX_HOME/config.toml（默认 ~/.codex/config.toml）。
// 单活动 Provider：应用即替换；仅接受 openai_responses。
// 另写 $CODEX_HOME/reins-models.json 并以顶层 model_catalog_json 指向它：
// Codex 不认识的模型会走兜底元数据（272000 窗口）并从选择器消失（fallback
// visibility=none），目录条目让已选模型带着正确元数据回到选择器。
// 注意该键是「整体替换」语义：应用期间 Codex 内置模型不出现在选择器，
// 移除平台后恢复。
package providers

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

//go:embed codex_prompt.md
var codexPrompt string

type CodexAdapter struct{}

// wireAPI：wire_api 唯一合法取值来自协议方言表（codex 仅接受
// openai_responses）。
func wireAPI() string {
	return codexProtocol.Spell(ProtocolOpenaiResponses)
}

const catalogFileName = "reins-models.json"

// codexPrompt：codex 内置兜底提示词（Apache-2.0，取自 openai/codex
// rust-v0.145.0 的 codex-rs/models-manager/prompt.md；0.158.0 起同一内容另存
// 于 codex-rs/protocol/src/prompts/base_instructions/default.md，两路径并存，
// 0.145→0.162.1 实测内容零漂移）。目录条目的 instructions_template 是逐模型
// 系统提示，留空会让 Codex 发出空提示词，因此对齐兜底值：从「未知模型走
// fallback」切到「目录条目」行为零变化。

// catalogEfforts：目录条目中该模型支持的思考档位：优先用模型元数据
// reasoning_levels；只声明 reasoning=true 时给保守三档；其余给空集（Codex
// 接受空数组）。
func catalogEfforts(reasoning *bool, reasoningLevels []ReasoningLevel) []ReasoningLevel {
	if len(reasoningLevels) > 0 {
		return reasoningLevels
	}
	if reasoning != nil && *reasoning {
		return []ReasoningLevel{ReasoningLow, ReasoningMedium, ReasoningHigh}
	}
	return []ReasoningLevel{}
}

// withSelectedEffort：config.toml 的 model_reasoning_effort 无条件写用户所选
// 档位，而 codex /model 只认目录条目的档位列表：当前档位不在列表里时会被
// codex 回落到列表中间档（codex-rs turn_context.rs），用户所选被静默丢弃。
// 因此所选档位按强度序并入列表，default_reasoning_level 也随之始终可写。
func withSelectedEffort(efforts []ReasoningLevel, selected *ReasoningLevel) []ReasoningLevel {
	if selected == nil {
		return efforts
	}
	for _, effort := range efforts {
		if effort == *selected {
			return efforts
		}
	}
	at := len(efforts)
	for i, effort := range efforts {
		if reasoningRank(effort) > reasoningRank(*selected) {
			at = i
			break
		}
	}
	result := make([]ReasoningLevel, 0, len(efforts)+1)
	result = append(result, efforts[:at]...)
	result = append(result, *selected)
	result = append(result, efforts[at:]...)
	return result
}

// buildCatalog 生成 reins-models.json 内容：字段对齐 codex 0.158.0 的
// ModelInfo——supports_parallel_tool_calls 已在 0.158.0 删除、不再写；系统
// 提示用 model_messages.instructions_template（0.158.0 起的规范形式，旧顶层
// base_instructions 只是保留的 legacy 提升路径），visibility=list 让模型进
// 选择器。dialect 从 apply 传入：目录文件里的档位拼写与 config.toml 的
// model_reasoning_effort 同源，都走方言表，反读（parse）才正反闭合。
func buildCatalog(provider *ResolvedProvider, plan *ApplyProviderInput, dialect *ReasoningDialect) (string, error) {
	models := make([]map[string]any, 0, len(plan.ModelIDs))
	for index, modelID := range plan.ModelIDs {
		model := findModel(provider, modelID)
		efforts := withSelectedEffort(
			catalogEfforts(model.Reasoning, model.ReasoningLevels),
			plan.DefaultReasoningLevel,
		)
		levelEntries := make([]map[string]any, 0, len(efforts))
		for _, level := range efforts {
			levelEntries = append(levelEntries, map[string]any{
				"effort":      dialect.Spell(level),
				"description": "",
			})
		}
		inputModalities := []any{"text"}
		if model.SupportsImages != nil && *model.SupportsImages {
			inputModalities = []any{"text", "image"}
		}
		entry := map[string]any{
			"slug":                         model.ID,
			"display_name":                 model.Label,
			"description":                  fmt.Sprintf("%s（%s）", model.Label, provider.Label),
			"supported_reasoning_levels":   levelEntries,
			"shell_type":                   "default",
			"visibility":                   "list",
			"supported_in_api":             true,
			"priority":                     index + 1,
			"support_verbosity":            false,
			"default_verbosity":            nil,
			"apply_patch_tool_type":        nil,
			"truncation_policy":            map[string]any{"mode": "bytes", "limit": 10000},
			"experimental_supported_tools": []any{},
			"input_modalities":             inputModalities,
			"service_tiers":                []any{},
			"model_messages":               map[string]any{"instructions_template": codexPrompt},
		}
		if model.ContextWindow != nil {
			entry["context_window"] = *model.ContextWindow
		}
		if plan.DefaultReasoningLevel != nil {
			// withSelectedEffort 已保证所选档位在列表里。
			entry["default_reasoning_level"] = dialect.Spell(*plan.DefaultReasoningLevel)
		}
		models = append(models, entry)
	}
	// serde_json::to_string_pretty 等价：map 键序字典序、2 空格缩进。
	content, err := serializeJSON(map[string]any{"models": models})
	if err != nil {
		return "", err
	}
	return content, nil
}

// defaultModelContextWindow：聚合模型多不在 codex 内置目录里，缺该键时
// codex 会走兜底元数据（context_window = 272000）并告警 Unknown model；
// 有元数据就写默认模型的窗口。
func defaultModelContextWindow(provider *ResolvedProvider, defaultModelID string) *int64 {
	model := findModel(provider, defaultModelID)
	if model == nil {
		return nil
	}
	return model.ContextWindow
}

// findModel 按模型 ID 在平台目录里查找；validate_plan 已保证存在，找不到
// 返回 nil 由调用方防御（apply 路径上不会发生）。
func findModel(provider *ResolvedProvider, modelID string) *ProviderModelRecord {
	for i := range provider.Models {
		if provider.Models[i].ID == modelID {
			return &provider.Models[i]
		}
	}
	return nil
}

// codexSupportedLevels：思考等级：官方值域 low/medium/high/xhigh/max/ultra
// （config-reference，非穷举）。minimal 为 Codex 历史合法档位，保留；off
// （写入 "none"）无任何文档依据，不放行（用户决策，2026-09-25）。
var codexSupportedLevels = []ReasoningLevel{
	ReasoningMinimal,
	ReasoningLow,
	ReasoningMedium,
	ReasoningHigh,
	ReasoningXhigh,
	ReasoningMax,
}

func (a *CodexAdapter) ID() ProviderApp {
	return agents.ProviderCodex
}

func (a *CodexAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	path, err := codexConfigPath(env)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func (a *CodexAdapter) RestrictedPaths(env *support.Environment) []string {
	return nil
}

func (a *CodexAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols:  []ProviderProtocol{ProtocolOpenaiResponses},
		Additive:            false,
		RequiredModelFields: nil,
		// 窗口已写 model_context_window；最大输出与图像输入在 codex 配置里
		// 没有模型级字段，推理能力也只有全局默认档、没有 per-model 开关。
		UnwrittenModelFields:     []ModelField{FieldMaxOutputTokens, FieldSupportsImages, FieldReasoning},
		SupportedReasoningLevels: codexSupportedLevels,
		BehaviorNotes: []string{
			"单活动提供商：应用会替换 Codex 当前配置中的供应商设置。",
			"已运行的 Codex 不会自动读取变更（0.159 起模型目录由后台守护进程缓存）：完全退出后重开；仍未生效时执行 codex app-server daemon restart。",
			"Codex 桌面应用的模型控件只展示其设置里「可用推理强度」勾选过的档位。",
			"Reins 另写 reins-models.json 模型目录（整体替换语义）：应用期间 Codex 内置模型不出现在选择器，移除平台后恢复。",
		},
	}
}

func (a *CodexAdapter) ReasoningDialect() *ReasoningDialect {
	return identityReasoning
}

func (a *CodexAdapter) ProtocolDialect() *ProtocolDialect {
	return codexProtocol
}

func (a *CodexAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	path, err := codexConfigPath(env)
	if err != nil {
		return nil, err
	}
	state := EmptyState(agents.ProviderCodex, []string{path})
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return state, nil
	}
	state.ConfigExists = true
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	// 应用弹窗据此预选；反读值只用于回显，不参与漂移判定。
	if effort, ok := nodeAsTOMLStr(tomlGet(root, "model_reasoning_effort")); ok {
		if level, ok := identityReasoning.Parse(effort); ok {
			levelCopy := level
			state.DefaultReasoningLevel = &levelCopy
		}
	}
	modelProvider, _ := nodeAsTOMLStr(tomlGet(root, "model_provider"))
	model, _ := nodeAsTOMLStr(tomlGet(root, "model"))

	providersTable, ok := tomlTable(tomlGet(root, "model_providers"))
	if !ok {
		return state, nil
	}
	for _, key := range sortedStringKeys(providersTable) {
		value, _ := tomlTable(providersTable[key])
		baseURL, hasBaseURL := nodeAsTOMLStr(tomlGet(value, "base_url"))
		protocol := (*ProviderProtocol)(nil)
		if wire, ok := nodeAsTOMLStr(tomlGet(value, "wire_api")); ok {
			if parsed, ok := codexProtocol.Parse(wire); ok {
				protocol = &parsed
			}
		}
		var entry ProviderAppEntry
		if strings.HasPrefix(key, ReinsPrefix) {
			entry = classifyReinsEntry(key, baseURL, hasBaseURL, protocol, providers)
			if modelProvider == key {
				entry.DefaultModelID = stringPtrOrNil(model)
				if model != "" {
					entry.ModelIDs = []string{model}
				}
			}
			// wire_api 缺失或不是协议拼写都算手工修改痕迹。
			wire, _ := nodeAsTOMLStr(tomlGet(value, "wire_api"))
			if wire != wireAPI() {
				entry.Notes = append(entry.Notes,
					fmt.Sprintf("wire_api 不是 %s，可能已被手工修改。", wireAPI()))
			}
		} else {
			label, _ := nodeAsTOMLStr(tomlGet(value, "name"))
			var notes []string
			if label != "" {
				notes = []string{fmt.Sprintf("外部 Provider：%s", label)}
			}
			entry = externalEntry(key, baseURL, hasBaseURL, notes, protocol)
		}
		state.Entries = append(state.Entries, entry)
	}

	return state, nil
}

func (a *CodexAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	path, err := codexConfigPath(env)
	if err != nil {
		return nil, err
	}
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}

	// 替换语义：写入前记录旧的活动 Provider，换到另一个平台时把属于
	// 旧 Reins 应用的思考等级一并清掉。
	previousProvider, _ := nodeAsTOMLStr(tomlGet(root, "model_provider"))
	replacingReins := strings.HasPrefix(previousProvider, ReinsPrefix)

	registration := RegistrationKey(provider.ID)

	// Codex 是单活动 Provider：应用新平台时移除旧的、仍可识别的
	// reins- 条目；用户手工改过的漂移配置不自动删。
	if providersTable, ok := tomlTable(tomlGet(root, "model_providers")); ok {
		for _, key := range sortedStringKeys(providersTable) {
			value, _ := tomlTable(providersTable[key])
			_, hasBaseURL := nodeAsTOMLStr(tomlGet(value, "base_url"))
			wire, hasWire := nodeAsTOMLStr(tomlGet(value, "wire_api"))
			if strings.HasPrefix(key, ReinsPrefix) && key != registration &&
				hasBaseURL && hasWire && wire == wireAPI() {
				delete(providersTable, key)
			}
		}
	}
	providerTable := map[string]any{
		"name":                      provider.Label,
		"base_url":                  provider.BaseURL,
		"wire_api":                  wireAPI(),
		"experimental_bearer_token": apiKey,
	}

	// readTOML 的顶层恒为表（TOML 文档根），Rust 的 is_table 防御在 Go
	// 形态下不可达，不再保留。
	rootTable := root
	providersAny, exists := rootTable["model_providers"]
	if !exists {
		providersTable := map[string]any{}
		rootTable["model_providers"] = providersTable
		providersAny = providersTable
	}
	providersTable, ok := providersAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("model_providers 段不是表：%s", path)
	}
	providersTable[registration] = providerTable

	rootTable["model_provider"] = registration
	rootTable["model"] = plan.DefaultModelID
	// 目录文件先于 config.toml 生成：model_catalog_json 一旦指向缺失或
	// 非法文件，Codex 配置加载整体失败，两个文件由 commands 层按序原子写。
	home, err := codexHomeDir(env)
	if err != nil {
		return nil, err
	}
	catalogPath := filepath.Join(home, catalogFileName)
	rootTable["model_catalog_json"] = catalogPath
	if contextWindow := defaultModelContextWindow(provider, plan.DefaultModelID); contextWindow != nil {
		rootTable["model_context_window"] = *contextWindow
	} else {
		// 元数据被清空后不再写；只有本次是替换 Reins 自己的配置时才
		// 清掉旧值，避免动用户自设的窗口。
		if replacingReins {
			delete(rootTable, "model_context_window")
		}
	}
	if plan.DefaultReasoningLevel != nil {
		rootTable["model_reasoning_effort"] = identityReasoning.Spell(*plan.DefaultReasoningLevel)
	} else {
		// 同一个平台重新应用时保留旧值：本次没选等级不代表要撤销
		// 上次的选择；换到另一个平台才清掉。
		if replacingReins && previousProvider != registration {
			delete(rootTable, "model_reasoning_effort")
		}
	}

	catalogContent, err := buildCatalog(provider, plan, identityReasoning)
	if err != nil {
		return nil, err
	}
	configContent, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{
		{Path: catalogPath, Content: catalogContent},
		{Path: path, Content: configContent},
	}, nil
}

func (a *CodexAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	_ = provider
	path, err := codexConfigPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("Codex 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	rootTable := root
	registration := RegistrationKey(providerID)

	recognizable := false
	if providersTable, ok := tomlTable(tomlGet(rootTable, "model_providers")); ok {
		if entry, exists := providersTable[registration]; exists {
			entryTable, _ := tomlTable(entry)
			// 只删内容仍可识别的条目：必须带 base_url 且 wire_api 是
			// responses；用户改过的漂移配置不自动删。
			_, hasBaseURL := nodeAsTOMLStr(tomlGet(entryTable, "base_url"))
			wire, hasWire := nodeAsTOMLStr(tomlGet(entryTable, "wire_api"))
			if !hasBaseURL || !hasWire || wire != wireAPI() {
				return nil, modifiedEntryError(registration)
			}
			recognizable = true
			delete(providersTable, registration)
		}
	}
	if !recognizable {
		return nil, unrecognizedEntryError("Codex", registration)
	}

	// model_catalog_json 只在仍指向 Reins 的目录文件时清；用户自己
	// 的目录指向不动。目录文件本身留在原地（写入契约只产出文件
	// 内容），下次 apply 覆盖，不再被引用即无害。
	home, err := codexHomeDir(env)
	if err != nil {
		return nil, err
	}
	catalogPath := filepath.Join(home, catalogFileName)
	if pointer, ok := nodeAsTOMLStr(tomlGet(rootTable, "model_catalog_json")); ok && pointer == catalogPath {
		delete(rootTable, "model_catalog_json")
	}

	if current, _ := nodeAsTOMLStr(tomlGet(rootTable, "model_provider")); current == registration {
		delete(rootTable, "model_provider")
		delete(rootTable, "model_reasoning_effort")
		// model 只有在仍指向该平台的模型目录时才随移除清掉。
		if provider != nil {
			model, _ := nodeAsTOMLStr(tomlGet(rootTable, "model"))
			modelMatches := false
			var metadataWindow *int64
			if model != "" {
				if entry := findModel(provider, model); entry != nil {
					modelMatches = true
					metadataWindow = entry.ContextWindow
				}
			}
			// 窗口值只有仍等于该模型元数据时才清，避免误删用户自设值。
			if metadataWindow != nil {
				if current, ok := tomlInt64(tomlGet(rootTable, "model_context_window")); ok && current == *metadataWindow {
					delete(rootTable, "model_context_window")
				}
			}
			if modelMatches {
				delete(rootTable, "model")
			}
		}
	}

	content, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func (a *CodexAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	path, err := codexConfigPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("Codex 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	rootTable := root

	// 与 reins- 条目移除一致：活动 Provider 指向被删条目时随删除清空，
	// 不再要求先切换。model 是裸模型名，不属于特定条目，保持不动。
	if current, _ := nodeAsTOMLStr(tomlGet(rootTable, "model_provider")); current == entryKey {
		delete(rootTable, "model_provider")
		delete(rootTable, "model_reasoning_effort")
	}

	providersTable, ok := tomlTable(tomlGet(rootTable, "model_providers"))
	if !ok {
		return nil, entryNotFoundError("Codex", entryKey)
	}
	if _, exists := providersTable[entryKey]; !exists {
		return nil, entryNotFoundError("Codex", entryKey)
	}
	delete(providersTable, entryKey)

	content, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

// ---------------------------------------------------------------------------
// toml.Value 的小工具：BurntSushi 解码到 map[string]any 后的形状访问
// ---------------------------------------------------------------------------

func tomlGet(table map[string]any, key string) any {
	if table == nil {
		return nil
	}
	return table[key]
}

func tomlTable(value any) (map[string]any, bool) {
	table, ok := value.(map[string]any)
	return table, ok
}

func nodeAsTOMLStr(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

func tomlInt64(value any) (int64, bool) {
	return asInt64(value)
}
