import { bindingsCall } from "@shared/lib/bindings";
import { WorkspaceService } from "@bindings/reins/internal/workspace";
import type {
  McpTransport,
  BatchGitSkillImportResult,
  SkillBatchImportFile,
  SkillDiscoveryResult,
  WorkspaceSelection,
  WorkspaceMcpMutationResult,
  WorkspacePathMutationResult,
  SyncPathOption,
  SyncSkillOptionsResult,
  SkillSyncItem,
  SourceSyncInput,
  SourceSyncConflict,
  SourceSyncResult,
  SkillSourceRefreshResult,
  SkillPathCleanupPreview,
  McpPathCleanupPreview,
  McpSyncInput,
  McpSyncPreview,
  McpSyncResult,
  SkillPathPreset,
  McpPathPreset,
  McpFormatInfo,
  WorkspaceState,
} from "./types";

// 后端调用经 bindings service；类型与运行时同源（frontend/bindings/），
// Promise 收口断言见 @shared/lib/bindings。参数是 bindings 的逐位置参数：
// 路径条目/MCP server 的创建传散参，struct 入参直接满足 bindings 接口。

type WorkspaceMcpPayload = {
  name: string;
  transport: McpTransport;
  homepage?: string | null;
  command?: string | null;
  args?: string[];
  env?: Record<string, string>;
  url?: string | null;
  headers?: Record<string, string>;
  timeout?: number | null;
};

type SkillSourceMutationInput = {
  sourceId: string;
  ref?: string | null;
  description?: string | null;
  includeNamePatterns: string[];
  includePathPatterns: string[];
};

function buildWorkspaceMcpPayload(payload: WorkspaceMcpPayload) {
  return {
    name: payload.name,
    transport: payload.transport,
    homepage: payload.homepage ?? null,
    command: payload.command ?? null,
    args: payload.args ?? [],
    env: payload.env ?? {},
    url: payload.url ?? null,
    headers: payload.headers ?? {},
    timeout: payload.timeout ?? null
  };
}

export function getSkillPathPresets(): Promise<SkillPathPreset[]> {
  return bindingsCall(WorkspaceService.GetSkillPathPresets());
}

export function getMcpPathPresets(): Promise<McpPathPreset[]> {
  return bindingsCall(WorkspaceService.GetMcpPathPresets());
}

export function getMcpFormatInfos(): Promise<McpFormatInfo[]> {
  return bindingsCall(WorkspaceService.GetMcpFormatInfos());
}

export function getWorkspaceState(): Promise<WorkspaceState> {
  return bindingsCall(WorkspaceService.GetWorkspaceState());
}

export function selectLocalSkillSourceDirectory(
  sourcePath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(
    WorkspaceService.SelectLocalSkillSourceDirectory(sourcePath ?? null)
  );
}

export function selectSkillPathDirectory(
  currentPath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(
    WorkspaceService.SelectSkillPathDirectory(currentPath ?? null)
  );
}

export function selectMcpPathConfigFile(
  currentPath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(
    WorkspaceService.SelectMcpPathConfigFile(currentPath ?? null)
  );
}

export function discoverGitSkills(
  repo: string,
  ref?: string
): Promise<SkillDiscoveryResult> {
  // bindings 逐位置传参，Go 参数名就是 reference，不再有 ref→reference 改写。
  return bindingsCall(WorkspaceService.DiscoverGitSkills(repo, ref ?? null));
}

export function discoverLocalSkills(
  sourcePath: string
): Promise<SkillDiscoveryResult> {
  return bindingsCall(WorkspaceService.DiscoverLocalSkills(sourcePath));
}

export function filterDiscoveredSkills(
  discoveryId: string,
  includeNamePatterns?: string[],
  includePathPatterns?: string[]
): Promise<SkillDiscoveryResult> {
  return bindingsCall(WorkspaceService.FilterDiscoveredSkills(
    discoveryId,
    includeNamePatterns ?? null,
    includePathPatterns ?? null
  ));
}

export function importDiscoveredSkills(
  discoveryId: string,
  description?: string,
  includeNamePatterns?: string[],
  includePathPatterns?: string[]
): Promise<SkillDiscoveryResult> {
  return bindingsCall(WorkspaceService.ImportDiscoveredSkills(
    discoveryId,
    description ?? null,
    includeNamePatterns ?? null,
    includePathPatterns ?? null
  ));
}

export function importBatchGitSkills(
  jsonContent: string
): Promise<BatchGitSkillImportResult> {
  return bindingsCall(WorkspaceService.ImportBatchGitSkills({
    jsonContent
  }));
}

export function selectSkillBatchImportFile(): Promise<SkillBatchImportFile | null> {
  return bindingsCall(WorkspaceService.SelectSkillBatchImportFile());
}

export function createSkillPath(
  name: string,
  dir: string
): Promise<WorkspacePathMutationResult> {
  return bindingsCall(WorkspaceService.CreateSkillPath(name, dir));
}

export function updateSkillPath(
  currentName: string,
  name: string,
  dir: string
): Promise<WorkspacePathMutationResult> {
  return bindingsCall(WorkspaceService.UpdateSkillPath(currentName, name, dir));
}

