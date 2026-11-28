// Package workspace：技能路径/MCP 路径/技能源/MCP server 分发域。wire 契约经
// frontend/bindings/ 暴露给前端（wails 生成、不手改）：字段 camelCase、枚举
// snake_case（SkillLinkState 是唯一 camelCase 例外）、Option 字段区分「省略」
// （omitempty 指针）与「写 null」（裸指针）；本包的 JSON 结构与该口径逐字段
// 核对（见 types_wire_test.go）。
//
// 旧 schema（2026-11-28 前的 targets/projects）只在读入时迁移，见
// RawManagerConfig.UnmarshalYAML 与 docs/decisions/2026-11-28-skill-and-mcp-paths.md。
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
// unknown variant 语义，config_type 只在后端内部与预设下发使用）。
func parseMcpConfigType(value string) (agents.McpConfigType, error) {
	switch agents.McpConfigType(value) {
	case agents.McpConfigCommon, agents.McpConfigOpenCode, agents.McpConfigGrokBuild, agents.McpConfigDsh:
		return agents.McpConfigType(value), nil
	case agents.McpConfigHermes:
		// 旧配置的 hermes 类型：方言与 common 相同（YAML 载体），归一。
		return agents.McpConfigCommon, nil
	}
	return "", fmt.Errorf("未知 config_type：%s", value)
}

// SkillLinkState：技能路径目录下链接的归类（serde camelCase——该枚举
// 是前端契约里唯一的 camelCase 枚举，拼写以 ts 生成物为准）。
type SkillLinkState string

const (
	LinkStateLinked       SkillLinkState = "linked"
	LinkStateExcluded     SkillLinkState = "excluded"
	LinkStateSourceMissed SkillLinkState = "sourceMissing"
	LinkStateUnmanaged    SkillLinkState = "unmanaged"
)

// McpPathState：MCP 条目在 MCP 路径配置里的在场状态（serde snake_case）。
type McpPathState string

const (
	PathStatePresent McpPathState = "present"
	PathStateMissing McpPathState = "missing"
	PathStateError   McpPathState = "error"
)

// McpSyncAction：MCP 矩阵同步预演条目的动作（值即契约，前端按成员引用
// 分组渲染确认清单）。
type McpSyncAction string

const (
	McpSyncActionCreate McpSyncAction = "create"
	McpSyncActionRemove McpSyncAction = "remove"
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
	SkillPaths   []SkillPathView         `json:"skillPaths"`
	McpPaths     []McpPathView           `json:"mcpPaths"`
	SkillSources []SkillSourceConfigView `json:"skillSources"`
	Mcps         []McpConfigView         `json:"mcps"`
}

// SkillPathView：技能路径条目视图。
type SkillPathView struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// McpPathView：MCP 路径条目视图。config_type 由创建时选定、更新时保留
// 存量值，视图不携带（与旧 target 的编辑契约一致）。
type McpPathView struct {
	Name                 string             `json:"name"`
	ConfigPath           string             `json:"configPath"`
	ConfigPrefix         string             `json:"configPrefix"`
	MCPFormatDescription string             `json:"mcpFormatDescription"`
	MCPFormatExamples    []McpFormatExample `json:"mcpFormatExamples"`
	// SSEUnsupported：该配置文件对应的工具不接受 legacy SSE transport；
	// 前端据此禁用矩阵里 SSE server 的格子（值即契约，知识来自清单）。
	SSEUnsupported bool `json:"sseUnsupported"`
}

// McpFormatExample：一段写入形态示例（形态标题 + 已序列化正文）。
type McpFormatExample struct {
	Label string `json:"label"`
	Body  string `json:"body"`
}

