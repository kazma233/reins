// state_test.go：发现快照 LRU(4) 的容量与淘汰行为（对齐
// state/skill_discovery.rs 内嵌测试）。
package workspace

import (
	"strconv"
	"testing"
)

func testSnapshot() *SkillDiscoverySnapshot {
	return &SkillDiscoverySnapshot{
		Source: DiscoverySourceDefinition{Kind: rawSourceKindLocal, RootPath: "/tmp"},
		Skills: []DiscoveredSkill{},
	}
}

func TestSkillDiscoveryStateEvictsOldestSnapshotBeyondCapacity(t *testing.T) {
	state := &SkillDiscoveryState{}
	for index := 0; index < maxSnapshots+2; index++ {
		state.Store(discoveryIDForIndex(index), testSnapshot())
	}

	if _, ok := state.Load("id-0"); ok {
		t.Fatal("id-0 应被淘汰")
	}
	if _, ok := state.Load("id-1"); ok {
		t.Fatal("id-1 应被淘汰")
	}
	if _, ok := state.Load(discoveryIDForIndex(maxSnapshots)); !ok {
		t.Fatal("最新两条应仍在")
	}
	if _, ok := state.Load(discoveryIDForIndex(maxSnapshots + 1)); !ok {
		t.Fatal("最新两条应仍在")
	}
}

func TestSkillDiscoveryStateRemoveAllowsStoringMoreWithoutEviction(t *testing.T) {
	state := &SkillDiscoveryState{}
	for index := 0; index < maxSnapshots; index++ {
		state.Store(discoveryIDForIndex(index), testSnapshot())
	}
	state.Remove("id-0")
	state.Store("id-new", testSnapshot())

	if _, ok := state.Load("id-1"); !ok {
		t.Fatal("id-1 应仍在")
	}
	if _, ok := state.Load("id-new"); !ok {
		t.Fatal("id-new 应仍在")
	}
}

// 同 id 覆盖不改变插入序：重复点击发现不会把旧 id 挤掉后又挤掉新邻居。
func TestSkillDiscoveryStateOverwriteKeepsInsertionOrder(t *testing.T) {
	state := &SkillDiscoveryState{}
	state.Store("a", testSnapshot())
	state.Store("b", testSnapshot())
	state.Store("a", testSnapshot()) // 覆盖，不追加插入序
	state.Store("c", testSnapshot())
	state.Store("d", testSnapshot())
	state.Store("e", testSnapshot())

	// 容量 4：插入序 a,b,c,d,e → 淘汰 a（最早插入且未被覆盖改变），b,c,d,e 留存。
	if _, ok := state.Load("a"); ok {
		t.Fatal("a 应被淘汰（覆盖不改变插入序）")
	}
	for _, id := range []string{"b", "c", "d", "e"} {
		if _, ok := state.Load(id); !ok {
			t.Fatalf("%s 应仍在", id)
		}
	}
}

func discoveryIDForIndex(index int) string {
	return "id-" + strconv.Itoa(index)
}
