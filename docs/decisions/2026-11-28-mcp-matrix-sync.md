# MCP 分发改矩阵同步（对齐 skills 同步体验）

- 日期：2026-11-28
- 状态：生效中
- 来源：用户需求（逐路径点按钮太繁琐、状态不直观，希望 MCP 与 skills 一样以同步形式分发）

## 结论

### 1. 全局一个同步入口，格子矩阵弹窗

MCP tab 顶部新增「同步」入口，打开矩阵同步弹窗：行 = 登记的 MCP server，
列 = MCP 路径，每格独立勾选，行/列头提供批量勾选。不用 skills 弹窗的两列
交叉积形态——skills 的源是「待分发全集」，交叉积天然成立；MCP 的登记表是
菜单，每条路径装哪些 server 是各自挑选的，交叉积会把已装 server 传播进所有
勾选路径（默认预选下点同步即发生），语义错误。与 skills 的统一在交互骨架
（弹窗、聚合预演、结果就地更新、后台静默刷新），不在勾选控件形状。

### 2. 同步 = 期望态收敛（按列走）

同步把每条 MCP 路径收敛到该列勾选集合：勾选 → 写入/刷新；已装未勾选 →
移除；未装未勾选 → 不动。只收敛登记在册的 server 名（writer 的
entryKeyFor 口径，如 dsh 的清洗名），工具配置里用户手工添加的其他条目不碰
（与旧单格移除同一边界）。默认勾选 = 已装格子（present），因此默认动作 =
纯刷新，零传播零意外；移除通过取消勾选表达，由确认清单兜底。

### 3. 聚合预演 + 确认清单

点同步先做聚合预演（条目级）：将创建（missing 且勾选）与将移除（present 且
未勾选）弹确认清单；创建项携带序列化内容，与真实写入同一条构造路径
（writer 的 previewEntry——先确认文件可解析再展示条目，「写不进的文件不给
预览」契约不变），纯刷新（已装且勾选）不弹确认直接执行。error 路径（文件
读不出来）整列禁用不参与。执行串行逐格原子写、逐格收集成败（一处失败不
中断其余格子），结果就地更新（弹窗不关），后台静默刷新状态。

- SSE 防呆禁用结构化：旧实现按条目名猜（路径名恰为 `pi` 才识别），散装
  模型下自建别名路径即漏判。SSE 兼容性是工具对配置文件的契约（pi 的
  mcp.json 不接受 legacy SSE transport），知识归 `internal/agents` 清单
  （AgentSpec 的 Mcp.SSEUnsupported），MCP 路径视图按配置文件一致判定后
  下发 `sseUnsupported`，前端按成员引用消费、不再感知工具名。
- 条目构造失败的创建项不进确认清单：预演时构造失败的格子单独以
  `failedCreates` 通道下发（只告知不参与执行），确认执行集合剔除这些格子
  ——「看到的就是将要发生的」严格成立；预演与执行之间盘面漂移的重试路径
  不保留（用户可重新打开弹窗再同步）。

### 4. 卡片收敛为展示面

McpCard 移除逐路径启停按钮与「同步到路径 (N)」按钮，只留每条路径的状态
pill（不可点）+ 修改/删除；所有装卸收敛到同步弹窗。

### 5. 命令面收口

- 新增 `preview_mcp_sync`（聚合预演）与 `sync_mcps_to_paths`（矩阵同步）。
- `apply_mcp_to_path` / `remove_mcp_from_path` / `preview_mcp_path` 命令
  删除：弹窗与改名都不再逐路径调用，条目内容预览并入聚合预演返回；不留
  死接口。writer 契约的四个操作入口（read/preview/apply/remove）不变，
  同步编排经同一分发点调用。
- `update_workspace_mcp` 升级：改名时先对已装路径做手术（apply 新名 + remove
  旧名，以盘上旧名条目为准、与巡检同口径），成功后才写登记——路径手术
  中途失败时登记保持旧名，整个动作可重试；内容变更（不改名）不再自动
  传播，由同步弹窗刷新。
- 矩阵同步整段在 store 锁内：登记视图与各路径写入派生自同一份配置快照
  （与删除 MCP 同模式），并发命令被挡在串行写入之外。

### 6. 本期不做

- 内容漂移检测（巡检反读条目与登记比对）：反读归一层缺位
  （docs/context/2026-09-15-mcp-config-mapping.md 的字段口径静默失败清单
  未解决），字段级对比会误报；等字段映射适配器落地后再加。已装格子重写
  以登记为权威，覆盖手改（与旧「同步到路径」同口径）。
- 外部 MCP 源（mcp.json 之类）：MCP 是结构化配置且各工具格式不同，没有
  「目录源」的天然对应物，引入会凭空多出源格式契约。
- 泛型同步引擎 / 通用 SyncDialog 组件：两域数据模型与写入机制本质不同
  （symlink 先清后装 vs 配置文件手术），先复刻交互骨架，出现真重复再提炼。

## 影响面

- `internal/workspace`：新增 mcp_sync.go（预演 + 同步编排）；UpdateWorkspaceMCPInner
  改名收敛；service.go 命令面增删；types.go DTO 增删（McpSyncInput/
  McpSyncPreview/McpSyncResult，删 McpPathPreviewResult）。
- 前端 workspace 域：新增 McpSyncDialog + useMcpSync + mcpSync（格子矩阵
  纯逻辑）；McpCard 精简；McpTargetButton.vue 与 ProjectMcpTargetButton.vue
  删除；useMcpMutations 收缩为创建/编辑（含改名确认）/删除；api.ts 增删。
- GLOSSARY：新增「MCP 同步」词条。
- 验收：go test ./internal/...（新增预演/收敛/失败收集/改名收敛用例）+
  pnpm check + pnpm test + task build（bindings 重生成）。
