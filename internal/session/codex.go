package session

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"reins/internal/support"
)

// codex.go：Codex 读取器。真差异声明：
//   - 孤儿策略 KeepLastKnown（父链中断保留最后已知 id，孤儿挂靠最近的存活
//     祖先——与 opencode/zcode 认领缺失 id 的语义不同，见 family_index）；
//   - root 挑选按续跑段年龄（resume 段共享 session id，root 必须选原始段）；
//   - 目录失效自扫两个转录根的整棵树（归档与压缩只改父目录 mtime，引擎
//     默认的「jsonl 文件 mtime 最大值」看不见）；
//   - family usage/models 逐成员经 path_summary 整文件解析（索引行只读首行）；
//   - marker extras、subagent 生命周期前置事件、overview 不计数。

// 归档会话的存放目录，与活跃会话的 sessions/ 并列在 CODEX_HOME 下；上游
// 归档/取消归档都是在两者之间 rename 文件（thread-store/local/
// archive_thread.rs），所以文件所在目录就是归档状态的权威。
const codexArchivedSessionsSubdir = "archived_sessions"

// CodexSessionRow：索引行。json tag 供目录行持久缓存（rows_cache）序列化。
type CodexSessionRow struct {
	Path            string         `json:"path"`
	Summary         SessionSummary `json:"summary"`
	ParentSessionID *string        `json:"parent_session_id"`
	ForkedFromID    *string        `json:"forked_from_id"`
	AgentNickname   *string        `json:"agent_nickname"`
	AgentRole       *string        `json:"agent_role"`
	// resume 产生的续跑段：与原始段共享 session id，靠 session_meta 里的
	// history_base 区分。family root 必须选原始段，否则标题取自续跑段的
	// 首条消息（通常是"继续"），且选哪一段取决于文件枚举顺序、不确定。
	IsResumeSegment bool `json:"is_resume_segment"`
}

// CodexSessionFamily：codex 族。
type CodexSessionFamily = Family[CodexSessionRow]

// FamilyRow 契约。
func (r CodexSessionRow) MemberPath() string       { return r.Path }
func (r CodexSessionRow) FamilyRootID() string     { return r.Summary.SourceSessionID }
func (r CodexSessionRow) MemberID() string         { return r.Summary.SourceSessionID }
func (r CodexSessionRow) MemberCreatedAt() *int64  { return r.Summary.CreatedAt }
func (r CodexSessionRow) MemberUpdatedAt() *int64  { return r.Summary.UpdatedAt }
func (r CodexSessionRow) MemberTieBreaker() string { return r.Summary.SourceSessionID }

// ParentChainRow 契约。
func (r CodexSessionRow) ChainID() string { return r.Summary.SourceSessionID }
func (r CodexSessionRow) ChainParentID() (string, bool) {
	if r.ParentSessionID == nil {
		return "", false
	}
	return *r.ParentSessionID, true
}

// 编译期钉住：行契约与引擎 Reader 契约。
var (
	_ FamilyRow      = CodexSessionRow{}
	_ ParentChainRow = CodexSessionRow{}
	_ Reader         = (*FamilyReader[CodexSessionRow])(nil)
)

// CodexSpec：codex 的真差异声明。
type CodexSpec struct{}

func (CodexSpec) App() SourceApp                 { return SourceCodex }
func (CodexSpec) Label() string                  { return "Codex" }
func (CodexSpec) SummaryKind() SummaryKind       { return SummaryRootTranscript }
func (CodexSpec) OverviewCounts() OverviewCounts { return OverviewOmitted }

// ScanRoot：root 下的 sessions 子目录。
func (CodexSpec) ScanRoot(root string) string { return joinPath(root, "sessions") }

func (CodexSpec) ListRows(scanRoot string, _ *RowsDb) ([]CodexSessionRow, error) {
	rows := make([]CodexSessionRow, 0)
	for _, root := range codexTranscriptRoots(scanRoot) {
		scanned, err := ScanFiles(rolloutFilesIn(root), parseSessionIndexRow, RowAbort, "Codex")
		if err != nil {
			return nil, err
		}
		rows = append(rows, scanned...)
	}
	return rows, nil
}

func (CodexSpec) GroupFamilies(rows []CodexSessionRow) ([]CodexSessionFamily, error) {
	// 孤儿策略：父链中断时保留最后已知 id，孤儿挂靠最近的存活祖先。
	return GroupFamiliesByParentChain(rows, OrphanKeepLastKnown, segmentAgeRoot), nil
}

