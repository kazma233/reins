# reins

reins 是管理多个 AI CLI 编码工具的桌面应用：浏览历史会话、统计 token 用量、配置聚合平台模型、向各工具分发 MCP 与 skills。术语按域分组，中文名为准。

## 语言

### 通用

**工具清单（agents / AgentSpec）**：
reins 管理的全部 AI 编码工具的静态描述表：规范键、产品名、跨域 id 映射、全局布局；是唯一的单一来源，各域的 defaults 表与文案从中派生。
_Avoid_: agent 列表、target 表（指派生出的副本）

### 会话（session 域）

**来源（Source / SourceApp）**：
reins 读取会话数据的一个 CLI 工具（Codex、Claude Code、OpenCode、Pi、Grok Build、ZCode、DSH）。
_Avoid_: 工具、app、backend

**来源注册表（source registry）**：
全部来源静态描述的单张表，一个来源一条；来源的探测、缓存清理、用量采集都由它驱动，新增来源即新增一条。
_Avoid_: 分发表、来源清单（指迭代结果时另说）

**读取器（reader）**：
一个来源的会话数据解析实现，每个来源恰好一个。
_Avoid_: backend

**读取器引擎（reader engine）**：
family 形态读取器共享的机械底座：缓存生命周期（目录索引、双半时间线、三级摘要）、失效判定、marker 注入、排序分页、clear 编排；读取器只声明真差异，典型来源零覆写。
_Avoid_: harness（域内该词专指 DSH/DeepSeek Harness 这类被管理的 agent 运行时）、runtime

**会话目录（catalog）**：
一个来源的会话文件枚举结果与缓存，列表分页的数据源。
_Avoid_: 索引（易与会话族索引混淆）

**会话族（family）**：
同一逻辑会话的全部转录文件（root、resume 段、子代理线程），列表与时间线以族为单位聚合。
_Avoid_: 会话组

**孤儿策略（orphan policy）**：
父链分组的族根上溯在父节点缺失时的处置语义：认领缺失父 id 自成一家（OpenCode/ZCode）、保留最后已知 id 挂靠存活祖先（Codex）；DSH 走 catalog 声明的边集、链断即隐藏，不经共享部件。由 family_index 的 `OrphanPolicy` 显式声明，读取器不得静默统一。
_Avoid_: 兜底分组

**会话身份（session identity）**：
跨层寻址一个会话的唯一二元组（来源, source_session_id）；transcript_path 只是展示字段，不参与寻址、缓存键或前端主键。
_Avoid_: 路径主键、transcript key

### 工作区（workspace 域）

**技能路径（skill path）**：
接受 skills 分发的一个命名目录条目：技能以目录链接装入，同步、冲突预演与卸载的物理语义都挂在这条路径上。skills 与 MCP 各自独立配置，互不引用。
_Avoid_: 目标、target（已废弃的旧概念）、容器

**MCP 路径（MCP path）**：
接受 MCP 分发的一份命名条目：MCP 配置文件路径 + 格式 writer + 前缀。与技能路径互不约束。
_Avoid_: 目标、target（已废弃的旧概念）

**目标（target）**：
已废弃。2026-11-28 前接受 skills 与 MCP 分发的一份工具配置（分全局与项目两级、二者捆绑寻址），由技能路径与 MCP 路径替代；见 [技能路径决策](docs/decisions/2026-11-28-skill-and-mcp-paths.md)。
_Avoid_: 在新代码与文档里继续使用

**环境重定向（env redirect）**：
工具官方支持的全局目录重定位（如 `CODEX_HOME`、`CLAUDE_CONFIG_DIR`、`GROK_HOME`、`PI_CODING_AGENT_DIR`、`DSH_HOME`）。认哪个变量、默认目录是什么由 `internal/agents` 清单的 `GlobalRoot` 持有，解析统一走 `internal/support` 的 `Environment.Resolve`；不随重定向搬移的落点在清单里以 `~/` 前缀显式标注。域内自己再拼一份 `~/.codex` 之类的路径视为重复定义。
_Avoid_: 路径助手（指各域自带的路径拼接函数）

**安装面（install surface）**：
「把一个 skill 装进一个技能路径」的物理语义：路径是否参与分发、目录链接落在哪、落点当前是什么（未安装 / 已指向本 skill / 指向同来源别的 skill / 指向别处 / 真实文件目录）、装与卸的结果。安装、冲突预演、链接枚举、按来源卸载四条路径都经 `internal/workspace/skill_install.go`，判定只此一份。
_Avoid_: 链接工具函数（指各调用方自带的路径比较与 skip 判断）

**启停（enable/disable）**：
已废弃的概念。技能路径、MCP 路径与 MCP 条目都没有开关：配置在盘上就是生效的，不需要分发就不写进该路径的配置；旧 config.yaml 里的 `enabled` 键被忽略且不再写回。
_Avoid_: 停用路径、禁用 MCP（指旧版本里的开关语义）

**别名路径（alias skill dir）**：
某个技能路径的目录本身是指向别处的软链接。此时该条目不拥有那个目录：内容由链接另一端负责，它不参与技能分发，界面也不给勾选，只标注「内容属谁」与「文件实际在哪」。
_Avoid_: 继承（只在需要强调来源时使用）、写穿（这是个动作描述，不是状态）

**跳过（skip）**：
「这条技能路径不参与分发」的领域判定，只有一处（寻址层）。它回答原因（条目不存在 / 目录整层是链接），调用方只决定表现方式：报错、提示或静默。
_Avoid_: 禁用（已废弃的启停概念）、忽略

**技能源（skill source）**：
workspace 管理的 skill 来源（git 或本地目录），负责发现、同步、导入与分发。
_Avoid_: skill 仓库

**MCP 格式 writer（MCP format writer）**：
一种目标工具 MCP 配置格式的完整知识（条目形态、支持的文件格式、前缀约束、读写与预览）；mcps 域的编排只经唯一分发点调用 writer，不再按工具类型写特例。
_Avoid_: config type 分发（指旧的散落 match 形态）

**MCP 同步（MCP sync）**：
把登记的 MCP server 批量收敛进各 MCP 路径的单一动作：格子矩阵（行 = server、列 = 路径）勾出期望态，同步 = 每条路径收敛到该列勾选集合（勾选写入/刷新、已装未勾移除），只动登记在册的 server 名；写前聚合预演，结果逐格收集。
_Avoid_: 应用（apply，指旧的逐路径写入动作）、分发目标、启停

### 模型配置（providers 域）

**聚合平台（provider）**：
除 OpenAI、Anthropic 官方外的一切模型平台与自建网关，由 reins 管理端点、协议、模型与 API Key。
_Avoid_: 供应商、平台（单独使用太泛）

**方言（dialect）**：
一个归一化枚举（协议、思考档位）在某工具配置文件里的实际拼写知识：正向写入、反向识别（含只读别名与有损折叠）由同一张表派生，各适配器经 `AppAdapter` 声明（Pi 与 dsh 共用 pi-ai 方言），roundtrip 测试守卫正反互逆。
_Avoid_: 映射函数（指散落在适配器里的正反函数对）
