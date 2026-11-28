// mcp_sync.go：MCP 矩阵同步的预演与执行编排（决策记录
// 2026-11-28-mcp-matrix-sync.md）。收敛语义按列走：每条路径收敛到该列勾选
// 集合——勾选 → 写入/刷新；已装未勾 → 移除；未装未勾 → 不动。只动登记在册
// 的 server 名（writer 的 entryKeyFor 口径），工具配置里的手工条目不碰。
// 条目形态与文件手术全部经 mcpFormatWriter 的唯一分发点，本文件不感知格式。
package workspace

import (
	"fmt"
	"sort"
	"strings"

	"reins/internal/support"
)

// syncPlan：入参格子的一次解释。预演与执行共用同一份解释，保证「看到的就
// 是将要发生的」。
type syncPlan struct {
	// checked：路径条目名 → 勾选的 server 名集合。
	checked map[string]map[string]bool
	// warnings：未知格子（弹窗快照可能落后于当前登记）按 warning 跳过。
	warnings []string
}

// planMcpSync：把入参格子解释成参与同步的勾选集合。
func planMcpSync(config *ResolvedManagerConfig, input McpSyncInput) *syncPlan {
	plan := &syncPlan{checked: map[string]map[string]bool{}}
	knownServers := make(map[string]bool, len(config.Mcps))
	for _, server := range config.Mcps {
		knownServers[server.Name] = true
	}
	for _, cell := range input.Cells {
		serverName := strings.TrimSpace(cell.ServerName)
		pathName := strings.TrimSpace(cell.PathID)
		if serverName == "" || pathName == "" || !knownServers[serverName] || config.mcpPathByName(pathName) == nil {
			plan.warnings = append(plan.warnings, fmt.Sprintf("跳过未知格子：%s → %s（登记里不存在）。", cell.ServerName, cell.PathID))
			continue
		}
		set, ok := plan.checked[pathName]
		if !ok {
			set = map[string]bool{}
			plan.checked[pathName] = set
		}
		set[serverName] = true
	}
	return plan
}

// sortedMcpServerNames：server 的迭代序（条目名字典序，与路径迭代口径一致）。
func sortedMcpServerNames(config *ResolvedManagerConfig) []string {
	names := make([]string, 0, len(config.Mcps))
	for _, server := range config.Mcps {
		names = append(names, server.Name)
	}
	sort.Strings(names)
	return names
}

// mcpSyncSkippedPathWarning：读不出来的路径整条跳过的统一说法（预演与执行
// 各自给出，口径只有一处）。
func mcpSyncSkippedPathWarning(path *ResolvedMcpPath, err error) string {
	return fmt.Sprintf("MCP 路径 %s 读取失败，本次同步不涉及该路径：%v。", path.Name, err)
}

// PreviewMcpSyncInner：preview_mcp_sync 命令核心。只读不改文件；清单只列
// 状态会变化的格子（将创建 / 将移除）——确认环节的粒度是「变化」，纯刷新
// 与未装未勾不进清单。
func PreviewMcpSyncInner(store *WorkspaceConfigStore, input McpSyncInput) (*McpSyncPreview, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	plan := planMcpSync(config, input)

	preview := &McpSyncPreview{
		Items:         []McpSyncPreviewItem{},
		FailedCreates: []McpSyncPreviewItem{},
		Warnings:      append([]string{}, plan.warnings...),
	}
	for _, name := range sortedKeys(config.McpPaths) {
		path := config.McpPaths[name]
		existing, err := ReadExistingMCPEntries(path, path.ConfigPath)
		if err != nil {
			preview.Warnings = append(preview.Warnings, mcpSyncSkippedPathWarning(path, err))
			continue
		}
		checked := plan.checked[name]
		writer := mcpFormatWriterFor(path.MCPConfigType)
		for _, serverName := range sortedMcpServerNames(config) {
			_, isPresent := existing[writer.entryKeyFor(serverName)]
			_, isChecked := checked[serverName]
			switch {
			case isChecked && !isPresent:
				// 构造失败的创建项不进确认清单：确认可执行的与真实写入同一
				// 构造路径，失败的格子只告知（前端剔除后不再执行）。
				cellPreview, err := previewMCPCell(config, serverName, name)
				if err != nil {
					preview.FailedCreates = append(preview.FailedCreates, McpSyncPreviewItem{
						ServerName: serverName,
						PathID:     name,
						Action:     McpSyncActionCreate,
						Detail:     err.Error(),
					})
					continue
				}
				preview.Items = append(preview.Items, McpSyncPreviewItem{
					ServerName: serverName,
					PathID:     name,
					Action:     McpSyncActionCreate,
					ConfigPath: cellPreview.ConfigPath,
					Format:     &cellPreview.Format,
					Content:    &cellPreview.Content,
					Detail:     "条目不存在，将新建。",
				})
			case !isChecked && isPresent:
				preview.Items = append(preview.Items, McpSyncPreviewItem{
					ServerName: serverName,
					PathID:     name,
					Action:     McpSyncActionRemove,
					ConfigPath: support.DisplayPath(path.ConfigPath),
					Detail:     "条目已存在，将移除。",
				})
			}
		}
	}
	return preview, nil
}

