package session

// commands.go：list/refresh 两个命令共享的入参归一层（对齐 Rust 版
// session/commands.rs 的入参处理）。Rust 的 run_blocking/spawn_blocking 属于
// Tauri 运行时，Go 侧由调用方决定 goroutine 池，不在域内移植；其余命令无
// 归一逻辑，service 层直调对应 *Inner。

const (
	// DefaultSessionPageSize 与 MaxSessionPageSize：列表分页参数的默认与钳制。
	DefaultSessionPageSize = 20
	MaxSessionPageSize     = 200
)

// listParams：list/refresh 命令共享的入参归一结果。
type listParams struct {
	selection SourceSelection
	offset    int
	limit     int
	query     string
	reverse   bool
	refresh   bool
}

// normalizeListParams：offset 缺省 0；limit 缺省 20 并钳制到 [1, 200]；
// query/reverse 缺省空/false；refresh 缺省 = offset == 0（翻页不刷新）。
func normalizeListParams(sourceApp string, offset *int, limit *int, query *string, reverse *bool, refresh *bool, refreshDefaultsOnList bool) (listParams, error) {
	selection, err := ParseSourceSelection(sourceApp)
	if err != nil {
		return listParams{}, err
	}

	params := listParams{selection: selection}
	if offset != nil {
		params.offset = *offset
	}
	params.limit = DefaultSessionPageSize
	if limit != nil {
		params.limit = *limit
	}
	if params.limit < 1 {
		params.limit = 1
	}
	if params.limit > MaxSessionPageSize {
		params.limit = MaxSessionPageSize
	}
	if query != nil {
		params.query = *query
	}
	if reverse != nil {
		params.reverse = *reverse
	}
	if refreshDefaultsOnList {
		params.refresh = params.offset == 0
	}
	if refresh != nil {
		params.refresh = *refresh
	}
	return params, nil
}

// ListSessionsCommand：list_sessions 命令的可测核心（含 refresh 默认值）。
func ListSessionsCommand(
	state *SessionIndexState,
	sourceApp string,
	offset *int,
	limit *int,
	query *string,
	reverse *bool,
	refresh *bool,
) (SessionPage, error) {
	params, err := normalizeListParams(sourceApp, offset, limit, query, reverse, refresh, true)
	if err != nil {
		return SessionPage{}, err
	}
	return ListSessionsInner(state, params.selection, params.offset, params.limit, params.query, params.reverse, params.refresh)
}

// RefreshSessionsCommand：refresh_sessions 命令的可测核心。
func RefreshSessionsCommand(
	state *SessionIndexState,
	sourceApp string,
	offset *int,
	limit *int,
	query *string,
	reverse *bool,
) (SessionRefreshResult, error) {
	params, err := normalizeListParams(sourceApp, offset, limit, query, reverse, nil, false)
	if err != nil {
		return SessionRefreshResult{}, err
	}
	return RefreshSessionsInner(state, params.selection, params.offset, params.limit, params.query, params.reverse)
}
