// dsh（DeepSeek Harness）：LLM 路由写 Cordis patch 层。
// 桌面版对 $DSH_HOME/settings.yaml 是「一次性导入」语义（启动时改名
// .imported，被运行组合拒绝的节只留日志不落盘），不能作为常驻写入目标；
// 路由走与 dsh 自身设置一致的持久化形态：全局 cordis.patch.yml 里
// id=llm-pi-ai 的行覆盖提供 providers 路由（config 整块替换），desktop
// profile 的 cordis.patch.yml 里 id=agent-default-model 的行提供默认模型
// 与默认思考档。密钥写入 dsh 托管的 .credentials.yaml（名字→值严格映射），
// 路由经 apiKeyEnv 引用——与 dsh Models 页自己的写入方式一致；Reins 只
// 行级 patch 自己的 ref，其余条目原样保留。
//
// YAML 一律经 yaml.Node 操作：serde_yaml 的 Mapping 保持插入序（用户条目
// 的键序、!!js 标签都是文件语义），Go 的 map 会按字典序重排并丢标签。
package providers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
)

type DshAdapter struct{}

// ---------------------------------------------------------------------------
// 路径
// ---------------------------------------------------------------------------

func dshHome(env *support.Environment) (string, error) {
	return env.ResolveRequired(agents.RootDshHome.Redirect)
}

func dshPatchPath(env *support.Environment) (string, error) {
	home, err := dshHome(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cordis.patch.yml"), nil
}

// dshProfilePatchPath：默认模型行写在桌面版实际使用的 profile patch 里——
// profile 层晚于全局层生效，之前 settings 导入落下的 agent-default-model 行
// 就位于此，只有同层覆盖才能纠正它。
func dshProfilePatchPath(env *support.Environment) (string, error) {
	home, err := dshHome(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "profiles", "desktop", "cordis.patch.yml"), nil
}

func dshCredentialsPath(env *support.Environment) (string, error) {
	home, err := dshHome(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".credentials.yaml"), nil
}

// ---------------------------------------------------------------------------
// patch 行操作
// ---------------------------------------------------------------------------

// findRow：行覆盖型 op 的形态是顶层 {id, name, config}；insert 型 op 的
// 条目嵌在 insert 数组里（顶层无 id 键），天然不会与行覆盖混淆。
func findRow(ops []*yaml.Node, rowID string) *yaml.Node {
	for _, op := range ops {
		if id, ok := support.NodeAsStr(support.NodeGet(op, "id")); ok && id == rowID {
			return op
		}
	}
	return nil
}

// rowConfig：行的 config 映射；行缺失或形状不符返回空映射（调用方在其上
// 的取值全部安全落空）。
func rowConfig(row *yaml.Node) *yaml.Node {
	if config := support.NodeGet(row, "config"); config != nil && config.Kind == yaml.MappingNode {
		return config
	}
	return support.YMap()
}

// rowIntoOps：按 id 覆盖或追加一行（整块替换语义）。
func rowIntoOps(ops []*yaml.Node, rowID string, name string, config *yaml.Node) []*yaml.Node {
	row := support.YMap(
		[2]*yaml.Node{support.YStr("id"), support.YStr(rowID)},
		[2]*yaml.Node{support.YStr("name"), support.YStr(name)},
		[2]*yaml.Node{support.YStr("config"), config},
	)
	for index, op := range ops {
		if id, ok := support.NodeAsStr(support.NodeGet(op, "id")); ok && id == rowID {
			ops[index] = row
			return ops
		}
	}
	return append(ops, row)
}

// removeRow：按 id 删行；返回是否删除了内容。
func removeRow(ops []*yaml.Node, rowID string) ([]*yaml.Node, bool) {
	kept := make([]*yaml.Node, 0, len(ops))
	removed := false
	for _, op := range ops {
		if id, ok := support.NodeAsStr(support.NodeGet(op, "id")); ok && id == rowID {
			removed = true
			continue
		}
		kept = append(kept, op)
	}
	return kept, removed
}

// llmProviders：llm 行 config.providers 映射；行缺失时返回空映射。
func llmProviders(row *yaml.Node) *yaml.Node {
	config := support.NodeGet(row, "config")
	providers := support.NodeGet(config, "providers")
	if providers != nil && providers.Kind == yaml.MappingNode {
		return providers
	}
	return support.YMap()
}

// ---------------------------------------------------------------------------
// 凭据引用名
// ---------------------------------------------------------------------------

// apiKeyRef：路由的凭据引用名：.credentials.yaml 的键与路由 apiKeyEnv 用
// 同一名字。非 ASCII 字母数字一律折叠为下划线（等价 Rust 的
// is_ascii_alphanumeric 判定）。
func apiKeyRef(providerID string) string {
	var name strings.Builder
	name.WriteString("REINS_")
	for _, ch := range providerID {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			name.WriteRune(unicode.ToUpper(ch))
		} else {
			name.WriteRune('_')
		}
	}
	result := name.String()
	for strings.HasSuffix(result, "_") {
		result = strings.TrimSuffix(result, "_")
	}
	return result
}

