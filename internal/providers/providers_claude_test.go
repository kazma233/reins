// Claude Code 适配器端到级行为（对齐 tests/providers.rs 的 Claude Code 一节）。
package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reins/internal/testsupport"
)

// 聚合模型对 Claude Code 是「不认识的模型 ID」（输出默认 32000、窗口按内置
// 同名 ID 推断），窗口与输出上限经官方 env 入口显式纠正。
func TestClaudeApplyWritesContextAndOutputEnvAndRemoveClearsThem(t *testing.T) {
	home := newIsolated(t)
	home.seedProviderWithModels(t, "agg", ProtocolAnthropicMessages, "https://agg.test/api", []string{"model-a"})

	home.apply(t, "agg", agentsClaude, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, mustClaudeSettingsPath(t, home.env)))
	if jstr(t, applied, "env", "CLAUDE_CODE_MAX_CONTEXT_TOKENS") != "200000" {
		t.Fatalf("context env: %v", jopt(applied, "env", "CLAUDE_CODE_MAX_CONTEXT_TOKENS"))
	}
	if jstr(t, applied, "env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS") != "64000" {
		t.Fatalf("output env: %v", jopt(applied, "env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"))
	}

	if err := home.removeFrom(t, "agg", agentsClaude); err != nil {
		t.Fatalf("remove: %v", err)
	}
	removed := mustJSON(t, mustRead(t, mustClaudeSettingsPath(t, home.env)))
	if jopt(removed, "env") != nil {
		t.Fatalf("env 段应随移除清掉：%v", removed["env"])
	}
}

func TestClaudeApplyPreservesKeysAndRemoveMatchesBaseURL(t *testing.T) {
	home := newIsolated(t)
	path := mustClaudeSettingsPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, `{"theme":"dark"}`)
	home.seedProvider(t, "agg", ProtocolAnthropicMessages, "https://agg.test/api")

	home.apply(t, "agg", agentsClaude, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	applied := mustJSON(t, mustRead(t, path))
	if jstr(t, applied, "theme") != "dark" {
		t.Fatalf("用户自己的键应保留：%v", applied["theme"])
	}
	if jstr(t, applied, "env", "ANTHROPIC_BASE_URL") != "https://agg.test/api" {
		t.Fatalf("base url: %v", jopt(applied, "env", "ANTHROPIC_BASE_URL"))
	}
	if jstr(t, applied, "env", "ANTHROPIC_AUTH_TOKEN") != "sk-test-secret" {
		t.Fatalf("auth token: %v", jopt(applied, "env", "ANTHROPIC_AUTH_TOKEN"))
	}
	if jstr(t, applied, "model") != "model-a" {
		t.Fatalf("model: %v", applied["model"])
	}
	if jstr(t, applied, "effortLevel") != "high" {
		t.Fatalf("effortLevel: %v", applied["effortLevel"])
	}

	// base_url 一致 → 连带清理 model / effortLevel，保留用户自己的键。
	if err := home.removeFrom(t, "agg", agentsClaude); err != nil {
		t.Fatalf("remove: %v", err)
	}
	removed := mustJSON(t, mustRead(t, path))
	if jstr(t, removed, "theme") != "dark" {
		t.Fatalf("theme 应保留：%v", removed["theme"])
	}
	if jopt(removed, "env") != nil || jopt(removed, "model") != nil || jopt(removed, "effortLevel") != nil {
		t.Fatalf("写入的键应随移除清掉：%+v", removed)
	}
}

func TestClaudeRemoveRefusesMismatchedOrOrphanedBaseURL(t *testing.T) {
	home := newIsolated(t)
	path := mustClaudeSettingsPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, `{"env":{"ANTHROPIC_BASE_URL":"https://official"}}`)
	home.seedProvider(t, "agg", ProtocolAnthropicMessages, "https://agg.test/api")

	// base_url 不一致：拒绝且不改文件。
	if err := home.removeFrom(t, "agg", agentsClaude); err == nil {
		t.Fatal("base_url 不一致应拒绝移除")
	}
	if got := mustRead(t, path); got != `{"env":{"ANTHROPIC_BASE_URL":"https://official"}}` {
		t.Fatalf("拒绝时不应改文件：%s", got)
	}

	// 平台元数据已删除：无法安全移除，直接报错。
	if err := home.store.Delete("agg"); err != nil {
		t.Fatal(err)
	}
	if err := home.removeFrom(t, "agg", agentsClaude); err == nil {
		t.Fatal("平台已删除时应拒绝移除")
	}
}

