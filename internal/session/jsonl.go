package session

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// JSONL 逐行解析（对齐 Rust 版 session/jsonl.rs）。
//
// 解析用 UseNumber：serde_json 把整数存成精确的 u64/i64，浮点才落 f64；Go
// encoding/json 默认全转 float64，会截断大整数 id（>2^53 的 snowflake）并在
// payload 回传时改写数字字面量。json.Number 保留原始字面量，两侧行为一致。

// parseJSONLine 解析一行 JSON。错误文案对齐 Rust 的 with_context 包装。
func parseJSONLine(line string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("Invalid JSONL line: %s: %w", line, err)
	}
	// serde_json::from_str 拒绝尾随内容；单次 Decode 不检查，补一次 More。
	if decoder.More() {
		return nil, fmt.Errorf("Invalid JSONL line: %s: trailing data", line)
	}
	return value, nil
}

// jsonType 取 value.type 的字符串值（Rust Value::get("type").as_str()）。
func jsonType(value any) (string, bool) {
	return jsonString(value, "type")
}

// jsonString 沿 keys 逐层下钻取末层字符串值；任一层缺失或非字符串返回 false。
func jsonString(value any, keys ...string) (string, bool) {
	current := value
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = object[key]
		if !ok {
			return "", false
		}
	}
	text, ok := current.(string)
	return text, ok
}

// jsonU64 取 value[key] 的无符号整数值。json.Number 精确解析；负数、小数与
// 超出 u64 的值都按缺失处理（对齐 serde as_u64）。
func jsonU64(value any, key string) (uint64, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	number, ok := object[key].(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// jsonI64 取 value[key] 的整数值（serde as_i64 口径：浮点字面量按缺失处理）。
func jsonI64(value any, key string) (int64, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	number, ok := object[key].(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

// jsonI64Path 沿 keys 逐层下钻取整数值（serde as_i64 口径）。
func jsonI64Path(value any, keys ...string) (int64, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	current := object
	for i, key := range keys {
		next, ok := current[key]
		if !ok {
			return 0, false
		}
		if i == len(keys)-1 {
			number, ok := next.(json.Number)
			if !ok {
				return 0, false
			}
			parsed, err := strconv.ParseInt(number.String(), 10, 64)
			if err != nil {
				return 0, false
			}
			return parsed, true
		}
		current, ok = next.(map[string]any)
		if !ok {
			return 0, false
		}
	}
	return 0, false
}

// jsonBool 取 value[key] 的布尔值。
func jsonBool(value any, key string) (bool, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return false, false
	}
	flag, ok := object[key].(bool)
	return flag, ok
}

// jsonArray 沿 keys 逐层下钻取数组值（Value::as_array 守卫）。
func jsonArray(value any, keys ...string) ([]any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	current := object
	for i, key := range keys {
		next, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(keys)-1 {
			items, ok := next.([]any)
			return items, ok
		}
		current, ok = next.(map[string]any)
		if !ok {
			return nil, false
		}
	}
	return nil, false
}

// jsonMarshal / jsonUnmarshal：any ↔ 结构体的桥（strictUnmarshal 的底座）。
func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}

func jsonUnmarshal(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