// ---------------------------------------------------------------------------
// 凭据文档 patch（version:1 布局感知）
// ---------------------------------------------------------------------------

// PatchCredentialRef：.credentials.yaml 是 version:1 分层文档：顶层只允许
// version/refs/records，顶层出现其它键会让 dsh 的凭据服务启动失败并级联
// 拖垮整个应用（实测）。这里做结构化往返：refs 段下只增删自己的 ref，
// version/records 与其它键原样保留。代价是丢掉文档内注释（dsh 明确该文件
// 只装凭据，可接受）；解析失败时大声报错，绝不静默重写。apiKey 为 nil 表示
// 移除该 ref。
func PatchCredentialRef(contents string, name string, apiKey *string) (string, error) {
	serialize := func(root *yaml.Node) (string, error) {
		support.MarkEmptyMappings(root)
		serialized, err := support.MarshalYAMLNode(root)
		if err != nil {
			return "", errors.New("dsh 凭据文档序列化失败")
		}
		return serialized, nil
	}
	// 空文档从零建立 version:1 布局。
	if strings.TrimSpace(contents) == "" {
		refs := support.YMap()
		if apiKey != nil {
			support.NodeSet(refs, name, support.YStr(*apiKey))
		}
		root := support.YMap(
			[2]*yaml.Node{support.YStr("version"), support.YInt(1)},
			[2]*yaml.Node{support.YStr("refs"), refs},
		)
		return serialize(root)
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(contents), &document); err != nil {
		return "", errors.New("解析 dsh 凭据文档失败")
	}
	if len(document.Content) == 0 {
		return "", errors.New("解析 dsh 凭据文档失败")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", errors.New("dsh 凭据文档必须是映射。")
	}

	// 平铺旧布局迁移（与 dsh renderFlatLayoutMigration 同语义）：无 version
	// 键时把顶层条目全部嵌进 refs:，补 version: 1；非字符串/空值按 dsh 的
	// loud 拒绝语义报错。
	if support.NodeGet(root, "version") == nil && len(root.Content) > 0 {
		refs := support.YMap()
		for i := 0; i+1 < len(root.Content); i += 2 {
			keyNode := root.Content[i]
			valueNode := root.Content[i+1]
			text, ok := support.NodeAsStr(valueNode)
			if !ok {
				return "", errors.New("凭据文档是平铺旧布局且含非字符串条目，请手动迁移后再应用。")
			}
			if text == "" {
				return "", errors.New("凭据文档是平铺旧布局且含空值条目，请手动删除后再应用。")
			}
			refs.Content = append(refs.Content, keyNode, support.YStr(text))
		}
		root.Content = nil
		support.NodeSet(root, "version", support.YInt(1))
		support.NodeSet(root, "refs", refs)
	}
	if version, ok := support.NodeAsInt64(support.NodeGet(root, "version")); !ok || version != 1 {
		return "", errors.New("dsh 凭据文档的 version 不是 1，无法自动写入。")
	}

	if apiKey != nil {
		refs := support.NodeGet(root, "refs")
		if refs == nil {
			refs = support.YMap()
			support.NodeSet(root, "refs", refs)
		}
		if refs.Kind != yaml.MappingNode {
			return "", errors.New("dsh 凭据文档的 refs 段必须是映射。")
		}
		support.NodeSet(refs, name, support.YStr(*apiKey))
	} else {
		if refs := support.NodeGet(root, "refs"); refs != nil && refs.Kind == yaml.MappingNode {
			support.NodeRemove(refs, name)
		}
	}

	// refs 清空后补空映射，避免序列化成 null（dsh 要求 refs 是映射）。
	if refs := support.NodeGet(root, "refs"); refs != nil && refs.Kind == yaml.MappingNode && len(refs.Content) == 0 {
		support.NodeSet(root, "refs", support.YMap())
	}

	return serialize(root)
}

