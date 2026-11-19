//go:build unix

package support

import "os"

// applyMode：write_atomic 的 unix 权限分支（对齐 Rust #[cfg(unix)] 实现）。
// private 强制 0600；否则复制目标的权限位（含 setuid/setgid/sticky——Rust 的
// mode() & 0o7777 取原始 st_mode 权限段）；目标不存在时保持 umask 默认。
func applyMode(tmpPath string, path string, private bool) error {
	mode := os.FileMode(0o600)
	if !private {
		info, err := os.Stat(path)
		if err != nil {
			return nil // 目标不存在：新文件保持 umask 默认权限（Rust Err(_) => Ok(())）
		}
		raw := info.Mode().Perm()
		if info.Mode()&os.ModeSetuid != 0 {
			raw |= 0o4000
		}
		if info.Mode()&os.ModeSetgid != 0 {
			raw |= 0o2000
		}
		if info.Mode()&os.ModeSticky != 0 {
			raw |= 0o1000
		}
		mode = os.FileMode(raw)
	}
	return os.Chmod(tmpPath, mode)
}
