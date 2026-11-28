// workspace 域契约类型的唯一来源是 wails bindings（frontend/bindings/）；
// 本文件 re-export bindings 模型，并保留纯前端类型。枚举按值导出，成员比较
// 处用 `McpTransport.TransportStdio` 这类成员引用。

export type {
  BatchGitSkillImportInput,
  BatchGitSkillImportItemResult,
  BatchGitSkillImportResult,
  DiscoveredSkill,
  McpConfigView,
  McpFormatExample,
  McpInspection,
  McpPathCleanupPreview,
  McpPathPreset,
  McpFormatInfo,
  McpPathStateInspection,
  McpPathView,
  McpSyncCell,
  McpSyncInput,
  McpSyncItem,
  McpSyncPreview,
  McpSyncPreviewItem,
  McpSyncResult,
  SkillBatchImportFile,
  SkillDiscoveryResultView,
  SkillDirLinkView,
  SkillLinkAssociation,
  SkillPathCleanupPreview,
  SkillPathInspection,
  SkillPathPreset,
  SkillPathView,
  SkillSourceConfigView,
  SkillSourceMutationInput,
  SkillSourceRefreshResult,
  SkillSourceRelative,
  SkillSyncItem,
  SourceSyncConflict,
  SourceSyncInput,
  SourceSyncResult,
  SyncPathOption,
  SyncSkillOption,
  SyncSkillOptionsResult,
  ValidationReport,
  WorkspaceConfigDocument,
  WorkspaceConfigView,
  WorkspaceInspection,
  WorkspaceMcpMutationResult,
  WorkspacePathMutationResult,
  WorkspaceSelection,
  WorkspaceState,
} from "@bindings/reins/internal/workspace/models";

export {
  McpPathState,
  McpSyncAction,
  McpTransport,
  SkillLinkState,
} from "@bindings/reins/internal/workspace/models";

import type { SkillDiscoveryResultView } from "@bindings/reins/internal/workspace/models";

// Frontend-facing alias: the backend names this SkillDiscoveryResultView.
export type SkillDiscoveryResult = SkillDiscoveryResultView;

export type WorkspaceTab = "skills" | "mcp";

export type SkillSourceKind = "git" | "local";
