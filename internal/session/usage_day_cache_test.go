package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reins/internal/testsupport"
)

// usage_day_cache.rs 无内联测试；以下守住本文件的行为分区：mtime 门控、来源
// 隔离、整表清空、旧版本文件清理与 HOME 切换重连。usage 桶值用
// UsageHourBuckets（模型为键的 map）这一真实持久化形状。

func TestUsageDayStoreLoadRoundTripAndMtimeGate(t *testing.T) {
	tempHome := testsupport.TestDir(t, "usage-roundtrip")
	homeGuard(t, tempHome)

	buckets := map[string]UsageHourBucket{
		"2026-09-04T09": {Usage: SessionTokenUsage{InputTokens: 10, OutputTokens: 5}},
		"2026-09-04T10": {
			Usage:  SessionTokenUsage{InputTokens: 3},
			Models: map[string]SessionTokenUsage{"deepseek-flash": {InputTokens: 3}},
		},
	}
	UsageDayStore(SourceCodex, "/tmp/some-transcript.jsonl", 100, buckets)

	loaded, ok := UsageDayLoad[map[string]UsageHourBucket](SourceCodex, "/tmp/some-transcript.jsonl", 100)
	if !ok {
		t.Fatal("cache hit expected")
	}
	if len(loaded) != 2 {
		t.Fatalf("days = %d, want 2", len(loaded))
	}
	if loaded["2026-09-04T09"].Usage.InputTokens != 10 {
		t.Errorf("bucket usage = %+v", loaded["2026-09-04T09"].Usage)
	}
	if model := loaded["2026-09-04T10"].Models["deepseek-flash"]; model.InputTokens != 3 {
		t.Errorf("model slice = %+v", model)
	}

	// mtime 变化即 miss；未写入的其他 mtime 也不命中。
	if _, ok := UsageDayLoad[map[string]UsageHourBucket](SourceCodex, "/tmp/some-transcript.jsonl", 101); ok {
		t.Error("changed mtime should miss")
	}
	// 来源不同不共享行。
	if _, ok := UsageDayLoad[map[string]UsageHourBucket](SourcePi, "/tmp/some-transcript.jsonl", 100); ok {
		t.Error("other source should miss")
	}
}

func TestUsageDayAccumulateEventShapes(t *testing.T) {
	var bucket UsageHourBucket
	bucket.AccumulateEvent("", &SessionTokenUsage{InputTokens: 4})
	bucket.AccumulateEvent("gpt-6-luna", &SessionTokenUsage{OutputTokens: 2})
	bucket.AccumulateEvent("gpt-6-luna", &SessionTokenUsage{OutputTokens: 3})

	if bucket.Usage.InputTokens != 4 || bucket.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v", bucket.Usage)
	}
	if model := bucket.Models["gpt-6-luna"]; model.OutputTokens != 5 {
		t.Errorf("model slice = %+v", model)
	}
	// 取不到模型信息的事件只进总量。
	if _, present := bucket.Models[""]; present {
		t.Error("empty model must not become a slice key")
	}

	// Accumulate 合并另一个桶（缓存聚合路径）。
	var merged UsageHourBucket
	merged.Accumulate(&bucket)
	if merged.Usage.OutputTokens != 5 || merged.Models["gpt-6-luna"].OutputTokens != 5 {
		t.Errorf("merged = %+v", merged)
	}
}

