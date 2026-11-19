// OpenCode v2：~/.config/opencode/opencode.json(c)。多 Provider 并存：新增
// reins- 条目，不动用户已有 Provider。
//
// opencode.json 与 opencode.jsonc 都会被 OpenCode 加载，顶层键按「后文件
// 整键覆盖先文件」合并，.jsonc 优先（v2.0.18 config/discovery.ts 的
// names 顺序 + config.ts 的 findLast）；OpenCode 自身的配置更新也优先写
// .jsonc。因此读取看两个文件，写入目标选已存在的最高优先级文件，两个
// 都不存在时创建 opencode.json。
//
// 写入一律用 v2 规范 schema（providers 节点 + package + settings，见
// opencode.ai/v2/docs/providers）；v2 运行时同时兼容 v1 遗留 schema
// （provider 节点 + npm + options），用户手工配置或旧版 Reins 写入的
// 条目可能留在 v1 节点，因此读取与删除两个节点都支持，但不向 v1 写入。
// openai_responses：2026-09-25 在 v2.0.16 实测 responses 包静默加载失败而
// 关闭；2026-09-27 在 v2.0.18 复测，openai/responses 本地监听端到端可用，
// 恢复放行；openai-compatible/responses 同版初始化即报
// Cannot find package '@opencode/ai'，不采用。
package providers

import (
	"fmt"
	"os"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

type OpencodeAdapter struct{}

// loadConfigFiles 按低→高优先级加载两个候选文件；缺失文件返回空对象，
// 是否真实存在以 os.Stat 判断。.jsonc 走 JSONC 容忍解析。
func loadConfigFiles(env *support.Environment) ([]string, []map[string]any, error) {
	candidates, err := opencodeCandidatePaths(env)
	if err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(candidates))
	roots := make([]map[string]any, 0, len(candidates))
	for _, path := range candidates {
		var root map[string]any
		if isJSONC(path) {
			root, err = readJSONCObject(path)
		} else {
			root, err = readJSONObject(path)
		}
		if err != nil {
			return nil, nil, err
		}
		paths = append(paths, path)
		roots = append(roots, root)
	}
	return paths, roots, nil
}

func isJSONC(path string) bool {
	extension := pathExtensionBase(path)
	return strings.EqualFold(extension, "jsonc")
}

