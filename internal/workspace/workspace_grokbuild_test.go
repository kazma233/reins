// workspace_grokbuild_test.go：grokbuild 的预设一致性、显式路径优先、skill
// 链接生命周期与 TOML MCP 读写（对齐 tests/workspace_grokbuild.rs）。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reins/internal/agents"
	"reins/internal/support"
)

func TestGrokbuildMcpPathPresetFollowsGrokHome(t *testing.T) {
	_, dir := newTestStore(t, "grok-defaults")
	grokRoot := filepath.Join(dir, "grok-home")
	if err := os.MkdirAll(grokRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("GROK_HOME", grokRoot)
	env := envOf(t)

	// 预设路径在派生时读取 GROK_HOME：这正是前端静态预设无法预填、需要
	// 后端下发的原因。
	presets := GetSkillPathPresetsInner(env)
	var skillPreset *SkillPathPreset
	for i := range presets {
		if presets[i].Name == "Grok Build" {
			skillPreset = &presets[i]
		}
	}
	if skillPreset == nil {
		t.Fatal("缺少 grokbuild 技能路径预设")
	}
	if skillPreset.Dir != joinPath(grokRoot, "skills") {
		t.Fatalf("preset dir：%s", skillPreset.Dir)
	}

	mcpPresets := GetMcpPathPresetsInner(env)
	var mcpPreset *McpPathPreset
	for i := range mcpPresets {
		if mcpPresets[i].Name == "Grok Build" {
			mcpPreset = &mcpPresets[i]
		}
	}
	if mcpPreset == nil {
		t.Fatal("缺少 grokbuild MCP 路径预设")
	}
	if mcpPreset.ConfigPath != joinPath(grokRoot, "config.toml") {
		t.Fatalf("preset config_path：%s", mcpPreset.ConfigPath)
	}
	if mcpPreset.ConfigPrefix != "mcp_servers" {
		t.Fatalf("preset prefix：%s", mcpPreset.ConfigPrefix)
	}
	if mcpPreset.ConfigType != string(agents.McpConfigGrokBuild) {
		t.Fatalf("preset config_type：%s", mcpPreset.ConfigType)
	}

	// config_type 的存储串是 serde snake_case：读取与写入互逆，未知串报错。
	if string(agents.McpConfigGrokBuild) != "grokbuild" {
		t.Fatalf("存储串漂移：%s", agents.McpConfigGrokBuild)
	}
	if _, err := parseMcpConfigType("grokbuild"); err != nil {
		t.Fatalf("grokbuild 应可解析：%v", err)
	}
	if _, err := parseMcpConfigType("grok_build"); err == nil {
		t.Fatal("grok_build 不是合法拼写")
	}
}

func TestGrokbuildExplicitPathsWinAndLegacyConfigIsUnchanged(t *testing.T) {
	store, root := newTestStore(t, "grok-explicit")
	raw := "skill_paths:\n  grokbuild:\n    dir: explicit/skills\nmcp_paths:\n  grokbuild:\n    config_path: explicit/config.toml\n"
	writeFileT(t, store.ConfigPath(), raw)
	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	path := config.skillPathByName("grokbuild")
	if path == nil {
		t.Fatal("grokbuild skill path exists")
	}
	if path.Dir != joinPath(root, "explicit/skills") {
		t.Fatalf("dir：%s", path.Dir)
	}
	if _, err := store.LoadDocument(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := readFileT(t, store.ConfigPath()); got != raw {
		t.Fatalf("load_document 不应改写显式配置：\n got: %q\nwant: %q", got, raw)
	}

	// 旧 schema（targets:）读时迁移成 skill_paths 视图，但文件原样保留：
	// 迁移发生在内存，落盘收敛等到下一次真实写盘。
	old := "targets:\n  grokbuild:\n    skill_dir: isolated/skills\n"
	writeFileT(t, store.ConfigPath(), old)
	document, err := store.LoadDocument()
	if err != nil {
		t.Fatalf("load old: %v", err)
	}
	if document.Config == nil || len(document.Config.SkillPaths) != 1 {
		t.Fatalf("迁移视图 skill_paths：%+v", document.Config)
	}
	if got := readFileT(t, store.ConfigPath()); got != old {
		t.Fatalf("旧配置不该在读路径被改写：\n got: %q\nwant: %q", got, old)
	}
	if config2, err := store.Parse(); err != nil {
		t.Fatalf("parse old: %v", err)
	} else if config2.skillPathByName("grokbuild") == nil {
		t.Fatal("迁移视图里应有 grokbuild")
	}
}

// 与 dsh fixture 同构：MCP 名称/transport/timeout 可注入，skill 路径覆盖
// 全局内置与自定义两种形态。
func grokFixture(t *testing.T, root string, transport string, timeout *uint64) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	timeoutValue := "null"
	if timeout != nil {
		timeoutValue = strconv.FormatUint(*timeout, 10)
	}
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`skill_paths:
  grokbuild:
    dir: global/.grok/skills
  codex:
    dir: codex/skills
mcp_paths:
  grokbuild:
    config_path: global/.grok/config.toml
    config_type: grokbuild
  custom:
    config_path: custom/config.toml
    config_prefix: mcp_servers
    config_type: grokbuild
  codex:
    config_path: codex/config.toml
    config_prefix: mcp_servers
    config_type: common
skill_sources:
  - id: first
    type: local
    root_path: source-a
  - id: second
    type: local
    root_path: source-b
mcps:
- name: probe
  transport: %s
  command: node
  args: [server.js]
  env:
    VALUE: ${VAR:-default}
  url: https://example.invalid/mcp
  headers:
    Authorization: Bearer ${TOKEN}
  timeout: %s
`, transport, timeoutValue))
	return store
}

