// command.go：通用外部命令执行器（对齐 mod.rs 的 run_command）：协作式取消、
// 管道排空与 Windows 隐藏子进程窗口。Rust 版 50ms try_wait 轮询是 CancelCheck
// 的检查节拍；Go 侧取消统一走 context.Context——CommandContext 在 ctx 取消时
// 自动 kill 子进程，Wait 返回包装 context.Canceled 的错误，语义等价（取消
// 即 kill，不必再手写轮询）。取消优先于退出状态：ctx 已取消时无论子进程
// 退出码是什么都按取消处理——调用方靠它跳过后续收尾（如写 marker）。
package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// runCommand 执行外部命令并允许协作式取消。context：错误前缀；ctx 取消即
// kill 并以 context.Canceled 返回（skills/git 缓存层用 errors.Is 识别）。
// 取消由本函数全权执行（watch goroutine 等待 ctx.Done 后 kill），不依赖调用
// 方用 CommandContext 构造——Rust 版的 50ms try_wait 轮询是 CancelCheck 标记
// 的检查节拍，Go 的 ctx.Done 直等没有轮询必要，语义等价（取消即 kill）。
//
// stdio 与 Rust Command::output() 对齐：stdin 关闭（git 不会停下来等凭据
// 输入），stdout/stderr 管道捕获；管道必须由排空 goroutine 持续读取，否则
// 子进程输出塞满缓冲区后会卡住，永远等不到退出。
func runCommand(ctx context.Context, cmd *exec.Cmd, context string) error {
	hideChildConsole(cmd)
	cmd.Stdin = nil // exec.Cmd 缺省即 /dev/null（stdin 关闭）

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s：无法执行命令：%v", context, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("%s：无法执行命令：%v", context, err)
	}

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s：无法执行命令：%v", context, err)
	}

	// 取消 watcher：ctx 完成即杀子进程（重复 kill 与杀已退出进程均为无害
	// no-op）。stop 在 Wait 返回后关闭，避免 goroutine 泄漏。
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-stop:
		}
	}()

	stdoutDrained := make(chan []byte, 1)
	stderrDrained := make(chan []byte, 1)
	go drainPipe(stdoutPipe, stdoutDrained)
	go drainPipe(stderrPipe, stderrDrained)

	waitErr := cmd.Wait()

	// 取消优先于退出状态：Rust 版在每轮轮询先查取消标记，已退出的子进程
	// 收到取消同样按取消处理——调用方靠它跳过后续收尾（如写 marker）。
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	stdoutBytes := <-stdoutDrained
	stderrBytes := <-stderrDrained

	if waitErr == nil {
		return nil
	}

	stderr := strings.TrimSpace(string(stderrBytes))
	stdout := strings.TrimSpace(string(stdoutBytes))
	message := stderr
	if message == "" {
		message = stdout
	}
	if message == "" {
		// 两侧都空时退回退出状态本身（Rust format!("exit status {status}")）。
		message = waitErr.Error()
	}
	return fmt.Errorf("%s：%s", context, message)
}

// drainPipe 读空管道并投递结果；读错误按 EOF 收尾（Rust read_to_end 的
// `let _ =` 同口径：排空失败不改变退出状态判定）。
func drainPipe(pipe io.ReadCloser, out chan<- []byte) {
	var buffer bytes.Buffer
	_, _ = io.Copy(&buffer, pipe)
	_ = pipe.Close()
	out <- buffer.Bytes()
}

// cancelled：取消错误的统一判定入口（Rust `error.is::<Cancelled>()` 的
// context 惯例对应物）。集中一处，避免各调用方直接依赖 errors.Is 细节。
func cancelled(err error) bool {
	return errors.Is(err, context.Canceled)
}