// ---------------------------------------------------------------------------
// 路由条目组装
// ---------------------------------------------------------------------------

func routeEntry(provider *ResolvedProvider, plan *ApplyProviderInput) *yaml.Node {
	models := make([]*yaml.Node, 0, len(plan.ModelIDs))
	for _, modelID := range plan.ModelIDs {
		model := findModel(provider, modelID)
		if model == nil {
			// validate_plan 已保证；防御异常输入。
			continue
		}
		modelEntry := support.YMap([2]*yaml.Node{support.YStr("id"), support.YStr(model.ID)})
		if model.ContextWindow != nil {
			support.NodeSet(modelEntry, "contextWindow", support.YInt(*model.ContextWindow))
		}
		if model.MaxOutputTokens != nil {
			support.NodeSet(modelEntry, "maxTokens", support.YInt(*model.MaxOutputTokens))
		}
		// 请求模态：显式声明 image 才让手工声明的视觉模型可用（官方
		// PiAiModelProfile.input 语义），false/未填时回落 [text]。
		if model.SupportsImages != nil {
			input := []*yaml.Node{support.YStr("text")}
			if *model.SupportsImages {
				input = append(input, support.YStr("image"))
			}
			support.NodeSet(modelEntry, "input", support.YSeq(input...))
		}
		// 推理元数据：显式非推理用 false 剥离目录模型自带的推理能力；
		// 有等级集则只声明勾选的档位——pi-ai 规定字典键缺席即不提供该档，
		// 未勾选档写成 null 会因"没有线上值"被拒绝（仅 off 允许空值）。
		if model.Reasoning != nil && !*model.Reasoning {
			support.NodeSet(modelEntry, "reasoningEfforts", support.YBool(false))
		} else if len(model.ReasoningLevels) > 0 {
			efforts := support.YMap()
			for _, level := range model.ReasoningLevels {
				var wire *yaml.Node
				if level == ReasoningOff {
					wire = support.YNull()
				} else {
					wire = support.YStr(piAIReasoning.Spell(level))
				}
				support.NodeSet(efforts, piAIReasoning.Spell(level), wire)
			}
			support.NodeSet(modelEntry, "reasoningEfforts", efforts)
		}
		models = append(models, modelEntry)
	}

	route := support.YMap(
		[2]*yaml.Node{support.YStr("apiKeyEnv"), support.YStr(apiKeyRef(provider.ID))},
		[2]*yaml.Node{support.YStr("baseURL"), support.YStr(provider.BaseURL)},
		[2]*yaml.Node{support.YStr("api"), support.YStr(piAIProtocol.Spell(provider.Protocol))},
		[2]*yaml.Node{support.YStr("models"), support.YSeq(models...)},
	)
	// 路由级默认思考档；会话实际采用的默认档来自 agent-default-model 行。
	if plan.DefaultReasoningLevel != nil {
		support.NodeSet(route, "reasoning", support.YStr(piAIReasoning.Spell(*plan.DefaultReasoningLevel)))
	}
	return route
}

func defaultModelRow(plan *ApplyProviderInput, registration string) *yaml.Node {
	config := support.YMap(
		[2]*yaml.Node{support.YStr("provider"), support.YStr(registration)},
		[2]*yaml.Node{support.YStr("model"), support.YStr(plan.DefaultModelID)},
	)
	if plan.DefaultReasoningLevel != nil {
		// dsh 的默认档插件值是 high；不写该键时回落它，Reins 的选择必须
		// 显式落在这里才能纠正 settings 导入留下的旧行。
		support.NodeSet(config, "reasoningEffort", support.YStr(piAIReasoning.Spell(*plan.DefaultReasoningLevel)))
	}
	return config
}

// ---------------------------------------------------------------------------
// AppAdapter
// ---------------------------------------------------------------------------

func (a *DshAdapter) ID() ProviderApp {
	return agents.ProviderDsh
}

