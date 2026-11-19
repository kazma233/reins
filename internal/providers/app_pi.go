// Pi：$PI_CODING_AGENT_DIR/models.json + settings.json（默认 ~/.pi/agent）。
// 多 Provider 并存：新增 reins- 条目；默认值写 settings.json。
package providers

import (
	"fmt"
	"os"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

type PiAdapter struct{}

// clearDefaultRefs：默认 Provider/Model/ThinkingLevel 三元组随删除清空;只有
// settings 真正被修改时才写回,避免凭空创建 settings.json。reins- 条目与
// 外部条目两条删除路径共用。
func clearDefaultRefs(settings map[string]any, settingsPath string, entryKey string, files *[]FileOutput) error {
	if current, _ := asString(settings["defaultProvider"]); current == entryKey {
		delete(settings, "defaultProvider")
		delete(settings, "defaultModel")
		delete(settings, "defaultThinkingLevel")
		content, err := serializeJSON(settings)
		if err != nil {
			return err
		}
		*files = append(*files, FileOutput{Path: settingsPath, Content: content})
	}
	return nil
}

// thinkingLevelMap：Pi 的思考等级选择器只对 thinkingLevelMap 里显式声明的
// 模型开放 xhigh/max 档（pi getSupportedThinkingLevels：值 null 禁用该档，
// xhigh/max 缺键即不可选），因此模型的 reasoning_levels 必须逐档下发，
// 否则 Reins 应用的推理模型在 Pi 里最高只能选到 high。键是 Pi 档位名
// （reasoning_dialect 的拼写），值是该档实际发给 API 的 effort（off 档
// 为 none，Pi 各协议的缺省值）——值侧是独立于方言表的第二套拼写，
// 不得「顺手统一」成 dialect.Spell。
func thinkingLevelMap(levels []ReasoningLevel) map[string]any {
	selected := map[ReasoningLevel]bool{}
	for _, level := range levels {
		selected[level] = true
	}
	mapNode := map[string]any{}
	for _, level := range AllReasoningLevels() {
		if selected[level] {
			mapNode[piAIReasoning.Spell(level)] = level.WriteForm()
		} else {
			mapNode[piAIReasoning.Spell(level)] = nil
		}
	}
	return mapNode
}

func (a *PiAdapter) ID() ProviderApp {
	return agents.ProviderPi
}

func (a *PiAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	modelsPath, err := piModelsPath(env)
	if err != nil {
		return nil, err
	}
	settingsPath, err := piSettingsPath(env)
	if err != nil {
		return nil, err
	}
	return []string{modelsPath, settingsPath}, nil
}

func (a *PiAdapter) RestrictedPaths(env *support.Environment) []string {
	return nil
}

func (a *PiAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols:  AllProtocols(),
		Additive:            true,
		RequiredModelFields: nil,
		// Pi 的 models.json 逐模型写 reasoning/contextWindow/maxTokens/input。
		UnwrittenModelFields:     nil,
		SupportedReasoningLevels: AllReasoningLevels(),
		BehaviorNotes: []string{
			"默认 Provider、模型与思考档写在 settings.json；移除平台时这三项随删清空。",
		},
	}
}

func (a *PiAdapter) ReasoningDialect() *ReasoningDialect {
	return piAIReasoning
}

func (a *PiAdapter) ProtocolDialect() *ProtocolDialect {
	return piAIProtocol
}

func (a *PiAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	modelsPath, err := piModelsPath(env)
	if err != nil {
		return nil, err
	}
	settingsPath, err := piSettingsPath(env)
	if err != nil {
		return nil, err
	}
	state := EmptyState(agents.ProviderPi, []string{modelsPath, settingsPath})
	modelsExists := fileExists(modelsPath)
	settingsExists := fileExists(settingsPath)
	if !modelsExists && !settingsExists {
		return state, nil
	}
	state.ConfigExists = true
	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return nil, err
	}
	// 应用弹窗据此预选；反读值只用于回显，不参与漂移判定。
	if level, ok := asString(settings["defaultThinkingLevel"]); ok {
		if parsed, ok := piAIReasoning.Parse(level); ok {
			levelCopy := parsed
			state.DefaultReasoningLevel = &levelCopy
		}
	}
	defaultProvider, _ := asString(settings["defaultProvider"])
	defaultModel, _ := asString(settings["defaultModel"])

	root, err := readJSONObject(modelsPath)
	if err != nil {
		return nil, err
	}
	table, ok := asObject(root["providers"])
	if !ok {
		return state, nil
	}
	for _, key := range sortedStringKeys(table) {
		value, ok := asObject(table[key])
		if !ok {
			continue
		}
		baseURL, hasBaseURL := asString(value["baseUrl"])
		protocol := (*ProviderProtocol)(nil)
		if api, ok := asString(value["api"]); ok {
			if parsed, ok := piAIProtocol.Parse(api); ok {
				protocol = &parsed
			}
		}
		var entry ProviderAppEntry
		if strings.HasPrefix(key, ReinsPrefix) {
			entry = classifyReinsEntry(key, baseURL, hasBaseURL, protocol, providers)
			if modelsArray, ok := asArray(value["models"]); ok {
				modelIDs := make([]string, 0, len(modelsArray))
				for _, model := range modelsArray {
					if modelObject, ok := asObject(model); ok {
						if id, ok := asString(modelObject["id"]); ok {
							modelIDs = append(modelIDs, id)
						}
					}
				}
				entry.ModelIDs = modelIDs
			}
			if defaultProvider == key {
				entry.DefaultModelID = stringPtrOrNil(defaultModel)
			}
		} else {
			entry = externalEntry(key, baseURL, hasBaseURL, []string{"外部 Provider。"}, protocol)
		}
		state.Entries = append(state.Entries, entry)
	}

	return state, nil
}

