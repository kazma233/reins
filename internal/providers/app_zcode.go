// ZCode：~/.zcode/v2/provider_config.json（v2 布局，无官方重定向变量）。
// 多 Provider 并存：providerRules 数组内 reins- 条目与用户自配条目互不
// 影响；官方 OAuth 登录不经该文件（凭据在 ~/.zcode/v2/credentials.json，
// 属 ZCode 自管的敏感存储，不读写）。
// 格式实证（ZCode.app bundle 的 zod schema + 本机文件，2026-10-13）：
//   - api.type ∈ anthropic-messages / openai-chat-completions / openai-responses；
//   - group ∈ standard-personal（自配）/ zai-family / bigmodel-family（官方
//     家族），Reins 只写 standard-personal；
//   - 模型目录 = personalModelIds ∪ 模板内置（模板平台才有）；模型元数据落
//     modelConfigRules.providerModelRules 的 config（enabled /
//     properties.contextWindow / properties.inputFormat.supportsImage /
//     optionSpecs.maxOutputTokens.max）；optionSpecs.reasoningLevel 的内部
//     结构未实证，思考档位与默认模型（ZCode 的模型选择是会话级，不落盘）
//     均不写。
package providers

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

type ZcodeAdapter struct{}

// zcodeProviderConfigPath：provider 配置在 v2 布局（~/.zcode/v2/），与清单
// 登记的 MCP 配置（cli/）互不隶属；根从清单的 GlobalRoot 解析，目录段在此
// 声明。
func zcodeProviderConfigPath(env *support.Environment) (string, error) {
	spec := agents.SpecByProviderApp(agents.ProviderZcode)
	if spec == nil {
		return "", errors.New("zcode 未登记 agents 清单。")
	}
	root, ok := spec.Global.Root.Resolve(env)
	if !ok {
		return "", errors.New("无法解析 zcode 主目录。")
	}
	return filepath.Join(root, ".zcode", "v2", "provider_config.json"), nil
}

// arraySectionMut：深取数组段：缺失时就地建空数组容器，损坏类型报错不吞。
// 返回当前列表与写回函数——Go 的切片头是值拷贝，retain 类修改必须经写回
// 函数更新容器，否则 map 里的旧切片不受影响。
func arraySectionMut(root map[string]any, sectionNames [2]string, listName string) ([]any, func([]any), error) {
	sectionValue, exists := root[sectionNames[0]]
	if !exists {
		sectionValue = map[string]any{}
		root[sectionNames[0]] = sectionValue
	}
	sectionTable, ok := sectionValue.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("provider_config.json 的 %s 段必须是对象。", sectionNames[0])
	}
	nestedValue, exists := sectionTable[sectionNames[1]]
	if !exists {
		nestedValue = map[string]any{}
		sectionTable[sectionNames[1]] = nestedValue
	}
	nestedTable, ok := nestedValue.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("provider_config.json 的 %s 段必须是对象。", sectionNames[1])
	}
	listValue, exists := nestedTable[listName]
	if !exists {
		listValue = []any{}
		nestedTable[listName] = listValue
	}
	list, ok := listValue.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("provider_config.json 的 %s 必须是数组。", listName)
	}
	return list, func(updated []any) { nestedTable[listName] = updated }, nil
}

func providerRulesMut(root map[string]any) ([]any, func([]any), error) {
	return arraySectionMut(root, [2]string{"config", "providerConfigRules"}, "providerRules")
}

func modelRulesMut(root map[string]any) ([]any, func([]any), error) {
	return arraySectionMut(root, [2]string{"config", "modelConfigRules"}, "providerModelRules")
}

func manualModelRulesMut(root map[string]any) ([]any, func([]any), error) {
	return arraySectionMut(root, [2]string{"config", "modelConfigRules"}, "manualProviderModelRules")
}

func providerOrderMut(root map[string]any) ([]any, func([]any), error) {
	configValue, exists := root["config"]
	if !exists {
		configValue = map[string]any{}
		root["config"] = configValue
	}
	config, ok := configValue.(map[string]any)
	if !ok {
		return nil, nil, errors.New("provider_config.json 的 config 段必须是对象。")
	}
	orderValue, exists := config["providerOrder"]
	if !exists {
		orderValue = []any{}
		config["providerOrder"] = orderValue
	}
	order, ok := orderValue.([]any)
	if !ok {
		return nil, nil, errors.New("provider_config.json 的 providerOrder 必须是数组。")
	}
	return order, func(updated []any) { config["providerOrder"] = updated }, nil
}

