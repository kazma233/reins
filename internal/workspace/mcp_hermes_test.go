// mcp_hermes_test.go：common 方言 YAML 载体的引擎测试——hermes config.yaml
// 同方言（command/args/env 条目、mcp_servers 顶层键、yaml.Node 手术编辑）。
// 契约见 Ref.md Hermes 节与 docs/decisions/2026-11-28-skill-and-mcp-paths.md。
package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

func hermesFixture(t *testing.T, root string) (*WorkspaceConfigStore, string) {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), `skill_paths:
  hermes:
    dir: hermes/skills
mcp_paths:
  hermes:
    config_path: hermes/config.yaml
    config_prefix: mcp_servers
    config_type: common
mcps:
- name: probe
  transport: stdio
  command: node
  args: [server.js]
  env:
    KEY: value
`)
	return store, filepath.Join(root, "hermes", "config.yaml")
}

// 手术编辑：既有顶层键、注释与既有 mcp_servers 条目原样保留，新条目追加。
func TestCommonYAMLApplySurgicallyEditsConfigAndPreservesContent(t *testing.T) {
	store, configPath := hermesFixture(t, testsupport.TestDir(t, "hermes-apply"))
	writeFileT(t, configPath, `# Hermes 主配置（用户注释必须保留）
gateway:
  bind: 127.0.0.1
skills:
  write_approval: false
mcp_servers:
  existing:
    command: keep-me
`)

	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	content := readFileT(t, configPath)
	if !strings.Contains(content, "# Hermes 主配置（用户注释必须保留）") {
		t.Fatalf("文件头注释必须保留：%s", content)
	}
	if !strings.Contains(content, "bind: 127.0.0.1") || !strings.Contains(content, "write_approval: false") {
		t.Fatalf("无关顶层键必须保留：%s", content)
	}
	if !strings.Contains(content, "command: keep-me") {
		t.Fatalf("既有 mcp_servers 条目必须保留：%s", content)
	}

	// 结构断言：probe 条目 command/args/env，无 type 判别字段。
	raw, err := support.ReadYAMLDocument(configPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	probe := support.NodeGet(support.NodeGet(raw, "mcp_servers"), "probe")
	if probe == nil {
		t.Fatalf("probe 条目应存在：%s", content)
	}
	if got, _ := support.NodeAsStr(support.NodeGet(probe, "command")); got != "node" {
		t.Fatalf("command：%s", got)
	}
	if args := support.NodeGet(probe, "args"); len(args.Content) != 1 {
		t.Fatalf("args：%v", args)
	}
	if env := support.NodeGet(probe, "env"); support.NodeGet(env, "KEY") == nil {
		t.Fatalf("env：%v", env)
	}
	if support.NodeGet(probe, "type") != nil {
		t.Fatalf("YAML 载体的条目无 type 判别字段：%v", probe)
	}

	// 重复 apply 幂等：文件字节不变。
	first := readFileT(t, configPath)
	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply twice: %v", err)
	}
	if got := readFileT(t, configPath); got != first {
		t.Fatalf("重复 apply 必须幂等：\n got: %q\nwant: %q", got, first)
	}
}

// 文件不存在时 apply 创建含 mcp_servers 的最小配置；读取侧对缺失键读空表。
func TestCommonYAMLApplyCreatesAbsentFileAndKey(t *testing.T) {
	store, configPath := hermesFixture(t, testsupport.TestDir(t, "hermes-apply-absent"))

	// 文件不存在：remove 是 noop 且不创建文件。
	mutation, err := removeMCPCell(mustParseT(t, store), "probe", "hermes")
	if err != nil || mutation.Action != "noop" {
		t.Fatalf("remove absent：%+v %v", mutation, err)
	}
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("noop 不该创建文件：%v", statErr)
	}

	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := support.ReadYAMLDocument(configPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if support.NodeGet(raw, "mcp_servers") == nil {
		t.Fatalf("mcp_servers 键应已创建：%s", readFileT(t, configPath))
	}

	writer := mcpFormatWriterFor(agents.McpConfigCommon)
	entries, err := writer.readExistingEntries("mcp_servers", configPath)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := entries["probe"]; !has {
		t.Fatalf("应读到 probe 条目：%v", entries)
	}

	// 空文件同文件缺失：apply 从零构造。
	writeFileT(t, configPath, "")
	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply empty: %v", err)
	}
	if !strings.Contains(readFileT(t, configPath), "mcp_servers:") {
		t.Fatalf("空文件应从零构造：%s", readFileT(t, configPath))
	}
}

