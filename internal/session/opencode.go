package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reins/internal/support"
)

// opencode.go：OpenCode 读取器（SQLite 来源）。真差异声明：
//   - 孤儿策略 ClaimMissingParent：认领缺失父 id、root 回退首成员（与 zcode
//     同源，与 codex 保留最后已知 id 的语义不同）；
//   - 数据全部在 root 下的 opencode.db 里，扫描根就是 root 目录本身；索引
//     失效只看 db 文件 mtime；
//   - SummaryKind::Rows：摘要直接从 root 行合成；usage/models 在索引扫描时
//     按 session_id 预聚合（贡献表行级增量）；
//   - overview 计数 Declared（SQL COUNT），source_paths 以 db 文件打头；
//   - marker id 沿用历史拼写 "opencode-subagent_started-{id}"。

// OpenCodeSessionRow：索引行。path 是「db路径:id」组合串——行来自 SQLite，
// 没有真实转录文件，组合串是该记录的稳定展示 key。
type OpenCodeSessionRow struct {
	ID          string  `json:"id"`
	ParentID    *string `json:"parent_id"`
	Directory   string  `json:"directory"`
	Title       string  `json:"title"`
	TimeCreated int64   `json:"time_created"`
	TimeUpdated int64   `json:"time_updated"`
	// 归档时间戳，nil 表示未归档；老库可能没有该列，探测后缺失即视为未归档。
	TimeArchived *int64 `json:"time_archived"`
	// 一次 GROUP BY 查询按 session_id 预聚合，避免列表逐会话全表扫。
	TokenUsage *SessionTokenUsage `json:"token_usage"`
	// 会话用过的模型，同样按 session_id 预聚合；主模型排在最前。
	Models []SessionModel `json:"models"`
	Path   string         `json:"path"`
}

// OpenCodeSessionFamily：opencode 族。
type OpenCodeSessionFamily = Family[OpenCodeSessionRow]

type openCodeMessageRow struct {
	id        string
	sessionID string
	// type 列：user/assistant 进消息时间线，其余（system/idle/synthetic/
	// compaction/agent-switched/model-switched）归入事件。
	kind        string
	timeCreated int64
	value       any
}

func (r OpenCodeSessionRow) MemberPath() string       { return r.Path }
func (r OpenCodeSessionRow) FamilyRootID() string     { return r.ID }
func (r OpenCodeSessionRow) MemberID() string         { return r.ID }
func (r OpenCodeSessionRow) MemberCreatedAt() *int64  { return &r.TimeCreated }
func (r OpenCodeSessionRow) MemberUpdatedAt() *int64  { return &r.TimeUpdated }
func (r OpenCodeSessionRow) MemberTieBreaker() string { return r.ID }

func (r OpenCodeSessionRow) ChainID() string { return r.ID }
func (r OpenCodeSessionRow) ChainParentID() (string, bool) {
	if r.ParentID == nil {
		return "", false
	}
	return *r.ParentID, true
}

var (
	_ FamilyRow      = OpenCodeSessionRow{}
	_ ParentChainRow = OpenCodeSessionRow{}
	_ Reader         = (*FamilyReader[OpenCodeSessionRow])(nil)
)

// OpenCodeSpec：opencode 的真差异声明。
type OpenCodeSpec struct{}

func (OpenCodeSpec) App() SourceApp                 { return SourceOpenCode }
func (OpenCodeSpec) Label() string                  { return "OpenCode" }
func (OpenCodeSpec) SummaryKind() SummaryKind       { return SummaryRows }
func (OpenCodeSpec) OverviewCounts() OverviewCounts { return OverviewDeclared }

// ScanRoot：数据全部在 root 下的 opencode.db 里，扫描根就是 root 目录本身。
func (OpenCodeSpec) ScanRoot(root string) string { return root }

func (OpenCodeSpec) ListRows(scanRoot string, cache *RowsDb) ([]OpenCodeSessionRow, error) {
	return opencodeListSessionRows(scanRoot, cache)
}

func (OpenCodeSpec) GroupFamilies(rows []OpenCodeSessionRow) ([]OpenCodeSessionFamily, error) {
	// 孤儿策略与 zcode 同源：认领缺失父 id，root 回退首成员。
	return GroupFamiliesByParentChain(rows, OrphanClaimMissingParent, MemberRoot[OpenCodeSessionRow]), nil
}

