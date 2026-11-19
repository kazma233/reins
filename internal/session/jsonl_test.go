package session

import (
	"encoding/json"
	"testing"
)

// jsonl.rs 本身无内联测试；这里守住解析器的行为分区：合法行、非法行、尾随
// 内容、数字精度与取值助手的缺失路径。

func TestParseJSONLineObject(t *testing.T) {
	value, err := parseJSONLine(`{"type":"message","n":18446744073709551615,"f":1.5}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	object := value.(map[string]any)
	if typ, ok := jsonType(object); !ok || typ != "message" {
		t.Errorf("jsonType = %q %v, want message", typ, ok)
	}
	// 整数按原始字面量保留（serde_json 的精确整数语义）。
	if number, ok := object["n"].(json.Number); !ok || number.String() != "18446744073709551615" {
		t.Errorf("n = %v, want exact u64 literal", object["n"])
	}
	if n, ok := jsonU64(object, "n"); !ok || n != 18446744073709551615 {
		t.Errorf("jsonU64 = %d %v", n, ok)
	}
}

func TestParseJSONLineRejectsGarbageAndTrailing(t *testing.T) {
	if _, err := parseJSONLine(`not json`); err == nil {
		t.Error("garbage line should fail")
	}
	if _, err := parseJSONLine(`{"a":1} trailing`); err == nil {
		t.Error("trailing content should fail (serde_json from_str)")
	}
	if _, err := parseJSONLine(``); err == nil {
		t.Error("empty line should fail")
	}
}

func TestJSONStringTraversesAndRejectsMissing(t *testing.T) {
	value := map[string]any{
		"a": map[string]any{"b": "hit", "n": 1},
	}
	if text, ok := jsonString(value, "a", "b"); !ok || text != "hit" {
		t.Errorf("jsonString = %q %v, want hit", text, ok)
	}
	if _, ok := jsonString(value, "a", "n"); ok {
		t.Error("non-string leaf should be false")
	}
	if _, ok := jsonString(value, "missing"); ok {
		t.Error("missing key should be false")
	}
	// 数组按字符串键下钻取不到（Rust Value::get 只对对象键命中）。
	if _, ok := jsonString([]any{"x"}, "0"); ok {
		t.Error("array should not answer string keys")
	}
}

func TestJSONU64RejectsNegativeAndFloat(t *testing.T) {
	value := map[string]any{
		"neg": json.Number("-1"),
		"fl":  json.Number("1.5"),
		"big": json.Number("18446744073709551616"),
		"u64": json.Number("42"),
	}
	for _, key := range []string{"neg", "fl", "big"} {
		if _, ok := jsonU64(value, key); ok {
			t.Errorf("jsonU64(%s) should be false", key)
		}
	}
	if n, ok := jsonU64(value, "u64"); !ok || n != 42 {
		t.Errorf("jsonU64(u64) = %d %v, want 42", n, ok)
	}
	if _, ok := jsonU64("not-object", "k"); ok {
		t.Error("non-object value should be false")
	}
}
