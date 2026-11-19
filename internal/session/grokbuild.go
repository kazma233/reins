package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reins/internal/support"
)

// grokbuild.go：Grok Build 读取器（bespoke：不走 ReaderEngine）。真差异：
//   - 会话目录是 bucket 三层布局下的 summary.json，家族结构由 summary 集合
//     与 subagents/*/meta.json 决定；指纹覆盖 summary/history/meta 三类文件
//     的 (路径, mtime, 大小)，外部进程写入后指纹失配即重建，reins 自己删除
//     经 ClearCache 显式清；
//   - 归属只认 meta（subagents/*/meta.json 声明），父缺失的子会话保留占位
//     入口；父自身也是子代理（嵌套）的声明不折叠，保留独立条目；
//   - 列表只读 summary/usage（不扫转录正文）；主时间线只暴露子代理入口
//     （marker），子会话正文由 ParseAgentMessages 在打开入口时单独读取；
//   - overview 计数恒缺省（不加载正文）。

// GrokBuildReader：按根缓存索引实例——换根（测试切 HOME）即整实例重建，
// 与 pi 的 reader slot 同形。
type grokBuildReader struct {
	mu        sync.Mutex
	slotRoot  string
	slotIndex *grokIndex
}

// GrokBuildRoot：env 解析的 grok 根（GROK_HOME 重定向优先）+ sessions 子目录。
func GrokBuildRoot() (string, error) {
	root, err := SpecRoot(SourceGrokBuild)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sessions"), nil
}

// GrokBuildReader 全局实例（注册表与 session 命令共用）。
var GrokBuildReader = &grokBuildReader{}

var _ Reader = (*grokBuildReader)(nil)

func (b *grokBuildReader) index() (*grokIndex, error) {
	root, err := GrokBuildRoot()
	if err != nil {
		return nil, err
	}
	// 根不存在时保留原始拼写，实例内的扫描对缺失根返回空索引。
	if pathExists(root) {
		canonical, err := support.Canonicalize(root)
		if err != nil {
			return nil, err
		}
		root = canonical
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.slotIndex != nil && b.slotRoot == root {
		return b.slotIndex, nil
	}
	index := newGrokIndex(root)
	b.slotRoot = index.root
	b.slotIndex = index
	return index, nil
}

// ClearCache：索引实例的失效入口——reins 删除自己的数据后在动作发生地清掉；
// 通用清缓存流程（ClearAllCaches）经 Reader.ClearCache 到这。
func (b *grokBuildReader) ClearCache() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.slotIndex = nil
	b.slotRoot = ""
	return nil
}

// grokIndex：一个根下的族索引与它的指纹门控缓存。
type grokIndex struct {
	root   string
	mu     sync.Mutex
	cached *grokCachedFamilies
}

type grokCachedFamilies struct {
	fingerprint string
	index       FamilyIndex[grokRow]
}

func newGrokIndex(root string) *grokIndex {
	return &grokIndex{root: root}
}

// grokSessionRef：扫描阶段收集的会话入口路径（自家枚举，非符号链接、已在
// 根内），解析成本只发生在指纹失配时。
type grokSessionRef struct {
	path    string
	history *string
}

func (idx *grokIndex) familyIndex() (FamilyIndex[grokRow], error) {
	if !pathExists(idx.root) {
		return BuildFamilyIndex([]Family[grokRow]{}), nil
	}

	fingerprintFiles := make([]string, 0)
	sessions := make([]grokSessionRef, 0)

	// 会话目录在 bucket 目录下第三层，depth=3 的 summary.json 即全部会话
	// 入口；与 family index 构建使用同一发现规则。
	err := filepath.WalkDir(idx.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.Type().IsRegular() || entry.Name() != "summary.json" || dshDepthBelow(idx.root, path) != 3 {
			return nil
		}
		history, err := grokSiblingAt(path, "chat_history.jsonl")
		if err != nil {
			return err
		}
		fingerprintFiles = append(fingerprintFiles, path)
		if history != nil {
			fingerprintFiles = append(fingerprintFiles, *history)
		}
		sessions = append(sessions, grokSessionRef{path: path, history: history})
		return nil
	})
	if err != nil {
		return FamilyIndex[grokRow]{}, err
	}
	metaPaths, err := grokCollectSubagentMetaPaths(idx.root)
	if err != nil {
		return FamilyIndex[grokRow]{}, err
	}
	fingerprintFiles = append(fingerprintFiles, metaPaths...)
	fingerprint, err := SessionsFingerprint(fingerprintFiles)
	if err != nil {
		return FamilyIndex[grokRow]{}, err
	}

	idx.mu.Lock()
	if idx.cached != nil && idx.cached.fingerprint == fingerprint {
		index := idx.cached.index
		idx.mu.Unlock()
		return index, nil
	}
	idx.mu.Unlock()

	index, err := idx.buildFamilyIndex(sessions, metaPaths)
	if err != nil {
		return FamilyIndex[grokRow]{}, err
	}
	idx.mu.Lock()
	idx.cached = &grokCachedFamilies{fingerprint: fingerprint, index: index}
	idx.mu.Unlock()
	return index, nil
}