func ruleProviderID(rule map[string]any) (string, bool) {
	return asString(rule["providerId"])
}

func ruleModelID(rule map[string]any) (string, bool) {
	return asString(rule["modelId"])
}

// zcodeOwnedLeaves：reins 拥有的模型规则叶子；apply 只改写这些路径，其余
// （enabled、用户自配的 reasoningLevel.map 等）原样保留。optionSpecs
// .reasoningLevel.map 不写：内置的按 API 类型兜底 map 会把档位名转成各协议
// 的正确请求字段（anthropic-messages 写 thinking.type/output_config.effort，
// chat-completions 写 reasoning_effort，responses 写 reasoning.effort），
// reins 自编 map 只会引入第二份协议知识。
var zcodeOwnedLeaves = [][]string{
	{"properties", "contextWindow"},
	{"properties", "inputFormat", "supportsImage"},
	{"optionSpecs", "maxOutputTokens", "max"},
	{"optionSpecs", "reasoningLevel", "values"},
}

// zcodeReasoningValues：reasoningLevel.values 的取值：模型声明的档位优先；
// 只声明 reasoning 布尔时给保守三档（对齐 codex 目录条目的口径）；显式
// false 只有关闭档；无任何信息时不写该叶子（继承内置兜底）。ZCode 的
// values 要求按语义强度低到高、非空且不重复，故按归一化枚举顺序排序去重。
func zcodeReasoningValues(model *ProviderModelRecord) []ReasoningLevel {
	var levels []ReasoningLevel
	if len(model.ReasoningLevels) > 0 {
		levels = model.ReasoningLevels
	} else if model.Reasoning != nil {
		if *model.Reasoning {
			levels = []ReasoningLevel{ReasoningLow, ReasoningMedium, ReasoningHigh}
		} else {
			levels = []ReasoningLevel{ReasoningOff}
		}
	} else {
		return nil
	}
	return sortDedupReasoningLevels(levels)
}

// mergeJSON：深合并：对象递归，其余后者覆盖前者。用于折叠同一
// (provider, model) 的多条既有规则——ZCode 自己的 upsert 会收敛成一条，
// 这里防御手工编辑出的多条。
func mergeJSON(base map[string]any, next map[string]any) {
	for key, value := range next {
		nextTable, valueIsObject := value.(map[string]any)
		if valueIsObject {
			if baseTable, ok := base[key].(map[string]any); ok {
				mergeJSON(baseTable, nextTable)
				continue
			}
		}
		base[key] = value
	}
}

// mergeJSONValue：单值层面的深合并（base 可能不是对象：非对象一律被
// next 覆盖，等价 Rust merge_json 的 (base, next) => *base = next 分支）。
func mergeJSONValue(base any, next any) any {
	baseTable, baseIsObject := base.(map[string]any)
	nextTable, nextIsObject := next.(map[string]any)
	if baseIsObject && nextIsObject {
		mergeJSON(baseTable, nextTable)
		return base
	}
	return next
}

// removePath：删除一个叶子路径；中间节点缺失或类型不符时空操作。
func removePath(root map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	current := root
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	delete(current, path[len(path)-1])
}

// pruneEmptyObjects：自底向上清掉空对象容器（删空叶子后残留的 {}），避免
// 写出无意义噪声。
func pruneEmptyObjects(root map[string]any) {
	for key, value := range root {
		if child, ok := value.(map[string]any); ok {
			pruneEmptyObjects(child)
			if len(child) == 0 {
				delete(root, key)
			}
		}
	}
}

// copyJSONTree：apply 以既有规则的克隆为底（Rust base.cloned()），修改不
// 反写进按模型折叠的缓存。
func copyJSONTree(value any) any {
	table, ok := value.(map[string]any)
	if !ok {
		return value
	}
	copied := make(map[string]any, len(table))
	for key, item := range table {
		copied[key] = copyJSONTree(item)
	}
	return copied
}