func (OpenCodeSpec) LoadMembers(scanRoot string, members []OpenCodeSessionRow) ([]MemberTimeline, error) {
	connection, err := opencodeOpenConnectionAt(scanRoot)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	memberIDs := make([]string, 0, len(members))
	for i := range members {
		memberIDs = append(memberIDs, members[i].ID)
	}
	messageRows, err := opencodeLoadMessageRows(connection, memberIDs)
	if err != nil {
		return nil, err
	}

	messagesBySession := map[string][]SessionMessage{}
	eventsBySession := map[string][]SessionEvent{}

	for _, row := range messageRows {
		if row.kind == "user" || row.kind == "assistant" {
			blocks := opencodeLoadMessageBlocks(row)
			if row.kind == "user" {
				blocks = SanitizeUserBlocks(blocks)
			}
			if len(blocks) == 0 {
				blocks = append(blocks, EmptyMessageBlock("OpenCode", "message has no visible parts", row.value))
			}

			// assistant 消息的 data.model 是这次调用实际用的模型。
			var model *SessionModel
			if id, ok := jsonString(row.value, "model", "id"); ok {
				provider, _ := jsonString(row.value, "model", "providerID")
				sessionModel := NewSessionModel(provider, id)
				model = &sessionModel
			}
			messagesBySession[row.sessionID] = append(messagesBySession[row.sessionID], SessionMessage{
				ID:        row.id,
				Role:      row.kind,
				Timestamp: opencodeMessageRowTimestamp(row),
				Blocks:    blocks,
				Model:     model,
				SessionID: someStr(row.sessionID),
			})
		} else {
			eventsBySession[row.sessionID] = append(eventsBySession[row.sessionID], opencodeEventFromMessageRow(row))
		}
	}

	return assembleMemberTimelines(memberIDs, messagesBySession, eventsBySession), nil
}

func (OpenCodeSpec) AgentName(row OpenCodeSessionRow) string { return row.Title }

// Marker：marker id 沿用历史拼写 "_started"（引擎默认是 "-start-"）；payload
// 差异字段 directory/parent_id 全放 extras，基底由引擎组装。
func (OpenCodeSpec) markerShape(memberID string, row OpenCodeSessionRow) MarkerShape {
	return subagentMarkerShape("opencode", memberID, row.Directory, row.ParentID)
}

func (OpenCodeSpec) AgentLabel(family OpenCodeSessionFamily, row OpenCodeSessionRow) FamilyAgentLabel {
	return familyAgentLabel(row.ID == family.Root.ID, row.Title)
}

func (OpenCodeSpec) Hooks() *FamilyHooks[OpenCodeSessionRow] {
	return &FamilyHooks[OpenCodeSessionRow]{
		// 原始行摘要；(+N) 标题、transcript_path、族级时间戳与 usage 求和由
		// 引擎统一聚合。cwd/git_branch 引擎不动，在这里给。
		SummaryFromRows: func(root OpenCodeSessionRow) SessionSummary {
			return SessionSummary{
				SourceApp:       SourceOpenCode,
				SourceSessionID: root.ID,
				Title:           root.Title,
				Cwd:             someStr(root.Directory),
				GitBranch:       nil,
				TranscriptPath:  root.Path,
				CreatedAt:       someI64(root.TimeCreated),
				UpdatedAt:       someI64(root.TimeUpdated),
				TokenUsage:      nil,
				Models:          root.Models,
				Archived:        root.TimeArchived != nil,
				// OpenCode v2 的转录只存在 sqlite（session_message.data 为明文
				// JSON），没有压缩形态。
				Compressed: false,
			}
		},
		// usage 已在索引扫描时按 session_id 预聚合，family 总量交给引擎默认求和。
		RowUsage:  func(row OpenCodeSessionRow) *SessionTokenUsage { return row.TokenUsage },
		RowModels: func(row OpenCodeSessionRow) []SessionModel { return row.Models },
		// 索引失效只看 db 文件 mtime：全部行都从它 join 出来，行内时间不参与。
		IndexFreshness: func(scanRoot string) (Freshness, error) {
			stamp, err := opencodeDBTimestampAt(scanRoot)
			if err != nil {
				return Freshness{}, err
			}
			return StampFreshness(stamp), nil
		},
		// 时间线失效：max(db mtime, family updated_at)。
		FamilyFreshness: func(scanRoot string, family OpenCodeSessionFamily) (int64, error) {
			dbStamp, err := opencodeDBTimestampAt(scanRoot)
			if err != nil {
				return 0, err
			}
			updated := int64(0)
			if family.UpdatedAt() != nil {
				updated = *family.UpdatedAt()
			}
			if updated > dbStamp {
				return updated, nil
			}
			return dbStamp, nil
		},
		Marker: func(memberID string, row OpenCodeSessionRow) MarkerShape {
			return (OpenCodeSpec{}).markerShape(memberID, row)
		},
		// overview.source_paths：db 文件打头，成员行由它 join 出来，删除只动 db。
		FamilySourcePaths: func(scanRoot string, family OpenCodeSessionFamily) []string {
			paths := []string{opencodeDBPathAt(scanRoot)}
			return append(paths, family.SourcePaths()...)
		},
		CountFamilyRecords: func(scanRoot string, family OpenCodeSessionFamily) (int, int, error) {
			memberIDs := make([]string, 0, len(family.Members))
			for i := range family.Members {
				memberIDs = append(memberIDs, family.Members[i].ID)
			}
			return opencodeCountFamilyRecords(scanRoot, memberIDs)
		},
	}
}

