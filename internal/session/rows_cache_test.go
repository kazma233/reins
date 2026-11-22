package session

import (
	"fmt"
	"testing"
)

// 对齐 Rust 版 session/rows_cache.rs 内联测试。

type simpleRow struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

func fixtureRows() []simpleRow {
	return []simpleRow{{Name: "a", Value: 1}, {Name: "b", Value: 2}}
}

func TestRowsStoreThenLoadRoundTripsAndStampGatesHits(t *testing.T) {
	dir := t.TempDir()
	db := RowsDbAt(dir)
	defer db.Close()

	if rows, ok := LoadRows[simpleRow](db, SourceCodex, "/scan/root", "stamp:1"); ok {
		t.Fatalf("empty cache should miss, got %+v", rows)
	}

	StoreRows(db, SourceCodex, "/scan/root", "stamp:1", fixtureRows())
	hit, ok := LoadRows[simpleRow](db, SourceCodex, "/scan/root", "stamp:1")
	if !ok {
		t.Fatal("cache hit expected")
	}
	want := fixtureRows()
	if len(hit) != len(want) {
		t.Fatalf("rows = %d, want %d", len(hit), len(want))
	}
	for i := range want {
		if hit[i] != want[i] {
			t.Errorf("rows[%d] = %+v, want %+v", i, hit[i], want[i])
		}
	}

	// freshness 变化或换扫描根都视为 miss。
	if _, ok := LoadRows[simpleRow](db, SourceCodex, "/scan/root", "stamp:2"); ok {
		t.Error("changed stamp should miss")
	}
	if _, ok := LoadRows[simpleRow](db, SourceCodex, "/other/root", "stamp:1"); ok {
		t.Error("other root should miss")
	}
	// 来源不同也不共享行。
	if _, ok := LoadRows[simpleRow](db, SourceDsh, "/scan/root", "stamp:1"); ok {
		t.Error("other app should miss")
	}
}

func TestRowsClearAllInvalidatesEveryRow(t *testing.T) {
	dir := t.TempDir()
	db := RowsDbAt(dir)
	defer db.Close()
	StoreRows(db, SourceCodex, "/scan/root", "stamp:1", fixtureRows())
	db.ClearAll()

	if _, ok := LoadRows[simpleRow](db, SourceCodex, "/scan/root", "stamp:1"); ok {
		t.Error("clear_all must invalidate every row")
	}
}

func TestRowsDeserializationFailureIsAMiss(t *testing.T) {
	dir := t.TempDir()
	db := RowsDbAt(dir)
	defer db.Close()
	// 存入的形状与请求的类型不符，读出按 miss 而不是报错。
	StoreRows(db, SourceCodex, "/scan/root", "stamp:1", fixtureRows())
	if _, ok := LoadRows[string](db, SourceCodex, "/scan/root", "stamp:1"); ok {
		t.Error("type mismatch must be treated as a miss")
	}
}

func TestRowsContribRoundTripStampGatesAndRemoval(t *testing.T) {
	dir := t.TempDir()
	db := RowsDbAt(dir)
	defer db.Close()

	// 空表按空集处理，不报错。
	if contribs := db.LoadContribs(SourceOpenCode, "/db"); len(contribs) != 0 {
		t.Fatalf("empty table returned %+v", contribs)
	}

	db.SyncContribs(SourceOpenCode, "/db", []Contrib{
		{ID: "m1", Stamp: 10, Payload: `{"usage":1}`},
		{ID: "m2", Stamp: 20, Payload: `{"usage":2}`},
	}, nil)
	if contribs := db.LoadContribs(SourceOpenCode, "/db"); len(contribs) != 2 {
		t.Fatalf("contribs = %d, want 2", len(contribs))
	}

	// stamp 变化的行 upsert 覆盖，消失的行删除，未变的行原样保留。
	db.SyncContribs(SourceOpenCode, "/db", []Contrib{
		{ID: "m1", Stamp: 11, Payload: `{"usage":3}`},
	}, []string{"m2"})
	loaded := db.LoadContribs(SourceOpenCode, "/db")
	if len(loaded) != 1 {
		t.Fatalf("contribs = %d, want 1", len(loaded))
	}
	want := Contrib{ID: "m1", Stamp: 11, Payload: `{"usage":3}`}
	if loaded[0] != want {
		t.Errorf("contrib = %+v, want %+v", loaded[0], want)
	}

	// 来源或扫描根不同不共享贡献。
	db.SyncContribs(SourceOpenCode, "/db", []Contrib{{ID: "m3", Stamp: 1, Payload: "{}"}}, nil)
	if contribs := db.LoadContribs(SourceZcode, "/db"); len(contribs) != 0 {
		t.Errorf("zcode should see no contribs, got %d", len(contribs))
	}
	if contribs := db.LoadContribs(SourceOpenCode, "/other"); len(contribs) != 0 {
		t.Errorf("other root should see no contribs, got %d", len(contribs))
	}

	// 显式清缓存连贡献表一起清。
	db.ClearAll()
	if contribs := db.LoadContribs(SourceOpenCode, "/db"); len(contribs) != 0 {
		t.Errorf("clear_all must clear contribs, got %d", len(contribs))
	}
}

// 来源库整体清空/替换时 removed 是全量缓存行；超过 SQLite 绑定变量上限的
// 单条 DELETE IN 会让整个事务回滚，缓存从此永远写不进去，必须分批删净。
func TestRowsSyncRemovesBeyondSQLParameterLimitInBatches(t *testing.T) {
	dir := t.TempDir()
	db := RowsDbAt(dir)
	defer db.Close()

	const count = 32767
	upserts := make([]Contrib, 0, count)
	for i := 0; i < count; i++ {
		upserts = append(upserts, Contrib{ID: fmt.Sprintf("m%d", i), Stamp: int64(i), Payload: "{}"})
	}
	db.SyncContribs(SourceOpenCode, "/db", upserts, nil)
	if contribs := db.LoadContribs(SourceOpenCode, "/db"); len(contribs) != count {
		t.Fatalf("contribs = %d, want %d", len(contribs), count)
	}

	removed := make([]string, 0, count)
	for i := 0; i < count; i++ {
		removed = append(removed, fmt.Sprintf("m%d", i))
	}
	db.SyncContribs(SourceOpenCode, "/db", nil, removed)
	if contribs := db.LoadContribs(SourceOpenCode, "/db"); len(contribs) != 0 {
		t.Fatalf("batched DELETE must clear every row, %d survived", len(contribs))
	}
}

// 同一事务内 upsert 与删除混合提交：任一步失败整体回滚的原子性语义。
func TestRowsSyncUpsertsAndRemovalsCommitAtomically(t *testing.T) {
	dir := t.TempDir()
	db := RowsDbAt(dir)
	defer db.Close()

	// 一次事务里先 upsert 200 行再分批删 150 行，验证混合操作在同一事务内
	// 原子生效。
	upserts := make([]Contrib, 0, 200)
	for i := 0; i < 200; i++ {
		upserts = append(upserts, Contrib{ID: fmt.Sprintf("row-%d", i), Stamp: int64(i), Payload: "{}"})
	}
	removed := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		removed = append(removed, fmt.Sprintf("row-%d", i))
	}
	db.SyncContribs(SourceOpenCode, "/db", upserts, removed)
	if contribs := db.LoadContribs(SourceOpenCode, "/db"); len(contribs) != 50 {
		t.Fatalf("contribs = %d, want 50", len(contribs))
	}
}