// ConfigPaths：providers 域只管理 profile 层 patch 与凭据；全局 cordis
// patch 属于 workspace 域 MCP 分发，不在此列（inspect 反读时会带上它）。
func (a *DshAdapter) ConfigPaths(env *support.Environment) ([]string, error) {
	profile, err := dshProfilePatchPath(env)
	if err != nil {
		return nil, err
	}
	credentials, err := dshCredentialsPath(env)
	if err != nil {
		return nil, err
	}
	return []string{profile, credentials}, nil
}

// RestrictedPaths：dsh 启动时会拒绝加载非 owner-only(0600) 的凭据文件,
// 整个 app 起不来;凭据写入必须强制该权限(路径解析失败时返回空,由后续
// 写入路径自己报错)。
func (a *DshAdapter) RestrictedPaths(env *support.Environment) []string {
	path, err := dshCredentialsPath(env)
	if err != nil {
		return nil
	}
	return []string{path}
}

func (a *DshAdapter) Capability() AppCapability {
	return AppCapability{
		SupportedProtocols:  AllProtocols(),
		Additive:            true,
		RequiredModelFields: nil,
		// settings.yaml 的模型条目（PiAiModelProfile）有 contextWindow/
		// maxTokens/input/reasoningEfforts 落点（见官方 config-catalog）；
		// 唯一没有独立字段的是"推理能力"布尔，只能经思考等级字典或
		// 显式 false 间接表达（见 routeEntry），真值+空等级组合无法表达。
		UnwrittenModelFields:     []ModelField{FieldReasoning},
		SupportedReasoningLevels: AllReasoningLevels(),
		BehaviorNotes: []string{
			"桌面版对 settings.yaml 是一次性导入语义（启动时改名 .imported），Reins 不写它：路由与默认档以行级 patch 写入全局与 desktop profile 的 cordis.patch.yml。",
			"密钥写入 dsh 托管的 .credentials.yaml（严格名字映射），路由经 apiKeyEnv 引用。",
		},
	}
}

func (a *DshAdapter) ReasoningDialect() *ReasoningDialect {
	return piAIReasoning
}

func (a *DshAdapter) ProtocolDialect() *ProtocolDialect {
	return piAIProtocol
}

func (a *DshAdapter) Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error) {
	globalPatch, err := dshPatchPath(env)
	if err != nil {
		return nil, err
	}
	profilePatch, err := dshProfilePatchPath(env)
	if err != nil {
		return nil, err
	}
	credentials, err := dshCredentialsPath(env)
	if err != nil {
		return nil, err
	}
	state := EmptyState(agents.ProviderDsh, []string{globalPatch, profilePatch, credentials})
	if !fileExists(globalPatch) && !fileExists(profilePatch) && !fileExists(credentials) {
		return state, nil
	}
	state.ConfigExists = true

	profileOps, err := support.ReadOps(profilePatch)
	if err != nil {
		return nil, fmt.Errorf("读取失败：%s", support.DisplayPath(profilePatch))
	}
	// 路由与默认模型都在 profile patch 层（与 dsh Models 页同层）。
	llmRow := findRow(profileOps, "llm-pi-ai")
	// 默认模型行在 profile patch 层（晚于全局层生效），不能从全局找。
	defaultRow := findRow(profileOps, "agent-default-model")
	defaultProvider, defaultModel := "", ""
	if defaultRow != nil {
		config := rowConfig(defaultRow)
		if value, ok := support.NodeAsStr(support.NodeGet(config, "provider")); ok {
			defaultProvider = value
		}
		if value, ok := support.NodeAsStr(support.NodeGet(config, "model")); ok {
			defaultModel = value
		}
		if value, ok := support.NodeAsStr(support.NodeGet(config, "reasoningEffort")); ok {
			if level, ok := piAIReasoning.Parse(value); ok {
				levelCopy := level
				state.DefaultReasoningLevel = &levelCopy
			}
		}
	}

	providersTable := llmProviders(llmRow)
	for i := 0; i+1 < len(providersTable.Content); i += 2 {
		keyNode := providersTable.Content[i]
		value := providersTable.Content[i+1]
		entryKey, ok := support.NodeAsStr(keyNode)
		if !ok {
			continue
		}
		baseURL, hasBaseURL := support.NodeAsStr(support.NodeGet(value, "baseURL"))
		protocol := (*ProviderProtocol)(nil)
		if api, ok := support.NodeAsStr(support.NodeGet(value, "api")); ok {
			if parsed, ok := piAIProtocol.Parse(api); ok {
				protocol = &parsed
			}
		}
		var entry ProviderAppEntry
		if strings.HasPrefix(entryKey, ReinsPrefix) {
			entry = classifyReinsEntry(entryKey, baseURL, hasBaseURL, protocol, providers)
		} else {
			entry = externalEntry(entryKey, baseURL, hasBaseURL, []string{"外部 Provider。"}, protocol)
		}
		if modelsSeq := support.NodeGet(value, "models"); modelsSeq != nil && modelsSeq.Kind == yaml.SequenceNode {
			modelIDs := make([]string, 0, len(modelsSeq.Content))
			for _, modelNode := range modelsSeq.Content {
				if id, ok := support.NodeAsStr(support.NodeGet(modelNode, "id")); ok {
					modelIDs = append(modelIDs, id)
				}
			}
			entry.ModelIDs = modelIDs
		}
		if defaultProvider == entryKey {
			entry.DefaultModelID = stringPtrOrNil(defaultModel)
		}
		state.Entries = append(state.Entries, entry)
	}

	return state, nil
}

