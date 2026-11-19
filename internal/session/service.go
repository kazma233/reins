// service.go：session 域的 Wails service 薄壳（对齐 Rust 版 session/
// commands.rs 的 9 个命令）。只做参数归一与 *Inner/*Command 分发；运行时
// goroutine 调度属于 Wails，错误经 error 返回（Wails 转 Promise reject，
// message = error string，与 Rust Result<T,String> 口径一致）。
package session

// SessionService：会话与用量域的 Wails service。SessionIndexState 由应用层
// （main）构造注入，进程内单实例。
type SessionService struct {
	state *SessionIndexState
}

// NewSessionService 构造 service；state 持有目录缓存。
func NewSessionService(state *SessionIndexState) *SessionService {
	return &SessionService{state: state}
}

// DetectSources 对齐 detect_sources。
func (s *SessionService) DetectSources() ([]SourceStatus, error) {
	return DetectSourcesInner(s.state)
}

// ClearSessionCaches 对齐 clear_session_caches（连持久层一起清空的全量重建）。
func (s *SessionService) ClearSessionCaches() error {
	return ClearSessionCachesInner(s.state)
}

// ListSessions 对齐 list_sessions：分页参数缺省与钳制在 Command 层归一。
func (s *SessionService) ListSessions(
	sourceApp string,
	offset *int,
	limit *int,
	query *string,
	reverse *bool,
	refresh *bool,
) (SessionPage, error) {
	return ListSessionsCommand(s.state, sourceApp, offset, limit, query, reverse, refresh)
}

// RefreshSessions 对齐 refresh_sessions。
func (s *SessionService) RefreshSessions(
	sourceApp string,
	offset *int,
	limit *int,
	query *string,
	reverse *bool,
) (SessionRefreshResult, error) {
	return RefreshSessionsCommand(s.state, sourceApp, offset, limit, query, reverse)
}

// GetSessionOverview 对齐 get_session_overview；source_app 的解析报错文案
// 与 Rust SourceApp::from_str 逐字一致（ParseSourceApp）。
func (s *SessionService) GetSessionOverview(sourceApp string, sourceSessionID string) (SessionOverview, error) {
	app, err := ParseSourceApp(sourceApp)
	if err != nil {
		return SessionOverview{}, err
	}
	return GetSessionOverviewInner(app, sourceSessionID)
}

// GetSessionMessages 对齐 get_session_messages（详情正文全量返回）。
func (s *SessionService) GetSessionMessages(sourceApp string, sourceSessionID string) ([]SessionMessage, error) {
	app, err := ParseSourceApp(sourceApp)
	if err != nil {
		return nil, err
	}
	return GetSessionMessagesInner(app, sourceSessionID)
}

// GetSessionAgentMessages 对齐 get_session_agent_messages（子代理弹窗取数）。
func (s *SessionService) GetSessionAgentMessages(sourceApp string, sourceSessionID string, agentSessionID string) ([]SessionMessage, error) {
	app, err := ParseSourceApp(sourceApp)
	if err != nil {
		return nil, err
	}
	return GetSessionAgentMessagesInner(app, sourceSessionID, agentSessionID)
}

// GetSessionEvents 对齐 get_session_events。
func (s *SessionService) GetSessionEvents(sourceApp string, sourceSessionID string) ([]SessionEvent, error) {
	app, err := ParseSourceApp(sourceApp)
	if err != nil {
		return nil, err
	}
	return GetSessionEventsInner(app, sourceSessionID)
}

// GetUsageStats 对齐 get_usage_stats（用量统计在 session 域实现，前端独立
// usage 视图消费）。
func (s *SessionService) GetUsageStats() (UsageStats, error) {
	return UsageStatsInner()
}