func (CodexSpec) LoadMembers(_ string, members []CodexSessionRow) ([]MemberTimeline, error) {
	timelines := make([]MemberTimeline, 0, len(members))
	for i := range members {
		messages := make([]SessionMessage, 0)
		events := make([]SessionEvent, 0)
		// provider 由 session_meta 声明（文件首行），模型由 turn_context 按轮
		// 声明：该轮之后的用户输入与助手输出都归它，直到下一次声明。
		provider := ""
		var currentModel *SessionModel

		reader, err := OpenLines(members[i].Path)
		if err != nil {
			return nil, err
		}
		loadErr := forEachLine(reader, func(index int, line string) error {
			value, err := parseJSONLine(line)
			if err != nil {
				return err
			}
			if kind, _ := jsonType(value); kind == "session_meta" {
				if text, ok := jsonString(value, "payload", "model_provider"); ok {
					provider = text
				}
			} else if kind == "turn_context" {
				if model, ok := jsonString(value, "payload", "model"); ok {
					sessionModel := NewSessionModel(provider, model)
					currentModel = &sessionModel
				}
			}
			if record := parseCodexTimelineRecord(index, value); record != nil {
				if record.Message != nil {
					message := *record.Message
					message.Model = cloneSessionModel(currentModel)
					messages = append(messages, message)
				} else {
					events = append(events, *record.Event)
				}
			}
			return nil
		})
		closeErr := reader.Close()
		if loadErr != nil {
			return nil, loadErr
		}
		if closeErr != nil {
			return nil, closeErr
		}

		timelines = append(timelines, MemberTimeline{Messages: messages, Events: events})
	}
	return timelines, nil
}

func (CodexSpec) AgentName(row CodexSessionRow) string {
	return codexFamilyMemberDisplayName(row)
}

func (CodexSpec) AgentLabel(family CodexSessionFamily, row CodexSessionRow) FamilyAgentLabel {
	if row.Summary.SourceSessionID == family.Root.Summary.SourceSessionID {
		return FamilyAgentLabel{Kind: AgentLabelRoot}
	}
	if row.ForkedFromID != nil {
		return FamilyAgentLabel{Kind: AgentLabelDerived, Name: codexFamilyMemberDisplayName(row)}
	}
	return FamilyAgentLabel{Kind: AgentLabelChild, Name: codexFamilyMemberDisplayName(row)}
}

func (CodexSpec) Hooks() *FamilyHooks[CodexSessionRow] {
	return &FamilyHooks[CodexSessionRow]{
		ParseFullSummary: parseFullCodexSummary,
		// 归档与压缩都改文件路径：归档把 rollout 在 sessions/ 与
		// archived_sessions/ 之间移动，压缩是原地改名。两者只改父目录 mtime，
		// 引擎默认的「jsonl 文件 mtime 最大值」看不见，所以 codex 自己走一遍
		// 两个根的整棵树。
		IndexFreshness: func(scanRoot string) (Freshness, error) {
			latest := int64(0)
			for _, root := range codexTranscriptRoots(scanRoot) {
				info, err := os.Stat(root)
				if err != nil || !info.IsDir() {
					continue
				}
				stamp, err := support.FileModifiedTimestampMillis(root)
				if err != nil {
					return Freshness{}, err
				}
				if stamp > latest {
					latest = stamp
				}
				// 遍历期间文件可能正好被压缩或归档搬走，取不到 metadata 的
				// 条目跳过即可——搬运本身就会让父目录 mtime 变化，从而触发
				// 下一次重建。
				_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if stamp, err := support.FileModifiedTimestampMillis(path); err == nil && stamp > latest {
						latest = stamp
					}
					return nil
				})
			}
			return StampFreshness(latest), nil
		},
		// 索引行只读到首行 session_meta 即停，成员 usage 只能经路径级缓存取
		// 整文件解析结果；resume 段与子代理线程的消耗并入 family 统计。
		FamilyUsage: func(engine *ReaderEngine[CodexSessionRow], family CodexSessionFamily) (*SessionTokenUsage, error) {
			var total *SessionTokenUsage
			for i := range family.Members {
				summary, err := engine.PathSummary(family.Members[i].Path)
				if err != nil {
					return nil, err
				}
				if summary.TokenUsage != nil {
					MergeTokenUsage(&total, *summary.TokenUsage)
				}
			}
			return total, nil
		},
		// 与 family_usage 同因：索引行只读首行，拿不到 turn_context 里的模型，
		// 只能逐成员走 path_summary（命中摘要缓存）。子代理线程常换模型，合并
		// 口径与 token 求和一致。
		FamilyModels: func(engine *ReaderEngine[CodexSessionRow], family CodexSessionFamily) ([]SessionModel, error) {
			tally := ModelTally{}
			for i := range family.Members {
				summary, err := engine.PathSummary(family.Members[i].Path)
				if err != nil {
					return nil, err
				}
				tally.Absorb(summary.Models)
			}
			return tally.IntoModels(), nil
		},
		Marker: func(memberID string, row CodexSessionRow) MarkerShape {
			extras := map[string]any{
				"agent_nickname":    row.AgentNickname,
				"agent_role":        row.AgentRole,
				"parent_session_id": row.ParentSessionID,
				"transcript_path":   row.Path,
			}
			return LabeledMarker("Codex", memberID).WithExtras(extras, extras)
		},
		RootExtraEvents: func(family CodexSessionFamily) ([]SessionEvent, error) {
			return codexSubagentLifecycleEvents(family.Root.Path, family.Members), nil
		},
	}
}

