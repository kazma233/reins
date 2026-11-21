// mcps.go：MCP 域编排——config.yaml 的 MCP server CRUD + 跨格式的 per-target
// MCP 配置写入（JSON / TOML / dsh Cordis patch YAML）。各 config type 的条目
// 形态与约束在 mcp_formats.go；本文件持有文件解析、前缀导航与 JSON/TOML 引擎。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"reins/internal/support"
)

// ---------------------------------------------------------------------------
// MCP apply / preview / remove（config-path 写入目标）
// ---------------------------------------------------------------------------

// ApplyMCPToTargetInner：apply_mcp_to_target 命令核心。
func ApplyMCPToTargetInner(store *WorkspaceConfigStore, serverName string, targetID string) (*McpTargetMutationResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	serverName, err = normalizeMCPName(serverName)
	if err != nil {
		return nil, err
	}
	server := findResolvedMcp(config, serverName)
	if server == nil {
		return nil, fmt.Errorf("MCP not found: %s", serverName)
	}
	target := config.targetByID(targetID)
	if target == nil {
		return nil, targetUnavailable(targetID)
	}

	// 落点缺失由 applyMCPToTargetConfig 统一判定（那里是「没有落点」口径的
	// 唯一定义处），这里不再复述一遍。
	updatedPath, err := applyMCPToTargetConfig(target, server)
	if err != nil {
		return nil, err
	}

	displayed := support.DisplayPath(updatedPath)
	return &McpTargetMutationResult{
		ServerName:  serverName,
		TargetID:    AgentTargetID(targetID),
		UpdatedPath: &displayed,
		Action:      "apply",
		Detail:      fmt.Sprintf("已写入 %s", support.DisplayPath(updatedPath)),
	}, nil
}

// PreviewMCPTargetInner：preview_mcp_target 命令核心。
func PreviewMCPTargetInner(store *WorkspaceConfigStore, serverName string, targetID string) (*McpTargetPreviewResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	serverName, err = normalizeMCPName(serverName)
	if err != nil {
		return nil, err
	}
	server := findResolvedMcp(config, serverName)
	if server == nil {
		return nil, fmt.Errorf("MCP not found: %s", serverName)
	}
	target := config.targetByID(targetID)
	if target == nil {
		return nil, targetUnavailable(targetID)
	}

	if target.ConfigPath == nil {
		// 这里要的不只是「检查」：结果里要回传落点，所以得先拿到它。文案与
		// previewMCPEntry 保持一致。
		return nil, fmt.Errorf("目标 %s 没有 MCP 配置路径", targetID)
	}

	format, content, err := previewMCPEntry(target, server)
	if err != nil {
		return nil, err
	}

	return &McpTargetPreviewResult{
		ServerName: serverName,
		TargetID:   AgentTargetID(targetID),
		ConfigPath: support.DisplayPath(*target.ConfigPath),
		Format:     format,
		Content:    content,
	}, nil
}

// RemoveMCPFromTargetInner：remove_mcp_from_target 命令核心。
func RemoveMCPFromTargetInner(store *WorkspaceConfigStore, serverName string, targetID string) (*McpTargetMutationResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	serverName, err = normalizeMCPName(serverName)
	if err != nil {
		return nil, err
	}
	target := config.targetByID(targetID)
	if target == nil {
		return nil, targetUnavailable(targetID)
	}

	mutation, err := removeMCPFromTargetConfig(target, serverName)
	if err != nil {
		return nil, err
	}
	return &mutation, nil
}

// ---------------------------------------------------------------------------
// MCP 配置文件读写（格式无关：JSON / TOML / OpenCode / dsh）
// ---------------------------------------------------------------------------

// ReadExistingMCPEntries：读既有条目（key 为 server 名；dsh 按清洗后的
// serverName）。prefix 的内置回落在这里完成，writer 只认生效前缀。导出供
// 测试（对齐 Rust pub(super)）。
func ReadExistingMCPEntries(target *ResolvedTargetConfig, configPath string, env *support.Environment) (map[string]any, error) {
	prefix := effectivePrefix(target.MCPConfigPrefix, &target.ID, env)
	return mcpFormatWriterFor(target.MCPConfigType).readExistingEntries(prefix, configPath)
}

