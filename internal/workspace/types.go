// Package workspace：目标/技能源/MCP 分发域。wire 契约经 frontend/bindings/
// 暴露给前端（wails 生成、不手改）：字段 camelCase、枚举 snake_case
// （SkillLinkState 是唯一 camelCase 例外）、Option 字段区分「省略」（omitempty
// 指针）与「写 null」（裸指针）；本包的 JSON 结构与该口径逐字段核对（见
// types_wire_test.go）。
package workspace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
)

// AgentTargetID：workspace 域的 target 标识（Rust AgentTargetId newtype）。
// wire 上就是字符串；规范化只经 normalizeTargetID 构造。
type AgentTargetID string

// ---------------------------------------------------------------------------
// 枚举（wire 拼写即常量值）
// ---------------------------------------------------------------------------

// McpTransport：MCP 条目的传输形态（serde snake_case）。
type McpTransport string

const (
	TransportStdio McpTransport = "stdio"
	TransportHTTP  McpTransport = "http"
	TransportSSE   McpTransport = "sse"
)

// parseMcpTransport：wire 串 → 枚举；未知值报错（serde unknown variant 语义）。
func parseMcpTransport(value string) (McpTransport, error) {
	switch McpTransport(value) {
	case TransportStdio, TransportHTTP, TransportSSE:
		return McpTransport(value), nil
	}
	return "", fmt.Errorf("未知 transport：%s", value)
}

// debugTransport：命令日志用的 Rust {:?} 拼写（变体 PascalCase）。
func debugTransport(t McpTransport) string {
	switch t {
	case TransportStdio:
		return "Stdio"
	case TransportHTTP:
		return "Http"
	case TransportSSE:
		return "Sse"
	}
	return string(t)
}

// parseMcpConfigType：config_type 存储串 → 枚举；未知值报错（serde
// unknown variant 语义，config_type 只在后端内部使用，不进 wire）。
func parseMcpConfigType(value string) (agents.McpConfigType, error) {
	switch agents.McpConfigType(value) {
	case agents.McpConfigCommon, agents.McpConfigOpenCode, agents.McpConfigGrokBuild, agents.McpConfigDsh:
		return agents.McpConfigType(value), nil
	}
	return "", fmt.Errorf("未知 config_type：%s", value)
}

// SkillLinkState：target skills 目录下链接的归类（serde camelCase——该枚举
// 是前端契约里唯一的 camelCase 枚举，拼写以 ts 生成物为准）。
type SkillLinkState string

const (
	LinkStateLinked       SkillLinkState = "linked"
	LinkStateExcluded     SkillLinkState = "excluded"
	LinkStateSourceMissed SkillLinkState = "sourceMissing"
	LinkStateUnmanaged    SkillLinkState = "unmanaged"
)

// McpTargetState：MCP 条目在 target 配置里的在场状态（serde snake_case）。
type McpTargetState string

const (
	TargetStatePresent      McpTargetState = "present"
	TargetStateMissing      McpTargetState = "missing"
	TargetStateError        McpTargetState = "error"
	TargetStateUnconfigured McpTargetState = "unconfigured"
)

// ---------------------------------------------------------------------------
// wire DTO（camelCase json tag；Option 语义见包注释）
// ---------------------------------------------------------------------------

// WorkspaceState：应用级状态（document/inspection 均为「写 null」的 Option）。
type WorkspaceState struct {
	Document   *WorkspaceConfigDocument `json:"document"`
	Inspection *WorkspaceInspection     `json:"inspection"`
}

// WorkspaceSelection：通用路径选择器的返回。
type WorkspaceSelection struct {
	WorkspaceDir string `json:"workspaceDir"`
}

// SkillBatchImportFile：批量导入文件选择返回；内容由后端读出，前端不再
// 另发读文件请求（也没有读任意路径的通用 command）。
type SkillBatchImportFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SkillSourceMutationInput：来源编辑弹窗的回写契约。
type SkillSourceMutationInput struct {
	SourceID            string   `json:"sourceId"`
	Ref                 *string  `json:"ref,omitempty"`
	Description         *string  `json:"description,omitempty"` // 总是携带当前值，nil 表示清空
	IncludeNamePatterns []string `json:"includeNamePatterns"`
	IncludePathPatterns []string `json:"includePathPatterns"`
}