// SkillPathPreset：技能路径创建弹窗的内置工具预设（目录为 env 解析后的
// 绝对真实路径；Name 是预填条目名，用户可改）。
type SkillPathPreset struct {
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

// McpPathPreset：MCP 路径创建弹窗的内置工具预设。ConfigType 随预设下发、
// 创建时原样传回（预设的格式知识归清单，前端不再静态维护）。
type McpPathPreset struct {
	Name              string `json:"name"`
	ConfigPath        string `json:"configPath"`
	ConfigPrefix      string `json:"configPrefix"`
	ConfigType        string `json:"configType"`
	FormatDescription string `json:"formatDescription"`
}

// McpFormatInfo：一个 MCP 配置格式的说明与写入形态示例（创建弹窗的格式
// 选择区下发；示例与真实写入走同一构造路径，前缀取该格式在清单里首个工具
// 的约定值，用户改前缀后以编辑页示例为准）。
type McpFormatInfo struct {
	ConfigType     string             `json:"configType"`
	Description    string             `json:"description"`
	FormatExamples []McpFormatExample `json:"formatExamples"`
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

// WorkspaceInspection：盘上现状巡检。
type WorkspaceInspection struct {
	ConfigPath string                `json:"configPath"`
	SkillPaths []SkillPathInspection `json:"skillPaths"`
	Mcps       []McpInspection       `json:"mcps"`
	Warnings   []string              `json:"warnings"`
}

// SkillPathInspection：技能路径巡检（dirLink 为「写 null」的 Option）。
type SkillPathInspection struct {
	Name      string            `json:"name"`
	DirExists bool              `json:"dirExists"`
	DirLink   *SkillDirLinkView `json:"dirLink"`
}

// McpInspection：MCP 条目在各 MCP 路径的巡检。
type McpInspection struct {
	Name  string                   `json:"name"`
	Paths []McpPathStateInspection `json:"paths"`
}

// McpPathStateInspection：单条 MCP 路径的条目在场状态。
type McpPathStateInspection struct {
	PathID string       `json:"pathId"`
	State  McpPathState `json:"state"`
	Detail string       `json:"detail"`
}

// SkillDirLinkView：给界面的落点链接归属（可选字段只发有值的那部分）。
// 别名 = 这条技能路径不拥有那个目录，前端据它禁用与标注。
type SkillDirLinkView struct {
	LinkedPathID *string `json:"linkedPathId,omitempty"`
	LinkTarget   string  `json:"linkTarget"`
}

// SkillSyncItem：同步/卸载的事实条目。
type SkillSyncItem struct {
	SkillName       string `json:"skillName"`
	PathID          string `json:"pathId"`
	SourcePath      string `json:"sourcePath"`
	DestinationPath string `json:"destinationPath"`
	Action          string `json:"action"`
	Detail          string `json:"detail"`
}

// WorkspaceMcpMutationResult：MCP 增删改结果。
type WorkspaceMcpMutationResult struct {
	ServerName   string   `json:"serverName"`
	UpdatedPaths []string `json:"updatedPaths"`
}

// WorkspacePathMutationResult：技能路径/MCP 路径增删改结果。
type WorkspacePathMutationResult struct {
	PathID       string   `json:"pathId"`
	UpdatedPaths []string `json:"updatedPaths"`
}

// McpPathMutationResult：单条 MCP 路径的应用/移除结果（updatedPath 为
// 「写 null」的 Option：noop 时为 null）。
type McpPathMutationResult struct {
	ServerName  string  `json:"serverName"`
	PathID      string  `json:"pathId"`
	UpdatedPath *string `json:"updatedPath"`
	Action      string  `json:"action"`
	Detail      string  `json:"detail"`
}

// McpSyncInput：MCP 矩阵同步入参：cells 是勾选的格子（登记 server × MCP
// 路径）；未勾选的组合按收敛语义处理（已装移除、未装不动），见决策记录
// 2026-11-28-mcp-matrix-sync.md 第 2 条。
type McpSyncInput struct {
	Cells []McpSyncCell `json:"cells"`
}

// McpSyncCell：矩阵里勾选的一个格子。
type McpSyncCell struct {
	ServerName string `json:"serverName"`
	PathID     string `json:"pathId"`
}

// McpSyncPreviewItem：聚合预演的条目级清单项（只列状态会变化的格子）。
// format/content 为「写 null」的 Option：仅创建项且条目构造成功时有值，
// 与真实写入同一条构造路径（writer 的 previewEntry）。
type McpSyncPreviewItem struct {
	ServerName string        `json:"serverName"`
	PathID     string        `json:"pathId"`
	Action     McpSyncAction `json:"action"`
	ConfigPath string        `json:"configPath"`
	Format     *string       `json:"format"`
	Content    *string       `json:"content"`
	Detail     string        `json:"detail"`
}

// McpSyncPreview：聚合预演结果。items 是确认可执行的清单（「看到的就
// 是将要发生的」按此成立）；failedCreates 是条目构造失败的创建项——只告知
// 不参与执行，确认执行集合由前端剔除对应格子；warnings 记录整条跳过的
// 路径（文件读不出）与未知格子。
type McpSyncPreview struct {
	Items         []McpSyncPreviewItem `json:"items"`
	FailedCreates []McpSyncPreviewItem `json:"failedCreates"`
	Warnings      []string             `json:"warnings"`
}

// McpSyncItem：矩阵同步的逐格事实。
type McpSyncItem struct {
	ServerName string `json:"serverName"`
	PathID     string `json:"pathId"`
	Detail     string `json:"detail"`
}

// McpSyncResult：矩阵同步结果。failed 只收集不中断其余格子；warnings 同预演。
type McpSyncResult struct {
	Applied  []McpSyncItem `json:"applied"`
	Removed  []McpSyncItem `json:"removed"`
	Failed   []McpSyncItem `json:"failed"`
	Warnings []string      `json:"warnings"`
}

// SkillPathCleanupPreview：删除技能路径的清理预演：将卸载的目录链接清单
// （这些链接指向任意受管技能源）。
type SkillPathCleanupPreview struct {
	Links []SkillSyncItem `json:"links"`
}

// McpPathCleanupPreview：删除 MCP 路径的清理预演：将被移除的 MCP 条目名
// （名字与 Reins 已配置 server 相同的条目）。
type McpPathCleanupPreview struct {
	ServerNames []string `json:"serverNames"`
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

// SourceSyncInput：同步来源到一组技能路径的统一入参（overwrite/sourceRoot/
// skills 可省略，serde(default) 语义）。
type SourceSyncInput struct {
	SourceID          string            `json:"sourceId"`
	SkillPaths        []string          `json:"skillPaths"`
	PathIDs           []string          `json:"pathIds"`
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
	SkillName       string `json:"skillName"`
	PathID          string `json:"pathId"`
	SourcePath      string `json:"sourcePath"`
	DestinationPath string `json:"destinationPath"`
	ExistingKind    string `json:"existingKind"`
	Detail          string `json:"detail"`
}

// SyncPathOption：同步弹窗的技能路径候选（linkedPathID/dirLinkTarget 为
// 「写 null」的 Option）。
type SyncPathOption struct {
	Name          string                 `json:"name"`
	Dir           string                 `json:"dir"`
	LinkedPathID  *string                `json:"linkedPathId"`
	DirLinkTarget *string                `json:"dirLinkTarget"`
	Links         []SkillLinkAssociation `json:"links"`
}

// SkillLinkAssociation：技能路径目录直接子项中发现的目录链接；实际
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
// config.yaml 落盘形态（Raw*）：snake_case 键、Option 无 skip 属性的写
// null、空集合写 {} / []、RawSkillSourceConfig 经判别联合 flatten（type 内
// 联）。nil 与空集合在 marshal 时归一（见 normalizeRawManagerConfig）。
// ---------------------------------------------------------------------------

// RawManagerConfig：config.yaml 的存储形态（user-editable）。旧 schema 的
// targets/projects 键由 UnmarshalYAML 读入并就地迁移（决策记录
// 2026-11-28-skill-and-mcp-paths.md 第 3 条），结构体不持有 legacy 字段、
// 落盘永不写回。
type RawManagerConfig struct {
	SkillPaths   map[string]*RawSkillPathConfig `yaml:"skill_paths" json:"skillPaths"`
	McpPaths     map[string]*RawMcpPathConfig   `yaml:"mcp_paths" json:"mcpPaths"`
	SkillSources []*RawSkillSourceConfig        `yaml:"skill_sources" json:"skillSources"`
	Mcps         []*RawMcpConfig                `yaml:"mcps" json:"mcps"`
}

// UnmarshalYAML：新 schema 解析 + 旧 targets/projects 读时迁移。迁移规则：
// 全局 target 拆成同名的技能路径与 MCP 路径各一条（MCP 段只有 config_path
// 存在才迁），新键已存在的条目不被旧值覆盖；projects 丢弃。旧条目的
// config_type 坏值按旧解析口径报错。
func (c *RawManagerConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		SkillPaths   map[string]*RawSkillPathConfig `yaml:"skill_paths"`
		McpPaths     map[string]*RawMcpPathConfig   `yaml:"mcp_paths"`
		SkillSources []*RawSkillSourceConfig        `yaml:"skill_sources"`
		Mcps         []*RawMcpConfig                `yaml:"mcps"`
		Targets      map[string]*legacyTargetConfig `yaml:"targets"`
		Projects     map[string]yaml.Node           `yaml:"projects"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	c.SkillPaths = raw.SkillPaths
	c.McpPaths = raw.McpPaths
	c.SkillSources = raw.SkillSources
	c.Mcps = raw.Mcps
	// 迁移可能向两个表写条目，而 YAML 缺键时 decode 出的是 nil map。
	if c.SkillPaths == nil {
		c.SkillPaths = map[string]*RawSkillPathConfig{}
	}
	if c.McpPaths == nil {
		c.McpPaths = map[string]*RawMcpPathConfig{}
	}

	for _, id := range sortedKeys(raw.Targets) {
		legacy := raw.Targets[id]
		if _, exists := c.SkillPaths[id]; !exists {
			c.SkillPaths[id] = &RawSkillPathConfig{Dir: legacy.SkillDir}
		}
		if legacy.Mcp.ConfigPath == nil {
			continue
		}
		if _, exists := c.McpPaths[id]; exists {
			continue
		}
		var configType *agents.McpConfigType
		if legacy.Mcp.ConfigType != nil {
			parsed, err := parseMcpConfigType(*legacy.Mcp.ConfigType)
			if err != nil {
				return err
			}
			configType = &parsed
		}
		c.McpPaths[id] = &RawMcpPathConfig{
			ConfigPath:   legacy.Mcp.ConfigPath,
			ConfigPrefix: legacy.Mcp.ConfigPrefix,
			ConfigType:   configType,
		}
	}
	return nil
}

// legacyTargetConfig：旧 targets 条目（2026-11-28 前的 schema），读入只为
// 迁移，见 RawManagerConfig.UnmarshalYAML。
type legacyTargetConfig struct {
	SkillDir string          `yaml:"skill_dir"`
	Mcp      legacyTargetMcp `yaml:"mcp"`
}

type legacyTargetMcp struct {
	ConfigPath   *string `yaml:"config_path"`
	ConfigPrefix *string `yaml:"config_prefix"`
	ConfigType   *string `yaml:"config_type"`
}

// RawSkillPathConfig：技能路径条目。
type RawSkillPathConfig struct {
	Dir string `yaml:"dir" json:"dir"`
}

// RawMcpPathConfig：MCP 路径条目。三个 Option 都不带 skip 属性，落盘时
// 未填写 null（与 serde 一致）。
type RawMcpPathConfig struct {
	ConfigPath   *string               `yaml:"config_path" json:"configPath"`
	ConfigPrefix *string               `yaml:"config_prefix" json:"configPrefix"`
	ConfigType   *agents.McpConfigType `yaml:"config_type" json:"configType"`
}

// UnmarshalYAML：raw 解析阶段即校验 config_type 取值（serde unknown
// variant 语义：坏值在这里报错，而不是等到 writer 分发时静默错认）。
func (c *RawMcpPathConfig) UnmarshalYAML(node *yaml.Node) error {
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

// RawSkillPathInput：技能路径增改入参（命令层散参组装）。
type RawSkillPathInput struct {
	Name string
	Dir  string
}

// RawMcpPathInput：MCP 路径增改入参（命令层散参组装）。
type RawMcpPathInput struct {
	Name         string
	ConfigPath   string
	ConfigPrefix string
	ConfigType   string
}

// ---------------------------------------------------------------------------
// Resolved*：解析后的内存工作形态（BTreeMap → map + 显式排序，见
// sortedKeys 的迭代口径）。
// ---------------------------------------------------------------------------

// ResolvedManagerConfig：解析完成的配置。
type ResolvedManagerConfig struct {
	SkillPaths   map[string]*ResolvedSkillPath
	McpPaths     map[string]*ResolvedMcpPath
	SkillSources map[string]*ResolvedSkillSourceDefinition
	Mcps         []*ResolvedMcpConfig
}

// ResolvedSkillPath：解析完成的技能路径。
type ResolvedSkillPath struct {
	Name string
	Dir  string
}

// ResolvedMcpPath：解析完成的 MCP 路径。新模型下 ConfigPath 恒非 nil
// （解析侧校验 config_path 必填）。
type ResolvedMcpPath struct {
	Name            string
	ConfigPath      string
	MCPConfigPrefix string
	MCPConfigType   agents.McpConfigType
	// SSEUnsupported：配置文件与声明了 SSE 不兼容的内置工具一致（解析时
	// 判定一次）；视图据此下发，前端禁用 SSE server 的格子。
	SSEUnsupported bool
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
// 但后者会让 config.yaml 与 Rust 版逐字节漂移。legacy 键不在结构体上，
// 天然永不写回。
func normalizeRawManagerConfig(config *RawManagerConfig) *RawManagerConfig {
	if config.SkillPaths == nil {
		config.SkillPaths = map[string]*RawSkillPathConfig{}
	}
	if config.McpPaths == nil {
		config.McpPaths = map[string]*RawMcpPathConfig{}
	}
	if config.SkillSources == nil {
		config.SkillSources = []*RawSkillSourceConfig{}
	}
	if config.Mcps == nil {
		config.Mcps = []*RawMcpConfig{}
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
