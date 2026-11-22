package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reins/internal/logger"
	"reins/internal/support"

	_ "modernc.org/sqlite" // 注册 "sqlite" 驱动（三个缓存库共用）
)

// 持久化会话摘要缓存（SQLite，对齐 Rust 版 session/summary_cache.rs）。
//
// 首次加载时 codex/claude 的每个会话根转录文件要整文件解析一次才能拿到标题
// （如用户机器上 1233 个文件共 2.1GB）。这份缓存按 (来源, 路径, mtime) 存解
// 析结果：进程重启后 mtime 未变的文件直接命中缓存，只有新增或变更过的文件
// 才重新解析。缓存键的失效语义与各后端的内存摘要缓存完全一致（都信任文件
// mtime），缓存只是把同一份结果延伸到了进程生命周期之外。
//
// 错误契约：缓存是加速层，转录文件才是权威数据源；任何缓存读写失败都记日志
// 并按 miss 处理（Load 返回 nil、Store 放弃写入），不得阻断列表加载。

// SummaryDBFileName 是缓存库文件名。解析逻辑变更时（如标题提取规则调整）必须
// bump 文件名里的版本号：旧版本库文件整体失效，新版本从空库全量重建一次。
// v3：summary 增加 archived/compressed 状态位，旧行反序列化后两个标志恒为
// false，而缓存键只看 mtime，不重建就会一直漏掉角标。
// v4：summary 增加 models（会话用过的模型与供应商），同理旧行恒为空。
// v5：标题不再截断（normalize_title 去掉了 72 字符上限），旧的截断标题同样
// 会被 mtime 缓存一直沿用。
const SummaryDBFileName = "session-summary-cache_v5.db"

// SummaryDb：实例级句柄——目录随读取器引擎实例注入（测试传临时目录），连接
// 惰性打开。SQL 与全局层同源，两张脸共享同一份库文件格式。
// 串行语义与 Rust 相同：全部操作过同一把锁、复用同一条连接。
type SummaryDb struct {
	dir string

	mu sync.Mutex
	db *sql.DB
}

// reinsStoreDir：生产路径的持久缓存目录（`~/.reins`）的唯一解析点——引擎
// 与 pi 实例的前置层、ClearAllCaches 都从这里取。
func reinsStoreDir() (string, error) {
	home, ok := support.UserHomeDir()
	if !ok {
		return "", fmt.Errorf("Unable to determine home directory")
	}
	return filepath.Join(home, ".reins"), nil
}

// SummaryDbAtStoreDir：生产路径的摘要库（目录经 reinsStoreDir 解析）。
func SummaryDbAtStoreDir() (*SummaryDb, error) {
	dir, err := reinsStoreDir()
	if err != nil {
		return nil, err
	}
	return SummaryDbAt(dir), nil
}

// SummaryDbAt 以显式目录构造（测试与引擎注入用）。
func SummaryDbAt(dir string) *SummaryDb {
	return &SummaryDb{dir: dir}
}

// Load：mtime 未变时返回持久化摘要；查询失败或反序列化失败都按 miss（nil）。
func (s *SummaryDb) Load(app SourceApp, path string, mtime int64) *SessionSummary {
	var summary *SessionSummary
	err := s.withConnection(func(db *sql.DB) error {
		loaded, err := lookupSummary(db, app, path, mtime)
		if err != nil {
			return err
		}
		summary = loaded
		return nil
	})
	if err != nil {
		return nil
	}
	return summary
}

// Store：写入或覆盖一行；失败记日志放弃。
func (s *SummaryDb) Store(app SourceApp, path string, mtime int64, summary *SessionSummary) {
	err := s.withConnection(func(db *sql.DB) error {
		return insertSummary(db, app, path, mtime, summary)
	})
	if err != nil {
		logger.Error(fmt.Sprintf("summary cache store failed: %v", err))
	}
}

// ClearAll：清空整表（用户触发的刷新是「全量重建」的逃生通道）。跨来源共用
// 同一份库文件，所以清一次即覆盖所有用到持久摘要的来源。
func (s *SummaryDb) ClearAll() {
	err := s.withConnection(func(db *sql.DB) error {
		_, err := db.Exec("DELETE FROM session_summary_cache")
		return err
	})
	if err != nil {
		logger.Error(fmt.Sprintf("summary cache clear failed: %v", err))
	}
}

