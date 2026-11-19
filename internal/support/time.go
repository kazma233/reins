package support

import (
	"fmt"
	"os"
	"time"
)

// ParseTimestamp 解析 RFC3339 时间戳为毫秒值（Rust chrono
// parse_from_rfc3339 → timestamp_millis）。已知口径差异：chrono 接受小写
// 't' 与空格分隔符，Go 的 time.Parse(RFC3339) 只认大写 'T'/'Z'——实际转录
// 文件里的时间戳（serde 序列化产物）恒为大写拼写，不受影响。
func ParseTimestamp(raw string) (int64, bool) {
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, false
	}
	return value.UnixMilli(), true
}

// FileModifiedTimestampMillis 返回文件 mtime 的毫秒值。早于 Unix 纪元的
// mtime 归零——Rust duration_since(UNIX_EPOCH) 失败后走 unwrap_or_default。
func FileModifiedTimestampMillis(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		// 上下文文案与 Rust with_context 一致；Go 顺带保留底层原因。
		return 0, fmt.Errorf("Failed to read metadata for %s: %w", path, err)
	}
	modified := info.ModTime()
	if modified.Before(time.Unix(0, 0)) {
		return 0, nil
	}
	return modified.UnixMilli(), nil
}
