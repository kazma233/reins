// 外部测试包：本文件同时使用 testsupport 夹具，而 testsupport 依赖 agents
// （SetHome 的清空清单从 Redirects 派生），内部测试包会构成导入环；点导入
// 让正文保持对包内符号的直接引用。
package agents_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

// AgentSpec 清单自身的一致性：键与各域 id 唯一、ProviderApps 全覆盖。
// （Rust agents_registry_is_consistent）
func TestAgentsRegistryConsistency(t *testing.T) {
	keys := map[string]bool{}
	targets := map[string]bool{}
	providers := map[ProviderApp]bool{}
	wireIDs := map[string]bool{}
	for i := range AgentSpecs {
		spec := &AgentSpecs[i]
		if keys[spec.Key] {
			t.Fatalf("key 重复：%s", spec.Key)
		}
		keys[spec.Key] = true
		if spec.TargetID != "" {
			if targets[spec.TargetID] {
				t.Fatalf("target_id 重复：%s", spec.TargetID)
			}
			targets[spec.TargetID] = true
		}
		if spec.ProviderApp != "" {
			if providers[spec.ProviderApp] {
				t.Fatalf("provider_app 重复：%s", spec.ProviderApp)
			}
			providers[spec.ProviderApp] = true
		}
		if spec.SourceApp != "" {
			if wireIDs[string(spec.SourceApp)] {
				t.Fatalf("wire id 重复：%s", spec.SourceApp)
			}
			wireIDs[string(spec.SourceApp)] = true
		}
	}

	for _, app := range ProviderApps() {
		if SpecByProviderApp(app) == nil {
			t.Fatalf("ProviderApps 的 %s 未登记 agents 清单", app)
		}
	}

	// 清单驱动的重定向变量表：恰好五个官方变量、无重复（GLOSSARY 红线：
	// support 不许再写第二张表，这里就是唯一那张）。
	want := map[string]bool{
		"GROK_HOME": true, "PI_CODING_AGENT_DIR": true, "CODEX_HOME": true,
		"CLAUDE_CONFIG_DIR": true, "DSH_HOME": true,
	}
	got := map[string]bool{}
	for _, redirect := range Redirects() {
		if got[redirect.EnvVar] {
			t.Fatalf("重定向变量重复：%s", redirect.EnvVar)
		}
		got[redirect.EnvVar] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("重定向变量缺失：%s", name)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("重定向变量清单不符：got %v", got)
	}
}

// 查询入口按 wire id / provider 键命中同一份产品名。
func TestSpecLookupDerivesLabel(t *testing.T) {
	if spec := SpecBySourceApp(SourceClaudeCode); spec == nil || spec.Key != "claude" || spec.Label != "Claude Code" {
		t.Fatalf("claude_code 应命中 claude：got %v", spec)
	}
	if spec := SpecByProviderApp(ProviderGrokbuild); spec == nil || spec.Label != "Grok Build" {
		t.Fatalf("grokbuild 应命中 Grok Build：got %v", spec)
	}
	if SpecBySourceApp("nope") != nil || SpecByProviderApp("nope") != nil {
		t.Fatalf("未知 id 应返回 nil")
	}
}

// PROVIDER_APPS 的顺序与派生产品名（Rust snapshot_provider_app_labels）。
func TestProviderAppsOrderAndLabels(t *testing.T) {
	want := []struct {
		app   ProviderApp
		label string
	}{
		{ProviderCodex, "Codex"},
		{ProviderClaude, "Claude Code"},
		{ProviderOpencode, "OpenCode"},
		{ProviderPi, "Pi"},
		{ProviderGrokbuild, "Grok Build"},
		{ProviderDsh, "DeepSeek Harness"},
		{ProviderZcode, "ZCode"},
	}
	got := ProviderApps()
	if len(got) != len(want) {
		t.Fatalf("ProviderApps 长度不符：got %d want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w.app {
			t.Fatalf("ProviderApps[%d] = %s want %s", i, got[i], w.app)
		}
		spec := SpecByProviderApp(got[i])
		if spec == nil || spec.Label != w.label {
			t.Fatalf("派生产品名不符：%s got %v", got[i], spec)
		}
	}
}

