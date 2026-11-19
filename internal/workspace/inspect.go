// inspect.go：盘上现状巡检（对齐 inspect.rs）：解析 config 后逐 target 查
// 落点存在性与链接归属，逐 MCP 查各 target 的条目在场状态。inspection 是
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
	targetID   AgentTargetID
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

	// 落点归属只查一次：两个视图（全局 target 与项目 agent）都从这张表取。
	links, err := config.skillDirLinks()
	if err != nil {
		return nil, err
	}
	linkOf := func(id string) (*skillDirLinkEntry, bool) {
		for i := range links {
			if links[i].reference.CompositeID() == id {
				return &links[i], links[i].link.IsLink
			}
		}
		return nil, false
	}
	// 落点是软链接的 target 不参与分发（判定在寻址层），这里把「为什么不参与」
	// 一并说清楚：面板已按行标注，warnings 让启动与其它消费方也能看到。
	warnings := make([]string, 0)
	for i := range links {
		if links[i].link.IsAlias() {
			warnings = append(warnings, fmt.Sprintf(
				"%s 的 skills 目录是软链接，内容由链接另一端负责，不参与技能分发。",
				links[i].reference.CompositeID()))
		}
	}

	targets := make([]TargetInspection, 0, len(config.Targets))
	for _, id := range sortedTargetIDs(config.Targets) {
		target := config.Targets[id]
		inspection := TargetInspection{
			ID:             target.ID,
			SkillDirExists: pathExistsFollow(target.SkillDir),
			SkillDirLink:   nil,
		}
		if entry, linked := linkOf(string(target.ID)); linked {
			inspection.SkillDirLink = entry.link.View()
		}
		targets = append(targets, inspection)
	}

	entryCache := map[mcpEntryCacheKey]mcpEntryCacheValue{}
	mcps := make([]McpInspection, 0, len(config.Mcps))
	for _, mcp := range config.Mcps {
		inspection, err := inspectMCP(mcp, config, entryCache, env)
		if err != nil {
			return nil, err
		}
		mcps = append(mcps, *inspection)
	}

	projects := make([]ProjectInspection, 0, len(config.Projects))
	for _, projectID := range sortedKeys(config.Projects) {
		project := config.Projects[projectID]
		agentsView := make([]TargetInspection, 0, len(project.Agents))
		for _, agentID := range sortedTargetIDs(project.Agents) {
			target := project.Agents[agentID]
			compositeID := targetRef{projectID: project.ID, agentID: target.ID}.CompositeID()
			inspection := TargetInspection{
				ID:             target.ID,
				SkillDirExists: pathExistsFollow(target.SkillDir),
				SkillDirLink:   nil,
			}
			if entry, linked := linkOf(compositeID); linked {
				inspection.SkillDirLink = entry.link.View()
			}
			agentsView = append(agentsView, inspection)
		}
		projects = append(projects, ProjectInspection{ID: project.ID, Agents: agentsView})
	}

	return &WorkspaceInspection{
		ConfigPath: support.DisplayPath(configPath),
		Targets:    targets,
		Mcps:       mcps,
		Projects:   projects,
		Warnings:   warnings,
	}, nil
}

// inspectMCP：一个 MCP 在全部 target 上的在场状态。一次遍历全部 target：
// 复合 id 由寻址层给出，项目归属从 id 拆回来，本地不再拼串。
func inspectMCP(mcp *ResolvedMcpConfig, config *ResolvedManagerConfig, entryCache map[mcpEntryCacheKey]mcpEntryCacheValue, env *support.Environment) (*McpInspection, error) {
	refs := config.targetRefs()
	targets := make([]McpTargetInspection, 0, len(refs))
	for _, entry := range refs {
		inspection, err := inspectMCPTarget(mcp, entry.target, entry.reference, entryCache, env)
		if err != nil {
			return nil, err
		}
		targets = append(targets, *inspection)
	}
	return &McpInspection{Name: mcp.Name, Targets: targets}, nil
}

// inspectMCPTarget：单 target 的 MCP 在场状态。落点状态只有「有没有找到同名
// 条目」两态：agent 与 MCP 都没有启停开关，配置在盘上就是生效的。
func inspectMCPTarget(
	mcp *ResolvedMcpConfig,
	target *ResolvedTargetConfig,
	reference targetRef,
	entryCache map[mcpEntryCacheKey]mcpEntryCacheValue,
	env *support.Environment,
) (*McpTargetInspection, error) {
	var state McpTargetState
	var detail string

	if target.ConfigPath == nil {
		state = TargetStateUnconfigured
		detail = "当前 target 还没有 MCP 配置文件路径。"
	} else {
		existing, err := readExistingMCPEntriesCached(entryCache, target, *target.ConfigPath, env)
		if err != nil {
			state = TargetStateError
			detail = fmt.Sprintf("读取目标配置失败：%v", err)
		} else {
			// 条目 key 的归类由各 config type 的 writer 声明（dsh 为清洗后
			// 的 serverName，其余与 Reins 的 mcp name 一致）。
			lookupName := mcpFormatWriterFor(target.MCPConfigType).entryKeyFor(mcp.Name)
			if _, exists := existing[lookupName]; exists {
				state = TargetStatePresent
				detail = "目标配置里已经存在同名条目。"
			} else {
				state = TargetStateMissing
				detail = "目标配置里还没有同名条目。"
			}
		}
	}

	inspection := &McpTargetInspection{
		TargetID: AgentTargetID(reference.CompositeID()),
		State:    state,
		Detail:   detail,
	}
	if reference.projectID != "" {
		projectID := reference.projectID
		inspection.ProjectID = &projectID
	}
	return inspection, nil
}

// readExistingMCPEntriesCached：同键重复读走缓存；错误同样缓存（一次
// inspection 里同一份坏文件只报一次读取成本）。
func readExistingMCPEntriesCached(
	entryCache map[mcpEntryCacheKey]mcpEntryCacheValue,
	target *ResolvedTargetConfig,
	configPath string,
	env *support.Environment,
) (map[string]any, error) {
	key := mcpEntryCacheKey{
		targetID:   target.ID,
		configPath: configPath,
		prefix:     target.MCPConfigPrefix,
		configType: target.MCPConfigType,
	}
	if cached, ok := entryCache[key]; ok {
		return cached.entries, cached.err
	}
	entries, err := ReadExistingMCPEntries(target, configPath, env)
	entryCache[key] = mcpEntryCacheValue{entries: entries, err: err}
	return entries, err
}

// pathExistsFollow：存在性判断跟随符号链接（Rust Path::exists 的口径：断链
// 的落点报「不存在」，界面中性提示）。
func pathExistsFollow(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
