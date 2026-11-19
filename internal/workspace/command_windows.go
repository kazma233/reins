//go:build windows

// Windows：子进程默认继承父进程的控制台，GUI 应用派生的 git 等命令会闪出
// 黑窗。CREATE_NO_WINDOW 抑制之（对齐 mod.rs 的 hide_child_console）。
package workspace

import (
	"os/exec"
	"syscall"
)

// createNoWindow：CREATE_NO_WINDOW 标志位。
const createNoWindow uint32 = 0x08000000

// hideChildConsole 让子进程不创建/不继承控制台窗口；非 Windows 平台无此概念
// （见 command_other.go）。
func hideChildConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
}
