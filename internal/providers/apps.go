// 各工具全局配置文件的适配器层：每个适配器只负责「生成新文件内容」，
// diff 预览与原子写入统一由 commands 层完成。providers 域与 workspace
// 互不依赖，这里自带一份极小的 JSON/TOML 读写助手。
//
// JSON/TOML 的统一内存形态是 map[string]any：Rust serde_json::Map 与
// toml::Value::Table 都是排序字典，Go 的 encoding/json 与 BurntSushi/toml
// 对 map 同样按 key 排序序列化，生成的文件内容形态等价；数组保序两侧一致。
// 数值统一经 json.Number / int64 承载，避免 float64 重组大整数丢精度。
package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"reins/internal/agents"
	"reins/internal/support"
)

// ReinsPrefix：Reins 在各工具配置中的内部注册键前缀；反显按该前缀识别
// Reins 条目。
const ReinsPrefix = "reins-"

// MaskedKey：预览时密钥的脱敏占位符；任何路径（日志、错误、diff）都只允许
// 出现它。
const MaskedKey = "******"

func RegistrationKey(providerID string) string {
	return ReinsPrefix + providerID
}

// 移除路径的三句报错，全部条目生命周期共用、逐字单一来源：条目内容被
// 手工改过不自动删；注册键查不到；外部键查不到。app_label 是各家文案里
// 的既有主语（Codex/OpenCode/Pi/Grok/dsh），不做大小写统一。
func modifiedEntryError(key string) error {
	return fmt.Errorf("条目 %s 已被手工修改，无法自动识别，请手动处理。", key)
}

func unrecognizedEntryError(appLabel string, registration string) error {
	return fmt.Errorf("%s 中没有可识别的 %s 条目。", appLabel, registration)
}

func entryNotFoundError(appLabel string, entryKey string) error {
	return fmt.Errorf("%s 中没有外部条目 %s。", appLabel, entryKey)
}

// FileOutput：适配器产出的一份「完整新文件内容」；不直接落盘。
type FileOutput struct {
	Path    string
	Content string
}

// ModelField：模型元数据维度。requiredModelFields 用它做应用门控，
// unwrittenModelFields 用它声明该工具不写入哪些元数据。
type ModelField int

const (
	FieldMaxOutputTokens ModelField = iota
	FieldSupportsImages
	FieldReasoning
)

func (f ModelField) Label() string {
	switch f {
	case FieldMaxOutputTokens:
		return "最大输出"
	case FieldSupportsImages:
		return "图像输入"
	case FieldReasoning:
		return "推理能力"
	}
	return ""
}

func (f ModelField) isPresent(model *ProviderModelRecord) bool {
	switch f {
	case FieldMaxOutputTokens:
		return model.MaxOutputTokens != nil
	case FieldSupportsImages:
		return model.SupportsImages != nil
	case FieldReasoning:
		return model.Reasoning != nil
	}
	return false
}

// AppCapability：工具能力表——协议兼容 + 必填字段门控 + 不写入的元数据 +
// 思考等级支持集 + 替换/并存语义。
type AppCapability struct {
	SupportedProtocols []ProviderProtocol
	// Additive：true = 多 Provider 并存（新增条目）；false = 单活动
	// Provider（应用即替换）。
	Additive bool
	// RequiredModelFields：目标工具必填的模型元数据；缺失时应用按钮禁用
	// 并列出缺失项。v1 五个工具暂无硬必填项，机制保留待 unverified 项
	// 验证后填充。
	RequiredModelFields []ModelField
	// UnwrittenModelFields：该工具配置里没有落点的模型元数据；应用弹窗
	// 据此明示用户填写的这些值不会影响该工具。
	UnwrittenModelFields     []ModelField
	SupportedReasoningLevels []ReasoningLevel
	// BehaviorNotes：工具的隐形行为约束（如档位覆盖、进程缓存、一次性
	// 导入语义），Agent 页「?」说明弹窗按此展示；没有值得说明的行为时为空。
	BehaviorNotes []string
}