export function deleteSkillPath(
  name: string,
  cleanup: boolean
): Promise<WorkspacePathMutationResult> {
  return bindingsCall(WorkspaceService.DeleteSkillPath(name, cleanup));
}

export function previewSkillPathCleanup(
  name: string
): Promise<SkillPathCleanupPreview> {
  return bindingsCall(WorkspaceService.PreviewSkillPathCleanup(name));
}

export function createMcpPath(
  name: string,
  configPath: string,
  configType: string,
  configPrefix: string
): Promise<WorkspacePathMutationResult> {
  return bindingsCall(WorkspaceService.CreateMcpPath(name, configPath, configType, configPrefix));
}

export function updateMcpPath(
  currentName: string,
  name: string,
  configPath: string,
  configPrefix: string
): Promise<WorkspacePathMutationResult> {
  return bindingsCall(WorkspaceService.UpdateMcpPath(currentName, name, configPath, configPrefix));
}

export function deleteMcpPath(
  name: string,
  cleanup: boolean
): Promise<WorkspacePathMutationResult> {
  return bindingsCall(WorkspaceService.DeleteMcpPath(name, cleanup));
}

export function previewMcpPathCleanup(
  name: string
): Promise<McpPathCleanupPreview> {
  return bindingsCall(WorkspaceService.PreviewMcpPathCleanup(name));
}

export function createWorkspaceMcp(
  payload: WorkspaceMcpPayload
): Promise<WorkspaceMcpMutationResult> {
  const mcp = buildWorkspaceMcpPayload(payload);
  return bindingsCall(WorkspaceService.CreateWorkspaceMCP(
    mcp.name,
    mcp.transport,
    mcp.homepage,
    mcp.command,
    mcp.args,
    mcp.env,
    mcp.url,
    mcp.headers,
    mcp.timeout
  ));
}

export function updateWorkspaceMcp(
  serverName: string,
  payload: WorkspaceMcpPayload
): Promise<WorkspaceMcpMutationResult> {
  const mcp = buildWorkspaceMcpPayload(payload);
  return bindingsCall(WorkspaceService.UpdateWorkspaceMCP(
    serverName,
    mcp.name,
    mcp.transport,
    mcp.homepage,
    mcp.command,
    mcp.args,
    mcp.env,
    mcp.url,
    mcp.headers,
    mcp.timeout
  ));
}

export function deleteWorkspaceMcp(
  serverName: string
): Promise<WorkspaceMcpMutationResult> {
  return bindingsCall(WorkspaceService.DeleteWorkspaceMCP(serverName));
}

export function updateSkillSource(input: SkillSourceMutationInput): Promise<WorkspaceState> {
  return bindingsCall(WorkspaceService.UpdateSkillSource(input));
}

export function refreshGitSkillSource(
  sourceId: string
): Promise<SkillSourceRefreshResult> {
  return bindingsCall(WorkspaceService.RefreshGitSkillSource(sourceId));
}

// 取消当前在途的来源拉取；没有在途拉取时后端是无害的空操作。
export function cancelSkillSourceFetch(): Promise<void> {
  return bindingsCall(WorkspaceService.CancelSkillSourceFetch());
}

// 逐路径 apply/remove/preview 已随矩阵同步删除（决策记录
// 2026-11-28-mcp-matrix-sync.md 第 5 条）：分发动作统一走 previewMcpSync +
// syncMcpsToPaths。
export function previewMcpSync(
  input: McpSyncInput
): Promise<McpSyncPreview> {
  return bindingsCall(WorkspaceService.PreviewMcpSync(input));
}

export function syncMcpsToPaths(
  input: McpSyncInput
): Promise<McpSyncResult> {
  return bindingsCall(WorkspaceService.SyncMcpsToPaths(input));
}

export function deleteSkillSource(sourceId: string): Promise<WorkspaceState> {
  return bindingsCall(WorkspaceService.DeleteSkillSource(sourceId));
}

export function deleteSkillSources(sourceIds: string[]): Promise<void> {
  return bindingsCall(WorkspaceService.DeleteSkillSources(sourceIds));
}

export function getSyncPathOptions(): Promise<SyncPathOption[]> {
  return bindingsCall(WorkspaceService.GetSyncPathOptions());
}

export function getSyncSkillOptions(sourceId: string): Promise<SyncSkillOptionsResult> {
  return bindingsCall(WorkspaceService.GetSyncSkillOptions(sourceId));
}

export function syncSourceToPaths(
  input: SourceSyncInput
): Promise<SourceSyncResult> {
  return bindingsCall(WorkspaceService.SyncSourceToPaths(input));
}

export function previewSourceSyncConflicts(
  input: SourceSyncInput
): Promise<SourceSyncConflict[]> {
  return bindingsCall(WorkspaceService.PreviewSourceSyncConflicts(input));
}

export function removeSourceSync(
  sourceId: string,
  pathNames: string[]
): Promise<SourceSyncResult> {
  return bindingsCall(WorkspaceService.RemoveSourceSync(sourceId, pathNames));
}

export function removeSkillPathLink(
  pathName: string,
  destinationPath: string
): Promise<SkillSyncItem> {
  return bindingsCall(WorkspaceService.RemoveSkillPathLink(pathName, destinationPath));
}