// zcodeModelRule：生成/更新一条模型规则：以既有规则为底（保留非自有叶子），
// 去掉自有叶子后按当前模型元数据重写。无任何叶子可写时返回 nil，不产生
// 空规则。不写 enabled：内置 `.*` 兜底默认启用，显式写反而会覆盖用户在
// ZCode 里对该模型的启停选择。
func zcodeModelRule(registration string, model *ProviderModelRecord, baseConfig any, dialect *ReasoningDialect) map[string]any {
	config, ok := baseConfig.(map[string]any)
	if !ok || config == nil {
		config = map[string]any{}
	}
	for _, path := range zcodeOwnedLeaves {
		removePath(config, path)
	}
	pruneEmptyObjects(config)

	owned := map[string]any{}
	if model.ContextWindow != nil {
		owned["contextWindow"] = *model.ContextWindow
	}
	if model.SupportsImages != nil {
		owned["inputFormat"] = map[string]any{"supportsImage": *model.SupportsImages}
	}
	if len(owned) > 0 {
		mergeJSON(config, map[string]any{"properties": owned})
	}
	if model.MaxOutputTokens != nil {
		mergeJSON(config, map[string]any{
			"optionSpecs": map[string]any{
				"maxOutputTokens": map[string]any{"max": *model.MaxOutputTokens},
			},
		})
	}
	if levels := zcodeReasoningValues(model); levels != nil {
		values := make([]any, 0, len(levels))
		for _, level := range levels {
			values = append(values, dialect.Spell(level))
		}
		mergeJSON(config, map[string]any{
			"optionSpecs": map[string]any{
				"reasoningLevel": map[string]any{"values": values},
			},
		})
	}

	if len(config) == 0 {
		return nil
	}
	return map[string]any{
		"providerId": registration,
		"modelId":    model.ID,
		"config":     config,
	}
}

// collectModelRuleState：应用前收集本 providerId 的模型规则现状：智能规则
// 按模型折叠成底（保留非自有叶子），手动规则的模型名集合（跳过写入的依据）。
func collectModelRuleState(root map[string]any, registration string) (map[string]any, map[string]bool) {
	existingByModel := map[string]any{}
	manualModels := map[string]bool{}
	configTable, ok := asObject(root["config"])
	if !ok {
		return existingByModel, manualModels
	}
	section, ok := asObject(configTable["modelConfigRules"])
	if !ok {
		return existingByModel, manualModels
	}
	if rules, ok := asArray(section["providerModelRules"]); ok {
		for _, ruleAny := range rules {
			rule, ok := ruleAny.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := ruleProviderID(rule); id != registration {
				continue
			}
			modelID, ok := ruleModelID(rule)
			if !ok {
				continue
			}
			configAny, has := rule["config"]
			if !has {
				configAny = map[string]any{}
			}
			existingByModel[modelID] = mergeJSONValue(existingByModel[modelID], configAny)
		}
	}
	if rules, ok := asArray(section["manualProviderModelRules"]); ok {
		for _, ruleAny := range rules {
			rule, ok := ruleAny.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := ruleProviderID(rule); id != registration {
				continue
			}
			if modelID, ok := ruleModelID(rule); ok {
				manualModels[modelID] = true
			}
		}
	}
	return existingByModel, manualModels
}

func (a *ZcodeAdapter) ID() ProviderApp {
	return agents.ProviderZcode
}

func (a *ZcodeAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	path, err := zcodeProviderConfigPath(env)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

func (a *ZcodeAdapter) RestrictedPaths(env *support.Environment) []string {
	return nil
}

func (a *ZcodeAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols:  AllProtocols(),
		Additive:            true,
		RequiredModelFields: nil,
		// 四类元数据都有落点（上下文窗口、最大输出、图像输入、思考档位
		// 经 optionSpecs.reasoningLevel.values 写入）；无工具级默认档位，
		// 档位是每模型值域 + 会话级选择。
		UnwrittenModelFields:     nil,
		SupportedReasoningLevels: []ReasoningLevel{},
		BehaviorNotes: []string{
			"ZCode 的模型与思考档选择是会话级、不落盘：Reins 不写这两项。",
			"官方 OAuth 登录不经 Reins（凭据由 ZCode 自管的 credentials.json 存储，不读写）。",
		},
	}
}

func (a *ZcodeAdapter) ReasoningDialect() *ReasoningDialect {
	return zcodeReasoning
}

func (a *ZcodeAdapter) ProtocolDialect() *ProtocolDialect {
	return zcodeProtocol
}