// buildFamilyIndex：构建逻辑与迁入前逐条对齐——行来自 summary 解析，归属只
// 认 meta，父缺失的子会话保留占位入口。解析成本只发生在指纹失配时：每会话
// 一次读取 summary/usage，不再逐文件重复跑路径验证。
func (idx *grokIndex) buildFamilyIndex(sessions []grokSessionRef, metaPaths []string) (FamilyIndex[grokRow], error) {
	rows := map[string]grokRow{}
	// child 校验所需的 (attempt_id, session_kind)：解析时顺手留存，避免
	// child 循环整份重读 summary.json。
	facts := map[string]struct {
		attemptID   *string
		sessionKind *string
	}{}
	for _, session := range sessions {
		value, err := grokReadSummary(session.path)
		if err != nil {
			return FamilyIndex[grokRow]{}, err
		}
		usage, err := grokReadUsageFile(session.path)
		if err != nil {
			return FamilyIndex[grokRow]{}, err
		}
		summary, err := grokSummaryFromParts(&value, session.path, usage)
		if err != nil {
			return FamilyIndex[grokRow]{}, err
		}
		readable := true
		if session.history != nil {
			// 尚未写入 history 的会话按 0 条消息读取，而不是当成坏会话。
			file, err := os.Open(*session.history)
			if err != nil {
				readable = false
			} else {
				file.Close()
			}
		}
		facts[summary.SourceSessionID] = struct {
			attemptID   *string
			sessionKind *string
		}{attemptID: value.AttemptID, sessionKind: value.SessionKind}
		id := summary.SourceSessionID
		if _, exists := rows[id]; exists {
			return FamilyIndex[grokRow]{}, fmt.Errorf("Conflicting Grok Build session ids")
		}
		rows[id] = grokRow{summary: summary, readable: readable}
	}

	metas, err := grokParseSubagentMetas(metaPaths)
	if err != nil {
		return FamilyIndex[grokRow]{}, err
	}
	owners := map[string]string{}
	subagentIDs := map[string]bool{}
	for _, meta := range metas {
		if subagentIDs[meta.SubagentID] || meta.ParentSessionID == meta.ChildSessionID {
			return FamilyIndex[grokRow]{}, fmt.Errorf("Conflicting Grok Build subagent ownership")
		}
		subagentIDs[meta.SubagentID] = true
		if _, exists := owners[meta.ChildSessionID]; exists {
			return FamilyIndex[grokRow]{}, fmt.Errorf("Conflicting Grok Build subagent ownership")
		}
		owners[meta.ChildSessionID] = meta.ParentSessionID
	}

	claimed := map[string]bool{}
	for childID := range owners {
		claimed[childID] = true
	}
	children := map[string][]grokRow{}
	for _, meta := range metas {
		if _, parentExists := rows[meta.ParentSessionID]; !parentExists || claimed[meta.ParentSessionID] {
			continue
		}
		var child grokRow
		if row, ok := rows[meta.ChildSessionID]; ok {
			fact := facts[meta.ChildSessionID]
			if derefString(fact.attemptID) != meta.AttemptID || derefString(fact.sessionKind) != "subagent" {
				return FamilyIndex[grokRow]{}, fmt.Errorf("Grok Build child summary does not match subagent meta")
			}
			child = row
		} else {
			createdAt, err := grokParseOptionalTimestamp(meta.StartedAt, "subagent started_at")
			if err != nil {
				return FamilyIndex[grokRow]{}, err
			}
			updatedAt, err := grokParseOptionalTimestamp(meta.CompletedAt, "subagent completed_at")
			if err != nil {
				return FamilyIndex[grokRow]{}, err
			}
			if updatedAt == nil {
				updatedAt = createdAt
			}
			child = grokRow{summary: SessionSummary{
				SourceApp:       SourceGrokBuild,
				SourceSessionID: meta.ChildSessionID,
				Title:           meta.title(),
				TranscriptPath:  meta.ChildSummaryPath,
				CreatedAt:       createdAt,
				UpdatedAt:       updatedAt,
				// 子会话文件缺失，无法读 usage.json。
				Models:     []SessionModel{},
				Archived:   false,
				Compressed: false,
			}, readable: false}
		}
		child.meta = &meta
		children[meta.ParentSessionID] = append(children[meta.ParentSessionID], child)
	}

	// 与 Rust HashMap 迭代一样无序；BuildFamilyIndex 的排序把它归一。
	families := make([]Family[grokRow], 0, len(rows))
	for id, row := range rows {
		if parent, hasOwner := owners[id]; hasOwner {
			if _, parentExists := rows[parent]; parentExists && !claimed[parent] {
				continue
			}
		}
		members := []grokRow{row}
		members = append(members, children[id]...)
		delete(children, id)
		families = append(families, Family[grokRow]{Root: row, Members: members})
	}
	return BuildFamilyIndex(families), nil
}

// grokSummaryFile：summary.json 的已解析形态（serde derive 契约的字段集）。
type grokSummaryFile struct {
	Info struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"info"`
	ChatFormatVersion uint64  `json:"chat_format_version"`
	SessionSummary    string  `json:"session_summary"`
	GeneratedTitle    *string `json:"generated_title"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
	AttemptID         *string `json:"attempt_id"`
	SessionKind       *string `json:"session_kind"`
	// 会话当前选中的模型；旧版本会话可能没有该字段。
	CurrentModelID *string `json:"current_model_id"`
}

// grokUsageFile：Grok CLI 落盘的会话目录 usage.json——session 是全轮
// turn_completed 的累计汇总，turns/modelUsage 只是切片。旧版本会话没有该
// 文件，也没有 turns 数组。
type grokUsageFile struct {
	Session *grokUsageTotals `json:"session"`
	Turns   []grokUsageTurn  `json:"turns"`
}

type grokUsageTotals struct {
	InputTokens         uint64 `json:"inputTokens"`
	OutputTokens        uint64 `json:"outputTokens"`
	CachedReadTokens    uint64 `json:"cachedReadTokens"`
	CacheCreationTokens uint64 `json:"cacheCreationTokens"`
}