// AppAdapter：一个工具配置的适配器。方言（reasoning/protocol）的正向写入、
// 反向识别与 empty_state 下发的映射文案全部从方言表派生（dialect.go）。
type AppAdapter interface {
	ID() ProviderApp
	Capability() AppCapability

	// ConfigPaths：该工具在 providers 域管理的配置文件清单（能力声明）。
	// inspect 失败的兜底展示与外部调用共用；路径解析失败由调用方兜底为
	// 空列表。注意与 Inspect 内部读取的文件集不一定相同（如 dsh 的
	// Inspect 还会读 workspace 域管理的全局 cordis patch）。
	ConfigPaths(env *support.Environment) ([]string, error)

	// RestrictedPaths：敏感文件清单（凭据等），写入时强制 owner-only 权限。
	// 默认空；依赖方若对文件权限有守卫（如 dsh 启动时拒绝加载非 0600 的
	// 凭据），必须在此声明，否则原子写会用默认权限落盘。
	RestrictedPaths(env *support.Environment) []string

	// ReasoningDialect：本工具的思考档位方言。apply 写入、inspect 反读、
	// 弹窗映射文案全部从这张表派生（特殊映射在表内声明，如 Grok
	// off→none、zcode off→disabled、pi-ai off→off），正反互逆由 roundtrip
	// 测试守卫。
	ReasoningDialect() *ReasoningDialect

	// ProtocolDialect：本工具的协议方言；配置文件里没有协议字段、语义
	// 固定的工具（Claude）返回 nil。roundtrip 测试经适配器遍历取用；
	// 生产路径直接引用各方言常量。
	ProtocolDialect() *ProtocolDialect

	// Inspect：反读工具配置；providers.yaml 解析结果用于把 reins- 条目
	// 归类为已应用 / 漂移，无法归类的都按外部配置展示。
	Inspect(env *support.Environment, providers map[string]*ResolvedProvider) (*ProviderAppState, error)

	// Apply：生成应用后的完整新文件内容列表；不直接落盘。
	Apply(env *support.Environment, provider *ResolvedProvider, plan *ApplyProviderInput, apiKey string) ([]FileOutput, error)

	// Remove：生成移除后的完整新文件内容列表。provider 为 nil 表示平台
	// 元数据已删除，只能按注册键结构化识别；不支持这种方式（Claude）
	// 时报错。
	Remove(env *support.Environment, providerID string, provider *ResolvedProvider) ([]FileOutput, error)

	// RemoveExternal：删除外部（非 Reins 纳管）provider 条目：按工具配置
	// 里的原样键结构化删除。外部条目内容任意，跳过内容识别校验；默认
	// 模型/活动 Provider 仍指向被删条目时随删除清空。Claude 的外部配置
	// 内嵌在 env 中，无可删除条目，实现固定报错。
	RemoveExternal(env *support.Environment, entryKey string) ([]FileOutput, error)
}

// 各适配器单例：方言共享（pi 与 dsh 引用同一份 pi-ai 方言常量）依赖同一
// 实例，不得每次调用新建。
var (
	codexAdapter     = &CodexAdapter{}
	claudeAdapter    = &ClaudeAdapter{}
	opencodeAdapter  = &OpencodeAdapter{}
	piAdapter        = &PiAdapter{}
	grokbuildAdapter = &GrokbuildAdapter{}
	dshAdapter       = &DshAdapter{}
	zcodeAdapter     = &ZcodeAdapter{}
)

func AdapterFor(app ProviderApp) AppAdapter {
	switch app {
	case agents.ProviderCodex:
		return codexAdapter
	case agents.ProviderClaude:
		return claudeAdapter
	case agents.ProviderOpencode:
		return opencodeAdapter
	case agents.ProviderPi:
		return piAdapter
	case agents.ProviderGrokbuild:
		return grokbuildAdapter
	case agents.ProviderDsh:
		return dshAdapter
	case agents.ProviderZcode:
		return zcodeAdapter
	}
	return nil
}

// ---------------------------------------------------------------------------
// 能力检查
// ---------------------------------------------------------------------------

func EnsureProtocolSupported(adapter AppAdapter, provider *ResolvedProvider) error {
	capability := adapter.Capability()
	for _, protocol := range capability.SupportedProtocols {
		if protocol == provider.Protocol {
			return nil
		}
	}
	labels := make([]string, 0, len(capability.SupportedProtocols))
	for _, p := range capability.SupportedProtocols {
		labels = append(labels, ProtocolLabel(p))
	}
	return fmt.Errorf("%s 不支持协议 %s，支持：%s",
		ProviderAppLabel(adapter.ID()), ProtocolLabel(provider.Protocol), strings.Join(labels, "、"))
}