func (a *DshAdapter) Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error) {
	if err := EnsureProtocolSupported(a, provider); err != nil {
		return nil, err
	}
	registration := RegistrationKey(provider.ID)

	// LLM 路由与默认模型都写 desktop profile patch 层——与 dsh Models 页
	// 的读写同一层，设置页回显才与生效配置一致；全局层只保留 MCP 条目。
	//（patch 对同 id 行是整块替换；bundle 层的 llm-pi-ai 行会被本层覆盖。）
	profilePatch, err := dshProfilePatchPath(env)
	if err != nil {
		return nil, err
	}
	profileOps, err := support.ReadOps(profilePatch)
	if err != nil {
		return nil, err
	}

	// 迁移清理：早前实验/旧版本写在全局层的 llm-pi-ai 行（仅含 reins-*
	// 路由或为空）由本层覆盖后已失效，这里顺手删掉；含外部路由的行保留。
	globalPatch, err := dshPatchPath(env)
	if err != nil {
		return nil, err
	}
	if fileExists(globalPatch) {
		globalOps, err := support.ReadOps(globalPatch)
		if err != nil {
			return nil, err
		}
		if row := findRow(globalOps, "llm-pi-ai"); row != nil {
			providersTable := llmProviders(row)
			onlyReins := true
			for i := 0; i+1 < len(providersTable.Content); i += 2 {
				key, ok := support.NodeAsStr(providersTable.Content[i])
				if !ok || !strings.HasPrefix(key, ReinsPrefix) {
					onlyReins = false
					break
				}
			}
			if len(providersTable.Content) == 0 || onlyReins {
				globalOps, _ = removeRow(globalOps, "llm-pi-ai")
				if err := support.WriteOps(globalPatch, globalOps); err != nil {
					return nil, err
				}
			}
		}
	}

	providersTable := llmProviders(findRow(profileOps, "llm-pi-ai"))
	support.NodeSet(providersTable, registration, routeEntry(provider, plan))
	config := support.YMap([2]*yaml.Node{support.YStr("providers"), providersTable})
	profileOps = rowIntoOps(profileOps, "llm-pi-ai", "@deepseek-ai/dsh-llm-pi-ai", config)
	profileOps = rowIntoOps(profileOps, "agent-default-model",
		"@deepseek-ai/dsh-agent-default-model", defaultModelRow(plan, registration))
	profileContent, err := support.SerializeOps(profileOps)
	if err != nil {
		return nil, err
	}

	// 凭据：refs 段级 patch 自己的 ref。读取失败按空文档处理（Rust
	// read_to_string().unwrap_or_default()），由 patch 语义决定落盘形态。
	credentials, err := dshCredentialsPath(env)
	if err != nil {
		return nil, err
	}
	existingCredentials, _ := os.ReadFile(credentials)
	apiKeyCopy := apiKey
	credentialsContent, err := PatchCredentialRef(string(existingCredentials), apiKeyRef(provider.ID), &apiKeyCopy)
	if err != nil {
		return nil, err
	}

	return []FileOutput{
		{Path: profilePatch, Content: profileContent},
		{Path: credentials, Content: credentialsContent},
	}, nil
}

