// targets.go：目录链接探测（平台差异见 link_unix.go / link_windows.go）、
// target/项目的 config.yaml CRUD、内置 per-agent 路径 defaults（从 agents
// 清单派生）。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

// ---------------------------------------------------------------------------
// Directory-link skill_dir detection
// ---------------------------------------------------------------------------

// isLinkedSkillDir：落点是不是一个目录链接。
func isLinkedSkillDir(path string) (bool, error) {
	linked, err := readSkillDirLink(path)
	if err != nil {
		return false, err
	}
	return linked != nil, nil
}

func readSkillDirLink(path string) (*string, error) {
	return readDirectoryLinkTarget(path)
}

// readDirectoryLinkTarget：软链接/junction 的目标路径；不是链接返回 nil。
func readDirectoryLinkTarget(path string) (*string, error) {
	metadata, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Windows 的断链 junction 仍可能保留 reparse point，但 Lstat 会
			// 报 NotFound；直接读 reparse 数据可保留原目标路径。非 Windows
			// 平台断链没有可读数据，返回 nil。
			return readPlatformBrokenLinkTarget(path)
		}
		return nil, fmt.Errorf("读取 %s 的元数据失败：%v", path, err)
	}
	if metadata.Mode()&os.ModeSymlink != 0 {
		raw, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("读取 %s 的软链接失败：%v", path, err)
		}
		resolved := resolveSymlinkTargetPath(path, raw)
		return &resolved, nil
	}
	return readPlatformDirectoryLinkTarget(path, metadata)
}

// resolveSymlinkTargetPath：软链接目标解析（相对链接基于链接所在目录），并
// canonicalize（失败回落原路径）。与 skill_install.go 的同名函数不同：那
// 一份不做 canonicalize（分类时保留拼写），这份用于展示与归属表键。
func resolveSymlinkTargetPath(destination string, linkTarget string) string {
	resolved := linkTarget
	if filepath.IsAbs(linkTarget) {
		resolved = linkTarget
	} else {
		resolved = joinPath(filepath.Dir(destination), linkTarget)
	}
	if canonical, err := support.Canonicalize(resolved); err == nil {
		return canonical
	}
	return resolved
}

// ---------------------------------------------------------------------------
// target CRUD（config.yaml）
// ---------------------------------------------------------------------------