func ProtocolLabel(protocol ProviderProtocol) string {
	switch protocol {
	case ProtocolOpenaiResponses:
		return "openai_responses"
	case ProtocolOpenaiChatCompletions:
		return "openai_chat_completions"
	case ProtocolAnthropicMessages:
		return "anthropic_messages"
	}
	return ""
}

// DefaultReasoningLevelValidator：需要按模型菜单校验默认档的适配器实现。
// ValidatePlan 在通用能力校验后调用；Grok 的 reasoning_efforts 菜单只接受
// 菜单内的档位，写盘前必须按默认模型再校验一次。
type DefaultReasoningLevelValidator interface {
	ValidateDefaultReasoningLevel(provider *ResolvedProvider, plan *ApplyProviderInput) error
}

// ValidatePlan：应用前置校验——模型选择合法 + 必填元数据齐全；返回能力
// 警告（不阻断）。
func ValidatePlan(adapter AppAdapter, provider *ResolvedProvider, plan *ApplyProviderInput) ([]string, error) {
	if len(plan.ModelIDs) == 0 {
		return nil, errors.New("至少选择一个模型。")
	}
	known := map[string]bool{}
	for i := range provider.Models {
		known[provider.Models[i].ID] = true
	}
	for _, modelID := range plan.ModelIDs {
		if !known[modelID] {
			return nil, fmt.Errorf("模型 %s 不在平台 %s 的目录中。", modelID, provider.ID)
		}
	}
	hasDefault := false
	for _, modelID := range plan.ModelIDs {
		if modelID == plan.DefaultModelID {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		return nil, errors.New("默认模型必须在已选模型中。")
	}
	capability := adapter.Capability()
	if plan.DefaultReasoningLevel != nil {
		supported := false
		for _, level := range capability.SupportedReasoningLevels {
			if level == *plan.DefaultReasoningLevel {
				supported = true
				break
			}
		}
		if !supported {
			return nil, fmt.Errorf("%s 不支持思考等级 %s。",
				ProviderAppLabel(adapter.ID()), plan.DefaultReasoningLevel.WriteForm())
		}
	}
	if validator, ok := adapter.(DefaultReasoningLevelValidator); ok {
		if err := validator.ValidateDefaultReasoningLevel(provider, plan); err != nil {
			return nil, err
		}
	}

	var warnings []string
	for _, modelID := range plan.ModelIDs {
		var model *ProviderModelRecord
		for i := range provider.Models {
			if provider.Models[i].ID == modelID {
				model = &provider.Models[i]
				break
			}
		}
		var missing []string
		for _, field := range capability.RequiredModelFields {
			if !field.isPresent(model) {
				missing = append(missing, field.Label())
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("模型 %s 缺少 %s 必填项：%s",
				modelID, ProviderAppLabel(adapter.ID()), strings.Join(missing, "、"))
		}
		if model.ContextWindow == nil {
			warnings = append(warnings, fmt.Sprintf("模型 %s 未填写上下文窗口。", modelID))
		}
		if model.MaxOutputTokens == nil {
			warnings = append(warnings, fmt.Sprintf("模型 %s 未填写最大输出。", modelID))
		}
	}
	warnings = dedupStrings(warnings)
	return warnings, nil
}

func dedupStrings(values []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

// ---------------------------------------------------------------------------
// 配置文件读写原语（providers 域自持，避免依赖 workspace）
// ---------------------------------------------------------------------------

func readJSONObject(path string) (map[string]any, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %s: %w", path, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return map[string]any{}, nil
	}
	value, err := support.DecodeJSONStrict(string(content))
	if err != nil {
		return nil, fmt.Errorf("JSON 解析失败：%s", path)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("配置文件顶层必须是 JSON 对象：%s", path)
	}
	return object, nil
}

// decodeJSONNumber：json.Number 承载数值，往返不失真（等价 serde_json 直保
// 原始字面量）。
func decodeJSONNumber(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func serializeJSON(object map[string]any) (string, error) {
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	// serde_json 不做 HTML 转义；生成的配置可能携带 URL（含 &），保持原样。
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(object); err != nil {
		return "", err
	}
	// json.Encoder.Encode 自带换行，语义与 Rust push('\n') 一致。
	return builder.String(), nil
}

func readTOML(path string) (map[string]any, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %s: %w", path, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := toml.Unmarshal(content, &root); err != nil {
		return nil, fmt.Errorf("TOML 解析失败：%s", path)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func serializeTOML(value map[string]any) (string, error) {
	// 错误上下文不能嵌入文档内容：TOML 里可能带明文密钥。
	serialized, err := toml.Marshal(value)
	if err != nil {
		return "", errors.New("TOML 序列化失败")
	}
	return string(serialized), nil
}

// ---------------------------------------------------------------------------
// 行级 diff（LCS）
// ---------------------------------------------------------------------------

func DiffLines(oldContent string, newContent string) []DiffLine {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	// LCS 动态规划表；工具配置文件都很小，O(n*m) 可接受。
	table := make([][]int, len(oldLines)+1)
	for i := range table {
		table[i] = make([]int, len(newLines)+1)
	}
	for i := len(oldLines) - 1; i >= 0; i-- {
		for j := len(newLines) - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}

	diff := make([]DiffLine, 0, len(oldLines)+len(newLines))
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		switch {
		case oldLines[i] == newLines[j]:
			diff = append(diff, DiffLine{Kind: DiffContext, Text: oldLines[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			diff = append(diff, DiffLine{Kind: DiffRemove, Text: oldLines[i]})
			i++
		default:
			diff = append(diff, DiffLine{Kind: DiffAdd, Text: newLines[j]})
			j++
		}
	}
	for ; i < len(oldLines); i++ {
		diff = append(diff, DiffLine{Kind: DiffRemove, Text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		diff = append(diff, DiffLine{Kind: DiffAdd, Text: newLines[j]})
	}
	return diff
}

// splitLines 对齐 Rust str::lines()：按 \n 切分、丢弃结尾空段、容忍 \r\n
// （行内容不含 \r）。
func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// ---------------------------------------------------------------------------
// 共享条目构造（反显）
// ---------------------------------------------------------------------------

func stringPtr(value string) *string { return &value }

// stringPtrOrNil：空串归一为 nil（Rust Option<String> 的 Some 空串只会在
// 显式空值语义下出现，反读里的可选字符串缺省即 None）。
func stringPtrOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func appliedEntry(key string, provider *ResolvedProvider, baseURL string, hasBaseURL bool, modelIDs []string, defaultModelID *string, protocol *ProviderProtocol) ProviderAppEntry {
	entry := ProviderAppEntry{
		Key:            key,
		Status:         EntryStatusApplied,
		ProviderID:     stringPtr(provider.ID),
		Label:          stringPtr(provider.Label),
		ModelIDs:       modelIDs,
		DefaultModelID: defaultModelID,
		Notes:          []string{},
		Protocol:       protocol,
	}
	if hasBaseURL {
		entry.BaseURL = stringPtr(baseURL)
	}
	return entry
}

func driftedEntry(key string, providerID string, baseURL string, hasBaseURL bool, note string, protocol *ProviderProtocol) ProviderAppEntry {
	entry := ProviderAppEntry{
		Key:        key,
		Status:     EntryStatusDrifted,
		ProviderID: stringPtr(providerID),
		ModelIDs:   []string{},
		Notes:      []string{note},
		Protocol:   protocol,
	}
	if hasBaseURL {
		entry.BaseURL = stringPtr(baseURL)
	}
	return entry
}

func externalEntry(key string, baseURL string, hasBaseURL bool, notes []string, protocol *ProviderProtocol) ProviderAppEntry {
	entry := ProviderAppEntry{
		Key:      key,
		Status:   EntryStatusExternal,
		ModelIDs: []string{},
		Notes:    notes,
		Protocol: protocol,
	}
	if hasBaseURL {
		entry.BaseURL = stringPtr(baseURL)
	}
	return entry
}

// classifyReinsEntry：reins- 前缀条目的统一归类——providers.yaml 匹配 →
// 已应用；不匹配或平台已删除 → 漂移；无前缀 → 外部配置（只展示不纳管）。
// status 字段承载已应用/漂移二态，调用方直接入列。
func classifyReinsEntry(key string, baseURL string, hasBaseURL bool, protocol *ProviderProtocol, providers map[string]*ResolvedProvider) ProviderAppEntry {
	return classifyReinsEntryWith(key, baseURL, hasBaseURL, protocol, providers,
		func(entryBaseURL string, hasBaseURL bool, provider *ResolvedProvider) bool {
			return hasBaseURL && entryBaseURL == provider.BaseURL
		})
}

// classifyReinsEntryWith 的比较口径钩子：entry 的 base_url 与平台 base_url
// 怎么算一致。Grok 的写入口径会把 Anthropic 平台 base_url 折算成 /v1 结尾，
// 恒等比较会把它错判成漂移；口径不是恒等的适配器经此声明。
func classifyReinsEntryWith(key string, baseURL string, hasBaseURL bool, protocol *ProviderProtocol, providers map[string]*ResolvedProvider, baseURLMatches func(entryBaseURL string, hasBaseURL bool, provider *ResolvedProvider) bool) ProviderAppEntry {
	providerID := strings.TrimPrefix(key, ReinsPrefix)
	provider, exists := providers[providerID]
	switch {
	case exists && baseURLMatches(baseURL, hasBaseURL, provider):
		return appliedEntry(key, provider, baseURL, hasBaseURL, []string{}, nil, protocol)
	case exists:
		return driftedEntry(key, providerID, baseURL, hasBaseURL,
			fmt.Sprintf("base_url 与平台 %s 当前配置（%s）不一致。", provider.ID, provider.BaseURL), protocol)
	default:
		return driftedEntry(key, providerID, baseURL, hasBaseURL, "平台已删除。", protocol)
	}
}

func EmptyState(app ProviderApp, configPaths []string) *ProviderAppState {
	adapter := AdapterFor(app)
	capability := adapter.Capability()
	reasoningDialect := adapter.ReasoningDialect()
	reasoningLevelWrites := make([]ReasoningEffortWrite, 0, len(capability.SupportedReasoningLevels))
	for _, level := range capability.SupportedReasoningLevels {
		reasoningLevelWrites = append(reasoningLevelWrites, ReasoningEffortWrite{
			Level:  level,
			Writes: reasoningDialect.Spell(level),
		})
	}
	unwritten := make([]string, 0, len(capability.UnwrittenModelFields))
	for _, field := range capability.UnwrittenModelFields {
		unwritten = append(unwritten, field.Label())
	}
	behaviorNotes := make([]string, len(capability.BehaviorNotes))
	copy(behaviorNotes, capability.BehaviorNotes)
	displayPaths := make([]string, 0, len(configPaths))
	for _, path := range configPaths {
		displayPaths = append(displayPaths, support.DisplayPath(path))
	}
	return &ProviderAppState{
		App:                      app,
		ConfigPaths:              displayPaths,
		ConfigExists:             false,
		SupportedProtocols:       append([]ProviderProtocol{}, capability.SupportedProtocols...),
		SupportedReasoningLevels: append([]ReasoningLevel{}, capability.SupportedReasoningLevels...),
		ReasoningLevelWrites:     reasoningLevelWrites,
		Additive:                 capability.Additive,
		UnwrittenModelFields:     unwritten,
		BehaviorNotes:            behaviorNotes,
		Entries:                  []ProviderAppEntry{},
	}
}

// ---------------------------------------------------------------------------
// 工具配置路径
//
// 目录的相对布局不在本文件重述：根经 Environment 按重定向解析，配置文件名
// 从 agents 清单派生（McpConfigPath）；只有「同一目录下的另一个文件」
//（codex 的 reins-models.json、pi 的 models.json/settings.json、claude 的
// settings.json）在这里显式拼接。
// ---------------------------------------------------------------------------

func codexHomeDir(env *support.Environment) (string, error) {
	return env.ResolveRequired(agents.RootCodexHome.Redirect)
}

func codexConfigPath(env *support.Environment) (string, error) {
	path, ok := mcpConfigPath(env, agents.ProviderCodex)
	if !ok {
		return "", errors.New("codex 缺少 MCP 配置入口。")
	}
	return path, nil
}

func claudeConfigDir(env *support.Environment) (string, error) {
	return env.ResolveRequired(agents.RootClaudeConfigDir.Redirect)
}

func claudeSettingsPath(env *support.Environment) (string, error) {
	dir, err := claudeConfigDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// opencodeCandidatePaths：opencode.json 与 opencode.jsonc 都会被 OpenCode
// 加载并按顶层键合并，后者覆盖前者（v2.0.18 config/discovery.ts + config.ts
// findLast）；顺序即优先级：低 → 高。基名从 agents 清单派生（与 codex/grok
// 同一条路径来源），jsonc 是清单不表达的工具加载语义，按同目录兄弟候选枚举。
// OpenCode 在 Windows 上不认 XDG_CONFIG_HOME（v2.0.16 实测，官方文档未写
// 差异）；清单登记的就是 HOME 相对路径，派生结果与该行为一致。
func opencodeCandidatePaths(env *support.Environment) ([]string, error) {
	base, ok := mcpConfigPath(env, agents.ProviderOpencode)
	if !ok {
		return nil, errors.New("OpenCode 配置路径未在 agents 清单登记")
	}
	return []string{base, withExtension(base, "jsonc")}, nil
}

func piModelsPath(env *support.Environment) (string, error) {
	dir, err := env.ResolveRequired(agents.RootPiAgentDir.Redirect)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "models.json"), nil
}

func piSettingsPath(env *support.Environment) (string, error) {
	dir, err := env.ResolveRequired(agents.RootPiAgentDir.Redirect)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

func grokConfigPath(env *support.Environment) (string, error) {
	path, ok := mcpConfigPath(env, agents.ProviderGrokbuild)
	if !ok {
		return "", errors.New("grok 缺少 MCP 配置入口。")
	}
	return path, nil
}

// mcpConfigPath：某工具在全局清单里声明的 MCP 配置路径（无该入口返回
// false）。清单路径解析的唯一入口是 support.ManifestPath，`~/` 语义只有
// 一份。
func mcpConfigPath(env *support.Environment, app ProviderApp) (string, bool) {
	spec := agents.SpecByProviderApp(app)
	if spec == nil || spec.Global.McpConfigPath == "" {
		return "", false
	}
	root, rootOK := spec.Global.Root.Resolve(env)
	return support.ManifestPath(env, root, rootOK, spec.Global.McpConfigPath)
}

// withExtension 对齐 Rust Path::with_extension 的语义：只替换最后一个
// 扩展名，无扩展名时追加。
func withExtension(path string, extension string) string {
	dir, file := filepath.Split(path)
	dot := strings.LastIndexByte(file, '.')
	if dot <= 0 {
		return dir + file + "." + extension
	}
	return dir + file[:dot+1] + extension
}

// ---------------------------------------------------------------------------
// 共享写入助手
// ---------------------------------------------------------------------------

// WriteFiles：restricted 内的路径按敏感文件写（强制 owner-only 权限），其余
// 沿用原子写；受限清单由写入方从 adapter 的 RestrictedPaths() 取。
func WriteFiles(files []FileOutput, restricted []string) error {
	for _, file := range files {
		write := support.WriteAtomic
		for _, path := range restricted {
			if path == file.Path {
				write = support.WriteAtomicPrivate
				break
			}
		}
		if err := write(file.Path, file.Content); err != nil {
			return fmt.Errorf("Failed to write %s: %w", file.Path, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// map[string]any 的小工具：域内名号，口径统一在 support（As* 断言与
// SortedKeys——miss 语义与 json.Number 精度处理只此一份）。
// ---------------------------------------------------------------------------

func asObject(value any) (map[string]any, bool) {
	return support.AsObject(value)
}

func asArray(value any) ([]any, bool) {
	return support.AsArray(value)
}

func asString(value any) (string, bool) {
	return support.AsString(value)
}

func asInt64(value any) (int64, bool) {
	return support.AsInt64(value)
}

func asBool(value any) (bool, bool) {
	return support.AsBool(value)
}

// sortedStringKeys：排序字典遍历序。Rust BTreeMap 迭代恒为字典序，反读
// 条目顺序与匹配优先级依赖它。
func sortedStringKeys(object map[string]any) []string {
	return support.SortedKeys(object)
}

// sortedMapKeys：同上，值类型泛型版（models.dev 目录等专用 map 也共用）。
func sortedMapKeys[V any](object map[string]V) []string {
	return support.SortedKeys(object)
}
