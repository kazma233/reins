package session

import (
	"fmt"
	"path/filepath"
	"testing"
)

// family_chain.rs 的策略测试：两种孤儿归属（opencode/zcode 认领缺失父 id、
// codex 保留最后已知 id）与环检测是收编进 family_index 的共享行为，按策略
// 逐一钉住；与各来源迁移前的逐字等价由既有行为测试守卫。

type chainRow struct {
	id     string
	parent *string
}

func (r chainRow) ChainID() string { return r.id }
func (r chainRow) ChainParentID() (string, bool) {
	if r.parent == nil {
		return "", false
	}
	return *r.parent, true
}

// FamilyRow 契约（分组产物是族列表，行需同时满足两接口）。
func (r chainRow) MemberPath() string       { return r.id }
func (r chainRow) FamilyRootID() string     { return r.id }
func (r chainRow) MemberID() string         { return r.id }
func (r chainRow) MemberCreatedAt() *int64  { return nil }
func (r chainRow) MemberUpdatedAt() *int64  { return nil }
func (r chainRow) MemberTieBreaker() string { return r.id }

func chainRows(rows ...[2]string) []chainRow {
	out := make([]chainRow, 0, len(rows))
	for _, row := range rows {
		entry := chainRow{id: row[0]}
		if row[1] != "" {
			parent := row[1]
			entry.parent = &parent
		}
		out = append(out, entry)
	}
	return out
}

func byChainID(rows []chainRow) map[string]chainRow {
	out := make(map[string]chainRow, len(rows))
	for _, row := range rows {
		out[row.id] = row
	}
	return out
}

func TestClaimMissingParentOrphansFormOwnFamily(t *testing.T) {
	rows := chainRows([2]string{"root", ""}, [2]string{"child", "ghost"})
	table := byChainID(rows)

	if got := RootIDByParentChain(rows[1], table, OrphanClaimMissingParent); got != "ghost" {
		t.Errorf("orphan root id = %q, want ghost", got)
	}
	if got := RootIDByParentChain(rows[0], table, OrphanClaimMissingParent); got != "root" {
		t.Errorf("root id = %q, want root", got)
	}

	families := GroupFamiliesByParentChain(rows, OrphanClaimMissingParent, MemberRoot[chainRow])
	if len(families) != 2 {
		t.Fatalf("families = %d, want 2", len(families))
	}
	// 认领的族里没有该 id 的行，root 回退首成员。
	for _, family := range families {
		if family.Root.ChainID() == "child" {
			if len(family.Members) != 1 {
				t.Errorf("orphan family members = %d, want 1", len(family.Members))
			}
			return
		}
	}
	t.Fatal("orphan family not found")
}

func TestKeepLastKnownAttachesOrphanToSurvivingAncestor(t *testing.T) {
	// root 缺席：mid 的链断在父上，leaf 经 mid 归到 mid——孤儿挂靠最近的
	// 存活祖先而不是认领缺失 id。
	rows := chainRows([2]string{"mid", "root"}, [2]string{"leaf", "mid"})
	table := byChainID(rows)

	if got := RootIDByParentChain(rows[0], table, OrphanKeepLastKnown); got != "mid" {
		t.Errorf("mid root id = %q, want mid", got)
	}
	if got := RootIDByParentChain(rows[1], table, OrphanKeepLastKnown); got != "mid" {
		t.Errorf("leaf root id = %q, want mid", got)
	}
}

func TestCycleStopsAtVisitedNode(t *testing.T) {
	rows := chainRows([2]string{"a", "a"}, [2]string{"b", ""})
	table := byChainID(rows)

	if got := RootIDByParentChain(rows[0], table, OrphanClaimMissingParent); got != "a" {
		t.Errorf("claim root id = %q, want a", got)
	}
	if got := RootIDByParentChain(rows[0], table, OrphanKeepLastKnown); got != "a" {
		t.Errorf("keep root id = %q, want a", got)
	}
}

func TestMemberRootPrefersRootIDRowThenFirst(t *testing.T) {
	rows := chainRows([2]string{"second", "root"}, [2]string{"root", ""})
	if root, ok := MemberRoot[chainRow]("root", rows); !ok || root.ChainID() != "root" {
		t.Errorf("member root = %v %v, want root", root, ok)
	}
	// 认领的族根 id 无对应行时回退首成员。
	if root, ok := MemberRoot[chainRow]("ghost", rows); !ok || root.ChainID() != "second" {
		t.Errorf("fallback root = %v %v, want second", root, ok)
	}
}

