// Package agents：reins 管理的全部 AI 编码工具的静态清单（AgentSpec）。一级
// 包而非业务域：session / workspace / providers 的 defaults 表、模板与文案都
// 从这里派生；本包只收静态知识，不吸收任何行为。
//
// 跨域 id 枚举（SourceApp、ProviderApp、McpConfigType）当前寄居于此：Rust
// 版分属 session/providers/workspace 各域、agents.rs 仅引用；对应业务域在
// 后续 Phase 落地时应把类型迁回各自包（Go 侧暂无域包可挂，先与清单同住）。
package agents

import (
	"fmt"
	"strings"

	"reins/internal/support"
)

// SourceApp：session 域 wire id（wire 写法各自保持现状，如 claude_code）。
type SourceApp string

const (
	SourceCodex      SourceApp = "codex"
	SourceClaudeCode SourceApp = "claude_code"
	SourceOpenCode   SourceApp = "opencode"
	SourcePi         SourceApp = "pi"
	SourceGrokBuild  SourceApp = "grokbuild"
	SourceZcode      SourceApp = "zcode"
	SourceDsh        SourceApp = "dsh"
)

// ProviderApp：providers 域键（Rust ProviderAppId 变体的稳定拼写）。
type ProviderApp string

const (
	ProviderCodex     ProviderApp = "codex"
	ProviderClaude    ProviderApp = "claude"
	ProviderOpencode  ProviderApp = "opencode"
	ProviderPi        ProviderApp = "pi"
	ProviderGrokbuild ProviderApp = "grokbuild"
	ProviderDsh       ProviderApp = "dsh"
	ProviderZcode     ProviderApp = "zcode"
)

// ProviderApps：providers/types.rs 的 PROVIDER_APPS 原序拷贝。产品名唯一
// 来源是 AgentSpecs 清单（Rust label() 派生自 spec），这里只保序枚举键。
func ProviderApps() []ProviderApp {
	return []ProviderApp{
		ProviderCodex,
		ProviderClaude,
		ProviderOpencode,
		ProviderPi,
		ProviderGrokbuild,
		ProviderDsh,
		ProviderZcode,
	}
}

// McpConfigType：MCP 配置格式（workspace/types.rs，snake_case 拼写）。
type McpConfigType string

const (
	McpConfigCommon    McpConfigType = "common"
	McpConfigOpenCode  McpConfigType = "opencode"
	McpConfigGrokBuild McpConfigType = "grokbuild"
	McpConfigDsh       McpConfigType = "dsh"
)

// TemplateEntry：默认 config.yaml 模板里该工具的条目形态。
type TemplateEntry uint8

const (
	// TemplateStandard：写全 skill_dir 与 MCP 路径（~ 缩写；pi 在 env 重定向
	// 时固化为绝对路径）。mcp.enabled 键现状只有部分条目携带（pi 段没有），
	// 如实区分。
	TemplateStandard TemplateEntry = iota
	// TemplateFollowEnv：留空 skill_dir 与 MCP 路径：解析时回落 builtin
	// defaults，跟随 GROK_HOME 重定向，避免模板固化错误路径（grokbuild）。
	TemplateFollowEnv
	// TemplateAbsent：不出现在默认模板（dsh）。
	TemplateAbsent
)

// GlobalRoot：全局布局的路径根。Redirect.EnvVar 非空表示该工具官方支持
// 重定向（变量名与默认目录就是重定向知识的唯一登记处，GLOSSARY 环境重定向
// 词条）；Home 根各字段为零值。
type GlobalRoot struct {
	Name     string
	Redirect support.Redirect
}

var (
	RootHome            = GlobalRoot{Name: "home"}
	RootGrokHome        = GlobalRoot{Name: "grok_home", Redirect: support.Redirect{EnvVar: "GROK_HOME", DefaultDir: ".grok", Label: "grok"}}
	RootPiAgentDir      = GlobalRoot{Name: "pi_agent_dir", Redirect: support.Redirect{EnvVar: "PI_CODING_AGENT_DIR", DefaultDir: ".pi/agent", Label: "pi"}}
	RootCodexHome       = GlobalRoot{Name: "codex_home", Redirect: support.Redirect{EnvVar: "CODEX_HOME", DefaultDir: ".codex", Label: "codex"}}
	RootClaudeConfigDir = GlobalRoot{Name: "claude_config_dir", Redirect: support.Redirect{EnvVar: "CLAUDE_CONFIG_DIR", DefaultDir: ".claude", Label: "claude"}}
	RootDshHome         = GlobalRoot{Name: "dsh_home", Redirect: support.Redirect{EnvVar: "DSH_HOME", DefaultDir: ".dsh", Label: "dsh"}}
)

