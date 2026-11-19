// workspace_dsh_test.go：dsh target 的 Cordis patch 读写（对齐
// tests/workspace_dsh.rs）：预设与前缀豁免、项目级 skills-only、条目合并不
// 覆盖用户内容、serverName 清洗、streamable-http 形态与失败不落盘。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
)

const dshPluginName = "@deepseek-ai/dsh-mcp-client"

// 与 grokbuild fixture 同构：dsh target 的 patch 路径落在 TestDir 下，
// MCP 名称/transport/timeout 可注入。
func dshFixture(t *testing.T, root string, transport string, timeout *uint64, name string) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	timeoutValue := "null"
	if timeout != nil {
		timeoutValue = fmt.Sprintf("%d", *timeout)
	}
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  dsh:
    skill_dir: dsh/skills
    mcp:
      config_path: dsh/cordis.patch.yml
      config_type: dsh
mcps:
- name: %s
  enabled: true
  transport: %s
  command: node
  args: [server.js]
  env:
    VALUE: "1"
  url: https://example.invalid/mcp
  headers: {}
  timeout: %s
`, name, transport, timeoutValue))
	return store
}

// 远端形态 fixture：url/headers 可注入，stdio 专属字段留空，用于验证
// streamable-http 条目与 stdio 形态互不携带对方字段。
func dshRemoteFixture(t *testing.T, root string, transport string, url string, headers string, name string) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	urlValue := "null"
	if url != "" {
		urlValue = fmt.Sprintf("%q", url)
	}
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  dsh:
    skill_dir: dsh/skills
    mcp:
      config_path: dsh/cordis.patch.yml
      config_type: dsh
mcps:
- name: %s
  enabled: true
  transport: %s
  url: %s
  headers: %s
`, name, transport, urlValue, headers))
	return store
}

func dshPatchPath(t *testing.T, store *WorkspaceConfigStore) string {
	t.Helper()
	return filepath.Join(filepath.Dir(store.ConfigPath()), "dsh", "cordis.patch.yml")
}

func parseOpsT(t *testing.T, path string) *yaml.Node {
	t.Helper()
	raw := readFileT(t, path)
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("parse ops: %v", err)
	}
	if len(document.Content) == 0 {
		t.Fatalf("patch 应是操作列表：%q", raw)
	}
	return document.Content[0]
}

// claimedConfigT：找到 Reins 认领条目（name+serverName），返回它的 config
// mapping；未命中返回 nil。
func claimedConfigT(t *testing.T, ops *yaml.Node, serverName string) *yaml.Node {
	t.Helper()
	if ops.Kind != yaml.SequenceNode {
		return nil
	}
	for _, op := range ops.Content {
		entries := support.NodeGet(op, "insert")
		if entries == nil || entries.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range entries.Content {
			nameNode := support.NodeGet(entry, "name")
			name, _ := support.NodeAsStr(nameNode)
			configServerName := support.NodeGet(support.NodeGet(entry, "config"), "serverName")
			serverNameValue, _ := support.NodeAsStr(configServerName)
			if name == dshPluginName && serverNameValue == serverName {
				return support.NodeGet(entry, "config")
			}
		}
	}
	return nil
}

func nodeChild(t *testing.T, node *yaml.Node, key string) *yaml.Node {
	t.Helper()
	child := support.NodeGet(node, key)
	if child == nil {
		t.Fatalf("缺 %s 键：%v", key, node)
	}
	return child
}

func nodeStr(t *testing.T, node *yaml.Node, key string) string {
	t.Helper()
	value, ok := support.NodeAsStr(nodeChild(t, node, key))
	if !ok {
		t.Fatalf("%s 应是字符串：%v", key, node)
	}
	return value
}

