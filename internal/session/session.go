package session

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

// mod.rs 的域级共享助手：SQLite 探测、freshness 指纹、目录条目排序、来源
// 根解析、清缓存编排。reader(source_app) 注册表在 timeline.go（查 sources
// 注册表）；rescan/clear_all_caches 在本文件末尾（依赖注册表，2b 落地）。

// RescanAllCaches：重新扫盘——只丢内存层（目录索引、时间线、内存摘要），
// 持久层不动。刷新与删除走这条：文件集合与 mtime 的变化由索引失效与缓存键
// 自己发现。
func RescanAllCaches() error {
	for _, spec := range Sources {
		if err := spec.Reader.ClearCache(); err != nil {
			return err
		}
	}
	return nil
}

// ClearAllCaches：全量重建的逃生通道（显式清缓存命令）——持久层一并清空。
//
// 持久摘要与用量缓存按 (路径, mtime) 自失效，清空它们并不是刷新正确性所需，
// 而是「怀疑缓存本身」时的手段；代价随会话总量增长——本机 codex 侧清一次
// 要重新解析 1329 个转录（27.6s），只丢内存层是 1.4s。解析逻辑变更走缓存
// 文件版本号 bump（SummaryDBFileName），不靠这条路径。
func ClearAllCaches() error {
	if err := RescanAllCaches(); err != nil {
		return err
	}
	// 持久摘要与行缓存跨来源共用同一份库文件，所以清一次即覆盖所有用到它的来源。
	summaryDB, err := SummaryDbAtStoreDir()
	if err != nil {
		return err
	}
	defer summaryDB.Close()
	summaryDB.ClearAll()
	rowsDB, err := RowsDbAtStoreDir()
	if err != nil {
		return err
	}
	defer rowsDB.Close()
	rowsDB.ClearAll()
	UsageDayClearAll()
	return nil
}

// Reader：SessionReader trait 的 Go 接口缝。每来源恰好一个实现（GLOSSARY
// 读取器词条），会话身份是 (来源, source_session_id) 二元组——接口不认
// transcript_path。
//
// Rust 的 default 方法语义由实现方保持：ClearCache 无缓存语义的读取器返回
// nil；ParseAgentMessages 不支持独立子会话的来源返回与 Rust 相同的错误
// （「该来源的子代理不以独立会话存储…」），让「来源不支持」与「子会话确实
// 没消息」保持可区分。
type Reader interface {
	ListEntries() ([]SessionFileEntry, error)
	ClearCache() error
	ParseSummary(sourceSessionID string) (SessionSummary, error)
	ParseOverview(sourceSessionID string) (SessionOverview, error)
	// 会话详情的正文全量返回：时间线分组器需要完整消息流才能配对调用与
	// 结果、收拢子代理入口，分页会把跨页配对变成脏数据。
	ParseMessages(sourceSessionID string) ([]SessionMessage, error)
	ParseEvents(sourceSessionID string) ([]SessionEvent, error)
	// family 聚合来源（Codex/Claude Code/OpenCode/Pi）按 agent session id 取
	// 该子代理的全部消息，子代理入口弹窗用它。
	ParseAgentMessages(sourceSessionID string, agentSessionID string) ([]SessionMessage, error)
}

// SqlParameterBatch：SQLite 绑定变量有编译期上限（bundled 为 32766，历史
// 构建 999），动态拼 IN (?,?,...) 的读取点必须按批拆分，批大小取远低于一切
// 上限的值。
const SqlParameterBatch = 900

// SQLiteTableExists：SQLite 来源的 db 文件与数据表都随该来源首次写入才落地，
// 表缺失只说明它还没有会话。读取器据此把「没有数据」与「读取失败」分开，
// 不把 no such table 报给用户。
func SQLiteTableExists(db *sql.DB, table string) (bool, error) {
	var one *int64
	err := db.QueryRow(
		"SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", table,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("Failed to inspect sqlite table %s: %w", table, err)
	}
	return true, nil
}

// SQLiteColumnExists：同 SQLiteTableExists 的列级探测。zcode/opencode 的归档
// 列是后加的，老库没有该列只说明该来源还没有归档这一状态，读取器据此跳过，
// 而不是让整条列表报错。
func SQLiteColumnExists(db *sql.DB, table string, column string) (bool, error) {
	var one *int64
	err := db.QueryRow(
		"SELECT 1 FROM pragma_table_info(?) WHERE name = ?", table, column,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("Failed to inspect sqlite column %s.%s: %w", table, column, err)
	}
	return true, nil
}

// SpecRoot：来源会话根的统一解析，走 agents 清单的 GlobalRoot（重定向优先、
// HOME 兜底），与 workspace/providers 同一条路径来源——设了 CODEX_HOME 之类
// 的重定向后，各域看到同一份数据。pi 例外：它的根有官方 sessionDir 链等
// 特有契约，留在 pi 读取器（2b）里，行为本身已认重定向。
func SpecRoot(app SourceApp) (string, error) {
	env := support.FromEnv(agents.Redirects())
	spec := agents.SpecBySourceApp(app)
	if spec == nil {
		// SourceApp 的合法集合来自同一张清单，落到这里说明清单与枚举脱节，
		// 是编程错误而非运行时错误——对齐 Rust 的 expect。
		panic("registered in AgentSpecs")
	}
	root, ok := spec.Global.Root.Resolve(env)
	if !ok {
		return "", fmt.Errorf("Unable to determine home directory")
	}
	return root, nil
}

// SessionsFingerprint：文件集合的新鲜度指纹，(路径, mtime, 大小) 排序串接。
// stat 全集合远便宜于解析，自管 freshness 的目录型来源（pi/grokbuild/dsh）
// 用它门控各自的索引缓存；口径只有这一份，各家不得再拼。
func SessionsFingerprint(files []string) (string, error) {
	parts := make([]string, 0, len(files))
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("Failed to read metadata for %s: %w", path, err)
		}
		mtime, err := support.FileModifiedTimestampMillis(path)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", path, mtime, info.Size()))
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n"), nil
}

// sortEntries：目录条目按时间倒序、同刻按路径升序（2b 的 catalog 与引擎共用）。
func sortEntries(entries []SessionFileEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].SortTimestamp != entries[j].SortTimestamp {
			return entries[i].SortTimestamp > entries[j].SortTimestamp
		}
		return comparePaths(entries[i].Path, entries[j].Path) < 0
	})
}

// comparePaths 对齐 Rust PathBuf 的 Ord：按分量逐段比较（而非整串字节比较），
// 使 "a/b" 与 "a.b" 这类跨分隔符边界的大小关系与 Rust 一致。
func comparePaths(left string, right string) int {
	leftParts := splitPathComponents(left)
	rightParts := splitPathComponents(right)
	for i := 0; i < len(leftParts) && i < len(rightParts); i++ {
		if leftParts[i] != rightParts[i] {
			if leftParts[i] < rightParts[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(leftParts) < len(rightParts):
		return -1
	case len(leftParts) > len(rightParts):
		return 1
	default:
		return 0
	}
}

// splitPathComponents：对齐 Rust PathBuf 的分量切分——Windows 下 / 与 \
// 都是合法分隔符，转录里的路径多为 / 形态，按单一平台分隔符切会把整串
// 当成一个分量，分量序与整串字节序的大小关系随之分叉。
func splitPathComponents(path string) []string {
	normalized := strings.ReplaceAll(path, "\\", "/")
	trimmed := strings.Trim(normalized, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