func CreateWorkspaceTargetInner(store *WorkspaceConfigStore, input RawTargetInput) (*WorkspaceTargetMutationResult, error) {
	var result *WorkspaceTargetMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		// create 只接受内置工具，MCP 格式由 AgentSpec 派生，不信任客户端
		// 传值。配置节点路径默认留空（用户未显式提供 config_path 时不假设
		// 想要 MCP，避免默认值诱发校验噪音）；用户显式提供 config_path 时
		// 从 defaults 取同工具的 configPrefix，与解析侧同口径。
		targetID, err := normalizeTargetID(input.TargetID)
		if err != nil {
			return err
		}
		env := lock.Env()
		configType, err := builtinCreateConfigType(targetID, env)
		if err != nil {
			return err
		}
		var defaultsPrefix *string
		if input.ConfigPath != nil && strings.TrimSpace(*input.ConfigPath) != "" {
			if defaults, ok := builtinTargetDefaultsMap(env)[targetID]; ok {
				prefix := defaults.ConfigPrefix
				defaultsPrefix = &prefix
			}
		}
		nextID, targetConfig, err := normalizeRawTargetInput(input, &configType, defaultsPrefix, env)
		if err != nil {
			return err
		}

		if _, exists := rawConfig.Targets[string(nextID)]; exists {
			return fmt.Errorf("target 已存在：%s", nextID)
		}

		rawConfig.Targets[string(nextID)] = targetConfig

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceTargetMutationResult{
			TargetID:     nextID,
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

func UpdateWorkspaceTargetInner(store *WorkspaceConfigStore, currentTargetID string, input RawTargetInput) (*WorkspaceTargetMutationResult, error) {
	currentID, err := normalizeTargetID(currentTargetID)
	if err != nil {
		return nil, err
	}

	var result *WorkspaceTargetMutationResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		current, exists := rawConfig.Targets[string(currentID)]
		if !exists {
			return fmt.Errorf("target 不存在：%s", currentID)
		}

		// 存量 config_type 与 config_prefix 原样保留（省略仍省略、显式仍
		// 显式），客户端不再传这两个字段；有效类型回落内置 defaults 的口径
		// 与解析侧一致。
		var storedPrefix *string
		if current.Mcp.ConfigPrefix != nil {
			prefix := *current.Mcp.ConfigPrefix
			storedPrefix = &prefix
		}
		var storedConfigType *agents.McpConfigType
		if current.Mcp.ConfigType != nil {
			configType := *current.Mcp.ConfigType
			storedConfigType = &configType
		}
		nextID, nextTarget, err := normalizeRawTargetInput(input, storedConfigType, storedPrefix, lock.Env())
		if err != nil {
			return err
		}

		if nextID != currentID {
			if _, exists := rawConfig.Targets[string(nextID)]; exists {
				return fmt.Errorf("target 已存在：%s", nextID)
			}
		}

		delete(rawConfig.Targets, string(currentID))
		rawConfig.Targets[string(nextID)] = nextTarget

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceTargetMutationResult{
			TargetID:     nextID,
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

func DeleteWorkspaceTargetInner(store *WorkspaceConfigStore, targetID string) (*WorkspaceTargetMutationResult, error) {
	id, err := normalizeTargetID(targetID)
	if err != nil {
		return nil, err
	}

	var result *WorkspaceTargetMutationResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		if _, exists := rawConfig.Targets[string(id)]; !exists {
			return fmt.Errorf("target 不存在：%s", id)
		}
		delete(rawConfig.Targets, string(id))

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceTargetMutationResult{
			TargetID:     id,
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

func CreateWorkspaceProjectInner(store *WorkspaceConfigStore, input ProjectMutationInput) (*WorkspaceTargetMutationResult, error) {
	var result *WorkspaceTargetMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		projectID := strings.TrimSpace(input.ProjectID)
		if projectID == "" {
			return errors.New("project id 不能为空。")
		}
		if err := validateProjectID(projectID); err != nil {
			return err
		}
		if _, exists := rawConfig.Projects[projectID]; exists {
			return fmt.Errorf("project id 已存在：%s", projectID)
		}
		agentsMap := map[string]*RawProjectAgentConfig{}
		for _, agentName := range input.Agents {
			id, err := normalizeTargetID(agentName)
			if err != nil {
				return err
			}
			agentsMap[string(id)] = &RawProjectAgentConfig{}
		}
		rawConfig.Projects[projectID] = &RawProjectConfig{Path: input.Path, Agents: agentsMap}

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceTargetMutationResult{
			TargetID:     AgentTargetID(projectID),
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

func UpdateWorkspaceProjectInner(store *WorkspaceConfigStore, input ProjectMutationInput) (*WorkspaceTargetMutationResult, error) {
	var result *WorkspaceTargetMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		originalID := strings.TrimSpace(input.ProjectID)
		if _, exists := rawConfig.Projects[originalID]; !exists {
			return fmt.Errorf("project 不存在：%s", originalID)
		}
		agentsMap := map[string]*RawProjectAgentConfig{}
		for _, agentName := range input.Agents {
			id, err := normalizeTargetID(agentName)
			if err != nil {
				return err
			}
			agentsMap[string(id)] = &RawProjectAgentConfig{}
		}
		rawConfig.Projects[originalID] = &RawProjectConfig{Path: input.Path, Agents: agentsMap}

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceTargetMutationResult{
			TargetID:     AgentTargetID(originalID),
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

func DeleteWorkspaceProjectInner(store *WorkspaceConfigStore, projectID string) (*WorkspaceTargetMutationResult, error) {
	var result *WorkspaceTargetMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		id := strings.TrimSpace(projectID)
		if _, exists := rawConfig.Projects[id]; !exists {
			return fmt.Errorf("project 不存在：%s", id)
		}
		delete(rawConfig.Projects, id)

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		// 与 Rust 一致：删除项目走原始路径拼写，不经 display_path。
		result = &WorkspaceTargetMutationResult{
			TargetID:     AgentTargetID(id),
			UpdatedPaths: []string{lock.ConfigPath()},
		}
		return nil
	})
	return result, err
}

// normalizeRawTargetInput：target 入参的归一与成对校验。
func normalizeRawTargetInput(
	input RawTargetInput,
	// persistedConfigType：落盘的 config_type：create 传派生值，update 传存量
	// 原值。
	persistedConfigType *agents.McpConfigType,
	// persistedConfigPrefix：MCP 配置节点路径：create 走内置 defaults 派生，
	// update 走存量原值；客户端不再传，有效值由调用方按场景选好传入。
	persistedConfigPrefix *string,
	env *support.Environment,
) (AgentTargetID, *RawTargetConfig, error) {
	targetID, err := normalizeTargetID(input.TargetID)
	if err != nil {
		return "", nil, err
	}
	skillDir, err := normalizeTargetSkillDir(input.SkillDir)
	if err != nil {
		return "", nil, err
	}

	var normalizedConfigPath *string
	if input.ConfigPath != nil && strings.TrimSpace(*input.ConfigPath) != "" {
		trimmed := strings.TrimSpace(*input.ConfigPath)
		normalizedConfigPath = &trimmed
	}
	configPrefix := stringOrEmpty(persistedConfigPrefix)

	// MCP 配置文件和 configPrefix 必须成对出现：只有前缀没有路径无处可写，
	// 只有路径没有前缀无法定位写入节点。不需要 MCP 分发的 target 允许两者
	// 都为空，此时只做 skill 分发。dsh 按 name+serverName 定位条目（writer
	// 声明 prefix 不必填），允许「有路径 + 空 prefix」。校验用的有效类型与
	// 解析侧同口径：存量值优先，其次内置 defaults。
	effectiveConfigType := agents.McpConfigCommon
	if persistedConfigType != nil {
		effectiveConfigType = *persistedConfigType
	} else if defaults, ok := builtinTargetDefaultsMap(env)[targetID]; ok {
		effectiveConfigType = defaults.ConfigType
	}
	prefixRequired := mcpFormatWriterFor(effectiveConfigType).prefixRequired()
	switch {
	case normalizedConfigPath != nil && configPrefix == "" && prefixRequired:
		return "", nil, fmt.Errorf("target %s 的 MCP configPrefix 不能为空。", targetID)
	// 路径缺失时，只有声明 prefix 必填的格式（common/grok/opencode）才算
	// 异常；dsh 这类 prefix 不必填的格式允许单独无 MCP 配置（用户只做
	// skills 分发，没有 MCP 需要清理）。
	case normalizedConfigPath == nil && configPrefix != "" && prefixRequired:
		return "", nil, fmt.Errorf("target %s 填写了 configPrefix,必须同时提供 MCP 配置文件路径。", targetID)
	}

	var configTypeCopy *agents.McpConfigType
	if persistedConfigType != nil {
		configType := *persistedConfigType
		configTypeCopy = &configType
	}
	var prefixCopy *string
	if configPrefix != "" {
		prefix := configPrefix
		prefixCopy = &prefix
	}

	return targetID, &RawTargetConfig{
		SkillDir: skillDir,
		Mcp: RawTargetMcpConfig{
			ConfigPath:   normalizedConfigPath,
			ConfigPrefix: prefixCopy,
			ConfigType:   configTypeCopy,
		},
	}, nil
}

func normalizeTargetSkillDir(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("target skills 目录不能为空。")
	}
	return trimmed, nil
}

// builtinCreateConfigType：create 只允许为内置工具建 target，并从同一份
// defaults 派生 MCP 格式；存量自定义 target 不迁移，照常解析、渲染、编辑、
// 删除（update 不走此校验）。
func builtinCreateConfigType(targetID AgentTargetID, env *support.Environment) (agents.McpConfigType, error) {
	if defaults, ok := builtinTargetDefaultsMap(env)[targetID]; ok {
		return defaults.ConfigType, nil
	}
	var allowed []string
	for i := range agents.AgentSpecs {
		if agents.AgentSpecs[i].TargetID != "" {
			allowed = append(allowed, agents.AgentSpecs[i].TargetID)
		}
	}
	return "", fmt.Errorf("仅支持创建内置工具的 target：%s。", strings.Join(allowed, "、"))
}

// ---------------------------------------------------------------------------
// Target defaults（从 agents 清单派生）
// ---------------------------------------------------------------------------

// TargetDefaults：一个内置工具的落点 defaults。
type TargetDefaults struct {
	ID           AgentTargetID
	SkillDir     string
	ConfigPath   *string
	ConfigPrefix string
	ConfigType   agents.McpConfigType
}

// TargetPresetsInner：创建弹窗一次下发的内置工具预设（与 builtin defaults
// 同源、按 AgentSpecs 清单顺序）。
func TargetPresetsInner(env *support.Environment) []TargetPreset {
	defaults := builtinTargetDefaultsMap(env)
	presets := make([]TargetPreset, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		if spec.TargetID == "" {
			continue
		}
		item, ok := defaults[AgentTargetID(spec.TargetID)]
		if !ok {
			continue
		}
		preset := TargetPreset{
			TargetID:        item.ID,
			Label:           spec.Label,
			SkillDir:        support.DisplayPath(item.SkillDir),
			ConfigPath:      nil,
			MCPConfigPrefix: item.ConfigPrefix,
		}
		if item.ConfigPath != nil {
			path := support.DisplayPath(*item.ConfigPath)
			preset.ConfigPath = &path
		}
		presets = append(presets, preset)
	}
	return presets
}

// builtinTargetDefaults：全局 defaults 从 agents 清单派生：路径 = 环境解析
// 出的 GlobalRoot + spec 的相对布局；根目录解析失败（无 HOME）时 skill_dir
// 落空串、config_path 落 nil，与逐条手写时代的口径一致。
func builtinTargetDefaults(env *support.Environment) []TargetDefaults {
	defaults := make([]TargetDefaults, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		if spec.TargetID == "" {
			continue
		}
		// 落点解析只有一处：清单里的 `~/` 与 root 相对都由 GlobalRoot 判。
		resolve := func(path string) *string {
			resolved, ok := spec.Global.Root.ResolvePath(env, path)
			if !ok {
				return nil
			}
			return &resolved
		}
		item := TargetDefaults{
			ID:           AgentTargetID(spec.TargetID),
			SkillDir:     "",
			ConfigPath:   nil,
			ConfigPrefix: spec.Mcp.Prefix,
			ConfigType:   spec.Mcp.ConfigType,
		}
		if resolved := resolve(spec.Global.SkillDir); resolved != nil {
			item.SkillDir = *resolved
		}
		if spec.Global.McpConfigPath != "" {
			item.ConfigPath = resolve(spec.Global.McpConfigPath)
		}
		defaults = append(defaults, item)
	}
	return defaults
}

func builtinTargetDefaultsMap(env *support.Environment) map[AgentTargetID]TargetDefaults {
	items := builtinTargetDefaults(env)
	byID := make(map[AgentTargetID]TargetDefaults, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	return byID
}

// projectAgentDefaults：项目级 defaults 同样从 agents 清单派生；项目与全局
// 非同构（spec 的 project 布局如实表达差异，如 dsh 项目级无 MCP 配置入口）。
func projectAgentDefaults(projectPath string) []TargetDefaults {
	defaults := make([]TargetDefaults, 0, len(agents.AgentSpecs))
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		if spec.TargetID == "" || spec.Project == nil {
			continue
		}
		item := TargetDefaults{
			ID:           AgentTargetID(spec.TargetID),
			SkillDir:     joinPath(projectPath, spec.Project.SkillDir),
			ConfigPath:   nil,
			ConfigPrefix: spec.Mcp.Prefix,
			ConfigType:   spec.Mcp.ConfigType,
		}
		if spec.Project.McpConfigPath != "" {
			path := joinPath(projectPath, spec.Project.McpConfigPath)
			item.ConfigPath = &path
		}
		defaults = append(defaults, item)
	}
	return defaults
}