// Redirects 返回清单登记的全部重定向描述（按 EnvVar 去重），供
// support.FromEnv 驱动进程环境读取。带重定向的根有增删时自动跟上，
// 无需另行登记变量表。
func Redirects() []support.Redirect {
	seen := make(map[string]bool)
	var redirects []support.Redirect
	for i := range AgentSpecs {
		root := AgentSpecs[i].Global.Root
		if !root.HasRedirect() || seen[root.Redirect.EnvVar] {
			continue
		}
		seen[root.Redirect.EnvVar] = true
		redirects = append(redirects, root.Redirect)
	}
	return redirects
}

// HasRedirect：该根是否对应官方重定向（模板渲染与路径解析共用的判据）。
func (r GlobalRoot) HasRedirect() bool { return r.Redirect.EnvVar != "" }

// Resolve 解析为实际根目录：重定向优先，HOME 兜底。home 走 Environment 里的
// UserHomeDir 口径，测试的 home 覆盖才能生效。
func (r GlobalRoot) Resolve(env *support.Environment) (string, bool) {
	if r.HasRedirect() {
		return env.Resolve(r.Redirect)
	}
	return env.Home, env.Home != ""
}

// ResolvePath：清单路径解析。`~/` 前缀以 HOME 为基准，其余以本 root 为基准。
// 同一工具的两种基准都合法：codex 的 skills 目录相对 HOME 解析
// （`~/.agents/skills`），只有 config.toml 随 CODEX_HOME 重定向。
func (r GlobalRoot) ResolvePath(env *support.Environment, path string) (string, bool) {
	root, rootOK := r.Resolve(env)
	return support.ManifestPath(env, root, rootOK, path)
}

// HomePrefix：未重定向时的 ~ 缩写前缀（默认模板路径与注释文案用）。
func (r GlobalRoot) HomePrefix() string {
	if !r.HasRedirect() {
		return "~"
	}
	return "~/" + r.Redirect.DefaultDir
}

// McpLayout：MCP 配置格式属性，层级无关：同一工具全局与项目的写入格式一致。
type McpLayout struct {
	// Prefix：MCP 条目键前缀。dsh 的 MCP 配置是 Cordis patch 操作列表，按
	// name+serverName 定位条目，没有 configPrefix 概念（空串）。
	Prefix     string
	ConfigType McpConfigType
}

// GlobalLayout：全局布局。路径默认相对 GlobalRoot 根；`~/` 前缀表示以 HOME
// 为基准、不随该根的重定向搬移（codex 的 skills、claude 的 .claude.json）。
type GlobalLayout struct {
	Root          GlobalRoot
	SkillDir      string
	McpConfigPath string // 空串 = 无 MCP 配置入口
}

// ProjectLayout：项目级布局，路径相对项目根。全局与项目非同构，如实表达
// （claude 的 MCP 配置在项目根 .mcp.json、opencode 在项目根 opencode.json、
// dsh 项目级无 MCP 配置入口等）。
type ProjectLayout struct {
	SkillDir      string
	McpConfigPath string // 空串 = 无
}

// AgentSpec：一个被管理工具的静态描述。清单只读，查询返回的指针不得改写。
type AgentSpec struct {
	// Key：规范键：各域 id 的公共形式（与 AgentTargetId 一致的连字符小写）；
	// 读者只在前端生成物导出（RenderAgentLabelsTS），生产路径不按键查询。
	Key string
	// Label：产品名：跨层唯一一份展示文案。
	Label string
	// SourceApp：session 域 wire id 映射（wire 写法各自保持现状）；空串 =
	// 无。同样只被前端生成物导出读取。
	SourceApp SourceApp
	// TargetID：workspace 域 AgentTargetId；空串 = 无 workspace 入口。
	TargetID string
	// ProviderApp：providers 域键；空串 = 无。
	ProviderApp ProviderApp
	Mcp         McpLayout
	Global      GlobalLayout
	// Project：项目级布局；nil = 无项目级入口。
	Project  *ProjectLayout
	Template TemplateEntry
}

