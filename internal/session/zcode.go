package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reins/internal/support"
)

// zcode.go：ZCode 读取器（SQLite 来源）。真差异声明：
//   - 孤儿策略 ClaimMissingParent：认领缺失父 id、root 回退首成员（与
//     opencode 同源）；
//   - root() 就是 db 文件本身：可用性以 db 为准，扫描根与其重合（目录存在
//     但 db 缺失时只读打开会失败，让 detect 的 available=false 而不是让整个
//     来源检测报错）；索引失效只看 db mtime；
//   - SummaryKind::Rows；usage/models 在索引扫描时按 session_id 预聚合
//     （turn_usage 求和、model_usage GROUP BY）；
//   - 消息级模型来自 model_usage 的调用明细，按 assistant_message_id 归到
//     产出该回答的那次调用；同一消息多次尝试取最早一次；
//   - message/part 两表装配双半；semantics.kind 是 zcode 官方消息语义标记
//     （user_prompt / assistant_response 进时间线，其余归事件）；
//   - marker id 沿用历史拼写 "zcode-subagent_started-{id}"。

// ZcodeSessionRow：索引行。
type ZcodeSessionRow struct {
	ID          string  `json:"id"`
	ParentID    *string `json:"parent_id"`
	Directory   string  `json:"directory"`
	Title       string  `json:"title"`
	TimeCreated int64   `json:"time_created"`
	TimeUpdated int64   `json:"time_updated"`
	// 归档时间戳，nil 表示未归档；老库可能没有该列，探测后缺失即视为未归档。
	TimeArchived *int64             `json:"time_archived"`
	TokenUsage   *SessionTokenUsage `json:"token_usage"`
	Models       []SessionModel     `json:"models"`
	// 「db路径:id」组合串：行来自 SQLite 没有真实转录文件，组合串是该记录
	// 的稳定展示 key。
	Path string `json:"path"`
}

// ZcodeSessionFamily：zcode 族。
type ZcodeSessionFamily = Family[ZcodeSessionRow]

type zcodeMessageRow struct {
	id        string
	sessionID string
	// data.role 列：user/assistant。
	role string
	// data.semantics.kind 是 zcode 官方的消息语义标记：user_prompt /
	// assistant_response 进消息时间线，todo_reminder / timeline_event 等归事件。
	kind        string
	timeCreated int64
	value       any
}

func (r ZcodeSessionRow) MemberPath() string       { return r.Path }
func (r ZcodeSessionRow) FamilyRootID() string     { return r.ID }
func (r ZcodeSessionRow) MemberID() string         { return r.ID }
func (r ZcodeSessionRow) MemberCreatedAt() *int64  { return &r.TimeCreated }
func (r ZcodeSessionRow) MemberUpdatedAt() *int64  { return &r.TimeUpdated }
func (r ZcodeSessionRow) MemberTieBreaker() string { return r.ID }

func (r ZcodeSessionRow) ChainID() string { return r.ID }
func (r ZcodeSessionRow) ChainParentID() (string, bool) {
	if r.ParentID == nil {
		return "", false
	}
	return *r.ParentID, true
}

var (
	_ FamilyRow      = ZcodeSessionRow{}
	_ ParentChainRow = ZcodeSessionRow{}
	_ Reader         = (*FamilyReader[ZcodeSessionRow])(nil)
)

// ZcodeSpec：zcode 的真差异声明。
type ZcodeSpec struct{}

func (ZcodeSpec) App() SourceApp                 { return SourceZcode }
func (ZcodeSpec) Label() string                  { return "ZCode" }
func (ZcodeSpec) SummaryKind() SummaryKind       { return SummaryRows }
func (ZcodeSpec) OverviewCounts() OverviewCounts { return OverviewDeclared }

// ScanRoot：root() 就是 db 文件本身，扫描根与其重合。
func (ZcodeSpec) ScanRoot(root string) string { return root }

