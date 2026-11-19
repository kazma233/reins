// workspace 域契约类型的唯一来源是 wails bindings（frontend/bindings/）；
// 本文件 re-export bindings 模型（出口名保持与旧 generated barrel 一致，
// bindings 的 AgentTargetID 以前端既有拼写 AgentTargetId 导出），并保留
// 纯前端类型。枚举按值导出，成员比较处用 `McpTransport.TransportStdio`
// 这类成员引用。

export type {
  AgentTargetID as AgentTargetId,
  BatchGitSkillImportInput,
  BatchGitSkillImportItemResult,
  BatchGitSkillImportResult,
  DiscoveredSkill,
  McpConfigView,
  McpFormatExample,
  McpInspection,
  McpTargetInspection,
  McpTargetMutationResult,
  McpTargetPreviewResult,
  ProjectConfigView,
  ProjectInspection,
  ProjectMutationInput,
  SkillBatchImportFile,
  SkillDiscoveryResultView,
  SkillLinkAssociation,
  SkillSourceConfigView,
  SkillSourceMutationInput,
  SkillSourceRefreshResult,
  SkillSourceRelative,
  SkillSyncItem,
  SourceSyncConflict,
  SourceSyncInput,
  SourceSyncResult,
  SyncSkillOption,
  SyncSkillOptionsResult,
  SyncTargetOption,
  TargetConfigView,
  TargetInspection,
  TargetPreset,
  ValidationReport,
  WorkspaceConfigDocument,
  WorkspaceConfigView,
  WorkspaceInspection,
  WorkspaceMcpMutationResult,
  WorkspaceSelection,
  WorkspaceState,
  WorkspaceTargetMutationResult,
} from "@bindings/reins/internal/workspace/models";

export {
  McpTargetState,
  McpTransport,
  SkillLinkState,
} from "@bindings/reins/internal/workspace/models";

import type { SkillDiscoveryResultView } from "@bindings/reins/internal/workspace/models";

// Frontend-facing alias: the backend names this SkillDiscoveryResultView.
export type SkillDiscoveryResult = SkillDiscoveryResultView;

export type WorkspaceTab = "targets" | "skills" | "mcp";

export type SkillSourceKind = "git" | "local";
