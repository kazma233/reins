// Package support 是全域共享底座：home 口径、原子写、路径拼写与清单路径
// 拼接、环境重定向解析、时间换算。行为对齐 Rust 版 support/*（Phase 1
// 迁移）；与 Rust 版的已知口径差异逐条写在对应函数注释里。
package support

import (
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
)

// homeOverride：进程级测试 home 覆盖。Rust 版等价物是 cfg(test) 的静态量，
// 仅测试二进制可写；Go 无条件编译，靠"生产代码不调用导出的 setter"维持
// 生产路径不变。守卫负责设置并在 cleanup 时清除。
var homeOverride struct {
	sync.Mutex
	value string
	set   bool
}

// SetHomeOverride 写进程级 home 覆盖，仅供 internal/testsupport 的守卫使用；
// 空串表示清除。生产路径绝不调用。
func SetHomeOverride(home string) {
	homeOverride.Lock()
	defer homeOverride.Unlock()
	if home == "" {
		homeOverride.set = false
		homeOverride.value = ""
		return
	}
	homeOverride.set = true
	homeOverride.value = home
}

// UserHomeDir 返回当前用户的 home。优先级与 Rust user_home_dir 逐条对齐：
//  1. REINS_TEST_HOME 环境变量（Rust 里仅测试二进制生效——子进程继承不了
//     进程内 override，改经环境变量传递临时 home；Go 无 cfg(test)，靠变量名
//     限定在测试语境，生产环境约定不设置）
//  2. 进程级测试覆盖（testsupport 守卫写入）
//  3. 系统 home
//
// Rust 的 dirs::home_dir() 在 Windows 走 known-folder API、忽略 HOME 与
// USERPROFILE；Go 的 os.UserHomeDir 读 %USERPROFILE%。这是本次移植唯一
// 的平台级口径差异（标准库没有 known-folder API）；测试隔离不受影响，
// 守卫改写的环境变量在 Go 全平台生效。
func UserHomeDir() (string, bool) {
	if value, ok := os.LookupEnv("REINS_TEST_HOME"); ok {
		return value, true
	}
	homeOverride.Lock()
	defer homeOverride.Unlock()
	if homeOverride.set {
		return homeOverride.value, true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return home, true
}

// Canonicalize 返回绝对且解析过全部符号链接的路径（等价 std::fs::canonicalize，
// 路径不存在时报错）。Rust 在 Windows 用 dunce 规避 \\?\ verbatim 前缀；
// Go 的 EvalSymlinks 产出普通拼写、从不附加前缀，天然同效。
func Canonicalize(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// PathKey 返回路径键的统一拼写。Windows 接受两种分隔符，同一文件按字符串
// 构造方式不同会有不同拼写；存在时 canonicalize，让以路径为键的 map 命中
// 同一文件。不存在（或解析失败）时原样返回。
func PathKey(path string) string {
	if canonical, err := Canonicalize(path); err == nil {
		return canonical
	}
	return path
}

// WriteAtomic 经同目录临时文件 + rename 落盘，崩溃不会留下半截文件；
// os.Rename 在 Windows 上同样替换既有目标（MOVEFILE_REPLACE_EXISTING）。
//
// 权限语义：rename 会用临时文件的权限覆盖目标，而临时文件按默认 umask
// 建出——要求 0600 的敏感文件（如 dsh 的 .credentials.yaml）会被静默放宽为
// 0644，依赖方下次启动时拒绝加载。因此覆盖既有文件时先复制目标权限，
// 敏感文件用 WriteAtomicPrivate 强制 owner-only。
func WriteAtomic(path string, contents string) error {
	return writeAtomicWith(path, contents, false)
}

// WriteAtomicPrivate：敏感文件（凭据等）的原子写。无论新建还是覆盖都强制
// 0600（unix），顺带修复被写坏权限的既有文件。非 unix 平台行为相同
// （applyMode 在非 unix 是 no-op）。
func WriteAtomicPrivate(path string, contents string) error {
	return writeAtomicWith(path, contents, true)
}

func writeAtomicWith(path string, contents string, private bool) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return err
	}

	fileName := tempBasename(path)
	// 唯一同目录名：并发写者互不覆盖临时文件，点前缀让它不进 glob 列表。
	tmpPath := filepath.Join(directory, "."+fileName+"."+randomToken()+".tmp")

	if err := os.WriteFile(tmpPath, []byte(contents), 0o666); err != nil {
		return err
	}
	if err := applyMode(tmpPath, path, private); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// tempBasename 对齐 Rust path.file_name().to_str().unwrap_or("reins.tmp")：
// 非法拼写（空、目录占位、非 UTF-8）统一退回固定名，临时文件名仍可生成。
func tempBasename(path string) string {
	base := filepath.Base(path)
	if base == "" || base == "." || base == ".." || base == "/" || base == "\\" || !utf8.ValidString(base) {
		return "reins.tmp"
	}
	return base
}

// randomToken 生成 uuid v4 拼写的随机串。临时文件名只需唯一性，不承载语义。
func randomToken() string {
	var b [16]byte
	// crypto/rand.Read 按契约永不失败且填满整个切片，无需错误分支。
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// DisplayPath 返回给界面的路径拼写。Windows 的规范化路径会附加 \\?\ 或
// \\?\UNC\ 扩展长度前缀；前缀只服务文件系统 API，剥离以免干扰阅读，并让
// 「规范化结果 vs join 构造路径」的相等比较建立在同一形式上。
// 运行时字符串判断即可：其余平台该前缀不可能出现，分支恒不命中，
// 无需 build tag。
func DisplayPath(path string) string {
	if unc, found := strings.CutPrefix(path, `\\?\UNC\`); found {
		return `\\` + unc
	}
	if rest, found := strings.CutPrefix(path, `\\?\`); found {
		return rest
	}
	return path
}

// JoinManifestRelative 把清单相对路径逐段拼到 base 上。清单里的相对路径以
// '/' 书写；Windows 下整段交给拼接会原样保留 '/'，产出「home\.codex/
// config.toml」这类混合分隔符，逐段拼接保证平台规范形态（fs 操作与展示
// 共用一份口径）。
//
// 不用 filepath.Join：它会 Clean 整条路径（折叠 '..'、归一分隔符），而 Rust
// 的 join 不做清洗；按原样逐段拼接才与 Rust 结果逐字节一致。
func JoinManifestRelative(base string, relative string) string {
	result := base
	for _, part := range strings.Split(relative, "/") {
		if part == "" {
			continue
		}
		result = pushPath(result, part)
	}
	return result
}

// pushPath 对齐 Rust PathBuf::push 的相对段语义：base 已以分隔符结尾时不
// 重复添加；空 base 直接取 part。不 Clean、不解析 '..'。
func pushPath(base string, part string) string {
	if base == "" {
		return part
	}
	if !endsWithSeparator(base) {
		return base + string(filepath.Separator) + part
	}
	return base + part
}

func endsWithSeparator(path string) bool {
	if path == "" {
		return false
	}
	last := path[len(path)-1]
	if last == '/' {
		return true
	}
	// 反斜杠只在 Windows 上是分隔符；unix 文件名可以合法包含它。
	return runtime.GOOS == "windows" && last == '\\'
}

// EnumerateJSONLFiles 递归收集 root 下的 .jsonl 文件。root 不存在（含悬空
// 符号链接）返回空；单个条目读取失败静默跳过（Rust walkdir filter_map ok）。
// 顺序说明：Rust walkdir 是目录序（平台相关、不稳定），消费方统一排序后
// 使用；Go 的 WalkDir 给出字典序（确定性），对排序后的消费方等价。
func EnumerateJSONLFiles(root string) []string {
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // 静默跳过失败条目
		}
		// 符号链接不算文件：WalkDir 的 Type 基于 lstat，与 walkdir 默认
		// 不跟随符号链接的 file_type().is_file() 口径一致。
		if entry.Type().IsRegular() && pathExtension(entry.Name()) == "jsonl" {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// pathExtension 对齐 Rust Path::extension 的口径：首字符的 '.' 属于隐藏
// 文件名本身（".jsonl" 整体是 stem、无扩展名），不当作分隔点。
func pathExtension(name string) string {
	index := strings.LastIndexByte(name, '.')
	if index <= 0 {
		return ""
	}
	return name[index+1:]
}