// CodexRoot：env 解析的 codex 根（CODEX_HOME 重定向优先、HOME 兜底）。
func CodexRoot() (string, error) {
	return SpecRoot(SourceCodex)
}

// CodexReader：static 前置（env 根 → 引擎实例）。
var CodexReader = NewFamilyReader[CodexSessionRow](CodexSpec{}, CodexRoot)

// codexTranscriptRoots：codex 的两个转录根——活跃的 sessions/ 与并列的
// archived_sessions/。归档会话仍属于用户的会话历史，列表与用量都按同一份
// 发现规则收进来。
func codexTranscriptRoots(scanRoot string) [2]string {
	return [2]string{scanRoot, filepath.Join(filepath.Dir(scanRoot), codexArchivedSessionsSubdir)}
}

// CodexTranscriptPaths：usage 采集用的转录发现（引擎的列表扫描走 list_rows
// 的同名枚举）。
func CodexTranscriptPaths(scanRoot string) ([]string, error) {
	files := make([]string, 0)
	for _, root := range codexTranscriptRoots(scanRoot) {
		files = append(files, rolloutFilesIn(root)...)
	}
	return files, nil
}

// rolloutFilesIn：单个根下的 rollout 文件——明文与压缩形态都算转录。同目录
// 同时存在明文与压缩兄弟文件时只认明文——上游读取侧就是明文优先
// （rollout/src/compression.rs should_skip_compressed_sibling），否则同一
// 会话会出两条行。
func rolloutFilesIn(root string) []string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}

	plain := make([]string, 0)
	compressed := make([]string, 0)

	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		name := filepath.Base(path)
		if strings.HasSuffix(name, ".jsonl") {
			plain = append(plain, path)
		} else if strings.HasSuffix(name, CodexCompressedSuffix) {
			compressed = append(compressed, path)
		}
		return nil
	})

	plainSet := make(map[string]bool, len(plain))
	for _, path := range plain {
		plainSet[path] = true
	}
	for _, path := range compressed {
		name := filepath.Base(path)
		stem, found := strings.CutSuffix(name, CodexCompressedSuffix)
		if !found {
			continue
		}
		sibling := filepath.Join(filepath.Dir(path), stem+".jsonl")
		if !plainSet[sibling] {
			plain = append(plain, path)
		}
	}

	return plain
}

// codexIsArchivedPath：归档会话由文件所在目录标识。
func codexIsArchivedPath(path string) bool {
	return filepath.Base(filepath.Dir(path)) == codexArchivedSessionsSubdir
}