func templateName(entry TemplateEntry) string {
	switch entry {
	case TemplateStandard:
		return "Standard"
	case TemplateFollowEnv:
		return "FollowEnv"
	case TemplateAbsent:
		return "Absent"
	default:
		return fmt.Sprintf("TemplateEntry(%d)", uint8(entry))
	}
}

func projectPath(layout *ProjectLayout) string {
	if layout == nil {
		return "-"
	}
	return layout.SkillDir
}

func projectMcp(layout *ProjectLayout) string {
	if layout == nil || layout.McpConfigPath == "" {
		return "-"
	}
	return layout.McpConfigPath
}

// 派生等价快照：锁住清单现状的完整输出（字段值与顺序）。清单任何字段
// 漂移都在这里失败，而不是悄悄改变各域派生结果。
func TestAgentSpecSnapshot(t *testing.T) {
	want := []string{
		"codex|Codex|codex|codex|codex|mcp_servers|common|codex_home|~/.agents/skills|config.toml|.agents/skills|.codex/config.toml|Standard",
		"claude|Claude Code|claude_code|claude|claude|mcpServers|common|claude_config_dir|skills|.claude.json|.claude/skills|.mcp.json|Standard",
		"opencode|OpenCode|opencode|opencode|opencode|mcp.servers|opencode|home|.config/opencode/skills|.config/opencode/opencode.json|.opencode/skills|opencode.json|Standard",
		"zcode|ZCode|zcode|zcode|zcode|mcp.servers|common|home|.zcode/skills|.zcode/cli/config.json|.zcode/skills|.zcode/config.json|Standard",
		"grokbuild|Grok Build|grokbuild|grokbuild|grokbuild|mcp_servers|grokbuild|grok_home|skills|config.toml|.grok/skills|.grok/config.toml|FollowEnv",
		"pi|Pi|pi|pi|pi|mcpServers|common|pi_agent_dir|skills|mcp.json|.pi/skills|.pi/mcp.json|Standard",
		"dsh|DeepSeek Harness|dsh|dsh|dsh||dsh|dsh_home|skills|cordis.patch.yml|.dsh/skills|-|Absent",
	}
	if len(AgentSpecs) != len(want) {
		t.Fatalf("清单条目数不符：got %d want %d", len(AgentSpecs), len(want))
	}
	for i, w := range want {
		spec := &AgentSpecs[i]
		globalMcp := spec.Global.McpConfigPath
		if globalMcp == "" {
			globalMcp = "-"
		}
		got := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
			spec.Key, spec.Label, string(spec.SourceApp), spec.TargetID, string(spec.ProviderApp),
			spec.Mcp.Prefix, string(spec.Mcp.ConfigType),
			spec.Global.Root.Name, spec.Global.SkillDir, globalMcp,
			projectPath(spec.Project), projectMcp(spec.Project),
			templateName(spec.Template),
		)
		if got != w {
			t.Fatalf("清单快照漂移[%d]：\ngot  %s\nwant %s", i, got, w)
		}
	}
}

// 未重定向时的 ~ 缩写前缀（默认模板路径与注释文案用）。
func TestGlobalRootHomePrefix(t *testing.T) {
	want := map[string]string{
		"home":              "~",
		"grok_home":         "~/.grok",
		"pi_agent_dir":      "~/.pi/agent",
		"codex_home":        "~/.codex",
		"claude_config_dir": "~/.claude",
		"dsh_home":          "~/.dsh",
	}
	for i := range AgentSpecs {
		root := AgentSpecs[i].Global.Root
		if root.HomePrefix() != want[root.Name] {
			t.Fatalf("HomePrefix(%s) = %q want %q", root.Name, root.HomePrefix(), want[root.Name])
		}
	}
}