// OpenCodeReader：static 前置。
var OpenCodeReader = NewFamilyReader[OpenCodeSessionRow](OpenCodeSpec{}, OpenCodeRoot)

// OpenCodeRoot：env 解析的 opencode 根（无官方重定向，固定 ~/.local/share/opencode）。
func OpenCodeRoot() (string, error) {
	home, ok := support.UserHomeDir()
	if !ok {
		return "", fmt.Errorf("Unable to determine home directory")
	}
	return filepath.Join(home, ".local", "share", "opencode"), nil
}

// OpenCodeDBPath：模块级 env 版 db 路径，仅供生产路径（usage）使用；reader
// 引擎实例的数据查询一律从实例 scan_root 派生，env 与实例互不串扰。
func OpenCodeDBPath() (string, error) {
	root, err := OpenCodeRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "opencode.db"), nil
}

func opencodeDBPathAt(scanRoot string) string {
	return filepath.Join(scanRoot, "opencode.db")
}

func opencodeOpenConnectionAt(scanRoot string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", opencodeDBPathAt(scanRoot))
	if err != nil {
		return nil, fmt.Errorf("Failed to open OpenCode sqlite database: %w", err)
	}
	return db, nil
}

// opencodeDBTimestampAt：db 缺失（来源还没有会话）时取 0，让 family_index
// 走到空列表分支，而不是在 metadata 读取上失败。
func opencodeDBTimestampAt(scanRoot string) (int64, error) {
	path := opencodeDBPathAt(scanRoot)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0, nil
	}
	return support.FileModifiedTimestampMillis(path)
}

// assistantContrib：assistant 消息的行级贡献——usage 求和与模型分组的持久
// 最小单元。行内容可变（opencode 会回写消息行），不能按会话或 rowid 水位做
// 增量合并；失效键用消息行自己的 time_updated——data 变化伴随它推进。轻扫
// 只读 (id, time_updated) 两个整数列，不触碰 data 大 JSON；仅变化行做
// json_extract。
type assistantContrib struct {
	SessionID   string  `json:"session_id"`
	MessageTime int64   `json:"message_time"`
	Input       int64   `json:"input"`
	Output      int64   `json:"output"`
	Reasoning   int64   `json:"reasoning"`
	CacheRead   int64   `json:"cache_read"`
	CacheWrite  int64   `json:"cache_write"`
	Provider    *string `json:"provider"`
	Model       *string `json:"model"`
}

