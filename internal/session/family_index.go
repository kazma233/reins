package session

import (
	"fmt"
	"sort"
)

// family_index.rs：SQL 父链分组与族索引的共享骨架。各来源自己枚举行、按
// 真差异分组（Claude 按共享 sessionId、dsh 走 catalog 声明的边集），SQL
// 父链（OpenCode/ZCode/Codex）共用这里的链上溯与分组骨架——来源只声明
// 孤儿策略与 root 挑选。分组之后的一切（成员与族排序、id 寻址）在这里
// 恰好一份。

// FamilyRow：引擎对来源行的契约。路径对文件型来源是转录文件，OpenCode/
// ZCode 的行来自 SQLite 没有路径字段，member_path 用「db路径:id」组合串。
// Option<i64> 用指针承载（nil ≡ None）。
type FamilyRow interface {
	// MemberPath：成员转录路径。
	MemberPath() string
	// FamilyRootID：族根的可解析 id——root 自己的 source id，不是分组键：
	// 孤儿 Codex 族回退以成员为 root，只有该 root 自己的 id 能认领族条目。
	FamilyRootID() string
	// MemberID：解析到成员自身文件的 id（Claude 是 agent_session_id，
	// Codex 是 source_session_id）。
	MemberID() string
	MemberCreatedAt() *int64
	MemberUpdatedAt() *int64
	// MemberTieBreaker：成员排序的次级键（Claude 用路径拼写，其余用 id）。
	MemberTieBreaker() string
}

// Family：同一逻辑会话的族：root 加全部成员（root 也在 members 里）。
type Family[R FamilyRow] struct {
	Root    R
	Members []R
}

// CreatedAt：全部成员 created_at 的最小值；无人有时为 nil。
func (f Family[R]) CreatedAt() *int64 {
	var min *int64
	for i := range f.Members {
		value := f.Members[i].MemberCreatedAt()
		if value != nil && (min == nil || *value < *min) {
			copied := *value
			min = &copied
		}
	}
	return min
}

// UpdatedAt：全部成员 updated_at 的最大值。
func (f Family[R]) UpdatedAt() *int64 {
	var max *int64
	for i := range f.Members {
		value := f.Members[i].MemberUpdatedAt()
		if value != nil && (max == nil || *value > *max) {
			copied := *value
			max = &copied
		}
	}
	return max
}

// SourcePaths：全部成员的转录路径，按成员顺序；OpenCode 这类多一个非成员
// 来源（db 文件）的来源自己前置。
func (f Family[R]) SourcePaths() []string {
	paths := make([]string, 0, len(f.Members))
	for i := range f.Members {
		paths = append(paths, f.Members[i].MemberPath())
	}
	return paths
}

// SumTokenUsage：family 全部成员（root、resume 段、子代理线程）的消耗相加
// 才是整个会话的用量；成员没有 usage 数据时跳过。
func (f Family[R]) SumTokenUsage(usageOf func(row R) *SessionTokenUsage) *SessionTokenUsage {
	var total *SessionTokenUsage
	for i := range f.Members {
		if usage := usageOf(f.Members[i]); usage != nil {
			MergeTokenUsage(&total, *usage)
		}
	}
	return total
}

// ApplySummaryAggregates：把 root 成员摘要折叠成 family 级形态——"(+N
// subagents)" 标题、root 转录路径、族级时间戳。OpenCode 直接从数据库行构
// 摘要，不走这个形状。
func (f Family[R]) ApplySummaryAggregates(summary *SessionSummary) {
	// 只统计 id 与 root 不同的成员：Codex 的 resume 续跑段与原始段共享
	// session id，是同一条线程而不是 subagent，不能按 len-1 计。
	rootID := f.Root.MemberID()
	childCount := 0
	for i := range f.Members {
		if f.Members[i].MemberID() != rootID {
			childCount++
		}
	}
	if childCount > 0 {
		summary.Title = fmt.Sprintf("%s (+%d subagents)", summary.Title, childCount)
	}

	summary.TranscriptPath = f.Root.MemberPath()
	if created := f.CreatedAt(); created != nil {
		summary.CreatedAt = created
	}
	summary.UpdatedAt = f.UpdatedAt()
}

// ---------------------------------------------------------------------------
// SQL 来源的父链分组（行内 parent_id 形态：opencode / zcode / codex）
// ---------------------------------------------------------------------------

// ParentChainRow：父链上溯的最低要求——行自身 id 与可选父 id。
type ParentChainRow interface {
	ChainID() string
	// ChainParentID：可选父 id；缺失时返回 ("", false)。
	ChainParentID() (string, bool)
}

// OrphanPolicy：父链上溯时父节点缺失的处置，各来源的孤儿归属语义。
// 由 family_index 显式声明，读取器不得静默统一（GLOSSARY 孤儿策略词条）。
type OrphanPolicy uint8

const (
	// OrphanClaimMissingParent：认领缺失的父 id 作为族根 id（opencode/zcode）
	// ：孤儿成员自成一家，族内没有该 id 的行，root 挑选回退首成员。
	OrphanClaimMissingParent OrphanPolicy = iota
	// OrphanKeepLastKnown：保留链上最后已知 id（codex）：孤儿挂靠最近的
	// 存活祖先。
	OrphanKeepLastKnown
)