// parseSessionIndexRow：索引行扫描——只读首行 session_meta。
func parseSessionIndexRow(path string) (CodexSessionRow, error) {
	var summary SummaryAccumulator
	var parentSessionID, forkedFromID, agentNickname, agentRole *string
	isResumeSegment := false

	reader, err := OpenLines(path)
	if err != nil {
		return CodexSessionRow{}, err
	}
	scanErr := forEachLine(reader, func(_ int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return err
		}
		if kind, _ := jsonType(value); kind != "session_meta" {
			return nil
		}

		if summary.SessionID == nil {
			summary.SessionID = strField(jsonString(value, "payload", "id"))
		}
		if summary.Cwd == nil {
			summary.Cwd = strField(jsonString(value, "payload", "cwd"))
		}
		if summary.CreatedAt == nil {
			if timestamp, ok := jsonString(value, "payload", "timestamp"); ok {
				if millis, ok := support.ParseTimestamp(timestamp); ok {
					summary.CreatedAt = &millis
				}
			}
		}
		if summary.GitBranch == nil {
			if branch, ok := jsonString(value, "payload", "git", "branch"); ok {
				summary.GitBranch = &branch
			} else if branch, ok := jsonString(value, "payload", "git_branch"); ok {
				summary.GitBranch = &branch
			} else if branch, ok := jsonString(value, "payload", "gitBranch"); ok {
				summary.GitBranch = &branch
			}
		}
		if forkedFromID == nil {
			forkedFromID = strField(jsonString(value, "payload", "forked_from_id"))
		}
		// history_base 存在（非 null）即续跑段。
		if raw, _ := getJSONObjectPath(mustPayload(value), "history_base"); raw != nil {
			isResumeSegment = true
		}
		if parentSessionID == nil {
			parentSessionID = strField(jsonString(value, "payload", "source", "subagent", "thread_spawn", "parent_thread_id"))
		}
		if agentNickname == nil {
			if nickname, ok := jsonString(value, "payload", "agent_nickname"); ok {
				agentNickname = &nickname
			} else if nickname, ok := jsonString(value, "payload", "source", "subagent", "thread_spawn", "agent_nickname"); ok {
				agentNickname = &nickname
			}
		}
		if agentRole == nil {
			if role, ok := jsonString(value, "payload", "agent_role"); ok {
				agentRole = &role
			} else if role, ok := jsonString(value, "payload", "source", "subagent", "thread_spawn", "agent_role"); ok {
				agentRole = &role
			}
		}
		return errStopIteration
	})
	closeErr := reader.Close()
	if scanErr != nil && scanErr != errStopIteration {
		return CodexSessionRow{}, scanErr
	}
	if closeErr != nil {
		return CodexSessionRow{}, closeErr
	}

	// 状态位取自扫描到的物理文件：压缩是命名形态，归档是所在目录。读取路径
	// 统一经 Resolve 归一，压缩/解压切换后旧路径仍能读。
	summary.Archived = codexIsArchivedPath(path)
	summary.Compressed = IsCompressed(path)

	built, err := BuildSummary(SourceCodex, path, summary)
	if err != nil {
		return CodexSessionRow{}, err
	}
	mtime, err := support.FileModifiedTimestampMillis(Resolve(path))
	if err != nil {
		return CodexSessionRow{}, err
	}
	built.UpdatedAt = &mtime

	return CodexSessionRow{
		Path:            path,
		Summary:         built,
		ParentSessionID: parentSessionID,
		ForkedFromID:    forkedFromID,
		AgentNickname:   agentNickname,
		AgentRole:       agentRole,
		IsResumeSegment: isResumeSegment,
	}, nil
}

// errStopIteration：索引行扫描只认首行 session_meta，读到后提前收工。
var errStopIteration = fmt.Errorf("stop iteration")

// mustPayload：value["payload"]——Rust 索引语法在缺失时等价于 Null；Go 用
// getJSONObjectPath 的 (值, 存在) 直接承载。
func mustPayload(value any) any {
	payload, _ := getJSONObjectPath(value, "payload")
	return payload
}

// strField：jsonString 的 (值, 存在) → 指针包装。
func strField(text string, ok bool) *string { return optionalString(text, ok) }

// someStr：无条件 Some 包装。
func someStr(text string) *string { return &text }

// cloneSessionModel：消息级模型指针逐消息复制（共享指针会让下游对单条消息
// 的局部改动波及其余消息）。
func cloneSessionModel(model *SessionModel) *SessionModel {
	if model == nil {
		return nil
	}
	copied := *model
	return &copied
}

// parseFullCodexSummary：root 转录整文件解析（三级摘要缓存的 miss loader）。
func parseFullCodexSummary(path string) (SessionSummary, error) {
	summary := SummaryAccumulator{
		Archived:   codexIsArchivedPath(path),
		Compressed: IsCompressed(path),
	}
	provider := ""

	reader, err := OpenLines(path)
	if err != nil {
		return SessionSummary{}, err
	}
	scanErr := forEachLine(reader, func(_ int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return err
		}
		UpdateSummaryTimestamp(&summary, value)

		switch kind, _ := jsonType(value); kind {
		case "session_meta":
			provider, _ = jsonString(value, "payload", "model_provider")
			if summary.SessionID == nil {
				summary.SessionID = strField(jsonString(value, "payload", "id"))
			}
			if summary.Cwd == nil {
				summary.Cwd = strField(jsonString(value, "payload", "cwd"))
			}
			if summary.CreatedAt == nil {
				if timestamp, ok := jsonString(value, "payload", "timestamp"); ok {
					if millis, ok := support.ParseTimestamp(timestamp); ok {
						summary.CreatedAt = &millis
					}
				}
			}
			if summary.GitBranch == nil {
				if branch, ok := jsonString(value, "payload", "git", "branch"); ok {
					summary.GitBranch = &branch
				} else if branch, ok := jsonString(value, "payload", "git_branch"); ok {
					summary.GitBranch = &branch
				} else if branch, ok := jsonString(value, "payload", "gitBranch"); ok {
					summary.GitBranch = &branch
				}
			}
		case "response_item":
			if payloadType, _ := jsonString(value, "payload", "type"); payloadType == "message" {
				role, _ := jsonString(value, "payload", "role")
				content, hasContent := getJSONObjectPath(mustPayload(value), "content")
				if role == "user" && summary.Title == nil {
					summary.Title = codexExtractTitle(content, hasContent)
				}
			}
		case "turn_context":
			if summary.Cwd == nil {
				summary.Cwd = strField(jsonString(value, "payload", "cwd"))
			}
			// 每轮一次声明，按出现次数计；同一次会话里换模型能同时体现。
			if model, ok := jsonString(value, "payload", "model"); ok {
				summary.Models.Record(provider, model)
			}
		case "event_msg":
			if payloadType, _ := jsonString(value, "payload", "type"); payloadType == "token_count" {
				// total_token_usage 是本文件开跑以来的累计值而非增量，只认
				// 最后一条，按增量求和会把同一消耗重复计入。
				info, hasInfo := getJSONObjectPath(mustPayload(value), "info")
				if hasInfo {
					if total, hasTotal := getJSONObjectPath(info, "total_token_usage"); hasTotal {
						if usage := CodexCumulativeUsage(total); usage != nil {
							summary.TokenUsage = usage
						}
					}
				}
			}
		}
		return nil
	})
	closeErr := reader.Close()
	if scanErr != nil {
		return SessionSummary{}, scanErr
	}
	if closeErr != nil {
		return SessionSummary{}, closeErr
	}

	return BuildSummary(SourceCodex, path, summary)
}

