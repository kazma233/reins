// inspect.go：盘上现状巡检（对齐 inspect.rs）：解析 config 后逐技能路径查
// 落点存在性与链接归属，逐 MCP 查各 MCP 路径的条目在场状态。inspection 是
// 只读视图，不改任何文件。
package workspace

import (
	"fmt"
	"os"

	"reins/internal/agents"
	"reins/internal/support"
)

// mcpEntryCacheKey：MCP 条目读取的缓存键：同一份配置文件 + 同一 writer 语义
// 在一次 inspection 里只读一次。
type mcpEntryCacheKey struct {
	pathID     string
	configPath string
	prefix     string
	configType agents.McpConfigType
}

type mcpEntryCacheValue struct {
	entries map[string]any
	err     error
}

// InspectAppStateDocument：inspect_app_state_document 的平移。
func inspectAppStateDocument(rawContent string, configPath string, env *support.Environment) (*WorkspaceInspection, error) {
	config, err := parseManagerConfig(rawContent, configPath, env)
	if err != nil {
		return nil, err
	}

	// 落点归属只查一次：技能路径巡检与同步弹窗候选都从这张表取。
	links, err := config.skillDirLinks()
	if err != nil {
		return nil, err
	}
	// 目录是软链接的技能路径不参与分发（判定在寻址层），这里把「为什么不参与」
	// 一并说清楚：面板已按行标注，warnings 让启动与其它消费方也能看到。
	warnings := make([]string, 0)
	for i := range links {
		if links[i].link.IsAlias() {
			warnings = append(warnings, fmt.Sprintf(
				"%s 的目录是软链接，内容由链接另一端负责，不参与技能分发。",
				links[i].name))
		}
	}

	skillPaths := make([]SkillPathInspection, 0, len(config.SkillPaths))
	for _, name := range sortedKeys(config.SkillPaths) {
		path := config.SkillPaths[name]
		inspection := SkillPathInspection{
			Name:      name,
			DirExists: pathExistsFollow(path.Dir),
			DirLink:   nil,
		}
		if entry, linked := linkOf(links, name); linked {
			inspection.DirLink = entry.link.View()
		}
		skillPaths = append(skillPaths, inspection)
	}

	entryCache := map[mcpEntryCacheKey]mcpEntryCacheValue{}
	mcps := make([]McpInspection, 0, len(config.Mcps))
	for _, mcp := range config.Mcps {
		inspection, err := inspectMCP(mcp, config, entryCache)
		if err != nil {
			return nil, err
		}
		mcps = append(mcps, *inspection)
	}

	return &WorkspaceInspection{
		ConfigPath: support.DisplayPath(configPath),
		SkillPaths: skillPaths,
		Mcps:       mcps,
		Warnings:   warnings,
	}, nil
}

// linkOf：按条目名取链接归属（skillDirLinks 一次遍历的查找封装）。
func linkOf(links []skillDirLinkEntry, name string) (*skillDirLinkEntry, bool) {
	for i := range links {
		if links[i].name == name {
			return &links[i], links[i].link.IsLink
		}
	}
	return nil, false
}

// inspectMCP：一个 MCP 在全部 MCP 路径上的在场状态。一次遍历全部路径，
// 条目名即寻址键。
func inspectMCP(mcp *ResolvedMcpConfig, config *ResolvedManagerConfig, entryCache map[mcpEntryCacheKey]mcpEntryCacheValue) (*McpInspection, error) {
	paths := make([]McpPathStateInspection, 0, len(config.McpPaths))
	for _, name := range sortedKeys(config.McpPaths) {
		inspection, err := inspectMCPPath(mcp, config.McpPaths[name], name, entryCache)
		if err != nil {
			return nil, err
		}
		paths = append(paths, *inspection)
	}
	return &McpInspection{Name: mcp.Name, Paths: paths}, nil
}

// inspectMCPPath：单条 MCP 路径的条目在场状态。落点状态只有「有没有找到同名
// 条目」两态：路径与 MCP 条目都没有启停开关，配置在盘上就是生效的。
func inspectMCPPath(
	mcp *ResolvedMcpConfig,
	path *ResolvedMcpPath,
	name string,
	entryCache map[mcpEntryCacheKey]mcpEntryCacheValue,
) (*McpPathStateInspection, error) {
	var state McpPathState
	var detail string

	existing, err := readExistingMCPEntriesCached(entryCache, path, path.ConfigPath)
	if err != nil {
		state = PathStateError
		detail = fmt.Sprintf("读取目标配置失败：%v", err)
	} else {
		// 条目 key 的归类由各 config type 的 writer 声明（dsh 为清洗后
		// 的 serverName，其余与 Reins 的 mcp name 一致）。
		lookupName := mcpFormatWriterFor(path.MCPConfigType).entryKeyFor(mcp.Name)
		if _, exists := existing[lookupName]; exists {
			state = PathStatePresent
			detail = "目标配置里已经存在同名条目。"
		} else {
			state = PathStateMissing
			detail = "目标配置里还没有同名条目。"
		}
	}

	return &McpPathStateInspection{
		PathID: name,
		State:  state,
		Detail: detail,
	}, nil
}

// readExistingMCPEntriesCached：同键重复读走缓存；错误同样缓存（一次
// inspection 里同一份坏文件只报一次读取成本）。
func readExistingMCPEntriesCached(
	entryCache map[mcpEntryCacheKey]mcpEntryCacheValue,
	path *ResolvedMcpPath,
	configPath string,
) (map[string]any, error) {
	key := mcpEntryCacheKey{
		pathID:     path.Name,
		configPath: configPath,
		prefix:     path.MCPConfigPrefix,
		configType: path.MCPConfigType,
	}
	if cached, ok := entryCache[key]; ok {
		return cached.entries, cached.err
	}
	entries, err := ReadExistingMCPEntries(path, configPath)
	entryCache[key] = mcpEntryCacheValue{entries: entries, err: err}
	return entries, err
}

// pathExistsFollow：存在性判断跟随符号链接（Rust Path::exists 的口径：断链
// 的落点报「不存在」，界面中性提示）。
func pathExistsFollow(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
