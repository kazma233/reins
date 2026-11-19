import { bindingsCall } from "@shared/lib/bindings";
import { WorkspaceService } from "@bindings/reins/internal/workspace";
import type {
  AgentTargetId,
  TargetPreset,
  WorkspaceState,
  McpTransport,
  McpTargetPreviewResult,
  McpTargetMutationResult,
  BatchGitSkillImportResult,
  SkillBatchImportFile,
  SkillDiscoveryResult,
  WorkspaceSelection,
  WorkspaceMcpMutationResult,
  WorkspaceTargetMutationResult,
  SyncTargetOption,
  SyncSkillOptionsResult,
  SkillSyncItem,
  SourceSyncInput,
  SourceSyncConflict,
  SourceSyncResult,
  SkillSourceRefreshResult,
} from "./types";

// 后端调用经 bindings service；类型与运行时同源（frontend/bindings/），
// Promise 收口断言见 @shared/lib/bindings。参数是 bindings 的逐位置参数：
// target/MCP 的创建传散参，struct 入参直接满足 bindings 接口。

type WorkspaceTargetPayload = {
  targetId: string;
  skillDir: string;
  configPath?: string | null;
};

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

export function getTargetPresets(): Promise<TargetPreset[]> {
  return bindingsCall(WorkspaceService.GetTargetPresets());
}

export function getWorkspaceState(): Promise<WorkspaceState> {
  return bindingsCall(WorkspaceService.GetWorkspaceState());
}

export function selectProjectPath(
  projectPath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(WorkspaceService.SelectProjectPath(projectPath ?? null));
}

export function selectLocalSkillSourceDirectory(
  sourcePath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(
    WorkspaceService.SelectLocalSkillSourceDirectory(sourcePath ?? null)
  );
}

export function selectTargetSkillDirectory(
  currentPath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(
    WorkspaceService.SelectTargetSkillDirectory(currentPath ?? null)
  );
}

export function selectTargetMcpConfigFile(
  currentPath?: string
): Promise<WorkspaceSelection | null> {
  return bindingsCall(
    WorkspaceService.SelectTargetMCPConfigFile(currentPath ?? null)
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

export function createWorkspaceTarget(
  payload: WorkspaceTargetPayload
): Promise<WorkspaceTargetMutationResult> {
  return bindingsCall(WorkspaceService.CreateWorkspaceTarget(
    payload.targetId,
    payload.skillDir,
    payload.configPath ?? null
  ));
}

export function updateWorkspaceTarget(
  currentTargetId: string,
  payload: WorkspaceTargetPayload
): Promise<WorkspaceTargetMutationResult> {
  return bindingsCall(WorkspaceService.UpdateWorkspaceTarget(
    currentTargetId,
    payload.targetId,
    payload.skillDir,
    payload.configPath ?? null
  ));
}

export function deleteWorkspaceTarget(
  targetId: string
): Promise<WorkspaceTargetMutationResult> {
  return bindingsCall(WorkspaceService.DeleteWorkspaceTarget(targetId));
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

export function applyMcpToTarget(
  serverName: string,
  targetId: AgentTargetId
): Promise<McpTargetMutationResult> {
  return bindingsCall(WorkspaceService.ApplyMCPToTarget(serverName, targetId));
}

export function removeMcpFromTarget(
  serverName: string,
  targetId: AgentTargetId
): Promise<McpTargetMutationResult> {
  return bindingsCall(WorkspaceService.RemoveMCPFromTarget(serverName, targetId));
}

export function previewMcpTarget(
  serverName: string,
  targetId: AgentTargetId
): Promise<McpTargetPreviewResult> {
  return bindingsCall(WorkspaceService.PreviewMCPTarget(serverName, targetId));
}

export function createWorkspaceProject(
  projectId: string,
  path: string,
  agents: string[]
): Promise<WorkspaceTargetMutationResult> {
  return bindingsCall(WorkspaceService.CreateWorkspaceProject({
    projectId,
    path,
    agents,
  }));
}

export function updateWorkspaceProject(
  projectId: string,
  path: string,
  agents: string[]
): Promise<WorkspaceTargetMutationResult> {
  return bindingsCall(WorkspaceService.UpdateWorkspaceProject({
    projectId,
    path,
    agents,
  }));
}

export function deleteWorkspaceProject(
  projectId: string
): Promise<WorkspaceTargetMutationResult> {
  return bindingsCall(WorkspaceService.DeleteWorkspaceProject(projectId));
}

export function deleteSkillSource(sourceId: string): Promise<WorkspaceState> {
  return bindingsCall(WorkspaceService.DeleteSkillSource(sourceId));
}

export function deleteSkillSources(sourceIds: string[]): Promise<void> {
  return bindingsCall(WorkspaceService.DeleteSkillSources(sourceIds));
}

export function getSyncTargetOptions(): Promise<SyncTargetOption[]> {
  return bindingsCall(WorkspaceService.GetSyncTargetOptions());
}

export function getSyncSkillOptions(sourceId: string): Promise<SyncSkillOptionsResult> {
  return bindingsCall(WorkspaceService.GetSyncSkillOptions(sourceId));
}

export function syncSourceToTargets(
  input: SourceSyncInput
): Promise<SourceSyncResult> {
  return bindingsCall(WorkspaceService.SyncSourceToTargets(input));
}

export function previewSourceSyncConflicts(
  input: SourceSyncInput
): Promise<SourceSyncConflict[]> {
  return bindingsCall(WorkspaceService.PreviewSourceSyncConflicts(input));
}

export function removeSourceSync(
  sourceId: string,
  targetIds: string[]
): Promise<SourceSyncResult> {
  return bindingsCall(WorkspaceService.RemoveSourceSync(sourceId, targetIds));
}

export function removeTargetSkillLink(
  targetId: string,
  destinationPath: string
): Promise<SkillSyncItem> {
  return bindingsCall(WorkspaceService.RemoveTargetSkillLink(targetId, destinationPath));
}
