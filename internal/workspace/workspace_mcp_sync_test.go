// workspace_mcp_sync_test.go：MCP 矩阵同步的行为分区——聚合预演只列变化、
// 收敛语义按列走（勾选写入/已装未勾移除/手工条目不碰）、失败逐格收集、
// 坏路径整条跳过、改名收敛在登记写入前完成。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncTestConfig：两条 common JSON 路径（claude / zcode）+ 两个 server
// （alpha stdio / beta http）的公共夹具；路径文件内容由用例自写。
func syncTestConfig(t *testing.T, store *WorkspaceConfigStore, root string) (claudeConfig string, zcodeConfig string) {
	t.Helper()
	claudeConfig = filepath.Join(root, "claude.json")
	zcodeConfig = filepath.Join(root, "zcode.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
  zcode:
    config_path: %s
    config_prefix: mcp.servers
    config_type: common
mcps:
- name: alpha
  transport: stdio
  command: node
  args:
  - server.js
- name: beta
  transport: http
  url: https://example.com/mcp
`, claudeConfig, zcodeConfig))
	return claudeConfig, zcodeConfig
}

func TestMcpSyncPreviewListsOnlyStateChanges(t *testing.T) {
	store, root := newTestStore(t, "mcp-sync-preview")
	claudeConfig, zcodeConfig := syncTestConfig(t, store, root)
	writeFileT(t, claudeConfig, `{"mcpServers": {"alpha": {"type": "stdio", "command": "old"}}}`)
	writeFileT(t, zcodeConfig, `{"mcp": {"servers": {"beta": {"type": "http", "url": "https://old.example.com/mcp"}}}}`)

	input := McpSyncInput{Cells: []McpSyncCell{
		{ServerName: "alpha", PathID: "claude"},
		{ServerName: "alpha", PathID: "zcode"},
	}}

	preview, err := PreviewMcpSyncInner(store, input)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Warnings) != 0 {
		t.Fatalf("warnings：%+v", preview.Warnings)
	}
	// alpha→claude 已装且勾选（纯刷新）与 beta（未装未勾）都不进清单。
	if len(preview.Items) != 2 {
		t.Fatalf("应只列两个变化格子：%+v", preview.Items)
	}
	creates := filterSyncPreviewItems(preview.Items, McpSyncActionCreate)
	removes := filterSyncPreviewItems(preview.Items, McpSyncActionRemove)
	if len(creates) != 1 || creates[0].ServerName != "alpha" || creates[0].PathID != "zcode" {
		t.Fatalf("create 项应为 alpha→zcode：%+v", creates)
	}
	if creates[0].Format == nil || *creates[0].Format != "json" || creates[0].Content == nil || *creates[0].Content == "" {
		t.Fatalf("create 项应携带序列化内容：%+v", creates[0])
	}
	if len(removes) != 1 || removes[0].ServerName != "beta" || removes[0].PathID != "zcode" {
		t.Fatalf("remove 项应为 beta→zcode：%+v", removes)
	}
	if removes[0].Content != nil || removes[0].Format != nil {
		t.Fatalf("remove 项不携带内容：%+v", removes[0])
	}

	// 纯刷新（全部已装格子勾选、无新增）清单为空——默认动作不需要确认。
	preview, err = PreviewMcpSyncInner(store, McpSyncInput{Cells: []McpSyncCell{
		{ServerName: "alpha", PathID: "claude"},
		{ServerName: "beta", PathID: "zcode"},
	}})
	if err != nil {
		t.Fatalf("preview refresh: %v", err)
	}
	if len(preview.Items) != 0 {
		t.Fatalf("纯刷新不该有确认清单：%+v", preview.Items)
	}
}

func TestMcpSyncConvergesCheckedSetPerPath(t *testing.T) {
	store, root := newTestStore(t, "mcp-sync-converge")
	claudeConfig, zcodeConfig := syncTestConfig(t, store, root)
	writeFileT(t, claudeConfig, `{"mcpServers": {"alpha": {"type": "stdio", "command": "old"}, "manual-server": {"type": "stdio", "command": "keep"}}}`)
	writeFileT(t, zcodeConfig, `{"mcp": {"servers": {"beta": {"type": "http", "url": "https://old.example.com/mcp"}}}}`)

	input := McpSyncInput{Cells: []McpSyncCell{
		{ServerName: "alpha", PathID: "claude"},
		{ServerName: "alpha", PathID: "zcode"},
	}}

	result, err := SyncMcpsToPathsInner(store, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("不该有失败格子：%+v", result.Failed)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("应写入两格：%+v", result.Applied)
	}
	if len(result.Removed) != 1 || result.Removed[0].ServerName != "beta" || result.Removed[0].PathID != "zcode" {
		t.Fatalf("应移除 beta→zcode：%+v", result.Removed)
	}

	claude := parseJSONT(t, readFileT(t, claudeConfig))["mcpServers"].(map[string]any)
	if _, ok := claude["manual-server"]; !ok {
		t.Fatal("用户手工条目不该被碰")
	}
	alphaEntry, ok := claude["alpha"].(map[string]any)
	if !ok || alphaEntry["command"] != "node" {
		t.Fatalf("已装格子应刷新为登记内容：%+v", claude["alpha"])
	}
	if _, hasBeta := claude["beta"]; hasBeta {
		t.Fatal("未勾选的已装条目应被移除")
	}

	zcode := parseJSONT(t, readFileT(t, zcodeConfig))["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := zcode["alpha"]; !ok {
		t.Fatal("勾选的未装格子应被写入")
	}
	if _, hasBeta := zcode["beta"]; hasBeta {
		t.Fatal("未勾选的已装条目应被移除（zcode）")
	}
}

func TestMcpSyncCollectsFailuresWithoutInterrupting(t *testing.T) {
	store, root := newTestStore(t, "mcp-sync-partial")
	claudeConfig := filepath.Join(root, "claude.json")
	grokConfig := filepath.Join(root, "grok.toml")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
  grokbuild:
    config_path: %s
    config_prefix: mcp_servers
    config_type: grokbuild
mcps:
- name: probe
  transport: stdio
  command: node
  timeout: 1500
`, claudeConfig, grokConfig))

	input := McpSyncInput{Cells: []McpSyncCell{
		{ServerName: "probe", PathID: "claude"},
		{ServerName: "probe", PathID: "grokbuild"},
	}}

	// 构造失败的创建项不进确认清单，单独走 failedCreates 通道（只告知不
	// 参与，执行集合由前端剔除对应格子）。
	preview, err := PreviewMcpSyncInner(store, input)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Items) != 1 || preview.Items[0].PathID != "claude" {
		t.Fatalf("确认清单应只含构造成功的 claude 格子：%+v", preview.Items)
	}
	if len(preview.FailedCreates) != 1 || preview.FailedCreates[0].PathID != "grokbuild" {
		t.Fatalf("失败的创建项应单独下发：%+v", preview.FailedCreates)
	}
	if !strings.Contains(preview.FailedCreates[0].Detail, "整秒") {
		t.Fatalf("失败创建项的 Detail 应说明原因：%+v", preview.FailedCreates[0].Detail)
	}

	result, err := SyncMcpsToPathsInner(store, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Failed) != 1 || result.Failed[0].PathID != "grokbuild" {
		t.Fatalf("失败应只收集 grokbuild 格子：%+v", result.Failed)
	}
	if len(result.Applied) != 1 || result.Applied[0].PathID != "claude" {
		t.Fatalf("claude 格子不该被失败中断：%+v", result.Applied)
	}
	if !strings.Contains(readFileT(t, claudeConfig), "probe") {
		t.Fatal("claude 条目应已写入")
	}
}