// BatchGitSkillImportInput：批量导入命令入参。
type BatchGitSkillImportInput struct {
	JSONContent string `json:"jsonContent"`
}

// ValidationReport：config.yaml 的解析报告。
type ValidationReport struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// WorkspaceConfigDocument：config.yaml 文档视图（config 为「写 null」的
// Option：文件缺失或解析失败时为 null）。
type WorkspaceConfigDocument struct {
	ConfigPath string               `json:"configPath"`
	Exists     bool                 `json:"exists"`
	RawContent string               `json:"rawContent"`
	Config     *WorkspaceConfigView `json:"config"`
	Validation ValidationReport     `json:"validation"`
}

// WorkspaceConfigView：解析后的配置视图。
type WorkspaceConfigView struct {
	Targets      []TargetConfigView      `json:"targets"`
	SkillSources []SkillSourceConfigView `json:"skillSources"`
	Mcps         []McpConfigView         `json:"mcps"`
	Projects     []ProjectConfigView     `json:"projects"`
}

// TargetConfigView：target 编辑页视图。
type TargetConfigView struct {
	ID                   AgentTargetID      `json:"id"`
	SkillDir             string             `json:"skillDir"`
	ConfigPath           *string            `json:"configPath"` // 写 null 的 Option
	MCPConfigPrefix      string             `json:"mcpConfigPrefix"`
	MCPFormatDescription string             `json:"mcpFormatDescription"`
	MCPFormatExamples    []McpFormatExample `json:"mcpFormatExamples"`
}

// McpFormatExample：一段写入形态示例（形态标题 + 已序列化正文）。
type McpFormatExample struct {
	Label string `json:"label"`
	Body  string `json:"body"`
}

// TargetPreset：创建弹窗一次下发的内置工具预设（路径为 env 解析后的绝对
// 真实路径，grokbuild/pi 也能预填）。
type TargetPreset struct {
	TargetID        AgentTargetID `json:"targetId"`
	Label           string        `json:"label"`
	SkillDir        string        `json:"skillDir"`
	ConfigPath      *string       `json:"configPath"` // 写 null 的 Option
	MCPConfigPrefix string        `json:"mcpConfigPrefix"`
}

// SkillSourceConfigView：来源列表条目。Rust 侧是 tag="type" 判别联合，Go
// 侧建模为单一 struct：Type 是判别字段，Local/Git 变体字段并集且全部
// omitempty（local 变体不含 git 字段、反之亦然，wire 形状与 serde 输出
// 一致，由 types_wire_test.go 逐字节钉住）；公共字段（id/patterns/label）
// 两个变体都恒出现，不带 omitempty。前端按 .type narrow，无需改动。
type SkillSourceConfigView struct {
	Type                string   `json:"type"` // "local" | "git"
	ID                  string   `json:"id"`
	RootPath            string   `json:"rootPath,omitempty"`      // local 变体
	Repo                string   `json:"repo,omitempty"`          // git 变体
	Ref                 *string  `json:"ref,omitempty"`           // git 变体，未填省略
	LastFetchedAt       *int64   `json:"lastFetchedAt,omitempty"` // git 变体，取 git cache marker mtime
	Description         *string  `json:"description,omitempty"`
	IncludeNamePatterns []string `json:"includeNamePatterns"`
	IncludePathPatterns []string `json:"includePathPatterns"`
	Label               string   `json:"label"`
}

