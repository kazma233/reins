// service.go：workspace 域的 Wails service 薄壳（对齐 Rust 版 workspace/
// commands.rs 的 35 个命令）。职责与 Rust 命令层相同：诊断日志、来源拉取的
// 取消槽登记、散参 → 域内 Input 的组装；核心行为全部在 *Inner 与共享的
// BuildSync* 里。错误经 error 返回（Wails 转 Promise reject），文案由 inner
// 保证与 Rust 逐字一致。
package workspace

import (
	"fmt"

	"reins/internal/agents"
	"reins/internal/logger"
	"reins/internal/support"
)

// WorkspaceService：工作区分发域的 Wails service。三个运行时状态（配置
// store、发现快照缓存、单槽取消）与对话框端口都由应用层（main）注入。
type WorkspaceService struct {
	store       *WorkspaceConfigStore
	discovery   *SkillDiscoveryState
	fetchCancel *SkillSourceFetchCancelState
	dialog      DialogPort
}

// NewWorkspaceService 构造 service；dialog 用 Wails 对话框适配器实现。
func NewWorkspaceService(
	store *WorkspaceConfigStore,
	discovery *SkillDiscoveryState,
	fetchCancel *SkillSourceFetchCancelState,
	dialog DialogPort,
) *WorkspaceService {
	return &WorkspaceService{
		store:       store,
		discovery:   discovery,
		fetchCancel: fetchCancel,
		dialog:      dialog,
	}
}

// env 对齐 Rust 命令层的 environment()：每次调用读进程环境，环境视图不缓存
// 在 service 上（用户改了重定向变量后下一命令即生效）。
func (s *WorkspaceService) env() *support.Environment {
	return support.FromEnv(agents.Redirects())
}

// GetTargetPresets 对齐 get_target_presets。
func (s *WorkspaceService) GetTargetPresets() []TargetPreset {
	return TargetPresetsInner(s.env())
}

// GetWorkspaceState 对齐 get_workspace_state。
func (s *WorkspaceService) GetWorkspaceState() (*WorkspaceState, error) {
	return WorkspaceStateInner(s.store)
}

// SelectProjectPath 对齐 select_project_path。
func (s *WorkspaceService) SelectProjectPath(projectPath *string) (*WorkspaceSelection, error) {
	return SelectProjectPathInner(s.dialog, projectPath)
}

// SelectLocalSkillSourceDirectory 对齐 select_local_skill_source_directory。
func (s *WorkspaceService) SelectLocalSkillSourceDirectory(sourcePath *string) (*WorkspaceSelection, error) {
	return SelectLocalSkillSourceDirectoryInner(s.dialog, sourcePath)
}

// SelectSkillBatchImportFile 对齐 select_skill_batch_import_file。
func (s *WorkspaceService) SelectSkillBatchImportFile() (*SkillBatchImportFile, error) {
	return SelectSkillBatchImportFileInner(s.dialog)
}

// SelectTargetSkillDirectory 对齐 select_target_skill_directory。
func (s *WorkspaceService) SelectTargetSkillDirectory(currentPath *string) (*WorkspaceSelection, error) {
	return SelectTargetSkillDirectoryInner(s.dialog, currentPath)
}

// SelectTargetMCPConfigFile 对齐 select_target_mcp_config_file。
func (s *WorkspaceService) SelectTargetMCPConfigFile(currentPath *string) (*WorkspaceSelection, error) {
	return SelectTargetMCPConfigFileInner(s.dialog, currentPath)
}

// ApplyMCPToTarget 对齐 apply_mcp_to_target。
func (s *WorkspaceService) ApplyMCPToTarget(serverName string, targetID string) (*McpTargetMutationResult, error) {
	return ApplyMCPToTargetInner(s.store, serverName, targetID)
}

// RemoveMCPFromTarget 对齐 remove_mcp_from_target。
func (s *WorkspaceService) RemoveMCPFromTarget(serverName string, targetID string) (*McpTargetMutationResult, error) {
	return RemoveMCPFromTargetInner(s.store, serverName, targetID)
}

// PreviewMCPTarget 对齐 preview_mcp_target。
func (s *WorkspaceService) PreviewMCPTarget(serverName string, targetID string) (*McpTargetPreviewResult, error) {
	return PreviewMCPTargetInner(s.store, serverName, targetID)
}

// DiscoverGitSkills 对齐 discover_git_skills（clone/pull 到持久缓存再扫描）。
func (s *WorkspaceService) DiscoverGitSkills(repo string, reference *string) (*SkillDiscoveryResultView, error) {
	return DiscoverGitSkillsInner(s.store, s.discovery, repo, reference)
}

// DiscoverLocalSkills 对齐 discover_local_skills。
func (s *WorkspaceService) DiscoverLocalSkills(sourcePath string) (*SkillDiscoveryResultView, error) {
	return DiscoverLocalSkillsInner(s.discovery, sourcePath)
}

