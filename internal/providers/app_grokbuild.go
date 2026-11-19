// Grok Build：$GROK_HOME/config.toml（默认 ~/.grok/config.toml）。
// 多 Provider 并存：[model_providers.reins-x] + 每个模型一个
// [model."reins-x--<id>"] 条目；默认值写 [models].default 与
// [models].default_reasoning_effort。api_backend 取值已获官方确认
// （docs.x.ai/build/settings/reference：chat_completions/responses/messages），
// 但 [model_providers] 表本身官方未文档化，见验证文档备注。
package providers

import (
	"fmt"
	"sort"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

type GrokbuildAdapter struct{}

const (
	grokAnthropicVersionHeader = "anthropic-version"
	grokAnthropicVersion       = "2023-06-01"
)

// grokBaseURL：Grok 的 messages 后端固定请求 {base_url}/messages，不补 /v1
// 段（grok 1.0.41 本地监听实测）；而 anthropic 生态约定 base_url 不带
// /v1、由 SDK 拼版本段。写入时统一补 /v1，反读比对按同一口径折算，
// 避免应用后误判漂移。
func grokBaseURL(protocol ProviderProtocol, baseURL string) string {
	trimmed := strings.TrimRight(baseURL, "/")
	if protocol == ProtocolAnthropicMessages && !strings.HasSuffix(trimmed, "/v1") {
		return trimmed + "/v1"
	}
	return baseURL
}

// grokBaseURLMatches：反读比对口径：Anthropic 平台在 Grok 配置里写的是补了
// /v1 的 base_url（见 grokBaseURL），classify 前对平台侧做同样折算再比较。
func grokBaseURLMatches(entryBaseURL string, hasEntryBaseURL bool, provider *ResolvedProvider) bool {
	return hasEntryBaseURL && entryBaseURL == grokBaseURL(provider.Protocol, provider.BaseURL)
}

func modelEntryKey(registration string, modelID string) string {
	return registration + "--" + modelID
}

// clearModelsDefault：models.default 与 default_reasoning_effort 是一对:
// 默认指向被删条目时随删除清掉,单独留思考等级没有意义。Provider 树与
// 独立模型两条删除路径共用。
func clearModelsDefault(rootTable map[string]any) {
	if modelsTable, ok := tomlTable(tomlGet(rootTable, "models")); ok {
		delete(modelsTable, "default")
		delete(modelsTable, "default_reasoning_effort")
	}
}

// removeProviderTree：remove 与 remove_external 的共享删除路径：清掉指向
// 该键的默认值、删除 model_providers 条目与 `{key}--` 前缀的模型条目。
// requireRecognizable 区分两条路径的防护：reins- 条目内容被手工改过时
// 不自动删，外部条目内容任意、直接删。返回是否删除了内容，无条目的
// 报错文案由调用方决定。
func removeProviderTree(rootTable map[string]any, key string, requireRecognizable bool) (bool, error) {
	// 默认模型仍指向该条目时随移除清掉；思考等级修饰默认值，一并清。
	if modelsTable, ok := tomlTable(tomlGet(rootTable, "models")); ok {
		if defaultModel, ok := nodeAsTOMLStr(tomlGet(modelsTable, "default")); ok &&
			strings.HasPrefix(defaultModel, key+"--") {
			clearModelsDefault(rootTable)
		}
	}

	removed := false
	if providersTable, ok := tomlTable(tomlGet(rootTable, "model_providers")); ok {
		if entry, exists := providersTable[key]; exists {
			if requireRecognizable {
				entryTable, _ := tomlTable(entry)
				_, hasBaseURL := nodeAsTOMLStr(tomlGet(entryTable, "base_url"))
				_, hasBackend := nodeAsTOMLStr(tomlGet(entryTable, "api_backend"))
				if !hasBaseURL || !hasBackend {
					return false, modifiedEntryError(key)
				}
			}
			delete(providersTable, key)
			removed = true
		}
	}

	if modelTable, ok := tomlTable(tomlGet(rootTable, "model")); ok {
		for _, modelKey := range sortedStringKeys(modelTable) {
			value, _ := tomlTable(modelTable[modelKey])
			if provider, _ := nodeAsTOMLStr(tomlGet(value, "model_provider")); provider == key {
				delete(modelTable, modelKey)
				removed = true
			}
		}
		if len(modelTable) == 0 {
			delete(rootTable, "model")
		}
	}

	return removed, nil
}

// removeStandaloneModel：独立模型条目（[model.<键>] 自带 base_url，不挂
// model_provider）的删除路径：删掉该模型键，并在它被 [models].default 引用
// 时一并清默认值，避免留下指向缺失模型的引用。返回是否删除了内容。
func removeStandaloneModel(rootTable map[string]any, key string) bool {
	removed := false
	if modelTable, ok := tomlTable(tomlGet(rootTable, "model")); ok {
		if _, exists := modelTable[key]; exists {
			delete(modelTable, key)
			removed = true
		}
		if len(modelTable) == 0 {
			delete(rootTable, "model")
		}
	}
	if !removed {
		return false
	}

	if modelsTable, ok := tomlTable(tomlGet(rootTable, "models")); ok {
		if defaultModel, _ := nodeAsTOMLStr(tomlGet(modelsTable, "default")); defaultModel == key {
			clearModelsDefault(rootTable)
		}
	}
	return true
}

func (a *GrokbuildAdapter) ID() ProviderApp {
	return agents.ProviderGrokbuild
}

func (a *GrokbuildAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	path, err := grokConfigPath(env)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func (a *GrokbuildAdapter) RestrictedPaths(env *support.Environment) []string {
	return nil
}

func (a *GrokbuildAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols:  AllProtocols(),
		Additive:            true,
		RequiredModelFields: nil,
		// Grok 有 context_window 与 supports_reasoning_effort 落点；
		// 最大输出（max_completion_tokens 属采样上限，Reins 不写）与
		// 图像输入（配置无对应字段）无落点。
		UnwrittenModelFields:     []ModelField{FieldMaxOutputTokens, FieldSupportsImages},
		SupportedReasoningLevels: AllReasoningLevels(),
		BehaviorNotes: []string{
			"选择最高（max）档时实际以 xhigh 落盘：Grok 交互界面只渲染到 xhigh，两档的 thinking 参数完全相同（1.0.41 实测）。",
			"Anthropic Messages 协议下 Grok 固定请求 {Base URL}/messages，不自动补 /v1 段。",
		},
	}
}

func (a *GrokbuildAdapter) ReasoningDialect() *ReasoningDialect {
	return grokReasoning
}

func (a *GrokbuildAdapter) ProtocolDialect() *ProtocolDialect {
	return grokProtocol
}

func (a *GrokbuildAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	path, err := grokConfigPath(env)
	if err != nil {
		return nil, err
	}
	state := EmptyState(agents.ProviderGrokbuild, []string{path})
	if !fileExists(path) {
		return state, nil
	}
	state.ConfigExists = true
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	// 应用弹窗据此预选；反读值只用于回显，不参与漂移判定。写入时 max
	// 已折算成 xhigh，反读同样是 xhigh，两边写入值等价。
	if modelsTable, ok := tomlTable(tomlGet(root, "models")); ok {
		if effort, ok := nodeAsTOMLStr(tomlGet(modelsTable, "default_reasoning_effort")); ok {
			if level, ok := grokReasoning.Parse(effort); ok {
				levelCopy := level
				state.DefaultReasoningLevel = &levelCopy
			}
		}
	}
	defaultModel := ""
	if modelsTable, ok := tomlTable(tomlGet(root, "models")); ok {
		defaultModel, _ = nodeAsTOMLStr(tomlGet(modelsTable, "default"))
	}

	providerTable, hasProviderTable := tomlTable(tomlGet(root, "model_providers"))
	if hasProviderTable {
		for _, key := range sortedStringKeys(providerTable) {
			value, _ := tomlTable(providerTable[key])
			baseURL, hasBaseURL := nodeAsTOMLStr(tomlGet(value, "base_url"))
			protocol := (*ProviderProtocol)(nil)
			if backend, ok := nodeAsTOMLStr(tomlGet(value, "api_backend")); ok {
				if parsed, ok := grokProtocol.Parse(backend); ok {
					protocol = &parsed
				}
			}
			var entry ProviderAppEntry
			if strings.HasPrefix(key, ReinsPrefix) {
				entry = classifyReinsEntryWith(key, baseURL, hasBaseURL, protocol, providers, grokBaseURLMatches)
				// 属于该 provider 的模型条目：model 表中 model_provider
				// 指向注册键的 reins- 键。
				if modelTable, ok := tomlTable(tomlGet(root, "model")); ok {
					modelIDs := make([]string, 0, 4)
					for _, modelKey := range sortedStringKeys(modelTable) {
						if !strings.HasPrefix(modelKey, key+"--") {
							continue
						}
						if modelValue, ok := tomlTable(modelTable[modelKey]); ok {
							if modelID, ok := nodeAsTOMLStr(tomlGet(modelValue, "model")); ok {
								modelIDs = append(modelIDs, modelID)
							}
						}
					}
					sort.Strings(modelIDs)
					entry.ModelIDs = modelIDs
				}
				if defaultModel != "" {
					prefix := key + "--"
					if modelID, found := strings.CutPrefix(defaultModel, prefix); found {
						entry.DefaultModelID = stringPtr(modelID)
					}
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
	}

	// 独立 [model.<键>] 条目（官方简化式：模型自带 base_url/api_backend，
	// 不挂 model_provider）不落在 model_providers 里，按模型键展示为外部
	// 配置；挂到某个 Provider 条目下的模型已由该条目代表，不重复列出。
	if modelTable, ok := tomlTable(tomlGet(root, "model")); ok {
		for _, modelKey := range sortedStringKeys(modelTable) {
			value, _ := tomlTable(modelTable[modelKey])
			mounted := false
			if providerKey, ok := nodeAsTOMLStr(tomlGet(value, "model_provider")); ok && hasProviderTable {
				_, mounted = providerTable[providerKey]
			}
			// 键与某个 Provider 条目同名时无法在界面区分，留给该条目。
			_, shadowed := providerTable[modelKey]
			if strings.HasPrefix(modelKey, ReinsPrefix) || mounted || shadowed {
				continue
			}
			label, _ := nodeAsTOMLStr(tomlGet(value, "name"))
			var notes []string
			if label != "" {
				notes = []string{fmt.Sprintf("外部模型：%s", label)}
			}
			baseURL, hasBaseURL := nodeAsTOMLStr(tomlGet(value, "base_url"))
			protocol := (*ProviderProtocol)(nil)
			if backend, ok := nodeAsTOMLStr(tomlGet(value, "api_backend")); ok {
				if parsed, ok := grokProtocol.Parse(backend); ok {
					protocol = &parsed
				}
			}
			state.Entries = append(state.Entries,
				externalEntry(modelKey, baseURL, hasBaseURL, notes, protocol))
		}
	}

	return state, nil
}

func (a *GrokbuildAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	path, err := grokConfigPath(env)
	if err != nil {
		return nil, err
	}
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	rootTable := root
	registration := RegistrationKey(provider.ID)

	// 1. model_providers 段
	providersAny, exists := rootTable["model_providers"]
	if !exists {
		providersTable := map[string]any{}
		rootTable["model_providers"] = providersTable
		providersAny = providersTable
	}
	providersTable, ok := tomlTable(providersAny)
	if !ok {
		return nil, fmt.Errorf("model_providers 段不是表：%s", path)
	}
	// 实测：provider 表不接受 name 字段（unknown-field 警告）。
	providersTable[registration] = map[string]any{
		"base_url":    grokBaseURL(provider.Protocol, provider.BaseURL),
		"api_backend": grokProtocol.Spell(provider.Protocol),
	}

	// 2. 每个选中模型一个 model 表条目；重新应用时同步移除不在本次
	// 选择里的旧 reins 模型条目（替换该平台的模型集合）。
	modelAny, exists := rootTable["model"]
	if !exists {
		modelTable := map[string]any{}
		rootTable["model"] = modelTable
		modelAny = modelTable
	}
	modelTable, ok := tomlTable(modelAny)
	if !ok {
		return nil, fmt.Errorf("model 段不是表：%s", path)
	}
	for _, key := range sortedStringKeys(modelTable) {
		if strings.HasPrefix(key, registration+"--") {
			delete(modelTable, key)
		}
	}
	for _, modelID := range plan.ModelIDs {
		model := findModel(provider, modelID)
		if model == nil {
			return nil, fmt.Errorf("模型 %s 不在平台 %s 的目录中。", modelID, provider.ID)
		}
		entry := map[string]any{
			"name":           model.Label,
			"model":          model.ID,
			"model_provider": registration,
		}
		// 缺 context_window 时 grok 按 200000 兜底并据此计算自动压缩
		// 时机（1.0.41 debug 日志：missing context_window, defaulting to
		// 200000），有元数据就显式写入。
		if model.ContextWindow != nil {
			entry["context_window"] = *model.ContextWindow
		}
		// 未声明支持的模型会被 grok 判为不支持思考等级并丢弃配置档位
		//（1.0.41 实测 WARN：model does not support effort; ignoring it），
		// 官方字段 supports_reasoning_effort 是 [models].default_reasoning_effort
		// 生效的前提。
		if model.Reasoning != nil && *model.Reasoning {
			entry["supports_reasoning_effort"] = true
		}
		if provider.Protocol == ProtocolAnthropicMessages {
			// anthropic 协议的模型条目用 extra_headers 携带 x-api-key
			// 与 anthropic-version，不写 api_key 字段。
			entry["extra_headers"] = map[string]any{
				"x-api-key":                apiKey,
				grokAnthropicVersionHeader: grokAnthropicVersion,
			}
		} else {
			entry["api_key"] = apiKey
		}
		modelTable[modelEntryKey(registration, modelID)] = entry
	}

	// 3. 默认值：default 与 default_reasoning_effort 都在 [models]
	// 表内（顶层 default_reasoning_effort 会被 grok 判为未知键）。
	modelsAny, exists := rootTable["models"]
	if !exists {
		modelsSection := map[string]any{}
		rootTable["models"] = modelsSection
		modelsAny = modelsSection
	}
	modelsSection, ok := tomlTable(modelsAny)
	if !ok {
		return nil, fmt.Errorf("models 段不是表：%s", path)
	}
	modelsSection["default"] = modelEntryKey(registration, plan.DefaultModelID)
	if plan.DefaultReasoningLevel != nil {
		modelsSection["default_reasoning_effort"] = grokReasoning.Spell(*plan.DefaultReasoningLevel)
	}

	content, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func (a *GrokbuildAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	_ = provider
	path, err := grokConfigPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("Grok 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	rootTable := root
	registration := RegistrationKey(providerID)
	removed, err := removeProviderTree(rootTable, registration, true)
	if err != nil {
		return nil, err
	}
	if !removed {
		return nil, unrecognizedEntryError("Grok", registration)
	}
	content, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func (a *GrokbuildAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	path, err := grokConfigPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("Grok 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readTOML(path)
	if err != nil {
		return nil, err
	}
	rootTable := root
	// 外部条目键来自两处：model_providers 的 Provider 条目，或 model 表的
	// 独立模型键；前者按 Provider 树删除，后者只删模型键。
	providersTable, _ := tomlTable(tomlGet(rootTable, "model_providers"))
	_, isProviderEntry := providersTable[entryKey]
	var removed bool
	if isProviderEntry {
		removed, err = removeProviderTree(rootTable, entryKey, false)
		if err != nil {
			return nil, err
		}
	} else {
		removed = removeStandaloneModel(rootTable, entryKey)
	}
	if !removed {
		return nil, entryNotFoundError("Grok", entryKey)
	}
	content, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}
