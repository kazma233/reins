package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"reins/internal/logger"
	"reins/internal/support"
)

// 持久化用量小时桶缓存（SQLite，对齐 Rust 版 session/usage_day_cache.rs）。
//
// 用量曲线按消耗事件的时间戳归桶，每个转录文件都要整文件解析一次才能拿到
// 逐条 usage；这层缓存按 (来源, 路径, mtime) 存归桶结果，进程重启后 mtime
// 未变的文件直接命中，只有新增或变更过的文件才重新解析。失效语义与
// summary_cache 一致（信任文件 mtime）。SQL 来源以 db 文件作路径键，值是
// 带采集日期的整份统计。
//
// 错误契约：缓存是加速层，转录文件才是权威数据源；读写失败记日志并按 miss
// 处理（Load 返回 miss、Store 放弃写入），不得阻断统计。

// UsageDayDBFileName：归桶逻辑变更时（如 day→hour 粒度调整）必须 bump 文件名
// 里的版本号：旧版本库文件整体失效，新版本从空库全量重建一次。
// v3：小时桶增加按模型的切片子结构。
const UsageDayDBFileName = "usage-day-cache_v3.db"

// usageDayState：进程级单例（Rust LazyLock<Mutex<Option<(PathBuf, Connection)>>>
// 的对应物）。记录已打开连接对应的库路径——测试会切换 HOME 覆盖，路径变化
// 时必须重开连接，避免读写到上一个环境的库。
var usageDayState struct {
	sync.Mutex
	path string
	db   *sql.DB
}

// UsageDayLoad：mtime 未变时返回持久化值；查询失败或反序列化失败都按 miss
// （ok=false，静默——与 Rust 的 .ok() 一致）。
func UsageDayLoad[T any](app SourceApp, path string, mtime int64) (T, bool) {
	var loaded T
	err := usageDayWithConnection(func(db *sql.DB) error {
		var daysJSON string
		if err := db.QueryRow(
			"SELECT days FROM usage_day_cache WHERE source_app = ? AND path = ? AND mtime = ?",
			app, usageDayPathKey(path), mtime,
		).Scan(&daysJSON); err != nil {
			return fmt.Errorf("usage day cache lookup failed: %w", err)
		}
		if err := json.Unmarshal([]byte(daysJSON), &loaded); err != nil {
			return fmt.Errorf("usage day cache row failed to parse: %w", err)
		}
		return nil
	})
	if err != nil {
		var zero T
		return zero, false
	}
	return loaded, true
}

// UsageDayStore：写入或覆盖一行；失败记日志放弃。
func UsageDayStore[T any](app SourceApp, path string, mtime int64, value T) {
	err := usageDayWithConnection(func(db *sql.DB) error {
		hoursJSON, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = db.Exec(
			"INSERT INTO usage_day_cache (source_app, path, mtime, days) VALUES (?, ?, ?, ?)"+
				" ON CONFLICT(source_app, path) DO UPDATE SET mtime = excluded.mtime, days = excluded.days",
			app, usageDayPathKey(path), mtime, string(hoursJSON),
		)
		return err
	})
	if err != nil {
		logger.Error(fmt.Sprintf("usage day cache store failed: %v", err))
	}
}

// UsageDayClearAll：清空整表；失败记日志。
func UsageDayClearAll() {
	err := usageDayWithConnection(func(db *sql.DB) error {
		_, err := db.Exec("DELETE FROM usage_day_cache")
		return err
	})
	if err != nil {
		logger.Error(fmt.Sprintf("usage day cache clear failed: %v", err))
	}
}

func usageDayPathKey(path string) string {
	return support.PathKey(path)
}

func usageDayDBPath() (string, error) {
	dir, err := reinsStoreDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, UsageDayDBFileName), nil
}

// usageDayWithConnection：每次调用重算库路径（home 可能被测试覆盖），路径
// 变化即重开连接；打开时的建目录、清残留、pragma、建表与另两库同序。
func usageDayWithConnection(operation func(db *sql.DB) error) error {
	path, err := usageDayDBPath()
	if err != nil {
		return err
	}

	usageDayState.Lock()
	defer usageDayState.Unlock()

	if usageDayState.db != nil && usageDayState.path != path {
		_ = usageDayState.db.Close()
		usageDayState.db = nil
	}

	if usageDayState.db == nil {
		opened, err := openUsageDayConnection(path)
		if err != nil {
			return err
		}
		usageDayState.db = opened
		usageDayState.path = path
	}

	return operation(usageDayState.db)
}

// openUsageDayConnection：按完整库路径打开（调用方传入 home 推导的路径），
// 机械部分在 openCacheConnection。
func openUsageDayConnection(path string) (*sql.DB, error) {
	return openCacheConnection(filepath.Dir(path), UsageDayDBFileName, "usage-day-cache",
		`CREATE TABLE IF NOT EXISTS usage_day_cache (
            source_app TEXT NOT NULL,
            path TEXT NOT NULL,
            mtime INTEGER NOT NULL,
            days TEXT NOT NULL,
            PRIMARY KEY (source_app, path)
        )`)
}