func (ZcodeSpec) ListRows(scanRoot string, _ *RowsDb) ([]ZcodeSessionRow, error) {
	return zcodeListSessionRows(scanRoot)
}

func (ZcodeSpec) GroupFamilies(rows []ZcodeSessionRow) ([]ZcodeSessionFamily, error) {
	// 孤儿策略与 opencode 同源：认领缺失父 id，root 回退首成员。
	return GroupFamiliesByParentChain(rows, OrphanClaimMissingParent, MemberRoot[ZcodeSessionRow]), nil
}

func (ZcodeSpec) LoadMembers(scanRoot string, members []ZcodeSessionRow) ([]MemberTimeline, error) {
	connection, err := zcodeOpenConnectionAt(scanRoot)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	memberIDs := make([]string, 0, len(members))
	for i := range members {
		memberIDs = append(memberIDs, members[i].ID)
	}
	messageRows, err := zcodeLoadMessageRows(connection, memberIDs)
	if err != nil {
		return nil, err
	}
	partsByMessage, err := zcodeLoadPartsByMessage(connection, memberIDs)
	if err != nil {
		return nil, err
	}
	// 消息级模型来自 model_usage 的调用明细，按 assistant_message_id 归到
	// 产出该回答的那次调用；同一消息有多次尝试时取最早一次。
	modelsByMessage, err := zcodeLoadModelsByMessage(connection, memberIDs)
	if err != nil {
		return nil, err
	}

	messagesBySession := map[string][]SessionMessage{}
	eventsBySession := map[string][]SessionEvent{}

	for _, row := range messageRows {
		if row.kind == "user_prompt" || row.kind == "assistant_response" {
			parts := partsByMessage[row.id]
			blocks := zcodeLoadMessageBlocks(row, parts)
			if row.kind == "user_prompt" {
				blocks = SanitizeUserBlocks(blocks)
			}
			if len(blocks) == 0 {
				blocks = append(blocks, EmptyMessageBlock("ZCode", "message has no visible parts", row.value))
			}

			messagesBySession[row.sessionID] = append(messagesBySession[row.sessionID], SessionMessage{
				ID:        row.id,
				Role:      row.role,
				Timestamp: zcodeMessageRowTimestamp(row),
				Blocks:    blocks,
				Model:     cloneSessionModel(modelsByMessage[row.id]),
				SessionID: someStr(row.sessionID),
			})
		} else {
			eventsBySession[row.sessionID] = append(eventsBySession[row.sessionID], zcodeEventFromMessageRow(row, partsByMessage[row.id]))
		}
	}

	return assembleMemberTimelines(memberIDs, messagesBySession, eventsBySession), nil
}

func (ZcodeSpec) AgentName(row ZcodeSessionRow) string { return row.Title }

// markerShape：marker id 沿用历史拼写 "_started"（引擎默认是 "-start-"）；
// payload 差异字段 directory/parent_id 全放 extras，基底由引擎组装。
func (ZcodeSpec) markerShape(memberID string, row ZcodeSessionRow) MarkerShape {
	return subagentMarkerShape("zcode", memberID, row.Directory, row.ParentID)
}

func (ZcodeSpec) AgentLabel(family ZcodeSessionFamily, row ZcodeSessionRow) FamilyAgentLabel {
	return familyAgentLabel(row.ID == family.Root.ID, row.Title)
}

