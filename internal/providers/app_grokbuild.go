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
		// Grok 有 context_window 与 reasoning_efforts 菜单落点；最大输出
		// （max_completion_tokens 属采样上限，Reins 不写）与图像输入
		// （配置无对应字段）无落点。
		UnwrittenModelFields:     []ModelField{FieldMaxOutputTokens, FieldSupportsImages},
		SupportedReasoningLevels: AllReasoningLevels(),
		BehaviorNotes: []string{
			"Grok 的思考档位是每个模型一份菜单：Reins 按模型声明的思考等级写 reasoning_efforts；模型未声明等级时沿用 Grok 默认菜单（low/medium/high/xhigh，端点自报菜单时以端点为准）。",
			"每个菜单会标一个默认项：默认模型优先用工具默认档，其余模型取 high（未声明 high 时取最高档）；否则 Grok 会退回菜单第一项，按枚举序可能是「无」。",
			"默认思考等级只作用于默认模型，且必须是该模型菜单中的一档；不在菜单内时会拒绝应用（Grok 的 /effort 与 --effort 也只接受菜单内的档位）。",
			"「最高（max）」只在模型菜单声明 max 时按 max 写入；模型未声明等级时折算为 xhigh（Grok 旧式默认菜单没有 max）。",
			"关闭档写作 none（Grok 不接受 off）；声明为不推理的模型写 supports_reasoning_effort = false，避免继承内置同名模型（grok-4.5/4.6）的菜单。",
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
	// 应用弹窗据此预选；反读值只用于回显，不参与漂移判定。模型有菜单时
	// max 原样写盘；没有等级元数据时折算 xhigh，反读也就是 xhigh。
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
		// 思考档位菜单：Grok 1.0.92 起每个模型一份 reasoning_efforts 菜单，
		// 值域固定为 none/minimal/low/medium/high/xhigh/max；数组里任意一项
		// 非法（含未知 value）会让整个菜单静默失效，所以只按模型声明的等级
		// 写，不猜。未声明等级的推理模型保留旧式 supports_reasoning_effort，
		// 让 Grok 从端点 /v1/models 或内置默认菜单取档位；显式不推理的模型
		// 写 false，阻断与内置同名模型（grok-4.5/4.6）的菜单继承。
		switch {
		case model.Reasoning != nil && !*model.Reasoning:
			entry["supports_reasoning_effort"] = false
		case len(model.ReasoningLevels) > 0:
			// 菜单默认项只对默认模型跟随工具默认档；其余模型取 high/最高档，
			// 否则 Grok 会退回菜单第一项（可能是 none）。
			var preferred *ReasoningLevel
			if modelID == plan.DefaultModelID {
				preferred = plan.DefaultReasoningLevel
			}
			entry["reasoning_efforts"] = grokReasoningEfforts(model.ReasoningLevels, preferred)
		case model.Reasoning != nil && *model.Reasoning:
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
		modelsSection["default_reasoning_effort"] = grokDefaultEffort(
			findModel(provider, plan.DefaultModelID), *plan.DefaultReasoningLevel)
	}

	content, err := serializeTOML(rootTable)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

// grokReasoningEfforts：把模型声明的等级写成 Grok 的 reasoning_efforts 数组。
// value 是唯一必填字段，label/description 省略时 Grok 按档位自动补；数组里
// 任意一项非法都会让整个菜单静默失效，来源等级已在 providers.yaml 反序列化
// 期经过枚举校验，不会产出非法 value。必须标一个 default 项：没有 default
// 时 Grok 取数组第一项，按枚举序可能是 none。
func grokReasoningEfforts(levels []ReasoningLevel, preferred *ReasoningLevel) []map[string]any {
	defaultLevel, hasDefault := grokMenuDefault(levels, preferred)
	efforts := make([]map[string]any, 0, len(levels))
	for _, level := range levels {
		item := map[string]any{"value": grokReasoning.Spell(level)}
		if hasDefault && level == defaultLevel {
			item["default"] = true
		}
		efforts = append(efforts, item)
	}
	return efforts
}

// grokMenuDefault：菜单的 default 项。工具默认档在模型声明范围内时优先用它
// （只有默认模型会传入）；否则取 high（Grok 内置菜单的默认档），模型没有
// high 时取声明的最高档。
func grokMenuDefault(levels []ReasoningLevel, preferred *ReasoningLevel) (ReasoningLevel, bool) {
	if len(levels) == 0 {
		return "", false
	}
	if preferred != nil {
		for _, level := range levels {
			if level == *preferred {
				return level, true
			}
		}
	}
	for _, level := range levels {
		if level == ReasoningHigh {
			return ReasoningHigh, true
		}
	}
	highest := levels[0]
	for _, level := range levels[1:] {
		if reasoningRank(level) > reasoningRank(highest) {
			highest = level
		}
	}
	return highest, true
}

// grokDefaultEffort：默认档的写入值。模型有自己的菜单时按菜单值写（max 是
// 独立档位）；没有等级元数据时退回 Grok 旧式默认菜单
// （low/medium/high/xhigh），该菜单没有 max，只能写旧式最高档 xhigh。
func grokDefaultEffort(model *ProviderModelRecord, level ReasoningLevel) string {
	if model != nil && len(model.ReasoningLevels) > 0 {
		return grokReasoning.Spell(level)
	}
	if level == ReasoningMax {
		return grokReasoning.Spell(ReasoningXhigh)
	}
	return grokReasoning.Spell(level)
}

// ValidateDefaultReasoningLevel：默认档写进 [models].default_reasoning_effort，
// 只作用于默认模型；Grok 的 /effort 与 --effort 只接受当前模型菜单内的档位
// （1.0.92 实测 error: unknown effort level ... use one of），所以默认模型
// 声明了等级集时必须在写盘前校验。模型没有等级元数据时菜单由端点或内置
// 默认决定，Reins 无从判断，保持旧式兜底不阻断。
func (a *GrokbuildAdapter) ValidateDefaultReasoningLevel(provider *ResolvedProvider, plan *ApplyProviderInput) error {
	if plan.DefaultReasoningLevel == nil {
		return nil
	}
	model := findModel(provider, plan.DefaultModelID)
	if model == nil {
		return nil
	}
	if model.Reasoning != nil && !*model.Reasoning {
		return fmt.Errorf("默认模型 %s 声明为不支持思考等级，不能设置默认思考等级。", model.ID)
	}
	if len(model.ReasoningLevels) == 0 {
		return nil
	}
	for _, level := range model.ReasoningLevels {
		if level == *plan.DefaultReasoningLevel {
			return nil
		}
	}
	allowed := make([]string, 0, len(model.ReasoningLevels))
	for _, level := range model.ReasoningLevels {
		allowed = append(allowed, grokReasoning.Spell(level))
	}
	return fmt.Errorf("默认思考等级 %s 不在模型 %s 的菜单内（可选：%s）。",
		grokReasoning.Spell(*plan.DefaultReasoningLevel), model.ID, strings.Join(allowed, "、"))
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
