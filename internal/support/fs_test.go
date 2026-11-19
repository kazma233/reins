package support

import (
	"os"
	"path/filepath"
	"testing"
)

// user_home_dir 的三级优先级是移植红线（Rust 源无直接单测，靠夹具全体
// 隐式覆盖）；这里显式钉住顺序：REINS_TEST_HOME > 进程覆盖 > 系统 home。
func TestUserHomeDirPriorityOrder(t *testing.T) {
	SetHomeOverride("/reins-override-home")
	defer SetHomeOverride("")

	t.Setenv("REINS_TEST_HOME", "/reins-env-home")
	if home, ok := UserHomeDir(); !ok || home != "/reins-env-home" {
		t.Fatalf("REINS_TEST_HOME 应最高优先：got %q ok=%v", home, ok)
	}

	// 环境变量缺席时轮到进程覆盖（t.Setenv 的 cleanup 稍后还原原状态，
	// 这里手动 unset 是被测行为本身，不影响还原）。
	if err := os.Unsetenv("REINS_TEST_HOME"); err != nil {
		t.Fatalf("unset 失败: %v", err)
	}
	if home, ok := UserHomeDir(); !ok || home != "/reins-override-home" {
		t.Fatalf("进程覆盖应次优先：got %q ok=%v", home, ok)
	}

	SetHomeOverride("")
	want, wantErr := os.UserHomeDir()
	home, ok := UserHomeDir()
	if ok != (wantErr == nil) || home != want {
		t.Fatalf("系统 home 兜底：got %q ok=%v want %q err=%v", home, ok, want, wantErr)
	}
}

// Windows 规范化路径的 verbatim 前缀必须剥干净（\\?\UNC\ 还原 \\ 开头），
// 其余拼写原样通过。纯字符串函数，全平台可测。
func TestDisplayPathStripsVerbatimPrefix(t *testing.T) {
	cases := [][2]string{
		{`\\?\UNC\server\share\sessions`, `\\server\share\sessions`},
		{`\\?\C:\Users\me\.codex`, `C:\Users\me\.codex`},
		{`C:\Users\me\.codex`, `C:\Users\me\.codex`},
		{"/home/u/.codex", "/home/u/.codex"},
		{"relative/path", "relative/path"},
	}
	for _, c := range cases {
		if got := DisplayPath(c[0]); got != c[1] {
			t.Fatalf("DisplayPath(%q) = %q want %q", c[0], got, c[1])
		}
	}
}

// 清单相对路径逐段拼接：空段过滤、多段平台化、结尾分隔符不重复。
func TestJoinManifestRelativeSegments(t *testing.T) {
	cases := []struct {
		base     string
		relative string
		want     string
	}{
		{"/home/u", ".pi/agent", joinSegs("/home/u", ".pi", "agent")},
		{"/home/u", "/.codex//config.toml", joinSegs("/home/u", ".codex", "config.toml")},
		{"/home/u", "", "/home/u"},
		{"/home/u/", ".codex", joinSegs("/home/u", ".codex")},
	}
	for _, c := range cases {
		if got := JoinManifestRelative(c.base, c.relative); got != c.want {
			t.Fatalf("JoinManifestRelative(%q, %q) = %q want %q", c.base, c.relative, got, c.want)
		}
	}
}

// 同一文件经符号链接目录与真实目录两种拼写，path key 必须相同；
// 不存在的路径原样返回（Rust canonicalize 失败时回退原值）。
func TestPathKeyMatchesAcrossSpellings(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(real, 0o777); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(real, "a.jsonl")
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("符号链接不可用: %v", err)
	}

	keyReal := PathKey(filepath.Join(real, "a.jsonl"))
	keyLink := PathKey(filepath.Join(link, "a.jsonl"))
	if keyReal != keyLink {
		t.Fatalf("同一文件两种拼写的 key 不同：%q vs %q", keyReal, keyLink)
	}

	missing := filepath.Join(dir, "no-such", "x.jsonl")
	if got := PathKey(missing); got != missing {
		t.Fatalf("缺失路径应原样返回：got %q", got)
	}
}

// 枚举只收普通 .jsonl 文件：递归子目录、大小写敏感、隐藏文件名整体无
// 扩展名（".jsonl"）不算、符号链接文件与符号链接目录都不跟随。
func TestEnumerateJSONLFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	deep := filepath.Join(sub, "deep")
	if err := os.MkdirAll(deep, 0o777); err != nil {
		t.Fatal(err)
	}
	write := func(path string, name string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("写 %s 失败: %v", name, err)
		}
	}
	write(filepath.Join(dir, "a.jsonl"), "a.jsonl")
	write(filepath.Join(sub, "b.jsonl"), "b.jsonl")
	write(filepath.Join(deep, "c.jsonl"), "c.jsonl")
	write(filepath.Join(dir, "notes.txt"), "notes.txt")
	write(filepath.Join(dir, "d.JSONL"), "d.JSONL")
	write(filepath.Join(dir, ".hidden.jsonl"), ".hidden.jsonl")
	write(filepath.Join(dir, ".jsonl"), ".jsonl")

	// 符号链接在 Windows 需要特权，环境不支持时跳过相应断言。
	symlinkOK := true
	if err := os.Symlink(filepath.Join(dir, "a.jsonl"), filepath.Join(dir, "ref.jsonl")); err != nil {
		symlinkOK = false
	}
	if symlinkOK {
		if err := os.Symlink(sub, filepath.Join(dir, "linkdir")); err != nil {
			symlinkOK = false
		}
	}

	files := EnumerateJSONLFiles(dir)
	got := map[string]bool{}
	for _, path := range files {
		got[filepath.Base(path)] = true
	}
	want := map[string]bool{"a.jsonl": true, "b.jsonl": true, "c.jsonl": true, ".hidden.jsonl": true}
	if symlinkOK {
		// b.jsonl 只经真实目录计入一次，符号链接目录本身不被跟随。
		delete(want, "b.jsonl")
		want["b.jsonl"] = true
		if got["ref.jsonl"] {
			t.Fatalf("符号链接文件不应被枚举: %v", files)
		}
	}
	if !mapsEqual(got, want) {
		t.Fatalf("枚举结果不符：got %v want %v", got, want)
	}

	if got := EnumerateJSONLFiles(filepath.Join(dir, "missing")); len(got) != 0 {
		t.Fatalf("缺失 root 应返回空：got %v", got)
	}

	// root 本身是一个 .jsonl 文件时按单文件枚举（walkdir 同款行为）。
	if got := EnumerateJSONLFiles(filepath.Join(dir, "a.jsonl")); len(got) != 1 {
		t.Fatalf("单文件 root 应返回自身：got %v", got)
	}
}

func mapsEqual(got map[string]bool, want map[string]bool) bool {
	if len(got) != len(want) {
		return false
	}
	for key := range want {
		if !got[key] {
			return false
		}
	}
	return true
}

// 原子写自动补建父目录，成功后不留临时文件。
func TestWriteAtomicCreatesDirsAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "dir", "config.yaml")
	if err := WriteAtomic(target, "data"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "data" {
		t.Fatalf("内容不符: %q err=%v", content, err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "nested", "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Fatalf("应只剩目标文件: %v", entries)
	}
}