// mcp_servers 不是映射（或根不是映射）时 apply 拒绝且不改写文件；根不是
// 映射时 preview 一并拒绝（与 JSON 引擎同口径：preview 校验根可解析）。
func TestCommonYAMLRejectsNonMappingShapesWithoutWriting(t *testing.T) {
	store, configPath := hermesFixture(t, testsupport.TestDir(t, "hermes-invalid"))
	for _, content := range []string{"mcp_servers: [1, 2]\n", "- just\n- a list\n", "key: value\nmcp_servers: not-a-map\n"} {
		writeFileT(t, configPath, content)
		if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err == nil {
			t.Fatalf("apply(%q) 应报错", content)
		} else if !strings.Contains(err.Error(), "必须是映射") {
			t.Fatalf("错误文案要说清形状要求：%v", err)
		}
		if got := readFileT(t, configPath); got != content {
			t.Fatalf("失败路径不得改写文件：\n got: %q\nwant: %q", got, content)
		}
	}
	// 根不是映射：preview 同样拒绝。
	writeFileT(t, configPath, "- just\n- a list\n")
	if _, err := previewMCPCell(mustParseT(t, store), "probe", "hermes"); err == nil {
		t.Fatal("preview 对非映射根应报错")
	}
}

// remove 只删认领的键；条目缺失是 noop；删空后保留空映射。
func TestCommonYAMLRemoveDeletesOnlyClaimedEntry(t *testing.T) {
	store, configPath := hermesFixture(t, testsupport.TestDir(t, "hermes-remove"))
	writeFileT(t, configPath, `mcp_servers:
  mine:
    command: keep
`)
	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	mutation, err := removeMCPCell(mustParseT(t, store), "probe", "hermes")
	if err != nil || mutation.Action != "remove" {
		t.Fatalf("remove：%+v %v", mutation, err)
	}
	raw, err := support.ReadYAMLDocument(configPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	servers := support.NodeGet(raw, "mcp_servers")
	if servers == nil || support.NodeGet(servers, "mine") == nil {
		t.Fatalf("用户条目必须保留：%s", readFileT(t, configPath))
	}
	if support.NodeGet(servers, "probe") != nil {
		t.Fatalf("probe 条目应已删除：%s", readFileT(t, configPath))
	}

	if mutation, err := removeMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil || mutation.Action != "noop" {
		t.Fatalf("二次移除应为 noop：%+v %v", mutation, err)
	}

	// 只剩 Reins 条目时删空保留空映射（与「不整文件覆盖」的合并语义一致）。
	writeFileT(t, configPath, "mcp_servers:\n  probe:\n    command: node\n")
	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := removeMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := readFileT(t, configPath); got != "mcp_servers: {}\n" {
		t.Fatalf("删空后应保留空映射：%q", got)
	}
}

// 远程条目：url + headers（headers 非空才写）；无链接拒绝且不落盘。
func TestCommonYAMLRemoteEntryShapeAndValidation(t *testing.T) {
	store, configPath := hermesFixture(t, testsupport.TestDir(t, "hermes-remote"))
	if _, err := UpdateWorkspaceMCPInner(store, "probe", RawMcpConfig{
		Name:      "probe",
		Transport: TransportHTTP,
		URL:       strPtr("https://example.com/mcp"),
		Headers:   map[string]string{"X-Key": "value"},
	}); err != nil {
		t.Fatalf("update mcp: %v", err)
	}

	preview, err := previewMCPCell(mustParseT(t, store), "probe", "hermes")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Format != "yaml" {
		t.Fatalf("format：%s", preview.Format)
	}
	// 预览是条目本体（与 JSON/TOML 同口径），写入位置由 config_prefix 交代。
	// 断言语义键而非字节（缩进由序列化器决定，写入侧幂等测试已钉住落盘形态）。
	if !strings.HasPrefix(preview.Content, "url: https://example.com/mcp") ||
		!strings.Contains(preview.Content, "X-Key: value") {
		t.Fatalf("preview 形态漂移：\n%s", preview.Content)
	}

	if _, err := applyMCPCell(mustParseT(t, store), "probe", "hermes"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	written := readFileT(t, configPath)
	if !strings.Contains(written, "url: https://example.com/mcp") ||
		!strings.Contains(written, "X-Key: value") {
		t.Fatalf("写入内容缺远程条目：%s", written)
	}

	// 无链接：解析层已拦截（normalizeRawMcp），writer 防御性同样拒绝。
	urlLess := &ResolvedMcpConfig{Name: "probe", Transport: TransportSSE}
	writer := mcpFormatWriterFor(agents.McpConfigCommon)
	path := &ResolvedMcpPath{Name: "hermes", ConfigPath: configPath, MCPConfigPrefix: "mcp_servers", MCPConfigType: agents.McpConfigCommon}
	if err := writer.applyEntry(path, configPath, urlLess); err == nil ||
		!strings.Contains(err.Error(), "远程 MCP 缺少 mcp 链接") {
		t.Fatalf("无链接的 apply 应拒绝：%v", err)
	}
	if got := readFileT(t, configPath); got != written {
		t.Fatalf("失败路径不得改写文件：\n got: %q\nwant: %q", got, written)
	}
}
