// paths_crud.go：技能路径/MCP 路径的 config.yaml CRUD（含删除清理预演）、
// 内置 per-agent 路径 defaults 与创建预设（从 agents 清单派生）。
package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

// ---------------------------------------------------------------------------
// 技能路径 CRUD
// ---------------------------------------------------------------------------

func CreateSkillPathInner(store *WorkspaceConfigStore, input RawSkillPathInput) (*WorkspacePathMutationResult, error) {
	var result *WorkspacePathMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		name, dir, err := normalizeRawSkillPathInput(input)
		if err != nil {
			return err
		}
		if _, exists := rawConfig.SkillPaths[name]; exists {
			return fmt.Errorf("技能路径已存在：%s", name)
		}
		rawConfig.SkillPaths[name] = &RawSkillPathConfig{Dir: dir}

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}
		return mutationResult(lock, name, &result)
	})
	return result, err
}

func UpdateSkillPathInner(store *WorkspaceConfigStore, currentName string, input RawSkillPathInput) (*WorkspacePathMutationResult, error) {
	current, err := normalizePathName(currentName)
	if err != nil {
		return nil, err
	}

	var result *WorkspacePathMutationResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		if _, exists := rawConfig.SkillPaths[current]; !exists {
			return pathUnavailable(current)
		}
		name, dir, err := normalizeRawSkillPathInput(input)
		if err != nil {
			return err
		}
		if name != current {
			if _, exists := rawConfig.SkillPaths[name]; exists {
				return fmt.Errorf("技能路径已存在：%s", name)
			}
		}
		delete(rawConfig.SkillPaths, current)
		rawConfig.SkillPaths[name] = &RawSkillPathConfig{Dir: dir}

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}
		return mutationResult(lock, name, &result)
	})
	return result, err
}

// DeleteSkillPathInner：删除技能路径登记；cleanup=true 时一并卸载目录里
// 指向任意受管技能源的目录链接（Reins 写入的内容由 Reins 清干净，见决策
// 记录 2026-11-28-skill-and-mcp-paths.md 第 5 条）。
func DeleteSkillPathInner(store *WorkspaceConfigStore, name string, cleanup bool) (*WorkspacePathMutationResult, error) {
	var result *WorkspacePathMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		config, err := parseManagerConfig(rawContent, lock.ConfigPath(), lock.Env())
		if err != nil {
			return err
		}
		path := config.skillPathByName(name)
		if path == nil {
			return pathUnavailable(name)
		}

		if cleanup {
			links, err := collectPathSourceLinks(store, config, path.Dir)
			if err != nil {
				return err
			}
			for _, link := range links {
				if err := removeExistingPath(link.destination); err != nil {
					return err
				}
			}
		}

		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		if _, exists := rawConfig.SkillPaths[name]; !exists {
			return pathUnavailable(name)
		}
		delete(rawConfig.SkillPaths, name)

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}
		return mutationResult(lock, name, &result)
	})
	return result, err
}

// PreviewSkillPathCleanupInner：删除清理预演：列出该目录里指向任意受管
// 技能源的目录链接（即 cleanup 删除时会卸载的全部内容）。只读，不改文件。
func PreviewSkillPathCleanupInner(store *WorkspaceConfigStore, name string) (*SkillPathCleanupPreview, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	path := config.skillPathByName(name)
	if path == nil {
		return nil, pathUnavailable(name)
	}
	links, err := collectPathSourceLinks(store, config, path.Dir)
	if err != nil {
		return nil, err
	}
	items := make([]SkillSyncItem, 0, len(links))
	for _, link := range links {
		items = append(items, removedItem(name, link.destination, link.resolved).ToSyncItem())
	}
	return &SkillPathCleanupPreview{Links: items}, nil
}

// collectPathSourceLinks：目录里指向任意受管技能源的目录链接（destination,
// resolved）对。删除清理与预演共用同一份判定。
func collectPathSourceLinks(store *WorkspaceConfigStore, config *ResolvedManagerConfig, dir string) ([]enumeratedLink, error) {
	links, err := enumerateLinks(dir)
	if err != nil {
		return nil, err
	}
	kept := make([]enumeratedLink, 0, len(links))
	for _, link := range links {
		for _, source := range config.SkillSources {
			root, err := resolveSourceRoot(store, source)
			if err != nil {
				return nil, err
			}
			if _, ok := relativeToSourceRoot(normalizeLinkPath(link.resolved), normalizeLinkPath(root)); ok {
				kept = append(kept, link)
				break
			}
		}
	}
	return kept, nil
}

// ---------------------------------------------------------------------------
// MCP 路径 CRUD
// ---------------------------------------------------------------------------

