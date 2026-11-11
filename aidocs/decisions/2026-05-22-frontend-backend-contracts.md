# 前后端 Contract 对照

- 日期：2026-05-22
- 状态：生效中

## Session

| 前端位置 | 后端位置 | command / DTO | 说明 |
| --- | --- | --- | --- |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `detect_sources` | 返回 `SourceStatus[]` |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `clear_session_caches` | 清空 session index 和 reader cache |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `list_sessions` | 返回 `SessionPage` |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `refresh_sessions` | 返回 `SessionRefreshResult` |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `get_session_overview` | 返回 `SessionOverview`；参数只有 `(sourceApp, sourceSessionId)`，会话身份显式化后 `transcript_path` 参数已移除（展示字段仍在 DTO 上） |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `get_session_messages` | 返回 `SessionMessagePage`；参数 `(sourceApp, sourceSessionId, offset, limit)`，`transcript_path` 参数已移除 |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `get_session_events` | 返回 `SessionEventPage`；参数 `(sourceApp, sourceSessionId, offset, limit)`，`transcript_path` 参数已移除 |
| `src/features/sessions/api.ts` | `src-tauri/src/session/commands.rs` | `get_session_agent_messages` | 参数 `(sourceApp, sourceSessionId, agentSessionId)`，`transcript_path` 参数已移除 |

### Session 关键 DTO

| 前端类型 | 后端类型 | 后端文件 |
| --- | --- | --- |
| `SourceApp` | `SourceApp` | `src-tauri/src/session/model.rs` |
| `SourceStatus` | `SourceStatus` | `src-tauri/src/session/model.rs` |
| `SessionOverview` | `SessionOverview` | `src-tauri/src/session/model.rs` |
| `SessionMessagePage` | `SessionMessagePage` | `src-tauri/src/session/model.rs` |
| `SessionEventPage` | `SessionEventPage` | `src-tauri/src/session/model.rs` |
| `SessionPage` | `SessionPage` | `src-tauri/src/session/model.rs` |
| `SessionRefreshResult` | `SessionRefreshResult` | `src-tauri/src/session/model.rs` |

## Workspace

| 前端位置 | 后端位置 | command / DTO | 说明 |
| --- | --- | --- | --- |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `get_workspace_state` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `load_workspace_state` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `set_workspace_dir` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `create_workspace` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `add_workspace` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `read_workspace` | 返回 `ManagedWorkspace` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `update_workspace` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `remove_workspace` | 返回 `WorkspaceState` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `select_workspace_directory` | 返回 `WorkspaceSelection \| null` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `select_local_skill_source_directory` | 返回 `WorkspaceSelection \| null` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `select_target_skill_directory` | 返回 `WorkspaceSelection \| null` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `select_target_mcp_config_file` | 返回 `WorkspaceSelection \| null` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `select_skill_batch_import_file` | 弹原生文件选择器并读出所选 JSON 文件；返回 `SkillBatchImportFile \| null`（`path` 供界面提示，`content` 为文件文本），取消选择返回 `null` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `inspect_workspace_state` | 返回 `WorkspaceInspection` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `discover_git_skills` / `discover_local_skills` / `filter_discovered_skills` / `import_discovered_skills` | 返回 `SkillDiscoveryResult`；import 入参含 `description`（可选来源描述，trim 空按未填写） |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `import_batch_git_skills` | 批量导入 git 来源；入参 `BatchGitSkillImportInput.jsonContent` 为来源数组 JSON（schema：`repo`/`ref` 必填，`description`/`include_name_patterns`/`include_path_patterns` 可省略，`deny_unknown_fields`），返回 `BatchGitSkillImportResult` |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `load_skill_detail` / `sync_skill_source` / `update_skill_source` | skill 详情与来源同步 |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `get_target_presets` | 返回 `TargetPreset[]`：七个内置工具的创建预设，由 AgentSpec 派生，路径为 env 解析后的绝对真实路径（grokbuild/pi 因此可预填） |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `create_workspace_target` / `update_workspace_target` / `delete_workspace_target` | target CRUD；create 仅接受七个内置 id（其余报错），且不接收 `mcpConfigType`（后端按 AgentSpec 派生）；update 同样不接收 `mcpConfigType`，保留存量存储值（省略仍省略），存量自定义 target 照常编辑/删除 |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `import_workspace_skill` | workspace skill 导入 |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `delete_skill_source` / `delete_skill_sources` | source 删除（详见 2026-06-04 ADR） |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `create_workspace_mcp` / `update_workspace_mcp` / `delete_workspace_mcp` | MCP CRUD |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `import_skill_to_target` / `remove_skill_from_target` | skill 分发 |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `apply_mcp_to_target` / `remove_mcp_from_target` / `preview_mcp_target` | MCP 分发与预览 |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `refresh_git_skill_source` | 强制拉取 git 来源缓存；返回 `SkillSourceRefreshResult`（`cancelled=true` 表示被用户取消，不得当成功上报） |
| `src/features/workspace/api.ts` | `src-tauri/src/workspace/commands.rs` | `cancel_skill_source_fetch` | 取消当前在途的来源 git 拉取；无在途拉取时为空操作（详见 2026-11-03 拉取取消决策） |