func TestDshDefaultsPresetAndPrefixRelaxation(t *testing.T) {
	_, dir := newTestStore(t, "dsh-defaults")
	home := filepath.Join(dir, "home")
	presets := TargetPresetsInner(envOf(t))
	var preset *TargetPreset
	for i := range presets {
		if string(presets[i].TargetID) == "dsh" {
			preset = &presets[i]
		}
	}
	if preset == nil {
		t.Fatal("缺少 dsh 预设")
	}
	if preset.SkillDir != joinPath(home, ".dsh/skills") {
		t.Fatalf("preset skill_dir：%s", preset.SkillDir)
	}
	if preset.ConfigPath == nil || *preset.ConfigPath != joinPath(home, ".dsh/cordis.patch.yml") {
		t.Fatalf("preset config_path：%+v", preset.ConfigPath)
	}
	if preset.MCPConfigPrefix != "" {
		t.Fatalf("preset prefix 应为空：%q", preset.MCPConfigPrefix)
	}
	if string(agents.McpConfigDsh) != "dsh" {
		t.Fatalf("存储串漂移：%s", agents.McpConfigDsh)
	}

	// config.yaml 里 dsh 允许「有 config_path + 空 prefix」；common 不允许。
	path := filepath.Join(t.TempDir(), "reins-dsh-defaults.yaml")
	config, err := parseManagerConfig(
		"targets:\n  dsh:\n    skill_dir: dsh/skills\n    mcp:\n      config_path: dsh/cordis.patch.yml\n",
		path, &support.Environment{})
	if err != nil {
		t.Fatalf("parse dsh: %v", err)
	}
	target := config.targetByID("dsh")
	if target == nil {
		t.Fatal("dsh target exists")
	}
	if target.MCPConfigType != agents.McpConfigDsh {
		t.Fatalf("config_type：%s", target.MCPConfigType)
	}
	if target.MCPConfigPrefix != "" {
		t.Fatalf("prefix 应为空：%q", target.MCPConfigPrefix)
	}
	if _, err := parseManagerConfig(
		"targets:\n  other:\n    skill_dir: x/skills\n    mcp:\n      config_path: x/config.json\n",
		path, &support.Environment{}); err == nil {
		t.Fatal("common 有 config_path 无 prefix 必须报错")
	}
}

// dsh 项目层只派生 .dsh/skills；MCP 走全局 Cordis patch，项目级无配置文件。
func TestDshProjectDefaultsAreSkillsOnly(t *testing.T) {
	project := t.TempDir()
	var dsh *TargetDefaults
	for _, item := range projectAgentDefaults(project) {
		if string(item.ID) == "dsh" {
			dsh = &item
		}
	}
	if dsh == nil {
		t.Fatal("dsh 项目默认值应存在")
	}
	if dsh.SkillDir != filepath.Join(project, ".dsh", "skills") {
		t.Fatalf("skill_dir：%s", dsh.SkillDir)
	}
	if dsh.ConfigPath != nil {
		t.Fatalf("项目层不应派生 MCP 配置路径：%+v", dsh.ConfigPath)
	}
	if dsh.ConfigPrefix != "" {
		t.Fatalf("prefix：%q", dsh.ConfigPrefix)
	}
	if dsh.ConfigType != agents.McpConfigDsh {
		t.Fatalf("config_type：%s", dsh.ConfigType)
	}
}

