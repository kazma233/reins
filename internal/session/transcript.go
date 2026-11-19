package session

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// 转录文件的物理形态适配（对齐 Rust 版 session/transcript.rs）。
//
// 两家来源会把磁盘上的转录压缩，读取方必须按各自契约处理：
//   - codex 的后台 worker 把 7 天未修改的冷会话原地压成 `.jsonl.zst` 并删除
//     明文（上游 `rollout/src/compression.rs`），归档会话的文件在 `sessions/`
//     与 `archived_sessions/` 之间移动；
//   - dsh 的转录按写入批次写成多帧 `.jsonl.zstd`。
//
// 路径归一与解码收在这里，按行循环不再各写一遍 zstd。

const (
	// CodexCompressedSuffix：codex 压缩转录的后缀，上游给整个 rollout 文件名
	// 追加 `.zst`。
	CodexCompressedSuffix = ".jsonl.zst"
	// DshCompressedSuffix：dsh 压缩转录的后缀。
	DshCompressedSuffix = ".jsonl.zstd"
)

// IsCompressed：该路径是否为压缩形态的转录。
func IsCompressed(path string) bool {
	name := filepath.Base(path)
	return strings.HasSuffix(name, CodexCompressedSuffix) || strings.HasSuffix(name, DshCompressedSuffix)
}

// Resolve：转录当前的物理位置——明文优先，其次压缩兄弟文件，与 codex 上游
// `existing_rollout_path` 同语义。压缩/解压会改写这两种拼写，读取方拿到的
// 路径可能是上一轮的形态，按此归一后再打开，不必各自重扫目录。
//
// 换拼配方与 codex reader 自扫的 shadow 判定保持一致（剥后缀补 .jsonl）：
// Rust 版此处漏补 .jsonl，换拼从未生效（「存在即原样」），已按决策记录
// 2026-11-19-fix-transcript-resolve-suffix-reassembly 修正。
func Resolve(path string) string {
	if isFile(path) {
		return path
	}

	name := filepath.Base(path)

	// 记的是压缩路径而磁盘上已还原成明文（codex 续跑会先 materialize）。
	for _, suffix := range []string{CodexCompressedSuffix, DshCompressedSuffix} {
		if stem, found := strings.CutSuffix(name, suffix); found {
			candidate := filepath.Join(filepath.Dir(path), stem+".jsonl")
			if isFile(candidate) {
				return candidate
			}
		}
	}

	// 记的是明文路径而磁盘上已压缩（codex 的冷会话压缩）。
	if stem, found := strings.CutSuffix(name, ".jsonl"); found {
		for _, suffix := range []string{CodexCompressedSuffix, DshCompressedSuffix} {
			candidate := filepath.Join(filepath.Dir(path), stem+suffix)
			if isFile(candidate) {
				return candidate
			}
		}
	}

	return path
}

// LinesReader：转录逐行读取句柄。Go 没有 Drop 语义，调用方用完必须 Close；
// 关闭连带关闭底层文件（对齐 Rust Box<dyn BufRead> 的 Drop 释放）。Close
// 幂等：Drop 语义只发生一次，重复 Close 不报错。
type LinesReader struct {
	*bufio.Reader
	closeOnce sync.Once
	closer    io.Closer
	closeErr  error
}

// Close 关闭解码器与底层文件；幂等。
func (r *LinesReader) Close() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.closer.Close()
	})
	return r.closeErr
}

// OpenLines：打开按行读取的转录——压缩形态边解边读，只读前缀的调用方（如
// codex 索引扫描只取首行 session_meta）不必为一行解压整个文件。
func OpenLines(path string) (*LinesReader, error) {
	resolved := Resolve(path)
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %s: %w", resolved, err)
	}

	if IsCompressed(resolved) {
		decoder, err := zstd.NewReader(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("Failed to decode %s: %w", resolved, err)
		}
		closer := closerFunc(func() error {
			decoder.Close()
			return file.Close()
		})
		return &LinesReader{Reader: bufio.NewReader(decoder), closer: closer}, nil
	}
	return &LinesReader{Reader: bufio.NewReader(file), closer: file}, nil
}

// openPlainLines：打开明文转录按行读取——不解析压缩形态（claude 转录恒为
// 明文 jsonl，与 Rust 的 BufReader::new(File::open) 一致；压缩来源走
// OpenLines）。调用方用完必须 Close。
func openPlainLines(path string) (*LinesReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %s: %w", path, err)
	}
	return &LinesReader{Reader: bufio.NewReader(file), closer: file}, nil
}

// ReadFrames：读取转录全文——压缩形态整份解码后返回原始字节。
func ReadFrames(path string) ([]byte, error) {
	resolved := Resolve(path)
	file, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %s: %w", resolved, err)
	}
	defer file.Close()

	if !IsCompressed(resolved) {
		raw, err := io.ReadAll(file)
		if err != nil {
			return nil, fmt.Errorf("Failed to read %s: %w", resolved, err)
		}
		return raw, nil
	}

	return decodeAll(file, resolved)
}

// decodeAll 的容错口径：live 转录的尾帧可能截断（正在写入），截断前已解出
// 的内容照常返回，只有第一段就失败才判定为文件损坏。dsh 的多帧拼接与
// codex 的单帧都走这条路径。io.EOF 是合法终止（含空帧），不算失败——对齐
// Rust 循环里 Ok(0) → break 先于错误分支的次序。
func decodeAll(file io.Reader, path string) ([]byte, error) {
	decoder, err := zstd.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("Failed to decode %s: %w", path, err)
	}
	defer decoder.Close()

	output := make([]byte, 0, 64*1024)
	buffer := make([]byte, 64*1024)

	for {
		read, readErr := decoder.Read(buffer)
		if read > 0 {
			output = append(output, buffer[:read]...)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if len(output) == 0 {
				return nil, fmt.Errorf("Failed to decode zstd transcript %s: %w", path, readErr)
			}
			break
		}
		if read == 0 {
			break
		}
	}

	return output, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }
