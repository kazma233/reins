// JSONC 容忍读取：OpenCode 的 opencode.jsonc 允许注释与尾逗号。Rust 侧用
// json5 全量解析；Go 侧按主控拍板自写最小预处理——剥 `//` 与 `/* */` 注释、
// 剥 `}`/`]` 前的尾逗号——再交 encoding/json，不引第三方 JSON5 库。支持范围
// 是 opencode.jsonc 实际使用的子集；JSON5 的其余特性（无引号键、单引号
// 字符串、十六进制数字等）不支持，边界说明见交付报告。
package providers

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"reins/internal/support"
)

// readJSONCObject 读一个 JSONC 文件为顶层对象。行为与 readJSONObject 对齐：
// 文件不存在或空白内容返回空对象；顶层不是对象报错。
func readJSONCObject(path string) (map[string]any, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %s: %w", path, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return map[string]any{}, nil
	}
	stripped, err := support.StripJSONCCommentsAndTrailingCommas(content)
	if err != nil {
		return nil, fmt.Errorf("JSONC 解析失败：%s", path)
	}
	value, err := support.DecodeJSONStrict(string(stripped))
	if err != nil {
		return nil, fmt.Errorf("JSONC 解析失败：%s", path)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("配置文件顶层必须是 JSON 对象：%s", path)
	}
	return object, nil
}
