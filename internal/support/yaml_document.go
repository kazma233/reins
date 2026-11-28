// yaml_document.go：整文档 YAML 的读改写原语。与 dsh patch（ReadOps/WriteOps）
// 同一套 !! 标签哨兵化口径（见 dsh_patch.go 的 bangbangSentinel 注释），但
// 作用于映射/任意根，供「手术编辑一份 YAML 配置、保留其余内容」的 writer
// 使用；本包不持有任何业务语义。
package support

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ReadYAMLDocument：读 YAML 文档的根节点。文件不存在或空白内容、根为
// null 时返回 nil（调用方自行构造新根）；根节点形状由调用方校验。
func ReadYAMLDocument(configPath string) (*yaml.Node, error) {
	rawContent, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	raw := string(rawContent)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	// !! 标签换哨兵：见 bangbangSentinel 注释。
	normalized := strings.ReplaceAll(raw, "!!", bangbangSentinel)
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(normalized), &document); err != nil {
		return nil, fmt.Errorf("YAML 解析失败：%s", configPath)
	}
	if len(document.Content) == 0 || IsNullNode(document.Content[0]) {
		return nil, nil
	}
	return document.Content[0], nil
}

// WriteYAMLDocument：以给定根节点整文档写回（哨兵还原 + 原子写）。空映射
// 强制 flow 形态（与 SerializeOps 同口径）。
func WriteYAMLDocument(configPath string, root *yaml.Node) error {
	MarkEmptyMappings(root)
	serialized, err := MarshalYAMLNode(root)
	if err != nil {
		return errors.New("YAML 序列化失败")
	}
	// 哨兵标签还原回 !! 简写：见 bangbangSentinel 注释。
	serialized = strings.ReplaceAll(serialized, bangbangSentinel, "!!")
	if err := WriteAtomic(configPath, serialized); err != nil {
		return fmt.Errorf("写入 YAML 配置失败：%s", configPath)
	}
	return nil
}