func TestDshCreateTargetAcceptsConfigPathWithoutPrefix(t *testing.T) {
	store, root := newTestStore(t, "dsh-create-target")
	writeFileT(t, store.ConfigPath(), "{}\n")

	if _, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID:   "dsh",
		SkillDir:   "dsh/skills",
		ConfigPath: strPtr("dsh/cordis.patch.yml"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := config.targetByID("dsh")
	if target == nil {
		t.Fatal("dsh target exists")
	}
	if target.ConfigPath == nil || *target.ConfigPath != filepath.Join(root, "dsh", "cordis.patch.yml") {
		t.Fatalf("config_path：%+v", target.ConfigPath)
	}
	if target.MCPConfigPrefix != "" {
		t.Fatalf("prefix 应为空：%q", target.MCPConfigPrefix)
	}
}

func TestDshApplyCreatesPatchFileAndPreviewMatches(t *testing.T) {
	store, _ := newTestStore(t, "dsh-apply-create")
	store = dshFixture(t, filepath.Dir(store.ConfigPath()), "stdio", ptrUint64(30_000), "probe")
	path := dshPatchPath(t, store)

	preview, err := PreviewMCPTargetInner(store, "probe", "dsh")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Format != "yaml" {
		t.Fatalf("format：%s", preview.Format)
	}

	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}

	ops := parseOpsT(t, path)
	config := claimedConfigT(t, ops, "probe")
	if config == nil {
		t.Fatal("写入后应包含 probe 条目")
	}
	if len(ops.Content) != 1 {
		t.Fatalf("新建文件只包含一个 insert 操作：%d", len(ops.Content))
	}
	if nodeStr(t, config, "serverName") != "probe" {
		t.Fatalf("serverName：%s", nodeStr(t, config, "serverName"))
	}
	if nodeStr(t, config, "transport") != "stdio" {
		t.Fatalf("transport：%s", nodeStr(t, config, "transport"))
	}
	if nodeStr(t, config, "command") != "node" {
		t.Fatalf("command：%s", nodeStr(t, config, "command"))
	}
	if args := nodeChild(t, config, "args"); len(args.Content) != 1 {
		t.Fatalf("args 应有 1 项：%v", args)
	}
	if nodeChild(t, config, "env").Content[1].Value != "1" {
		t.Fatalf("env VALUE：%v", nodeChild(t, config, "env"))
	}
	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}
	if nodeStr(t, config, "cwd") != home {
		t.Fatalf("cwd 应为 home：%s", nodeStr(t, config, "cwd"))
	}
	if nodeChild(t, config, "toolCallTimeoutMs").Value != "30000" {
		t.Fatalf("toolCallTimeoutMs：%s", nodeChild(t, config, "toolCallTimeoutMs").Value)
	}
	if nodeChild(t, config, "failOnStartupError").Value != "false" {
		t.Fatalf("failOnStartupError：%s", nodeChild(t, config, "failOnStartupError").Value)
	}
	if strings.Contains(readFileT(t, path), "!!js") {
		t.Fatal("写入的条目不得携带 js 扩展标签")
	}

	parsed, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := parsed.targetByID("dsh")
	entries, err := ReadExistingMCPEntries(target, path, &support.Environment{})
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := entries["probe"]; !has {
		t.Fatalf("应读到 probe 条目：%v", entries)
	}

	// preview 展示的操作与落盘内容一致：用户看到的就是将要写入的。
	previewOps := parseOpsNodeT(t, preview.Content)
	if !yamlEqualNodes(t, previewOps, ops) {
		t.Fatalf("preview 与落盘内容不一致：\n%s\n---\n%s", preview.Content, readFileT(t, path))
	}
}

// parseOpsNodeT：解析 YAML 文本为操作列表节点（preview 内容侧）。
func parseOpsNodeT(t *testing.T, content string) *yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("parse preview ops: %v", err)
	}
	if len(document.Content) == 0 {
		t.Fatalf("preview 应是操作列表：%q", content)
	}
	return document.Content[0]
}

// yamlEqualNodes：语义相等（重新序列化后比较，规避节点内部指针差异）。
func yamlEqualNodes(t *testing.T, left *yaml.Node, right *yaml.Node) bool {
	t.Helper()
	leftText, err := support.MarshalYAMLNode(left)
	if err != nil {
		t.Fatalf("marshal left: %v", err)
	}
	rightText, err := support.MarshalYAMLNode(right)
	if err != nil {
		t.Fatalf("marshal right: %v", err)
	}
	return leftText == rightText
}

