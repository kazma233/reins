package session

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"reins/internal/testsupport"
)

// session.go（mod.rs）共享助手的分区测试。Rust 侧这些助手由 2b 的读取器
// 测试间接覆盖；此处先钉住行为分区，为 2b 提供接缝保障。

func openMemoryDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSQLiteTableExistsDistinguishesMissingFromError(t *testing.T) {
	db := openMemoryDB(t)

	exists, err := SQLiteTableExists(db, "nope")
	if err != nil {
		t.Fatalf("probe missing table: %v", err)
	}
	if exists {
		t.Error("missing table must report false, not an error")
	}

	if _, err := db.Exec("CREATE TABLE present (id INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	exists, err = SQLiteTableExists(db, "present")
	if err != nil || !exists {
		t.Errorf("exists = %v err = %v, want true/nil", exists, err)
	}
}

func TestSQLiteColumnExistsDistinguishesMissingFromError(t *testing.T) {
	db := openMemoryDB(t)
	if _, err := db.Exec("CREATE TABLE t (old_col INTEGER)"); err != nil {
		t.Fatalf("create table: %v", err)
	}

	exists, err := SQLiteColumnExists(db, "t", "old_col")
	if err != nil || !exists {
		t.Fatalf("old col: exists = %v err = %v", exists, err)
	}
	// 后加的列在老库不存在：只说明该来源还没有这一状态，不算错误。
	exists, err = SQLiteColumnExists(db, "t", "added_later")
	if err != nil {
		t.Fatalf("probe missing column: %v", err)
	}
	if exists {
		t.Error("missing column must report false, not an error")
	}
	// 表不存在时列探测同样安静地返回 false。
	exists, err = SQLiteColumnExists(db, "ghost", "x")
	if err != nil || exists {
		t.Errorf("ghost table: exists = %v err = %v", exists, err)
	}
}

func TestParseSourceAppRoundTripAndRejectsUnknown(t *testing.T) {
	for _, app := range []SourceApp{
		SourceCodex, SourceClaudeCode, SourceOpenCode,
		SourcePi, SourceGrokBuild, SourceZcode, SourceDsh,
	} {
		parsed, err := ParseSourceApp(string(app))
		if err != nil || parsed != app {
			t.Errorf("ParseSourceApp(%q) = %v, %v", app, parsed, err)
		}
	}
	if _, err := ParseSourceApp("cursor"); err == nil {
		t.Error("unknown source app must fail")
	} else if want := "Unsupported source app: cursor"; err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

func TestSpecRootFollowsEnvRedirectAndHomeFallback(t *testing.T) {
	tempHome := testsupport.TestDir(t, "spec-root")
	guard := testsupport.SetHome(t, tempHome)

	// HOME 兜底：codex 根 = HOME/.codex。
	root, err := SpecRoot(SourceCodex)
	if err != nil {
		t.Fatalf("SpecRoot: %v", err)
	}
	if want := filepath.Join(tempHome, ".codex"); root != want {
		t.Errorf("root = %q, want %q", root, want)
	}

	// 重定向优先：CODEX_HOME 生效后与 workspace/providers 看到同一份数据。
	t.Setenv("CODEX_HOME", filepath.Join(tempHome, "custom-codex"))
	root, err = SpecRoot(SourceCodex)
	if err != nil {
		t.Fatalf("SpecRoot redirect: %v", err)
	}
	if want := filepath.Join(tempHome, "custom-codex"); root != want {
		t.Errorf("root = %q, want %q", root, want)
	}

	guard.Restore()
}

func TestSessionsFingerprintIsOrderIndependentAndSensitive(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.jsonl")
	second := filepath.Join(dir, "b.jsonl")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}

	forward, err := SessionsFingerprint([]string{first, second})
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	reverse, err := SessionsFingerprint([]string{second, first})
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	// 排序串接：输入顺序不影响指纹。
	if forward != reverse {
		t.Errorf("fingerprint depends on input order:\n%s\n%s", forward, reverse)
	}

	// 内容变化（mtime 推进）会改变指纹。
	after, err := SessionsFingerprint([]string{first, second})
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if err := os.WriteFile(first, []byte("more bytes"), 0o644); err != nil {
		t.Fatalf("touch: %v", err)
	}
	changed, err := SessionsFingerprint([]string{first, second})
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if after == changed {
		t.Error("mtime change must alter fingerprint")
	}

	// 统计失败的路径按错误返回（对齐 Rust with_context）。
	if _, err := SessionsFingerprint([]string{filepath.Join(dir, "ghost.jsonl")}); err == nil {
		t.Error("missing file must fail fingerprinting")
	}
}

func TestSortEntriesTimestampDescThenPathAsc(t *testing.T) {
	entries := []SessionFileEntry{
		{Path: "/p/old", SortTimestamp: 100},
		{Path: "/p/z", SortTimestamp: 200},
		{Path: "/p/a", SortTimestamp: 200},
		{Path: "/p/mid", SortTimestamp: 150},
	}
	sortEntries(entries)

	wantOrder := []string{"/p/a", "/p/z", "/p/mid", "/p/old"}
	for i, want := range wantOrder {
		if entries[i].Path != want {
			t.Errorf("entries[%d].Path = %q, want %q", i, entries[i].Path, want)
		}
	}
}

// comparePaths 对齐 Rust PathBuf Ord 的分量比较："a/b" 与 "a.b" 的次序由
// 分量决定（"a" < "a.b"），而不是整串字节序（'.' < '/'）。
func TestComparePathsComponentWise(t *testing.T) {
	cases := []struct {
		left  string
		right string
		want  int
	}{
		{"/a/b.jsonl", "/a/c.jsonl", -1},
		// 分量比较对齐 Rust PathBuf Ord：["a","b"] vs ["a.b"] → "a" < "a.b"，
		// 所以 "/a/b" < "/a.b"——整串字节序会给出相反结果（'.' < '/'）。
		{"/a/b", "/a.b", -1},
		{"/a/b", "/a/b/c", -1},
		{"/same", "/same", 0},
	}
	for _, c := range cases {
		if got := comparePaths(c.left, c.right); got != c.want {
			t.Errorf("comparePaths(%q, %q) = %d, want %d", c.left, c.right, got, c.want)
		}
	}
}