func (ZcodeSpec) Hooks() *FamilyHooks[ZcodeSessionRow] {
	return &FamilyHooks[ZcodeSessionRow]{
		// 原始行摘要；(+N) 标题、transcript_path、族级时间戳与 usage 求和由
		// 引擎统一聚合。cwd/git_branch 引擎不动，在这里给。
		SummaryFromRows: func(root ZcodeSessionRow) SessionSummary {
			return SessionSummary{
				SourceApp:       SourceZcode,
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
				// ZCode 的转录只存在 sqlite（part.data 为明文 JSON），没有压缩
				// 形态。
				Compressed: false,
			}
		},
		// usage 已在索引扫描时按 session_id 预聚合，family 总量交给引擎默认求和。
		RowUsage:  func(row ZcodeSessionRow) *SessionTokenUsage { return row.TokenUsage },
		RowModels: func(row ZcodeSessionRow) []SessionModel { return row.Models },
		// 索引失效只看 db 文件 mtime：全部行都从它 join 出来，行内时间不参与。
		IndexFreshness: func(scanRoot string) (Freshness, error) {
			stamp, err := support.FileModifiedTimestampMillis(scanRoot)
			if err != nil {
				return Freshness{}, err
			}
			return StampFreshness(stamp), nil
		},
		// 时间线失效：max(db mtime, family updated_at)。
		FamilyFreshness: func(scanRoot string, family ZcodeSessionFamily) (int64, error) {
			dbStamp, err := support.FileModifiedTimestampMillis(scanRoot)
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
		Marker: func(memberID string, row ZcodeSessionRow) MarkerShape {
			return (ZcodeSpec{}).markerShape(memberID, row)
		},
		// overview.source_paths：db 文件打头，成员行由它 join 出来。
		FamilySourcePaths: func(scanRoot string, family ZcodeSessionFamily) []string {
			paths := []string{scanRoot}
			return append(paths, family.SourcePaths()...)
		},
		CountFamilyRecords: func(scanRoot string, family ZcodeSessionFamily) (int, int, error) {
			memberIDs := make([]string, 0, len(family.Members))
			for i := range family.Members {
				memberIDs = append(memberIDs, family.Members[i].ID)
			}
			return zcodeCountFamilyRecords(scanRoot, memberIDs)
		},
	}
}

// ZcodeReader：static 前置。
var ZcodeReader = NewFamilyReader[ZcodeSessionRow](ZcodeSpec{}, ZcodeRoot)

// ZcodeRoot：可用性以 db 文件本身为准而不是 ~/.zcode。
func ZcodeRoot() (string, error) {
	return ZcodeDBPath()
}

// ZcodeDBPath：模块级 env 版 db 路径，仅供生产路径（usage）使用；reader 引擎
// 实例的数据查询一律以实例 scan_root（即 db 文件路径）为准，env 与实例互不
// 串扰。
func ZcodeDBPath() (string, error) {
	home, ok := support.UserHomeDir()
	if !ok {
		return "", fmt.Errorf("Unable to determine home directory")
	}
	return filepath.Join(home, ".zcode", "cli", "db", "db.sqlite"), nil
}

// zcodeOpenConnectionAt：db 属主是常驻的 ZCode 进程，只读打开避免与其写入
// 争锁；WAL 模式支持并发读。
func zcodeOpenConnectionAt(scanRoot string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+scanRoot+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("Failed to open ZCode sqlite database: %w", err)
	}
	return db, nil
}