func (a *PiAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	modelsPath, err := piModelsPath(env)
	if err != nil {
		return nil, err
	}
	settingsPath, err := piSettingsPath(env)
	if err != nil {
		return nil, err
	}
	registration := RegistrationKey(provider.ID)

	root, err := readJSONObject(modelsPath)
	if err != nil {
		return nil, err
	}
	// Pi 的 models.json schema 要求 models 是数组，元素带 id 字段
	//（真实文件验证：id/name/reasoning/contextWindow/maxTokens/input）。
	models := make([]any, 0, len(plan.ModelIDs))
	for _, modelID := range plan.ModelIDs {
		model := findModel(provider, modelID)
		if model == nil {
			// validate_plan 已保证选中模型都在目录里；防御异常输入。
			return nil, fmt.Errorf("模型 %s 不在平台 %s 的目录中。", modelID, provider.ID)
		}
		modelEntry := map[string]any{
			"id":   model.ID,
			"name": model.Label,
		}
		if model.Reasoning != nil {
			modelEntry["reasoning"] = *model.Reasoning
		}
		// 未填等级时不写 map：Pi 对无 map 的模型默认开放 off～high，
		// 与空档位集的语义一致；写全 null 反而连 off 档都会消失。
		if len(model.ReasoningLevels) > 0 {
			modelEntry["thinkingLevelMap"] = thinkingLevelMap(model.ReasoningLevels)
		}
		if model.ContextWindow != nil {
			modelEntry["contextWindow"] = *model.ContextWindow
		}
		if model.MaxOutputTokens != nil {
			modelEntry["maxTokens"] = *model.MaxOutputTokens
		}
		if model.SupportsImages != nil {
			input := []any{"text"}
			if *model.SupportsImages {
				input = append(input, "image")
			}
			modelEntry["input"] = input
		}
		models = append(models, modelEntry)
	}

	providerEntry := map[string]any{
		"baseUrl": provider.BaseURL,
		"api":     piAIProtocol.Spell(provider.Protocol),
		"apiKey":  apiKey,
		"models":  models,
	}

	providersAny, exists := root["providers"]
	if !exists {
		providersTable := map[string]any{}
		root["providers"] = providersTable
		providersAny = providersTable
	}
	providersTable, ok := asObject(providersAny)
	if !ok {
		return nil, fmt.Errorf("providers 段不是对象：%s", modelsPath)
	}
	providersTable[registration] = providerEntry

	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return nil, err
	}
	settings["defaultProvider"] = registration
	settings["defaultModel"] = plan.DefaultModelID
	if plan.DefaultReasoningLevel != nil {
		settings["defaultThinkingLevel"] = piAIReasoning.Spell(*plan.DefaultReasoningLevel)
	}

	modelsContent, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	settingsContent, err := serializeJSON(settings)
	if err != nil {
		return nil, err
	}
	return []FileOutput{
		{Path: modelsPath, Content: modelsContent},
		{Path: settingsPath, Content: settingsContent},
	}, nil
}

func (a *PiAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	_ = provider
	modelsPath, err := piModelsPath(env)
	if err != nil {
		return nil, err
	}
	settingsPath, err := piSettingsPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(modelsPath) {
		return nil, fmt.Errorf("Pi models.json 不存在：%s", support.DisplayPath(modelsPath))
	}
	root, err := readJSONObject(modelsPath)
	if err != nil {
		return nil, err
	}
	registration := RegistrationKey(providerID)

	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return nil, err
	}
	files := make([]FileOutput, 0, 2)
	if err := clearDefaultRefs(settings, settingsPath, registration, &files); err != nil {
		return nil, err
	}

	providersAny, exists := root["providers"]
	if !exists {
		return nil, unrecognizedEntryError("Pi", registration)
	}
	providersTable, ok := asObject(providersAny)
	if !ok {
		return nil, unrecognizedEntryError("Pi", registration)
	}
	entryAny, exists := providersTable[registration]
	if !exists {
		return nil, unrecognizedEntryError("Pi", registration)
	}
	entry, _ := asObject(entryAny)
	if entry == nil {
		return nil, unrecognizedEntryError("Pi", registration)
	}
	if _, hasBaseURL := asString(entry["baseUrl"]); !hasBaseURL {
		return nil, modifiedEntryError(registration)
	}
	delete(providersTable, registration)
	if len(providersTable) == 0 {
		delete(root, "providers")
	}

	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	files = append(files, FileOutput{Path: modelsPath, Content: content})
	return files, nil
}

func (a *PiAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	modelsPath, err := piModelsPath(env)
	if err != nil {
		return nil, err
	}
	settingsPath, err := piSettingsPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(modelsPath) {
		return nil, fmt.Errorf("Pi models.json 不存在：%s", support.DisplayPath(modelsPath))
	}
	root, err := readJSONObject(modelsPath)
	if err != nil {
		return nil, err
	}

	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return nil, err
	}
	files := make([]FileOutput, 0, 2)
	if err := clearDefaultRefs(settings, settingsPath, entryKey, &files); err != nil {
		return nil, err
	}

	providersAny, exists := root["providers"]
	if !exists {
		return nil, entryNotFoundError("Pi", entryKey)
	}
	providersTable, ok := asObject(providersAny)
	if !ok {
		return nil, entryNotFoundError("Pi", entryKey)
	}
	if _, exists := providersTable[entryKey]; !exists {
		return nil, entryNotFoundError("Pi", entryKey)
	}
	delete(providersTable, entryKey)
	if len(providersTable) == 0 {
		delete(root, "providers")
	}

	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	files = append(files, FileOutput{Path: modelsPath, Content: content})
	return files, nil
}

// fileExists：存在性判断（跟随符号链接语义，等价 Rust Path::exists）。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