// pathExtensionBase：路径末段的扩展名（不含点）；对齐 Rust
// Path::extension 的口径。
func pathExtensionBase(path string) string {
	file := path
	if index := strings.LastIndexAny(path, `/\`); index >= 0 {
		file = path[index+1:]
	}
	dot := strings.LastIndexByte(file, '.')
	if dot <= 0 {
		return ""
	}
	return file[dot+1:]
}

func fileDisplayName(path string) string {
	if index := strings.LastIndexAny(path, `/\`); index >= 0 {
		return path[index+1:]
	}
	return path
}

// effectiveFile：顶层键的生效归属：最后定义该键的文件整键生效（findLast
// 语义）。
func effectiveFile(roots []map[string]any, key string) map[string]any {
	for i := len(roots) - 1; i >= 0; i-- {
		if _, has := roots[i][key]; has {
			return roots[i]
		}
	}
	return nil
}

// opencodeSupportedLevels：思考等级：openai 系包写 settings.reasoningEffort，
// anthropic 包的 reasoningEffort 实测不上线，改用显式 thinking 预算，故同
// 档位都能表达。OpenCode v2 对 reasoningEffort 只做字符串校验并按原值透传
// （asOpenAIReasoningEffort 恒等，二进制实证），OpenAI 官方 ReasoningEffort
// 枚举含 max（gpt-5.6/gpt-6-astra 同时支持 xhigh 与 max，两档强度不同），
// 因此 max 直写；anthropic 侧落到与 xhigh 相同的预算。
var opencodeSupportedLevels = []ReasoningLevel{
	ReasoningOff,
	ReasoningMinimal,
	ReasoningLow,
	ReasoningMedium,
	ReasoningHigh,
	ReasoningXhigh,
	ReasoningMax,
}

// thinkingBudgetTokens：anthropic 包的 thinking 必须带预算（实测：
// {type:"enabled"} 无 budgetTokens 时请求不发出，{type:"disabled"} 上线）。
// 官方与 OpenCode 都没有档位到预算的通用映射，这里是 Reins 的固定阶梯；
// 换档位等于换思考预算。
func thinkingBudgetTokens(level ReasoningLevel) int64 {
	switch level {
	case ReasoningOff:
		return 0
	case ReasoningMinimal:
		return 1_024
	case ReasoningLow:
		return 2_048
	case ReasoningMedium:
		return 8_192
	case ReasoningHigh:
		return 16_384
	default: // Xhigh | Max
		return 32_768
	}
}

// reasoningSettings：模型条目里的思考设置；openai 系与 anthropic 包写法
// 不同（见上）。dialect 从 apply 传入：reasoningEffort 的拼写正（这里）反
// （currentReasoningLevel）同源，都走方言表。
func reasoningSettings(protocol ProviderProtocol, level ReasoningLevel, dialect *ReasoningDialect) map[string]any {
	settings := map[string]any{}
	if protocol == ProtocolAnthropicMessages {
		budget := thinkingBudgetTokens(level)
		var thinking map[string]any
		if budget == 0 {
			thinking = jsonThinking("disabled", nil)
		} else {
			thinking = jsonThinking("enabled", &budget)
		}
		settings["thinking"] = thinking
	} else {
		settings["reasoningEffort"] = dialect.Spell(level)
	}
	return settings
}

func jsonThinking(kind string, budgetTokens *int64) map[string]any {
	thinking := map[string]any{"type": kind}
	if budgetTokens != nil {
		thinking["budgetTokens"] = *budgetTokens
	}
	return thinking
}

// levelFromThinkingBudget：反向查表，与 thinkingBudgetTokens 同源，避免
// 两条阶梯分叉。
func levelFromThinkingBudget(budgetTokens int64) (ReasoningLevel, bool) {
	for _, level := range opencodeSupportedLevels {
		if thinkingBudgetTokens(level) == budgetTokens {
			return level, true
		}
	}
	return "", false
}

// currentReasoningLevel：反读默认模型条目里的思考设置（openai 系
// settings.reasoningEffort、anthropic 包 settings.thinking）；默认模型不是
// Reins 条目或没有该字段时留空，不猜等级。
func currentReasoningLevel(root map[string]any, dialect *ReasoningDialect) (ReasoningLevel, bool) {
	model, ok := asString(root["model"])
	if !ok {
		return "", false
	}
	providerKey, modelKey, found := strings.Cut(model, "/")
	if !found {
		return "", false
	}
	providers, ok := asObject(root["providers"])
	if !ok {
		return "", false
	}
	provider, ok := asObject(providers[providerKey])
	if !ok {
		return "", false
	}
	models, ok := asObject(provider["models"])
	if !ok {
		return "", false
	}
	modelEntry, ok := asObject(models[modelKey])
	if !ok {
		return "", false
	}
	settings, ok := asObject(modelEntry["settings"])
	if !ok {
		return "", false
	}
	if effort, ok := asString(settings["reasoningEffort"]); ok {
		return dialect.Parse(effort)
	}
	thinking, ok := asObject(settings["thinking"])
	if !ok {
		return "", false
	}
	if kind, _ := asString(thinking["type"]); kind == "disabled" {
		return ReasoningOff, true
	}
	budget, ok := asInt64(thinking["budgetTokens"])
	if !ok {
		return "", false
	}
	return levelFromThinkingBudget(budget)
}

func entryFromProviderValue(
	key string,
	baseURL string,
	hasBaseURL bool,
	protocol *ProviderProtocol,
	models map[string]any,
	defaultModel string,
	providers map[string]*ResolvedProvider,
) ProviderAppEntry {
	if strings.HasPrefix(key, ReinsPrefix) {
		entry := classifyReinsEntry(key, baseURL, hasBaseURL, protocol, providers)
		if models != nil {
			entry.ModelIDs = sortedStringKeys(models)
		}
		if defaultModel != "" {
			prefix := key + "/"
			if modelID, found := strings.CutPrefix(defaultModel, prefix); found {
				entry.DefaultModelID = stringPtr(modelID)
			}
		}
		return entry
	}
	return externalEntry(key, baseURL, hasBaseURL, []string{"外部 Provider。"}, protocol)
}

// opencodeNodes：OpenCode 配置里的两个承载节点：v2 的 providers 与 v1 的
// provider，读两个、删两个、写只写 v2——这条规则以前靠四处字面量维持一致。
var opencodeNodes = [2]string{"providers", "provider"}

// entryBaseURL：条目的连接信息：v2 在 settings.baseURL，v1 在 options.baseURL。
// 识别只看「任一节有」；is_v2 的分支要区分的只是 package/npm 等其余字段。
func entryBaseURL(entry map[string]any) (string, bool) {
	for _, section := range []string{"settings", "options"} {
		if object, ok := asObject(entry[section]); ok {
			if baseURL, ok := asString(object["baseURL"]); ok {
				return baseURL, true
			}
		}
	}
	return "", false
}

// removeRegistrationFrom：从单个文件根里移除注册键：v2 providers 与 v1
// provider 两个节点都查，并清掉指向该键的顶层 model。调用方负责可识别性
// 校验；返回是否发生了修改，外部条目路径靠它判断「键是否存在」。
func removeRegistrationFrom(root map[string]any, registration string) bool {
	changed := false
	for _, node := range opencodeNodes {
		table, ok := asObject(root[node])
		if !ok {
			continue
		}
		if _, has := table[registration]; has {
			delete(table, registration)
			changed = true
			if len(table) == 0 {
				delete(root, node)
			}
		}
	}
	if model, ok := asString(root["model"]); ok && strings.HasPrefix(model, registration+"/") {
		delete(root, "model")
		changed = true
	}
	return changed
}

func (a *OpencodeAdapter) ID() ProviderApp {
	return agents.ProviderOpencode
}

func (a *OpencodeAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	return opencodeCandidatePaths(env)
}

func (a *OpencodeAdapter) RestrictedPaths(env *support.Environment) []string {
	return nil
}

func (a *OpencodeAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols: []ProviderProtocol{
			ProtocolOpenaiChatCompletions,
			ProtocolOpenaiResponses,
			ProtocolAnthropicMessages,
		},
		Additive:            true,
		RequiredModelFields: nil,
		// 四类元数据都有落点：limit.context/output、capabilities.input、
		// settings 的思考等级。
		UnwrittenModelFields:     nil,
		SupportedReasoningLevels: opencodeSupportedLevels,
		BehaviorNotes: []string{
			"OpenCode 里手动选过的思考档位（变体）会覆盖这里写入的默认档并长期生效；要让默认档生效，请在 OpenCode 的 Select variant 里选回 Default。",
			"档位变体列表由 OpenCode 按内置枚举生成（none 至 xhigh），不含 max：max 作为模型默认档写入后照常生效，只是选择器里看不到。",
			"OpenCode 同时加载 opencode.json 与 opencode.jsonc（后者整键优先）；Reins 写入两者中已存在的最高优先级文件。",
		},
	}
}

// 档位方言只覆盖 openai 系包的 settings.reasoningEffort 字符串拼写；
// anthropic 包的 thinking 是结构化预算阶梯（reasoningSettings），不经
// 此表，弹窗的「实际写入值」提示对 anthropic 协议不适用（已知限制，
// 见 2026-10-11 方言表决策文档）。
func (a *OpencodeAdapter) ReasoningDialect() *ReasoningDialect {
	return identityReasoning
}

func (a *OpencodeAdapter) ProtocolDialect() *ProtocolDialect {
	return opencodeProtocol
}

func (a *OpencodeAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	paths, roots, err := loadConfigFiles(env)
	if err != nil {
		return nil, err
	}
	candidates, err := opencodeCandidatePaths(env)
	if err != nil {
		return nil, err
	}
	existing := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, path)
		}
	}
	statePaths := existing
	if len(statePaths) == 0 {
		// 两个候选都不存在时展示默认写入目标。
		statePaths = []string{candidates[0]}
	}
	state := EmptyState(agents.ProviderOpencode, statePaths)
	anyExists := false
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			anyExists = true
			break
		}
	}
	if !anyExists {
		return state, nil
	}
	state.ConfigExists = true

	// model 与 providers 各自取生效文件的值组成合并视图，供默认档位
	// 反读（currentReasoningLevel 只读这两个键）。
	merged := map[string]any{}
	for _, key := range []string{"model", "providers"} {
		if root := effectiveFile(roots, key); root != nil {
			merged[key] = root[key]
		}
	}
	// 应用弹窗据此预选；反读值只用于回显，不参与漂移判定。
	if level, ok := currentReasoningLevel(merged, identityReasoning); ok {
		levelCopy := level
		state.DefaultReasoningLevel = &levelCopy
	}
	defaultModel, _ := asString(merged["model"])

	for fileIndex, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		root := roots[fileIndex]
		fileName := fileDisplayName(path)
		// 节点序即版本序：providers=v2、provider=v1。
		for nodeIndex, node := range opencodeNodes {
			isV2 := nodeIndex == 0
			table, ok := asObject(root[node])
			if !ok {
				continue
			}
			// 该文件在此节点被更高优先级文件覆盖时，条目仍列出但注明
			// 不生效，避免用户在低优先级文件里改了个寂寞。
			shadowedBy := ""
			for other := len(paths) - 1; other > fileIndex; other-- {
				if _, has := roots[other][node]; has {
					// 缺失文件加载为空根，不会进入本分支；能命中的都是
					// 真实存在的更高优先级文件。
					shadowedBy = fileDisplayName(paths[other])
					break
				}
			}
			for _, key := range sortedStringKeys(table) {
				value, ok := asObject(table[key])
				if !ok {
					continue
				}
				baseURL, hasBaseURL := entryBaseURL(value)
				packageKey := "package"
				if !isV2 {
					packageKey = "npm"
				}
				protocol := (*ProviderProtocol)(nil)
				if pkg, ok := asString(value[packageKey]); ok {
					if parsed, ok := opencodeProtocol.Parse(pkg); ok {
						parsedCopy := parsed
						protocol = &parsedCopy
					}
				}
				models, _ := asObject(value["models"])
				entry := entryFromProviderValue(key, baseURL, hasBaseURL, protocol, models, defaultModel, providers)
				if shadowedBy != "" {
					entry.Notes = append(entry.Notes,
						fmt.Sprintf("%s 的 %s 键被 %s 覆盖，不生效。", fileName, node, shadowedBy))
				}
				state.Entries = append(state.Entries, entry)
			}
		}
	}

	return state, nil
}

func (a *OpencodeAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	paths, roots, err := loadConfigFiles(env)
	if err != nil {
		return nil, err
	}
	registration := RegistrationKey(provider.ID)
	// 写入目标：已存在的最高优先级文件（与 OpenCode 自身更新器的选择
	// 一致）；都不存在时创建 opencode.json。
	targetIndex := 0
	for index := len(paths) - 1; index >= 0; index-- {
		if _, err := os.Stat(paths[index]); err == nil {
			targetIndex = index
			break
		}
	}

	outputs := make([]FileOutput, 0, 2)
	{
		targetPath := paths[targetIndex]
		root := roots[targetIndex]
		// 该平台上一次写入的模型条目：本次没选思考等级时沿用其中的
		// settings，重新应用不会把上次选的等级写没。
		var previousModels map[string]any
		if providersTable, ok := asObject(root["providers"]); ok {
			if entry, ok := asObject(providersTable[registration]); ok {
				previousModels, _ = asObject(entry["models"])
			}
		}

		models := map[string]any{}
		for _, modelID := range plan.ModelIDs {
			model := findModel(provider, modelID)
			label := modelID
			if model != nil {
				label = model.Label
			}
			modelEntry := map[string]any{"name": label}
			if model != nil {
				// limit 的 context/output 在官方 schema 里成对必填；只有一项时
				// 整段不写，避免半截限制参与压缩与输出截断判断。
				if model.ContextWindow != nil && model.MaxOutputTokens != nil {
					modelEntry["limit"] = map[string]any{
						"context": *model.ContextWindow,
						"output":  *model.MaxOutputTokens,
					}
				}
				// capabilities 同时表达工具支持与输入模态；官方对目录外模型
				// 默认假设 text+image 输入，有元数据就显式声明。
				if model.SupportsImages != nil {
					input := []any{"text"}
					if *model.SupportsImages {
						input = append(input, "image")
					}
					modelEntry["capabilities"] = map[string]any{
						"tools":  true,
						"input":  input,
						"output": []any{"text"},
					}
				}
				if plan.DefaultReasoningLevel != nil {
					modelEntry["settings"] = reasoningSettings(provider.Protocol, *plan.DefaultReasoningLevel, identityReasoning)
				} else if previousModels != nil {
					if previousEntry, ok := asObject(previousModels[modelID]); ok {
						if settings, has := previousEntry["settings"]; has {
							modelEntry["settings"] = settings
						}
					}
				}
			}
			models[modelID] = modelEntry
		}

		settings := map[string]any{
			"baseURL": provider.BaseURL,
			"apiKey":  apiKey,
		}
		providerEntry := map[string]any{
			"name":     provider.Label,
			"package":  opencodeProtocol.Spell(provider.Protocol),
			"settings": settings,
			"models":   models,
		}

		providersAny, exists := root["providers"]
		if !exists {
			providersTable := map[string]any{}
			root["providers"] = providersTable
			providersAny = providersTable
		}
		providersTable, ok := asObject(providersAny)
		if !ok {
			return nil, fmt.Errorf("providers 段不是对象：%s", targetPath)
		}
		providersTable[registration] = providerEntry
		root["model"] = fmt.Sprintf("%s/%s", registration, plan.DefaultModelID)
		content, err := serializeJSON(root)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, FileOutput{Path: targetPath, Content: content})
	}

	// 写入目标之外还留有本平台旧注册键的文件：清掉旧键，避免同一个
	// reins- 条目散在两个文件里误导反读。
	for index, path := range paths {
		if index == targetIndex {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		root := roots[index]
		hasRegistration := false
		for _, node := range opencodeNodes {
			if table, ok := asObject(root[node]); ok {
				if _, has := table[registration]; has {
					hasRegistration = true
					break
				}
			}
		}
		if !hasRegistration {
			continue
		}
		removeRegistrationFrom(root, registration)
		content, err := serializeJSON(root)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, FileOutput{Path: path, Content: content})
	}

	return outputs, nil
}

func (a *OpencodeAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	_ = provider
	registration := RegistrationKey(providerID)
	paths, roots, err := loadConfigFiles(env)
	if err != nil {
		return nil, err
	}
	outputs := make([]FileOutput, 0, 1)
	found := false
	for index, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		root := roots[index]
		containsRegistration := false
		for _, node := range opencodeNodes {
			if table, ok := asObject(root[node]); ok {
				if _, has := table[registration]; has {
					containsRegistration = true
					break
				}
			}
		}
		if !containsRegistration {
			continue
		}
		// 只删内容仍可识别的条目：baseURL 必须是字符串；两个节点里的
		// 同名条目都要通过校验后再动文件。
		for _, node := range opencodeNodes {
			if table, ok := asObject(root[node]); ok {
				if entry, has := table[registration]; has {
					if entryObject, ok := asObject(entry); !ok {
						return nil, modifiedEntryError(registration)
					} else if _, hasBaseURL := entryBaseURL(entryObject); !hasBaseURL {
						return nil, modifiedEntryError(registration)
					}
				}
			}
		}
		found = true
		// 默认 Provider 仍指向该平台时随移除清掉；model 是顶层单值键，
		// 留空后由用户在 OpenCode 内重选，不再要求先切换。
		removeRegistrationFrom(root, registration)
		content, err := serializeJSON(root)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, FileOutput{Path: path, Content: content})
	}
	if !found {
		return nil, unrecognizedEntryError("OpenCode", registration)
	}
	return outputs, nil
}

func (a *OpencodeAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	paths, roots, err := loadConfigFiles(env)
	if err != nil {
		return nil, err
	}
	outputs := make([]FileOutput, 0, 1)
	removed := false
	for index, path := range paths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		root := roots[index]
		// 与 reins- 条目移除同一助手：默认模型指向被删条目时随删除清空,
		// 两个节点都删。外部条目可能同时出现在两个文件里（低优先级那份
		// 本就不生效），一并删除。
		if removeRegistrationFrom(root, entryKey) {
			content, err := serializeJSON(root)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, FileOutput{Path: path, Content: content})
			removed = true
		}
	}
	if !removed {
		return nil, entryNotFoundError("OpenCode", entryKey)
	}
	return outputs, nil
}
