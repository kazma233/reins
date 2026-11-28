// service.go：workspace 域的 Wails service 薄壳（对齐 Rust 版 workspace/
// commands.rs 的命令面，2026-11-28 起为技能路径/MCP 路径散装模型）。职责
// 与 Rust 命令层相同：诊断日志、来源拉取的取消槽登记、散参 → 域内 Input
// 的组装；核心行为全部在 *Inner 与共享的 BuildSync* 里。错误经 error 返回
// （Wails 转 Promise reject），文案由 inner 保证与 Rust 逐字一致。
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

// GetSkillPathPresets 对齐 get_skill_path_presets。
func (s *WorkspaceService) GetSkillPathPresets() []SkillPathPreset {
	return GetSkillPathPresetsInner(s.env())
}

// GetMcpPathPresets 对齐 get_mcp_path_presets。
func (s *WorkspaceService) GetMcpPathPresets() []McpPathPreset {
	return GetMcpPathPresetsInner(s.env())
}

// GetMcpFormatInfos 对齐 get_mcp_format_infos。
func (s *WorkspaceService) GetMcpFormatInfos() []McpFormatInfo {
	return GetMcpFormatInfosInner()
}

// GetWorkspaceState 对齐 get_workspace_state。
func (s *WorkspaceService) GetWorkspaceState() (*WorkspaceState, error) {
	return WorkspaceStateInner(s.store)
}

// SelectLocalSkillSourceDirectory 对齐 select_local_skill_source_directory。
func (s *WorkspaceService) SelectLocalSkillSourceDirectory(sourcePath *string) (*WorkspaceSelection, error) {
	return SelectLocalSkillSourceDirectoryInner(s.dialog, sourcePath)
}

// SelectSkillBatchImportFile 对齐 select_skill_batch_import_file。
func (s *WorkspaceService) SelectSkillBatchImportFile() (*SkillBatchImportFile, error) {
	return SelectSkillBatchImportFileInner(s.dialog)
}

// SelectSkillPathDirectory 对齐 select_skill_path_directory。
func (s *WorkspaceService) SelectSkillPathDirectory(currentPath *string) (*WorkspaceSelection, error) {
	return SelectSkillPathDirectoryInner(s.dialog, currentPath)
}

// SelectMcpPathConfigFile 对齐 select_mcp_path_config_file。
func (s *WorkspaceService) SelectMcpPathConfigFile(currentPath *string) (*WorkspaceSelection, error) {
	return SelectMcpPathConfigFileInner(s.dialog, currentPath)
}

// PreviewMcpSync：聚合预演 MCP 矩阵同步（2026-11-28 新增，无 Rust 对应命令；
// 逐路径 apply/remove/preview 命令已随矩阵同步删除，见决策记录
// 2026-11-28-mcp-matrix-sync.md 第 5 条）。
func (s *WorkspaceService) PreviewMcpSync(input McpSyncInput) (*McpSyncPreview, error) {
	return PreviewMcpSyncInner(s.store, input)
}