func CreateMcpPathInner(store *WorkspaceConfigStore, input RawMcpPathInput) (*WorkspacePathMutationResult, error) {
	var result *WorkspacePathMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		name, path, err := normalizeRawMcpPathInput(input, nil, lock.Env())
		if err != nil {
			return err
		}
		if _, exists := rawConfig.McpPaths[name]; exists {
			return fmt.Errorf("MCP 路径已存在：%s", name)
		}
		rawConfig.McpPaths[name] = path

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}
		return mutationResult(lock, name, &result)
	})
	return result, err
}

func UpdateMcpPathInner(store *WorkspaceConfigStore, currentName string, input RawMcpPathInput) (*WorkspacePathMutationResult, error) {
	current, err := normalizePathName(currentName)
	if err != nil {
		return nil, err
	}

	var result *WorkspacePathMutationResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		stored, exists := rawConfig.McpPaths[current]
		if !exists {
			return mcpPathUnavailable(current)
		}
		// config_type 是创建时选定的格式知识，更新保留存量值（客户端不传）。
		name, path, err := normalizeRawMcpPathInput(input, stored.ConfigType, lock.Env())
		if err != nil {
			return err
		}
		if name != current {
			if _, exists := rawConfig.McpPaths[name]; exists {
				return fmt.Errorf("MCP 路径已存在：%s", name)
			}
		}
		delete(rawConfig.McpPaths, current)
		rawConfig.McpPaths[name] = path

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}
		return mutationResult(lock, name, &result)
	})
	return result, err
}

// DeleteMcpPathInner：删除 MCP 路径登记；cleanup=true 时一并移除该配置
// 文件里名字与 Reins 已配置 server 相同的条目。
func DeleteMcpPathInner(store *WorkspaceConfigStore, name string, cleanup bool) (*WorkspacePathMutationResult, error) {
	var result *WorkspacePathMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		config, err := parseManagerConfig(rawContent, lock.ConfigPath(), lock.Env())
		if err != nil {
			return err
		}
		path := config.mcpPathByName(name)
		if path == nil {
			return mcpPathUnavailable(name)
		}

		if cleanup {
			serverNames, err := collectPathReinsServers(config, path)
			if err != nil {
				return err
			}
			for _, serverName := range serverNames {
				if _, err := removeMCPFromPathConfig(path, serverName); err != nil {
					return err
				}
			}
		}

		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		if _, exists := rawConfig.McpPaths[name]; !exists {
			return mcpPathUnavailable(name)
		}
		delete(rawConfig.McpPaths, name)

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}
		return mutationResult(lock, name, &result)
	})
	return result, err
}

// PreviewMcpPathCleanupInner：删除清理预演：列出 cleanup 删除时会移除的
// MCP 条目名。只读，不改文件。
func PreviewMcpPathCleanupInner(store *WorkspaceConfigStore, name string) (*McpPathCleanupPreview, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	path := config.mcpPathByName(name)
	if path == nil {
		return nil, mcpPathUnavailable(name)
	}
	serverNames, err := collectPathReinsServers(config, path)
	if err != nil {
		return nil, err
	}
	return &McpPathCleanupPreview{ServerNames: serverNames}, nil
}