func (a *ZcodeAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	path, err := zcodeProviderConfigPath(env)
	if err != nil {
		return nil, err
	}
	state := EmptyState(agents.ProviderZcode, []string{path})
	if !fileExists(path) {
		return state, nil
	}
	state.ConfigExists = true
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	configTable, ok := asObject(root["config"])
	if !ok {
		return state, nil
	}
	rulesSection, ok := asObject(configTable["providerConfigRules"])
	if !ok {
		return state, nil
	}
	rules, ok := asArray(rulesSection["providerRules"])
	if !ok {
		return state, nil
	}
	for _, ruleAny := range rules {
		rule, ok := ruleAny.(map[string]any)
		if !ok {
			continue
		}
		// providerId 是 zod 必填；缺失的条目无法寻址，跳过展示。
		key, ok := ruleProviderID(rule)
		if !ok {
			continue
		}
		api, _ := asObject(rule["config"])
		var apiTable map[string]any
		if api != nil {
			apiTable, _ = asObject(api["api"])
		}
		baseURL, hasBaseURL := asString(mapGet(apiTable, "baseUrl"))
		protocol := (*ProviderProtocol)(nil)
		if apiType, ok := asString(mapGet(apiTable, "type")); ok {
			if parsed, ok := zcodeProtocol.Parse(apiType); ok {
				protocol = &parsed
			}
		}
		var entry ProviderAppEntry
		if strings.HasPrefix(key, ReinsPrefix) {
			entry = classifyReinsEntry(key, baseURL, hasBaseURL, protocol, providers)
		} else {
			notes := []string{}
			if name, ok := asString(rule["providerName"]); ok {
				notes = append(notes, fmt.Sprintf("外部 Provider：%s", name))
			}
			if template, ok := asString(rule["templateId"]); ok {
				notes = append(notes, fmt.Sprintf("官方模板：%s", template))
			}
			entry = externalEntry(key, baseURL, hasBaseURL, notes, protocol)
		}
		state.Entries = append(state.Entries, entry)
	}
	return state, nil
}