// SyncMcpsToPaths：矩阵同步（2026-11-28 新增，无 Rust 对应命令）。
func (s *WorkspaceService) SyncMcpsToPaths(input McpSyncInput) (*McpSyncResult, error) {
	logger.Info(fmt.Sprintf("sync_mcps_to_paths cells=%d", len(input.Cells)))
	return SyncMcpsToPathsInner(s.store, input)
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

// CreateSkillPath 对齐 create_skill_path。
func (s *WorkspaceService) CreateSkillPath(name string, dir string) (*WorkspacePathMutationResult, error) {
	logger.Info(fmt.Sprintf("create_skill_path name=%s", name))
	return CreateSkillPathInner(s.store, RawSkillPathInput{Name: name, Dir: dir})
}

// UpdateSkillPath 对齐 update_skill_path。
func (s *WorkspaceService) UpdateSkillPath(currentName string, name string, dir string) (*WorkspacePathMutationResult, error) {
	logger.Info(fmt.Sprintf("update_skill_path current_name=%s name=%s", currentName, name))
	return UpdateSkillPathInner(s.store, currentName, RawSkillPathInput{Name: name, Dir: dir})
}

// DeleteSkillPath 对齐 delete_skill_path；cleanup=true 时一并卸载受管链接。
func (s *WorkspaceService) DeleteSkillPath(name string, cleanup bool) (*WorkspacePathMutationResult, error) {
	logger.Info(fmt.Sprintf("delete_skill_path name=%s cleanup=%t", name, cleanup))
	return DeleteSkillPathInner(s.store, name, cleanup)
}

// PreviewSkillPathCleanup 对齐 preview_skill_path_cleanup。
func (s *WorkspaceService) PreviewSkillPathCleanup(name string) (*SkillPathCleanupPreview, error) {
	return PreviewSkillPathCleanupInner(s.store, name)
}

// CreateMcpPath 对齐 create_mcp_path。
func (s *WorkspaceService) CreateMcpPath(name string, configPath string, configType string, configPrefix string) (*WorkspacePathMutationResult, error) {
	logger.Info(fmt.Sprintf("create_mcp_path name=%s config_type=%s", name, configType))
	return CreateMcpPathInner(s.store, RawMcpPathInput{
		Name:         name,
		ConfigPath:   configPath,
		ConfigType:   configType,
		ConfigPrefix: configPrefix,
	})
}

// UpdateMcpPath 对齐 update_mcp_path：config_type 保留存量值（客户端不传）。
func (s *WorkspaceService) UpdateMcpPath(currentName string, name string, configPath string, configPrefix string) (*WorkspacePathMutationResult, error) {
	logger.Info(fmt.Sprintf("update_mcp_path current_name=%s name=%s", currentName, name))
	return UpdateMcpPathInner(s.store, currentName, RawMcpPathInput{
		Name:         name,
		ConfigPath:   configPath,
		ConfigPrefix: configPrefix,
	})
}

// DeleteMcpPath 对齐 delete_mcp_path；cleanup=true 时一并移除 Reins 写入的
// 条目。
func (s *WorkspaceService) DeleteMcpPath(name string, cleanup bool) (*WorkspacePathMutationResult, error) {
	logger.Info(fmt.Sprintf("delete_mcp_path name=%s cleanup=%t", name, cleanup))
	return DeleteMcpPathInner(s.store, name, cleanup)
}

// PreviewMcpPathCleanup 对齐 preview_mcp_path_cleanup。
func (s *WorkspaceService) PreviewMcpPathCleanup(name string) (*McpPathCleanupPreview, error) {
	return PreviewMcpPathCleanupInner(s.store, name)
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

// UpdateWorkspaceMCP 对齐 update_workspace_mcp：更新不携带 created_at；
// 改名时由后端在锁内一次完成登记更新与已装路径重写（apply 新名 + remove
// 旧名），前端不再串行逐路径调用。
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

// GetSyncPathOptions 对齐 get_sync_path_options。
func (s *WorkspaceService) GetSyncPathOptions() ([]SyncPathOption, error) {
	return BuildSyncPathOptions(s.store)
}

// GetSyncSkillOptions 对齐 get_sync_skill_options：进入取数前登记取消槽，
// 与 Rust 命令层的 fetch_cancel.begin() 同位。
func (s *WorkspaceService) GetSyncSkillOptions(sourceID string) (*SyncSkillOptionsResult, error) {
	ctx := s.fetchCancel.Begin()
	return BuildSyncSkillOptions(ctx, s.store, sourceID)
}

// SyncSourceToPaths 对齐 sync_source_to_paths。
func (s *WorkspaceService) SyncSourceToPaths(input SourceSyncInput) (*SourceSyncResult, error) {
	return SyncSourceToPathsInner(s.store, input)
}

// PreviewSourceSyncConflicts 对齐 preview_source_sync_conflicts。
func (s *WorkspaceService) PreviewSourceSyncConflicts(input SourceSyncInput) ([]SourceSyncConflict, error) {
	return PreviewSourceSyncConflictsInner(s.store, input)
}

// RemoveSourceSync 对齐 remove_source_sync。
func (s *WorkspaceService) RemoveSourceSync(sourceID string, pathNames []string) (*SourceSyncResult, error) {
	return RemoveSourceSyncInner(s.store, sourceID, pathNames)
}

// RemoveSkillPathLink 对齐 remove_skill_path_link。
func (s *WorkspaceService) RemoveSkillPathLink(pathName string, destinationPath string) (*SkillSyncItem, error) {
	return RemovePathSkillLinkInner(s.store, pathName, destinationPath)
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