// collectPathReinsServers：该 MCP 路径配置文件里与 Reins 已配置 server 同名
// 的条目名（writer 的 entryKeyFor 口径，如 dsh 的清洗名）。
func collectPathReinsServers(config *ResolvedManagerConfig, path *ResolvedMcpPath) ([]string, error) {
	existing, err := ReadExistingMCPEntries(path, path.ConfigPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(config.Mcps))
	for _, server := range config.Mcps {
		if _, exists := existing[mcpFormatWriterFor(path.MCPConfigType).entryKeyFor(server.Name)]; exists {
			names = append(names, server.Name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// ---------------------------------------------------------------------------
// 入参归一与成对校验
// ---------------------------------------------------------------------------

// normalizeRawSkillPathInput：技能路径入参的归一：条目名 trim 非空、目录
// trim 非空。
func normalizeRawSkillPathInput(input RawSkillPathInput) (string, string, error) {
	name, err := normalizePathName(input.Name)
	if err != nil {
		return "", "", err
	}
	dir := strings.TrimSpace(input.Dir)
	if dir == "" {
		return "", "", errors.New("技能路径的目录不能为空。")
	}
	return name, dir, nil
}

// normalizeRawMcpPathInput：MCP 路径入参的归一与成对校验。
//
// persistedConfigType：落盘的 config_type：create 传 nil（客户端必传、
// 入参层校验），update 传存量原值——config_type 是创建时选定的格式知识，
// 更新保留。configPrefix 缺省时按条目名回落内置 defaults（与解析侧同
// 口径，迁移条目与预设同名时成立）。
func normalizeRawMcpPathInput(
	input RawMcpPathInput,
	persistedConfigType *agents.McpConfigType,
	env *support.Environment,
) (string, *RawMcpPathConfig, error) {
	name, err := normalizePathName(input.Name)
	if err != nil {
		return "", nil, err
	}
	configPath := strings.TrimSpace(input.ConfigPath)
	if configPath == "" {
		return "", nil, fmt.Errorf("MCP 路径 %s 的 config_path 不能为空。", name)
	}

	// 有效 config_type：存量值优先，其次按名字回落内置 defaults，最后回落
	// common（自定义名字的缺省格式）。
	configType := agents.McpConfigCommon
	switch {
	case persistedConfigType != nil:
		configType = *persistedConfigType
	case input.ConfigType != "":
		parsed, err := parseMcpConfigType(strings.TrimSpace(input.ConfigType))
		if err != nil {
			return "", nil, err
		}
		configType = parsed
	}

	prefix := strings.TrimSpace(input.ConfigPrefix)
	if prefix == "" {
		if defaults, ok := builtinMcpPathDefaultsMap(env)[name]; ok {
			prefix = defaults.ConfigPrefix
		}
	}

	// MCP 配置文件和 configPrefix 必须成对出现：只有前缀没有路径无处可写
	// （路径已必填），只有路径没有前缀无法定位写入节点。dsh/hermes 按
	// 文件结构定位条目（writer 声明 prefix 不必填），允许空 prefix。
	if prefix == "" && mcpFormatWriterFor(configType).prefixRequired() {
		return "", nil, fmt.Errorf("MCP 路径 %s 的 config_prefix 不能为空。", name)
	}

	var configTypeCopy *agents.McpConfigType
	if persistedConfigType != nil {
		copied := *persistedConfigType
		configTypeCopy = &copied
	} else {
		copied := configType
		configTypeCopy = &copied
	}
	var prefixCopy *string
	if prefix != "" {
		copied := prefix
		prefixCopy = &copied
	}
	pathCopy := configPath

	return name, &RawMcpPathConfig{
		ConfigPath:   &pathCopy,
		ConfigPrefix: prefixCopy,
		ConfigType:   configTypeCopy,
	}, nil
}

// normalizePathName：条目名 trim 后非空（用户可见命名，不做字符集限制；
// YAML 键的转义由序列化器负责）。
func normalizePathName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("路径条目名不能为空。")
	}
	return trimmed, nil
}

func mcpPathUnavailable(name string) error {
	return fmt.Errorf("MCP 路径不存在：%s", name)
}

// mutationResult：CRUD 成功回执的统一组装（updatedPaths = config.yaml）。
func mutationResult(lock *ConfigLock, name string, result **WorkspacePathMutationResult) error {
	path := support.DisplayPath(lock.ConfigPath())
	*result = &WorkspacePathMutationResult{
		PathID:       name,
		UpdatedPaths: []string{path},
	}
	return nil
}

// ---------------------------------------------------------------------------
// Defaults 与预设（从 agents 清单派生）
// ---------------------------------------------------------------------------

// SkillPathDefaults：一个内置工具的技能路径 defaults。
type SkillPathDefaults struct {
	Name string
	Dir  string
}

// McpPathDefaults：一个内置工具的 MCP 路径 defaults。
type McpPathDefaults struct {
	Name         string
	ConfigPath   string
	ConfigPrefix string
	ConfigType   agents.McpConfigType
}

// GetSkillPathPresetsInner：技能路径创建弹窗一次下发的内置工具预设（与
// defaults 同源、按 AgentSpecs 清单顺序）。条目名预填产品名（用户可改）；
// 根目录解析失败（无 HOME）的预设不下发——空目录预设只会制造必填校验噪音。
func GetSkillPathPresetsInner(env *support.Environment) []SkillPathPreset {
	defaults := builtinSkillPathDefaultsMap(env)
	presets := make([]SkillPathPreset, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		item, ok := defaults[spec.Key]
		if !ok || item.Dir == "" {
			continue
		}
		presets = append(presets, SkillPathPreset{
			Name: spec.Label,
			Dir:  support.DisplayPath(item.Dir),
		})
	}
	return presets
}

// GetMcpPathPresetsInner：MCP 路径创建弹窗一次下发的内置工具预设；解析
// 失败的预设同样不下发。
func GetMcpPathPresetsInner(env *support.Environment) []McpPathPreset {
	defaults := builtinMcpPathDefaultsMap(env)
	presets := make([]McpPathPreset, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		item, ok := defaults[spec.Key]
		if !ok || item.ConfigPath == "" {
			continue
		}
		presets = append(presets, McpPathPreset{
			Name:              spec.Label,
			ConfigPath:        support.DisplayPath(item.ConfigPath),
			ConfigPrefix:      item.ConfigPrefix,
			ConfigType:        string(item.ConfigType),
			FormatDescription: mcpFormatWriterFor(item.ConfigType).description(),
		})
	}
	return presets
}

// GetMcpFormatInfosInner：全部 MCP 配置格式的说明与写入形态示例，按清单
// 首次出现顺序去重下发。示例与真实写入走同一条构造路径
// （writer.shapeExamples），前缀取该格式在清单里首个工具的约定值。
func GetMcpFormatInfosInner() []McpFormatInfo {
	seen := map[agents.McpConfigType]bool{}
	infos := make([]McpFormatInfo, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		if seen[spec.Mcp.ConfigType] {
			continue
		}
		seen[spec.Mcp.ConfigType] = true
		writer := mcpFormatWriterFor(spec.Mcp.ConfigType)
		examples := make([]McpFormatExample, 0)
		if shaped, err := writer.shapeExamples(spec.Mcp.Prefix, "my-server"); err == nil {
			for _, example := range shaped {
				examples = append(examples, McpFormatExample{Label: example.label, Body: example.body})
			}
		}
		infos = append(infos, McpFormatInfo{
			ConfigType:     string(spec.Mcp.ConfigType),
			Description:    writer.description(),
			FormatExamples: examples,
		})
	}
	return infos
}

// builtinSkillPathDefaults：技能路径 defaults 从 agents 清单派生：目录 =
// 环境解析出的 GlobalRoot + spec 的相对布局；根目录解析失败（无 HOME）时
// dir 落空串（解析回落与预设过滤共用同一张表），与逐条手写时代的口径一致。
func builtinSkillPathDefaults(env *support.Environment) []SkillPathDefaults {
	defaults := make([]SkillPathDefaults, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		item := SkillPathDefaults{Name: spec.Key, Dir: ""}
		if resolved, ok := spec.Global.Root.ResolvePath(env, spec.Global.SkillDir); ok {
			item.Dir = resolved
		}
		defaults = append(defaults, item)
	}
	return defaults
}

func builtinSkillPathDefaultsMap(env *support.Environment) map[string]SkillPathDefaults {
	items := builtinSkillPathDefaults(env)
	byName := make(map[string]SkillPathDefaults, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}
	return byName
}

// builtinMcpPathDefaults：MCP 路径 defaults 从 agents 清单派生；无 MCP 配置
// 入口的工具不进表。解析失败（无 HOME）的条目 ConfigPath 落空串——解析回落
// 的前缀/类型豁免（dsh 空前缀合法）仍需要这张表，预设层再按空值过滤。
func builtinMcpPathDefaults(env *support.Environment) []McpPathDefaults {
	defaults := make([]McpPathDefaults, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		if spec.Global.McpConfigPath == "" {
			continue
		}
		item := McpPathDefaults{
			Name:         spec.Key,
			ConfigPath:   "",
			ConfigPrefix: spec.Mcp.Prefix,
			ConfigType:   spec.Mcp.ConfigType,
		}
		if resolved, ok := spec.Global.Root.ResolvePath(env, spec.Global.McpConfigPath); ok {
			item.ConfigPath = resolved
		}
		defaults = append(defaults, item)
	}
	return defaults
}

func builtinMcpPathDefaultsMap(env *support.Environment) map[string]McpPathDefaults {
	items := builtinMcpPathDefaults(env)
	byName := make(map[string]McpPathDefaults, len(items))
	for _, item := range items {
		byName[item.Name] = item
	}
	return byName
}

// sseUnsupportedConfigPaths：声明「不接受 legacy SSE transport」的内置工具
// 的配置文件（展示形态小写归一，兼容 Windows 大小写差异）。MCP 路径视图按
// 配置文件一致判定 SSE 兼容性，不按条目名猜——散装化后自建别名路径也能
// 命中（决策记录 2026-11-28-mcp-matrix-sync.md 第 3 条）。
func sseUnsupportedConfigPaths(env *support.Environment) map[string]bool {
	paths := make(map[string]bool)
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		if !spec.Mcp.SSEUnsupported || spec.Global.McpConfigPath == "" {
			continue
		}
		if resolved, ok := spec.Global.Root.ResolvePath(env, spec.Global.McpConfigPath); ok {
			paths[strings.ToLower(support.DisplayPath(resolved))] = true
		}
	}
	return paths
}
