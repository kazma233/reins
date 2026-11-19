package support

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// RFC3339 → 毫秒：秒级、带毫秒小数、带时区偏移各一；非法输入返回 false。
func TestParseTimestamp(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{"2024-01-02T03:04:05Z", 1704164645000},
		{"2024-01-02T03:04:05.123Z", 1704164645123},
		{"2024-01-02T03:04:05+02:00", 1704157445000},
	}
	for _, c := range cases {
		got, ok := ParseTimestamp(c.raw)
		if !ok || got != c.want {
			t.Fatalf("ParseTimestamp(%q) = %d ok=%v want %d", c.raw, got, ok, c.want)
		}
	}
	for _, raw := range []string{"", "not-a-time", "2024-13-40T00:00:00Z"} {
		if got, ok := ParseTimestamp(raw); ok {
			t.Fatalf("ParseTimestamp(%q) 应失败：got %d", raw, got)
		}
	}
}

func TestFileModifiedTimestampMillis(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := FileModifiedTimestampMillis(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := info.ModTime().UnixMilli(); got != want {
		t.Fatalf("mtime 毫秒值不符：got %d want %d", got, want)
	}

	_, err = FileModifiedTimestampMillis(filepath.Join(dir, "missing.jsonl"))
	if err == nil || !strings.Contains(err.Error(), "Failed to read metadata for") {
		t.Fatalf("缺失文件错误文案应与 Rust 一致：got %v", err)
	}
}

// 早于 Unix 纪元的 mtime 归零（Rust duration_since 失败 → unwrap_or_default）。
// 通过把文件时间戳置为 1970 前构造（unix 允许 touch 过去时间）。
func TestFileModifiedTimestampBeforeEpochIsZero(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过时间戳改写")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "old.jsonl")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := time.Unix(-1000, 0)
	if err := os.Chtimes(path, before, before); err != nil {
		t.Skipf("无法设置纪元前时间戳: %v", err)
	}
	got, err := FileModifiedTimestampMillis(path)
	if err != nil || got != 0 {
		t.Fatalf("纪元前 mtime 应归零：got %d err=%v", got, err)
	}
}
