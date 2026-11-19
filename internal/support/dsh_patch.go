// dsh 的 Cordis patch YAML（cordis.patch.yml）操作列表读写，对齐
// Rust 版 support/dsh_patch.rs。workspace 域（MCP 分发）与 providers
// 域（LLM 路由）写同一层格式、共用哨兵处理与序列化；Rust 版放在
// internal/support，Go 迁移期 internal/support 只读不改，先寄居本包——
// workspace 域移植时应上提为共享包（见交付报告）。
//
// YAML 文档用 yaml.Node 承载而不是 map[string]any：serde_yaml 的 Mapping
// 保持插入序（用户条目的键序、哨兵标签都是文件语义），Go 的 map 会按字典序
// 重排并丢弃标签，两条都会静默改变用户条目。
package support

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// bangbangSentinel：yaml.v3 会把双叹号标签（!!js 等 YAML 主命名空间简写）
// 解析成 tag:yaml.org,2002:js 并在解码 typed 值时报错；直接往返会静默改变
// 用户条目的运行时语义（!!js process.cwd() 变成字面路径）。读入时换成哨兵
// 单叹号标签保住 Tagged 结构，写出时再还原文本。哨兵字符串本身出现在用户
// 文件里的概率视为零。
const bangbangSentinel = "!reins-yaml-bangbang-"

// ReadOps 读 cordis patch 操作列表。文件不存在或空白内容返回空列表；
// 根节点不是序列报错。
func ReadOps(configPath string) ([]*yaml.Node, error) {
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
		return nil, fmt.Errorf("Cordis patch 解析失败：%s", configPath)
	}
	if len(document.Content) == 0 || IsNullNode(document.Content[0]) {
		return nil, nil
	}
	root := document.Content[0]
	if root.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("Cordis patch 根节点必须是操作列表：%s", configPath)
	}
	return root.Content, nil
}

// SerializeOps 序列化为可落盘文本；commands 层用它组装预览/写入内容，不落盘。
func SerializeOps(ops []*yaml.Node) (string, error) {
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: ops}
	MarkEmptyMappings(sequence)
	serialized, err := MarshalYAMLNode(sequence)
	if err != nil {
		return "", errors.New("Cordis patch 序列化失败")
	}
	// 哨兵标签还原回 !! 简写：见 bangbangSentinel 注释。
	return strings.ReplaceAll(serialized, bangbangSentinel, "!!"), nil
}

// MarshalYAMLNode：serde_yaml 缺省缩进 2 空格；yaml.v3 缺省 4，显式对齐。
func MarshalYAMLNode(node *yaml.Node) (string, error) {
	var builder strings.Builder
	encoder := yaml.NewEncoder(&builder)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// MarkEmptyMappings：空映射节点强制 flow 形态（{}）。yaml.v3 对空映射
// 默认只写键名不写值（语义是 null），而 serde_yaml 写 {}——dsh 要求
// refs 等段必须是映射，null 会被拒绝。
func MarkEmptyMappings(node *yaml.Node) {
	if node == nil {
		return
	}
	if node.Kind == yaml.MappingNode && len(node.Content) == 0 {
		node.Style = yaml.FlowStyle
		return
	}
	for _, child := range node.Content {
		MarkEmptyMappings(child)
	}
}

func WriteOps(configPath string, ops []*yaml.Node) error {
	contents, err := SerializeOps(ops)
	if err != nil {
		return err
	}
	return WriteOpsContent(configPath, contents)
}

func WriteOpsContent(configPath string, contents string) error {
	if err := WriteAtomic(configPath, contents); err != nil {
		return fmt.Errorf("写入 Cordis patch 失败：%s", configPath)
	}
	return nil
}

// ---------------------------------------------------------------------------
// yaml.Node 小工具（本文件与 dsh 适配器共用）
// ---------------------------------------------------------------------------

func YStr(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func YInt(value int64) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(value, 10)}
}

func YBool(value bool) *yaml.Node {
	if value {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
}

func YNull() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
}

// YMap 按给定的键值对序构造映射节点（insertion order 是文件语义）。
func YMap(pairs ...[2]*yaml.Node) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, pair := range pairs {
		node.Content = append(node.Content, pair[0], pair[1])
	}
	return node
}

func YSeq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
}

func IsNullNode(node *yaml.Node) bool {
	return node == nil || (node.Kind == yaml.ScalarNode && node.Tag == "!!null")
}

// NodeGet：映射节点按键取值；节点缺失或不是映射返回 nil。
func NodeGet(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// NodeSet：映射节点按键设值。键已存在时原位替换（保留键序，等价
// serde_yaml Mapping::insert 的既有键行为）；不存在时追加。node 不是映射
// 时不做任何事（调用方先行校验形状）。
func NodeSet(node *yaml.Node, key string, value *yaml.Node) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, YStr(key), value)
}

// NodeRemove：映射节点按键删除；返回是否删除了内容。
func NodeRemove(node *yaml.Node, key string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Kind == yaml.ScalarNode && node.Content[i].Value == key {
			node.Content = append(node.Content[:i], node.Content[i+2:]...)
			return true
		}
	}
	return false
}

// NodeAsStr：标量节点的字符串取值（本地标签节点如 !js value 的 Value 仍是
// 标量文本，同样可读——哨兵保证非字符串标签进不了这层）。
func NodeAsStr(node *yaml.Node) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	return node.Value, true
}

// NodeAsInt64：标量节点的整数值。
func NodeAsInt64(node *yaml.Node) (int64, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" {
		return 0, false
	}
	value, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// NodeAsBool：标量节点的布尔值。
func NodeAsBool(node *yaml.Node) (bool, bool) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return false, false
	}
	switch node.Tag {
	case "!!bool":
		return node.Value == "true" || node.Value == "True" || node.Value == "TRUE", true
	}
	return false, false
}

// Untag：展示用途的标签剥离（对齐 Rust support::dsh_patch::untag）：标签对
// 读取者有语义（ReadOps 靠哨兵保住 !!js），但展示给用户看的是干净值。深拷贝
// 剥离，不改入参；`!!` 开头的内建 tag（!!str 等）保留，自定义 tag 清空——与
// Rust 版一致。
func Untag(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	untagged := *node
	if untagged.Style&yaml.TaggedStyle != 0 {
		untagged.Style &^= yaml.TaggedStyle
		if !strings.HasPrefix(untagged.Tag, "!!") {
			untagged.Tag = ""
		}
	}
	for i := range untagged.Content {
		untagged.Content[i] = Untag(untagged.Content[i])
	}
	return &untagged
}