// FilterDiscoveredSkills 对齐 filter_discovered_skills；patterns nil = 不过滤。
func (s *WorkspaceService) FilterDiscoveredSkills(
	discoveryID string,
	includeNamePatterns []string,
	includePathPatterns []string,
) (*SkillDiscoveryResultView, error) {
	return FilterDiscoveredSkillsInner(s.discovery, discoveryID, includeNamePatterns, includePathPatterns)
}

// ImportDiscoveredSkills 对齐 import_discovered_skills。
func (s *WorkspaceService) ImportDiscoveredSkills(
	discoveryID string,
	description *string,
	includeNamePatterns []string,
	includePathPatterns []string,
) (*SkillDiscoveryResultView, error) {
	return ImportDiscoveredSkillsInner(s.store, s.discovery, discoveryID, description, includeNamePatterns, includePathPatterns)
}

// ImportBatchGitSkills 对齐 import_batch_git_skills。
func (s *WorkspaceService) ImportBatchGitSkills(input BatchGitSkillImportInput) (*BatchGitSkillImportResult, error) {
	return ImportBatchGitSkillsInner(s.store, input)
}

// CreateWorkspaceTarget 对齐 create_workspace_target。
func (s *WorkspaceService) CreateWorkspaceTarget(targetID string, skillDir string, configPath *string) (*WorkspaceTargetMutationResult, error) {
	logger.Info(fmt.Sprintf("create_workspace_target target_id=%s", targetID))
	return CreateWorkspaceTargetInner(s.store, RawTargetInput{TargetID: targetID, SkillDir: skillDir, ConfigPath: configPath})
}

// UpdateWorkspaceTarget 对齐 update_workspace_target。
func (s *WorkspaceService) UpdateWorkspaceTarget(currentTargetID string, targetID string, skillDir string, configPath *string) (*WorkspaceTargetMutationResult, error) {
	logger.Info(fmt.Sprintf("update_workspace_target current_target_id=%s target_id=%s", currentTargetID, targetID))
	return UpdateWorkspaceTargetInner(s.store, currentTargetID, RawTargetInput{TargetID: targetID, SkillDir: skillDir, ConfigPath: configPath})
}

// DeleteWorkspaceTarget 对齐 delete_workspace_target。
func (s *WorkspaceService) DeleteWorkspaceTarget(targetID string) (*WorkspaceTargetMutationResult, error) {
	logger.Info(fmt.Sprintf("delete_workspace_target target_id=%s", targetID))
	return DeleteWorkspaceTargetInner(s.store, targetID)
}

// CreateWorkspaceMCP 对齐 create_workspace_mcp：created_at 由命令层填
// currentTimestampMs（Rust commands.rs 的 current_timestamp_ms 调用点在同一
// 层）；系统时钟早于 Unix 纪元时归零。
func (s *WorkspaceService) CreateWorkspaceMCP(
	name string,
	transport string,
	homepage *string,
	command *string,
	args []string,
	env map[string]string,
	url *string,
	headers map[string]string,
	timeout *uint64,
) (*WorkspaceMcpMutationResult, error) {
	parsedTransport, err := parseMcpTransport(transport)
	if err != nil {
		return nil, err
	}
	logger.Info(fmt.Sprintf("create_workspace_mcp name=%s transport=%s", name, debugTransport(parsedTransport)))
	createdAt := currentTimestampMs()
	return CreateWorkspaceMCPInner(s.store, RawMcpConfig{
		Name:      name,
		Transport: parsedTransport,
		CreatedAt: &createdAt,
		Homepage:  homepage,
		Command:   command,
		Args:      args,
		Env:       env,
		URL:       url,
		Headers:   headers,
		Timeout:   timeout,
	})
}

// UpdateWorkspaceMCP 对齐 update_workspace_mcp：更新不携带 created_at。
func (s *WorkspaceService) UpdateWorkspaceMCP(
	serverName string,
	name string,
	transport string,
	homepage *string,
	command *string,
	args []string,
	env map[string]string,
	url *string,
	headers map[string]string,
	timeout *uint64,
) (*WorkspaceMcpMutationResult, error) {
	parsedTransport, err := parseMcpTransport(transport)
	if err != nil {
		return nil, err
	}
	logger.Info(fmt.Sprintf("update_workspace_mcp server_name=%s name=%s transport=%s", serverName, name, debugTransport(parsedTransport)))
	return UpdateWorkspaceMCPInner(s.store, serverName, RawMcpConfig{
		Name:      name,
		Transport: parsedTransport,
		Homepage:  homepage,
		Command:   command,
		Args:      args,
		Env:       env,
		URL:       url,
		Headers:   headers,
		Timeout:   timeout,
	})
}