// CodexCumulativeUsage：Codex 的 input_tokens 含缓存命中部分，扣除后与其他
// 来源的「新输入」口径对齐；output_tokens 本身已含 reasoning。
func CodexCumulativeUsage(usage any) *SessionTokenUsage {
	input, ok := jsonU64(usage, "input_tokens")
	if !ok {
		return nil
	}
	cached, _ := jsonU64(usage, "cached_input_tokens")
	output, _ := jsonU64(usage, "output_tokens")
	cacheWrite, _ := jsonU64(usage, "cache_write_input_tokens")

	return &SessionTokenUsage{
		InputTokens:      input - min64(input, cached),
		OutputTokens:     output,
		CacheReadTokens:  cached,
		CacheWriteTokens: cacheWrite,
	}
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// CodexUsageHours：用量曲线的小时桶——token_count 的 total_token_usage 是本
// 文件开跑以来的累计值，相邻事件差分得到增量，归到事件时间戳所在小时。首
// 事件视作从 0 起步；累计回退（compact 等重置）时差分钳 0，宁可少计也不重
// 复计。
func CodexUsageHours(path string) (UsageHourBuckets, error) {
	buckets := UsageHourBuckets{}
	previous := SessionTokenUsage{}
	// turn_context 声明该 turn 使用的模型；token_count 事件归属最近一次声明
	// 的模型，文件开头未声明时只进总量。
	currentModel := ""

	reader, err := OpenLines(path)
	if err != nil {
		return nil, err
	}
	scanErr := forEachLine(reader, func(_ int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return err
		}
		kind, _ := jsonType(value)
		if kind == "turn_context" {
			if model, ok := jsonString(value, "payload", "model"); ok {
				currentModel = model
			}
			return nil
		}
		if kind != "event_msg" {
			return nil
		}
		if payloadType, _ := jsonString(value, "payload", "type"); payloadType != "token_count" {
			return nil
		}

		info, hasInfo := getJSONObjectPath(mustPayload(value), "info")
		if !hasInfo {
			return nil
		}
		total, hasTotal := getJSONObjectPath(info, "total_token_usage")
		if !hasTotal {
			return nil
		}
		current := CodexCumulativeUsage(total)
		if current == nil {
			return nil
		}

		var timestamp *int64
		if raw, ok := jsonString(value, "timestamp"); ok {
			if millis, ok := support.ParseTimestamp(raw); ok {
				timestamp = &millis
			}
		}

		MergeUsageEvent(buckets, hourKeyOpt(timestamp), currentModel, SessionTokenUsage{
			InputTokens:      current.InputTokens - min64(current.InputTokens, previous.InputTokens),
			OutputTokens:     current.OutputTokens - min64(current.OutputTokens, previous.OutputTokens),
			CacheReadTokens:  current.CacheReadTokens - min64(current.CacheReadTokens, previous.CacheReadTokens),
			CacheWriteTokens: current.CacheWriteTokens - min64(current.CacheWriteTokens, previous.CacheWriteTokens),
		})
		previous = *current
		return nil
	})
	closeErr := reader.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return buckets, nil
}

func hourKeyOpt(timestamp *int64) *string {
	if timestamp == nil {
		return nil
	}
	return HourKey(*timestamp)
}

