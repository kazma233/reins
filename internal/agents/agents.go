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
	"runtime"
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
	// McpConfigHermes：旧配置里的 hermes 类型（2026-11-28 曾按载体单独成
	// 型）。条目方言与 common 相同，只是载体文件是 YAML，解析时归一为
	// common，由文件扩展名决定序列化形态；不是独立方言，也不再下发。
	McpConfigHermes McpConfigType = "hermes"
)

// GlobalRoot：全局布局的路径根。Redirect.EnvVar 非空表示该工具官方支持
// 重定向（变量名与默认目录就是重定向知识的唯一登记处，GLOSSARY 环境重定向
// 词条）；Home 根各字段为零值。
type GlobalRoot struct {
	Name     string
	Redirect support.Redirect
}

// hermesDefaultDir：HERMES_HOME 未设置时的默认目录。POSIX 官方默认
// `~/.hermes`；Windows 官方默认 `%LOCALAPPDATA%\hermes`，标准环境下
// LOCALAPPDATA 恒为 HOME\AppData\Local，按 HOME 相对目录表达（Ref.md
// Hermes 节）。
func hermesDefaultDir() string {
	if runtime.GOOS == "windows" {
		return nativeDefaultDir("AppData/Local/hermes")
	}
	return ".hermes"
}

// nativeDefaultDir：把以 '/' 书写的多段默认目录按当前 OS 落原生分隔符。
// Resolve 对 DefaultDir 只做单次拼接、保留内嵌分隔符，Windows 上内嵌 '/'
// 会产出 `home\AppData/Local/hermes\skills` 这类混合形态并直接进展示层
// （预设回填、目录不存在提示）；比较路径虽会 canonicalize，展示不该靠它
// 兜底。单段目录（.grok 等）不受影响。
func nativeDefaultDir(dir string) string {
	if runtime.GOOS == "windows" {
		return strings.ReplaceAll(dir, "/", "\\")
	}
	return dir
}