// DeleteWorkspaceMCP 对齐 delete_workspace_mcp。
func (s *WorkspaceService) DeleteWorkspaceMCP(serverName string) (*WorkspaceMcpMutationResult, error) {
	logger.Info(fmt.Sprintf("delete_workspace_mcp server_name=%s", serverName))
	return DeleteWorkspaceMCPInner(s.store, serverName)
}

// DeleteSkillSource 对齐 delete_skill_source。
func (s *WorkspaceService) DeleteSkillSource(sourceID string) (*WorkspaceState, error) {
	logger.Info(fmt.Sprintf("delete_skill_source source_id=%s", sourceID))
	return DeleteSkillSourceInner(s.store, sourceID)
}

// DeleteSkillSources 对齐 delete_skill_sources。
func (s *WorkspaceService) DeleteSkillSources(sourceIDs []string) error {
	logger.Info(fmt.Sprintf("delete_skill_sources count=%d", len(sourceIDs)))
	return DeleteSkillSourcesInner(s.store, sourceIDs)
}

// CreateWorkspaceProject 对齐 create_workspace_project。
func (s *WorkspaceService) CreateWorkspaceProject(input ProjectMutationInput) (*WorkspaceTargetMutationResult, error) {
	logger.Info(fmt.Sprintf("create_workspace_project project_id=%s", input.ProjectID))
	return CreateWorkspaceProjectInner(s.store, input)
}

// UpdateWorkspaceProject 对齐 update_workspace_project。
func (s *WorkspaceService) UpdateWorkspaceProject(input ProjectMutationInput) (*WorkspaceTargetMutationResult, error) {
	logger.Info(fmt.Sprintf("update_workspace_project project_id=%s", input.ProjectID))
	return UpdateWorkspaceProjectInner(s.store, input)
}

// DeleteWorkspaceProject 对齐 delete_workspace_project。
func (s *WorkspaceService) DeleteWorkspaceProject(projectID string) (*WorkspaceTargetMutationResult, error) {
	logger.Info(fmt.Sprintf("delete_workspace_project project_id=%s", projectID))
	return DeleteWorkspaceProjectInner(s.store, projectID)
}

// GetSyncTargetOptions 对齐 get_sync_target_options。
func (s *WorkspaceService) GetSyncTargetOptions() ([]SyncTargetOption, error) {
	return BuildSyncTargetOptions(s.store)
}

// GetSyncSkillOptions 对齐 get_sync_skill_options：进入取数前登记取消槽，
// 与 Rust 命令层的 fetch_cancel.begin() 同位。
func (s *WorkspaceService) GetSyncSkillOptions(sourceID string) (*SyncSkillOptionsResult, error) {
	ctx := s.fetchCancel.Begin()
	return BuildSyncSkillOptions(ctx, s.store, sourceID)
}

// SyncSourceToTargets 对齐 sync_source_to_targets。
func (s *WorkspaceService) SyncSourceToTargets(input SourceSyncInput) (*SourceSyncResult, error) {
	return SyncSourceToTargetsInner(s.store, input)
}

// PreviewSourceSyncConflicts 对齐 preview_source_sync_conflicts。
func (s *WorkspaceService) PreviewSourceSyncConflicts(input SourceSyncInput) ([]SourceSyncConflict, error) {
	return PreviewSourceSyncConflictsInner(s.store, input)
}

// RemoveSourceSync 对齐 remove_source_sync。
func (s *WorkspaceService) RemoveSourceSync(sourceID string, targetIDs []string) (*SourceSyncResult, error) {
	return RemoveSourceSyncInner(s.store, sourceID, targetIDs)
}

// RemoveTargetSkillLink 对齐 remove_target_skill_link。
func (s *WorkspaceService) RemoveTargetSkillLink(targetID string, destinationPath string) (*SkillSyncItem, error) {
	return RemoveTargetSkillLinkInner(s.store, targetID, destinationPath)
}

// RefreshGitSkillSource 对齐 refresh_git_skill_source：同 get_sync_skill_options，
// 先登记取消槽再进入拉取。
func (s *WorkspaceService) RefreshGitSkillSource(sourceID string) (*SkillSourceRefreshResult, error) {
	ctx := s.fetchCancel.Begin()
	return RefreshGitSkillSourceInner(ctx, s.store, sourceID)
}

// CancelSkillSourceFetch 对齐 cancel_skill_source_fetch：无在途拉取时是
// 空操作，无返回值（Wails 侧 CancellablePromise<void>）。
func (s *WorkspaceService) CancelSkillSourceFetch() {
	s.fetchCancel.Cancel()
}

// UpdateSkillSource 对齐 update_skill_source。
func (s *WorkspaceService) UpdateSkillSource(input SkillSourceMutationInput) (*WorkspaceState, error) {
	return UpdateSkillSourceInner(s.store, input)
}