// RootIDByParentChain 沿 parent 链上溯族根 id；环检测统一，成环按到达者收束。
func RootIDByParentChain[R ParentChainRow](row R, byID map[string]R, orphan OrphanPolicy) string {
	currentID := row.ChainID()
	parentID, hasParent := row.ChainParentID()
	visited := map[string]bool{currentID: true}

	for hasParent {
		if visited[parentID] {
			break
		}
		visited[parentID] = true

		parent, ok := byID[parentID]
		if !ok {
			if orphan == OrphanClaimMissingParent {
				currentID = parentID
			}
			break
		}

		currentID = parent.ChainID()
		parentID, hasParent = parent.ChainParentID()
	}

	return currentID
}

// GroupFamiliesByParentChain：按 root id 分组的公共骨架；root 在成员中的
// 挑选由来源声明（opencode/zcode 用 MemberRoot，codex 自带续跑段年龄的
// 确定性挑选）。pickRoot 返回 false 表示放弃该族（Rust None）。R 同时满足
// FamilyRow：分组产物直接是族列表。
func GroupFamiliesByParentChain[R interface {
	ParentChainRow
	FamilyRow
}](
	rows []R,
	orphan OrphanPolicy,
	pickRoot func(rootID string, members []R) (R, bool),
) []Family[R] {
	byID := make(map[string]R, len(rows))
	for i := range rows {
		byID[rows[i].ChainID()] = rows[i]
	}
	grouped := map[string][]R{}

	for i := range rows {
		row := rows[i]
		familyRootID := RootIDByParentChain(row, byID, orphan)
		grouped[familyRootID] = append(grouped[familyRootID], row)
	}

	// 与 Rust HashMap 迭代一样无序；下游 sort_families 与 id 表双写把它
	// 归一成确定性输出，不依赖这里的顺序。
	families := make([]Family[R], 0, len(grouped))
	for rootID, members := range grouped {
		root, ok := pickRoot(rootID, members)
		if !ok {
			continue
		}
		families = append(families, Family[R]{Root: root, Members: members})
	}
	return families
}

// MemberRoot：opencode/zcode 的 root 挑选——优先族根 id 自身的行，缺失
// （孤儿认领了不存在的父 id）时回退首成员。
func MemberRoot[R ParentChainRow](rootID string, members []R) (R, bool) {
	for i := range members {
		if members[i].ChainID() == rootID {
			return members[i], true
		}
	}
	if len(members) > 0 {
		return members[0], true
	}
	var zero R
	return zero, false
}

// FamilyIndex：族列表 + id → family 下标。id 表是会话身份的唯一寻址面。
type FamilyIndex[R FamilyRow] struct {
	Families []Family[R]
	// sessionsByID：双写胜者规则——family key（根自身 id）首见占位、成员 id
	// 后写覆盖，与迁移前 id→path 双写图逐字一致。
	sessionsByID map[string]int
}

// BuildFamilyIndex 排序后建 id 表。成员必须先全排序：下面所有首见占位
// 都依赖成员顺序，排序让索引成为输入的纯函数。
func BuildFamilyIndex[R FamilyRow](families []Family[R]) FamilyIndex[R] {
	sorted := make([]Family[R], len(families))
	copy(sorted, families)
	sortMembers(sorted)
	sortFamilies(sorted)

	sessionsByID := make(map[string]int)
	for position := range sorted {
		family := &sorted[position]
		if _, ok := sessionsByID[family.Root.FamilyRootID()]; !ok {
			sessionsByID[family.Root.FamilyRootID()] = position
		}
		for i := range family.Members {
			sessionsByID[family.Members[i].MemberID()] = position
		}
	}

	return FamilyIndex[R]{Families: sorted, sessionsByID: sessionsByID}
}

// FamilyForID：id → family 直查（返回副本；切片底层数组只读共享）。
func (idx *FamilyIndex[R]) FamilyForID(sourceSessionID string) (Family[R], bool) {
	position, ok := idx.sessionsByID[sourceSessionID]
	if !ok {
		return Family[R]{}, false
	}
	return idx.Families[position], true
}

func sortMembers[R FamilyRow](families []Family[R]) {
	for fi := range families {
		members := families[fi].Members
		sort.SliceStable(members, func(i, j int) bool {
			leftCreated, rightCreated := members[i].MemberCreatedAt(), members[j].MemberCreatedAt()
			if c := compareOptionalInt64(leftCreated, rightCreated); c != 0 {
				return c < 0
			}
			return members[i].MemberTieBreaker() < members[j].MemberTieBreaker()
		})
	}
}

// sortFamilies：family 顺序本身不进任何顺序敏感的消费（list_entries 会重排
// 输出），保持确定性是为了让 id 表也确定。
func sortFamilies[R FamilyRow](families []Family[R]) {
	sort.SliceStable(families, func(i, j int) bool {
		if c := compareOptionalInt64(families[j].UpdatedAt(), families[i].UpdatedAt()); c != 0 {
			return c < 0
		}
		return comparePaths(families[i].Root.MemberPath(), families[j].Root.MemberPath()) < 0
	})
}

// compareOptionalInt64 对齐 Rust Option<i64> 的 Ord：None < Some(_)。
func compareOptionalInt64(left *int64, right *int64) int {
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
