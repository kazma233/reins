// mcp_formats_test.go：各格式 writer 的往返、约束声明与拒绝文案（对齐
// mcp_formats.rs 内嵌测试模块 + 决策记录 2026-11-19-mcp-format-detection-by-extension.md
// 的新行为分区）。三句 bail 文案是既有契约，改造前后必须逐字一致。
package workspace

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

// 三句 bail 文案是既有契约，改造前后必须逐字一致。
const (
	grokTomlOnly     = "Grok Build config type 仅支持 TOML。"
	opencodeJSONOnly = "OpenCode config type 不支持 TOML。"
	dshYAMLOnly      = "Dsh config type 仅支持 Cordis patch YAML。"
)

func stdioServer(name string) *ResolvedMcpConfig {
	return &ResolvedMcpConfig{
		Name:      name,
		Transport: TransportStdio,
		Command:   strPtr("node"),
		Args:      []string{"server.js"},
		Env:       map[string]string{"VALUE": "1"},
		Timeout:   ptrUint64(2000),
	}
}

func mcpFormatsPath(name string, configPath string, prefix string, configType agents.McpConfigType) *ResolvedMcpPath {
	return &ResolvedMcpPath{
		Name:            name,
		ConfigPath:      configPath,
		MCPConfigPrefix: prefix,
		MCPConfigType:   configType,
	}
}

