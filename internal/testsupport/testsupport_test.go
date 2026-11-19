package testsupport

import (
	"os"
	"path/filepath"
	"testing"

	"reins/internal/support"
)

// 守卫契约：SetHome 后 home 三件套生效、工具重定向变量清空；Restore 后
// 全部还原，且「原本未设置」的变量保持未设置（unset 与空串是不同状态）。
func TestSetHomeGuardRoundTrip(t *testing.T) {
	original := capture(t)

	dir := TestDir(t, "guard")
	guard := SetHome(t, dir)

	if got := os.Getenv("HOME"); got != dir {
		t.Fatalf("HOME 应指向临时目录：got %q want %q", got, dir)
	}
	if got := os.Getenv("REINS_TEST_HOME"); got != dir {
		t.Fatalf("REINS_TEST_HOME 应指向临时目录（子进程继承通道）：got %q want %q", got, dir)
	}
	for _, name := range toolHomeVars {
		if _, set := os.LookupEnv(name); set {
			t.Fatalf("%s 应被守卫清空", name)
		}
	}
	if got, ok := support.UserHomeDir(); !ok || got != dir {
		t.Fatalf("UserHomeDir 应命中守卫覆盖：got %q ok=%v", got, ok)
	}

	guard.Restore()
	for _, saved := range original {
		got, set := os.LookupEnv(saved.name)
		if set != saved.set || (set && got != saved.value) {
			t.Fatalf("%s 未正确还原：got %q set=%v want %q set=%v", saved.name, got, set, saved.value, saved.set)
		}
	}
	if got, ok := support.UserHomeDir(); ok && got == dir {
		t.Fatalf("Restore 后 home 覆盖应清除：got %q", got)
	}
}

// SetPiSessionDir 走同一份保存/还原账本。
func TestSetPiSessionDirRoundTrip(t *testing.T) {
	original := capture(t)

	dir := TestDir(t, "guard-pi")
	guard := SetHome(t, dir)
	sessionDir := filepath.Join(dir, "sessions")
	guard.SetPiSessionDir(sessionDir)
	if got := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); got != sessionDir {
		t.Fatalf("PI_CODING_AGENT_SESSION_DIR 应生效：got %q", got)
	}
	guard.Restore()
	for _, saved := range original {
		got, set := os.LookupEnv(saved.name)
		if set != saved.set || (set && got != saved.value) {
			t.Fatalf("%s 未正确还原：got %q set=%v want %q set=%v", saved.name, got, set, saved.value, saved.set)
		}
	}
}

func capture(t *testing.T) []savedVar {
	t.Helper()
	names := append([]string{"HOME", "REINS_TEST_HOME"}, toolHomeVars...)
	saved := make([]savedVar, 0, len(names)+1)
	for _, name := range names {
		value, set := os.LookupEnv(name)
		saved = append(saved, savedVar{name: name, value: value, set: set})
	}
	return saved
}
