package support

import (
	"encoding/json"
	"testing"
)

// 动态树取值口径：miss 语义（缺值/类型不符一律 false）与数字精度处理
//（json.Number 精确解析，浮点字面量按缺失）是三域共用的唯一口径。

func number(literal string) json.Number {
	return json.Number(literal)
}

func TestAsInt64AcceptsIntFormsAndRejectsFloats(t *testing.T) {
	cases := []struct {
		value any
		want  int64
		ok    bool
	}{
		{int64(7), 7, true},
		{int(9), 9, true},
		{number("42"), 42, true},
		{number("-3"), -3, true},
		// 浮点字面量按缺失（serde as_i64 口径），字符串与 nil 同样 miss。
		{number("1.5"), 0, false},
		{number("1e3"), 0, false},
		{"42", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := AsInt64(c.value)
		if ok != c.ok || got != c.want {
			t.Errorf("AsInt64(%v) = %d,%v want %d,%v", c.value, got, ok, c.want, c.ok)
		}
	}
}

func TestAsUint64IsStrictUnsigned(t *testing.T) {
	if got, ok := AsUint64(number("18446744073709551615")); !ok || got != 18446744073709551615 {
		t.Errorf("u64 上界：%d,%v", got, ok)
	}
	// 负数、小数与非 json.Number 形态都按缺失（serde as_u64 口径）。
	for _, value := range []any{number("-1"), number("1.5"), int64(3), "8", nil} {
		if got, ok := AsUint64(value); ok {
			t.Errorf("AsUint64(%v) = %d 应按缺失", value, got)
		}
	}
}

func TestPathValueDrillsObjectsOnly(t *testing.T) {
	tree := map[string]any{
		"a": map[string]any{
			"b": map[string]any{"leaf": number("1")},
		},
		"scalar": "text",
	}
	if got, ok := PathValue(tree, "a", "b", "leaf"); !ok || got != number("1") {
		t.Errorf("下钻取值：%v,%v", got, ok)
	}
	// 中途缺键、中途非对象都 miss。
	if _, ok := PathValue(tree, "a", "missing"); ok {
		t.Error("缺键应 miss")
	}
	if _, ok := PathValue(tree, "scalar", "x"); ok {
		t.Error("非对象层应 miss")
	}
	// 起点本身不是对象同样 miss（keys 非空时）。
	if _, ok := PathValue("text", "a"); ok {
		t.Error("起点非对象应 miss")
	}
	// keys 为空返回原值（map 不可比较，经字段间接断言）。
	got, ok := PathValue(tree)
	if !ok {
		t.Fatal("空 keys 应返回原值")
	}
	if same, ok := AsObject(got); !ok || same["scalar"] != "text" {
		t.Errorf("空 keys 应返回原树：%v", got)
	}
}

func TestLeafAssertionsMissOnWrongType(t *testing.T) {
	if _, ok := AsString(1); ok {
		t.Error("非字符串应 miss")
	}
	if text, ok := AsString("s"); !ok || text != "s" {
		t.Errorf("字符串：%q,%v", text, ok)
	}
	if _, ok := AsBool("true"); ok {
		t.Error("非布尔应 miss")
	}
	if _, ok := AsObject([]any{}); ok {
		t.Error("数组不是对象")
	}
	if _, ok := AsArray(map[string]any{}); ok {
		t.Error("对象不是数组")
	}
	if _, ok := ValueAt(map[string]any{}, "missing"); ok {
		t.Error("缺键应 miss")
	}
}

func TestSortedKeysIteratesInLexicographicOrder(t *testing.T) {
	got := SortedKeys(map[string]int{"b": 1, "a": 2, "c": 3})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("SortedKeys = %v", got)
	}
}