func TestCommonJSONRoundtripPreservesUnrelatedConfig(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-common-json")
	configPath := filepath.Join(root, "mcp.json")
	original := `{"topLevel": {"keep": true}, "mcpServers": {"other": {"type": "stdio", "command": "keep"}}}`
	writeFileT(t, configPath, original)
	target := mcpFormatsPath("claude", configPath, "mcpServers", agents.McpConfigCommon)
	writer := mcpFormatWriterFor(agents.McpConfigCommon)

	formatTag, content, err := writer.previewEntry(configPath, stdioServer("probe"))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if formatTag != "json" {
		t.Fatalf("format tag：%s", formatTag)
	}
	var previewed map[string]any
	if err := json.Unmarshal([]byte(content), &previewed); err != nil {
		t.Fatalf("preview content: %v", err)
	}
	if previewed["command"] != "node" {
		t.Fatalf("previewed command：%v", previewed["command"])
	}

	if err := writer.applyEntry(target, configPath, stdioServer("probe")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	written := parseJSONT(t, readFileT(t, configPath))
	mcpServers := written["mcpServers"].(map[string]any)
	probe := mcpServers["probe"].(map[string]any)
	if probe["command"] != "node" {
		t.Fatalf("probe command：%v", probe["command"])
	}
	if probe["args"].([]any)[0] != "server.js" {
		t.Fatalf("probe args：%v", probe["args"])
	}
	if mcpServers["other"].(map[string]any)["command"] != "keep" {
		t.Fatalf("无关条目应保留：%v", mcpServers["other"])
	}
	if written["topLevel"].(map[string]any)["keep"] != true {
		t.Fatalf("无关顶层应保留：%v", written["topLevel"])
	}

	entries, err := writer.readExistingEntries(target.MCPConfigPrefix, configPath)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := entries["probe"]; !has {
		t.Fatalf("应读到 probe：%v", entries)
	}

	mutation, err := writer.removeEntry(target, configPath, "probe")
	if err != nil || mutation.Action != "remove" {
		t.Fatalf("remove：%+v %v", mutation, err)
	}
	var after map[string]any
	if err := json.Unmarshal([]byte(readFileT(t, configPath)), &after); err != nil {
		t.Fatalf("after: %v", err)
	}
	var originalValue map[string]any
	if err := json.Unmarshal([]byte(original), &originalValue); err != nil {
		t.Fatalf("original: %v", err)
	}
	if jsonEqual(after, originalValue) == false {
		t.Fatalf("删除后应还原原配置：\n%v\n%v", after, originalValue)
	}
	mutation, err = writer.removeEntry(target, configPath, "probe")
	if err != nil || mutation.Action != "noop" {
		t.Fatalf("二次删除应为 noop：%+v %v", mutation, err)
	}
}

func jsonEqual(left any, right any) bool {
	leftText, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightText, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return string(leftText) == string(rightText)
}

func TestCommonTOMLRoundtripUsesSecondFloats(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-common-toml")
	configPath := filepath.Join(root, "config.toml")
	original := "model = 'unchanged'\n\n[mcp_servers.other]\ncommand = 'keep'\n"
	writeFileT(t, configPath, original)
	target := mcpFormatsPath("codex", configPath, "mcp_servers", agents.McpConfigCommon)
	writer := mcpFormatWriterFor(agents.McpConfigCommon)

	formatTag, content, err := writer.previewEntry(configPath, stdioServer("probe"))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if formatTag != "toml" {
		t.Fatalf("format tag：%s", formatTag)
	}
	previewed := parseTOMLT(t, content)
	if previewed["command"] != "node" {
		t.Fatalf("previewed command：%v", previewed["command"])
	}

	if err := writer.applyEntry(target, configPath, stdioServer("probe")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	written := parseTOMLT(t, readFileT(t, configPath))
	if written["model"] != "unchanged" {
		t.Fatalf("model 应保留：%v", written["model"])
	}
	probe := written["mcp_servers"].(map[string]any)["probe"].(map[string]any)
	if probe["command"] != "node" {
		t.Fatalf("probe command：%v", probe["command"])
	}
	// Common 的 TOML 形态把毫秒 timeout 写成秒（浮点）。
	if probe["tool_timeout_sec"] != 2.0 {
		t.Fatalf("tool_timeout_sec 应为浮点 2.0：%+v (%T)", probe["tool_timeout_sec"], probe["tool_timeout_sec"])
	}

	entries, err := writer.readExistingEntries(target.MCPConfigPrefix, configPath)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := entries["probe"]; !has {
		t.Fatalf("应读到 probe：%v", entries)
	}

	if _, err := writer.removeEntry(target, configPath, "probe"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := parseTOMLT(t, readFileT(t, configPath))
	originalValue := parseTOMLT(t, original)
	if !tomlEqual(after, originalValue) {
		t.Fatalf("删除后应还原原配置：\n%v\n%v", after, originalValue)
	}
	mutation, err := writer.removeEntry(target, configPath, "probe")
	if err != nil || mutation.Action != "noop" {
		t.Fatalf("二次删除应为 noop：%+v %v", mutation, err)
	}
}

func TestGrokbuildTOMLRoundtripAndJSONRejectedVerbatim(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-grok")
	configPath := filepath.Join(root, "config.toml")
	original := "[mcp_servers.other]\ncommand = 'keep'\n"
	writeFileT(t, configPath, original)
	target := mcpFormatsPath("grokbuild", configPath, "mcp_servers", agents.McpConfigGrokBuild)
	writer := mcpFormatWriterFor(agents.McpConfigGrokBuild)

	if err := writer.applyEntry(target, configPath, stdioServer("probe")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	written := parseTOMLT(t, readFileT(t, configPath))
	probe := written["mcp_servers"].(map[string]any)["probe"].(map[string]any)
	// GrokBuild 的 TOML 形态要求整秒，写入整数秒。
	if probe["tool_timeout_sec"] != int64(2) {
		t.Fatalf("tool_timeout_sec 应为整数秒 2：%+v (%T)", probe["tool_timeout_sec"], probe["tool_timeout_sec"])
	}

	entries, err := writer.readExistingEntries(target.MCPConfigPrefix, configPath)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := entries["probe"]; !has {
		t.Fatalf("应读到 probe：%v", entries)
	}

	if _, err := writer.removeEntry(target, configPath, "probe"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := parseTOMLT(t, readFileT(t, configPath))
	if !tomlEqual(after, parseTOMLT(t, original)) {
		t.Fatalf("删除后应还原原配置：\n%v", after)
	}

	// GrokBuild 只接受 TOML：JSON 路径在任何操作下都按既定文案拒绝。
	jsonPath := filepath.Join(root, "config.json")
	writeFileT(t, jsonPath, `{"mcp_servers": {}}`)
	if _, _, err := writer.previewEntry(jsonPath, stdioServer("probe")); err == nil || err.Error() != grokTomlOnly {
		t.Fatalf("preview err：%v", err)
	}
	if err := writer.applyEntry(target, jsonPath, stdioServer("probe")); err == nil || err.Error() != grokTomlOnly {
		t.Fatalf("apply err：%v", err)
	}
	if _, err := writer.readExistingEntries(target.MCPConfigPrefix, jsonPath); err == nil || err.Error() != grokTomlOnly {
		t.Fatalf("read err：%v", err)
	}
	if _, err := writer.removeEntry(target, jsonPath, "probe"); err == nil || err.Error() != grokTomlOnly {
		t.Fatalf("remove err：%v", err)
	}
}

func TestOpencodeJSONRoundtripAndTOMLRejectedVerbatim(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-opencode")
	configPath := filepath.Join(root, "opencode.json")
	original := `{"theme": "dark"}`
	writeFileT(t, configPath, original)
	target := mcpFormatsPath("opencode", configPath, "mcp.servers", agents.McpConfigOpenCode)
	writer := mcpFormatWriterFor(agents.McpConfigOpenCode)

	if err := writer.applyEntry(target, configPath, stdioServer("probe")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	written := parseJSONT(t, readFileT(t, configPath))
	if written["theme"] != "dark" {
		t.Fatalf("theme 应保留：%v", written["theme"])
	}
	probe := written["mcp"].(map[string]any)["servers"].(map[string]any)["probe"].(map[string]any)
	if probe["type"] != "local" {
		t.Fatalf("type：%v", probe["type"])
	}
	if !jsonEqual(probe["command"], []any{"node", "server.js"}) {
		t.Fatalf("command 应为数组：%v", probe["command"])
	}

	entries, err := writer.readExistingEntries(target.MCPConfigPrefix, configPath)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := entries["probe"]; !has {
		t.Fatalf("应读到 probe：%v", entries)
	}

	if _, err := writer.removeEntry(target, configPath, "probe"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := parseJSONT(t, readFileT(t, configPath))
	if _, has := after["mcp"].(map[string]any)["servers"].(map[string]any)["probe"]; has {
		t.Fatalf("probe 应已删除：%v", after)
	}
	if after["theme"] != "dark" {
		t.Fatalf("theme 应保留：%v", after["theme"])
	}

	// OpenCode 的拒绝点在条目构造（读取 TOML 不拦），文案逐字。
	tomlPath := filepath.Join(root, "opencode.toml")
	writeFileT(t, tomlPath, "key = 'value'\n")
	if _, _, err := writer.previewEntry(tomlPath, stdioServer("probe")); err == nil || err.Error() != opencodeJSONOnly {
		t.Fatalf("preview err：%v", err)
	}
	if _, err := writer.desiredTomlEntry(stdioServer("probe")); err == nil || err.Error() != opencodeJSONOnly {
		t.Fatalf("desired toml err：%v", err)
	}
}

func TestDshPatchRoundtripClaimsAndRestoresOps(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-dsh")
	configPath := filepath.Join(root, "cordis.patch.yml")
	writeFileT(t, configPath, "- remove: [user-owned-node]\n")
	target := mcpFormatsPath("dsh", configPath, "", agents.McpConfigDsh)
	writer := mcpFormatWriterFor(agents.McpConfigDsh)

	formatTag, content, err := writer.previewEntry(configPath, stdioServer("probe"))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if formatTag != "yaml" {
		t.Fatalf("format tag：%s", formatTag)
	}
	previewOps := parseOpsNodeT(t, content)
	if seq := previewOps; len(seq.Content) != 1 {
		t.Fatalf("preview 应只有 1 个操作：%d", len(seq.Content))
	}

	if err := writer.applyEntry(target, configPath, stdioServer("probe")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops := parseOpsT(t, configPath)
	// 用户操作原样保留，Reins 只追加 insert。
	if len(ops.Content) != 2 {
		t.Fatalf("操作数应为 2：%d", len(ops.Content))
	}
	if support.NodeGet(ops.Content[0], "remove") == nil {
		t.Fatal("用户操作在前")
	}
	entries := support.NodeGet(ops.Content[1], "insert").Content
	entry := entries[0]
	if name, _ := support.NodeAsStr(support.NodeGet(entry, "name")); name != "@deepseek-ai/dsh-mcp-client" {
		t.Fatalf("name：%s", name)
	}
	config := support.NodeGet(entry, "config")
	if serverName, _ := support.NodeAsStr(support.NodeGet(config, "serverName")); serverName != "probe" {
		t.Fatalf("serverName：%v", support.NodeGet(config, "serverName"))
	}
	if command, _ := support.NodeAsStr(support.NodeGet(config, "command")); command != "node" {
		t.Fatalf("command：%s", command)
	}

	// 读取侧按清洗后的 serverName 建键。
	readEntries, err := writer.readExistingEntries(target.MCPConfigPrefix, configPath)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if _, has := readEntries["probe"]; !has {
		t.Fatalf("应读到 probe：%v", readEntries)
	}

	mutation, err := writer.removeEntry(target, configPath, "probe")
	if err != nil || mutation.Action != "remove" {
		t.Fatalf("remove：%+v %v", mutation, err)
	}
	after := parseOpsT(t, configPath)
	if len(after.Content) != 1 || support.NodeGet(after.Content[0], "remove") == nil {
		t.Fatalf("删除后只剩用户操作：%v", after)
	}
	mutation, err = writer.removeEntry(target, configPath, "probe")
	if err != nil || mutation.Action != "noop" {
		t.Fatalf("二次删除应为 noop：%+v %v", mutation, err)
	}

	// dsh 条目不走 TOML/JSON 形态，两个构造入口都按既定文案拒绝。
	if _, err := writer.desiredTomlEntry(stdioServer("probe")); err == nil || err.Error() != dshYAMLOnly {
		t.Fatalf("desired toml err：%v", err)
	}
	if _, err := writer.desiredJSONEntry(stdioServer("probe")); err == nil || err.Error() != dshYAMLOnly {
		t.Fatalf("desired json err：%v", err)
	}
}

func TestDshPatchRemoteEntryUsesStreamableHTTPShape(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-dsh-remote")
	configPath := filepath.Join(root, "cordis.patch.yml")
	target := mcpFormatsPath("dsh", configPath, "", agents.McpConfigDsh)
	writer := mcpFormatWriterFor(agents.McpConfigDsh)
	server := &ResolvedMcpConfig{
		Name:      "probe",
		Transport: TransportHTTP,
		URL:       strPtr("https://example.com/mcp"),
		Headers:   map[string]string{"X-Key": "value"},
	}

	if err := writer.applyEntry(target, configPath, server); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ops := parseOpsT(t, configPath)
	entry := ops.Content[0]
	insertList := support.NodeGet(entry, "insert")
	if insertList == nil || len(insertList.Content) == 0 {
		t.Fatal("写入后应有 insert 条目")
	}
	firstEntry := insertList.Content[0]
	entryConfig := support.NodeGet(firstEntry, "config")
	if transport, _ := support.NodeAsStr(support.NodeGet(entryConfig, "transport")); transport != "streamable-http" {
		t.Fatalf("transport：%s", transport)
	}
	if serverName, _ := support.NodeAsStr(support.NodeGet(entryConfig, "serverName")); serverName != "probe" {
		t.Fatalf("serverName：%s", serverName)
	}
	if url, _ := support.NodeAsStr(support.NodeGet(entryConfig, "url")); url != "https://example.com/mcp" {
		t.Fatalf("url：%s", url)
	}
	if header, _ := support.NodeAsStr(support.NodeGet(support.NodeGet(entryConfig, "headers"), "X-Key")); header != "value" {
		t.Fatalf("headers X-Key：%v", support.NodeGet(entryConfig, "headers"))
	}
	if support.NodeGet(entryConfig, "command") != nil {
		t.Fatal("远端形态没有 command")
	}

	// 配置解析层已拦截无链接的远端 MCP；writer 仍按既有文案防御性拒绝。
	withoutURL := *server
	withoutURL.URL = nil
	if err := writer.applyEntry(target, configPath, &withoutURL); err == nil || err.Error() != "远程 MCP 缺少 mcp 链接" {
		t.Fatalf("无链接 err：%v", err)
	}
}

func TestEntryKeyForIsIdentityExceptDshSanitized(t *testing.T) {
	// 非 dsh writer 的条目 key 与 Reins 的 mcp name 恒等。
	plain := "my probe.v2/中文"
	for _, configType := range []agents.McpConfigType{agents.McpConfigCommon, agents.McpConfigGrokBuild, agents.McpConfigOpenCode} {
		if got := mcpFormatWriterFor(configType).entryKeyFor(plain); got != plain {
			t.Fatalf("%s entry key：%s", configType, got)
		}
	}

	// dsh 的条目 key 是清洗后的 serverName；用例形态与
	// dsh_server_name_sanitization 既有测试保持一致。
	dsh := mcpFormatWriterFor(agents.McpConfigDsh)
	if got := dsh.entryKeyFor("probe"); got != "probe" {
		t.Fatalf("dsh probe key：%s", got)
	}
	if got := dsh.entryKeyFor("my probe.v2/中文"); got != "my-probe-v2---" {
		t.Fatalf("dsh sanitize key：%s", got)
	}
	if got := dsh.entryKeyFor(strings.Repeat("a", 40)); got != strings.Repeat("a", 32) {
		t.Fatalf("dsh 超长截断：%s", got)
	}
}

func TestWriterConstraintDeclarations(t *testing.T) {
	// 前缀必填：dsh 按 name+serverName 定位条目，没有 prefix 概念。
	if !mcpFormatWriterFor(agents.McpConfigCommon).prefixRequired() ||
		!mcpFormatWriterFor(agents.McpConfigGrokBuild).prefixRequired() ||
		!mcpFormatWriterFor(agents.McpConfigOpenCode).prefixRequired() {
		t.Fatal("common/grok/opencode 的 prefix 必填")
	}
	if mcpFormatWriterFor(agents.McpConfigDsh).prefixRequired() {
		t.Fatal("dsh 的 prefix 不必填")
	}

	// JSON 默认根：OpenCode 注入 $schema，其余 JSON 形态从空对象开始。
	commonRoot, err := mcpFormatWriterFor(agents.McpConfigCommon).defaultJSONRoot().(map[string]any)
	if !err || len(commonRoot) != 0 {
		t.Fatalf("common 默认根：%v", mcpFormatWriterFor(agents.McpConfigCommon).defaultJSONRoot())
	}
	grokRoot, err := mcpFormatWriterFor(agents.McpConfigGrokBuild).defaultJSONRoot().(map[string]any)
	if !err || len(grokRoot) != 0 {
		t.Fatalf("grok 默认根：%v", mcpFormatWriterFor(agents.McpConfigGrokBuild).defaultJSONRoot())
	}
	openCodeRoot, err := mcpFormatWriterFor(agents.McpConfigOpenCode).defaultJSONRoot().(map[string]any)
	if !err || openCodeRoot["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("opencode 默认根：%v", mcpFormatWriterFor(agents.McpConfigOpenCode).defaultJSONRoot())
	}
}

// ---------------------------------------------------------------------------
// 格式判定（后缀化决策的新行为分区）
// ---------------------------------------------------------------------------

// 已拍板的行为变更（docs/decisions/2026-11-19-mcp-format-detection-by-extension.md）：
// 格式判定只按扩展名——奇形怪状后缀的既有文件（哪怕内容是合法 JSON/TOML）
// 一律报错提示改名，不再做内容探测。这个错误路径此前在 Rust 测试里零覆盖，
// 由本用例钉住。
func TestDetectMCPFileFormatRejectsUnrecognizedExtensions(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-extension-error")
	writer := mcpFormatWriterFor(agents.McpConfigCommon)

	// 内容完全可解析、后缀不可识别：报错，不静默猜格式。
	for _, name := range []string{"mcp-conf", "mcp.json5", "mcp"} {
		configPath := filepath.Join(root, name)
		writeFileT(t, configPath, `{"mcpServers": {}}`)
		wantErr := "无法从文件名推断 MCP 配置格式"
		if _, err := readFileEntries(writer, "mcpServers", configPath); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("read(%s) 应报「无法从文件名推断」：%v", name, err)
		}
		if _, _, err := writer.previewEntry(configPath, stdioServer("probe")); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("preview(%s) 应报「无法从文件名推断」：%v", name, err)
		}
		if err := writer.applyEntry(mcpFormatsPath("claude", configPath, "mcpServers", agents.McpConfigCommon), configPath, stdioServer("probe")); err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("apply(%s) 应报「无法从文件名推断」：%v", name, err)
		}
		// 报错路径不得改写文件。
		if got := readFileT(t, configPath); got != `{"mcpServers": {}}` {
			t.Fatalf("失败路径不得改写文件：%s", got)
		}
	}
}

// 两个合法后缀的归属：大小写不敏感（Rust to_ascii_lowercase）；扩展名取自
// 最后一个组件（目录名不参与）。
func TestDetectMCPFileFormatMapsExtensions(t *testing.T) {
	if format, err := detectMCPFileFormat("/tmp/x/config.JSON"); err != nil || format != mcpFormatJSON {
		t.Fatalf("JSON：(%v, %v)", format, err)
	}
	if format, err := detectMCPFileFormat("/tmp/x/config.TOML"); err != nil || format != mcpFormatTOML {
		t.Fatalf("TOML：(%v, %v)", format, err)
	}
	if format, err := detectMCPFileFormat("/tmp/x/config.YML"); err != nil || format != mcpFormatYAML {
		t.Fatalf("YAML：(%v, %v)", format, err)
	}
	if format, err := detectMCPFileFormat("/tmp/x/no-extension"); err == nil {
		t.Fatalf("无后缀应报错：(%v, %v)", format, err)
	}
	// 首字符的点属于文件名本身（.json 整体是 stem、无扩展名）→ 报错。
	if format, err := detectMCPFileFormat("/tmp/x/.json"); err == nil {
		t.Fatalf("隐藏文件应报错：(%v, %v)", format, err)
	}
	// 文件不存在也走同一条判定（不再区分「先看内容」的两段式）。
	if format, err := detectMCPFileFormat(filepath.Join(t.TempDir(), "missing.json")); err != nil || format != mcpFormatJSON {
		t.Fatalf("不存在的 .json 也应判 JSON：(%v, %v)", format, err)
	}
}

// 空内容的既有 .json/.toml 文件按空配置处理（读空表、应用时从头写）——与
// Rust 的「空内容走后缀推断」分支行为一致，但判定本身只看后缀。
func TestReadEmptyExistingMCPConfigReturnsEmpty(t *testing.T) {
	root := testsupport.TestDir(t, "mcp-fmt-empty")
	writer := mcpFormatWriterFor(agents.McpConfigCommon)

	jsonPath := filepath.Join(root, "mcp.json")
	writeFileT(t, jsonPath, "   \n")
	entries, err := readFileEntries(writer, "mcpServers", jsonPath)
	if err != nil {
		t.Fatalf("read empty json: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("空文件应读空表：%v", entries)
	}

	tomlPath := filepath.Join(root, "mcp.toml")
	writeFileT(t, tomlPath, "")
	entries, err = readFileEntries(writer, "mcpServers", tomlPath)
	if err != nil {
		t.Fatalf("read empty toml: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("空文件应读空表：%v", entries)
	}

	// 不存在的文件同空表。
	entries, err = readFileEntries(writer, "mcpServers", filepath.Join(root, "missing.json"))
	if err != nil {
		t.Fatalf("read missing: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("缺失文件应读空表：%v", entries)
	}

	// 顶层不是 object 的 .json 报错。
	arrayPath := filepath.Join(root, "array.json")
	writeFileT(t, arrayPath, "[1, 2]")
	if _, err := readFileEntries(writer, "mcpServers", arrayPath); err == nil || !strings.Contains(err.Error(), "JSON 配置根节点必须是 object") {
		t.Fatalf("数组根应报错：%v", err)
	}
}