func zcodeListSessionRows(scanRoot string) ([]ZcodeSessionRow, error) {
	connection, err := zcodeOpenConnectionAt(scanRoot)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	// db 文件与数据表都随 zcode 首次写入才落地，表缺失只说明该来源还没有
	// 会话。
	exists, err := SQLiteTableExists(connection, "session")
	if err != nil {
		return nil, err
	}
	if !exists {
		return []ZcodeSessionRow{}, nil
	}

	tokenUsages, err := zcodeSessionTokenUsages(connection)
	if err != nil {
		return nil, err
	}
	modelsBySession, err := zcodeSessionModels(connection)
	if err != nil {
		return nil, err
	}
	// time_archived 是后加的列，老库没有它；缺失时列表照常产出，归档位恒假。
	archivedColumn, err := SQLiteColumnExists(connection, "session", "time_archived")
	if err != nil {
		return nil, err
	}
	archivedExpr := "NULL"
	if archivedColumn {
		archivedExpr = "time_archived"
	}

	rows, err := connection.Query(fmt.Sprintf(
		"SELECT id, parent_id, directory, title, time_created, time_updated, %s FROM session ORDER BY time_updated DESC",
		archivedExpr))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	collection := make([]ZcodeSessionRow, 0)
	for rows.Next() {
		var id string
		var parentID *string
		var directory string
		var title *string
		var timeCreated, timeUpdated int64
		var timeArchived *int64
		if err := rows.Scan(&id, &parentID, &directory, &title, &timeCreated, &timeUpdated, &timeArchived); err != nil {
			return nil, fmt.Errorf("Failed to read ZCode sessions: %w", err)
		}

		rowTitle := id
		if title != nil && *title != "" {
			rowTitle = *title
		}
		models := modelsBySession[id]
		if models == nil {
			models = []SessionModel{}
		}
		// zcode 会话只存在于 SQLite，没有 transcript 文件；用「db路径:id」
		// 组合串作为该记录的稳定 key。整体不是真实路径，path_key 会走原样
		// 字符串分支，写入与查询两侧同源，key 保持一致。
		collection = append(collection, ZcodeSessionRow{
			ID:           id,
			ParentID:     parentID,
			Directory:    directory,
			Title:        rowTitle,
			TimeCreated:  timeCreated,
			TimeUpdated:  timeUpdated,
			TimeArchived: timeArchived,
			TokenUsage:   tokenUsages[id],
			Models:       models,
			Path:         fmt.Sprintf("%s:%s", scanRoot, id),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Failed to read ZCode sessions: %w", err)
	}
	return collection, nil
}

// zcodeSessionModels：会话用过的模型按 session_id 预聚合——model_usage 是
// 调用级明细，provider_id 与 model_id 都在行上。计数只用于排序（主模型排
// 最前），同次数按模型名稳定排序。
func zcodeSessionModels(connection *sql.DB) (map[string][]SessionModel, error) {
	exists, err := SQLiteTableExists(connection, "model_usage")
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string][]SessionModel{}, nil
	}

	rows, err := connection.Query(
		"SELECT session_id, provider_id, model_id, COUNT(*) AS calls FROM model_usage GROUP BY session_id, provider_id, model_id ORDER BY session_id, calls DESC, model_id")
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare ZCode model query: %w", err)
	}
	defer rows.Close()

	models := map[string][]SessionModel{}
	for rows.Next() {
		// calls 列只服务于 ORDER BY；database/sql 的 Scan 要求目标数与列数
		// 严格一致（rusqlite 按索引取值无此约束），必须显式接住第 4 列。
		var sessionID, provider, model string
		var calls int64
		if err := rows.Scan(&sessionID, &provider, &model, &calls); err != nil {
			return nil, fmt.Errorf("Failed to read ZCode models: %w", err)
		}
		models[sessionID] = append(models[sessionID], NewSessionModel(provider, model))
	}
	return models, rows.Err()
}

// zcodeSessionTokenUsages：turn_usage 表按 turn 预聚合了四类用量列，直接
// SUM。其 input_tokens 是全口径（未命中 + 缓存读）：SQL 只产原始列，拆合与
// 钳制由 FullScopeUsage 的归一口径统一处理。
func zcodeSessionTokenUsages(connection *sql.DB) (map[string]*SessionTokenUsage, error) {
	rows, err := connection.Query(
		"SELECT session_id, COALESCE(SUM(input_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(reasoning_tokens), 0), COALESCE(SUM(cache_read_input_tokens), 0), COALESCE(SUM(cache_creation_input_tokens), 0) FROM turn_usage GROUP BY session_id")
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare ZCode token usage query: %w", err)
	}
	defer rows.Close()

	usages := map[string]*SessionTokenUsage{}
	for rows.Next() {
		var sessionID string
		var columns UsageColumns
		if err := rows.Scan(&sessionID, &columns.Input, &columns.Output, &columns.Reasoning, &columns.CacheRead, &columns.CacheWrite); err != nil {
			return nil, fmt.Errorf("Failed to read ZCode token usages: %w", err)
		}
		usage := FullScopeUsage(columns)
		usages[sessionID] = &usage
	}
	return usages, rows.Err()
}