func TestDshApplyMergesIntoExistingFileAndPreservesUserEntries(t *testing.T) {
	store, _ := newTestStore(t, "dsh-merge")
	store = dshFixture(t, filepath.Dir(store.ConfigPath()), "stdio", nil, "probe")
	path := dshPatchPath(t, store)
	// 用户已有内容：无关插件条目（带 !!js 扩展标签）、未知操作，以及和
	// probe 混在同一个 insert 里的手写条目（不同 id，按 serverName 命中）。
	existing := `- insert:
    - id: memory-memorix
      name: memorix
      config:
        cwd: !!js process.cwd()
        keep: true
- remove: [user-owned-node]
- insert:
    - id: handwritten
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: probe
        transport: stdio
        command: old-command
        args: []
        env: {}
        cwd: /tmp
        toolCallTimeoutMs: 1000
        failOnStartupError: true
    - id: sibling
      name: other-plugin
      config: {keep: me}
`
	writeFileT(t, path, existing)

	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	first := readFileT(t, path)
	ops := parseOpsT(t, path)
	if len(ops.Content) != 3 {
		t.Fatalf("不新增操作条目：%d", len(ops.Content))
	}

	memorix := support.NodeGet(ops.Content[0], "insert").Content[0]
	if name, _ := support.NodeAsStr(support.NodeGet(memorix, "name")); name != "memorix" {
		t.Fatalf("memorix name：%s", name)
	}
	// !!js 标签在字节层面原样保留（serde_yaml 解析会丢标签，所以断言落盘文本
	// 而不是解析结构）。
	if !strings.Contains(first, "!!js process.cwd()") {
		t.Fatalf("用户条目的 !!js 标签必须原样保留: %s", first)
	}
	if keep := support.NodeGet(support.NodeGet(memorix, "config"), "keep"); keep == nil || keep.Value != "true" {
		t.Fatalf("memorix keep 应为 true：%v", support.NodeGet(memorix, "config"))
	}

	if support.NodeGet(ops.Content[1], "remove") == nil {
		t.Fatal("未知操作原样保留")
	}

	config := claimedConfigT(t, ops, "probe")
	if nodeStr(t, config, "command") != "node" {
		t.Fatalf("probe command 应被改写为 node：%s", nodeStr(t, config, "command"))
	}
	if nodeChild(t, config, "toolCallTimeoutMs").Value != "60000" {
		t.Fatalf("未设置超时时写默认 60s：%s", nodeChild(t, config, "toolCallTimeoutMs").Value)
	}
	mixed := support.NodeGet(ops.Content[2], "insert").Content
	if len(mixed) != 2 {
		t.Fatalf("同 insert 内的其它条目保留：%d", len(mixed))
	}
	if id, _ := support.NodeAsStr(support.NodeGet(mixed[1], "id")); id != "sibling" {
		t.Fatalf("sibling 条目应保留：%s", id)
	}

	// 重复 apply 幂等：文件字节不变。
	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply twice: %v", err)
	}
	if got := readFileT(t, path); got != first {
		t.Fatalf("重复 apply 必须幂等：\n got: %q\nwant: %q", got, first)
	}

	// 更新 MCP 后再 apply：原地改写，不产生第二条目。
	if _, err := UpdateWorkspaceMCPInner(store, "probe", RawMcpConfig{
		Name:      "probe",
		Transport: TransportStdio,
		Command:   strPtr("deno"),
		Args:      []string{"run"},
		Env:       map[string]string{},
		Headers:   map[string]string{},
		Timeout:   ptrUint64(2_000),
	}); err != nil {
		t.Fatalf("update mcp: %v", err)
	}
	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply after update: %v", err)
	}
	ops = parseOpsT(t, path)
	if len(ops.Content) != 3 {
		t.Fatalf("更新后不应新增操作条目：%d", len(ops.Content))
	}
	config = claimedConfigT(t, ops, "probe")
	if nodeStr(t, config, "command") != "deno" {
		t.Fatalf("command 应为 deno：%s", nodeStr(t, config, "command"))
	}
	if nodeChild(t, config, "toolCallTimeoutMs").Value != "2000" {
		t.Fatalf("toolCallTimeoutMs 应为 2000：%s", nodeChild(t, config, "toolCallTimeoutMs").Value)
	}
}

