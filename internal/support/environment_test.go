package support

import (
	"path/filepath"
	"strings"
	"testing"
)

// 重定向优先、HOME 兜底；空串值与未设置同义（Rust env_path 归一口径）。
func TestEnvironmentResolveRedirectPrecedence(t *testing.T) {
	redirect := Redirect{EnvVar: "REINS_TEST_TOOL_HOME", DefaultDir: ".tool", Label: "tool"}

	withValue := NewEnvironment("/home/u", map[string]string{"REINS_TEST_TOOL_HOME": "/custom"})
	if got, ok := withValue.Resolve(redirect); !ok || got != "/custom" {
		t.Fatalf("重定向应优先：got %q ok=%v", got, ok)
	}

	homeFallback := NewEnvironment("/home/u", nil)
	want := joinSegs("/home/u", ".tool")
	if got, ok := homeFallback.Resolve(redirect); !ok || got != want {
		t.Fatalf("未重定向应落 HOME 兜底：got %q want %q", got, want)
	}

	// 空串视为未设置：不该把路径解析成相对路径。
	empty := NewEnvironment("/home/u", map[string]string{"REINS_TEST_TOOL_HOME": ""})
	if got, ok := empty.Resolve(redirect); !ok || got != want {
		t.Fatalf("空串应等同未设置：got %q ok=%v", got, ok)
	}
}

// home 缺失时 Resolve 返回 false，ResolveRequired 报错且文案主语来自清单。
func TestEnvironmentResolveRequiredError(t *testing.T) {
	redirect := Redirect{EnvVar: "REINS_TEST_TOOL_HOME", DefaultDir: ".tool", Label: "dsh"}
	noHome := NewEnvironment("", nil)
	if got, ok := noHome.Resolve(redirect); ok || got != "" {
		t.Fatalf("home 缺失应解析失败：got %q ok=%v", got, ok)
	}
	_, err := noHome.ResolveRequired(redirect)
	if err == nil || err.Error() != "无法解析 dsh 目录。" {
		t.Fatalf("错误文案应与 Rust 一致：got %v", err)
	}
}

// FromEnv 读进程环境：home 走 UserHomeDir（REINS_TEST_HOME 生效），重定向
// 变量空串归一为未设置。
func TestFromEnvReadsProcessEnv(t *testing.T) {
	t.Setenv("REINS_TEST_HOME", "/reins-fromenv-home")
	t.Setenv("REINS_TEST_TOOL_HOME", "/env-value")
	t.Setenv("REINS_TEST_EMPTY_HOME", "")

	redirects := []Redirect{
		{EnvVar: "REINS_TEST_TOOL_HOME", DefaultDir: ".tool", Label: "tool"},
		{EnvVar: "REINS_TEST_EMPTY_HOME", DefaultDir: ".empty", Label: "empty"},
	}
	env := FromEnv(redirects)

	if env.Home != "/reins-fromenv-home" {
		t.Fatalf("home 应经 UserHomeDir 口径（REINS_TEST_HOME 生效）：got %q", env.Home)
	}
	if got, ok := env.Value(redirects[0]); !ok || got != "/env-value" {
		t.Fatalf("重定向变量应读入：got %q ok=%v", got, ok)
	}
	if _, ok := env.Value(redirects[1]); ok {
		t.Fatalf("空串变量应归一为未设置")
	}
	redirect := redirects[0]
	if got, ok := env.Resolve(redirect); !ok || got != "/env-value" {
		t.Fatalf("Resolve 应取环境值：got %q ok=%v", got, ok)
	}
}

// 清单路径唯一解析：`~/` 前缀以 HOME 为基准（不随 root 搬移），其余相对
// root；root 或 home 缺失时返回 false。
func TestManifestPathSemantics(t *testing.T) {
	env := NewEnvironment("/home/u", nil)

	want := joinSegs("/home/u", "x", "skills")
	if got, ok := ManifestPath(env, "/irrelevant/root", true, "~/x/skills"); !ok || got != want {
		t.Fatalf("`~/` 前缀应落 HOME：got %q ok=%v", got, ok)
	}

	want = joinSegs("/cfgroot", "config.toml")
	if got, ok := ManifestPath(env, "/cfgroot", true, "config.toml"); !ok || got != want {
		t.Fatalf("非 `~/` 应相对 root：got %q ok=%v", got, ok)
	}

	if got, ok := ManifestPath(env, "", false, "config.toml"); ok || got != "" {
		t.Fatalf("root 缺失应返回 false：got %q ok=%v", got, ok)
	}
	if got, ok := ManifestPath(NewEnvironment("", nil), "/r", true, "~/f"); ok || got != "" {
		t.Fatalf("home 缺失时 `~/` 应返回 false：got %q ok=%v", got, ok)
	}
	// `~/` 只依赖 home：root 不可用也不影响。
	want = joinSegs("/home/u", "f")
	if got, ok := ManifestPath(env, "", false, "~/f"); !ok || got != want {
		t.Fatalf("`~/` 不依赖 root：got %q ok=%v", got, ok)
	}
}

func joinSegs(base string, parts ...string) string {
	sep := string(filepath.Separator)
	return base + sep + strings.Join(parts, sep)
}