// grokUsageTurn：每轮的增量切片——sum(turns) == session 汇总，endedAt 是该
// 轮完成时间。modelUsage 是该轮按模型的切片（map 键为模型名），空 map 的旧
// 版本会话没有模型信息。
type grokUsageTurn struct {
	EndedAt             *string                    `json:"endedAt"`
	InputTokens         uint64                     `json:"inputTokens"`
	OutputTokens        uint64                     `json:"outputTokens"`
	CachedReadTokens    uint64                     `json:"cachedReadTokens"`
	CacheCreationTokens uint64                     `json:"cacheCreationTokens"`
	ModelUsage          map[string]grokUsageTotals `json:"modelUsage"`
}

// grokSliceUsage：inputTokens 含缓存命中部分（实测 input+output==
// totalTokens），扣除后与其他来源的「新输入」口径一致；outputTokens 已含
// reasoning。
func grokSliceUsage(totals grokUsageTotals) SessionTokenUsage {
	return SessionTokenUsage{
		InputTokens:      totals.InputTokens - min64(totals.InputTokens, totals.CachedReadTokens),
		OutputTokens:     totals.OutputTokens,
		CacheReadTokens:  totals.CachedReadTokens,
		CacheWriteTokens: totals.CacheCreationTokens,
	}
}

// grokSubagentMeta：meta.json 的声明形态。
type grokSubagentMeta struct {
	SubagentID      string  `json:"subagent_id"`
	AttemptID       string  `json:"attempt_id"`
	ParentSessionID string  `json:"parent_session_id"`
	ChildSessionID  string  `json:"child_session_id"`
	SubagentType    *string `json:"subagent_type"`
	Description     *string `json:"description"`
	Status          *string `json:"status"`
	StartedAt       *string `json:"started_at"`
	CompletedAt     *string `json:"completed_at"`
	// 声明关系时子会话应当存在的同级目录路径，仅用于缺失子会话的占位入口。
	ChildSummaryPath string `json:"-"`
}

func (m *grokSubagentMeta) title() string {
	if m.Description != nil {
		trimmed := strings.TrimSpace(*m.Description)
		if trimmed != "" {
			return trimmed
		}
	}
	if m.SubagentType != nil {
		return *m.SubagentType
	}
	return m.ChildSessionID
}

// grokRow：族成员行。
type grokRow struct {
	summary  SessionSummary
	meta     *grokSubagentMeta
	readable bool
}

func (r grokRow) MemberPath() string       { return r.summary.TranscriptPath }
func (r grokRow) FamilyRootID() string     { return r.summary.SourceSessionID }
func (r grokRow) MemberID() string         { return r.summary.SourceSessionID }
func (r grokRow) MemberCreatedAt() *int64  { return r.summary.CreatedAt }
func (r grokRow) MemberUpdatedAt() *int64  { return r.summary.UpdatedAt }
func (r grokRow) MemberTieBreaker() string { return r.summary.SourceSessionID }

var (
	_ FamilyRow = grokRow{}
	_ Reader    = (*grokBuildReader)(nil)
)

func grokValidID(id string) error {
	if id == "" || id == "." || id == ".." {
		return fmt.Errorf("Invalid Grok Build id")
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_') {
			return fmt.Errorf("Invalid Grok Build id")
		}
	}
	return nil
}

// grokRegularPath：路径必须落在固定会话布局内、逐级无符号链接（自根而下）。
func grokRegularPath(path string) error {
	configuredRoot, err := GrokBuildRoot()
	if err != nil {
		return err
	}
	root, err := support.Canonicalize(configuredRoot)
	if err != nil {
		return err
	}
	relative := ""
	if rel, err := filepath.Rel(root, path); err == nil {
		relative = rel
	} else if rel, err := filepath.Rel(configuredRoot, path); err == nil {
		relative = rel
	} else {
		return fmt.Errorf("Grok Build path outside sessions root")
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Grok Build path outside sessions root")
	}

	current := root
	if relative != "." {
		for _, component := range strings.Split(relative, string(filepath.Separator)) {
			if component == "" || component == "." || component == ".." {
				return fmt.Errorf("Invalid Grok Build path")
			}
			current = filepath.Join(current, component)
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("Grok Build symlink is not allowed")
			}
		}
	}
	return nil
}