// 缓存行读写两端的 models 形状契约：Go 侧空桶写 null（nil map 的默认行
// 为），读回 null 按 nil map 处理等价空表；Rust 时代写入的旧行恒为对象，
// 同样可读。两端都容忍是迁移后契约——不设 MarshalJSON 的原因见
// UsageHourBucket 注释（自定义序列化器会让 wails bindings 渲染成 any）。
func TestUsageDayBucketModelsShapeContract(t *testing.T) {
	empty, err := json.Marshal(UsageHourBucket{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(empty), `"models":null`) {
		t.Errorf("empty bucket = %s, want null models", empty)
	}

	// 读回 null：反序列化成功且按空表处理（缓存读路径的容忍语义）。
	var fromNull UsageHourBucket
	if err := json.Unmarshal([]byte(`{"usage":{"inputTokens":0,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0},"models":null}`), &fromNull); err != nil {
		t.Fatalf("unmarshal null models: %v", err)
	}
	if len(fromNull.Models) != 0 {
		t.Errorf("null models = %+v, want empty", fromNull.Models)
	}

	// 读 Rust 时代写入的旧行（恒为对象）同样可行。
	var fromLegacy UsageHourBucket
	if err := json.Unmarshal([]byte(`{"usage":{"inputTokens":1,"outputTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0},"models":{}}`), &fromLegacy); err != nil {
		t.Fatalf("unmarshal legacy object models: %v", err)
	}

	populated, err := json.Marshal(UsageHourBucket{
		Usage:  SessionTokenUsage{InputTokens: 1},
		Models: map[string]SessionTokenUsage{"m": {}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(populated), `"models":{"m":{"inputTokens":0`) {
		t.Errorf("populated bucket = %s", populated)
	}
}

func TestUsageDayClearAllInvalidatesEveryRow(t *testing.T) {
	tempHome := testsupport.TestDir(t, "usage-clear")
	homeGuard(t, tempHome)

	UsageDayStore(SourceCodex, "/tmp/a.jsonl", 1, map[string]UsageHourBucket{"2026-09-04T09": {}})
	UsageDayClearAll()

	if _, ok := UsageDayLoad[map[string]UsageHourBucket](SourceCodex, "/tmp/a.jsonl", 1); ok {
		t.Error("clear_all must invalidate every row")
	}
}

func TestUsageDayOpeningRemovesStaleVersionFiles(t *testing.T) {
	tempHome := testsupport.TestDir(t, "usage-stale")
	cacheDir := filepath.Join(tempHome, ".reins")
	if err := os.MkdirAll(cacheDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	homeGuard(t, tempHome)

	for _, name := range []string{
		"usage-day-cache.db",
		"usage-day-cache.db-wal",
		"usage-day-cache_v1.db",
		"usage-day-cache_v2.db-shm",
	} {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte("stale"), 0o644); err != nil {
			t.Fatalf("write stale fixture: %v", err)
		}
	}

	UsageDayStore(SourceCodex, "/tmp/a.jsonl", 1, map[string]UsageHourBucket{})

	remaining, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	for _, entry := range remaining {
		if !strings.HasPrefix(entry.Name(), UsageDayDBFileName) {
			t.Errorf("stale cache db file survived: %s", entry.Name())
		}
	}
}

// HOME 被测试覆盖切换后，单例必须重开连接指向新库，而不是读到上一个环境的
// 数据（Rust with_connection 的路径变化分支）。
func TestUsageDayReopensWhenHomeChanges(t *testing.T) {
	firstHome := testsupport.TestDir(t, "usage-home-a")
	homeGuard(t, firstHome)
	UsageDayStore(SourceCodex, "/tmp/switch.jsonl", 1, map[string]UsageHourBucket{
		"2026-09-04T09": {Usage: SessionTokenUsage{InputTokens: 7}},
	})

	secondHome := testsupport.TestDir(t, "usage-home-b")
	homeGuard(t, secondHome)
	if _, ok := UsageDayLoad[map[string]UsageHourBucket](SourceCodex, "/tmp/switch.jsonl", 1); ok {
		t.Fatal("new home must start with an empty cache")
	}

	// 切回第一个 HOME 又能读到第一份库（路径寻址而非连接复用的证据）。
	homeGuard(t, firstHome)
	loaded, ok := UsageDayLoad[map[string]UsageHourBucket](SourceCodex, "/tmp/switch.jsonl", 1)
	if !ok {
		t.Fatal("switching back should hit the first home's cache")
	}
	if loaded["2026-09-04T09"].Usage.InputTokens != 7 {
		t.Errorf("loaded = %+v", loaded)
	}
}