### Workspace 关键 DTO

| 前端类型 | 后端类型 | 后端文件 |
| --- | --- | --- |
| `WorkspaceState` | `WorkspaceState` | `src-tauri/src/workspace/types.rs` |
| `ManagedWorkspace` | `ManagedWorkspace` | `src-tauri/src/workspace/types.rs` |
| `WorkspaceSelection` | `WorkspaceSelection` | `src-tauri/src/workspace/types.rs` |
| `SkillBatchImportFile` | `SkillBatchImportFile` | `src-tauri/src/workspace/types.rs` |
| `AgentTargetId` | `AgentTargetId` | `src-tauri/src/workspace/types.rs` |
| `TargetPreset` | `TargetPreset` | `src-tauri/src/workspace/types.rs` |
| `McpTransport` | `McpTransport` | `src-tauri/src/workspace/types.rs` |
| `SkillDiscoveryResult` | `SkillDiscoveryResultView` | `src-tauri/src/workspace/types.rs` |
| `SkillSourceRefreshResult` | `SkillSourceRefreshResult` | `src-tauri/src/workspace/types.rs` |
| `SkillSourceSyncResult` | `SkillSourceSyncResult` | `src-tauri/src/workspace/types.rs` |
| `SkillApplyResult` | `SkillApplyResult` | `src-tauri/src/workspace/types.rs` |
| `SkillRemoveResult` | `SkillRemoveResult` | `src-tauri/src/workspace/types.rs` |
| `WorkspaceTargetMutationResult` | `WorkspaceTargetMutationResult` | `src-tauri/src/workspace/types.rs` |
| `WorkspaceSkillMutationResult` | `WorkspaceSkillMutationResult` | `src-tauri/src/workspace/types.rs` |
| `WorkspaceMcpMutationResult` | `WorkspaceMcpMutationResult` | `src-tauri/src/workspace/types.rs` |
| `McpTargetMutationResult` | `McpTargetMutationResult` | `src-tauri/src/workspace/types.rs` |
| `McpTargetPreviewResult` | `McpTargetPreviewResult` | `src-tauri/src/workspace/types.rs` |

## 约束

1. 前端只允许通过 `src/features/sessions/api.ts` 和 `src/features/workspace/api.ts` 直接 `invoke` Tauri command。
2. 新增 command 时，必须同步更新本表和对应 feature 的 API wrapper。
3. DTO rename 如果只发生在单侧，必须在 wrapper 层显式说明映射关系，不能静默漂移。

## 变更记录

- 2026-06-04 增补：`delete_skill_source` / `delete_skill_sources` 与 `SkillSourceManageDialog` 一同引入，详见 `2026-06-04-skill-source-management.md`。
- 2026-11-03 增补：`refresh_git_skill_source` 返回值从 `void` 变为 `SkillSourceRefreshResult`；新增 `cancel_skill_source_fetch`（来源 git 拉取取消），详见 `2026-11-03-skill-source-fetch-cancellation.md`。
- 2026-11-11 变更：批量导入 git 来源的输入从 YAML 改为 JSON（`import_batch_git_skills` 入参 `yamlContent` → `jsonContent`，后端解析 serde_yaml → serde_json）；新增 `select_skill_batch_import_file`（选择并读取批量导入 JSON 文件，返回 `SkillBatchImportFile`）。
- 2026-11-11 增补：来源描述进入导入契约——`import_discovered_skills` 新增可选入参 `description`；批量导入 JSON schema 新增可选键 `description`（导入时与 `repo`/`ref` 同口径 trim 空按未填写）。
- 2026-10-08 变更：移除自定义 target 创建能力。新增 `get_target_presets`（`TargetPreset[]`，AgentSpec 派生下发）；`create_workspace_target` 收紧为七个内置 id 且 MCP 格式由后端派生。`McpConfigType` 从契约面退役：ts 不再导出，`create/update_workspace_target` 参数与 `TargetPreset` 均不携带，target 视图以 `mcp_format_description`（格式 writer 的中文说明）代替类型暴露；Rust 枚举保留为 config.yaml 存储判别字段与 writer 分发键，磁盘格式不变。
- 2026-11-11 变更：会话删除功能整体移除，`get_delete_plan` / `delete_session` 两个 command 与 `DeletePlan` / `DeletePlanAction` / `DeleteSessionResult` 三个 DTO 从契约面退役，详见 `2026-11-11-remove-session-deletion.md`。
