package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"reins/internal/logger"
)

// 持久化目录行缓存（SQLite，对齐 Rust 版 session/rows_cache.rs）。
//
// `list_rows` 是各来源冷启动最贵的一段：OpenCode 要对整个 session_message
// 全表跑 json_extract 聚合，dsh 要逐转录整份解压，codex 要逐文件读首行。
// 这份缓存按 (来源, 扫描根, freshness) 存行集合：进程重启后 freshness 未变
// 时直接复用，跳过物理扫描。失效口径与引擎的内存目录索引完全一致（都信任
// 各来源声明的 freshness），缓存只是把同一份结果延伸到进程生命周期之外。
//
// 行结构（字段增删或语义变化）变更时必须 bump 文件名里的版本号：旧行
// 反序列化失败按 miss 处理会全量重建，新版本从空库开始。
//
// 错误契约：缓存是加速层，来源数据才是权威；任何读写失败都按 miss 处理
// （LoadRows 返回 miss、StoreRows 记日志放弃写入），不得阻断列表加载。

const RowsDBFileName = "session-rows-cache_v1.db"

// Contrib：行级贡献表的一行。来源聚合类扫描按 (app, root, 行id) 存单行贡献
// ，行级失效键 stamp 由来源声明（如 opencode 的消息 time_updated）。
type Contrib struct {
	ID      string
	Stamp   int64
	Payload string
}

// RowsDb：实例级句柄——目录随读取器引擎实例注入（测试传临时目录），连接
// 惰性打开。串行语义与 Rust 相同：全部操作过同一把锁、复用同一条连接。
type RowsDb struct {
	dir string

	mu sync.Mutex
	db *sql.DB
}

// RowsDbAtStoreDir：生产路径的行缓存库（目录经 reinsStoreDir 解析）。
func RowsDbAtStoreDir() (*RowsDb, error) {
	dir, err := reinsStoreDir()
	if err != nil {
		return nil, err
	}
	return RowsDbAt(dir), nil
}

// RowsDbAt 以显式目录构造（测试与引擎注入用）。
func RowsDbAt(dir string) *RowsDb {
	return &RowsDb{dir: dir}
}

// LoadRows：freshness 未变时返回持久化的行集合；查询失败或反序列化失败都
// 按 miss（ok=false，与 Rust 一样完全静默——miss 是正常路径）。Go 泛型函数
// 替代 Rust 的 load<Row> 单态化。
func LoadRows[T any](db *RowsDb, app SourceApp, root string, stamp string) ([]T, bool) {
	// label 为空 = 静默：Rust 的 RowsDb::load 对任何失败都 .ok() 不记日志。
	rowsJSON, ok := withRowsConnection[string](db, "", func(conn *sql.DB) (*string, error) {
		return lookupRows(conn, app, root, stamp)
	})
	if !ok || rowsJSON == nil {
		return nil, false
	}
	var rows []T
	if err := json.Unmarshal([]byte(*rowsJSON), &rows); err != nil {
		return nil, false
	}
	return rows, true
}

// StoreRows：整集合写入或覆盖；序列化或 SQL 失败记日志放弃。
func StoreRows[T any](db *RowsDb, app SourceApp, root string, stamp string, rows []T) {
	rowsJSON, err := json.Marshal(rows)
	if err != nil {
		logger.Error(fmt.Sprintf("rows cache store failed: %v", err))
		return
	}
	withRowsConnection[struct{}](db, "rows cache store", func(conn *sql.DB) (*struct{}, error) {
		return nil, insertRows(conn, app, root, stamp, string(rowsJSON))
	})
}