func (a *DshAdapter) Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error) {
	_ = provider
	registration := RegistrationKey(providerID)

	profilePatch, err := dshProfilePatchPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(profilePatch) {
		return nil, fmt.Errorf("dsh 配置文件不存在：%s", support.DisplayPath(profilePatch))
	}
	profileOps, err := support.ReadOps(profilePatch)
	if err != nil {
		return nil, err
	}
	providersTable := llmProviders(findRow(profileOps, "llm-pi-ai"))
	entry := support.NodeGet(providersTable, registration)
	if entry == nil {
		return nil, unrecognizedEntryError("dsh", registration)
	}
	// 只删内容仍可识别的条目：必须带 baseURL（apply 必写，与 pi 的
	// pi-ai 路由同型）；用户改过的漂移配置不自动删。
	if _, hasBaseURL := support.NodeAsStr(support.NodeGet(entry, "baseURL")); !hasBaseURL {
		return nil, modifiedEntryError(registration)
	}
	support.NodeRemove(providersTable, registration)

	files := make([]FileOutput, 0, 2)
	// 凭据随路由一起清掉（refs 段删自己的 ref）；与 profile 是不同文件,
	// 先推后推无碍,profile 最终态只在最后序列化一次。
	credentials, err := dshCredentialsPath(env)
	if err != nil {
		return nil, err
	}
	if fileExists(credentials) {
		existing, _ := os.ReadFile(credentials)
		updated, err := PatchCredentialRef(string(existing), apiKeyRef(providerID), nil)
		if err != nil {
			return nil, err
		}
		if updated != string(existing) {
			files = append(files, FileOutput{Path: credentials, Content: updated})
		}
	}

	profileOps = writeProvidersRoute(profileOps, providersTable)
	profileOps = dropDefaultModelRow(profileOps, registration)
	content, err := support.SerializeOps(profileOps)
	if err != nil {
		return nil, err
	}
	files = append(files, FileOutput{Path: profilePatch, Content: content})

	return files, nil
}

func (a *DshAdapter) RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error) {
	profilePatch, err := dshProfilePatchPath(env)
	if err != nil {
		return nil, err
	}
	if !fileExists(profilePatch) {
		return nil, fmt.Errorf("dsh 配置文件不存在：%s", support.DisplayPath(profilePatch))
	}
	profileOps, err := support.ReadOps(profilePatch)
	if err != nil {
		return nil, err
	}
	providersTable := llmProviders(findRow(profileOps, "llm-pi-ai"))
	if !support.NodeRemove(providersTable, entryKey) {
		return nil, entryNotFoundError("dsh", entryKey)
	}

	profileOps = writeProvidersRoute(profileOps, providersTable)
	profileOps = dropDefaultModelRow(profileOps, entryKey)
	content, err := support.SerializeOps(profileOps)
	if err != nil {
		return nil, err
	}
	files := []FileOutput{{Path: profilePatch, Content: content}}
	return files, nil
}

// writeProvidersRoute：删除路径的共享机械：llm-pi-ai 行的 providers 表重写
// （表空删整行，恢复组合里的休眠挂载）。reins- 条目与外部条目两条删除路径
// 共用；profile 的最终态由调用方在全部修改完成后只序列化一次。
func writeProvidersRoute(ops []*yaml.Node, providersTable *yaml.Node) []*yaml.Node {
	if len(providersTable.Content) == 0 {
		ops, _ = removeRow(ops, "llm-pi-ai")
		return ops
	}
	config := support.YMap([2]*yaml.Node{support.YStr("providers"), providersTable})
	return rowIntoOps(ops, "llm-pi-ai", "@deepseek-ai/dsh-llm-pi-ai", config)
}

// dropDefaultModelRow：agent-default-model 行的指向清理（回落组合默认）。
func dropDefaultModelRow(ops []*yaml.Node, route string) []*yaml.Node {
	row := findRow(ops, "agent-default-model")
	if row != nil {
		if provider, ok := support.NodeAsStr(support.NodeGet(rowConfig(row), "provider")); ok && provider == route {
			ops, _ = removeRow(ops, "agent-default-model")
		}
	}
	return ops
}