func TestClaudeExternalBaseURLShowsAsExternal(t *testing.T) {
	home := newIsolated(t)
	path := mustClaudeSettingsPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, `{"env":{"ANTHROPIC_BASE_URL":"https://someone-else"}}`)
	state := home.state(t)
	claude := findAppState(t, state, agentsClaude)
	if len(claude.Entries) != 1 {
		t.Fatalf("entries: %d", len(claude.Entries))
	}
	if claude.Entries[0].Status != EntryStatusExternal {
		t.Fatalf("status: %s", claude.Entries[0].Status)
	}
}

// 认证键为 ANTHROPIC_AUTH_TOKEN（Bearer 头）；旧版写过的 ANTHROPIC_API_KEY
// 随重新应用清理（两个凭据头并存会被部分网关拒绝），反读时给出提示。
func TestClaudeApplyCleansLegacyAPIKeyAndNotesIt(t *testing.T) {
	home := newIsolated(t)
	path := mustClaudeSettingsPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, `{"env":{"ANTHROPIC_API_KEY":"old-key"}}`)
	home.seedProvider(t, "agg", ProtocolAnthropicMessages, "https://agg.test/api")

	home.apply(t, "agg", agentsClaude, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, path))
	if jstr(t, applied, "env", "ANTHROPIC_AUTH_TOKEN") != "sk-test-secret" {
		t.Fatalf("auth token: %v", jopt(applied, "env", "ANTHROPIC_AUTH_TOKEN"))
	}
	if jopt(applied, "env", "ANTHROPIC_API_KEY") != nil {
		t.Fatal("遗留的 ANTHROPIC_API_KEY 应随应用清理")
	}

	// 手工塞回遗留键 → 反读出现提示；移除时两个认证键一并清理。
	withLegacy := mustJSON(t, mustRead(t, path))
	envTable := jpath(t, withLegacy, "env").(map[string]any)
	envTable["ANTHROPIC_API_KEY"] = "leftover"
	mustWriteJSON(t, path, withLegacy)
	state := home.state(t)
	claude := findAppState(t, state, agentsClaude)
	noted := false
	for _, note := range claude.Entries[0].Notes {
		if strings.Contains(note, "ANTHROPIC_API_KEY") {
			noted = true
			break
		}
	}
	if !noted {
		t.Fatalf("反读应提示遗留键：%v", claude.Entries[0].Notes)
	}

	if err := home.removeFrom(t, "agg", agentsClaude); err != nil {
		t.Fatalf("remove: %v", err)
	}
	removed := mustJSON(t, mustRead(t, path))
	if jopt(removed, "env") != nil {
		t.Fatalf("env 段应清掉：%v", removed["env"])
	}
}

// CLAUDE_CONFIG_DIR 重定位配置目录（官方支持），与 CODEX_HOME 等同一模式。
func TestClaudeHonorsClaudeConfigDir(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "agg", ProtocolAnthropicMessages, "https://agg.test/api")
	customDir := testsupportTestDir(t, "claude-config-dir")
	// 只覆盖被检验的字段，其余保持夹具环境（home 仍是临时目录）。
	env := envWith(home.env, "CLAUDE_CONFIG_DIR", customDir)

	if _, err := applyProviderInner(home.store, env,
		plan("agg", agentsClaude, []string{"model-a"}, "model-a", nil)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	settings := filepath.Join(customDir, "settings.json")
	applied := mustJSON(t, mustRead(t, settings))
	if jstr(t, applied, "env", "ANTHROPIC_BASE_URL") != "https://agg.test/api" {
		t.Fatalf("重定向目录应被使用：%v", applied["env"])
	}
	// 默认路径不受影响：未在 ~/.claude 下创建文件。
	mustNotExist(t, mustClaudeSettingsPath(t, home.env))
}

// ---------------------------------------------------------------------------
// 夹具小工具
// ---------------------------------------------------------------------------

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("创建目录 %s: %v", dir, err)
	}
}

func testsupportTestDir(t *testing.T, prefix string) string {
	t.Helper()
	return testsupport.TestDir(t, prefix)
}