// McpConfigView：MCP 列表条目（createdAt/command/url/timeout 为「写 null」
// 的 Option，homepage 带 skip 属性未填省略）。
type McpConfigView struct {
	Name      string            `json:"name"`
	Transport McpTransport      `json:"transport"`
	CreatedAt *int64            `json:"createdAt"`
	Homepage  *string           `json:"homepage,omitempty"`
	Command   *string           `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       *string           `json:"url"`
	Headers   map[string]string `json:"headers"`
	Timeout   *uint64           `json:"timeout"`
}

// ProjectConfigView：项目视图。
type ProjectConfigView struct {
	ID     string             `json:"id"`
	Path   string             `json:"path"`
	Agents []TargetConfigView `json:"agents"`
}

// WorkspaceInspection：盘上现状巡检。
type WorkspaceInspection struct {
	ConfigPath string              `json:"configPath"`
	Targets    []TargetInspection  `json:"targets"`
	Mcps       []McpInspection     `json:"mcps"`
	Projects   []ProjectInspection `json:"projects"`
	Warnings   []string            `json:"warnings"`
}

// ProjectInspection：项目巡检。
type ProjectInspection struct {
	ID     string             `json:"id"`
	Agents []TargetInspection `json:"agents"`
}

// TargetInspection：target 巡检（skillDirLink 为「写 null」的 Option）。
type TargetInspection struct {
	ID             AgentTargetID     `json:"id"`
	SkillDirExists bool              `json:"skillDirExists"`
	SkillDirLink   *SkillDirLinkView `json:"skillDirLink"`
}

// McpInspection：MCP 在各 target 的巡检。
type McpInspection struct {
	Name    string                `json:"name"`
	Targets []McpTargetInspection `json:"targets"`
}

// McpTargetInspection：单 target 的 MCP 在场状态（projectId 未填省略）。
type McpTargetInspection struct {
	TargetID  AgentTargetID  `json:"targetId"`
	ProjectID *string        `json:"projectId,omitempty"`
	State     McpTargetState `json:"state"`
	Detail    string         `json:"detail"`
}

// SkillDirLinkView：给界面的落点链接归属（可选字段只发有值的那部分）。
// 别名 = 这个 target 不拥有那个目录，前端据它禁用与标注。
type SkillDirLinkView struct {
	LinkedTargetID *string `json:"linkedTargetId,omitempty"`
	LinkTarget     string  `json:"linkTarget"`
}

// SkillSyncItem：同步/卸载的事实条目。
type SkillSyncItem struct {
	SkillName       string        `json:"skillName"`
	TargetID        AgentTargetID `json:"targetId"`
	SourcePath      string        `json:"sourcePath"`
	DestinationPath string        `json:"destinationPath"`
	Action          string        `json:"action"`
	Detail          string        `json:"detail"`
}

// WorkspaceMcpMutationResult：MCP 增删改结果。
type WorkspaceMcpMutationResult struct {
	ServerName   string   `json:"serverName"`
	UpdatedPaths []string `json:"updatedPaths"`
}

// WorkspaceTargetMutationResult：target/项目增删改结果。
type WorkspaceTargetMutationResult struct {
	TargetID     AgentTargetID `json:"targetId"`
	UpdatedPaths []string      `json:"updatedPaths"`
}

// McpTargetMutationResult：单 target 的 MCP 应用/移除结果（updatedPath 为
// 「写 null」的 Option：noop 时为 null）。
type McpTargetMutationResult struct {
	ServerName  string        `json:"serverName"`
	TargetID    AgentTargetID `json:"targetId"`
	UpdatedPath *string       `json:"updatedPath"`
	Action      string        `json:"action"`
	Detail      string        `json:"detail"`
}

// McpTargetPreviewResult：单 target 的 MCP 预览。
type McpTargetPreviewResult struct {
	ServerName string        `json:"serverName"`
	TargetID   AgentTargetID `json:"targetId"`
	ConfigPath string        `json:"configPath"`
	Format     string        `json:"format"`
	Content    string        `json:"content"`
}

// DiscoveredSkill：发现的一个 skill。
type DiscoveredSkill struct {
	Name          string  `json:"name"`
	RelativePath  string  `json:"relativePath"`
	SkillFilePath string  `json:"skillFilePath"`
	SourcePath    *string `json:"sourcePath,omitempty"`
	Description   *string `json:"description,omitempty"` // frontmatter 解析失败或缺失为 nil
}

// SkillDiscoveryResultView：发现结果（ref 恒出现；excludedSkills 恒出现，
// 空 filter 时为空数组）。
type SkillDiscoveryResultView struct {
	DiscoveryID         string            `json:"discoveryId"`
	Repo                string            `json:"repo"`
	Ref                 *string           `json:"ref"`
	IncludeNamePatterns []string          `json:"includeNamePatterns"`
	IncludePathPatterns []string          `json:"includePathPatterns"`
	Skills              []DiscoveredSkill `json:"skills"`
	ExcludedSkills      []DiscoveredSkill `json:"excludedSkills"`
}

// BatchGitSkillImportSourceInput：批量导入 JSON 里的一条 git 来源。字段拼写
// 是导入文件的存储格式（Rust 侧无 rename_all，snake_case），不是 invoke 契约
// 的 camelCase；deny_unknown_fields 语义用 DisallowUnknownFields 复刻。
type BatchGitSkillImportSourceInput struct {
	Repo                string   `json:"repo"`
	Ref                 string   `json:"ref"`
	Description         *string  `json:"description"`
	IncludeNamePatterns []string `json:"include_name_patterns"`
	IncludePathPatterns []string `json:"include_path_patterns"`
}

// BatchGitSkillImportItemResult：批量导入的单条结果（ref/error 为「写 null」
// 的 Option）。
type BatchGitSkillImportItemResult struct {
	Repo                string   `json:"repo"`
	Ref                 *string  `json:"ref"`
	IncludeNamePatterns []string `json:"includeNamePatterns"`
	IncludePathPatterns []string `json:"includePathPatterns"`
	ImportedCount       int      `json:"importedCount"`
	SkillNames          []string `json:"skillNames"`
	Error               *string  `json:"error"`
}

// BatchGitSkillImportResult：批量导入汇总。
type BatchGitSkillImportResult struct {
	ImportedCount  int                             `json:"importedCount"`
	SucceededCount int                             `json:"succeededCount"`
	FailedCount    int                             `json:"failedCount"`
	Items          []BatchGitSkillImportItemResult `json:"items"`
}

// ProjectMutationInput：项目增改入参。
type ProjectMutationInput struct {
	ProjectID string   `json:"projectId"`
	Path      string   `json:"path"`
	Agents    []string `json:"agents"`
}

// SourceSyncInput：同步来源到一组 target 的统一入参（overwrite/sourceRoot/
// skills 可省略，serde(default) 语义）。
type SourceSyncInput struct {
	SourceID          string            `json:"sourceId"`
	SkillPaths        []string          `json:"skillPaths"`
	TargetIDs         []string          `json:"targetIds"`
	OverwriteExisting bool              `json:"overwriteExisting,omitempty"`
	SourceRoot        *string           `json:"sourceRoot,omitempty"`
	Skills            []SyncSkillOption `json:"skills,omitempty"`
}

// SourceSyncResult：同步结果。
type SourceSyncResult struct {
	Removed  []SkillSyncItem `json:"removed"`
	Applied  []SkillSyncItem `json:"applied"`
	Warnings []string        `json:"warnings"`
}

// SourceSyncConflict：同步冲突预演条目。
type SourceSyncConflict struct {
	SkillName       string        `json:"skillName"`
	TargetID        AgentTargetID `json:"targetId"`
	SourcePath      string        `json:"sourcePath"`
	DestinationPath string        `json:"destinationPath"`
	ExistingKind    string        `json:"existingKind"`
	Detail          string        `json:"detail"`
}

// SyncTargetOption：同步弹窗的 target 候选（projectId/linkedTargetID/
// skillDirLinkTarget 为「写 null」的 Option）。
type SyncTargetOption struct {
	ID                 string                 `json:"id"`
	ProjectID          *string                `json:"projectId"`
	AgentID            string                 `json:"agentId"`
	Label              string                 `json:"label"`
	SkillDir           string                 `json:"skillDir"`
	LinkedTargetID     *string                `json:"linkedTargetId"`
	SkillDirLinkTarget *string                `json:"skillDirLinkTarget"`
	Links              []SkillLinkAssociation `json:"links"`
}

// SkillLinkAssociation：target skills 目录直接子项中发现的目录链接；实际
// 链接而非 config.yaml 才是安装状态的权威来源。
type SkillLinkAssociation struct {
	SkillName        string                `json:"skillName"`
	DestinationPath  string                `json:"destinationPath"`
	SourcePath       string                `json:"sourcePath"`
	Description      *string               `json:"description,omitempty"`
	MatchedSourceIDs []string              `json:"matchedSourceIds"`
	SourceRelatives  []SkillSourceRelative `json:"sourceRelatives"`
	State            SkillLinkState        `json:"state"`
}

// SkillSourceRelative：链接在归属来源根下的相对路径（Windows 前缀/大小写
// 口径只在后端算一次，前端展示直接取用）。
type SkillSourceRelative struct {
	SourceID     string `json:"sourceId"`
	RelativePath string `json:"relativePath"`
}

// SyncSkillOption：同步弹窗的 skill 候选（matched 恒出现；description 未填
// 省略）。
type SyncSkillOption struct {
	Name         string  `json:"name"`
	RelativePath string  `json:"relativePath"`
	Matched      bool    `json:"matched"`
	Description  *string `json:"description,omitempty"`
}

// SyncSkillOptionsResult：同步弹窗的 skill 候选集。
type SyncSkillOptionsResult struct {
	Skills     []SyncSkillOption `json:"skills"`
	SourceRoot string            `json:"sourceRoot"`
}

// SkillSourceRefreshResult：refresh_git_skill_source 的返回；cancelled=true
// 表示拉取被用户取消，本地缓存停留在旧内容，调用方不得把它当成功上报。
type SkillSourceRefreshResult struct {
	Cancelled bool `json:"cancelled"`
}

// ---------------------------------------------------------------------------
// config.yaml 落盘形态（Raw*）：与 Rust serde 拼写一致——snake_case 键、
// Option 无 skip 属性的写 null、空集合写 {} / []、RawSkillSourceConfig 经
// 判别联合 flatten（type 内联）。nil 与空集合在 marshal 时归一（见
// normalizeRawManagerConfig）。
// ---------------------------------------------------------------------------

// RawManagerConfig：config.yaml 的存储形态（user-editable）。
type RawManagerConfig struct {
	Targets      map[string]*RawTargetConfig  `yaml:"targets" json:"targets"`
	Projects     map[string]*RawProjectConfig `yaml:"projects" json:"projects"`
	SkillSources []*RawSkillSourceConfig      `yaml:"skill_sources" json:"skillSources"`
	Mcps         []*RawMcpConfig              `yaml:"mcps" json:"mcps"`
}

// RawProjectConfig：项目条目。
type RawProjectConfig struct {
	Path   string                            `yaml:"path" json:"path"`
	Agents map[string]*RawProjectAgentConfig `yaml:"agents" json:"agents"`
}

// RawProjectAgentConfig：空占位对象。保留具名类型是为了序列化出 {}（而不
// 是 null），同时让旧配置里遗留的 enabled 键被自然忽略。
type RawProjectAgentConfig struct{}

// RawTargetConfig：target 条目。
type RawTargetConfig struct {
	SkillDir string             `yaml:"skill_dir" json:"skillDir"`
	Mcp      RawTargetMcpConfig `yaml:"mcp" json:"mcp"`
}

// RawTargetMcpConfig：target 的 MCP 段。三个 Option 都不带 skip 属性，
// 落盘时未填写 null（与 serde 一致）。
type RawTargetMcpConfig struct {
	ConfigPath   *string               `yaml:"config_path" json:"configPath"`
	ConfigPrefix *string               `yaml:"config_prefix" json:"configPrefix"`
	ConfigType   *agents.McpConfigType `yaml:"config_type" json:"configType"`
}

// UnmarshalYAML：raw 解析阶段即校验 config_type 取值（serde unknown
// variant 语义：坏值在这里报错，而不是等到 writer 分发时静默错认）。
func (c *RawTargetMcpConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		ConfigPath   *string `yaml:"config_path"`
		ConfigPrefix *string `yaml:"config_prefix"`
		ConfigType   *string `yaml:"config_type"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	if raw.ConfigType != nil {
		configType, err := parseMcpConfigType(*raw.ConfigType)
		if err != nil {
			return err
		}
		c.ConfigType = &configType
	}
	c.ConfigPath = raw.ConfigPath
	c.ConfigPrefix = raw.ConfigPrefix
	return nil
}