// grokCollectSubagentMetaPaths：meta.json 的路径收集与解析分开——路径集合进
// 索引指纹，解析只发生在指纹失配的重建里。depth=5：
// root/<bucket>/<session>/subagents/<child>/meta.json。
func grokCollectSubagentMetaPaths(root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() && entry.Name() == "meta.json" && dshDepthBelow(root, path) == 5 {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func grokParseSubagentMetas(paths []string) ([]grokSubagentMeta, error) {
	metas := make([]grokSubagentMeta, 0)
	for _, path := range paths {
		if err := grokRegularPath(path); err != nil {
			return nil, err
		}
		childDir := filepath.Dir(path)
		if childDir == "" {
			return nil, fmt.Errorf("Invalid subagent directory")
		}
		subagentsDir := filepath.Dir(childDir)
		if filepath.Base(subagentsDir) != "subagents" {
			continue
		}
		parentDir := filepath.Dir(subagentsDir)
		if parentDir == subagentsDir {
			return nil, fmt.Errorf("Invalid subagent directory")
		}
		bucket := filepath.Dir(parentDir)
		if bucket == parentDir {
			return nil, fmt.Errorf("Invalid subagent directory")
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		value, err := decodeJSONBytes(contents)
		if err != nil {
			return nil, fmt.Errorf("Invalid Grok Build subagent meta: %w", err)
		}
		var meta grokSubagentMeta
		// serde derive 对必需字段（subagent_id 等）缺失即解析失败；Go 的
		// Unmarshal 缺字段不报错，这里先做存在性校验对齐。
		for _, key := range []string{"subagent_id", "attempt_id", "parent_session_id", "child_session_id"} {
			if _, ok := jsonString(value, key); !ok {
				return nil, fmt.Errorf("Invalid Grok Build subagent meta")
			}
		}
		if err := strictUnmarshal(value, &meta); err != nil {
			return nil, fmt.Errorf("Invalid Grok Build subagent meta: %w", err)
		}
		for _, id := range []string{meta.SubagentID, meta.ParentSessionID, meta.ChildSessionID} {
			if err := grokValidID(id); err != nil {
				return nil, err
			}
		}
		parentID := filepath.Base(parentDir)
		if parentID == "" || parentID == "." {
			return nil, fmt.Errorf("Invalid parent directory")
		}
		if meta.ParentSessionID != parentID || filepath.Base(childDir) != meta.SubagentID {
			return nil, fmt.Errorf("Grok Build subagent meta directory mismatch")
		}
		meta.ChildSummaryPath = filepath.Join(bucket, meta.ChildSessionID, "summary.json")
		metas = append(metas, meta)
	}
	return metas, nil
}

// strictUnmarshal：decodeJSONBytes 的 any → 具体结构（json.Unmarshal 与
// serde derive 的字段宽容度一致：未知字段忽略、类型不符报错）。
func strictUnmarshal(value any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func grokParseOptionalTimestamp(value *string, label string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	millis, ok := support.ParseTimestamp(*value)
	if !ok {
		return nil, fmt.Errorf("Invalid Grok Build %s", label)
	}
	return &millis, nil
}

// —— 自由函数转发到按根缓存的实例（family_for_id、parse_*）——

func grokFamilyIndex() (FamilyIndex[grokRow], error) {
	index, err := GrokBuildReader.index()
	if err != nil {
		return FamilyIndex[grokRow]{}, err
	}
	return index.familyIndex()
}

// grokFamilyForID：会话身份的唯一寻址入口——id → family 直查，未命中文案
// 逐字保留现状契约。
func grokFamilyForID(sourceSessionID string) (Family[grokRow], error) {
	index, err := grokFamilyIndex()
	if err != nil {
		return Family[grokRow]{}, err
	}
	family, ok := index.FamilyForID(sourceSessionID)
	if !ok {
		return Family[grokRow]{}, fmt.Errorf("Grok Build session not found")
	}
	return family, nil
}

// grokMemberPathForID：events 按成员自身目录读取——id 解析到该成员的
// transcript 路径（索引里的每个 id 都是某条成员的 member id）。
func grokMemberPathForID(sourceSessionID string) (string, error) {
	family, err := grokFamilyForID(sourceSessionID)
	if err != nil {
		return "", err
	}
	for i := range family.Members {
		if family.Members[i].MemberID() == sourceSessionID {
			return family.Members[i].summary.TranscriptPath, nil
		}
	}
	return "", fmt.Errorf("Grok Build session not found")
}

func grokFamilySummary(family Family[grokRow]) SessionSummary {
	summary := family.Root.summary
	family.ApplySummaryAggregates(&summary)
	// 子代理子会话是完整会话目录、各有自己的 usage.json，按成员求和。
	summary.TokenUsage = family.SumTokenUsage(func(row grokRow) *SessionTokenUsage {
		return row.summary.TokenUsage
	})
	// 模型同样按成员合并：子代理可能换过模型，与 token 求和同口径。
	tally := ModelTally{}
	for i := range family.Members {
		tally.Absorb(family.Members[i].summary.Models)
	}
	summary.Models = tally.IntoModels()
	return summary
}

func grokMarker(row grokRow) SessionMessage {
	meta := row.meta
	title := meta.title()
	payload := map[string]any{
		"type":              "subagent_started",
		"session_id":        row.MemberID(),
		"title":             title,
		"parent_session_id": meta.ParentSessionID,
		"agent_role":        meta.SubagentType,
		"status":            meta.Status,
		"started_at":        meta.StartedAt,
		"completed_at":      meta.CompletedAt,
		"transcript_path":   row.summary.TranscriptPath,
	}
	text := title
	return SessionMessage{
		ID:        fmt.Sprintf("grok-subagent-%s-%s", meta.ParentSessionID, row.MemberID()),
		Role:      "assistant",
		Timestamp: row.summary.CreatedAt,
		Blocks:    []ContentBlock{{Kind: "output_text", Text: &text, Payload: payload}},
		SessionID: someStr(row.MemberID()),
	}
}

// grokParentMessages：主时间线只暴露子代理入口（marker），子会话正文由
// parse_agent_messages 在打开入口时单独读取；父会话分页因此不必扫描全部
// 子会话。history 没有逐条时间，入口单独追加，不推断 spawn 在父会话中的
// 位置。
func grokParentMessages(family Family[grokRow]) ([]SessionMessage, error) {
	result, err := grokMessages(family.Root.MemberPath())
	if err != nil {
		return nil, err
	}
	for i := range family.Members {
		if family.Members[i].meta != nil {
			result = append(result, grokMarker(family.Members[i]))
		}
	}
	return result, nil
}

// grokFamilySourcePaths：overview 只列 root 和可读成员的路径；缺失子会话只
// 保留入口，不产生假路径。
func grokFamilySourcePaths(family Family[grokRow]) ([]string, error) {
	paths, err := grokSourcePaths(family.Root.MemberPath())
	if err != nil {
		return nil, err
	}
	for i := range family.Members {
		row := family.Members[i]
		if row.meta != nil && row.readable {
			more, err := grokSourcePaths(row.MemberPath())
			if err != nil {
				return nil, err
			}
			paths = append(paths, more...)
		}
	}
	return paths, nil
}

// grokValidatePath：所有入口（包括直接传入 transcript_path）都必须留在固定
// 会话布局内。
func grokValidatePath(path string) (string, error) {
	configuredRoot, err := GrokBuildRoot()
	if err != nil {
		return "", err
	}
	root, err := support.Canonicalize(configuredRoot)
	if err != nil {
		return "", err
	}
	if err := grokRegularPath(path); err != nil {
		return "", err
	}
	canonical, err := support.Canonicalize(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, canonical)
	if err != nil {
		return "", fmt.Errorf("Grok Build session is outside sessions root")
	}
	if strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("Grok Build session is outside sessions root")
	}
	// 3 个分量：<bucket>/<session>/summary.json。
	if len(strings.Split(relative, string(filepath.Separator))) != 3 || filepath.Base(canonical) != "summary.json" {
		return "", fmt.Errorf("Invalid Grok Build summary path")
	}
	return canonical, nil
}

func grokSummaryAt(path string) (SessionSummary, error) {
	normalized, err := grokValidatePath(path)
	if err != nil {
		return SessionSummary{}, err
	}
	value, err := grokReadSummary(normalized)
	if err != nil {
		return SessionSummary{}, err
	}
	usage, err := grokReadUsageFile(normalized)
	if err != nil {
		return SessionSummary{}, err
	}
	return grokSummaryFromParts(&value, normalized, usage)
}

// grokReadSummary：summary.json 的读取与内容校验——调用方负责路径本身可信
// （公共入口已 validate，扫描路径来自自家枚举）。
func grokReadSummary(path string) (grokSummaryFile, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return grokSummaryFile{}, err
	}
	value, err := decodeJSONBytes(contents)
	if err != nil {
		return grokSummaryFile{}, fmt.Errorf("Invalid Grok Build summary: %w", err)
	}
	var parsed grokSummaryFile
	if err := strictUnmarshal(value, &parsed); err != nil {
		return grokSummaryFile{}, fmt.Errorf("Invalid Grok Build summary: %w", err)
	}
	if err := grokValidID(parsed.Info.ID); err != nil {
		return grokSummaryFile{}, err
	}
	if parsed.ChatFormatVersion != 1 {
		return grokSummaryFile{}, fmt.Errorf("Unsupported Grok Build chat_format_version: %d", parsed.ChatFormatVersion)
	}
	if filepath.Base(filepath.Dir(path)) != parsed.Info.ID {
		return grokSummaryFile{}, fmt.Errorf("Grok Build summary id does not match session directory")
	}
	return parsed, nil
}

// grokReadUsageFile：usage.json 整份读取一次，供 usage 与模型两路共用。
func grokReadUsageFile(summaryPath string) (*grokUsageFile, error) {
	usagePath, err := grokSiblingAt(summaryPath, "usage.json")
	if err != nil {
		return nil, err
	}
	if usagePath == nil {
		return nil, nil
	}
	contents, err := os.ReadFile(*usagePath)
	if err != nil {
		return nil, err
	}
	value, err := decodeJSONBytes(contents)
	if err != nil {
		return nil, fmt.Errorf("Invalid Grok Build usage file %s: %w", *usagePath, err)
	}
	var parsed grokUsageFile
	if err := strictUnmarshal(value, &parsed); err != nil {
		return nil, fmt.Errorf("Invalid Grok Build usage file %s: %w", *usagePath, err)
	}
	return &parsed, nil
}

// grokSummaryFromParts：从已读取的 summary/usage 装配列表摘要；扫描路径与
// 按 id 的单会话读取共用。
func grokSummaryFromParts(value *grokSummaryFile, path string, usage *grokUsageFile) (SessionSummary, error) {
	title := value.Info.ID
	if value.GeneratedTitle != nil && strings.TrimSpace(*value.GeneratedTitle) != "" {
		title = *value.GeneratedTitle
	} else if strings.TrimSpace(value.SessionSummary) != "" {
		title = value.SessionSummary
	}

	createdAt, ok := support.ParseTimestamp(value.CreatedAt)
	if !ok {
		return SessionSummary{}, fmt.Errorf("Invalid Grok Build created_at")
	}
	updatedAt, ok := support.ParseTimestamp(value.UpdatedAt)
	if !ok {
		return SessionSummary{}, fmt.Errorf("Invalid Grok Build updated_at")
	}

	cwd := value.Info.Cwd
	var tokenUsage *SessionTokenUsage
	if usage != nil && usage.Session != nil {
		slice := grokSliceUsage(*usage.Session)
		tokenUsage = &slice
	}

	return SessionSummary{
		SourceApp:       SourceGrokBuild,
		SourceSessionID: value.Info.ID,
		Title:           title,
		Cwd:             &cwd,
		GitBranch:       nil,
		TranscriptPath:  path,
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
		// inputTokens 含缓存命中部分，扣除口径见 grokSliceUsage。
		TokenUsage: tokenUsage,
		// 会话用过的模型：usage.json 的 turns[].modelUsage 按模型切片，能看出
		// 换过模型；文件缺失（或旧版本没有 turns）时退回 current_model_id，
		// 只是「当前选中的模型」。Grok Build 没有 provider 字段。
		Models: grokSessionModels(usage, value.CurrentModelID),
		// Grok Build 既无归档也无压缩：上游 Summary 没有 archived 字段，
		// 转录一律明文（session/persistence.rs）。
		Archived:   false,
		Compressed: false,
	}, nil
}

// grokSessionModels：会话用过的模型——usage.json 的 turns[].modelUsage 按
// 模型切片，能看出换过模型；该文件缺失（或旧版本没有 turns）时退回
// summary.json 的 current_model_id，只是「当前选中的模型」，不代表整段会话。
func grokSessionModels(usage *grokUsageFile, currentModelID *string) []SessionModel {
	tally := ModelTally{}

	if usage != nil {
		for _, turn := range usage.Turns {
			for model := range turn.ModelUsage {
				tally.Record("", model)
			}
		}
	}
	if currentModelID != nil {
		tally.Record("", *currentModelID)
	}

	return tally.IntoModels()
}

// GrokUsageHours：用量曲线的小时桶——turns[] 每轮自带增量用量与 endedAt，
// 按轮归到本地时区的小时；旧版本 usage.json 没有 turns 时返回空桶。输入来自
// session_summary_paths 的自家枚举，兄弟检查走轻量版。
func GrokUsageHours(summaryPath string) (UsageHourBuckets, error) {
	buckets := UsageHourBuckets{}
	usagePath, err := grokSiblingAt(summaryPath, "usage.json")
	if err != nil {
		return nil, err
	}
	if usagePath == nil {
		return buckets, nil
	}

	contents, err := os.ReadFile(*usagePath)
	if err != nil {
		return nil, err
	}
	value, err := decodeJSONBytes(contents)
	if err != nil {
		return nil, fmt.Errorf("Invalid Grok Build usage file %s: %w", *usagePath, err)
	}
	var file grokUsageFile
	if err := strictUnmarshal(value, &file); err != nil {
		return nil, fmt.Errorf("Invalid Grok Build usage file %s: %w", *usagePath, err)
	}

	for _, turn := range file.Turns {
		var timestamp *int64
		if turn.EndedAt != nil {
			if millis, ok := support.ParseTimestamp(*turn.EndedAt); ok {
				timestamp = &millis
			}
		}

		turnUsage := grokSliceUsage(grokUsageTotals{
			InputTokens:         turn.InputTokens,
			OutputTokens:        turn.OutputTokens,
			CachedReadTokens:    turn.CachedReadTokens,
			CacheCreationTokens: turn.CacheCreationTokens,
		})
		if len(turn.ModelUsage) == 0 {
			MergeUsageEvent(buckets, hourKeyOpt(timestamp), "", turnUsage)
		} else {
			for model, slice := range turn.ModelUsage {
				MergeUsageEvent(buckets, hourKeyOpt(timestamp), model, grokSliceUsage(slice))
			}
		}
	}

	return buckets, nil
}

// GrokSessionSummaryPaths：会话目录在 bucket 目录下第三层，depth=3 的
// summary.json 即全部会话入口；与 family index 构建使用同一发现规则。
func GrokSessionSummaryPaths() ([]string, error) {
	root, err := GrokBuildRoot()
	if err != nil {
		return nil, err
	}
	if !pathExists(root) {
		return []string{}, nil
	}

	canonicalRoot, err := support.Canonicalize(root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(canonicalRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() && entry.Name() == "summary.json" && dshDepthBelow(canonicalRoot, path) == 3 {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// grokSibling：公共入口的兄弟文件读取（先验证 summary 路径本身）。
func grokSibling(path string, name string) (*string, error) {
	normalized, err := grokValidatePath(path)
	if err != nil {
		return nil, err
	}
	return grokSiblingAt(normalized, name)
}

// grokSiblingAt：兄弟文件的符号链接防线。调用方先保证 summary 路径本身已
// 验证（公共入口 grokSibling）或来自自家枚举（walkdir 不跟符号链接，扫描
// 路径天然在根内），这里只拦兄弟文件自身指向外部或非常规形态；NotFound 视
// 为还没有该文件。
func grokSiblingAt(summaryPath string, name string) (*string, error) {
	parent := filepath.Dir(summaryPath)
	if parent == "" || parent == "." {
		return nil, fmt.Errorf("Missing session directory")
	}
	candidate := filepath.Join(parent, name)
	info, err := os.Lstat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	_ = info
	resolved, err := support.Canonicalize(candidate)
	if err != nil {
		return nil, err
	}
	if resolved != candidate || !isRegularFile(resolved) {
		return nil, fmt.Errorf("Invalid Grok Build auxiliary file path")
	}
	return &resolved, nil
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// grokScan：逐行扫描一个兄弟 jsonl 文件；visit 的错误带行号并入同一层
// （前端只展示最外层错误消息，行号必须和原因拼在同一层，否则报错无法定位）。
func grokScan(path string, name string, visit func(index int, value any) error) error {
	filePath, err := grokSibling(path, name)
	if err != nil {
		return err
	}
	if filePath == nil {
		return nil
	}
	lines, err := openPlainLines(*filePath)
	if err != nil {
		return err
	}
	defer lines.Close()

	scanErr := forEachLine(lines, func(index int, line string) error {
		if strings.TrimSpace(line) == "" {
			return nil
		}
		value, err := decodeJSONBytes([]byte(line))
		if err != nil {
			return fmt.Errorf("Invalid Grok Build %s JSON at line %d: %w", name, index+1, err)
		}
		if err := visit(index, value); err != nil {
			return fmt.Errorf("Invalid Grok Build %s entry at line %d: %w", name, index+1, err)
		}
		return nil
	})
	return scanErr
}

// grokRequired：缺失字符串字段的报错文案。
func grokRequired(value any, key string) (string, error) {
	if text, ok := jsonString(value, key); ok {
		return text, nil
	}
	return "", fmt.Errorf("Missing Grok Build string field: %s", key)
}

func grokBlock(kind string, text *string) ContentBlock {
	return ContentBlock{Kind: kind, Text: text}
}

// grokToolStatuses：tool 结果的错误标记——events.jsonl 的 tool_completed 与
// updates.jsonl 的 tool_call_update 汇总。
func grokToolStatuses(path string) (map[string]bool, error) {
	statuses := map[string]bool{}
	if err := grokScan(path, "events.jsonl", func(_ int, value any) error {
		if valueType, _ := jsonString(value, "type"); valueType == "tool_completed" {
			if id, ok := jsonString(value, "tool_call_id"); ok {
				if outcome, ok := jsonString(value, "outcome"); ok {
					switch outcome {
					case "success":
						statuses[id] = false
					case "error":
						statuses[id] = true
					}
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := grokScan(path, "updates.jsonl", func(_ int, value any) error {
		update, _ := getJSONObjectPath(getJSONObject(value, "params"), "update")
		sessionUpdate, _ := jsonString(update, "sessionUpdate")
		switch sessionUpdate {
		case "tool_call", "tool_call_update":
			if id, ok := jsonString(update, "toolCallId"); ok {
				if status, ok := jsonString(update, "status"); ok {
					switch status {
					case "completed":
						if _, exists := statuses[id]; !exists {
							statuses[id] = false
						}
					case "failed":
						statuses[id] = true
					}
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return statuses, nil
}

// grokMessages：chat_history.jsonl 的消息装配。
func grokMessages(path string) ([]SessionMessage, error) {
	summary, err := grokSummaryAt(path)
	if err != nil {
		return nil, err
	}
	sessionID := summary.SourceSessionID
	statuses, err := grokToolStatuses(path)
	if err != nil {
		return nil, err
	}
	messages := make([]SessionMessage, 0)
	tools := map[string]string{}

	err = grokScan(path, "chat_history.jsonl", func(index int, value any) error {
		kind, err := grokRequired(value, "type")
		if err != nil {
			return err
		}
		blocks := make([]ContentBlock, 0)
		role := ""
		switch kind {
		case "system":
			role = "system"
		case "user":
			if synthetic, _ := jsonString(value, "synthetic_reason"); synthetic == "system_reminder" {
				return nil
			}
			items, ok := jsonArray(value, "content")
			if !ok {
				return fmt.Errorf("Invalid Grok Build user content")
			}
			for _, item := range items {
				itemType, err := grokRequired(item, "type")
				if err != nil {
					return err
				}
				switch itemType {
				case "text":
					text, err := grokRequired(item, "text")
					if err != nil {
						return err
					}
					blocks = append(blocks, grokBlock("text", &text))
				case "image":
					url, err := grokRequired(item, "url")
					if err != nil {
						return err
					}
					image := grokBlock("image", &url)
					image.Payload = map[string]any{"type": "image", "url": url}
					blocks = append(blocks, image)
				default:
					return fmt.Errorf("Unsupported Grok Build user content type")
				}
			}
			role = "user"
		case "reasoning":
			// 只投影可展示的思考；加密字段不得进入归一化 payload。
			items, ok := jsonArray(value, "content")
			if !ok || len(items) == 0 {
				items, ok = jsonArray(value, "summary")
			}
			if ok {
				for _, item := range items {
					text, err := grokRequired(item, "text")
					if err != nil {
						return err
					}
					blocks = append(blocks, grokBlock("thinking", &text))
				}
			}
			role = "assistant"
		case "assistant":
			role = "assistant"
		case "tool_result":
			id, err := grokRequired(value, "tool_call_id")
			if err != nil {
				return err
			}
			content, err := grokRequired(value, "content")
			if err != nil {
				return err
			}
			result := grokBlock("tool_result", &content)
			result.ToolCallID = someStr(id)
			if name, ok := tools[id]; ok {
				result.ToolName = someStr(name)
			}
			if status, ok := statuses[id]; ok {
				result.IsError = &status
			}
			blocks = append(blocks, result)
			role = "tool"
		default:
			return fmt.Errorf("Unsupported Grok Build chat history type at line %d", index+1)
		}

		if kind == "system" || kind == "assistant" {
			text, err := grokRequired(value, "content")
			if err != nil {
				return err
			}
			if text != "" {
				blocks = append(blocks, grokBlock("text", &text))
			}
		}
		if kind == "assistant" {
			// tool_calls 缺失（纯文本回答）时跳过装配，不影响其余块。
			calls, hasCalls := jsonArray(value, "tool_calls")
			if hasCalls {
				for _, call := range calls {
					id, err := grokRequired(call, "id")
					if err != nil {
						return err
					}
					name, err := grokRequired(call, "name")
					if err != nil {
						return err
					}
					// 模型输出中断会把截断的 arguments 落盘，Grok Build 已把该
					// 调用记为失败并继续会话；按 {raw} 原文降级展示，不让单条
					// 坏数据导致整个会话不可读。
					raw, err := grokRequired(call, "arguments")
					if err != nil {
						return err
					}
					arguments, err := decodeJSONBytes([]byte(raw))
					if err != nil {
						arguments = map[string]any{"raw": raw}
					}
					tool := grokBlock("tool_use", nil)
					tool.ToolCallID = someStr(id)
					tool.ToolName = someStr(name)
					tool.Payload = map[string]any{"input": arguments}
					tools[id] = name
					blocks = append(blocks, tool)
				}
			}
		}
		if len(blocks) > 0 {
			messages = append(messages, SessionMessage{
				ID:        fmt.Sprintf("grok-%s-message-%d", sessionID, index),
				Role:      role,
				Timestamp: nil,
				Blocks:    blocks,
				SessionID: someStr(sessionID),
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// grokAllEvents：events 时间线全量装载；原始记录不裁剪字段（加密字段仍不
// 下发）。
func grokAllEvents(path string) ([]SessionEvent, error) {
	events := make([]SessionEvent, 0)
	err := grokScan(path, "events.jsonl", func(index int, value any) error {
		kind, err := grokRequired(value, "type")
		if err != nil {
			return err
		}
		var timestamp *int64
		if raw, ok := jsonString(value, "ts"); ok {
			if millis, ok := support.ParseTimestamp(raw); ok {
				timestamp = &millis
			}
		}
		events = append(events, SessionEvent{
			ID:        fmt.Sprintf("grok-event-%d", index),
			Kind:      kind,
			Timestamp: timestamp,
			Summary:   kind,
			// raw 页展示原始记录：不裁剪字段（加密字段仍不下发）
			Payload: RecordPayload(value),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func grokSourcePaths(path string) ([]string, error) {
	normalized, err := grokValidatePath(path)
	if err != nil {
		return nil, err
	}
	paths := []string{normalized}
	for _, name := range []string{"chat_history.jsonl", "events.jsonl", "updates.jsonl"} {
		sibling, err := grokSibling(normalized, name)
		if err != nil {
			return nil, err
		}
		if sibling != nil {
			paths = append(paths, *sibling)
		}
	}
	return paths, nil
}

// —— Reader 契约 ——

// ListEntries 实现 Reader。
func (b *grokBuildReader) ListEntries() ([]SessionFileEntry, error) {
	index, err := grokFamilyIndex()
	if err != nil {
		return nil, err
	}
	entries := make([]SessionFileEntry, 0, len(index.Families))
	for i := range index.Families {
		summary := grokFamilySummary(index.Families[i])
		entries = append(entries, SessionFileEntry{
			Path:            summary.TranscriptPath,
			SourceSessionID: index.Families[i].Root.MemberID(),
			SortTimestamp:   derefDefault(summary.UpdatedAt),
			Summary:         &summary,
		})
	}
	sortEntries(entries)
	return entries, nil
}

// ParseSummary 实现 Reader：与 family 聚合语义一致——成员 id 解析到所属
// family 的 root 摘要。
func (b *grokBuildReader) ParseSummary(sourceSessionID string) (SessionSummary, error) {
	family, err := grokFamilyForID(sourceSessionID)
	if err != nil {
		return SessionSummary{}, err
	}
	return grokFamilySummary(family), nil
}

// ParseOverview 实现 Reader。
func (b *grokBuildReader) ParseOverview(sourceSessionID string) (SessionOverview, error) {
	family, err := grokFamilyForID(sourceSessionID)
	if err != nil {
		return SessionOverview{}, err
	}
	sourcePaths, err := grokFamilySourcePaths(family)
	if err != nil {
		return SessionOverview{}, err
	}
	return SessionOverview{
		Summary:      grokFamilySummary(family),
		SourcePaths:  sourcePaths,
		MessageCount: nil,
		EventCount:   nil,
		Agents: FamilyAgents(family, func(row grokRow) FamilyAgentLabel {
			if row.meta != nil {
				return FamilyAgentLabel{Kind: AgentLabelChild, Name: row.meta.title()}
			}
			return FamilyAgentLabel{Kind: AgentLabelRoot}
		}),
	}, nil
}

// ParseMessages 实现 Reader。
func (b *grokBuildReader) ParseMessages(sourceSessionID string) ([]SessionMessage, error) {
	family, err := grokFamilyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	return grokParentMessages(family)
}

// ParseEvents 实现 Reader。
func (b *grokBuildReader) ParseEvents(sourceSessionID string) ([]SessionEvent, error) {
	path, err := grokMemberPathForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	if _, err := grokSummaryAt(path); err != nil {
		return nil, err
	}
	return grokAllEvents(path)
}

// ParseAgentMessages 实现 Reader。
func (b *grokBuildReader) ParseAgentMessages(sourceSessionID string, agentSessionID string) ([]SessionMessage, error) {
	family, err := grokFamilyForID(sourceSessionID)
	if err != nil {
		return nil, err
	}
	var row *grokRow
	for i := range family.Members {
		if family.Members[i].meta != nil && family.Members[i].MemberID() == agentSessionID {
			row = &family.Members[i]
			break
		}
	}
	if row == nil {
		return nil, fmt.Errorf("Grok Build subagent does not belong to this session")
	}
	// 子会话缺失或损坏时整个父会话仍可读，只有打开该入口才报错。
	if !row.readable {
		return nil, fmt.Errorf("Grok Build subagent session is not readable")
	}
	// marker + 子会话正文：弹窗依赖 marker 分组，缺失时会退化为空内容。
	result := []SessionMessage{grokMarker(*row)}
	messages, err := grokMessages(row.MemberPath())
	if err != nil {
		return nil, err
	}
	result = append(result, messages...)
	return result, nil
}