// SyncMcpsToPathsInner：sync_mcps_to_paths 命令核心。整段在 store 锁内：
// 登记视图与各路径写入派生自同一份配置快照（与删除 MCP 同模式），并发命令
// 被挡在串行写入之外。逐格收集成败，一处失败不中断其余格子。
func SyncMcpsToPathsInner(store *WorkspaceConfigStore, input McpSyncInput) (*McpSyncResult, error) {
	var result *McpSyncResult
	err := store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		config, err := parseManagerConfig(rawContent, lock.ConfigPath(), lock.Env())
		if err != nil {
			return err
		}
		result = syncMcpsToPaths(config, input)
		return nil
	})
	return result, err
}

// syncMcpsToPaths：矩阵收敛的执行体。逐格串行（writer 的文件手术是整文件
// 重写，乱序并发会互相覆盖——与旧逐路径流程的串行约束同源）；移除的存在性
// 判断用循环前读出的既有条目表，apply 只增键不移键，表在格子间不会失效。
func syncMcpsToPaths(config *ResolvedManagerConfig, input McpSyncInput) *McpSyncResult {
	plan := planMcpSync(config, input)
	result := &McpSyncResult{
		Applied:  []McpSyncItem{},
		Removed:  []McpSyncItem{},
		Failed:   []McpSyncItem{},
		Warnings: append([]string{}, plan.warnings...),
	}
	for _, name := range sortedKeys(config.McpPaths) {
		path := config.McpPaths[name]
		existing, err := ReadExistingMCPEntries(path, path.ConfigPath)
		if err != nil {
			result.Warnings = append(result.Warnings, mcpSyncSkippedPathWarning(path, err))
			continue
		}
		checked := plan.checked[name]
		writer := mcpFormatWriterFor(path.MCPConfigType)
		for _, serverName := range sortedMcpServerNames(config) {
			if checked[serverName] {
				mutation, err := applyMCPCell(config, serverName, name)
				if err != nil {
					result.Failed = append(result.Failed, McpSyncItem{ServerName: serverName, PathID: name, Detail: err.Error()})
					continue
				}
				result.Applied = append(result.Applied, McpSyncItem{ServerName: serverName, PathID: name, Detail: mutation.Detail})
				continue
			}
			if _, exists := existing[writer.entryKeyFor(serverName)]; !exists {
				continue
			}
			mutation, err := removeMCPCell(config, serverName, name)
			if err != nil {
				result.Failed = append(result.Failed, McpSyncItem{ServerName: serverName, PathID: name, Detail: err.Error()})
				continue
			}
			if mutation.UpdatedPath != nil {
				result.Removed = append(result.Removed, McpSyncItem{ServerName: serverName, PathID: name, Detail: mutation.Detail})
			}
		}
	}
	return result
}