// Close 关闭惰性连接并复位句柄。实例被丢弃前必须调用：*sql.DB 不会随 GC
// 关闭，漏关即泄漏 SQLite 文件描述符——Rust 侧靠 Connection 的 Drop 自动
// 释放，Go 没有 Drop 语义，丢弃点显式负责（换根重建、清缓存用完即弃）。
// Close 后实例再次使用会重新打开连接（缓存错误契约下按正常路径工作），
// 但旧连接的目的已失效，纪律上以丢弃前 Close 为准。
func (s *SummaryDb) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// withConnection：惰性打开并串行执行。操作失败不丢弃连接（与 Rust 的
// Mutex<Option<Connection>> 一致），只把错误上抛给调用方按 miss/日志处理。
func (s *SummaryDb) withConnection(operation func(db *sql.DB) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		opened, err := openSummaryConnection(s.dir)
		if err != nil {
			return err
		}
		s.db = opened
	}
	return operation(s.db)
}

// summaryPathKey 与 family 索引的 path map 一致，用 canonicalize 后的路径做
// 键，避免同一文件因分隔符拼写不同而重复缓存。
func summaryPathKey(path string) string {
	return support.PathKey(path)
}

func lookupSummary(db *sql.DB, app SourceApp, path string, mtime int64) (*SessionSummary, error) {
	var summaryJSON string
	err := db.QueryRow(
		"SELECT summary FROM session_summary_cache WHERE source_app = ? AND path = ? AND mtime = ?",
		app, summaryPathKey(path), mtime,
	).Scan(&summaryJSON)
	if err != nil {
		return nil, fmt.Errorf("summary cache lookup failed: %w", err)
	}

	var summary SessionSummary
	if err := json.Unmarshal([]byte(summaryJSON), &summary); err != nil {
		return nil, fmt.Errorf("summary cache row failed to parse: %w", err)
	}
	return &summary, nil
}

func insertSummary(db *sql.DB, app SourceApp, path string, mtime int64, summary *SessionSummary) error {
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = db.Exec(
		"INSERT INTO session_summary_cache (source_app, path, mtime, summary) VALUES (?, ?, ?, ?)"+
			" ON CONFLICT(source_app, path) DO UPDATE SET mtime = excluded.mtime, summary = excluded.summary",
		app, summaryPathKey(path), mtime, string(summaryJSON),
	)
	return err
}

// openSummaryConnection：建目录 → 清旧版本残留 → 打开 → pragma → 建表，机械
// 部分在 openCacheConnection。
func openSummaryConnection(dir string) (*sql.DB, error) {
	return openCacheConnection(dir, SummaryDBFileName, "session-summary-cache",
		`CREATE TABLE IF NOT EXISTS session_summary_cache (
            source_app TEXT NOT NULL,
            path TEXT NOT NULL,
            mtime INTEGER NOT NULL,
            summary TEXT NOT NULL,
            PRIMARY KEY (source_app, path)
        )`)
}

// openCacheConnection：三个缓存库共用的连接打开序——建目录 → 清旧版本残留 →
// 打开 → pragma → 逐条建表。WAL + synchronous NORMAL 与 Rust 逐条对应；单
// 连接串行（MaxOpenConns(1)）让 pragma 与数据读写落在同一条连接上。ddl 传
// 该库的全部 CREATE TABLE（幂等，IF NOT EXISTS）。
func openCacheConnection(dir string, fileName string, stem string, ddl ...string) (*sql.DB, error) {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, fmt.Errorf("Failed to create %s: %w", dir, err)
	}
	removeStaleCacheDBFiles(dir, fileName, stem)

	path := filepath.Join(dir, fileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("Failed to open %s: %w", path, err)
	}
	// 等价 Rust 单 Connection：一条连接串行服务全部操作，pragma 不随连接
	// 重建丢失。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA synchronous = NORMAL"); err != nil {
		db.Close()
		return nil, err
	}

	for _, statement := range ddl {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}

	return db, nil
}

// removeStaleCacheDBFiles：清掉旧版本（含版本化之前的无后缀命名）残留的库
// 文件，避免 bump 后旧库永远占着磁盘。失败不影响主流程——残留只是多占空间，
// 不影响正确性。
func removeStaleCacheDBFiles(dir string, current string, stem string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		name := entry.Name()

		base, hadWAL := strings.CutSuffix(name, "-wal")
		if !hadWAL {
			base, _ = strings.CutSuffix(name, "-shm")
		}

		isCacheDB := strings.HasPrefix(base, stem)
		if isCacheDB && !strings.HasPrefix(name, current) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