// RawSkillSourceConfig：来源条目（id + flatten 的判别联合）。
type RawSkillSourceConfig struct {
	ID     string                   `yaml:"id" json:"id"`
	Source RawSkillSourceDefinition `yaml:",inline" json:",inline"`
}

// MarshalYAML：flatten 语义——id 与联合字段同层。yaml.v3 对实现了
// Marshaler 的 inline 字段不展开，这里手动合并成一张映射。
func (c *RawSkillSourceConfig) MarshalYAML() (any, error) {
	source, err := c.Source.marshalNode()
	if err != nil {
		return nil, err
	}
	merged := support.YMap([2]*yaml.Node{support.YStr("id"), support.YStr(c.ID)})
	merged.Content = append(merged.Content, source.Content...)
	return merged, nil
}

// UnmarshalYAML：id 与 type 同层，先取 type 再按判别字段分发。
func (c *RawSkillSourceConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		ID string `yaml:"id"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	c.ID = raw.ID
	return c.Source.unmarshalNode(node)
}

// RawSkillSourceDefinition：来源判别联合（tag="type"，snake_case 变体）。
// Rust 用 serde enum；Go 侧自定义 marshal/unmarshal 按判别字段分发，键序
// 与省略口径对齐 serde 输出：type 在首位，未填的 description/ref 写 null，
// patterns 恒出现（空数组），local 变体不含 git 字段、反之亦然。
type RawSkillSourceDefinition struct {
	Type                string   `json:"type"`
	RootPath            string   `json:"rootPath,omitempty"`
	Repo                string   `json:"repo,omitempty"`
	Ref                 *string  `json:"ref,omitempty"`
	Description         *string  `json:"description,omitempty"`
	IncludeNamePatterns []string `json:"includeNamePatterns,omitempty"`
	IncludePathPatterns []string `json:"includePathPatterns,omitempty"`
}

const (
	rawSourceKindLocal = "local"
	rawSourceKindGit   = "git"
)

// marshalNode：按变体构造映射节点（insertion order 即落盘键序）。
func (d *RawSkillSourceDefinition) marshalNode() (*yaml.Node, error) {
	switch d.Type {
	case rawSourceKindLocal:
		return sourceVariantNode(d.Type, d.RootPath, "", d.Ref, d.Description, d.IncludeNamePatterns, d.IncludePathPatterns), nil
	case rawSourceKindGit:
		return sourceVariantNode(d.Type, "", d.Repo, d.Ref, d.Description, d.IncludeNamePatterns, d.IncludePathPatterns), nil
	}
	return nil, fmt.Errorf("未知 skill source type：%s", d.Type)
}

// MarshalJSON：判别联合的 JSON 形态（本域 wire 不直接用它，但批量导出的
// 同形结构测试复用该口径；local 变体不携带 git 字段）。键序按 Rust serde
// 的字段声明序（tag 在首位），不能走 map——encoding/json 对 map 按字典序
// 输出，与 serde 输出漂移。
func (d RawSkillSourceDefinition) MarshalJSON() ([]byte, error) {
	type field struct {
		key   string
		value any
	}
	var fields []field
	switch d.Type {
	case rawSourceKindLocal:
		fields = append(fields,
			field{"root_path", d.RootPath})
	case rawSourceKindGit:
		fields = append(fields,
			field{"repo", d.Repo},
			field{"ref", d.Ref})
	default:
		return nil, fmt.Errorf("未知 skill source type：%s", d.Type)
	}
	fields = append(fields,
		field{"description", d.Description},
		field{"include_name_patterns", orEmpty(d.IncludeNamePatterns)},
		field{"include_path_patterns", orEmpty(d.IncludePathPatterns)})

	var builder strings.Builder
	builder.WriteString(`{"type":`)
	encodedType, err := json.Marshal(d.Type)
	if err != nil {
		return nil, err
	}
	builder.Write(encodedType)
	for _, item := range fields {
		encodedValue, err := json.Marshal(item.value)
		if err != nil {
			return nil, err
		}
		builder.WriteByte(',')
		encodedKey, err := json.Marshal(item.key)
		if err != nil {
			return nil, err
		}
		builder.Write(encodedKey)
		builder.WriteByte(':')
		builder.Write(encodedValue)
	}
	builder.WriteByte('}')
	return []byte(builder.String()), nil
}

// UnmarshalJSON：按 type 分发；未知 type 报错（serde unknown variant）。
func (d *RawSkillSourceDefinition) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type                string   `json:"type"`
		RootPath            string   `json:"root_path"`
		Repo                string   `json:"repo"`
		Ref                 *string  `json:"ref"`
		Description         *string  `json:"description"`
		IncludeNamePatterns []string `json:"include_name_patterns"`
		IncludePathPatterns []string `json:"include_path_patterns"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch raw.Type {
	case rawSourceKindLocal, rawSourceKindGit:
	default:
		return fmt.Errorf("未知 skill source type：%s", raw.Type)
	}
	d.Type = raw.Type
	d.RootPath = raw.RootPath
	d.Repo = raw.Repo
	d.Ref = raw.Ref
	d.Description = raw.Description
	d.IncludeNamePatterns = raw.IncludeNamePatterns
	d.IncludePathPatterns = raw.IncludePathPatterns
	return nil
}

// UnmarshalYAML：type 判别分发；未知 type 报错。
func (d *RawSkillSourceDefinition) unmarshalNode(node *yaml.Node) error {
	var raw struct {
		Type                string   `yaml:"type"`
		RootPath            string   `yaml:"root_path"`
		Repo                string   `yaml:"repo"`
		Ref                 *string  `yaml:"ref"`
		Description         *string  `yaml:"description"`
		IncludeNamePatterns []string `yaml:"include_name_patterns"`
		IncludePathPatterns []string `yaml:"include_path_patterns"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	switch raw.Type {
	case rawSourceKindLocal, rawSourceKindGit:
	default:
		return fmt.Errorf("未知 skill source type：%s", raw.Type)
	}
	d.Type = raw.Type
	d.RootPath = raw.RootPath
	d.Repo = raw.Repo
	d.Ref = raw.Ref
	d.Description = raw.Description
	d.IncludeNamePatterns = raw.IncludeNamePatterns
	d.IncludePathPatterns = raw.IncludePathPatterns
	return nil
}

// sourceVariantNode：联合变体的映射节点。description/ref 未填写 null
// （serde 无 skip 属性），patterns 恒出现；非本变体的字段不写入。
func sourceVariantNode(
	kind string,
	rootPath string,
	repo string,
	ref *string,
	description *string,
	includeNamePatterns []string,
	includePathPatterns []string,
) *yaml.Node {
	pairs := [][2]*yaml.Node{{support.YStr("type"), support.YStr(kind)}}
	if kind == rawSourceKindLocal {
		pairs = append(pairs, [2]*yaml.Node{support.YStr("root_path"), support.YStr(rootPath)})
	}
	if kind == rawSourceKindGit {
		pairs = append(pairs, [2]*yaml.Node{support.YStr("repo"), support.YStr(repo)})
		pairs = append(pairs, [2]*yaml.Node{support.YStr("ref"), stringOrNullNode(ref)})
	}
	pairs = append(pairs, [2]*yaml.Node{support.YStr("description"), stringOrNullNode(description)})
	pairs = append(pairs, [2]*yaml.Node{support.YStr("include_name_patterns"), stringsNodeSeq(includeNamePatterns)})
	pairs = append(pairs, [2]*yaml.Node{support.YStr("include_path_patterns"), stringsNodeSeq(includePathPatterns)})
	return support.YMap(pairs...)
}

func stringOrNullNode(value *string) *yaml.Node {
	if value == nil {
		return support.YNull()
	}
	return support.YStr(*value)
}

func stringsNodeSeq(values []string) *yaml.Node {
	items := make([]*yaml.Node, 0, len(values))
	for _, value := range values {
		items = append(items, support.YStr(value))
	}
	return support.YSeq(items...)
}

// RawMcpConfig：config.yaml 里的 MCP 条目（homepage 未填省略，其余 Option
// 写 null；transport 经 UnmarshalYAML 校验取值）。
type RawMcpConfig struct {
	Name      string            `yaml:"name" json:"name"`
	Transport McpTransport      `yaml:"transport" json:"transport"`
	CreatedAt *int64            `yaml:"created_at" json:"createdAt"`
	Homepage  *string           `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Command   *string           `yaml:"command" json:"command"`
	Args      []string          `yaml:"args" json:"args"`
	Env       map[string]string `yaml:"env" json:"env"`
	URL       *string           `yaml:"url" json:"url"`
	Headers   map[string]string `yaml:"headers" json:"headers"`
	Timeout   *uint64           `yaml:"timeout" json:"timeout"`
}

// UnmarshalYAML：raw 解析阶段即校验 transport 取值（serde unknown variant）。
func (c *RawMcpConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Name      string            `yaml:"name"`
		Transport string            `yaml:"transport"`
		CreatedAt *int64            `yaml:"created_at"`
		Homepage  *string           `yaml:"homepage"`
		Command   *string           `yaml:"command"`
		Args      []string          `yaml:"args"`
		Env       map[string]string `yaml:"env"`
		URL       *string           `yaml:"url"`
		Headers   map[string]string `yaml:"headers"`
		Timeout   *uint64           `yaml:"timeout"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	transport, err := parseMcpTransport(raw.Transport)
	if err != nil {
		return err
	}
	c.Name = raw.Name
	c.Transport = transport
	c.CreatedAt = raw.CreatedAt
	c.Homepage = raw.Homepage
	c.Command = raw.Command
	c.Args = raw.Args
	c.Env = raw.Env
	c.URL = raw.URL
	c.Headers = raw.Headers
	c.Timeout = raw.Timeout
	return nil
}

// RawTargetInput：target 增改入参（命令层散参组装）。
type RawTargetInput struct {
	TargetID   string
	SkillDir   string
	ConfigPath *string
}

// ---------------------------------------------------------------------------
// Resolved*：解析后的内存工作形态（BTreeMap → map + 显式排序，见
// sortedKeys / targetRefs 的迭代口径）。
// ---------------------------------------------------------------------------

// ResolvedManagerConfig：解析完成的配置。
type ResolvedManagerConfig struct {
	Targets      map[AgentTargetID]*ResolvedTargetConfig
	Projects     map[string]*ResolvedProjectConfig
	SkillSources map[string]*ResolvedSkillSourceDefinition
	Mcps         []*ResolvedMcpConfig
}

// ResolvedProjectConfig：解析完成的项目。
type ResolvedProjectConfig struct {
	ID          string
	ProjectPath string
	Agents      map[AgentTargetID]*ResolvedTargetConfig
}

// ResolvedTargetConfig：解析完成的 target。
type ResolvedTargetConfig struct {
	ID              AgentTargetID
	IsProject       bool
	SkillDir        string
	ConfigPath      *string
	MCPConfigPrefix string
	MCPConfigType   agents.McpConfigType
}

// ResolvedSkillSourceDefinition：解析完成的来源定义。
type ResolvedSkillSourceDefinition struct {
	Kind                string // "local" | "git"
	RootPath            string
	Repo                string
	Ref                 *string
	Description         *string
	IncludeNamePatterns []string
	IncludePathPatterns []string
}

// ResolvedMcpConfig：解析完成的 MCP 条目。
type ResolvedMcpConfig struct {
	Name      string
	Transport McpTransport
	CreatedAt *int64
	Homepage  *string
	Command   *string
	Args      []string
	Env       map[string]string
	URL       *string
	Headers   map[string]string
	Timeout   *uint64
}

// SkillDiscoverySnapshot：发现快照（DiscoverySourceDefinition + skills）。
type SkillDiscoverySnapshot struct {
	Source DiscoverySourceDefinition
	Skills []DiscoveredSkill
}

// DiscoverySourceDefinition：发现时刻的来源；git 持久缓存 checkout，不自动
// 清理。
type DiscoverySourceDefinition struct {
	Kind     string // "local" | "git"
	RootPath string
	Repo     string
	Ref      *string
}

// orEmpty：wire 上恒出现的集合字段兜底（nil 会序列化成 null）。
func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// sortedKeys：map 的 BTreeMap 迭代序（键字节序）。
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// normalizeRawManagerConfig：落盘前的 nil 集合归一。serde 的 BTreeMap/Vec
// 空值序列化成 {} / []，Go 的 nil map/slice 会写 null——两者都是合法 YAML，
// 但后者会让 config.yaml 与 Rust 版逐字节漂移。
func normalizeRawManagerConfig(config *RawManagerConfig) *RawManagerConfig {
	if config.Targets == nil {
		config.Targets = map[string]*RawTargetConfig{}
	}
	if config.Projects == nil {
		config.Projects = map[string]*RawProjectConfig{}
	}
	if config.SkillSources == nil {
		config.SkillSources = []*RawSkillSourceConfig{}
	}
	if config.Mcps == nil {
		config.Mcps = []*RawMcpConfig{}
	}
	for _, project := range config.Projects {
		if project.Agents == nil {
			project.Agents = map[string]*RawProjectAgentConfig{}
		}
	}
	for _, mcp := range config.Mcps {
		if mcp.Args == nil {
			mcp.Args = []string{}
		}
		if mcp.Env == nil {
			mcp.Env = map[string]string{}
		}
		if mcp.Headers == nil {
			mcp.Headers = map[string]string{}
		}
	}
	return config
}
