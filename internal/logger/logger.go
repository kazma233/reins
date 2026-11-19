// Package logger：极简文件日志，行为对齐 Rust 版 logger.rs。落盘
// config_dir()/reins/logs/app.log，行格式 "[unix秒] LEVEL message"。
// 全链路静默失败：日志是诊断辅助，任何写失败都不允许打断业务流程。
package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"reins/internal/support"
)

var (
	// initOnce：首次 init 决定进程内日志路径，后续调用静默让位
	// （Rust OnceLock.set 失败即忽略的同款语义）。
	initOnce sync.Once
	// logPath 经 atomic.Value 发布：writeLog 与 Init 可能并发；空串表示
	// 尚未初始化（Rust OnceLock.get 不到就静默返回）。
	logPath atomic.Value
)

// Init 建日志目录并记住落盘路径。
func Init() {
	initOnce.Do(func() {
		path := logFilePath()
		if parent := filepath.Dir(path); parent != "" {
			_ = os.MkdirAll(parent, 0o777)
		}
		// 预建文件（create+append），失败同样静默；真正写日志时还会再开。
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666); err == nil {
			_ = file.Close()
		}
		logPath.Store(path)
	})
}

// Info 记一条 INFO 日志。
func Info(message string) {
	writeLog("INFO", message)
}

// Error 记一条 ERROR 日志。
func Error(message string) {
	writeLog("ERROR", message)
}

func logFilePath() string {
	return support.ConfigDirInApp("logs", "app.log")
}

// writeLog 每次单独 open+append（与 Rust 一致，依赖 O_APPEND 的追加原子性）；
// 未初始化或打开失败一律静默返回。系统时钟早于 Unix 纪元时秒值归零
// （Rust duration_since(UNIX_EPOCH) 失败后 unwrap_or_default）。
func writeLog(level string, message string) {
	path, _ := logPath.Load().(string)
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o666)
	if err != nil {
		return
	}
	defer file.Close()

	timestamp := time.Now().Unix()
	if timestamp < 0 {
		timestamp = 0
	}
	// 写失败（磁盘满等）静默吞掉：与 Rust writeln! 结果被忽略一致。
	_, _ = fmt.Fprintf(file, "[%d] %s %s\n", timestamp, level, message)
}