// AgentSpecs：清单本体。顺序即默认 config.yaml 模板的条目顺序；不在模板的
// dsh 殿后。
var AgentSpecs = []AgentSpec{
	{
		Key:         "codex",
		Label:       "Codex",
		SourceApp:   SourceCodex,
		TargetID:    "codex",
		ProviderApp: ProviderCodex,
		Mcp: McpLayout{
			Prefix:     "mcp_servers",
			ConfigType: McpConfigCommon,
		},
		Global: GlobalLayout{
			Root: RootCodexHome,
			// skills 不在 CODEX_HOME 内：codex 的 skills 目录相对 HOME 解析
			// （`.agents/skills`），只有 config.toml 随 CODEX_HOME 重定向。
			SkillDir:      "~/.agents/skills",
			McpConfigPath: "config.toml",
		},
		Project: &ProjectLayout{
			SkillDir:      ".agents/skills",
			McpConfigPath: ".codex/config.toml",
		},
		Template: TemplateStandard,
	},
	{
		Key:         "claude",
		Label:       "Claude Code",
		SourceApp:   SourceClaudeCode,
		TargetID:    "claude",
		ProviderApp: ProviderClaude,
		Mcp: McpLayout{
			Prefix:     "mcpServers",
			ConfigType: McpConfigCommon,
		},
		Global: GlobalLayout{
			// 三个落点都相对配置 home：实测（2.1.285）设置 CLAUDE_CONFIG_DIR 后
			// settings.json / .claude.json / projects / sessions 全在变量目录下，
			// ~/.claude 只是该变量的缺省值
			//（见 aidocs/decisions/2026-10-12-global-root-env-redirect.md）。
			Root:          RootClaudeConfigDir,
			SkillDir:      "skills",
			McpConfigPath: ".claude.json",
		},
		// Claude 的项目级 MCP 配置按官方约定在项目根 .mcp.json。
		Project: &ProjectLayout{
			SkillDir:      ".claude/skills",
			McpConfigPath: ".mcp.json",
		},
		Template: TemplateStandard,
	},
	{
		Key:         "opencode",
		Label:       "OpenCode",
		SourceApp:   SourceOpenCode,
		TargetID:    "opencode",
		ProviderApp: ProviderOpencode,
		// opencode v2 的 mcp 配置在 mcp.servers 下，顶层 mcp 不被识别。
		Mcp: McpLayout{
			Prefix:     "mcp.servers",
			ConfigType: McpConfigOpenCode,
		},
		Global: GlobalLayout{
			Root:          RootHome,
			SkillDir:      ".config/opencode/skills",
			McpConfigPath: ".config/opencode/opencode.json",
		},
		Project: &ProjectLayout{
			SkillDir:      ".opencode/skills",
			McpConfigPath: "opencode.json",
		},
		Template: TemplateStandard,
	},
	{
		Key:         "zcode",
		Label:       "ZCode",
		SourceApp:   SourceZcode,
		TargetID:    "zcode",
		ProviderApp: ProviderZcode,
		Mcp: McpLayout{
			Prefix:     "mcp.servers",
			ConfigType: McpConfigCommon,
		},
		Global: GlobalLayout{
			Root:          RootHome,
			SkillDir:      ".zcode/skills",
			McpConfigPath: ".zcode/cli/config.json",
		},
		Project: &ProjectLayout{
			SkillDir:      ".zcode/skills",
			McpConfigPath: ".zcode/config.json",
		},
		Template: TemplateStandard,
	},
	{
		Key:         "grokbuild",
		Label:       "Grok Build",
		SourceApp:   SourceGrokBuild,
		TargetID:    "grokbuild",
		ProviderApp: ProviderGrokbuild,
		Mcp: McpLayout{
			Prefix:     "mcp_servers",
			ConfigType: McpConfigGrokBuild,
		},
		Global: GlobalLayout{
			Root:          RootGrokHome,
			SkillDir:      "skills",
			McpConfigPath: "config.toml",
		},
		Project: &ProjectLayout{
			SkillDir:      ".grok/skills",
			McpConfigPath: ".grok/config.toml",
		},
		Template: TemplateFollowEnv,
	},
	{
		Key:         "pi",
		Label:       "Pi",
		SourceApp:   SourcePi,
		TargetID:    "pi",
		ProviderApp: ProviderPi,
		// pi ≥0.99 支持 MCP：配置在 <agentDir>/mcp.json 顶层 mcpServers，
		// 形状与其他 MCP client 一致；legacy SSE transport 不被接受。
		Mcp: McpLayout{
			Prefix:     "mcpServers",
			ConfigType: McpConfigCommon,
		},
		Global: GlobalLayout{
			Root:          RootPiAgentDir,
			SkillDir:      "skills",
			McpConfigPath: "mcp.json",
		},
		// 项目级 mcp.json 仅在项目被 pi trust 后生效；写入配置本身无害。
		Project: &ProjectLayout{
			SkillDir:      ".pi/skills",
			McpConfigPath: ".pi/mcp.json",
		},
		Template: TemplateStandard,
	},
	{
		Key:         "dsh",
		Label:       "DeepSeek Harness",
		SourceApp:   SourceDsh,
		TargetID:    "dsh",
		ProviderApp: ProviderDsh,
		// dsh 的 MCP 配置是 Cordis patch 操作列表，按 name+serverName
		// 定位条目，没有 configPrefix 概念。
		Mcp: McpLayout{
			Prefix:     "",
			ConfigType: McpConfigDsh,
		},
		Global: GlobalLayout{
			Root:          RootDshHome,
			SkillDir:      "skills",
			McpConfigPath: "cordis.patch.yml",
		},
		// dsh 项目层只读 skills（项目 rank 优先于用户级）；MCP 走全局
		// Cordis patch，项目级没有配置文件，是“仅 skill 分发”形态。
		Project: &ProjectLayout{
			SkillDir:      ".dsh/skills",
			McpConfigPath: "",
		},
		Template: TemplateAbsent,
	},
}

