// Package testsupport：Go 版测试夹具——临时目录与进程环境守卫，对齐
// Rust 版 tests/support.rs 的 TestDir / TestEnvGuard。
//
// 串行说明（对齐 Rust TestEnvLock 的设计注释）：守卫改写的是进程级环境，
// 只对串行测试安全；同包测试默认串行，调用 t.Parallel 的测试不得使用守卫。
// Rust 版用进程级 Mutex 显式串行，Go 侧依赖「同包默认串行 + t.Setenv 拒绝
// 并行」的既有机制，不再引入锁。
package testsupport

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"reins/internal/support"
)

// toolHomeVars：会被路径解析读取的工具重定向变量。SetHome 统一清空，避免
// 真实环境里的重定向漏进测试（断言 home 兜底路径时尤其致命）。
var toolHomeVars = []string{
	"GROK_HOME",
	"PI_CODING_AGENT_DIR",
	"CODEX_HOME",
	"CLAUDE_CONFIG_DIR",
	"DSH_HOME",
}

// TestDir 返回测试专用临时目录（reins-<prefix> 子目录便于排查），测试结束
// 由 t.TempDir 的清理机制自动删除。
func TestDir(t *testing.T, prefix string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "reins-"+prefix)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	return dir
}

// savedVar 记录一个环境变量的原值；set=false 表示原本未设置，还原时要
// 重新 unset（unset 与空串是不同状态：空串会经 FromEnv 归一为未设置，
// 但其他读者未必做同样归一）。
type savedVar struct {
	name  string
	value string
	set   bool
}

// EnvGuard：进程环境量守卫。SetHome/SetPiSessionDir 的每一步都先保存原值，
// Restore 时逆序还原；t.Cleanup 已自动调用 Restore，显式 defer 只用于需要
// 提前还原的场景（Restore 幂等）。
type EnvGuard struct {
	t           *testing.T
	saved       []savedVar
	homeOverrid bool
	restoreOnce sync.Once
}

// SetHome 把 home 指向 tempHome：写进程级 home 覆盖、设置 HOME 与
// REINS_TEST_HOME、清空工具重定向变量，Restore 时全部还原。
//
// HOME 同时改写是为了重定向子进程（opencode CLI、git 等）；REINS_TEST_HOME
// 则承载测试子进程里的 home 解析——子进程拿不到进程内 override，Rust 在
// Windows 上 dirs 忽略 HOME、专门需要这条通道，Go 全平台保留。
func SetHome(t *testing.T, tempHome string) *EnvGuard {
	t.Helper()
	guard := &EnvGuard{t: t}
	for _, name := range toolHomeVars {
		guard.save(name)
		mustUnsetenv(t, name)
	}
	guard.save("HOME")
	mustSetenv(t, "HOME", tempHome)
	guard.save("REINS_TEST_HOME")
	mustSetenv(t, "REINS_TEST_HOME", tempHome)
	guard.homeOverrid = true
	support.SetHomeOverride(tempHome)
	t.Cleanup(guard.Restore)
	return guard
}

// SetPiSessionDir 设置 PI_CODING_AGENT_SESSION_DIR（pi 会话目录的重定向，
// 后续 session 域测试使用），Restore 时还原。
func (g *EnvGuard) SetPiSessionDir(path string) {
	g.t.Helper()
	g.save("PI_CODING_AGENT_SESSION_DIR")
	mustSetenv(g.t, "PI_CODING_AGENT_SESSION_DIR", path)
}

// Restore 逆序还原守卫记录的全部环境量与 home 覆盖；幂等。
func (g *EnvGuard) Restore() {
	g.restoreOnce.Do(func() {
		for i := len(g.saved) - 1; i >= 0; i-- {
			saved := g.saved[i]
			if saved.set {
				_ = os.Setenv(saved.name, saved.value)
			} else {
				_ = os.Unsetenv(saved.name)
			}
		}
		if g.homeOverrid {
			support.SetHomeOverride("")
		}
	})
}

func (g *EnvGuard) save(name string) {
	value, set := os.LookupEnv(name)
	g.saved = append(g.saved, savedVar{name: name, value: value, set: set})
}

func mustSetenv(t *testing.T, name string, value string) {
	t.Helper()
	if err := os.Setenv(name, value); err != nil {
		t.Fatalf("设置 %s 失败: %v", name, err)
	}
}

func mustUnsetenv(t *testing.T, name string) {
	t.Helper()
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("清除 %s 失败: %v", name, err)
	}
}
