// json.go：配置读侧的 JSON 解码统一口径。对齐 Rust serde_json::from_str 的
// 原契约：除完整值外还拒绝尾随内容（json.Decoder 缺省只消费第一个值）；
// 数字用 json.Number 承载、往返不失真（serde_json 直保原始字面量）。providers
// 与 workspace 的配置读侧统一走这里，不再各自维护一份 decoder 样板。
//
// 动态树取值口径（叶子断言 + 逐层下钻）也在此：session / providers /
// workspace 的解码树共用同一套 miss 语义与数字精度处理。
package support

import (
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
)

// DecodeJSONStrict：严格解码单个 JSON 值，尾随内容按错误拒绝。
func DecodeJSONStrict(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("JSON 尾随内容")
	}
	return value, nil
}

// AsObject：断言为 JSON/TOML/YAML 的对象形态（map[string]any）；类型不符
// 按 miss（false）返回，下同。
func AsObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

// AsArray：断言为数组形态。
func AsArray(value any) ([]any, bool) {
	array, ok := value.([]any)
	return array, ok
}

// AsString：断言为字符串。
func AsString(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

// AsBool：断言为布尔。
func AsBool(value any) (bool, bool) {
	flag, ok := value.(bool)
	return flag, ok
}

// AsInt64：取整数值；int / int64 / json.Number（精确解析）都接受，浮点
// 字面量与超界按缺失处理。
func AsInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// AsUint64：取无符号整数值（serde as_u64 口径：负数、小数与超界都按缺失
// 处理）；仅接受 json.Number 精确解析。
func AsUint64(value any) (uint64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// ValueAt：对象的单键取值（缺键按 miss）。
func ValueAt(object map[string]any, key string) (any, bool) {
	value, ok := object[key]
	return value, ok
}

// PathValue：沿 keys 逐层下钻（任一层不是对象或缺键即 miss）；keys 为空
// 返回原值。终值只做定位，类型由调用方用 As* 断言。
func PathValue(value any, keys ...string) (any, bool) {
	current := value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// SortedKeys：map 键的字典序遍历（Rust BTreeMap 迭代序；反读条目顺序与
// 匹配优先级依赖它）。
func SortedKeys[V any](object map[string]V) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