// ZcodeUsageHours：用量曲线的小时桶——model_usage 是调用级明细，自带
// model_id 与官方 (started_at, provider_id, model_id) 索引，总量与模型切片
// 同源；它比 turn_usage（只记主 turn）多计入 subagent 调用，与 claude/pi 把
// 子代理计入的口径一致，列表 summary 仍走 turn_usage。归一口径与
// zcodeSessionTokenUsages 一致（FullScopeUsage 单点）。db 缺失、或表还没建，
// 都表示来源不可用，返回 nil。status 只取 completed，error/cancelled 的部分
// 用量不计。
func ZcodeUsageHours() (*SqlUsageHours, error) {
	dbPath, err := ZcodeDBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}
	connection, err := zcodeOpenConnectionAt(dbPath)
	if err != nil {
		return nil, nil
	}
	defer connection.Close()

	exists, err := SQLiteTableExists(connection, "model_usage")
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	rows, err := connection.Query(
		"SELECT session_id, started_at, model_id, input_tokens, output_tokens, reasoning_tokens, cache_read_input_tokens, cache_creation_input_tokens FROM model_usage WHERE status = 'completed'")
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare Zcode usage hours query: %w", err)
	}
	defer rows.Close()

	usageRows := make([]UsageRow, 0)
	for rows.Next() {
		var sessionID string
		var startedAt int64
		var model *string
		var columns UsageColumns
		if err := rows.Scan(&sessionID, &startedAt, &model, &columns.Input, &columns.Output, &columns.Reasoning, &columns.CacheRead, &columns.CacheWrite); err != nil {
			return nil, fmt.Errorf("Failed to read Zcode usage hours: %w", err)
		}
		usageRows = append(usageRows, UsageRow{
			SessionID: sessionID,
			Timestamp: startedAt,
			Model:     model,
			Usage:     FullScopeUsage(columns),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Failed to read Zcode usage hours: %w", err)
	}

	hours, err := HoursFromUsageRows(usageRows)
	if err != nil {
		return nil, fmt.Errorf("Failed to read Zcode usage hours: %w", err)
	}
	return &hours, nil
}

// zcodeCountFamilyRecords：与时间线加载同一条分类边界——semantics.kind 是
// zcode 官方的消息语义标记。
func zcodeCountFamilyRecords(scanRoot string, memberIDs []string) (int, int, error) {
	connection, err := zcodeOpenConnectionAt(scanRoot)
	if err != nil {
		return 0, 0, err
	}
	defer connection.Close()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(memberIDs)), ",")
	var messageCount, eventCount int
	if err := connection.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM message WHERE session_id IN (%s) AND json_extract(data, '$.semantics.kind') IN ('user_prompt', 'assistant_response')",
		placeholders), argsFromStrings(memberIDs)...).Scan(&messageCount); err != nil {
		return 0, 0, fmt.Errorf("Failed to count ZCode messages: %w", err)
	}
	if err := connection.QueryRow(fmt.Sprintf(
		"SELECT COUNT(*) FROM message WHERE session_id IN (%s) AND (json_extract(data, '$.semantics.kind') IS NULL OR json_extract(data, '$.semantics.kind') NOT IN ('user_prompt', 'assistant_response'))",
		placeholders), argsFromStrings(memberIDs)...).Scan(&eventCount); err != nil {
		return 0, 0, fmt.Errorf("Failed to count ZCode events: %w", err)
	}
	return messageCount, eventCount, nil
}

