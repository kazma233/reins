// JSONC 最小预处理：providers（opencode.jsonc）与 workspace（MCP JSON 配置）
// 两个域共用的读侧口径。Rust 侧两处各自用 json5 全量解析；Go 侧按迁移决策
// 自写——剥 `//` 与 `/* */` 注释、剥 `}`/`]` 前的尾逗号，再交 encoding/json，
// 不引第三方 JSON5 库。支持范围是实际配置文件使用的子集（JSONC）；JSON5 的
// 其余特性（无引号键、单引号字符串、十六进制数字等）不支持。
package support

import "fmt"

// StripJSONCCommentsAndTrailingCommas：单遍状态机。注释按位置替换为空格
// （行注释保留换行），让「剥注释」不改变相邻 token 的分隔关系；字符串字面量
// （含转义）原样复制，"http://x" 这类内容不会被误剥。尾逗号替换为空格，
// 位置不变，避免删除引起的下标移动。
func StripJSONCCommentsAndTrailingCommas(content []byte) ([]byte, error) {
	out := make([]byte, 0, len(content))
	pendingComma := -1 // out 中尚未被后续 token 跟随的逗号下标；-1 = 无

	i := 0
	flushComma := func() { pendingComma = -1 }
	for i < len(content) {
		ch := content[i]
		switch ch {
		case '"':
			// 字符串整体复制（含转义序列）。
			out = append(out, ch)
			i++
			escaped := false
			for i < len(content) {
				c := content[i]
				out = append(out, c)
				i++
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == '"' {
					break
				}
			}
			flushComma()
		case '/':
			if i+1 < len(content) && content[i+1] == '/' {
				// 行注释：内容替换为空格，换行保留。
				for i < len(content) && content[i] != '\n' {
					out = append(out, ' ')
					i++
				}
				continue
			}
			if i+1 < len(content) && content[i+1] == '*' {
				out = append(out, ' ', ' ')
				i += 2
				closed := false
				for i < len(content) {
					if content[i] == '*' && i+1 < len(content) && content[i+1] == '/' {
						out = append(out, ' ', ' ')
						i += 2
						closed = true
						break
					}
					if content[i] == '\n' {
						out = append(out, '\n')
					} else {
						out = append(out, ' ')
					}
					i++
				}
				if !closed {
					return nil, fmt.Errorf("未闭合的块注释")
				}
				continue
			}
			out = append(out, ch)
			i++
			flushComma()
		case ',':
			out = append(out, ch)
			pendingComma = len(out) - 1
			i++
		case ' ', '\t', '\r', '\n':
			out = append(out, ch)
			i++
		case '{', '[', '}', ']':
			if (ch == '}' || ch == ']') && pendingComma >= 0 {
				out[pendingComma] = ' '
			}
			out = append(out, ch)
			i++
			flushComma()
		default:
			out = append(out, ch)
			i++
			flushComma()
		}
	}
	return out, nil
}