func (a *ZcodeAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	path, err := zcodeProviderConfigPath(env)
	if err != nil {
		return nil, err
	}
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	// 新建文件时补 schemaVersion；既有值（未来升级）保持不动。
	if _, exists := root["schemaVersion"]; !exists {
		root["schemaVersion"] = int64(1)
	}

	registration := RegistrationKey(provider.ID)

	// 条目级替换语义：同名 reins- 条目覆盖，用户自配条目不动。
	modelIDs := make([]any, 0, len(plan.ModelIDs))
	for _, modelID := range plan.ModelIDs {
		modelIDs = append(modelIDs, modelID)
	}
	entry := map[string]any{
		"providerId":   registration,
		"providerName": provider.Label,
		"config": map[string]any{
			"group":  "standard-personal",
			"access": map[string]any{"type": "api-key", "apiKey": apiKey},
			"api": map[string]any{
				"type":    zcodeProtocol.Spell(provider.Protocol),
				"baseUrl": provider.BaseURL,
			},
			"personalModelIds": modelIDs,
			"modelOrder":       append([]any{}, modelIDs...),
		},
	}
	{
		rules, setRules, err := providerRulesMut(root)
		if err != nil {
			return nil, err
		}
		replaced := false
		for index, ruleAny := range rules {
			rule, ok := ruleAny.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := ruleProviderID(rule); id == registration {
				rules[index] = entry
				replaced = true
				break
			}
		}
		if !replaced {
			rules = append(rules, entry)
		}
		setRules(rules)
	}

	// providerOrder 是选择器里的平台顺序；reins 条目不在列时追加到末尾。
	{
		order, setOrder, err := providerOrderMut(root)
		if err != nil {
			return nil, err
		}
		found := false
		for _, value := range order {
			if text, ok := asString(value); ok && text == registration {
				found = true
				break
			}
		}
		if !found {
			order = append(order, registration)
		}
		setOrder(order)
	}

	// 模型级规则：本 providerId 的既有智能规则按模型折叠成底（保留
	// enabled 等非自有叶子），再按当前元数据重写自有叶子；未选中的模型
	// 整体丢弃。用户已手动配置的模型跳过不写——schema 禁止同一
	// (provider, model) 同时存在智能与手动两类规则，写了会让整份配置
	// 校验失败，用户的显式选择优先。
	{
		existingByModel, manualModels := collectModelRuleState(root, registration)
		rules, setRules, err := modelRulesMut(root)
		if err != nil {
			return nil, err
		}
		kept := make([]any, 0, len(rules))
		for _, ruleAny := range rules {
			rule, ok := ruleAny.(map[string]any)
			if !ok {
				kept = append(kept, ruleAny)
				continue
			}
			if id, _ := ruleProviderID(rule); id == registration {
				continue
			}
			kept = append(kept, ruleAny)
		}
		for _, modelID := range plan.ModelIDs {
			if manualModels[modelID] {
				continue
			}
			model := findModel(provider, modelID)
			if model == nil {
				return nil, fmt.Errorf("模型 %s 不在平台 %s 的目录中。", modelID, provider.ID)
			}
			base := copyJSONTree(existingByModel[modelID])
			if rule := zcodeModelRule(registration, model, base, zcodeReasoning); rule != nil {
				kept = append(kept, rule)
			}
		}
		setRules(kept)
	}

	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func (a *ZcodeAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	_ = provider
	path, err := zcodeProviderConfigPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("ZCode 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}
	registration := RegistrationKey(providerID)

	// 只删内容仍可识别的条目：必须带 api.baseUrl 且 api.type 是协议表
	// 拼写；用户改过的漂移配置不自动删。
	rules, setRules, err := providerRulesMut(root)
	if err != nil {
		return nil, unrecognizedEntryError("ZCode", registration)
	}
	var rule map[string]any
	found := false
	for _, ruleAny := range rules {
		candidate, ok := ruleAny.(map[string]any)
		if !ok {
			continue
		}
		if id, _ := ruleProviderID(candidate); id == registration {
			rule = candidate
			found = true
			break
		}
	}
	if !found {
		return nil, unrecognizedEntryError("ZCode", registration)
	}
	config, _ := asObject(rule["config"])
	api, _ := asObject(mapGet(config, "api"))
	_, hasBaseURL := asString(mapGet(api, "baseUrl"))
	recognizable := hasBaseURL
	if apiType, hasType := asString(mapGet(api, "type")); hasType {
		if _, ok := zcodeProtocol.Parse(apiType); !ok {
			recognizable = false
		}
	} else {
		recognizable = false
	}
	if !recognizable {
		return nil, modifiedEntryError(registration)
	}

	setRules(retainRules(rules, registration, false))
	order, setOrder, err := providerOrderMut(root)
	if err != nil {
		return nil, err
	}
	setOrder(retainStrings(order, registration, false))
	modelRuleList, setModelRules, err := modelRulesMut(root)
	if err != nil {
		return nil, err
	}
	setModelRules(retainRules(modelRuleList, registration, false))
	// 手动规则同样按 providerId 清掉：对齐 ZCode 自己的
	// deletePersonalProvider（deleteExactForProvider 覆盖智能与手动两类），
	// 平台已移除时残留的模型规则没有意义。
	manualRuleList, setManualRules, err := manualModelRulesMut(root)
	if err != nil {
		return nil, err
	}
	setManualRules(retainRules(manualRuleList, registration, false))

	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

func (a *ZcodeAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	path, err := zcodeProviderConfigPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(path) {
		return nil, fmt.Errorf("ZCode 配置文件不存在：%s", support.DisplayPath(path))
	}
	root, err := readJSONObject(path)
	if err != nil {
		return nil, err
	}

	// 外部条目内容任意，跳过内容识别校验。
	rules, setRules, err := providerRulesMut(root)
	if err != nil {
		return nil, err
	}
	before := len(rules)
	kept := retainRules(rules, entryKey, false)
	setRules(kept)
	if len(kept) == before {
		return nil, entryNotFoundError("ZCode", entryKey)
	}
	order, setOrder, err := providerOrderMut(root)
	if err != nil {
		return nil, err
	}
	setOrder(retainStrings(order, entryKey, false))
	modelRuleList, setModelRules, err := modelRulesMut(root)
	if err != nil {
		return nil, err
	}
	setModelRules(retainRules(modelRuleList, entryKey, false))
	// 外部条目的手动规则没有 reins 的稳定归属，不代用户清理。
	content, err := serializeJSON(root)
	if err != nil {
		return nil, err
	}
	return []FileOutput{{Path: path, Content: content}}, nil
}

// ---------------------------------------------------------------------------
// 数组 retain 小工具
// ---------------------------------------------------------------------------

// retainRules：保留 providerId 与 target（不）相等的规则。matchEqual=false
// 时剔除 target 条目（Rust retain 的语义）。
func retainRules(rules []any, target string, matchEqual bool) []any {
	kept := make([]any, 0, len(rules))
	for _, ruleAny := range rules {
		rule, ok := ruleAny.(map[string]any)
		if !ok {
			kept = append(kept, ruleAny)
			continue
		}
		id, _ := ruleProviderID(rule)
		if matchEqual == (id == target) {
			kept = append(kept, ruleAny)
		}
	}
	return kept
}

func retainStrings(values []any, target string, matchEqual bool) []any {
	kept := make([]any, 0, len(values))
	for _, value := range values {
		text, _ := asString(value)
		if matchEqual == (text == target) {
			kept = append(kept, value)
		}
	}
	return kept
}

// mapGet：nil 安全的 map 取值。
func mapGet(table map[string]any, key string) any {
	if table == nil {
		return nil
	}
	return table[key]
}