func zcodeLoadMessageRows(connection *sql.DB, memberIDs []string) ([]zcodeMessageRow, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(memberIDs)), ",")
	rows, err := connection.Query(fmt.Sprintf(
		"SELECT id, session_id, time_created, data FROM message WHERE session_id IN (%s) ORDER BY time_created ASC, sequence ASC, id ASC",
		placeholders), argsFromStrings(memberIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messageRows := make([]zcodeMessageRow, 0)
	for rows.Next() {
		var id, sessionID, rawData string
		var timeCreated int64
		if err := rows.Scan(&id, &sessionID, &timeCreated, &rawData); err != nil {
			return nil, err
		}
		value, err := decodeJSONBytes([]byte(rawData))
		if err != nil {
			return nil, fmt.Errorf("Invalid ZCode message JSON for %s: %w", id, err)
		}
		role := "unknown"
		if text, ok := jsonString(value, "role"); ok {
			role = text
		}
		kind := "unknown"
		if text, ok := jsonString(value, "semantics", "kind"); ok {
			kind = text
		}
		messageRows = append(messageRows, zcodeMessageRow{
			id: id, sessionID: sessionID, role: role, kind: kind, timeCreated: timeCreated, value: value,
		})
	}
	return messageRows, rows.Err()
}

// zcodeLoadModelsByMessage：消息级的模型归属——model_usage 记录每次调用的
// assistant_message_id，按它把 (provider, model) 映射回具体回答。同一消息有
// 多次尝试（重试）时取最早一次，与实际展示的那次回答对齐。
func zcodeLoadModelsByMessage(connection *sql.DB, memberIDs []string) (map[string]*SessionModel, error) {
	exists, err := SQLiteTableExists(connection, "model_usage")
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]*SessionModel{}, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(memberIDs)), ",")
	rows, err := connection.Query(fmt.Sprintf(
		"SELECT assistant_message_id, provider_id, model_id FROM model_usage WHERE session_id IN (%s) AND assistant_message_id IS NOT NULL ORDER BY started_at ASC, attempt_index ASC",
		placeholders), argsFromStrings(memberIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := map[string]*SessionModel{}
	for rows.Next() {
		var messageID, provider, model string
		if err := rows.Scan(&messageID, &provider, &model); err != nil {
			return nil, err
		}
		if _, ok := models[messageID]; !ok {
			sessionModel := NewSessionModel(provider, model)
			models[messageID] = &sessionModel
		}
	}
	return models, rows.Err()
}

// zcodeLoadPartsByMessage：parts 按 message_id 分组；sequence 是 zcode 写入时
// 的自增序，决定同一条消息内内容块的展示顺序。
func zcodeLoadPartsByMessage(connection *sql.DB, memberIDs []string) (map[string][]any, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(memberIDs)), ",")
	rows, err := connection.Query(fmt.Sprintf(
		"SELECT message_id, data FROM part WHERE session_id IN (%s) ORDER BY message_id, sequence, time_created, id",
		placeholders), argsFromStrings(memberIDs)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	partsByMessage := map[string][]any{}
	for rows.Next() {
		var messageID, rawData string
		if err := rows.Scan(&messageID, &rawData); err != nil {
			return nil, err
		}
		value, err := decodeJSONBytes([]byte(rawData))
		if err != nil {
			return nil, fmt.Errorf("Invalid ZCode part JSON for %s: %w", messageID, err)
		}
		partsByMessage[messageID] = append(partsByMessage[messageID], value)
	}
	return partsByMessage, rows.Err()
}

// zcodeMessageRowTimestamp：消息时间戳优先取 data.time.created，部分系统消息
// （如 timeline_event）的 data 可能没有 time，回退 time_created 列。
func zcodeMessageRowTimestamp(row zcodeMessageRow) *int64 {
	if created, ok := jsonI64Path(row.value, "time", "created"); ok {
		return &created
	}
	fallback := row.timeCreated
	return &fallback
}

func zcodeLoadMessageBlocks(row zcodeMessageRow, parts []any) []ContentBlock {
	if row.kind == "user_prompt" {
		return zcodeUserMessageBlocks(parts)
	}
	return zcodeAssistantMessageBlocks(parts)
}

