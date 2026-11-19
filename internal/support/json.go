// json.go：配置读侧的 JSON 解码统一口径。对齐 Rust serde_json::from_str 的
// 原契约：除完整值外还拒绝尾随内容（json.Decoder 缺省只消费第一个值）；
// 数字用 json.Number 承载、往返不失真（serde_json 直保原始字面量）。providers
// 与 workspace 的配置读侧统一走这里，不再各自维护一份 decoder 样板。
package support

import (
	"encoding/json"
	"errors"
	"io"
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