func TestDshRemoveDeletesOnlyClaimedEntries(t *testing.T) {
	store, _ := newTestStore(t, "dsh-remove")
	root := filepath.Dir(store.ConfigPath())
	store = dshFixture(t, root, "stdio", nil, "probe")
	path := dshPatchPath(t, store)

	// 文件不存在时移除是 noop，且不创建文件。
	mutation, err := RemoveMCPFromTargetInner(store, "probe", "dsh")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if mutation.Action != "noop" {
		t.Fatalf("action：%s", mutation.Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("noop 不该创建文件：%v", err)
	}

	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	reinsContent := readFileT(t, path)
	writeFileT(t, path, fmt.Sprintf("- insert:\n    - id: memory\n      name: memorix\n      config: {keep: true}\n%s", reinsContent))
	if ops := parseOpsT(t, path); len(ops.Content) != 2 {
		t.Fatalf("预置用户条目后应有 2 个操作：%d", len(ops.Content))
	}

	if _, err := RemoveMCPFromTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	ops := parseOpsT(t, path)
	if len(ops.Content) != 1 {
		t.Fatalf("移除后只剩用户条目：%d", len(ops.Content))
	}
	if claimedConfigT(t, ops, "probe") != nil {
		t.Fatal("probe 条目应已删除")
	}
	memorix := support.NodeGet(ops.Content[0], "insert").Content[0]
	if name, _ := support.NodeAsStr(support.NodeGet(memorix, "name")); name != "memorix" {
		t.Fatalf("用户条目不受移除影响：%s", name)
	}
	if mutation, err := RemoveMCPFromTargetInner(store, "probe", "dsh"); err != nil || mutation.Action != "noop" {
		t.Fatalf("二次移除应为 noop：%+v %v", mutation, err)
	}

	// 文件里只剩 Reins 条目时，删除后保留空列表文件。
	store2, _ := newTestStore(t, "dsh-remove-empty")
	store2 = dshFixture(t, filepath.Dir(store2.ConfigPath()), "stdio", nil, "probe")
	path2 := dshPatchPath(t, store2)
	if _, err := ApplyMCPToTargetInner(store2, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := RemoveMCPFromTargetInner(store2, "probe", "dsh"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := readFileT(t, path2); got != "[]\n" {
		t.Fatalf("删空后应保留空列表文件：%q", got)
	}
}

func TestDshServerNameSanitization(t *testing.T) {
	store, _ := newTestStore(t, "dsh-sanitize")
	store = dshFixture(t, filepath.Dir(store.ConfigPath()), "stdio", nil, "my probe.v2/中文")
	path := dshPatchPath(t, store)

	if _, err := ApplyMCPToTargetInner(store, "my probe.v2/中文", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops := parseOpsT(t, path)
	if claimedConfigT(t, ops, "my-probe-v2---") == nil {
		t.Fatal("非法字符折叠为 -")
	}

	parsed, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := parsed.targetByID("dsh")
	entries, err := ReadExistingMCPEntries(target, path, &support.Environment{})
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	if len(keys) != 1 || keys[0] != "my-probe-v2---" {
		t.Fatalf("读取侧同样按清洗后的 serverName 建键：%v", keys)
	}

	// 超长名称截断到 32 个字符。
	store2, _ := newTestStore(t, "dsh-sanitize-long")
	longName := strings.Repeat("a", 40)
	store2 = dshFixture(t, filepath.Dir(store2.ConfigPath()), "stdio", nil, longName)
	path2 := dshPatchPath(t, store2)
	if _, err := ApplyMCPToTargetInner(store2, longName, "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops2 := parseOpsT(t, path2)
	serverName := nodeStr(t, claimedConfigT(t, ops2, strings.Repeat("a", 32)), "serverName")
	if len(serverName) != 32 {
		t.Fatalf("超长名称应截断到 32：%d", len(serverName))
	}
}

func TestDshHTTPTransportWritesStreamableHTTPEntry(t *testing.T) {
	store, _ := newTestStore(t, "dsh-http")
	store = dshRemoteFixture(t, filepath.Dir(store.ConfigPath()), "http", "https://example.invalid/mcp", `{X-Key: value}`, "probe")
	path := dshPatchPath(t, store)

	preview, err := PreviewMCPTargetInner(store, "probe", "dsh")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Format != "yaml" {
		t.Fatalf("format：%s", preview.Format)
	}

	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops := parseOpsT(t, path)
	previewOps := parseOpsNodeT(t, preview.Content)
	if !yamlEqualNodes(t, previewOps, ops) {
		t.Fatalf("preview 展示的就是将要写入的：\n%s\n---\n%s", preview.Content, readFileT(t, path))
	}

	config := claimedConfigT(t, ops, "probe")
	if config == nil {
		t.Fatal("写入后应包含 probe 条目")
	}
	if nodeStr(t, config, "transport") != "streamable-http" {
		t.Fatalf("transport：%s", nodeStr(t, config, "transport"))
	}
	if nodeStr(t, config, "serverName") != "probe" {
		t.Fatalf("serverName：%s", nodeStr(t, config, "serverName"))
	}
	if nodeStr(t, config, "url") != "https://example.invalid/mcp" {
		t.Fatalf("url：%s", nodeStr(t, config, "url"))
	}
	if nodeChild(t, config, "headers").Content[1].Value != "value" {
		t.Fatalf("headers X-Key：%v", nodeChild(t, config, "headers"))
	}
	// 远端形态没有 command/args/env/cwd，校验这些字段不得出现。
	for _, key := range []string{"command", "args", "env", "cwd"} {
		if support.NodeGet(config, key) != nil {
			t.Fatalf("远端条目不得携带 %s", key)
		}
	}

	// 读回：inspect 归类与反显按 serverName 建键，远端的 url/headers 完整可读。
	parsed, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := parsed.targetByID("dsh")
	entries, err := ReadExistingMCPEntries(target, path, &support.Environment{})
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	readBack, ok := entries["probe"].(map[string]any)
	if !ok {
		t.Fatalf("读取应包含 probe：%v", entries)
	}
	if readBack["transport"] != "streamable-http" || readBack["url"] != "https://example.invalid/mcp" {
		t.Fatalf("read back：%v", readBack)
	}
	headers, ok := readBack["headers"].(map[string]any)
	if !ok || headers["X-Key"] != "value" {
		t.Fatalf("headers：%v", readBack["headers"])
	}

	// 重复 apply 幂等：文件字节不变。
	first := readFileT(t, path)
	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply twice: %v", err)
	}
	if got := readFileT(t, path); got != first {
		t.Fatalf("重复 apply 必须幂等：\n got: %q\nwant: %q", got, first)
	}

	mutation, err := RemoveMCPFromTargetInner(store, "probe", "dsh")
	if err != nil || mutation.Action != "remove" {
		t.Fatalf("remove：%+v %v", mutation, err)
	}
	if got := readFileT(t, path); got != "[]\n" {
		t.Fatalf("删空后保留空列表：%q", got)
	}
	if mutation, err := RemoveMCPFromTargetInner(store, "probe", "dsh"); err != nil || mutation.Action != "noop" {
		t.Fatalf("二次删除应为 noop：%+v %v", mutation, err)
	}
}

func TestDshHTTPWithoutHeadersOmitsHeadersKey(t *testing.T) {
	store, _ := newTestStore(t, "dsh-http-no-headers")
	store = dshRemoteFixture(t, filepath.Dir(store.ConfigPath()), "http", "https://example.invalid/mcp", "{}", "probe")
	path := dshPatchPath(t, store)

	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops := parseOpsT(t, path)
	config := claimedConfigT(t, ops, "probe")
	if config == nil {
		t.Fatal("缺少 probe 条目")
	}
	if support.NodeGet(config, "headers") != nil {
		t.Fatalf("headers 为空时不写该键：%v", config)
	}
}

func TestDshSSETransportMapsToStreamableHTTP(t *testing.T) {
	store, _ := newTestStore(t, "dsh-sse")
	store = dshRemoteFixture(t, filepath.Dir(store.ConfigPath()), "sse", "https://example.invalid/sse", "{}", "probe")
	path := dshPatchPath(t, store)

	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops := parseOpsT(t, path)
	config := claimedConfigT(t, ops, "probe")
	if config == nil {
		t.Fatal("缺少 probe 条目")
	}
	if nodeStr(t, config, "transport") != "streamable-http" {
		t.Fatalf("dsh 的 streamable-http 形态本身覆盖 SSE 系协议：%s", nodeStr(t, config, "transport"))
	}
	if nodeStr(t, config, "url") != "https://example.invalid/sse" {
		t.Fatalf("url：%s", nodeStr(t, config, "url"))
	}
}

func TestDshRemoteWithoutURLFailsWithoutWriting(t *testing.T) {
	store, _ := newTestStore(t, "dsh-remote-no-url")
	store = dshRemoteFixture(t, filepath.Dir(store.ConfigPath()), "http", "", "{}", "probe")
	path := dshPatchPath(t, store)

	// 配置解析层先于 writer 拒绝无链接的远端 MCP。
	if _, err := PreviewMCPTargetInner(store, "probe", "dsh"); err == nil {
		t.Fatal("preview 应报错")
	} else if !strings.Contains(err.Error(), "必须提供 mcp 链接") {
		t.Fatalf("无链接的远端 MCP 应报错：%v", err)
	}
	if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err == nil {
		t.Fatal("apply 应报错")
	} else if !strings.Contains(err.Error(), "必须提供 mcp 链接") {
		t.Fatalf("无链接的远端 MCP 应报错：%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("失败路径不得创建文件：%v", err)
	}
}

func TestDshMalformedFilesAreRejectedWithoutWriting(t *testing.T) {
	store, _ := newTestStore(t, "dsh-malformed")
	store = dshFixture(t, filepath.Dir(store.ConfigPath()), "stdio", nil, "probe")
	path := dshPatchPath(t, store)
	for _, content := range []string{"model: unchanged\n", "[unclosed\n"} {
		writeFileT(t, path, content)
		if _, err := PreviewMCPTargetInner(store, "probe", "dsh"); err == nil {
			t.Fatalf("preview(%q) 应报错", content)
		}
		if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err == nil {
			t.Fatalf("apply(%q) 应报错", content)
		}
		parsed, err := store.Parse()
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		target := parsed.targetByID("dsh")
		if _, err := ReadExistingMCPEntries(target, path, &support.Environment{}); err == nil {
			t.Fatalf("read(%q) 应报错", content)
		}
		if _, err := RemoveMCPFromTargetInner(store, "probe", "dsh"); err == nil {
			t.Fatalf("remove(%q) 应报错", content)
		}
		if got := readFileT(t, path); got != content {
			t.Fatalf("失败路径不得改写文件：\n got: %q\nwant: %q", got, content)
		}
	}

	// 空文件与空列表都是合法的 patch（按「无操作」处理）。
	for _, content := range []string{"", "[]\n"} {
		writeFileT(t, path, content)
		if _, err := ApplyMCPToTargetInner(store, "probe", "dsh"); err != nil {
			t.Fatalf("apply(%q): %v", content, err)
		}
		if claimedConfigT(t, parseOpsT(t, path), "probe") == nil {
			t.Fatalf("apply(%q) 后应有 probe 条目", content)
		}
	}
}