func zcodeUserMessageBlocks(parts []any) []ContentBlock {
	blocks := make([]ContentBlock, 0)

	for _, part := range parts {
		partType, _ := jsonString(part, "type")
		switch partType {
		case "text":
			if text, ok := jsonString(part, "text"); ok {
				blocks = append(blocks, ContentBlock{Kind: "text", Text: &text})
			}
		case "file":
			// file part 只有 mime 与 artifact URL，URL 无法脱离 zcode 进程解析，
			// 展示 mime 即可。
			if mime, ok := jsonString(part, "mime"); ok {
				text := fmt.Sprintf("文件类型:%s", mime)
				blocks = append(blocks, ContentBlock{Kind: "file", Text: &text})
			}
		}
	}

	return blocks
}

func zcodeAssistantMessageBlocks(parts []any) []ContentBlock {
	blocks := make([]ContentBlock, 0)

	for _, part := range parts {
		partType, _ := jsonString(part, "type")
		switch partType {
		case "tool":
			toolBlocks := ToolBlocks(part, zcodeToolShape)
			if len(toolBlocks) == 0 {
				blocks = append(blocks, EmptyToolBlock("ZCode", part))
			} else {
				blocks = append(blocks, toolBlocks...)
			}
		case "reasoning":
			text, hasText := jsonString(part, "text")
			blocks = append(blocks, ContentBlock{
				Kind:    "thinking",
				Text:    optionalString(text, hasText),
				Payload: part,
			})
		case "text":
			text, hasText := jsonString(part, "text")
			blocks = append(blocks, ContentBlock{
				Kind:    "text",
				Text:    optionalString(text, hasText),
				Payload: part,
			})
		case "step-start", "step-finish":
			// step-start/step-finish 是每步的边界与统计标记（空对象或纯计数），
			// 量大且无展示价值，不进时间线。
		default:
			blocks = append(blocks, UnsupportedBlock("ZCode", part))
		}
	}

	return blocks
}

// zcodeToolShape：zcode tool part 的形态 {type,callID,tool,
// state:{status,input,output}}；装配体在 tool_blocks，这里只声明字段差异。
var zcodeToolShape = ToolShape{
	ToolNameKey:   "tool",
	CallIDKey:     "callID",
	OutputKey:     "output",
	ExtractOutput: TextOutput,
}

// zcodeEventFromMessageRow：非 timeline 语义的消息（todo_reminder、
// timeline_event 等）转事件；正文不在 message.data 里而在 part 表，summary
// 与 payload 都要借 part 文本补全。raw 页要看到原始记录——message 行 + part
// 表内容一起给出去（事件类型的正文在 part 表里），摘要仍按 part 文本生成
// 可读文案。
func zcodeEventFromMessageRow(row zcodeMessageRow, parts []any) SessionEvent {
	summary := SummarizeEvent(row.kind, row.value)

	switch row.kind {
	case "todo_reminder":
		texts := make([]string, 0)
		for _, part := range parts {
			if partType, _ := jsonString(part, "type"); partType != "text" {
				continue
			}
			if text, ok := jsonString(part, "text"); ok {
				texts = append(texts, text)
			}
		}
		text := strings.Join(texts, "\n")
		if text != "" {
			summary = TruncateSummary(fmt.Sprintf("todo reminder: %s", NormalizeTitle(text)))
		}
	case "timeline_event":
		for _, part := range parts {
			if partType, _ := jsonString(part, "type"); partType != "timeline" {
				continue
			}
			if timelineType, ok := jsonString(part, "timelineType"); ok {
				summary = fmt.Sprintf("timeline: %s", timelineType)
				break
			}
		}
	}

	payload := map[string]any{
		"id":           row.id,
		"session_id":   row.sessionID,
		"time_created": row.timeCreated,
		"data":         row.value,
		"parts":        parts,
	}

	return SessionEvent{
		ID:        row.id,
		Kind:      row.kind,
		Timestamp: zcodeMessageRowTimestamp(row),
		Summary:   summary,
		Payload:   RecordPayload(payload),
		SessionID: someStr(row.sessionID),
	}
}