func previewMCPEntry(target *ResolvedTargetConfig, server *ResolvedMcpConfig) (string, string, error) {
	// 预览和写入必须拒绝没有落点的 target（地址都写不出去），删除相反：见
	// removeMCPFromTargetConfig 的 nil 分支。
	if target.ConfigPath == nil {
		return "", "", fmt.Errorf("目标 %s 没有 MCP 配置路径", target.ID)
	}
	return mcpFormatWriterFor(target.MCPConfigType).previewEntry(*target.ConfigPath, server)
}

func applyMCPToTargetConfig(target *ResolvedTargetConfig, server *ResolvedMcpConfig) (string, error) {
	if target.ConfigPath == nil {
		return "", fmt.Errorf("目标 %s 没有 MCP 配置路径", target.ID)
	}
	if err := mcpFormatWriterFor(target.MCPConfigType).applyEntry(target, *target.ConfigPath, server); err != nil {
		return "", err
	}
	return *target.ConfigPath, nil
}

// 「没有落点」两种 None 行为是刻意的，不是不一致：写入与预览必须报错（没有
// 地址就写不出去），删除必须跳过（它在遍历全部 target 的循环里被调用，报错
// 会中断整轮清理）。改口径只改这一处。
func removeMCPFromTargetConfig(target *ResolvedTargetConfig, serverName string) (McpTargetMutationResult, error) {
	// 未配置 MCP 的 target 无配置可清理；必须返回 noop 而非报错。
	// 否则删除 MCP 时遍历全部 target 会被这类 target 中断。
	if target.ConfigPath == nil {
		return McpTargetMutationResult{
			ServerName:  serverName,
			TargetID:    target.ID,
			UpdatedPath: nil,
			Action:      "noop",
			Detail:      "目标未配置 MCP，跳过。",
		}, nil
	}

	return mcpFormatWriterFor(target.MCPConfigType).removeEntry(target, *target.ConfigPath, serverName)
}

