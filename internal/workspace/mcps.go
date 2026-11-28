// mcps.go：MCP 域编排——config.yaml 的 MCP server CRUD + 跨格式的 per-path
// MCP 配置写入（JSON / TOML / dsh Cordis patch YAML / hermes YAML）。各
// config type 的条目形态与约束在 mcp_formats.go；本文件持有文件解析、
// 前缀导航与 JSON/TOML 引擎。
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
// 「一个 server × 一条路径」的格子操作（矩阵同步逐格执行与测试共用）
// ---------------------------------------------------------------------------

// resolveMcpCell：格子寻址（登记 server 名 + 路径条目名 → 解析形态）。
func resolveMcpCell(config *ResolvedManagerConfig, serverName string, pathName string) (*ResolvedMcpConfig, *ResolvedMcpPath, error) {
	server := findResolvedMcp(config, serverName)
	if server == nil {
		return nil, nil, fmt.Errorf("MCP not found: %s", serverName)
	}
	path := config.mcpPathByName(pathName)
	if path == nil {
		return nil, nil, mcpPathUnavailable(pathName)
	}
	return server, path, nil
}

// applyMCPCell：把登记 server 写进一条 MCP 路径（幂等，重复应用内容不变）。
func applyMCPCell(config *ResolvedManagerConfig, serverName string, pathName string) (McpPathMutationResult, error) {
	server, path, err := resolveMcpCell(config, serverName, pathName)
	if err != nil {
		return McpPathMutationResult{}, err
	}
	if err := applyMCPToPathConfig(path, server); err != nil {
		return McpPathMutationResult{}, err
	}
	displayed := support.DisplayPath(path.ConfigPath)
	return McpPathMutationResult{
		ServerName:  serverName,
		PathID:      pathName,
		UpdatedPath: &displayed,
		Action:      "apply",
		Detail:      fmt.Sprintf("已写入 %s", displayed),
	}, nil
}

// removeMCPCell：从一条 MCP 路径移除登记 server 的条目；条目不存在时返回
// action 为 noop 的结果而非报错。
func removeMCPCell(config *ResolvedManagerConfig, serverName string, pathName string) (McpPathMutationResult, error) {
	_, path, err := resolveMcpCell(config, serverName, pathName)
	if err != nil {
		return McpPathMutationResult{}, err
	}
	return removeMCPFromPathConfig(path, serverName)
}

// mcpCellPreview：单格条目预览——预览展示的就是将要写入的。
type mcpCellPreview struct {
	ConfigPath string
	Format     string
	Content    string
}

// previewMCPCell：条目预览（聚合预演的创建项构造与测试共用）。「写不进的
// 文件不该给出预览」由 writer.previewEntry 保证：先确认文件可解析再序列化
// desired 条目。
func previewMCPCell(config *ResolvedManagerConfig, serverName string, pathName string) (*mcpCellPreview, error) {
	server, path, err := resolveMcpCell(config, serverName, pathName)
	if err != nil {
		return nil, err
	}
	format, content, err := mcpFormatWriterFor(path.MCPConfigType).previewEntry(path.ConfigPath, server)
	if err != nil {
		return nil, err
	}
	return &mcpCellPreview{
		ConfigPath: support.DisplayPath(path.ConfigPath),
		Format:     format,
		Content:    content,
	}, nil
}

// ---------------------------------------------------------------------------
// MCP 配置文件读写（格式无关：JSON / TOML / OpenCode / dsh）
// ---------------------------------------------------------------------------

// ReadExistingMCPEntries：读既有条目（key 为 server 名；dsh 按清洗后的
// serverName）。导出供测试（对齐 Rust pub(super)）。
func ReadExistingMCPEntries(path *ResolvedMcpPath, configPath string) (map[string]any, error) {
	return mcpFormatWriterFor(path.MCPConfigType).readExistingEntries(path.MCPConfigPrefix, configPath)
}

func applyMCPToPathConfig(path *ResolvedMcpPath, server *ResolvedMcpConfig) error {
	return mcpFormatWriterFor(path.MCPConfigType).applyEntry(path, path.ConfigPath, server)
}

