# workspace 域重构：target 废弃，技能路径与 MCP 路径散装化

- 日期：2026-11-28
- 状态：生效中
- 来源：用户需求（target 支持 Hermes；skills 可引用到任意目录；skills 与 MCP 在 target 里相互约束不好用）

## 结论

### 1. target 概念整体废弃，拆成两组命名条目

workspace 域不再有「target」「全局/项目两级寻址」「`project:agent` 复合地址」。
接受 skills 与 MCP 分发的落点改为两组互不约束的命名条目：

- **技能路径（skill path）**：一个目录，技能以目录链接装入（物理语义与原
  target 的 skill_dir 完全一致）。
- **MCP 路径（MCP path）**：一份 MCP 配置文件的写入位置（配置文件路径 +
  格式 writer + 前缀），语义与原 target 的 mcp 段完全一致。

同一工具可以只配技能路径、只配 MCP 路径、或都配；两组条目各自增删改，
互不引用。config.yaml 顶层键改为 `skill_paths:` 与 `mcp_paths:`（条目名即
键，用户可自由命名，非空且同一组内唯一）。

### 2. 项目级分发移除

workspace 域的项目注册表（`projects:` 配置、项目级 agent 布局、项目 CRUD、
项目级 UI）全部移除；`AgentSpec.Project` 布局声明随之删除。想把技能装进某
个项目的 skills 目录，加一条指向该目录的技能路径即可。已装到项目里的存量
目录链接留在盘上不动（它们对工具仍然有效），Reins 不做清理。

### 3. 存量迁移（读时迁移，写时收敛）

旧 `targets:` 的全局条目在解析阶段自动拆成 `skill_paths:` + `mcp_paths:`
各一条（名字沿用 target id，路径不变——已装盘上的链接与已写入的 MCP 条目
按路径判定无缝接管）；只有 config_path 的 MCP 段才迁移出 MCP 路径。
旧 `projects:` 条目直接丢弃。迁移在 RawManagerConfig 的 UnmarshalYAML 完成：
读任意旧配置都得到迁移后的视图，文件本身在下次写盘时收敛为新 schema
（legacy 键永不写回）。空的 skill_dir 仍按名字回落内置 defaults（grokbuild
跟随 GROK_HOME 的既有用法不破坏）。

### 4. 新装空开始，预设是唯一便捷入口

默认 config.yaml 模板不再预置任何工具条目（只有空骨架与提示注释）；
`AgentSpec.TemplateEntry` 与逐工具模板渲染随之删除。前端创建弹窗从后端
预设接口取预填值（技能路径：目录；MCP 路径：配置文件路径 + 前缀 + 格式），
预填值是解析后的真实路径；用户也可完全自定义。

### 5. 删除条目 = 预演清理 + 确认执行

删除技能路径时可一并卸载该目录里指向**任意受管技能源**的目录链接（先预演
清单再确认）；删除 MCP 路径时可一并移除该配置文件里名字与 Reins 已配置
server 相同的条目。不做清理时只删登记，落盘内容原样保留。

### 6. 别名（目录链接）判定统一

原规则只对项目级 target 生效（skill_dir 整层是链接则跳过分发）；散装化后
统一到所有技能路径条目：条目目录本身是目录链接时，该条目不参与安装与清理，
界面只标注「内容属谁」与「文件实际在哪」。链接指向另一个受管条目目录时给
出归属 id 的既有展示语义保留。

### 7. Hermes 接入（仅全局，全官方契约）

- spec key `hermes`，显示名 `HermesAgent`；非会话来源、非 providers 应用。
- 全局根 `HERMES_HOME`（官方环境变量）：未设置时 POSIX 默认 `~/.hermes`，
  Windows 默认 `%LOCALAPPDATA%\hermes`——后者按标准环境等价于 HOME 下的
  `AppData/Local/hermes`，清单按 GOOS 给出 DefaultDir。
- 技能路径预设 `<hermes_home>/skills`：Hermes 扫描为递归无深度限制 +
  `followlinks=True`（本机安装源码 `agent/skill_utils.py` 的
  `iter_skill_index_files` 实证），Reins 的目录链接分发方式原生兼容；
  分类目录只是路径派生的展示字段，平铺安装合法。
- MCP 路径预设 `<hermes_home>/config.yaml`：**条目方言并入 common**
  （`command/args/env` / `url` + headers，与 JSON 形态同语义），只是载体是
  YAML——文件格式不是配置格式的区分依据。common 方言按扩展名支持
  JSON/TOML/YAML 三种载体，YAML 载体走 yaml.Node 手术编辑（保留注释与
  无关键），条目无 `type` 判别字段、空集合省略；写入节点为顶层
  `mcp_servers`（config_prefix）。旧配置里的 `config_type: hermes` 在解析
  时归一为 common。
- 与 Hermes bundled 技能同 frontmatter `name` 且内容不同的技能，Hermes
  按「Ambiguous skill name」拒收裸名（分类目录不构成命名空间）；该冲突
  属于工具侧行为，Reins 冲突预演不模拟，Ref.md 备注口径。
- 会话浏览与用量、providers 模型应用均不接入 Hermes。

### 8. WorkBuddy 与豆包工作已评估、本期不做

腾讯 WorkBuddy 桌面版（`~/.workbuddy/mcp.json` 官方文档化，skills 目录仅
第三方一致证据）证据中等；豆包工作桌面版 MCP/skills 落点零公开契约。按
「没有真实契约支持时不加 fallback」两者均不接入，调研结论不另行留档。

## 影响面

- `internal/agents`：去 `TargetID`/`Project`/`Template`；新增 Hermes 条目、
  `McpConfigHermes`、`RootHermesHome`；前端生成物 `AGENT_IDS` 改由清单键派生。
- `internal/workspace`：寻址层改路径条目；targets/projects CRUD 与 presets
  重写为两组路径 CRUD + 预设；同步/冲突/卸载链路改路径入参；新增 hermes
  writer；inspect/config 模板/迁移随之调整；service 命令面更换（含新增
  `get_mcp_format_infos`：创建弹窗的格式说明与形态示例下发，示例与真实
  写入走同一构造路径，前缀取该格式在清单里首个工具的约定值）。
- 前端 workspace 域：三 tab（targets/skills/mcp）改两 tab（skills/mcp）；
  技能路径管理并入 Skills tab、MCP 路径管理并入 MCP tab；项目相关组件删除。
- GLOSSARY：`目标（target）`词条废弃，新增技能路径/MCP 路径词条。
- 验收：单测 + 夹具 + `HERMES_HOME` 指向临时目录的沙箱端到端写入验证；
  Hermes 真机识别由用户人工确认，未做真机端到端验证。