func findResolvedMcp(config *ResolvedManagerConfig, name string) *ResolvedMcpConfig {
	for _, mcp := range config.Mcps {
		if mcp.Name == name {
			return mcp
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// JSON/TOML 引擎：writer 提供格式知识与约束，这里只做解析、前缀定位与写入
// ---------------------------------------------------------------------------

// readFileEntries：读既有条目。前缀段不存在时读空表（与 Rust 一致不报错）。
func readFileEntries(writer mcpFormatWriter, prefix string, configPath string) (map[string]any, error) {
	format, err := detectSupportedFormat(writer, configPath)
	if err != nil {
		return nil, err
	}
	switch format {
	case mcpFormatTOML:
		root, err := readTOMLConfig(configPath)
		if err != nil {
			return nil, err
		}
		table := getTOMLTablePath(root, prefix)
		if table == nil {
			return map[string]any{}, nil
		}
		entries := make(map[string]any, len(table))
		for name, value := range table {
			entries[name] = value
		}
		return entries, nil
	default:
		root, err := readJSONConfig(configPath)
		if err != nil {
			return nil, err
		}
		table := getJSONObjectPath(root, prefix)
		if table == nil {
			return map[string]any{}, nil
		}
		entries := make(map[string]any, len(table))
		for name, value := range table {
			entries[name] = value
		}
		return entries, nil
	}
}

// previewFileEntry：预览条目内容（格式标签 + 序列化文本），不落盘。预览先
// 确认文件可解析再展示条目（dsh preview 的同款契约）：Rust 版这一步由格式
// 判定的内容探测顺带完成，后缀判定化（决策记录
// 2026-11-19-mcp-format-detection-by-extension.md）后显式读一次——预览展示的
// 就是将要写入的，写不进的文件不该给出预览。
func previewFileEntry(writer mcpFormatWriter, configPath string, server *ResolvedMcpConfig) (string, string, error) {
	format, err := detectSupportedFormat(writer, configPath)
	if err != nil {
		return "", "", err
	}
	switch format {
	case mcpFormatTOML:
		if _, err := readTOMLConfig(configPath); err != nil {
			return "", "", err
		}
		entry, err := writer.desiredTomlEntry(server)
		if err != nil {
			return "", "", err
		}
		content, err := marshalTOML(entry)
		if err != nil {
			return "", "", fmt.Errorf("MCP 预览序列化失败：%v", err)
		}
		return "toml", content, nil
	default:
		if _, err := readJSONConfig(configPath); err != nil {
			return "", "", err
		}
		entry, err := writer.desiredJSONEntry(server)
		if err != nil {
			return "", "", err
		}
		content, err := marshalJSONPretty(entry)
		if err != nil {
			return "", "", fmt.Errorf("MCP 预览序列化失败：%v", err)
		}
		return "json", content, nil
	}
}

// applyFileEntry：应用条目到目标配置；幂等（重复应用内容不变）。
func applyFileEntry(writer mcpFormatWriter, target *ResolvedTargetConfig, configPath string, server *ResolvedMcpConfig) error {
	format, err := detectSupportedFormat(writer, configPath)
	if err != nil {
		return err
	}
	switch format {
	case mcpFormatTOML:
		root, err := readTOMLConfig(configPath)
		if err != nil {
			return err
		}
		serverMap, err := ensureTOMLTablePath(root, target.MCPConfigPrefix)
		if err != nil {
			return err
		}
		entry, err := writer.desiredTomlEntry(server)
		if err != nil {
			return err
		}
		serverMap[server.Name] = entry
		return writeTOMLConfig(configPath, root)
	default:
		root, err := readJSONConfig(configPath)
		if err != nil {
			return err
		}
		if root == nil {
			defaultRoot, ok := writer.defaultJSONRoot().(map[string]any)
			if !ok {
				return fmt.Errorf("JSON 配置根节点必须是 object：%s", configPath)
			}
			root = defaultRoot
		}
		serverMap, err := ensureJSONObjectPath(root, target.MCPConfigPrefix)
		if err != nil {
			return err
		}
		entry, err := writer.desiredJSONEntry(server)
		if err != nil {
			return err
		}
		serverMap[server.Name] = entry
		return writeJSONConfig(configPath, root)
	}
}

// removeFileEntry：移除条目；条目不存在时返回 action 为 noop 的结果而非报错。
func removeFileEntry(writer mcpFormatWriter, target *ResolvedTargetConfig, configPath string, serverName string) (McpTargetMutationResult, error) {
	noop := McpTargetMutationResult{
		ServerName:  serverName,
		TargetID:    target.ID,
		UpdatedPath: nil,
		Action:      "noop",
		Detail:      "目标配置里不存在该 MCP。",
	}

	format, err := detectSupportedFormat(writer, configPath)
	if err != nil {
		return McpTargetMutationResult{}, err
	}
	var removed bool
	switch format {
	case mcpFormatTOML:
		root, err := readTOMLConfig(configPath)
		if err != nil {
			return McpTargetMutationResult{}, err
		}
		table := getTOMLTablePath(root, target.MCPConfigPrefix)
		if table != nil {
			if _, exists := table[serverName]; exists {
				delete(table, serverName)
				if err := writeTOMLConfig(configPath, root); err != nil {
					return McpTargetMutationResult{}, err
				}
				removed = true
			}
		}
	default:
		root, err := readJSONConfig(configPath)
		if err != nil {
			return McpTargetMutationResult{}, err
		}
		table := getJSONObjectPath(root, target.MCPConfigPrefix)
		if table != nil {
			if _, exists := table[serverName]; exists {
				delete(table, serverName)
				if err := writeJSONConfig(configPath, root); err != nil {
					return McpTargetMutationResult{}, err
				}
				removed = true
			}
		}
	}

	if !removed {
		return noop, nil
	}

	updatedPath := support.DisplayPath(configPath)
	return McpTargetMutationResult{
		ServerName:  serverName,
		TargetID:    target.ID,
		UpdatedPath: &updatedPath,
		Action:      "remove",
		Detail:      fmt.Sprintf("已从 %s 移除 %s", target.ID, serverName),
	}, nil
}

func detectSupportedFormat(writer mcpFormatWriter, configPath string) (mcpConfigFileFormat, error) {
	format, err := detectMCPFileFormat(configPath)
	if err != nil {
		return 0, err
	}
	if err := writer.ensureFormatSupported(format); err != nil {
		return 0, err
	}
	return format, nil
}

// detectMCPFileFormat：MCP 配置文件格式判定，只按扩展名（.json → JSON、
// .toml → TOML、其余报错）。这是 Go 迁移的已拍板行为变更，取代 Rust
// detect_mcp_file_format 的两段式判定（文件存在且非空时先按 JSON5/TOML 试
// 解析、否则按后缀推断）——格式归属文件所有者，不是 Reins 该猜的，不再做
// 内容探测。见 docs/decisions/2026-11-19-mcp-format-detection-by-extension.md。
func detectMCPFileFormat(configPath string) (mcpConfigFileFormat, error) {
	switch filePathExtension(configPath) {
	case "json":
		return mcpFormatJSON, nil
	case "toml":
		return mcpFormatTOML, nil
	}
	return 0, fmt.Errorf("无法从文件名推断 MCP 配置格式：%s，仅支持 json 与 toml", configPath)
}

// effectivePrefix：读取侧的生效前缀：target 没配 prefix 时按 id 回落内置布局
// （旧配置兼容路径）。回落不出的非内置 id 按空前缀继续读——读空表、不猜测、
// 不报错（有专门用例钉住）。
func effectivePrefix(prefix string, targetID *AgentTargetID, env *support.Environment) string {
	if strings.TrimSpace(prefix) != "" {
		return prefix
	}
	if targetID == nil {
		return prefix
	}
	if defaults, ok := builtinTargetDefaultsMap(env)[*targetID]; ok {
		return defaults.ConfigPrefix
	}
	return prefix
}

// ---------------------------------------------------------------------------
// 配置文件原语（各文件格式 writer 共用）
// ---------------------------------------------------------------------------

// readTOMLConfig：读 TOML 根表。文件不存在或空白内容返回空表。
func readTOMLConfig(configPath string) (map[string]any, error) {
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]any{}, nil
	}

	var root map[string]any
	if _, err := toml.Decode(string(raw), &root); err != nil {
		return nil, fmt.Errorf("TOML 配置解析失败：%s：%v", configPath, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

func writeTOMLConfig(configPath string, root map[string]any) error {
	serialized, err := marshalTOML(root)
	if err != nil {
		return fmt.Errorf("TOML 配置序列化失败：%s：%v", configPath, err)
	}
	if err := support.WriteAtomic(configPath, serialized); err != nil {
		return fmt.Errorf("写入 TOML 配置失败：%s", configPath)
	}
	return nil
}

// readJSONConfig：读 JSON 根对象。文件不存在或空白内容返回空对象；顶层不是
// object 报错。读侧容忍注释与尾逗号（JSONC）：Rust 用 json5 全量解析；Go 沿
// providers 域 read_jsonc_object 的最小预处理口径（剥注释/尾逗号），再交
// support.DecodeJSONStrict（拒绝尾随，对齐 serde_json::from_str），写仍是
// 整文件重写为纯 JSON——见决策记录 2026-11-19-mcp-format-detection-by-extension.md
// 的「读写不对称不变」。数字用 json.Number 保精度（serde_json 同样不丢）。
func readJSONConfig(configPath string) (map[string]any, error) {
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return map[string]any{}, nil
	}

	stripped, err := support.StripJSONCCommentsAndTrailingCommas(raw)
	if err != nil {
		return nil, fmt.Errorf("JSON 配置解析失败：%s", configPath)
	}
	parsed, err := support.DecodeJSONStrict(string(stripped))
	if err != nil {
		return nil, fmt.Errorf("JSON 配置解析失败：%s", configPath)
	}

	object, ok := parsed.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON 配置根节点必须是 object：%s", configPath)
	}
	return object, nil
}

func writeJSONConfig(configPath string, root map[string]any) error {
	serialized, err := marshalJSONPretty(root)
	if err != nil {
		return fmt.Errorf("JSON 配置序列化失败：%s：%v", configPath, err)
	}
	if err := support.WriteAtomic(configPath, serialized); err != nil {
		return fmt.Errorf("写入 JSON 配置失败：%s", configPath)
	}
	return nil
}

// ensureJSONObjectPath：按点分 prefix 逐段下钻，缺失的中间层补空对象；返回
// 最终的条目表（原地修改 root）。
func ensureJSONObjectPath(root map[string]any, path string) (map[string]any, error) {
	keys, err := splitConfigPath(path)
	if err != nil {
		return nil, err
	}
	current := root
	for _, key := range keys {
		entry, exists := current[key]
		if !exists || entry == nil {
			entry = map[string]any{}
			current[key] = entry
		}
		next, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s 必须是 object。", path)
		}
		current = next
	}
	return current, nil
}

// getJSONObjectPath：只读下钻；任何一段不是 object 或缺失返回 nil。
func getJSONObjectPath(root map[string]any, path string) map[string]any {
	keys, err := splitConfigPath(path)
	if err != nil {
		return nil
	}
	current := root
	for _, key := range keys {
		entry, exists := current[key]
		if !exists {
			return nil
		}
		next, ok := entry.(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func ensureTOMLTablePath(root map[string]any, path string) (map[string]any, error) {
	keys, err := splitConfigPath(path)
	if err != nil {
		return nil, err
	}
	current := root
	for _, key := range keys {
		entry, exists := current[key]
		if !exists || entry == nil {
			entry = map[string]any{}
			current[key] = entry
		}
		next, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s 对应的 TOML 节点必须是 table。", path)
		}
		current = next
	}
	return current, nil
}

func getTOMLTablePath(root map[string]any, path string) map[string]any {
	return getJSONObjectPath(root, path)
}

// splitConfigPath：点分前缀拆段；空白段过滤，全空报错。
func splitConfigPath(path string) ([]string, error) {
	var keys []string
	for _, segment := range strings.Split(path, ".") {
		if trimmed := strings.TrimSpace(segment); trimmed != "" {
			keys = append(keys, trimmed)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("configPrefix 不能为空。")
	}
	return keys, nil
}

// ---------------------------------------------------------------------------
// config.yaml 的 MCP server CRUD
// ---------------------------------------------------------------------------

// CreateWorkspaceMCPInner：create_workspace_mcp 命令核心。CreatedAt 由
// service 层用 currentTimestampMs 填（与 Rust commands.rs 同层职责）。
func CreateWorkspaceMCPInner(store *WorkspaceConfigStore, rawMcp RawMcpConfig) (*WorkspaceMcpMutationResult, error) {
	var result *WorkspaceMcpMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		serverName, err := normalizeMCPName(rawMcp.Name)
		if err != nil {
			return err
		}
		nextMcp, err := normalizeRawMcp(rawMcp, serverName)
		if err != nil {
			return err
		}

		for _, mcp := range rawConfig.Mcps {
			if mcp.Name == serverName {
				return fmt.Errorf("mcp name 已存在：%s", serverName)
			}
		}

		rawConfig.Mcps = append(rawConfig.Mcps, nextMcp)

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceMcpMutationResult{
			ServerName:   serverName,
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

// UpdateWorkspaceMCPInner：update_workspace_mcp 命令核心（created_at 保留
// 存量值）。
func UpdateWorkspaceMCPInner(store *WorkspaceConfigStore, serverName string, rawMcp RawMcpConfig) (*WorkspaceMcpMutationResult, error) {
	serverName, err := normalizeMCPName(serverName)
	if err != nil {
		return nil, err
	}

	var result *WorkspaceMcpMutationResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		existingIndex := -1
		for index, mcp := range rawConfig.Mcps {
			if mcp.Name == serverName {
				existingIndex = index
				break
			}
		}
		if existingIndex < 0 {
			return fmt.Errorf("MCP not found: %s", serverName)
		}
		createdAt := rawConfig.Mcps[existingIndex].CreatedAt
		nextServerName, err := normalizeMCPName(rawMcp.Name)
		if err != nil {
			return err
		}

		if nextServerName != serverName {
			for _, mcp := range rawConfig.Mcps {
				if mcp.Name == nextServerName {
					return fmt.Errorf("mcp name 已存在：%s", nextServerName)
				}
			}
		}

		rawMcp.CreatedAt = createdAt
		nextMcp, err := normalizeRawMcp(rawMcp, nextServerName)
		if err != nil {
			return err
		}

		rawConfig.Mcps[existingIndex] = nextMcp

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		path := support.DisplayPath(lock.ConfigPath())
		result = &WorkspaceMcpMutationResult{
			ServerName:   nextServerName,
			UpdatedPaths: []string{path},
		}
		return nil
	})
	return result, err
}

// DeleteWorkspaceMCPInner：delete_workspace_mcp 命令核心。整段在锁内：
// manager config 的变更与各 target 的清理派生自同一份配置快照。
func DeleteWorkspaceMCPInner(store *WorkspaceConfigStore, serverName string) (*WorkspaceMcpMutationResult, error) {
	var result *WorkspaceMcpMutationResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		var rawConfig RawManagerConfig
		if err := unmarshalRawManagerConfig(rawContent, &rawConfig); err != nil {
			return err
		}
		resolvedConfig, err := parseManagerConfig(rawContent, lock.ConfigPath(), lock.Env())
		if err != nil {
			return err
		}
		serverName, err := normalizeMCPName(serverName)
		if err != nil {
			return err
		}
		updatedPaths := []string{support.DisplayPath(lock.ConfigPath())}

		rawFound := false
		for _, mcp := range rawConfig.Mcps {
			if mcp.Name == serverName {
				rawFound = true
				break
			}
		}
		if !rawFound {
			return fmt.Errorf("MCP not found: %s", serverName)
		}
		resolved := findResolvedMcp(resolvedConfig, serverName)
		if resolved == nil {
			return fmt.Errorf("MCP not found: %s", serverName)
		}

		kept := rawConfig.Mcps[:0]
		for _, mcp := range rawConfig.Mcps {
			if mcp.Name != serverName {
				kept = append(kept, mcp)
			}
		}
		rawConfig.Mcps = kept
		if err := lock.WriteRaw(&rawConfig); err != nil {
			return err
		}

		// 「全部 target」的定义在寻址层：全局在前、项目按配置顺序。
		for _, entry := range resolvedConfig.targetRefs() {
			mutation, err := removeMCPFromTargetConfig(entry.target, resolved.Name)
			if err != nil {
				return err
			}
			if mutation.UpdatedPath != nil {
				updatedPaths = append(updatedPaths, *mutation.UpdatedPath)
			}
		}

		sort.Strings(updatedPaths)
		updatedPaths = dedupStrings(updatedPaths)

		result = &WorkspaceMcpMutationResult{
			ServerName:   serverName,
			UpdatedPaths: updatedPaths,
		}
		return nil
	})
	return result, err
}

// unmarshalRawManagerConfig：raw 文档反序列化（含 serde(default) 的 nil 集合
// 归一；未知键容忍——旧配置里的 enabled 键靠它被自然忽略）。
func unmarshalRawManagerConfig(rawContent string, config *RawManagerConfig) error {
	if err := yaml.Unmarshal([]byte(rawContent), config); err != nil {
		return err
	}
	normalizeRawManagerConfig(config)
	return nil
}

// normalizeMCPName：trim 后非空。
func normalizeMCPName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("mcp name 不能为空。")
	}
	return trimmed, nil
}