func TestGrokbuildSkillLinkLifecycle(t *testing.T) {
	root := testsupportTestDir(t, "grok-skills")
	store := grokFixture(t, root, "stdio", nil)
	writeTestSkill(t, filepath.Join(root, "source-a"), "probe", "---\nname: probe\ndescription: synthetic\n---\nA")
	writeTestSkill(t, filepath.Join(root, "source-b"), "probe", "---\nname: probe\ndescription: synthetic\n---\nB")
	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	paths := []string{"grokbuild", "codex"}
	input := SourceSyncInput{
		SourceID:   "first",
		SkillPaths: []string{"probe"},
		PathIDs:    paths,
	}
	result, err := SyncSourceToPathsInner(store, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("applied：%d", len(result.Applied))
	}
	for _, name := range paths {
		dest := filepath.Join(config.skillPathByName(name).Dir, "probe")
		linked, err := readSkillDirLink(dest)
		if err != nil {
			t.Fatalf("readSkillDirLink(%s): %v", dest, err)
		}
		want := linkTarget(t, filepath.Join(root, "source-a", "probe"))
		if linked == nil || *linked != want {
			t.Fatalf("%s 链接应指向 source-a：%+v != %s", name, linked, want)
		}
	}

	input.SourceID = "second"
	conflicts, err := PreviewSourceSyncConflictsInner(store, input)
	if err != nil {
		t.Fatalf("preview conflicts: %v", err)
	}
	if len(conflicts) != 2 {
		t.Fatalf("切换来源后应报两条冲突：%d", len(conflicts))
	}
	if _, err := SyncSourceToPathsInner(store, input); err == nil {
		t.Fatal("不覆盖时同步必须被冲突阻断")
	}
	input.OverwriteExisting = true
	result, err = SyncSourceToPathsInner(store, input)
	if err != nil {
		t.Fatalf("overwrite sync: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("覆盖同步 applied：%d", len(result.Applied))
	}
	removeResult, err := RemoveSourceSyncInner(store, "second", paths)
	if err != nil {
		t.Fatalf("remove sync: %v", err)
	}
	if len(removeResult.Removed) != 2 {
		t.Fatalf("removed：%d", len(removeResult.Removed))
	}
	for _, name := range paths {
		dest := filepath.Join(config.skillPathByName(name).Dir, "probe")
		assertLinkMissing(t, dest)
	}
	if _, err := os.Stat(filepath.Join(root, "source-a", "probe", "SKILL.md")); err != nil {
		t.Fatalf("source-a 内容应完好：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "source-b", "probe", "SKILL.md")); err != nil {
		t.Fatalf("source-b 内容应完好：%v", err)
	}
}

func TestGrokbuildMcpPreviewApplyReadRemovePreservesUnrelatedConfig(t *testing.T) {
	for _, transport := range []string{"stdio", "http", "sse"} {
		root := testsupportTestDir(t, "grok-mcp")
		store := grokFixture(t, root, transport, ptrUint64(2000))
		config, err := store.Parse()
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		for _, name := range []string{"grokbuild", "custom"} {
			path := config.mcpPathByName(name)
			configPath := path.ConfigPath
			writeFileT(t, configPath, "model = 'unchanged'\n[mcp_servers.other]\ncommand = 'keep'\n")

			preview, err := previewMCPCell(mustParseT(t, store), "probe", name)
			if err != nil {
				t.Fatalf("preview %s(%s): %v", name, transport, err)
			}
			if preview.Format != "toml" {
				t.Fatalf("format：%s", preview.Format)
			}
			entry := parseTOMLT(t, preview.Content)
			// MCP 没有启停开关：写入的条目里不该再出现 enabled 键。
			if _, has := entry["enabled"]; has {
				t.Fatalf("条目里不该出现 enabled：%v", entry)
			}
			if entry["tool_timeout_sec"] != int64(2) {
				t.Fatalf("tool_timeout_sec 应为整秒 2：%+v", entry["tool_timeout_sec"])
			}
			if _, has := entry["http_headers"]; has {
				t.Fatalf("grok 形态的 headers 键是 headers，不该出现 http_headers：%v", entry)
			}
			if transport == "stdio" {
				if entry["command"] != "node" || entry["args"].([]any)[0] != "server.js" {
					t.Fatalf("stdio 条目：%v", entry)
				}
				if entry["env"].(map[string]any)["VALUE"] != "${VAR:-default}" {
					t.Fatalf("env：%v", entry["env"])
				}
			} else {
				if entry["url"] != "https://example.invalid/mcp" {
					t.Fatalf("url：%v", entry["url"])
				}
				if entry["headers"].(map[string]any)["Authorization"] != "Bearer ${TOKEN}" {
					t.Fatalf("headers：%v", entry["headers"])
				}
			}

			if _, err := applyMCPCell(mustParseT(t, store), "probe", name); err != nil {
				t.Fatalf("apply %s(%s): %v", name, transport, err)
			}
			first := readFileT(t, configPath)
			if _, err := applyMCPCell(mustParseT(t, store), "probe", name); err != nil {
				t.Fatalf("apply twice %s(%s): %v", name, transport, err)
			}
			if got := readFileT(t, configPath); got != first {
				t.Fatalf("重复 apply 必须幂等（字节一致）：%s(%s)", name, transport)
			}

			entries, err := ReadExistingMCPEntries(path, configPath)
			if err != nil {
				t.Fatalf("read entries %s: %v", name, err)
			}
			if entries["probe"] == nil {
				t.Fatalf("应读到 probe 条目：%v", entries)
			}

			if _, err := removeMCPCell(mustParseT(t, store), "probe", name); err != nil {
				t.Fatalf("remove %s: %v", name, err)
			}
			after := parseTOMLT(t, readFileT(t, configPath))
			original := parseTOMLT(t, "model = 'unchanged'\n[mcp_servers.other]\ncommand = 'keep'\n")
			if !tomlEqual(after, original) {
				t.Fatalf("删除后应还原原配置 %s(%s)：\n%v\n%v", name, transport, after, original)
			}
			mutation, err := removeMCPCell(mustParseT(t, store), "probe", name)
			if err != nil {
				t.Fatalf("remove twice %s: %v", name, err)
			}
			if mutation.Action != "noop" {
				t.Fatalf("二次删除应为 noop：%+v", mutation)
			}
		}
	}
}

func TestGrokbuildTimeoutRejectsFractionalSecondsWithoutChangingCodex(t *testing.T) {
	root := testsupportTestDir(t, "grok-timeout")
	store := grokFixture(t, root, "http", ptrUint64(1500))
	if _, err := previewMCPCell(mustParseT(t, store), "probe", "grokbuild"); err == nil {
		t.Fatal("非整秒必须报错")
	} else if !strings.Contains(err.Error(), "整秒") {
		t.Fatalf("错误文案：%v", err)
	}
	if _, err := applyMCPCell(mustParseT(t, store), "probe", "grokbuild"); err == nil {
		t.Fatal("apply 也必须拒绝非整秒")
	}
	if _, err := os.Stat(filepath.Join(root, "global", ".grok", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("失败路径不得创建文件：%v", err)
	}

	preview, err := previewMCPCell(mustParseT(t, store), "probe", "codex")
	if err != nil {
		t.Fatalf("codex preview: %v", err)
	}
	entry := parseTOMLT(t, preview.Content)
	if got := entry["tool_timeout_sec"]; got != 1.5 {
		t.Fatalf("common 形态写浮点秒：%+v", got)
	}
	if entry["http_headers"].(map[string]any)["Authorization"] != "Bearer ${TOKEN}" {
		t.Fatalf("common 形态的 headers 键是 http_headers：%v", entry)
	}
	if _, has := entry["headers"]; has {
		t.Fatalf("common 形态不该出现 headers 键：%v", entry)
	}

	for _, timeout := range []*uint64{nil, ptrUint64(0), ptrUint64(^uint64(0) - ^uint64(0)%1000)} {
		store := grokFixture(t, root, "stdio", timeout)
		preview, err := previewMCPCell(mustParseT(t, store), "probe", "grokbuild")
		if err != nil {
			t.Fatalf("preview(%v): %v", timeout, err)
		}
		entry := parseTOMLT(t, preview.Content)
		want := int64(0)
		if timeout != nil {
			want = int64(*timeout / 1000)
		}
		got, ok := entry["tool_timeout_sec"]
		if timeout == nil {
			if _, has := entry["tool_timeout_sec"]; has {
				t.Fatalf("无 timeout 不该写 tool_timeout_sec：%v", entry)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("tool_timeout_sec(%v)：%+v != %d", timeout, got, want)
		}
	}
}

func TestGrokbuildRejectsJSONAndMalformedConfigWithoutWriting(t *testing.T) {
	root := testsupportTestDir(t, "grok-invalid-format")
	store := grokFixture(t, root, "stdio", nil)
	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	path := config.mcpPathByName("grokbuild")
	configPath := path.ConfigPath
	for _, content := range []string{`{"mcp_servers":{}}`, "[invalid"} {
		writeFileT(t, configPath, content)
		if _, err := previewMCPCell(mustParseT(t, store), "probe", "grokbuild"); err == nil {
			t.Fatalf("preview(%q) 应报错", content)
		}
		if _, err := applyMCPCell(mustParseT(t, store), "probe", "grokbuild"); err == nil {
			t.Fatalf("apply(%q) 应报错", content)
		}
		if _, err := ReadExistingMCPEntries(path, configPath); err == nil {
			t.Fatalf("read(%q) 应报错", content)
		}
		if _, err := removeMCPCell(mustParseT(t, store), "probe", "grokbuild"); err == nil {
			t.Fatalf("remove(%q) 应报错", content)
		}
		if got := readFileT(t, configPath); got != content {
			t.Fatalf("失败路径不得改写文件：\n got: %q\nwant: %q", got, content)
		}
	}
}