// segmentAgeRoot：codex 的 root 挑选——同一 session id 可能有多个 rollout
// 文件（resume 续跑段），members 此时还是文件枚举顺序，不能直接 find/first：
// root 选取必须是输入顺序无关的，否则标题/路径随枚举序漂移。优先选原始段
// （无 history_base），其余按 created_at、路径取最小保证确定。
func segmentAgeRoot(rootID string, members []CodexSessionRow) (CodexSessionRow, bool) {
	key := func(row CodexSessionRow) segmentOrderKey {
		createdAt := int64(^uint64(0) >> 1) // i64::MAX
		if row.Summary.CreatedAt != nil {
			createdAt = *row.Summary.CreatedAt
		}
		return segmentOrderKey{resume: row.IsResumeSegment, createdAt: createdAt, path: row.Path}
	}

	bySegmentAge := func(rows []CodexSessionRow) (CodexSessionRow, bool) {
		if len(rows) == 0 {
			return CodexSessionRow{}, false
		}
		best := rows[0]
		bestKey := key(best)
		for _, row := range rows[1:] {
			rowKey := key(row)
			if segmentOrderLess(rowKey, bestKey) {
				best = row
				bestKey = rowKey
			}
		}
		return best, true
	}

	matching := make([]CodexSessionRow, 0, len(members))
	for i := range members {
		if members[i].Summary.SourceSessionID == rootID {
			matching = append(matching, members[i])
		}
	}
	if row, ok := bySegmentAge(matching); ok {
		return row, true
	}
	return bySegmentAge(members)
}

// segmentOrderKey：min_by_key 的元组键（bool false < true）。
type segmentOrderKey struct {
	resume    bool
	createdAt int64
	path      string
}

func segmentOrderLess(a, b segmentOrderKey) bool {
	if a.resume != b.resume {
		return !a.resume
	}
	if a.createdAt != b.createdAt {
		return a.createdAt < b.createdAt
	}
	return a.path < b.path
}

func codexFamilyMemberDisplayName(row CodexSessionRow) string {
	if row.AgentNickname != nil {
		return *row.AgentNickname
	}
	if row.AgentRole != nil {
		return *row.AgentRole
	}
	return row.Summary.Title
}

// codexSubagentLifecycleEvents：root 转录里的子代理生命周期事件（spawn/close
// 与 <subagent_notification>）。
func codexSubagentLifecycleEvents(rootPath string, members []CodexSessionRow) []SessionEvent {
	byID := make(map[string]*CodexSessionRow, len(members))
	for i := range members {
		byID[members[i].Summary.SourceSessionID] = &members[i]
	}
	events := make([]SessionEvent, 0)

	reader, err := OpenLines(rootPath)
	if err != nil {
		return events
	}
	defer reader.Close()

	_ = forEachLine(reader, func(index int, line string) error {
		value, err := parseJSONLine(line)
		if err != nil {
			return nil // 单行损坏跳过，不让一个坏行隐藏其余事件。
		}
		var timestamp *int64
		if raw, ok := jsonString(value, "timestamp"); ok {
			if millis, ok := support.ParseTimestamp(raw); ok {
				timestamp = &millis
			}
		}

		switch kind, _ := jsonType(value); kind {
		case "event_msg":
			eventType, _ := jsonString(value, "payload", "type")

			if eventType == "collab_agent_spawn_end" {
				if childID, ok := jsonString(value, "payload", "new_thread_id"); ok {
					if row, ok := byID[childID]; ok {
						events = append(events, SessionEvent{
							ID:        fmt.Sprintf("codex-subagent-spawn-%d", index),
							Kind:      "subagent_spawned",
							Timestamp: timestamp,
							Summary:   fmt.Sprintf("Sub-agent spawned: %s", codexFamilyMemberDisplayName(*row)),
							Payload:   RecordPayload(value),
							SessionID: someStr(row.Summary.SourceSessionID),
						})
					}
				}
			}

			if eventType == "collab_close_end" {
				if childID, ok := jsonString(value, "payload", "receiver_thread_id"); ok {
					if row, ok := byID[childID]; ok {
						events = append(events, SessionEvent{
							ID:        fmt.Sprintf("codex-subagent-close-%d", index),
							Kind:      "subagent_closed",
							Timestamp: timestamp,
							Summary:   fmt.Sprintf("Sub-agent closed: %s", codexFamilyMemberDisplayName(*row)),
							Payload:   RecordPayload(value),
							SessionID: someStr(row.Summary.SourceSessionID),
						})
					}
				}
			}
		case "response_item":
			if payloadType, _ := jsonString(value, "payload", "type"); payloadType != "message" {
				return nil
			}
			if role, _ := jsonString(value, "payload", "role"); role != "user" {
				return nil
			}
			content, ok := getJSONObjectPath(mustPayload(value), "content")
			if !ok {
				return nil
			}
			items, ok := content.([]any)
			if !ok {
				return nil
			}

			for _, block := range items {
				text, ok := jsonString(block, "text")
				if !ok {
					continue
				}
				trimmed := strings.TrimSpace(text)
				if !strings.HasPrefix(trimmed, "<subagent_notification>") {
					continue
				}

				jsonStart := strings.IndexByte(trimmed, '{')
				if jsonStart < 0 {
					continue
				}
				jsonEnd := strings.LastIndexByte(trimmed, '}')
				if jsonEnd < 0 || jsonEnd < jsonStart {
					continue
				}

				notification, err := decodeJSONBytes([]byte(trimmed[jsonStart : jsonEnd+1]))
				if err != nil {
					continue
				}
				childID, ok := jsonString(notification, "agent_path")
				if !ok {
					continue
				}
				row, ok := byID[childID]
				if !ok {
					continue
				}

				events = append(events, SessionEvent{
					ID:        fmt.Sprintf("codex-subagent-notification-%d", index),
					Kind:      "subagent_notification",
					Timestamp: timestamp,
					Summary:   fmt.Sprintf("Sub-agent notification: %s", codexFamilyMemberDisplayName(*row)),
					Payload:   RecordPayload(value),
					SessionID: someStr(row.Summary.SourceSessionID),
				})
			}
		}
		return nil
	})

	return events
}