var (
	RootHome            = GlobalRoot{Name: "home"}
	RootGrokHome        = GlobalRoot{Name: "grok_home", Redirect: support.Redirect{EnvVar: "GROK_HOME", DefaultDir: ".grok", Label: "grok"}}
	RootPiAgentDir      = GlobalRoot{Name: "pi_agent_dir", Redirect: support.Redirect{EnvVar: "PI_CODING_AGENT_DIR", DefaultDir: nativeDefaultDir(".pi/agent"), Label: "pi"}}
	RootCodexHome       = GlobalRoot{Name: "codex_home", Redirect: support.Redirect{EnvVar: "CODEX_HOME", DefaultDir: ".codex", Label: "codex"}}
	RootClaudeConfigDir = GlobalRoot{Name: "claude_config_dir", Redirect: support.Redirect{EnvVar: "CLAUDE_CONFIG_DIR", DefaultDir: ".claude", Label: "claude"}}
	RootDshHome         = GlobalRoot{Name: "dsh_home", Redirect: support.Redirect{EnvVar: "DSH_HOME", DefaultDir: ".dsh", Label: "dsh"}}
	RootHermesHome      = GlobalRoot{Name: "hermes_home", Redirect: support.Redirect{EnvVar: "HERMES_HOME", DefaultDir: hermesDefaultDir(), Label: "hermes"}}
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

// McpLayout：MCP 配置格式属性，层级无关。
type McpLayout struct {
	// Prefix：MCP 条目键前缀。dsh 的 MCP 配置是 Cordis patch 操作列表、
	// hermes 内嵌在 config.yaml 顶层 mcp_servers，都按各自结构定位条目，
	// 没有 configPrefix 概念（空串）。
	Prefix     string
	ConfigType McpConfigType
	// SSEUnsupported：官方契约——该工具的 MCP 配置不接受 legacy SSE
	// transport，写入会被工具拒连（当前仅 pi）。知识归清单，路径视图据此
	// 判定并下发给前端禁格，前端不按条目名猜。
	SSEUnsupported bool
}

// GlobalLayout：全局布局。路径默认相对 GlobalRoot 根；`~/` 前缀表示以 HOME
// 为基准、不随该根的重定向搬移（codex 的 skills、claude 的 .claude.json）。
type GlobalLayout struct {
	Root          GlobalRoot
	SkillDir      string
	McpConfigPath string // 空串 = 无 MCP 配置入口
}

// AgentSpec：一个被管理工具的静态描述。清单只读，查询返回的指针不得改写。
type AgentSpec struct {
	// Key：规范键：各域 id 与 workspace 预设名的公共形式（连字符小写）；
	// 读者只在前端生成物导出（RenderAgentLabelsTS），生产路径不按键查询。
	Key string
	// Label：产品名：跨层唯一一份展示文案。
	Label string
	// SourceApp：session 域 wire id 映射（wire 写法各自保持现状）；空串 = 无。
	// 同样只被前端生成物导出读取。
	SourceApp SourceApp
	// ProviderApp：providers 域键；空串 = 无。
	ProviderApp ProviderApp
	Mcp         McpLayout
	Global      GlobalLayout
}

// AgentSpecs：清单本体。顺序即 workspace 预设的下发顺序。
var AgentSpecs = []AgentSpec{
	{
		Key:         "codex",
		Label:       "Codex",
		SourceApp:   SourceCodex,
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
	},
	{
		Key:         "claude",
		Label:       "Claude Code",
		SourceApp:   SourceClaudeCode,
		ProviderApp: ProviderClaude,
		Mcp: McpLayout{
			Prefix:     "mcpServers",
			ConfigType: McpConfigCommon,
		},
		Global: GlobalLayout{
			// 三个落点都相对配置 home：实测（2.1.285）设置 CLAUDE_CONFIG_DIR 后
			// settings.json / .claude.json / projects / sessions 全在变量目录下，
			// ~/.claude 只是该变量的缺省值
			//（见 docs/decisions/2026-10-12-global-root-env-redirect.md）。
			Root:          RootClaudeConfigDir,
			SkillDir:      "skills",
			McpConfigPath: ".claude.json",
		},
	},
	{
		Key:         "opencode",
		Label:       "OpenCode",
		SourceApp:   SourceOpenCode,
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
	},
	{
		Key:         "zcode",
		Label:       "ZCode",
		SourceApp:   SourceZcode,
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
	},
	{
		Key:         "grokbuild",
		Label:       "Grok Build",
		SourceApp:   SourceGrokBuild,
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
	},
	{
		Key:         "pi",
		Label:       "Pi",
		SourceApp:   SourcePi,
		ProviderApp: ProviderPi,
		// pi ≥0.99 支持 MCP：配置在 <agentDir>/mcp.json 顶层 mcpServers，
		// 形状与其他 MCP client 一致；legacy SSE transport 不被接受。
		Mcp: McpLayout{
			Prefix:         "mcpServers",
			ConfigType:     McpConfigCommon,
			SSEUnsupported: true,
		},
		Global: GlobalLayout{
			Root:          RootPiAgentDir,
			SkillDir:      "skills",
			McpConfigPath: "mcp.json",
		},
	},
	{
		Key:         "dsh",
		Label:       "DeepSeek Harness",
		SourceApp:   SourceDsh,
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
	},
	{
		// Hermes（桌面版与 CLI 共用 HERMES_HOME）。非会话来源、非 providers
		// 应用，只做分发目标；契约见 Ref.md Hermes 节。MCP 条目方言与
		// common 相同（command/args/env），载体是 config.yaml（YAML 载体由
		// common 方言按扩展名支持），写入节点为顶层 mcp_servers。
		Key:   "hermes",
		Label: "HermesAgent",
		Mcp: McpLayout{
			Prefix:     "mcp_servers",
			ConfigType: McpConfigCommon,
		},
		Global: GlobalLayout{
			Root:          RootHermesHome,
			SkillDir:      "skills",
			McpConfigPath: "config.yaml",
		},
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
		// 清单全部工具都可通过 workspace 预设配置分发落点：前端「关于页工具
		// 名单」等从这里取，而不是自己再写一遍。
		fmt.Fprintf(&ids, "  \"%s\",\n", spec.Key)
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

// agent 的产品名集中维护：会话来源、workspace 预设、providers 的界面文案
// 都从这里取，避免同一个工具在不同界面出现 codex / Codex / claude 等不同写法。
export const AGENT_LABELS: Record<string, string> = {
%s};

// 可配置的 agent 清单与顺序（与后端 AgentSpecs 清单一致）：workspace 预设、
// 关于页工具名单等都从这里取，不再前端另维护一份拷贝。
export const AGENT_IDS = [
%s] as const;

// 跨域 id 映射（wire id → 规范键）：session 来源等 wire 写法与规范键不同时
// 显式列出；其余域 id 与规范键一致，不需要映射。
export const AGENT_ID_ALIASES: Record<string, string> = {
%s};
`, labels.String(), ids.String(), aliases.String())
}
