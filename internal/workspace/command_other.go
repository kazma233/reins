//go:build !windows

// 非 Windows 平台没有控制台窗口闪现问题，隐藏窗口是空操作（对齐 mod.rs
// 的 hide_child_console 非 Windows 分支）。
package workspace

import "os/exec"

func hideChildConsole(_ *exec.Cmd) {}
