// state.go：workspace 域的运行时状态（对齐 Rust 版 state/skill_discovery.rs
// 与 skill_source_fetch.rs）：发现快照的容量上限缓存，与来源 git 拉取的单槽
// 取消标记。两者都是进程内状态，不落盘。
package workspace

import (
	"context"
	"sync"
)

// maxSnapshots：发现快照只在弹窗打开期间被消费，id 重新发现后不复用；限制
// 容量防止反复点击「发现」把快照无界堆积。
const maxSnapshots = 4

// snapshotStore：id → 快照 + 插入序。LRU 淘汰按插入序（不是访问序）：超过
// 容量时淘汰最早插入且仍存在的 id，与 Rust SnapshotStore 逐字一致。
type snapshotStore struct {
	entries        map[string]*SkillDiscoverySnapshot
	insertionOrder []string
}

func (s *snapshotStore) insert(discoveryID string, snapshot *SkillDiscoverySnapshot) {
	if s.entries == nil {
		s.entries = map[string]*SkillDiscoverySnapshot{}
	}
	if _, exists := s.entries[discoveryID]; !exists {
		s.insertionOrder = append(s.insertionOrder, discoveryID)
	}
	s.entries[discoveryID] = snapshot

	for len(s.insertionOrder) > maxSnapshots {
		evicted := s.insertionOrder[0]
		s.insertionOrder = s.insertionOrder[1:]
		delete(s.entries, evicted)
	}
}

func (s *snapshotStore) remove(discoveryID string) {
	delete(s.entries, discoveryID)
	kept := s.insertionOrder[:0]
	for _, id := range s.insertionOrder {
		if id != discoveryID {
			kept = append(kept, id)
		}
	}
	s.insertionOrder = kept
}

// SkillDiscoveryState：发现快照缓存。单实例由应用层持有；测试直接构造。
type SkillDiscoveryState struct {
	mu        sync.Mutex
	snapshots snapshotStore
}

// Load 取快照；不存在返回 ok=false（调用方报「未找到 discovery」）。
func (s *SkillDiscoveryState) Load(discoveryID string) (*SkillDiscoverySnapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots.entries[discoveryID]
	return snapshot, ok
}

// Store 存快照（同 id 覆盖、不改变插入序，与 Rust 一致）。
func (s *SkillDiscoveryState) Store(discoveryID string, snapshot *SkillDiscoverySnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots.insert(discoveryID, snapshot)
}

// Remove 删除快照（导入完成后释放，腾出容量不触发淘汰）。
func (s *SkillDiscoveryState) Remove(discoveryID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots.remove(discoveryID)
}

// SkillSourceFetchCancelState：skill 来源 git 拉取的取消槽。同一时刻只有一个
// 在途拉取（弹窗 busy 串行保证），单槽足够：Begin 每次换新 context，被换下
// 的旧拉取自然不可再取消；Cancel 只作用于当前槽，没有在途拉取时是无害的
// 空操作。Rust 的 CancelCheck+AtomicBool 在 Go 侧统一为 context.Context +
// context.Canceled（与 session/providers 域同惯例）。
type SkillSourceFetchCancelState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

// Begin 登记一次新拉取并返回它的 context；上一次在途拉取被永久换下（不取消
// ——Rust 语义：被换下的旧标记不再有人能置位，旧拉取自己跑完）。换下的
// cancel 不再被调用，context 资源随拉取方一同消亡，可接受。
func (s *SkillSourceFetchCancelState) Begin() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	return ctx
}

// Cancel 取消当前在途拉取；没有在途拉取时是空操作。
func (s *SkillSourceFetchCancelState) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}
