// workspace_grokbuild_test.go：grokbuild target 的模板/预设一致性、显式路径
// 优先、skill 链接生命周期与 TOML MCP 读写（对齐 tests/workspace_grokbuild.rs）。
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

func TestGrokbuildDefaultsTemplateAndPresetAgree(t *testing.T) {
	_, dir := newTestStore(t, "grok-defaults")
	grokRoot := filepath.Join(dir, "grok-home")
	if err := os.MkdirAll(grokRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("GROK_HOME", grokRoot)
	env := envOf(t)

	path := filepath.Join(t.TempDir(), "reins-grok-defaults.yaml")
	config, err := parseManagerConfig(DefaultConfigTemplate(env), path, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := config.targetByID("grokbuild")
	if target == nil {
		t.Fatal("grokbuild target exists")
	}
	root := grokRoot
	if _, redirected := env.Value(agents.RootGrokHome.Redirect); !redirected {
		home, ok := support.UserHomeDir()
		if !ok {
			t.Fatal("测试环境缺少 HOME")
		}
		root = joinPath(home, ".grok")
	}
	if target.SkillDir != joinPath(root, "skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
	if target.ConfigPath == nil || *target.ConfigPath != joinPath(root, "config.toml") {
		t.Fatalf("config_path：%+v", target.ConfigPath)
	}
	if target.MCPConfigPrefix != "mcp_servers" || target.MCPConfigType != agents.McpConfigGrokBuild {
		t.Fatalf("mcp 布局：%+v", target)
	}
	// 预设列表是封闭集合（七个内置工具），不存在“未知内置 target”分支。
	presets := TargetPresetsInner(envOf(t))
	var preset *TargetPreset
	for i := range presets {
		if string(presets[i].TargetID) == "grokbuild" {
			preset = &presets[i]
		}
	}
	if preset == nil {
		t.Fatal("缺少 grokbuild 预设")
	}
	if preset.SkillDir != joinPath(root, "skills") {
		t.Fatalf("preset skill_dir：%s", preset.SkillDir)
	}
	if preset.ConfigPath == nil || *preset.ConfigPath != joinPath(root, "config.toml") {
		t.Fatalf("preset config_path：%+v", preset.ConfigPath)
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

func TestGrokbuildExplicitPathsWinAndOldConfigIsUnchanged(t *testing.T) {
	store, root := newTestStore(t, "grok-explicit")
	raw := "targets:\n  grokbuild:\n    skill_dir: explicit/skills\n    mcp:\n      config_path: explicit/config.toml\n"
	writeFileT(t, store.ConfigPath(), raw)
	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := config.targetByID("grokbuild")
	if target == nil {
		t.Fatal("grokbuild target exists")
	}
	if target.SkillDir != filepath.Join(root, "explicit", "skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
	if target.ConfigPath == nil || *target.ConfigPath != filepath.Join(root, "explicit", "config.toml") {
		t.Fatalf("config_path：%+v", target.ConfigPath)
	}
	if _, err := store.LoadDocument(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := readFileT(t, store.ConfigPath()); got != raw {
		t.Fatalf("load_document 不应改写显式配置：\n got: %q\nwant: %q", got, raw)
	}

	old := "targets:\n  pi:\n    enabled: true\n    skill_dir: isolated/skills\n"
	writeFileT(t, store.ConfigPath(), old)
	document, err := store.LoadDocument()
	if err != nil {
		t.Fatalf("load old: %v", err)
	}
	if document.Config == nil || len(document.Config.Targets) != 1 {
		t.Fatalf("old config targets：%+v", document.Config)
	}
	if got := readFileT(t, store.ConfigPath()); got != old {
		t.Fatalf("旧配置不该被模板改写：\n got: %q\nwant: %q", got, old)
	}
	if config2, err := store.Parse(); err != nil {
		t.Fatalf("parse old: %v", err)
	} else if config2.targetByID("grokbuild") != nil {
		t.Fatal("旧配置里不该有 grokbuild")
	}
}

// 与 dsh fixture 同构：MCP 名称/transport/timeout 可注入，target 覆盖全局
// 内置、自定义与项目级三种形态。
func grokFixture(t *testing.T, root string, transport string, timeout *uint64) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	timeoutValue := "null"
	if timeout != nil {
		timeoutValue = strconv.FormatUint(*timeout, 10)
	}
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  grokbuild:
    skill_dir: global/.grok/skills
    mcp:
      config_path: global/.grok/config.toml
  custom:
    skill_dir: custom/skills
    mcp:
      config_path: custom/config.toml
      config_prefix: mcp_servers
      config_type: grokbuild
  codex:
    skill_dir: codex/skills
    mcp:
      config_path: codex/config.toml
projects:
  demo:
    path: project
    agents:
      grokbuild:
        enabled: true
skill_sources:
  - id: first
    type: local
    root_path: source-a
  - id: second
    type: local
    root_path: source-b
mcps:
- name: probe
  enabled: false
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

func TestGrokbuildProjectLayoutAndSkillLinkLifecycle(t *testing.T) {
	root := testsupportTestDir(t, "grok-skills")
	store := grokFixture(t, root, "stdio", nil)
	writeTestSkill(t, filepath.Join(root, "source-a"), "probe", "---\nname: probe\ndescription: synthetic\n---\nA")
	writeTestSkill(t, filepath.Join(root, "source-b"), "probe", "---\nname: probe\ndescription: synthetic\n---\nB")
	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	project := config.targetByID("demo:grokbuild")
	if project == nil {
		t.Fatal("demo:grokbuild target exists")
	}
	if project.SkillDir != filepath.Join(root, "project", ".grok", "skills") {
		t.Fatalf("project skill_dir：%s", project.SkillDir)
	}
	if project.ConfigPath == nil || *project.ConfigPath != filepath.Join(root, "project", ".grok", "config.toml") {
		t.Fatalf("project config_path：%+v", project.ConfigPath)
	}
	if project.MCPConfigType != agents.McpConfigGrokBuild {
		t.Fatalf("project config_type：%s", project.MCPConfigType)
	}

	targets := []string{"grokbuild", "demo:grokbuild"}
	input := SourceSyncInput{
		SourceID:   "first",
		SkillPaths: []string{"probe"},
		TargetIDs:  targets,
	}
	result, err := SyncSourceToTargetsInner(store, input)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("applied：%d", len(result.Applied))
	}
	for _, id := range targets {
		dest := filepath.Join(config.targetByID(id).SkillDir, "probe")
		linked, err := readSkillDirLink(dest)
		if err != nil {
			t.Fatalf("readSkillDirLink(%s): %v", dest, err)
		}
		want := linkTarget(t, filepath.Join(root, "source-a", "probe"))
		if linked == nil || *linked != want {
			t.Fatalf("%s 链接应指向 source-a：%+v != %s", id, linked, want)
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
	if _, err := SyncSourceToTargetsInner(store, input); err == nil {
		t.Fatal("不覆盖时同步必须被冲突阻断")
	}
	input.OverwriteExisting = true
	result, err = SyncSourceToTargetsInner(store, input)
	if err != nil {
		t.Fatalf("overwrite sync: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Fatalf("覆盖同步 applied：%d", len(result.Applied))
	}
	removeResult, err := RemoveSourceSyncInner(store, "second", targets)
	if err != nil {
		t.Fatalf("remove sync: %v", err)
	}
	if len(removeResult.Removed) != 2 {
		t.Fatalf("removed：%d", len(removeResult.Removed))
	}
	for _, id := range targets {
		dest := filepath.Join(config.targetByID(id).SkillDir, "probe")
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
		for _, id := range []string{"grokbuild", "custom", "demo:grokbuild"} {
			target := config.targetByID(id)
			path := *target.ConfigPath
			writeFileT(t, path, "model = 'unchanged'\n[mcp_servers.other]\ncommand = 'keep'\n")

			preview, err := PreviewMCPTargetInner(store, "probe", id)
			if err != nil {
				t.Fatalf("preview %s(%s): %v", id, transport, err)
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

			if _, err := ApplyMCPToTargetInner(store, "probe", id); err != nil {
				t.Fatalf("apply %s(%s): %v", id, transport, err)
			}
			first := readFileT(t, path)
			if _, err := ApplyMCPToTargetInner(store, "probe", id); err != nil {
				t.Fatalf("apply twice %s(%s): %v", id, transport, err)
			}
			if got := readFileT(t, path); got != first {
				t.Fatalf("重复 apply 必须幂等（字节一致）：%s(%s)", id, transport)
			}

			env := &support.Environment{}
			entries, err := ReadExistingMCPEntries(target, path, env)
			if err != nil {
				t.Fatalf("read entries %s: %v", id, err)
			}
			if entries["probe"] == nil {
				t.Fatalf("应读到 probe 条目：%v", entries)
			}

			if _, err := RemoveMCPFromTargetInner(store, "probe", id); err != nil {
				t.Fatalf("remove %s: %v", id, err)
			}
			after := parseTOMLT(t, readFileT(t, path))
			original := parseTOMLT(t, "model = 'unchanged'\n[mcp_servers.other]\ncommand = 'keep'\n")
			if !tomlEqual(after, original) {
				t.Fatalf("删除后应还原原配置 %s(%s)：\n%v\n%v", id, transport, after, original)
			}
			mutation, err := RemoveMCPFromTargetInner(store, "probe", id)
			if err != nil {
				t.Fatalf("remove twice %s: %v", id, err)
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
	if _, err := PreviewMCPTargetInner(store, "probe", "grokbuild"); err == nil {
		t.Fatal("非整秒必须报错")
	} else if !strings.Contains(err.Error(), "整秒") {
		t.Fatalf("错误文案：%v", err)
	}
	if _, err := ApplyMCPToTargetInner(store, "probe", "grokbuild"); err == nil {
		t.Fatal("apply 也必须拒绝非整秒")
	}
	if _, err := os.Stat(filepath.Join(root, "global", ".grok", "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("失败路径不得创建文件：%v", err)
	}

	preview, err := PreviewMCPTargetInner(store, "probe", "codex")
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
		preview, err := PreviewMCPTargetInner(store, "probe", "grokbuild")
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
	target := config.targetByID("grokbuild")
	path := *target.ConfigPath
	env := &support.Environment{}
	for _, content := range []string{`{"mcp_servers":{}}`, "[invalid"} {
		writeFileT(t, path, content)
		if _, err := PreviewMCPTargetInner(store, "probe", "grokbuild"); err == nil {
			t.Fatalf("preview(%q) 应报错", content)
		}
		if _, err := ApplyMCPToTargetInner(store, "probe", "grokbuild"); err == nil {
			t.Fatalf("apply(%q) 应报错", content)
		}
		if _, err := ReadExistingMCPEntries(target, path, env); err == nil {
			t.Fatalf("read(%q) 应报错", content)
		}
		if _, err := RemoveMCPFromTargetInner(store, "probe", "grokbuild"); err == nil {
			t.Fatalf("remove(%q) 应报错", content)
		}
		if got := readFileT(t, path); got != content {
			t.Fatalf("失败路径不得改写文件：\n got: %q\nwant: %q", got, content)
		}
	}
}
