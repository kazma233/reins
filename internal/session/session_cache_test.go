package session

import (
	"os"
	"os/exec"
	"testing"

	"reins/internal/testsupport"
)

// 对齐 Rust 版 tests/session_cache.rs 中不依赖 reader 的可移植子集：
// 持久化的核心承诺是跨进程生效——子进程写入的缓存行，本进程必须能读到。
// reader 级的冷启动/失效/清空测试（codex_cold_start、pi_*、clear_* 等）依赖
// 2b 的读取器与 catalog，随下一半任务平移。

// 持久化摘要的固定形状（对齐 Rust cached_summary）。
func persistentSummary() *SessionSummary {
	return &SessionSummary{
		SourceApp:       SourceCodex,
		SourceSessionID: "cached-session",
		Title:           "persistent title",
		TranscriptPath:  "/tmp/some-transcript.jsonl",
		CreatedAt:       int64Ptr(1),
		UpdatedAt:       int64Ptr(2),
		Models:          []SessionModel{},
	}
}

// TestSummaryCachePersistenceChildHelper：子进程模式，由
// TestSummaryCachePersistenceSurvivesProcessRestart 以环境变量唤起；直接运行
// 时（普通 go test）跳过。
func TestSummaryCachePersistenceChildHelper(t *testing.T) {
	if os.Getenv("REINS_SESSION_CACHE_CHILD") == "" {
		t.Skip("child helper: only runs under persistence restart test")
	}
	// HOME 已由父进程经环境变量传入（REINS_TEST_HOME / HOME）。
	db, err := SummaryDbAtStoreDir()
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	db.Store(SourceCodex, "/tmp/some-transcript.jsonl", 100, persistentSummary())
}

func TestSummaryCachePersistenceSurvivesProcessRestart(t *testing.T) {
	tempHome := testsupport.TestDir(t, "summary-restart")
	homeGuard(t, tempHome)

	// 子进程继承本进程的环境变量（HOME / REINS_TEST_HOME 已指向临时目录）。
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestSummaryCachePersistenceChildHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "REINS_SESSION_CACHE_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child helper failed: %v\n%s", err, output)
	}

	loaded := SummaryDbAtStoreDirMust(t).Load(SourceCodex, "/tmp/some-transcript.jsonl", 100)
	if loaded == nil {
		t.Fatal("cache hit after restart expected")
	}
	if loaded.Title != "persistent title" {
		t.Errorf("title = %q, want persistent title", loaded.Title)
	}
}

// SummaryDbAtStoreDirMust：测试内打开生产路径库目录的便捷封装；收尾注册
// Close（Windows 下连接须先于 t.TempDir 清理释放）。
func SummaryDbAtStoreDirMust(t *testing.T) *SummaryDb {
	t.Helper()
	db, err := SummaryDbAtStoreDir()
	if err != nil {
		t.Fatalf("open summary db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