// parseCodexTimelineRecord：raw 页展示的是原始记录——事件的 payload 一律给
// 整条记录，摘要仍按内层字段生成。
func parseCodexTimelineRecord(index int, value any) *TimelineRecord {
	var timestamp *int64
	if raw, ok := jsonString(value, "timestamp"); ok {
		if millis, ok := support.ParseTimestamp(raw); ok {
			timestamp = &millis
		}
	}

	switch kind, _ := jsonType(value); kind {
	case "response_item":
		payloadType, _ := jsonString(value, "payload", "type")

		if payloadType == "message" {
			role, _ := jsonString(value, "payload", "role")
			if role == "" {
				role = "unknown"
			}
			content, hasContent := getJSONObjectPath(mustPayload(value), "content")
			blocks := parseCodexMessageBlocks(content, hasContent, role)

			if role == "user" {
				blocks = SanitizeUserBlocks(blocks)
			}

			if role == "developer" {
				return &TimelineRecord{Event: &SessionEvent{
					ID:        fmt.Sprintf("codex-event-%d", index),
					Kind:      "developer_message",
					Timestamp: timestamp,
					Summary:   SummarizeEvent("developer_message", mustPayload(value)),
					Payload:   RecordPayload(value),
					SessionID: strField(jsonString(value, "session_id")),
				}}
			}

			if len(blocks) == 0 {
				// 整条消息都是宿主注入的上下文（AGENTS.md 指令、
				// environment_context）时保留原文：这类消息没有对话内容，但
				// 内容本身要能看；其余空消息仍退化成一行原始报文诊断块。
				payload := mustPayload(value)
				if injected := InjectedContextBlock(content, payload); injected != nil {
					blocks = append(blocks, *injected)
				} else {
					blocks = append(blocks, EmptyMessageBlock("Codex", "content was empty after sanitization", payload))
				}
			}

			return &TimelineRecord{Message: &SessionMessage{
				ID:        fmt.Sprintf("codex-message-%d", index),
				Role:      role,
				Timestamp: timestamp,
				Blocks:    blocks,
				SessionID: strField(jsonString(value, "session_id")),
			}}
		} else if payloadType == "function_call" || payloadType == "function_call_output" {
			text, hasText := ToolText(mustPayload(value))
			var textPtr *string
			if hasText {
				textPtr = &text
			}
			callID := ""
			hasCallID := false
			if id, ok := jsonString(value, "payload", "call_id"); ok {
				callID, hasCallID = id, true
			} else if id, ok := jsonString(value, "payload", "callId"); ok {
				callID, hasCallID = id, true
			}
			return &TimelineRecord{Message: &SessionMessage{
				ID:        fmt.Sprintf("codex-tool-%d", index),
				Role:      "tool",
				Timestamp: timestamp,
				Blocks: []ContentBlock{{
					Kind:       payloadType,
					Text:       textPtr,
					ToolName:   strField(jsonString(value, "payload", "name")),
					ToolCallID: optionalString(callID, hasCallID),
					Payload:    mustPayload(value),
				}},
				SessionID: strField(jsonString(value, "session_id")),
			}}
		} else {
			return &TimelineRecord{Event: &SessionEvent{
				ID:        fmt.Sprintf("codex-event-%d", index),
				Kind:      payloadType,
				Timestamp: timestamp,
				Summary:   SummarizeEvent(payloadType, mustPayload(value)),
				Payload:   RecordPayload(value),
				SessionID: strField(jsonString(value, "session_id")),
			}}
		}
	case "event_msg":
		eventKind, _ := jsonString(value, "payload", "type")
		if eventKind == "" {
			eventKind = "event_msg"
		}
		return &TimelineRecord{Event: &SessionEvent{
			ID:        fmt.Sprintf("codex-event-%d", index),
			Kind:      eventKind,
			Timestamp: timestamp,
			Summary:   SummarizeEvent(eventKind, mustPayload(value)),
			Payload:   RecordPayload(value),
			SessionID: strField(jsonString(value, "session_id")),
		}}
	case "turn_context", "session_meta", "task_complete", "turn_aborted", "task_started":
		return &TimelineRecord{Event: &SessionEvent{
			ID:        fmt.Sprintf("codex-event-%d", index),
			Kind:      kind,
			Timestamp: timestamp,
			Summary:   SummarizeEvent(kind, value),
			Payload:   RecordPayload(value),
			SessionID: strField(jsonString(value, "session_id")),
		}}
	default:
		return &TimelineRecord{Event: &SessionEvent{
			ID:        fmt.Sprintf("codex-event-%d", index),
			Kind:      kind,
			Timestamp: timestamp,
			Summary:   SummarizeEvent("unknown", value),
			Payload:   RecordPayload(value),
			SessionID: strField(jsonString(value, "session_id")),
		}}
	}
}

