//go:build !unix

package support

// applyMode：非 unix 平台不做权限处理（对齐 Rust #[cfg(not(unix))] 分支）。
func applyMode(string, string, bool) error {
	return nil
}