// normalizeRawMcp：MCP 入参的归一与成对校验（trim、滤空、transport 校验）。
func normalizeRawMcp(rawMcp RawMcpConfig, name string) (*RawMcpConfig, error) {
	next := &RawMcpConfig{
		Name:      name,
		Transport: rawMcp.Transport,
		CreatedAt: rawMcp.CreatedAt,
		Homepage:  trimNonEmpty(rawMcp.Homepage),
		Command:   trimNonEmpty(rawMcp.Command),
		Args:      normalizePatterns(rawMcp.Args),
		Env:       trimStringMapKeys(rawMcp.Env),
		URL:       trimNonEmpty(rawMcp.URL),
		Headers:   trimStringMapKeys(rawMcp.Headers),
		Timeout:   rawMcp.Timeout,
	}

	switch next.Transport {
	case TransportStdio:
		if strings.TrimSpace(stringOrEmpty(next.Command)) == "" {
			return nil, errors.New("stdio MCP 必须提供 command。")
		}
	case TransportHTTP, TransportSSE:
		if strings.TrimSpace(stringOrEmpty(next.URL)) == "" {
			return nil, errors.New("远程 MCP 必须提供 mcp 链接。")
		}
	default:
		return nil, fmt.Errorf("未知 transport：%s", next.Transport)
	}

	return next, nil
}

// trimNonEmpty：trim 后为空按未填（None）处理。
func trimNonEmpty(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// trimStringMapKeys：键 trim 后为空的条目丢弃；值不 trim（Rust 同口径）。
func trimStringMapKeys(values map[string]string) map[string]string {
	normalized := make(map[string]string, len(values))
	for key, value := range values {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			continue
		}
		normalized[trimmed] = value
	}
	return normalized
}

// dedupStrings：已排序切片去重（Rust sort + dedup 的组合效果）。
func dedupStrings(values []string) []string {
	kept := values[:0]
	for index, value := range values {
		if index == 0 || values[index-1] != value {
			kept = append(kept, value)
		}
	}
	return kept
}
