package logger

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"reins/internal/testsupport"
)

// Init 前的写日志是 no-op；Init 后落盘 config_dir()/reins/logs/app.log，
// 行格式 "[unix秒] LEVEL message"（Rust logger.rs 的可观察行为全集）。
// Init 的 Once 语义（首调固定路径）是进程级状态，相关断言合并在一个测试里，
// 避免测试间经全局状态互相污染。
func TestLoggerFormatPathAndSilentFailure(t *testing.T) {
	home := testsupport.TestDir(t, "logger")
	guard := testsupport.SetHome(t, home)
	defer guard.Restore()

	// 未初始化：静默返回，不落任何文件。
	Info("before init")
	Init()

	expectedPath := logFilePath()
	if !strings.HasSuffix(expectedPath, filepath.Join("reins", "logs", "app.log")) {
		t.Fatalf("日志路径形态不符：%q", expectedPath)
	}
	// darwin（ConfigDir 恒基于 HOME）与未设 XDG_CONFIG_HOME 的 linux 下，
	// 日志必须落在守卫的临时 home 内——真实 home 漏进来即隔离失效。
	if runtime.GOOS == "darwin" || (runtime.GOOS != "windows" && os.Getenv("XDG_CONFIG_HOME") == "") {
		if !strings.HasPrefix(expectedPath, home) {
			t.Fatalf("日志应落在测试 home 内：%q 不在 %q 下", expectedPath, home)
		}
	}

	Info("hello")
	Error("oops")

	content, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("日志文件应已创建: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	if len(lines) != 2 {
		// "before init" 必须没写进来（未初始化静默丢弃）。
		t.Fatalf("应恰好两条日志: %q", lines)
	}
	lineRe := regexp.MustCompile(`^\[(\d+)\] (INFO|ERROR) (.*)$`)
	now := time.Now().Unix()
	for i, want := range [][2]string{{"INFO", "hello"}, {"ERROR", "oops"}} {
		m := lineRe.FindStringSubmatch(lines[i])
		if m == nil || m[2] != want[0] || m[3] != want[1] {
			t.Fatalf("日志行格式不符: %q", lines[i])
		}
		ts, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || ts < now-60 || ts > now+60 {
			t.Fatalf("时间戳应为当前 unix 秒: %q", lines[i])
		}
	}
}