func joinSegs(base string, parts ...string) string {
	sep := string(filepath.Separator)
	return base + sep + strings.Join(parts, sep)
}

// 未重定向的根解析：五个重定向根落 HOME 默认目录，Home 根落 HOME 本身
// （Rust snapshot_builtin_target_defaults 的根级子集）。
func TestGlobalRootResolveDefaultsToHome(t *testing.T) {
	home := testsupport.TestDir(t, "agents-roots-default")
	guard := testsupport.SetHome(t, home)
	defer guard.Restore()
	env := support.FromEnv(Redirects())

	want := map[string]string{
		"home": home,
		// pi 的默认目录是 ".pi/agent"：Resolve 以平台分隔符接 HOME、清单
		// 拼写原样保留（对齐 Rust），不清洗内嵌的 /。
		"pi_agent_dir":      home + string(filepath.Separator) + ".pi/agent",
		"grok_home":         joinSegs(home, ".grok"),
		"codex_home":        joinSegs(home, ".codex"),
		"claude_config_dir": joinSegs(home, ".claude"),
		"dsh_home":          joinSegs(home, ".dsh"),
	}
	for i := range AgentSpecs {
		root := AgentSpecs[i].Global.Root
		got, ok := root.Resolve(env)
		if !ok || got != want[root.Name] {
			t.Fatalf("Resolve(%s) = %q ok=%v want %q", root.Name, got, ok, want[root.Name])
		}
	}
}

// 重定向变量生效：被设置的根跟变量走，其余仍落 HOME 默认。
// （Rust snapshot_builtin_target_defaults_with_env_redirect）
func TestGlobalRootResolveWithRedirectVars(t *testing.T) {
	home := testsupport.TestDir(t, "agents-roots-redirect")
	guard := testsupport.SetHome(t, home)
	defer guard.Restore()
	grokHome := filepath.Join(home, "custom-grok")
	piDir := filepath.Join(home, "custom-pi")
	t.Setenv("GROK_HOME", grokHome)
	t.Setenv("PI_CODING_AGENT_DIR", piDir)
	env := support.FromEnv(Redirects())

	if got, _ := RootGrokHome.Resolve(env); got != grokHome {
		t.Fatalf("GROK_HOME 应生效：got %q want %q", got, grokHome)
	}
	if got, _ := RootPiAgentDir.Resolve(env); got != piDir {
		t.Fatalf("PI_CODING_AGENT_DIR 应生效：got %q want %q", got, piDir)
	}
	// 未设置的变量不受影响：codex 仍落 HOME 默认目录。
	if got, _ := RootCodexHome.Resolve(env); got != joinSegs(home, ".codex") {
		t.Fatalf("CODEX_HOME 未设置应落 HOME：got %q", got)
	}
}

// 其余三个重定向根与 grok / pi 同一口径：workspace 认的落点必须与各工具
// 官方变量一致，否则同一个配置文件会被两个域解析到两个位置。
// （Rust snapshot_builtin_target_defaults_with_codex_claude_dsh_redirect）
func TestGlobalRootResolveCodexClaudeDshRedirect(t *testing.T) {
	home := testsupport.TestDir(t, "agents-roots-redirect-more")
	guard := testsupport.SetHome(t, home)
	defer guard.Restore()
	codexHome := filepath.Join(home, "custom-codex")
	claudeDir := filepath.Join(home, "custom-claude")
	dshHome := filepath.Join(home, "custom-dsh")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("DSH_HOME", dshHome)
	env := support.FromEnv(Redirects())

	if got, _ := RootCodexHome.Resolve(env); got != codexHome {
		t.Fatalf("CODEX_HOME 应生效：got %q want %q", got, codexHome)
	}
	if got, _ := RootClaudeConfigDir.Resolve(env); got != claudeDir {
		t.Fatalf("CLAUDE_CONFIG_DIR 应生效：got %q want %q", got, claudeDir)
	}
	if got, _ := RootDshHome.Resolve(env); got != dshHome {
		t.Fatalf("DSH_HOME 应生效：got %q want %q", got, dshHome)
	}
}