// parseCodexMessageBlocks：message.content 的块解析（filter_map 语义：用户
// 传输噪音块整体跳过）。
func parseCodexMessageBlocks(content any, contentPresent bool, role string) []ContentBlock {
	if contentPresent {
		if items, ok := content.([]any); ok {
			blocks := make([]ContentBlock, 0, len(items))
			for _, item := range items {
				kind, _ := jsonString(item, "type")
				if kind == "" {
					kind = "unknown"
				}
				text := ""
				hasText := false
				if value, ok := jsonString(item, "text"); ok {
					text, hasText = value, true
				} else if value, ok := jsonString(item, "content"); ok {
					text, hasText = value, true
				} else if value, ok := jsonString(item, "thinking"); ok {
					text, hasText = value, true
				}

				if role == "user" && hasText && IsTransportMessage(text) {
					continue
				}

				// 粘贴进来的图片（input_image / image）没有文本字段：把引用
				// 提成正文，否则会被当成未支持块，只能看 JSON。
				if kind == "input_image" || kind == "image" {
					if reference, ok := ImageReference(item); ok {
						blocks = append(blocks, ContentBlock{
							Kind:    "image",
							Text:    &reference,
							Payload: item,
						})
						continue
					}
				}

				if !hasText && kind != "tool_use" && kind != "tool_result" {
					blocks = append(blocks, UnsupportedBlock("Codex", item))
					continue
				}

				callID := ""
				hasCallID := false
				if id, ok := jsonString(item, "id"); ok {
					callID, hasCallID = id, true
				} else if id, ok := jsonString(item, "tool_use_id"); ok {
					callID, hasCallID = id, true
				}
				blocks = append(blocks, ContentBlock{
					Kind:       kind,
					Text:       optionalString(text, hasText),
					ToolName:   strField(jsonString(item, "name")),
					ToolCallID: optionalString(callID, hasCallID),
					Payload:    item,
				})
			}
			return blocks
		}
	}
	// Some(非数组) 或 None：None 的 payload 是 null（Rust Option 序列化）。
	return []ContentBlock{UnsupportedContentBlock("Codex", content)}
}

// codexExtractTitle：第一个可当标题的用户文本块。
func codexExtractTitle(content any, contentPresent bool) *string {
	for _, block := range parseCodexMessageBlocks(content, contentPresent, "user") {
		if block.Kind == "unsupported_content" || block.Kind == "unsupported_block" {
			continue
		}
		if block.Text == nil {
			continue
		}
		if candidate, ok := TitleCandidateFromText(*block.Text); ok {
			return &candidate
		}
	}
	return nil
}

// forEachLine：按行遍历转录，对齐 Rust BufRead::lines——剥掉行尾 \n（与紧
// 邻的 \r），末行允许无换行；visit 返回错误即中断。
func forEachLine(reader *LinesReader, visit func(index int, line string) error) error {
	for index := 0; ; index++ {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if line != "" {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if err := visit(index, line); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

// decodeJSONBytes：单段 JSON 文本 → any（UseNumber，与 parseJSONLine 同口径；
// serde_json::from_str 拒绝尾随内容，补一次 More 检查）。
func decodeJSONBytes(data []byte) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return value, nil
}
