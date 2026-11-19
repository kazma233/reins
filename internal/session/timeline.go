package session

// timeline.go：详情与时间线的取数入口。全部委托给来源读取器；本文件只是
// 入口形状与 reader() 注册表的查表点，真正的解析在各读取器里。

// reader 返回来源对应的读取器（查 sources 注册表）。合法值集合来自同一张
// 清单，查不到是编程错误而非运行时错误——对齐 Rust 的穷尽 match，panic。
func reader(app SourceApp) (Reader, error) {
	return Spec(app).Reader, nil
}

// GetSessionOverviewInner：单会话详情的概要。
func GetSessionOverviewInner(app SourceApp, sourceSessionID string) (SessionOverview, error) {
	r, err := reader(app)
	if err != nil {
		return SessionOverview{}, err
	}
	return r.ParseOverview(sourceSessionID)
}

// GetSessionMessagesInner：详情正文全量返回——分组器与子代理入口配对都需要
// 完整消息流，不再分页。
func GetSessionMessagesInner(app SourceApp, sourceSessionID string) ([]SessionMessage, error) {
	r, err := reader(app)
	if err != nil {
		return nil, err
	}
	return r.ParseMessages(sourceSessionID)
}

// GetSessionAgentMessagesInner：子代理弹窗的取数入口——一次返回该 agent 的
// 完整消息，与时间线分页进度无关。
func GetSessionAgentMessagesInner(app SourceApp, sourceSessionID string, agentSessionID string) ([]SessionMessage, error) {
	r, err := reader(app)
	if err != nil {
		return nil, err
	}
	return r.ParseAgentMessages(sourceSessionID, agentSessionID)
}

// GetSessionEventsInner：时间线事件流。
func GetSessionEventsInner(app SourceApp, sourceSessionID string) ([]SessionEvent, error) {
	r, err := reader(app)
	if err != nil {
		return nil, err
	}
	return r.ParseEvents(sourceSessionID)
}