func TestMcpSyncSkipsUnreadablePathWithWarning(t *testing.T) {
	store, root := newTestStore(t, "mcp-sync-broken")
	claudeConfig := filepath.Join(root, "claude.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
mcps:
- name: alpha
  transport: stdio
  command: node
`, claudeConfig))
	broken := `{"mcpServers": {`
	writeFileT(t, claudeConfig, broken)

	input := McpSyncInput{Cells: []McpSyncCell{{ServerName: "alpha", PathID: "claude"}}}

	preview, err := PreviewMcpSyncInner(store, input)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Items) != 0 || len(preview.Warnings) != 1 || !strings.Contains(preview.Warnings[0], "读取失败") {
		t.Fatalf("坏路径应整条跳过并给原因：%+v", preview)
	}

	result, err := SyncMcpsToPathsInner(store, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Applied) != 0 || len(result.Failed) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("执行侧同口径跳过：%+v", result)
	}
	if readFileT(t, claudeConfig) != broken {
		t.Fatal("坏路径文件不该被改动")
	}
}

func TestMcpSyncWarnsUnknownCells(t *testing.T) {
	store, root := newTestStore(t, "mcp-sync-unknown")
	claudeConfig, _ := syncTestConfig(t, store, root)

	result, err := SyncMcpsToPathsInner(store, McpSyncInput{Cells: []McpSyncCell{
		{ServerName: "ghost", PathID: "claude"},
		{ServerName: "alpha", PathID: "ghost"},
		{ServerName: "  ", PathID: "claude"},
	}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Warnings) != 3 {
		t.Fatalf("未知格子应逐条 warning：%+v", result.Warnings)
	}
	if _, err := os.Stat(claudeConfig); err == nil {
		t.Fatal("没有任何可执行格子时不该创建路径文件")
	}
}

func TestUpdateWorkspaceMCPInnerRenameRewritesInstalledPaths(t *testing.T) {
	store, root := newTestStore(t, "mcp-rename-sync")
	claudeConfig := filepath.Join(root, "claude.json")
	zcodeConfig := filepath.Join(root, "zcode.json")
	freshConfig := filepath.Join(root, "fresh.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
  zcode:
    config_path: %s
    config_prefix: mcp.servers
    config_type: common
  fresh:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
mcps:
- name: old-name
  transport: stdio
  command: node
  args:
  - a.js
`, claudeConfig, zcodeConfig, freshConfig))
	writeFileT(t, claudeConfig, `{"mcpServers": {"old-name": {"type": "stdio", "command": "node", "args": ["a.js"]}}}`)
	writeFileT(t, zcodeConfig, `{"mcp": {"servers": {"old-name": {"type": "stdio", "command": "node", "args": ["a.js"]}}}}`)
	writeFileT(t, freshConfig, `{}`)

	result, err := UpdateWorkspaceMCPInner(store, "old-name", RawMcpConfig{
		Name:      "new-name",
		Transport: TransportStdio,
		Command:   strPtr("node"),
		Args:      []string{"b.js"},
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if result.ServerName != "new-name" {
		t.Fatalf("server_name：%s", result.ServerName)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse raw: %v", err)
	}
	if len(raw.Mcps) != 1 || raw.Mcps[0].Name != "new-name" {
		t.Fatalf("登记应已改名：%+v", raw.Mcps)
	}

	claude := parseJSONT(t, readFileT(t, claudeConfig))["mcpServers"].(map[string]any)
	newEntry, ok := claude["new-name"].(map[string]any)
	if !ok {
		t.Fatalf("已装路径应写入新名条目：%+v", claude)
	}
	if args, _ := newEntry["args"].([]any); len(args) != 1 || args[0] != "b.js" {
		t.Fatalf("新条目应携带新内容：%+v", newEntry)
	}
	if _, hasOld := claude["old-name"]; hasOld {
		t.Fatal("旧名条目应被移除")
	}
	zcode := parseJSONT(t, readFileT(t, zcodeConfig))["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := zcode["new-name"]; !ok {
		t.Fatalf("zcode 应同步改名：%+v", zcode)
	}
	if _, hasOld := zcode["old-name"]; hasOld {
		t.Fatal("zcode 旧名条目应被移除")
	}
	fresh := parseJSONT(t, readFileT(t, freshConfig))["mcpServers"]
	if fresh != nil {
		t.Fatalf("未安装的路径不该被写入：%+v", fresh)
	}
}

func TestUpdateWorkspaceMCPInnerRenameStaysRetryableWhenPathFails(t *testing.T) {
	store, root := newTestStore(t, "mcp-rename-retry")
	claudeConfig := filepath.Join(root, "claude.json")
	grokConfig := filepath.Join(root, "grok.toml")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
  grokbuild:
    config_path: %s
    config_prefix: mcp_servers
    config_type: grokbuild
mcps:
- name: old-name
  transport: stdio
  command: node
`, claudeConfig, grokConfig))
	writeFileT(t, claudeConfig, `{"mcpServers": {"old-name": {"type": "stdio", "command": "node"}}}`)
	writeFileT(t, grokConfig, "[mcp_servers.old-name]\ncommand = \"node\"\n")

	// grok 的 timeout 约束让 apply 必败：登记必须保持旧名，整个改名可重试。
	_, err := UpdateWorkspaceMCPInner(store, "old-name", RawMcpConfig{
		Name:      "new-name",
		Transport: TransportStdio,
		Command:   strPtr("node"),
		Timeout:   ptrUint64(1500),
	})
	if err == nil {
		t.Fatal("路径手术失败应报错")
	}
	var rawRetry RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &rawRetry); err != nil {
		t.Fatalf("parse raw: %v", err)
	}
	if len(rawRetry.Mcps) != 1 || rawRetry.Mcps[0].Name != "old-name" {
		t.Fatalf("登记应保持旧名：%+v", rawRetry.Mcps)
	}
}

func TestUpdateWorkspaceMCPInnerRenameToExistingNameRejected(t *testing.T) {
	store, root := newTestStore(t, "mcp-rename-clash")
	claudeConfig := filepath.Join(root, "claude.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
mcps:
- name: alpha
  transport: stdio
  command: node
- name: beta
  transport: stdio
  command: node
`, claudeConfig))

	_, err := UpdateWorkspaceMCPInner(store, "alpha", RawMcpConfig{
		Name:      "beta",
		Transport: TransportStdio,
		Command:   strPtr("node"),
	})
	if err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("改名撞名应拒绝：%v", err)
	}
}

func filterSyncPreviewItems(items []McpSyncPreviewItem, action McpSyncAction) []McpSyncPreviewItem {
	kept := make([]McpSyncPreviewItem, 0, len(items))
	for i := range items {
		if items[i].Action == action {
			kept = append(kept, items[i])
		}
	}
	return kept
}