func opencodeListSessionRows(scanRoot string, cache *RowsDb) ([]OpenCodeSessionRow, error) {
	// db 与 session_v2 表都随首次会话写入才落地，缺失只说明该来源还没有会话。
	// 这里用读写模式打开，不先判存在就会凭空创建一个空 db 文件。
	dbPath := opencodeDBPathAt(scanRoot)
	if info, err := os.Stat(dbPath); err != nil || !info.Mode().IsRegular() {
		return []OpenCodeSessionRow{}, nil
	}

	connection, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to open OpenCode sqlite database: %w", err)
	}
	defer connection.Close()

	if exists, err := SQLiteTableExists(connection, "session_v2"); err != nil {
		return nil, err
	} else if !exists {
		return []OpenCodeSessionRow{}, nil
	}

	contribs, err := opencodeSyncAssistantContribs(connection, cache, dbPath)
	if err != nil {
		return nil, err
	}
	tokenUsages := opencodeUsageBySession(contribs)
	modelsBySession := opencodeModelsBySession(contribs)
	// time_archived 由 v1→v2 迁移期加入，老库可能没有该列；缺失时列表照常
	// 产出，归档位恒假。
	archivedColumn, err := SQLiteColumnExists(connection, "session_v2", "time_archived")
	if err != nil {
		return nil, err
	}
	archivedExpr := "NULL"
	if archivedColumn {
		archivedExpr = "time_archived"
	}
	// model 同样是迁移期加入的列，老库探测后缺失时按无模型处理。
	modelColumn, err := SQLiteColumnExists(connection, "session_v2", "model")
	if err != nil {
		return nil, err
	}
	modelExpr := "NULL"
	if modelColumn {
		modelExpr = "model"
	}

	// session_v2.title 允许 NULL（v1 时代 NOT NULL），空标题回退到 id，避免
	// family 标题渲染成空白。
	rows, err := connection.Query(fmt.Sprintf(
		"SELECT id, parent_id, directory, title, time_created, time_updated, %s, %s FROM session_v2 ORDER BY time_updated DESC",
		archivedExpr, modelExpr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	collection := make([]OpenCodeSessionRow, 0)
	for rows.Next() {
		var id string
		var parentID *string
		var directory string
		var title *string
		var timeCreated int64
		var timeUpdated int64
		var timeArchived *int64
		var rawModel *string
		if err := rows.Scan(&id, &parentID, &directory, &title, &timeCreated, &timeUpdated, &timeArchived, &rawModel); err != nil {
			return nil, fmt.Errorf("Failed to read OpenCode sessions: %w", err)
		}

		usage := tokenUsages[id]
		models, hasModels := modelsBySession[id]
		if !hasModels {
			models = nil
			// 还没有 assistant 消息的新会话：session_v2.model 是当前选中的
			// 模型，是这时唯一能取到的模型信息。
			if rawModel != nil {
				if value, err := decodeJSONBytes([]byte(*rawModel)); err == nil {
					if modelID, ok := jsonString(value, "id"); ok {
						provider, _ := jsonString(value, "providerID")
						models = []SessionModel{NewSessionModel(provider, modelID)}
					}
				}
			}
		}

		rowTitle := id
		if title != nil && *title != "" {
			rowTitle = *title
		}
		// v2 会话只存在于 SQLite，没有 transcript 文件；用「db路径:id」组合串
		// 作为该记录的稳定 key。整体不是真实路径，path_key 会走原样字符串分支。
		collection = append(collection, OpenCodeSessionRow{
			ID:           id,
			ParentID:     parentID,
			Directory:    directory,
			Title:        rowTitle,
			TimeCreated:  timeCreated,
			TimeUpdated:  timeUpdated,
			TimeArchived: timeArchived,
			TokenUsage:   usage,
			Models:       models,
			Path:         fmt.Sprintf("%s:%s", dbPath, id),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Failed to read OpenCode sessions: %w", err)
	}
	return collection, nil
}

// opencodeSyncAssistantContribs：贡献表同步——diff 后只提取变化行，返回当前
// 全量贡献。db 的 session_message 表缺失只说明还没有消息，按空贡献处理。
func opencodeSyncAssistantContribs(connection *sql.DB, cache *RowsDb, dbPath string) ([]assistantContrib, error) {
	exists, err := SQLiteTableExists(connection, "session_message")
	if err != nil {
		return nil, err
	}
	if !exists {
		return []assistantContrib{}, nil
	}

	current := map[string]int64{}
	stampRows, err := connection.Query("SELECT id, time_updated FROM session_message WHERE type = 'assistant'")
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare OpenCode assistant scan: %w", err)
	}
	for stampRows.Next() {
		var id string
		var stamp int64
		if err := stampRows.Scan(&id, &stamp); err != nil {
			stampRows.Close()
			return nil, fmt.Errorf("Failed to read OpenCode assistant stamps: %w", err)
		}
		current[id] = stamp
	}
	if err := stampRows.Err(); err != nil {
		stampRows.Close()
		return nil, fmt.Errorf("Failed to read OpenCode assistant stamps: %w", err)
	}
	stampRows.Close()

	dbKey := support.PathKey(dbPath)
	cached := cache.LoadContribs(SourceOpenCode, dbKey)
	cachedStamps := make(map[string]int64, len(cached))
	contribs := map[string]assistantContrib{}
	for _, contrib := range cached {
		cachedStamps[contrib.ID] = contrib.Stamp
		var payload assistantContrib
		if err := json.Unmarshal([]byte(contrib.Payload), &payload); err == nil {
			contribs[contrib.ID] = payload
		}
	}

	var changedIDs []string
	for id, stamp := range current {
		if cachedStamp, ok := cachedStamps[id]; !ok || cachedStamp != stamp {
			changedIDs = append(changedIDs, id)
		}
	}
	var removedIDs []string
	for id := range cachedStamps {
		if _, ok := current[id]; !ok {
			removedIDs = append(removedIDs, id)
		}
	}

	if len(changedIDs) > 0 || len(removedIDs) > 0 {
		fresh, err := opencodeExtractAssistantContribs(connection, changedIDs)
		if err != nil {
			return nil, err
		}
		upserts := make([]Contrib, 0, len(fresh))
		for _, entry := range fresh {
			payload, err := json.Marshal(entry.contrib)
			if err != nil {
				return nil, fmt.Errorf("Failed to serialize OpenCode assistant contribution: %w", err)
			}
			upserts = append(upserts, Contrib{ID: entry.id, Stamp: entry.stamp, Payload: string(payload)})
			contribs[entry.id] = entry.contrib
		}
		cache.SyncContribs(SourceOpenCode, dbKey, upserts, removedIDs)
	}

	values := make([]assistantContrib, 0, len(contribs))
	for _, contrib := range contribs {
		values = append(values, contrib)
	}
	return values, nil
}

type opencodeContribRow struct {
	id      string
	stamp   int64
	contrib assistantContrib
}

// opencodeExtractAssistantContribs：变化行的 json_extract；token 原始列与模
// 型字段一次取齐，message_time 的 COALESCE 口径与消息时间线一致
// （data.time.created 回退 time_created）。冷缓存时变化行是全量消息 id，单条
// IN (...) 会超绑定变量上限，且失败发生在缓存写入之前，形成重试也不恢复的
// 循环，按批拆分提取。
func opencodeExtractAssistantContribs(connection *sql.DB, ids []string) ([]opencodeContribRow, error) {
	rows := make([]opencodeContribRow, 0)
	for start := 0; start < len(ids); start += SqlParameterBatch {
		end := start + SqlParameterBatch
		if end > len(ids) {
			end = len(ids)
		}
		batch, err := opencodeExtractAssistantContribsBatch(connection, ids[start:end])
		if err != nil {
			return nil, err
		}
		rows = append(rows, batch...)
	}
	return rows, nil
}

func opencodeExtractAssistantContribsBatch(connection *sql.DB, ids []string) ([]opencodeContribRow, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := fmt.Sprintf(`SELECT id,
            session_id,
            time_updated,
            COALESCE(json_extract(data, '$.time.created'), time_created),
            json_extract(data, '$.model.providerID'),
            json_extract(data, '$.model.id'),
            COALESCE(json_extract(data, '$.tokens.input'), 0),
            COALESCE(json_extract(data, '$.tokens.output'), 0),
            COALESCE(json_extract(data, '$.tokens.reasoning'), 0),
            COALESCE(json_extract(data, '$.tokens.cache.read'), 0),
            COALESCE(json_extract(data, '$.tokens.cache.write'), 0)
     FROM session_message
     WHERE type = 'assistant' AND id IN (%s)`, placeholders)

	rows, err := connection.Query(query, argsFromStrings(ids)...)
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare OpenCode assistant extract: %w", err)
	}
	defer rows.Close()

	collection := make([]opencodeContribRow, 0)
	for rows.Next() {
		var entry opencodeContribRow
		if err := rows.Scan(&entry.id, &entry.contrib.SessionID, &entry.stamp, &entry.contrib.MessageTime,
			&entry.contrib.Provider, &entry.contrib.Model,
			&entry.contrib.Input, &entry.contrib.Output, &entry.contrib.Reasoning,
			&entry.contrib.CacheRead, &entry.contrib.CacheWrite); err != nil {
			return nil, fmt.Errorf("Failed to read OpenCode assistant contributions: %w", err)
		}
		collection = append(collection, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Failed to read OpenCode assistant contributions: %w", err)
	}
	return collection, nil
}

// argsFromStrings：IN (...) 的绑定参数。
func argsFromStrings(ids []string) []any {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return args
}

// opencodeUsageBySession：assistant 消息的 data.tokens 是每次调用的增量，按
// 会话求和后统一走归一口径（钳制在求和之后，与原 SQL SUM 的顺序一致）；
// reasoning 与 output 分列存储，输出侧相加才与其他来源口径一致。
func opencodeUsageBySession(contribs []assistantContrib) map[string]*SessionTokenUsage {
	sums := map[string]UsageColumns{}
	for _, contrib := range contribs {
		columns := sums[contrib.SessionID]
		columns.Input += contrib.Input
		columns.Output += contrib.Output
		columns.Reasoning += contrib.Reasoning
		columns.CacheRead += contrib.CacheRead
		columns.CacheWrite += contrib.CacheWrite
		sums[contrib.SessionID] = columns
	}

	usages := make(map[string]*SessionTokenUsage, len(sums))
	for sessionID, columns := range sums {
		usage := SplitReasoningUsage(columns)
		usages[sessionID] = &usage
	}
	return usages
}

// opencodeModelsBySession：会话用过的模型——同模型可能换过 provider，一并
// 计入。计数只用于排序（主模型排最前），调用次数相同则按模型名稳定排序
// （原 SQL 口径，provider 顺序在 SQL 里未定义，这里取 provider 升序保证
// 确定性）。
func opencodeModelsBySession(contribs []assistantContrib) map[string][]SessionModel {
	type modelCall struct {
		sessionID string
		provider  *string
		model     string
	}
	calls := map[modelCall]int{}
	for _, contrib := range contribs {
		if contrib.Model != nil {
			calls[modelCall{sessionID: contrib.SessionID, provider: contrib.Provider, model: *contrib.Model}]++
		}
	}

	type countedCall struct {
		provider *string
		model    string
		count    int
	}
	perSession := map[string][]countedCall{}
	for call, count := range calls {
		perSession[call.sessionID] = append(perSession[call.sessionID], countedCall{
			provider: call.provider, model: call.model, count: count,
		})
	}

	models := make(map[string][]SessionModel, len(perSession))
	for sessionID, entries := range perSession {
		sort.SliceStable(entries, func(i, j int) bool {
			if entries[i].count != entries[j].count {
				return entries[i].count > entries[j].count
			}
			if entries[i].model != entries[j].model {
				return entries[i].model < entries[j].model
			}
			return compareOptionalString(entries[i].provider, entries[j].provider) < 0
		})
		row := make([]SessionModel, 0, len(entries))
		for _, entry := range entries {
			row = append(row, NewSessionModel(derefString(entry.provider), entry.model))
		}
		models[sessionID] = row
	}
	return models
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// compareOptionalString 对齐 Rust Option<String> 的 Ord：None < Some(_)。
func compareOptionalString(left *string, right *string) int {
	switch {
	case left == nil && right == nil:
		return 0
	case left == nil:
		return -1
	case right == nil:
		return 1
	case *left < *right:
		return -1
	case *left > *right:
		return 1
	default:
		return 0
	}
}

// opencodeUsageHours：用量曲线的小时桶——assistant 消息的增量 usage 按消息
// 时间归小时，数据来自贡献表（与列表扫描共享同一份同步，活跃写入时不再全表
// json_extract）；归一口径与列表侧一致（SplitReasoningUsage 单点），消息时间
// 优先 data.time.created，回退 time_created 列，与消息时间线一致。db 缺失、
// 或 session_message 表还没建，都表示来源不可用，返回 nil。
func OpenCodeUsageHours() (*SqlUsageHours, error) {
	dbPath, err := OpenCodeDBPath()
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(dbPath); err != nil || !info.Mode().IsRegular() {
		return nil, nil
	}
	connection, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, nil
	}
	defer connection.Close()

	exists, err := SQLiteTableExists(connection, "session_message")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	cache, err := RowsDbAtStoreDir()
	if err != nil {
		return nil, err
	}
	// 函数级句柄用完即关：不关会在每次用量采集时泄漏一条 SQLite 连接
	// （Windows 下还占住库文件删不掉）。
	defer cache.Close()
	contribs, err := opencodeSyncAssistantContribs(connection, cache, dbPath)
	if err != nil {
		return nil, err
	}

	rows := make([]UsageRow, 0, len(contribs))
	for _, contrib := range contribs {
		usage := SplitReasoningUsage(UsageColumns{
			Input:      contrib.Input,
			Output:     contrib.Output,
			Reasoning:  contrib.Reasoning,
			CacheRead:  contrib.CacheRead,
			CacheWrite: contrib.CacheWrite,
		})
		rows = append(rows, UsageRow{
			SessionID: contrib.SessionID,
			Timestamp: contrib.MessageTime,
			Model:     contrib.Model,
			Usage:     usage,
		})
	}

	hours, err := HoursFromUsageRows(rows)
	if err != nil {
		return nil, fmt.Errorf("Failed to read OpenCode usage hours: %w", err)
	}
	return &hours, nil
}

// opencodeCountFamilyRecords：与时间线加载同一条分类边界——user/assistant
// 行是消息，其余行是事件。
func opencodeCountFamilyRecords(scanRoot string, memberIDs []string) (int, int, error) {
	connection, err := opencodeOpenConnectionAt(scanRoot)
	if err != nil {
		return 0, 0, err
	}
	defer connection.Close()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(memberIDs)), ",")
	var messageCount, eventCount int
	if err := connection.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM session_message WHERE session_id IN (%s) AND type IN ('user','assistant')",
		placeholders), argsFromStrings(memberIDs)...).Scan(&messageCount); err != nil {
		return 0, 0, fmt.Errorf("Failed to count OpenCode messages: %w", err)
	}
	if err := connection.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM session_message WHERE session_id IN (%s) AND type NOT IN ('user','assistant')",
		placeholders), argsFromStrings(memberIDs)...).Scan(&eventCount); err != nil {
		return 0, 0, fmt.Errorf("Failed to count OpenCode events: %w", err)
	}
	return messageCount, eventCount, nil
}

