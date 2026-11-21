# 后端从 Rust/Tauri v2 迁移到 Go/Wails v3

- 日期：2026-11-19
- 状态：生效中
- 来源：用户决策（迁移必要性不在本文论证范围）

## 结论

桌面壳从 Tauri v2（Rust 后端）整体替换为 Wails v3.1.0（Go 后端），前端 Vue 3 + TS 保持不变。
`src-tauri/` 删除；Go 模块在仓库根（`module reins`），业务域按既有布局映射到
`internal/{session,workspace,providers}`，共享底座在 `internal/{support,agents,logger,testsupport}`。

架构映射：

| Tauri 侧 | Wails 侧 |
|---|---|
| `#[tauri::command]` ×55 | 三域 Service 方法 ×56（`internal/<域>/service.go` 薄壳，`*Inner` 保持业务归属） |
| `tauri::State` ×5 | service 注入 struct（main.go 构造） |
| `rfd` 同步对话框 | `workspace.DialogPort` 接口 + main 包 `wailsDialogPort` 适配 `app.Dialog` |
| `spawn_blocking` | 无等价物需要——绑定调用跑在独立 goroutine |
| `CancelCheck`/`Cancelled` | `context.Context` + `context.Canceled`（含 `exec.CommandContext` 取消即 kill） |
| ts-rs → `features/*/generated/` | `wails3 generate bindings` → `frontend/bindings/`（类型层已收口，见下方「DTO 与绑定的收口」） |
| tauri.conf.json | `build/config.yml` + Taskfile；窗口 1200×800/min 同值契约不变 |

存储与环境契约不变：应用数据目录 `reins`、`~/.reins/*_v{N}.db` 缓存版本文件名、
`reins-` 前缀条目、5 个 env redirect、git cache `reins-last-fetch` marker 全部逐字保留。

## 关键取舍

### 测试即规格，逐域等价移植

Rust 402 个测试逐文件平移为 Go 测试（Rust 109 workspace → Go 123、providers 105=105、
session 约 200），断言行为不复述实现；域内包级测试天然串行替代 Rust 的进程级
`test_env_lock`（`t.Setenv` 禁并行），跨进程持久化用例用测试二进制重跑自身。

### 第三方库选型

- **SQLite**：`modernc.org/sqlite`（纯 Go，CGO_ENABLED=0 可测业务域）；SQL/pragma/分批删除
  语义逐字保留，`rows` 关键字与 `?1`→`?` 占位符差异经 round-trip 测试证实无影响。
- **zstd**：`klauspost/compress`（纯 Go）。Rust 选 libzstd 是因纯 Rust 解码器 83MB/s 拖慢
  首屏；klauspost 汇编优化后该痛点不存在。多帧串联与截断尾帧容错按 `decode_all` 语义复刻。
- **JSONC/JSON5**：自写最小预处理（`internal/support/jsonc.go`，剥注释+尾逗号后交
  `encoding/json` 严格解码），不引 JSON5 库。支持范围是实际配置文件的子集（JSONC）；
  JSON5 独有特性（无引号键等）不支持。读写不对称口径不变：读容忍、写整文件重写纯 JSON。
- **YAML**：yaml.v3 + `yaml.Node` 保序（serde_yaml Mapping 保插入序、`!!js` 标签是文件
  语义，Go map 会重排丢标签）；dsh cordis patch 操作经 `internal/support/dsh_patch.go`
  （Rust 同名 support 模块的上提）。
- **TOML**：BurntSushi。落盘排版与 serde 系有逐字节差异（空父表头、缩进），落盘契约
  （合法格式+语义往返+幂等）有测试守卫，不追求字节级一致。

### 行为变更（唯一一处）

MCP 配置文件格式判定从「内容探测优先、后缀兜底」改为**只按后缀**
（[决策记录](2026-11-19-mcp-format-detection-by-extension.md)）：内置 preset 全部
`.json`/`.toml`，内容探测零增量且属无契约兜底；奇形怪状后缀从静默成功变显式报错。

### 已知 Rust 缺陷的处置：transcript.resolve

Rust `transcript.rs` 的 `resolve` 两条换拼分支在后缀常量（`.jsonl.zst`）下还原不出目标
（剥 `.jsonl.zst` 后未补回 `.jsonl`），实际语义退化为「存在即原样返回」，与 codex reader
自己的正确配方（剥后缀+补 `.jsonl`）不一致。迁移期曾逐字复刻并测试钉住；经用户拍板已
修正（[决策记录](2026-11-19-fix-transcript-resolve-suffix-reassembly.md)），配方对齐
codex reader 的 shadow 判定。

