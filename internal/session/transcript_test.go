package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// transcript.go 无 Rust 内联测试；夹具按迁移任务要求补齐：多帧串联解码与
// 尾帧截断容错是本文件的硬语义。

// compressFrames 产出若干个独立的 zstd 帧并串联（模拟 dsh 按写入批次落盘）。
func compressFrames(t *testing.T, chunks ...string) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, chunk := range chunks {
		var frame bytes.Buffer
		writer, err := zstd.NewWriter(&frame)
		if err != nil {
			t.Fatalf("new zstd writer: %v", err)
		}
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatalf("write zstd frame: %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close zstd frame: %v", err)
		}
		out.Write(frame.Bytes())
	}
	return out.Bytes()
}

func writeTranscript(t *testing.T, dir string, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestIsCompressedMatchesBothSuffixes(t *testing.T) {
	cases := map[string]bool{
		"rollout-2026.jsonl.zst": true,
		"batch.jsonl.zstd":       true,
		"plain.jsonl":            false,
		"plain.jsonl.zst.backup": false,
		"decoy.jsonl.zstdx":      false,
	}
	for name, want := range cases {
		if got := IsCompressed(name); got != want {
			t.Errorf("IsCompressed(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestResolvePrefersPlainAndSwapsSpelling(t *testing.T) {
	// 只存明文：明文原样返回。
	plainDir := t.TempDir()
	plain := writeTranscript(t, plainDir, "rollout.jsonl", []byte("plain"))
	if got := Resolve(plain); got != plain {
		t.Errorf("Resolve(plain) = %q, want unchanged", got)
	}

	// 记的是压缩路径而磁盘已明文化（codex 续跑 materialize）：换拼还原出
	// 明文名（剥压缩后缀补 .jsonl，对齐 codex reader 的 shadow 配方；
	// Rust 版漏补 .jsonl 换拼从未生效，见决策记录
	// 2026-11-19-fix-transcript-resolve-suffix-reassembly）。
	if got := Resolve(filepath.Join(plainDir, "rollout.jsonl.zst")); got != plain {
		t.Errorf("Resolve(codex-recorded) = %q, want %q", got, plain)
	}
	// dsh 后缀同理。
	if got := Resolve(filepath.Join(plainDir, "rollout.jsonl.zstd")); got != plain {
		t.Errorf("Resolve(dsh-recorded) = %q, want %q", got, plain)
	}

	// 记的是明文路径而磁盘已压缩（codex 冷会话压缩）：换拼还原出压缩名。
	zstdDir := t.TempDir()
	compressed := writeTranscript(t, zstdDir, "rollout.jsonl.zst", []byte{0x28, 0xb5, 0x2f, 0xfd})
	recordedPlain := filepath.Join(zstdDir, "rollout.jsonl")
	if got := Resolve(recordedPlain); got != compressed {
		t.Errorf("Resolve(plain-recorded, compressed on disk) = %q, want %q", got, compressed)
	}
	if got := Resolve(compressed); got != compressed {
		t.Errorf("Resolve(compressed) = %q, want unchanged", got)
	}

	// 裸 stem 不是合法目标：明文只认 "<stem>.jsonl"（Rust 怪癖曾把裸 stem
	// 当候选，修正后不允许）。
	stemOnlyDir := t.TempDir()
	writeTranscript(t, stemOnlyDir, "rollout", []byte("bare stem"))
	if got := Resolve(filepath.Join(stemOnlyDir, "rollout.jsonl.zst")); got != filepath.Join(stemOnlyDir, "rollout.jsonl.zst") {
		t.Errorf("Resolve(stem-only sibling) = %q, want unchanged", got)
	}

	// 两种拼写都不存在 → 原样返回。
	missing := filepath.Join(t.TempDir(), "ghost.jsonl.zst")
	if got := Resolve(missing); got != missing {
		t.Errorf("Resolve(missing) = %q, want unchanged", got)
	}
}

func TestOpenLinesDecodesCompressedLineByLine(t *testing.T) {
	dir := t.TempDir()
	content := "line one\nline two\nline three\n"
	path := writeTranscript(t, dir, "rollout.jsonl.zst", compressFrames(t, content))

	reader, err := OpenLines(path)
	if err != nil {
		t.Fatalf("OpenLines: %v", err)
	}
	defer reader.Close()

	var lines []string
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			lines = append(lines, strings.TrimSuffix(line, "\n"))
		}
		if err != nil {
			break
		}
	}
	want := []string{"line one", "line two", "line three"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("lines[%d] = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestOpenLinesReadsPlainWithoutDecode(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "plain.jsonl", []byte("hello\n"))

	reader, err := OpenLines(path)
	if err != nil {
		t.Fatalf("OpenLines: %v", err)
	}
	defer reader.Close()

	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSuffix(line, "\n") != "hello" {
		t.Errorf("line = %q err = %v, want hello", line, err)
	}
}

func TestReadFramesConcatenatesMultipleZstdFrames(t *testing.T) {
	dir := t.TempDir()
	// dsh 按写入批次落盘：两个独立帧拼在一个 .jsonl.zstd 里。
	first := "frame one line\nframe one line 2\n"
	second := "frame two line\n"
	path := writeTranscript(t, dir, "multi.jsonl.zstd", compressFrames(t, first, second))

	got, err := ReadFrames(path)
	if err != nil {
		t.Fatalf("ReadFrames: %v", err)
	}
	if string(got) != first+second {
		t.Errorf("decoded = %q, want %q", got, first+second)
	}
}

// 截断容错：live 转录的尾帧可能正在写入，截断前已解出的内容必须照常返回。
func TestDecodeAllKeepsPrefixWhenTailFrameTruncated(t *testing.T) {
	dir := t.TempDir()
	first := "complete frame\nwith two lines\n"
	second := "partial frame being written\n"
	full := compressFrames(t, first, second)

	// 掐掉尾帧后半段，首帧完整。
	truncated := full[:len(full)-len(full)/4]
	if bytes.Equal(truncated, full) {
		t.Fatal("truncation did not change fixture")
	}
	path := writeTranscript(t, dir, "live.jsonl.zstd", truncated)

	got, err := ReadFrames(path)
	if err != nil {
		t.Fatalf("ReadFrames should tolerate truncated tail, got error: %v", err)
	}
	if !strings.HasPrefix(string(got), "complete frame") {
		t.Errorf("decoded prefix = %q, want content of the first frame", got)
	}
	if bytes.Contains(got, []byte("partial frame being written")) {
		t.Errorf("decoded = %q, should not contain truncated frame content", got)
	}
}

// 单帧中途截断且已解出部分块：同样保留已解出的前缀。
func TestDecodeAllKeepsPartialBlocksOfTruncatedSingleFrame(t *testing.T) {
	dir := t.TempDir()
	// 足够大的输入让 zstd 产出多个块，截断才有已解出的内容可保留。
	var builder strings.Builder
	for i := 0; i < 8192; i++ {
		builder.WriteString("{\"line\": \"payload padding to force multiple blocks\", \"i\": ")
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString("}\n")
	}
	full := compressFrames(t, builder.String())

	truncated := full[:len(full)/2]
	path := writeTranscript(t, dir, "big.jsonl.zst", truncated)

	got, err := ReadFrames(path)
	if err != nil {
		t.Fatalf("ReadFrames should tolerate truncated tail, got error: %v", err)
	}
	if len(got) == 0 || len(got) >= builder.Len() {
		t.Errorf("decoded size = %d, want a non-empty strict prefix of %d", len(got), builder.Len())
	}
}

// 第一段就失败（非 zstd 数据 / 开头即截断）判定为文件损坏并报错。
func TestDecodeAllFailsWhenNothingDecoded(t *testing.T) {
	dir := t.TempDir()
	// 非 zstd 数据伪装成压缩转录。
	garbage := writeTranscript(t, dir, "corrupt.jsonl.zst", []byte("definitely not zstd"))
	if _, err := ReadFrames(garbage); err == nil {
		t.Error("garbage frame should fail decode")
	} else if !strings.Contains(err.Error(), "Failed to decode zstd transcript") {
		t.Errorf("error = %v, want decode-failure context", err)
	}

	// 单块帧的开头即被截断：零字节解出，同样报错。
	tiny := compressFrames(t, "short\n")
	path := writeTranscript(t, dir, "tiny.jsonl.zst", tiny[:4])
	if _, err := ReadFrames(path); err == nil {
		t.Error("head-truncated frame should fail decode")
	}
}

func TestReadFramesReturnsRawBytesForPlainTranscript(t *testing.T) {
	dir := t.TempDir()
	path := writeTranscript(t, dir, "plain.jsonl", []byte("raw\nbytes\n"))
	got, err := ReadFrames(path)
	if err != nil {
		t.Fatalf("ReadFrames: %v", err)
	}
	if string(got) != "raw\nbytes\n" {
		t.Errorf("got = %q, want raw bytes", got)
	}
}

// OpenLines 打开的句柄必须真正关闭底层文件（Go 无 Drop，靠调用方 Close）。
func TestOpenLinesCloseReleasesFile(t *testing.T) {
	dir := t.TempDir()
	content := "a\nb\n"
	path := writeTranscript(t, dir, "release.jsonl.zst", compressFrames(t, content))

	reader, err := OpenLines(path)
	if err != nil {
		t.Fatalf("OpenLines: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Errorf("double Close: %v", err)
	}
}