// SpecByProviderApp：providers 域键 → AgentSpec；缺失返回 nil。
func SpecByProviderApp(app ProviderApp) *AgentSpec {
	for i := range AgentSpecs {
		if AgentSpecs[i].ProviderApp == app {
			return &AgentSpecs[i]
		}
	}
	return nil
}

// SpecBySourceApp：session 域 wire id → AgentSpec；not-found 错误主语等产品
// 名从这里派生，与界面文案保持同一份拼写。缺失返回 nil。
func SpecBySourceApp(app SourceApp) *AgentSpec {
	for i := range AgentSpecs {
		if AgentSpecs[i].SourceApp == app {
			return &AgentSpecs[i]
		}
	}
	return nil
}

// RenderAgentLabelsTS 序列化 AgentSpecs 为前端常量文件
// （src/shared/lib/agent-labels.ts）。文件路径保持不变是为了 AGENTS.md 与
// 前端消费点的既有引用继续准确。输出必须与前端现有内容逐字节一致，
// 由导出测试守卫（等价 Rust export_bindings_agent_labels + pnpm codegen）。
func RenderAgentLabelsTS() string {
	var labels, aliases, ids strings.Builder
	for i := range AgentSpecs {
		spec := &AgentSpecs[i]
		fmt.Fprintf(&labels, "  %s: \"%s\",\n", spec.Key, spec.Label)
		// 有 target 入口的才是 workspace 里可配置的 agent：前端「启用的 agents」
		// 选择器要用这份清单，而不是自己再写一遍。
		if spec.TargetID != "" {
			fmt.Fprintf(&ids, "  \"%s\",\n", spec.TargetID)
		}
		if spec.SourceApp != "" {
			wire := string(spec.SourceApp)
			if wire != spec.Key {
				fmt.Fprintf(&aliases, "  %s: \"%s\",\n", wire, spec.Key)
			}
		}
	}
	// 头两行的触发说明与前端文件逐字节一致：改文案要先同步改这里与前端
	// 文件（同一提交内）。
	return fmt.Sprintf(`// 自动生成：go test ./internal/agents 触发。
// 来源：internal/agents 的 AgentSpecs 清单（工具静态清单单一来源）；请勿手改。

// agent 的产品名集中维护：会话来源、workspace target、providers 的界面文案
// 都从这里取，避免同一个工具在不同界面出现 codex / Codex / claude 等不同写法。
export const AGENT_LABELS: Record<string, string> = {
%s};

// 可配置的 agent 清单与顺序（与后端 AgentSpecs 清单一致）：项目成员的候选、目标
// 选择器等都从这里取，不再前端另维护一份拷贝。
export const AGENT_IDS = [
%s] as const;

// 跨域 id 映射（wire id → 规范键）：session 来源等 wire 写法与规范键不同时
// 显式列出；其余域 id 与规范键一致，不需要映射。
export const AGENT_ID_ALIASES: Record<string, string> = {
%s};
`, labels.String(), ids.String(), aliases.String())
}