// ClearAll：清空整表（显式清缓存命令的「全量重建」逃生通道）。
func (db *RowsDb) ClearAll() {
	withRowsConnection[struct{}](db, "rows cache clear", func(conn *sql.DB) (*struct{}, error) {
		if _, err := conn.Exec("DELETE FROM rows_cache"); err != nil {
			return nil, err
		}
		if _, err := conn.Exec("DELETE FROM row_contrib"); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

// Close 关闭惰性连接并复位句柄。实例被丢弃前必须调用：*sql.DB 不会随 GC
// 关闭，漏关即泄漏 SQLite 文件描述符——Rust 侧靠 Connection 的 Drop 自动
// 释放，Go 没有 Drop 语义，丢弃点显式负责（换根重建、清缓存用完即弃）。
// Close 后实例再次使用会重新打开连接（缓存错误契约下按正常路径工作），
// 但旧连接的目的已失效，纪律上以丢弃前 Close 为准。
func (db *RowsDb) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.db == nil {
		return nil
	}
	err := db.db.Close()
	db.db = nil
	return err
}

// LoadContribs：行级贡献表读取。读失败按空集处理（记日志），调用方退回全量
// 重提取，权威数据始终是来源自身。
func (db *RowsDb) LoadContribs(app SourceApp, root string) []Contrib {
	contribs, ok := withRowsConnection[[]Contrib](db, "row contrib load", func(conn *sql.DB) (*[]Contrib, error) {
		rows, err := conn.Query(
			"SELECT id, stamp, payload FROM row_contrib WHERE app = ? AND root = ?",
			app, root,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var collected []Contrib
		for rows.Next() {
			var contrib Contrib
			if err := rows.Scan(&contrib.ID, &contrib.Stamp, &contrib.Payload); err != nil {
				return nil, err
			}
			collected = append(collected, contrib)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return &collected, nil
	})
	if !ok || contribs == nil {
		return nil
	}
	return *contribs
}

// SyncContribs：贡献表事务写回——upsert 变化行、删除消失行；失败记日志放弃
// ，下轮按 stamp diff 自然补偿。
func (db *RowsDb) SyncContribs(app SourceApp, root string, upserts []Contrib, removedIDs []string) {
	withRowsConnection[struct{}](db, "row contrib sync", func(conn *sql.DB) (*struct{}, error) {
		return nil, syncContribsTx(conn, app, root, upserts, removedIDs)
	})
}

// syncContribsTx：单事务完成 upsert 与分批删除；任一步失败即整体回滚（Rust
// 的 transaction drop 隐式回滚），下轮 sync 自然补偿。
func syncContribsTx(conn *sql.DB, app SourceApp, root string, upserts []Contrib, removedIDs []string) error {
	transaction, err := conn.Begin()
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	statement, err := transaction.Prepare(
		"INSERT INTO row_contrib (app, root, id, stamp, payload) VALUES (?, ?, ?, ?, ?)" +
			" ON CONFLICT(app, root, id) DO UPDATE SET stamp = excluded.stamp, payload = excluded.payload",
	)
	if err != nil {
		return err
	}
	for i := range upserts {
		if _, err := statement.Exec(app, root, upserts[i].ID, upserts[i].Stamp, upserts[i].Payload); err != nil {
			statement.Close()
			return err
		}
	}
	statement.Close()

	if len(removedIDs) > 0 {
		// 单条 DELETE IN 同样受绑定变量上限约束：来源库整体清空/替换时
		// removed 是全量，超限会让整个事务（含 upsert）回滚，缓存从此永远
		// 写不进去，按批删除（批次常量与 Rust 同源：SqlParameterBatch）。
		for start := 0; start < len(removedIDs); start += SqlParameterBatch {
			end := start + SqlParameterBatch
			if end > len(removedIDs) {
				end = len(removedIDs)
			}
			batch := removedIDs[start:end]
			placeholders := make([]string, len(batch))
			args := make([]any, 0, len(batch)+2)
			args = append(args, app, root)
			for i, id := range batch {
				placeholders[i] = "?"
				args = append(args, id)
			}
			query := "DELETE FROM row_contrib WHERE app = ? AND root = ? AND id IN (" +
				strings.Join(placeholders, ",") + ")"
			if _, err := transaction.Exec(query, args...); err != nil {
				return err
			}
		}
	}

	return transaction.Commit()
}

// withRowsConnection：惰性打开并串行执行；失败统一按调用点的日志主语记日志
// （连接打开失败同样落进该主语，对齐 Rust open_connection 错误汇入同一处
// log_error 的行为）。label 为空表示完全静默（load 路径）。
func withRowsConnection[R any](db *RowsDb, label string, operation func(conn *sql.DB) (*R, error)) (*R, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.db == nil {
		opened, err := openRowsConnection(db.dir)
		if err != nil {
			logRowsFailure(label, err)
			return nil, false
		}
		db.db = opened
	}
	result, err := operation(db.db)
	if err != nil {
		logRowsFailure(label, err)
		return nil, false
	}
	return result, true
}

func logRowsFailure(label string, err error) {
	if label == "" {
		return
	}
	logger.Error(fmt.Sprintf("%s failed: %v", label, err))
}

// lookupRows：无行（normal miss）返回 (nil, nil)——Rust 把
// QueryReturnedNoRows 归一为 Ok(None)，不算错误。
func lookupRows(conn *sql.DB, app SourceApp, root string, stamp string) (*string, error) {
	var rowsJSON string
	err := conn.QueryRow(
		"SELECT rows FROM rows_cache WHERE app = ? AND root = ? AND stamp = ?",
		app, root, stamp,
	).Scan(&rowsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rows cache lookup failed: %w", err)
	}
	return &rowsJSON, nil
}

func insertRows(conn *sql.DB, app SourceApp, root string, stamp string, rowsJSON string) error {
	_, err := conn.Exec(
		"INSERT INTO rows_cache (app, root, stamp, rows) VALUES (?, ?, ?, ?)"+
			" ON CONFLICT(app, root) DO UPDATE SET stamp = excluded.stamp, rows = excluded.rows",
		app, root, stamp, rowsJSON,
	)
	return err
}

// openRowsConnection：建目录 → 清旧版本残留 → 打开 → pragma → 建表，机械
// 部分在 openCacheConnection。
func openRowsConnection(dir string) (*sql.DB, error) {
	return openCacheConnection(dir, RowsDBFileName, "session-rows-cache",
		`CREATE TABLE IF NOT EXISTS rows_cache (
            app TEXT NOT NULL,
            root TEXT NOT NULL,
            stamp TEXT NOT NULL,
            rows TEXT NOT NULL,
            PRIMARY KEY (app, root)
        )`,
		// 行级贡献表：rows_cache 存整来源行集合（单键），row_contrib 存来源自己
		// 声明的行级贡献（多键），同一库文件、同一失效信任模型。
		`CREATE TABLE IF NOT EXISTS row_contrib (
            app TEXT NOT NULL,
            root TEXT NOT NULL,
            id TEXT NOT NULL,
            stamp INTEGER NOT NULL,
            payload TEXT NOT NULL,
            PRIMARY KEY (app, root, id)
        )`)
}
