//go:build !windows

// 非 Windows 平台的目录链接探测与建删：只有软链接一种形态，junction 分支
// 恒空（Windows 的实现见 link_windows.go）。
package workspace

import (
	"fmt"
	"os"
)

// readPlatformDirectoryLinkTarget：非 Windows 没有 junction/reparse point，
// 普通目录恒返回 nil。
func readPlatformDirectoryLinkTarget(path string, metadata os.FileInfo) (*string, error) {
	_ = path
	_ = metadata
	return nil, nil
}

// readPlatformBrokenLinkTarget：非 Windows 断链没有可读数据。
func readPlatformBrokenLinkTarget(path string) (*string, error) {
	_ = path
	return nil, nil
}

// removePlatformDirectoryLink：非 Windows 没有独立目录链接形态，恒 false
// （软链接已在 removeExistingPath 的 symlink 分支处理）。
func removePlatformDirectoryLink(path string) (bool, error) {
	_ = path
	return false, nil
}

// createDirectorySymlink：Unix 目标目录链接即符号链接。
func createDirectorySymlink(source string, destination string) error {
	if err := os.Symlink(source, destination); err != nil {
		return fmt.Errorf("Failed to create symlink %s: %w", destination, err)
	}
	return nil
}