### DTO 与绑定的收口

类型层已完成收敛：`features/*/generated/` 的 ts-rs 产物已删除，前后端契约类型的唯一
来源是 `frontend/bindings/`（wails 生成，`*T`→`T | null`、omitempty→可选字段、json tag
camelCase），UI 经各域 `types.ts` barrel（providers 域无 barrel，直连 `@bindings/reins/
internal/<域>/models`）消费。过渡期曾以 generated/ 为 api.ts 的类型标注层、运行时走
bindings 并在 `shared/lib/bindings.ts` 断言调和两套生成物；收口后类型与运行时同源，
断言桥只剩 bindingsCall（CancellablePromise→Promise 收口，兼裁掉 cancel 面），bindingsParam
因 struct 参数类型同型而删除。支撑这次收口的 Go 侧前提：DTO 的非导出命名切片类型
（`contentBlocks`、`sessionAgents`、`usageSourceStatsList` 等）曾让 wails 无法具名生成
元素模型（字段渲染成 `any`），已改为 plain 切片并移除 session 域切片/结构体级（含
`UsageHourBucket`）自定义 MarshalJSON，wire 语义（显式空切片输出 `[]`、nil 输出 `null`）
由调用方按域契约处理；`RawSkillSourceDefinition` 的保键序 MarshalJSON 与此无关、保留。
bindings 枚举是 TS string enum（`$zero = ""` 为 Go 零值占位），前端字面量比较一律改
成员引用。三处 serde 判别联合在 Go 侧建模：`SkillSourceConfigView` 单 struct + Type
判别字段（wire 形状逐字节对齐）、`RawSkillSourceDefinition`（自定义 MarshalJSON 保键序）、
DSH MCP 配置自定义 MarshalJSON。

### 其他

- `@wailsio/runtime` 的 vite 插件接管 bindings 别名；前端 55 个 wrapper 函数名与签名
  零变化（vitest 258 用例零改动全绿），仅函数体从 `invoke` 换为逐位置参数绑定调用。
- 版本号：`AppInfoService.Version()` 常量与 `build/config.yml` 的 `info.version` 同步维护。
- Linux：wails3 默认 GTK4，`-tags gtk3` 可回退（对应 Rust 版 GTK3 依赖面）；
  Wayland 兼容环境变量（`WEBKIT_DISABLE_DMABUF_RENDERER` 等）待真机复验后决定是否在
  main.go 恢复（[待确认文档](../context/2026-11-19-wails3-wayland-compat-pending.md)）。
- 构建：`task dev` / `task build` / `task package`（Taskfile 平台分发）；`pnpm codegen`
  由 `wails3 generate bindings` + Go 导出测试（agent-labels.ts）共同承担。

## 失效文档

- [域边界决策](2026-05-22-domain-boundaries.md)：Rust 三业务域表述按 Go `internal/` 理解，
  红线本身不变（前端域间、Go 三域两两禁依赖；`support` 只放无业务语义工具——dsh_patch 与
  jsonc 预处理在 Go 侧已按此归位）。
- [前端后端契约](../archive/2026-05-22-frontend-backend-contracts.md)：command 对照表由各域
  `service.go` 顶部映射注释取代；`api.ts` 唯一 invoke 面的规则由「唯一 bindings 调用面」
  承接（现经 `shared/lib/bindings.ts` 收口）。
- [test_env_lock 决策](../archive/2026-10-12-test-env-lock-is-by-design.md)：机制随 Rust 退役；
  「改 env 的测试必须串行」的设计原则在 Go 侧由包级隔离与禁 `t.Parallel()` 承接。
- [ts-rs 契约生成](../archive/2026-08-18-ts-rs-contract-codegen.md)：ts-rs codegen 管线整体退役，
  由「DTO 与绑定的收口」一节取代。
- [Cargo 依赖约束](../archive/2026-10-08-cargo-constraints.md)：Cargo 依赖面退役，选型口径由
  「第三方库选型」一节承接。
- [域重组落地方案](../archive/2026-05-22-domain-restructure-rollout-plan.md)：重组早已完成，
  布局口径以本决策与仓库根 AGENTS.md 为准。
- AGENTS.md 中 `src-tauri/`、`cargo`、`pnpm tauri dev`、ts-rs codegen 表述已同步更新。