func opencodeLoadMessageRows(connection *sql.DB, memberIDs []string) ([]openCodeMessageRow, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(memberIDs)), ",")
	rows, err := connection.Query(fmt.Sprintf(
		"SELECT id, session_id, type, time_created, data FROM session_message WHERE session_id IN (%s) ORDER BY time_created ASC, id ASC",
		placeholders), argsFromStrings(memberIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messageRows := make([]openCodeMessageRow, 0)
	for rows.Next() {
		var id, sessionID, kind, rawData string
		var timeCreated int64
		if err := rows.Scan(&id, &sessionID, &kind, &timeCreated, &rawData); err != nil {
			return nil, err
		}
		value, err := decodeJSONBytes([]byte(rawData))
		if err != nil {
			return nil, fmt.Errorf("Invalid OpenCode message JSON for %s: %w", id, err)
		}
		messageRows = append(messageRows, openCodeMessageRow{
			id: id, sessionID: sessionID, kind: kind, timeCreated: timeCreated, value: value,
		})
	}
	return messageRows, rows.Err()
}

// opencodeMessageRowTimestamp：消息时间戳优先取 data.time.created，部分 type
// （如 compaction）的 data 可能没有 time，回退 time_created 列。
func opencodeMessageRowTimestamp(row openCodeMessageRow) *int64 {
	if created, ok := jsonI64Path(row.value, "time", "created"); ok {
		return &created
	}
	fallback := row.timeCreated
	return &fallback
}

func opencodeLoadMessageBlocks(row openCodeMessageRow) []ContentBlock {
	if row.kind == "user" {
		return opencodeUserMessageBlocks(row.value)
	}
	return opencodeAssistantMessageBlocks(row.value)
}

func opencodeUserMessageBlocks(value any) []ContentBlock {
	blocks := make([]ContentBlock, 0)

	if text, ok := jsonString(value, "text"); ok {
		// 附件的 base64 数据不进 payload，只保留文字说明。
		blocks = append(blocks, ContentBlock{Kind: "text", Text: &text})
	}

	files, ok := jsonArray(value, "files")
	if ok {
		for _, file := range files {
			filename, hasFilename := jsonString(file, "name")
			mime, hasMime := jsonString(file, "mime")
			var text *string
			if hasFilename && hasMime {
				formatted := fmt.Sprintf("文件：%s\n类型：%s", filename, mime)
				text = &formatted
			} else if hasFilename {
				formatted := fmt.Sprintf("文件：%s", filename)
				text = &formatted
			} else if hasMime {
				formatted := fmt.Sprintf("文件类型：%s", mime)
				text = &formatted
			}
			if text != nil {
				blocks = append(blocks, ContentBlock{Kind: "file", Text: text})
			}
		}
	}

	return blocks
}

func opencodeAssistantMessageBlocks(value any) []ContentBlock {
	blocks := make([]ContentBlock, 0)
	content, ok := jsonArray(value, "content")
	if !ok {
		return blocks
	}

	for _, item := range content {
		kind, _ := jsonString(item, "type")
		if kind == "" {
			kind = "unknown"
		}

		if kind == "tool" {
			toolBlocks := ToolBlocks(item, opencodeToolShape)
			if len(toolBlocks) == 0 {
				blocks = append(blocks, EmptyToolBlock("OpenCode", item))
			} else {
				blocks = append(blocks, toolBlocks...)
			}
			continue
		}

		normalizedKind := kind
		if kind == "reasoning" {
			normalizedKind = "thinking"
		}
		text, hasText := jsonString(item, "text")

		if !hasText && normalizedKind != "text" && normalizedKind != "thinking" {
			blocks = append(blocks, UnsupportedBlock("OpenCode", item))
		} else {
			blocks = append(blocks, ContentBlock{
				Kind:    normalizedKind,
				Text:    optionalString(text, hasText),
				Payload: item,
			})
		}
	}

	return blocks
}

// opencodeEventFromMessageRow：raw 页要看到原始记录——DB 行整行给出去（data
// 列是应用写入的原始 JSON）。
func opencodeEventFromMessageRow(row openCodeMessageRow) SessionEvent {
	payload := map[string]any{
		"id":           row.id,
		"session_id":   row.sessionID,
		"type":         row.kind,
		"time_created": row.timeCreated,
		"data":         row.value,
	}

	return SessionEvent{
		ID:        row.id,
		Kind:      row.kind,
		Timestamp: opencodeMessageRowTimestamp(row),
		Summary:   opencodeV2EventSummary(row.kind, row.value),
		Payload:   RecordPayload(payload),
		SessionID: someStr(row.sessionID),
	}
}

// opencodeV2EventSummary：共享 SummarizeEvent 只认 message/type/name 字段，
// v2 事件的可读字段因 type 而异（compaction 的总结、idle 的 outcome、
// agent-switched 的新 agent 名），先取这些字段生成摘要，取不到再退回通用
// 逻辑。synthetic 的 text 是 <system-reminder> 噪音，不作为摘要来源。
func opencodeV2EventSummary(kind string, value any) string {
	for _, key := range []string{"description", "summary", "outcome", "agent"} {
		if text, ok := jsonString(value, key); ok {
			normalized := NormalizeTitle(text)
			if normalized != "" {
				return TruncateSummary(fmt.Sprintf("%s: %s", kind, normalized))
			}
		}
	}
	return SummarizeEvent(kind, value)
}

// opencodeToolShape：v2 tool 块 {type,id,name,state:{status,input,content,
// metadata},time}。id 就是调用 id（call_xxx，v1 叫 callID）；输出在
// state.content 块数组里（v1 是 state.output 字符串）。装配体在 tool_blocks，
// 这里只声明字段差异。
var opencodeToolShape = ToolShape{
	ToolNameKey:   "name",
	CallIDKey:     "id",
	OutputKey:     "content",
	ExtractOutput: ContentBlocksOutput,
}

// someI64：Option<i64> 的 Some 包装。
func someI64(value int64) *int64 { return &value }
