//go:build unix

package support

import (
	"os"
	"path/filepath"
	"testing"
)

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// 覆盖既有文件时权限必须随目标保留：临时文件是 umask 默认权限，不处理后
// rename 会把目标权限静默放宽。（Rust write_atomic_preserves_existing_permissions）
func TestWriteAtomicPreservesExistingPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomic(path, "new"); err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil || string(content) != "new" {
		t.Fatalf("内容不符: %q err=%v", content, err)
	}
	if got := modeOf(t, path); got != 0o640 {
		t.Fatalf("权限应保留 0640：got %o", got)
	}
}

// 敏感文件无论覆盖还是新建都强制 0600：覆盖场景顺带修复被旧版本写坏的
// 权限，dsh 的启动守卫要求凭据文件必须 owner-only。
// （Rust write_atomic_private_forces_owner_only）
func TestWriteAtomicPrivateForcesOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, ".credentials.yaml")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteAtomicPrivate(existing, "new"); err != nil {
		t.Fatal(err)
	}
	if got := modeOf(t, existing); got != 0o600 {
		t.Fatalf("覆盖后应强制 0600：got %o", got)
	}

	fresh := filepath.Join(dir, "fresh-credentials.yaml")
	if err := WriteAtomicPrivate(fresh, "new"); err != nil {
		t.Fatal(err)
	}
	if got := modeOf(t, fresh); got != 0o600 {
		t.Fatalf("新建应强制 0600：got %o", got)
	}
}