func removeMCPFromPathConfig(path *ResolvedMcpPath, serverName string) (McpPathMutationResult, error) {
	return mcpFormatWriterFor(path.MCPConfigType).removeEntry(path, path.ConfigPath, serverName)
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
	case mcpFormatYAML:
		// YAML 载体走 yaml.Node 手术读写（保留注释与无关键），不走 map
		// 往返——整文件重排会丢用户的注释与键序。
		table, err := getYAMLMappingPathByDoc(configPath, prefix)
		if err != nil {
			return nil, err
		}
		entries := map[string]any{}
		if table == nil {
			return entries, nil
		}
		for i := 0; i+1 < len(table.Content); i += 2 {
			name, ok := support.NodeAsStr(table.Content[i])
			if !ok {
				continue
			}
			entries[name] = yamlNodeToJSONValue(support.Untag(table.Content[i+1]))
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
	case mcpFormatYAML:
		// 与 JSON/TOML 一致：preview 先确认文件可解析（根必须是映射；缺失
		// 的前缀层由 apply 创建，不构成预览障碍）。内容是条目本体，写入
		// 位置由 configPrefix 说明交代。
		if _, err := readYAMLMappingRoot(configPath); err != nil {
			return "", "", err
		}
		entryNode, err := writer.desiredYAMLEntry(server)
		if err != nil {
			return "", "", err
		}
		content, err := support.MarshalYAMLNode(entryNode)
		if err != nil {
			return "", "", fmt.Errorf("MCP 预览序列化失败：%v", err)
		}
		return "yaml", content, nil
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
func applyFileEntry(writer mcpFormatWriter, path *ResolvedMcpPath, configPath string, server *ResolvedMcpConfig) error {
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
		serverMap, err := ensureTOMLTablePath(root, path.MCPConfigPrefix)
		if err != nil {
			return err
		}
		entry, err := writer.desiredTomlEntry(server)
		if err != nil {
			return err
		}
		serverMap[server.Name] = entry
		return writeTOMLConfig(configPath, root)
	case mcpFormatYAML:
		// YAML 载体：根（含前缀逐层）在 yaml.Node 上导航/创建，条目外内容
		// 与注释原样保留；文件不存在或为空时从零构造。
		root, err := readYAMLMappingRoot(configPath)
		if err != nil {
			return err
		}
		serverMap, err := ensureYAMLMappingPath(root, path.MCPConfigPrefix)
		if err != nil {
			return err
		}
		entry, err := writer.desiredYAMLEntry(server)
		if err != nil {
			return err
		}
		support.NodeSet(serverMap, server.Name, entry)
		return support.WriteYAMLDocument(configPath, root)
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
		serverMap, err := ensureJSONObjectPath(root, path.MCPConfigPrefix)
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
func removeFileEntry(writer mcpFormatWriter, path *ResolvedMcpPath, configPath string, serverName string) (McpPathMutationResult, error) {
	noop := McpPathMutationResult{
		ServerName:  serverName,
		PathID:      path.Name,
		UpdatedPath: nil,
		Action:      "noop",
		Detail:      "目标配置里不存在该 MCP。",
	}

	format, err := detectSupportedFormat(writer, configPath)
	if err != nil {
		return McpPathMutationResult{}, err
	}
	var removed bool
	switch format {
	case mcpFormatYAML:
		root, err := readYAMLMappingRoot(configPath)
		if err != nil {
			return McpPathMutationResult{}, err
		}
		table := getYAMLMappingPath(root, path.MCPConfigPrefix)
		if table != nil {
			if support.NodeRemove(table, serverName) {
				if err := support.WriteYAMLDocument(configPath, root); err != nil {
					return McpPathMutationResult{}, err
				}
				removed = true
			}
		}
	case mcpFormatTOML:
		root, err := readTOMLConfig(configPath)
		if err != nil {
			return McpPathMutationResult{}, err
		}
		table := getTOMLTablePath(root, path.MCPConfigPrefix)
		if table != nil {
			if _, exists := table[serverName]; exists {
				delete(table, serverName)
				if err := writeTOMLConfig(configPath, root); err != nil {
					return McpPathMutationResult{}, err
				}
				removed = true
			}
		}
	default:
		root, err := readJSONConfig(configPath)
		if err != nil {
			return McpPathMutationResult{}, err
		}
		table := getJSONObjectPath(root, path.MCPConfigPrefix)
		if table != nil {
			if _, exists := table[serverName]; exists {
				delete(table, serverName)
				if err := writeJSONConfig(configPath, root); err != nil {
					return McpPathMutationResult{}, err
				}
				removed = true
			}
		}
	}

	if !removed {
		return noop, nil
	}

	updatedPath := support.DisplayPath(configPath)
	return McpPathMutationResult{
		ServerName:  serverName,
		PathID:      path.Name,
		UpdatedPath: &updatedPath,
		Action:      "remove",
		Detail:      fmt.Sprintf("已从 %s 移除 %s", path.Name, serverName),
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

// readYAMLMappingRoot：读 YAML 配置根；文件不存在/空白/根为 null 时返回
// 空映射节点（写入从零构造），根不是映射报错。
func readYAMLMappingRoot(configPath string) (*yaml.Node, error) {
	root, err := support.ReadYAMLDocument(configPath)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return support.YMap(), nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("YAML 配置根节点必须是映射：%s", configPath)
	}
	return root, nil
}

// getYAMLMappingPath：只读下钻点分前缀，任何一段缺失或不是映射返回 nil。
func getYAMLMappingPath(root *yaml.Node, path string) *yaml.Node {
	current := root
	for _, key := range strings.Split(path, ".") {
		if strings.TrimSpace(key) == "" {
			continue
		}
		if current == nil || current.Kind != yaml.MappingNode {
			return nil
		}
		child := support.NodeGet(current, key)
		if child == nil || child.Kind != yaml.MappingNode {
			return nil
		}
		current = child
	}
	return current
}

// getYAMLMappingPathByDoc：从文件出发的只读下钻（文件不存在/空白返回 nil）。
func getYAMLMappingPathByDoc(configPath string, prefix string) (*yaml.Node, error) {
	root, err := support.ReadYAMLDocument(configPath)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, nil
	}
	return getYAMLMappingPath(root, prefix), nil
}

// ensureYAMLMappingPath：按点分前缀逐段下钻，缺失的中间层补空映射；返回
// 最终的条目表（原地修改 root）。
func ensureYAMLMappingPath(root *yaml.Node, path string) (*yaml.Node, error) {
	keys, err := splitConfigPath(path)
	if err != nil {
		return nil, err
	}
	current := root
	for _, key := range keys {
		child := support.NodeGet(current, key)
		if child == nil || support.IsNullNode(child) {
			child = support.YMap()
			support.NodeSet(current, key, child)
		}
		if child.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s 必须是映射。", path)
		}
		current = child
	}
	return current, nil
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
	case "yaml", "yml":
		return mcpFormatYAML, nil
	}
	return 0, fmt.Errorf("无法从文件名推断 MCP 配置格式：%s，仅支持 json、toml 与 yaml", configPath)
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
// getJSONObjectPath：点分前缀下钻取子表（下钻口径在 support.PathValue，
// 终值须是对象形态）。
func getJSONObjectPath(root map[string]any, path string) map[string]any {
	keys, err := splitConfigPath(path)
	if err != nil {
		return nil
	}
	current, ok := support.PathValue(root, keys...)
	if !ok {
		return nil
	}
	object, _ := support.AsObject(current)
	return object
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
// 存量值）。改名时先对已装路径做手术（apply 新名条目 + remove 旧名条目，以
// 盘上旧名条目为准、与巡检同口径），全部成功后才写登记——路径手术中途失败
// 时登记保持旧名，整个动作可重试（决策记录 2026-11-28-mcp-matrix-sync.md
// 第 5 条）；内容变更（不改名）不自动传播，由同步弹窗刷新。
func UpdateWorkspaceMCPInner(store *WorkspaceConfigStore, serverName string, rawMcp RawMcpConfig) (*WorkspaceMcpMutationResult, error) {
	serverName, err := normalizeMCPName(serverName)
	if err != nil {
		return nil, err
	}

	var result *WorkspaceMcpMutationResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		resolvedConfig, err := parseManagerConfig(rawContent, lock.ConfigPath(), lock.Env())
		if err != nil {
			return err
		}
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

		if nextServerName != serverName {
			// 已装路径的手术在登记写入之前；串行整文件重写，apply 幂等，
			// 中途失败重试整个命令即可收敛。
			nextResolved := resolveRawMcp(nextServerName, nextMcp)
			for _, name := range sortedKeys(resolvedConfig.McpPaths) {
				path := resolvedConfig.McpPaths[name]
				existing, err := ReadExistingMCPEntries(path, path.ConfigPath)
				if err != nil {
					return err
				}
				if _, installed := existing[mcpFormatWriterFor(path.MCPConfigType).entryKeyFor(serverName)]; !installed {
					continue
				}
				if err := applyMCPToPathConfig(path, nextResolved); err != nil {
					return err
				}
				if _, err := removeMCPFromPathConfig(path, serverName); err != nil {
					return err
				}
			}
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

		// 「全部 MCP 路径」的迭代口径与寻址层一致（条目名字典序）。
		for _, name := range sortedKeys(resolvedConfig.McpPaths) {
			mutation, err := removeMCPFromPathConfig(resolvedConfig.McpPaths[name], resolved.Name)
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