// 清单路径的双基准：`~/` 前缀恒落 HOME（codex skills 不随 CODEX_HOME），
// 其余随 GlobalRoot 的解析结果走（claude 全部落点随 CLAUDE_CONFIG_DIR）。
func TestGlobalRootResolvePathBasis(t *testing.T) {
	home := testsupport.TestDir(t, "agents-resolve-path")
	guard := testsupport.SetHome(t, home)
	defer guard.Restore()
	codexHome := filepath.Join(home, "custom-codex")
	claudeDir := filepath.Join(home, "custom-claude")
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	env := support.FromEnv(Redirects())

	cases := []struct {
		root GlobalRoot
		path string
		want string
	}{
		{RootCodexHome, "~/.agents/skills", joinSegs(home, ".agents", "skills")},
		{RootCodexHome, "config.toml", joinSegs(codexHome, "config.toml")},
		{RootClaudeConfigDir, "skills", joinSegs(claudeDir, "skills")},
		{RootClaudeConfigDir, ".claude.json", joinSegs(claudeDir, ".claude.json")},
		{RootHome, ".zcode/cli/config.json", joinSegs(home, ".zcode", "cli", "config.json")},
	}
	for _, c := range cases {
		got, ok := c.root.ResolvePath(env, c.path)
		if !ok || got != c.want {
			t.Fatalf("ResolvePath(%s, %q) = %q ok=%v want %q", c.root.Name, c.path, got, ok, c.want)
		}
	}
}

// 空串与未设置同义（providers 读同一口径）：空值不该把路径解析成相对路径。
// （Rust empty_env_redirect_falls_back_to_home）
func TestEmptyEnvRedirectFallsBackToHome(t *testing.T) {
	home := testsupport.TestDir(t, "agents-empty-env")
	guard := testsupport.SetHome(t, home)
	defer guard.Restore()
	t.Setenv("CODEX_HOME", "")
	env := support.FromEnv(Redirects())

	spec := SpecByProviderApp(ProviderCodex)
	if spec == nil {
		t.Fatal("codex defaults exist")
	}
	got, ok := spec.Global.Root.ResolvePath(env, spec.Global.McpConfigPath)
	if !ok || got != joinSegs(home, ".codex", "config.toml") {
		t.Fatalf("空 CODEX_HOME 应回落 HOME：got %q ok=%v", got, ok)
	}
	got, ok = spec.Global.Root.ResolvePath(env, spec.Global.SkillDir)
	if !ok || got != joinSegs(home, ".agents", "skills") {
		t.Fatalf("skills 恒落 HOME：got %q ok=%v", got, ok)
	}
}

// 与 Rust export_bindings_agent_labels 同一模式的导出测试：渲染输出与
// frontend/src/shared/lib/agent-labels.ts 必须逐字节一致；不一致时覆写前端
// 文件（迁移后的 codegen 通道）并失败退出，让人按 diff 校对前端消费点。
func TestExportBindingsAgentLabelsByteIdentical(t *testing.T) {
	content := RenderAgentLabelsTS()
	path := filepath.Join("..", "..", "frontend", "src", "shared", "lib", "agent-labels.ts")
	current, err := os.ReadFile(path)
	if err == nil && string(current) == content {
		return
	}
	if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
		t.Fatalf("写入 agent-labels.ts 失败: %v", writeErr)
	}
	if err != nil {
		t.Fatalf("agent-labels.ts 缺失（%v），已重新生成，请再次运行确认", err)
	}
	t.Fatal("渲染输出与 frontend/src/shared/lib/agent-labels.ts 不一致，已覆写，请检查 git diff")
}