// —— 来源注册表（session_sources.rs）——

// 来源注册表是全部来源分发的唯一事实：必须恰好覆盖每个 SourceApp 一次；
// 顺序是可观察行为（detect 输出与 usage sources 都按该顺序产出），不得
// 随意重排。
func TestSourcesRegistryCoversEverySourceAppOnceInOrder(t *testing.T) {
	expected := []SourceApp{
		SourceCodex, SourceClaudeCode, SourceOpenCode,
		SourcePi, SourceGrokBuild, SourceZcode, SourceDsh,
	}
	if len(Sources) != len(expected) {
		t.Fatalf("registry size = %d, want %d", len(Sources), len(expected))
	}
	for i, spec := range Sources {
		if spec.App != expected[i] {
			t.Fatalf("registry[%d] = %s, want %s", i, spec.App, expected[i])
		}
	}

	// Spec 的查表必须与表同源：每个变体都路由到描述自己的条目，漏登记或
	// 错位都先在这里红。
	for _, app := range expected {
		if Spec(app).App != app {
			t.Errorf("Spec(%s) routed to %s", app, Spec(app).App)
		}
	}
}

// —— session_index.rs：family key / member id 双写寻址 ——

func TestClaudeResolvesFamilyKeyToRootAndMemberIDToMember(t *testing.T) {
	root := t.TempDir()
	rootID := "root-session"
	childID := "child-session"
	projectDir := filepath.Join(root, "projects", "demo-project")
	rootPath := filepath.Join(projectDir, rootID+".jsonl")
	childPath := filepath.Join(projectDir, "subagents", "agent-"+childID+".jsonl")

	// 子代理转录里的 sessionId 保持 root id——那是归族键；子代理自己的 id
	// 来自文件名。没有首见占位规则的话，后到的成员会抢走 family key。
	writeJSONLFile(t, rootPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Main task"}}`, rootID),
	))
	writeJSONLFile(t, childPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T15:00:00.000Z", "sessionId": %q, "type": "user", "message": {"content": "Child task"}}`, rootID),
	))

	reader := engineAt(t, ClaudeSpec{}, root, tempStore(t))
	rootSummary, err := reader.ParseSummary(rootID)
	if err != nil {
		t.Fatalf("parse root summary: %v", err)
	}
	if rootSummary.TranscriptPath != rootPath {
		t.Errorf("root transcript = %s, want %s", rootSummary.TranscriptPath, rootPath)
	}
	// 子会话按自身 id 直查仍归属同一 family（root 转录）。
	childSummary, err := reader.ParseSummary(childID)
	if err != nil {
		t.Fatalf("parse child summary: %v", err)
	}
	if childSummary.TranscriptPath != rootPath {
		t.Errorf("child resolves to %s, want root transcript %s", childSummary.TranscriptPath, rootPath)
	}
}

func TestCodexResolvesFamilyKeyToRootAndMemberIDToMember(t *testing.T) {
	root := t.TempDir()
	rootID := "44444444-4444-4444-8444-444444444444"
	childID := "55555555-5555-4555-8555-555555555555"
	rootPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T12-00-00-%s.jsonl", rootID))
	childPath := filepath.Join(root, "sessions", "2026", "04", "21",
		fmt.Sprintf("rollout-2026-04-21T15-00-00-%s.jsonl", childID))

	writeJSONLFile(t, rootPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T12:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace"}}`, rootID),
	))
	writeJSONLFile(t, childPath, jsonlLines(t,
		fmt.Sprintf(`{"timestamp": "2026-04-21T15:00:00.000Z", "type": "session_meta", "payload": {"id": %q, "cwd": "/tmp/reins/workspace", "source": {"subagent": {"thread_spawn": {"parent_thread_id": %q}}}}}`, childID, rootID),
	))

	reader := engineAt(t, CodexSpec{}, root, tempStore(t))
	rootSummary, err := reader.ParseSummary(rootID)
	if err != nil {
		t.Fatalf("parse root summary: %v", err)
	}
	if rootSummary.TranscriptPath != rootPath {
		t.Errorf("root transcript = %s, want %s", rootSummary.TranscriptPath, rootPath)
	}
	childSummary, err := reader.ParseSummary(childID)
	if err != nil {
		t.Fatalf("parse child summary: %v", err)
	}
	if childSummary.TranscriptPath != rootPath {
		t.Errorf("child resolves to %s, want root transcript %s", childSummary.TranscriptPath, rootPath)
	}
}
