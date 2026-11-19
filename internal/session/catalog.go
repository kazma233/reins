package session

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reins/internal/logger"
	"reins/internal/support"
)

// catalog.go：会话目录枚举与缓存（GLOSSARY 会话目录词条）。来源探测、列表
// 分页、合并视图的编排都在这里；目录条目本身由来源读取器枚举，进程内缓存
// 按 (来源 → SessionFileCatalog) 存放。

// SessionFileCatalog：一个来源的会话文件枚举结果与缓存。
type SessionFileCatalog struct {
	Entries []SessionFileEntry
}

// SessionIndexState：进程内的目录缓存状态（对齐 Rust 版 state/
// session_index.rs）。生命周期由 Phase 5 的应用层持有；测试各自构造。
type SessionIndexState struct {
	mu       sync.Mutex
	catalogs map[SourceApp]SessionFileCatalog
}

// NewSessionIndexState 构造空状态。
func NewSessionIndexState() *SessionIndexState {
	return &SessionIndexState{catalogs: map[SourceApp]SessionFileCatalog{}}
}

// Catalog：取来源的缓存目录，未命中返回 false。
func (s *SessionIndexState) Catalog(app SourceApp) (SessionFileCatalog, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog, ok := s.catalogs[app]
	return catalog, ok
}

// StoreCatalog：写入/覆盖来源目录。
func (s *SessionIndexState) StoreCatalog(app SourceApp, catalog SessionFileCatalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogs[app] = catalog
}

// Clear：清空全部来源目录（rescan 语义的一部分）。
func (s *SessionIndexState) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogs = map[SourceApp]SessionFileCatalog{}
}

// DetectSourcesInner：并行探测全部来源；扫描到的目录回填进程内缓存。
func DetectSourcesInner(state *SessionIndexState) ([]SourceStatus, error) {
	started := time.Now()
	inspections := ParallelCollect("Source inspection", func(spec *SourceSpec) (sourceInspection, error) {
		root, err := spec.Root()
		if err != nil {
			return sourceInspection{}, err
		}
		return inspectSource(state, spec.App, root, spec.DetectNote)
	})

	sources := make([]SourceStatus, 0, len(inspections))
	for _, outcome := range inspections {
		if outcome.Err != nil {
			return nil, outcome.Err
		}
		inspection := outcome.Value

		if inspection.catalog != nil {
			state.StoreCatalog(inspection.app, *inspection.catalog)
		}

		sources = append(sources, SourceStatus{
			App:          inspection.app,
			Available:    inspection.available,
			RootPath:     inspection.rootPath,
			SessionCount: inspection.sessionCount,
			Note:         inspection.note,
		})
	}

	logger.Info(fmt.Sprintf("session_scan_total seconds=%.2f", time.Since(started).Seconds()))
	return sources, nil
}

// RescanSessionCachesInner：刷新——重新扫盘。持久摘要、行与用量缓存按各自
// 的缓存键自失效，不参与清理，否则每次刷新都要把全部转录重新解析一遍
// （见 ClearAllCaches 的注释）。
func RescanSessionCachesInner(state *SessionIndexState) error {
	state.Clear()
	return RescanAllCaches()
}

// ClearSessionCachesInner：显式清缓存命令——连持久层一起清空的全量重建。
func ClearSessionCachesInner(state *SessionIndexState) error {
	state.Clear()
	return ClearAllCaches()
}

// ---------------------------------------------------------------------------
// 列表请求的来源选择：单一来源，或跨来源合并视图（全局分页）
// ---------------------------------------------------------------------------

// SourceSelection：列表请求的来源选择。
type SourceSelection struct {
	// All：跨来源合并视图；App 非空表示单一来源。
	All bool
	App SourceApp
}

// ParseSourceSelection 解析命令层的 source_app 参数（"all" = 合并视图）。
func ParseSourceSelection(value string) (SourceSelection, error) {
	if value == "all" {
		return SourceSelection{All: true}, nil
	}
	app, err := ParseSourceApp(value)
	if err != nil {
		return SourceSelection{}, err
	}
	return SourceSelection{App: app}, nil
}

// sources 返回本选择的来源列表；合并视图只含根目录存在的来源。
func (s SourceSelection) sources() []SourceApp {
	if s.All {
		return availableSources()
	}
	return []SourceApp{s.App}
}

// requestedSource：单一来源返回该来源；合并视图 nil。
func (s SourceSelection) requestedSource() *SourceApp {
	if s.All {
		return nil
	}
	app := s.App
	return &app
}

// availableSources：与 DetectSourcesInner 的可用性判断一致——只看来源根
// 目录是否存在。
func availableSources() []SourceApp {
	var apps []SourceApp
	for _, spec := range Sources {
		root, err := spec.Root()
		if err != nil || !pathExists(root) {
			continue
		}
		apps = append(apps, spec.App)
	}
	return apps
}

// ListSessionsInner：列表分页。缓存路径失败且不是显式刷新时，强制刷新重试
// 一次（自愈陈旧目录缓存）。
func ListSessionsInner(
	state *SessionIndexState,
	selection SourceSelection,
	offset int,
	limit int,
	query string,
	reverse bool,
	refresh bool,
) (SessionPage, error) {
	page, err := buildSessionPage(state, selection, offset, limit, query, reverse, refresh)
	if err == nil {
		return page, nil
	}
	if !refresh {
		page, retryErr := buildSessionPage(state, selection, offset, limit, query, reverse, true)
		if retryErr == nil {
			return page, nil
		}
		return SessionPage{}, fmt.Errorf("Failed to load cached session page: %w", err)
	}
	return SessionPage{}, err
}

// RefreshSessionsInner：刷新载荷——重扫 + 重探测 + 重新分页。合并视图始终
// 合并所有来源；单一来源保持「请求的不可用时回退到首个可用」。
func RefreshSessionsInner(
	state *SessionIndexState,
	requested SourceSelection,
	offset int,
	limit int,
	query string,
	reverse bool,
) (SessionRefreshResult, error) {
	if err := RescanSessionCachesInner(state); err != nil {
		return SessionRefreshResult{}, err
	}
	sources, err := DetectSourcesInner(state)
	if err != nil {
		return SessionRefreshResult{}, err
	}

	selection := requested
	if !requested.All {
		selection = SourceSelection{App: pickAvailableSource(requested.App, sources)}
	}
	page, err := ListSessionsInner(state, selection, offset, limit, query, reverse, false)
	if err != nil {
		return SessionRefreshResult{}, err
	}

	return SessionRefreshResult{
		SelectedSource: page.SourceApp,
		Sources:        sources,
		Page:           page,
	}, nil
}

// BuildSummary：扫描期累积器 → wire 摘要。session id 缺失时按文件名派生，
// 仍取不到才报错。
func BuildSummary(sourceApp SourceApp, path string, summary SummaryAccumulator) (SessionSummary, error) {
	sourceSessionID := ""
	if summary.SessionID != nil {
		sourceSessionID = *summary.SessionID
	} else if derived, ok := DeriveSessionID(path); ok {
		sourceSessionID = derived
	} else {
		return SessionSummary{}, fmt.Errorf("Unable to determine session id for %s", path)
	}

	title := sourceSessionID
	if summary.Title != nil && *summary.Title != "" {
		title = *summary.Title
	}

	models := summary.Models.IntoModels()
	return SessionSummary{
		SourceApp:       sourceApp,
		SourceSessionID: sourceSessionID,
		Title:           title,
		Cwd:             summary.Cwd,
		GitBranch:       summary.GitBranch,
		TranscriptPath:  path,
		CreatedAt:       summary.CreatedAt,
		UpdatedAt:       summary.UpdatedAt,
		TokenUsage:      summary.TokenUsage,
		Models:          models,
		Archived:        summary.Archived,
		Compressed:      summary.Compressed,
	}, nil
}

// MergeTokenUsage：各来源读取器先把自家 usage 字段归一，再经此合并；nil
// 表示尚未见过任何 usage 记录，与「字段缺失按 0 处理」区分开。
func MergeTokenUsage(total **SessionTokenUsage, next SessionTokenUsage) {
	if *total != nil {
		(*total).Accumulate(&next)
		return
	}
	copied := next
	*total = &copied
}

// UpdateSummaryTimestamp：按记录时间戳推进 created_at（取最小）与
// updated_at（取最大）。
func UpdateSummaryTimestamp(summary *SummaryAccumulator, value any) {
	timestamp, ok := jsonString(value, "timestamp")
	if !ok {
		return
	}
	millis, ok := support.ParseTimestamp(timestamp)
	if !ok {
		return
	}

	if summary.CreatedAt == nil || millis < *summary.CreatedAt {
		copied := millis
		summary.CreatedAt = &copied
	}
	if summary.UpdatedAt == nil || millis > *summary.UpdatedAt {
		copied := millis
		summary.UpdatedAt = &copied
	}
}

// DeriveSessionID：文件名派生会话 id——取文件名 stem，长度 ≥36 时取末
// 36 字符（UUID 后缀），否则整个 stem。
func DeriveSessionID(path string) (string, bool) {
	stem := fileStem(path)
	if stem == "" {
		return "", false
	}

	if len(stem) >= 36 {
		return stem[len(stem)-36:], true
	}
	return stem, true
}

// fileStem：对齐 Rust Path::file_stem（首字符的 '.' 属于文件名本身，不算
// 扩展名分隔点，与 support 的 pathExtension 同一口径）。
func fileStem(path string) string {
	name := filepath.Base(path)
	if index := strings.LastIndexByte(name, '.'); index > 0 {
		return name[:index]
	}
	return name
}

type sourceInspection struct {
	app          SourceApp
	available    bool
	rootPath     *string
	sessionCount int
	note         *string
	catalog      *SessionFileCatalog
}

// MergedEntry：合并视图里的条目——列表摘要的加载和解析按来源进行，所以
// 条目要带上所属来源。
type MergedEntry struct {
	sourceApp SourceApp
	entry     SessionFileEntry
}

func buildSessionPage(
	state *SessionIndexState,
	selection SourceSelection,
	offset int,
	limit int,
	query string,
	reverse bool,
	refresh bool,
) (SessionPage, error) {
	merged, err := collectMergedEntries(state, selection, refresh)
	if err != nil {
		return SessionPage{}, err
	}

	// 单一来源的目录本身有序，保持原顺序；合并视图需要跨来源统一排序。
	if selection.All {
		sort.SliceStable(merged, func(i, j int) bool {
			if merged[i].entry.SortTimestamp != merged[j].entry.SortTimestamp {
				return merged[i].entry.SortTimestamp > merged[j].entry.SortTimestamp
			}
			return comparePaths(merged[i].entry.Path, merged[j].entry.Path) < 0
		})
	}

	entries := make([]*MergedEntry, len(merged))
	for i := range merged {
		entries[i] = &merged[i]
	}
	if reverse {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}

	if normalized, ok := normalizeSessionQuery(query); ok {
		return buildFilteredSessionPage(selection, entries, offset, limit, normalized)
	}

	totalCount := len(entries)
	if offset > totalCount {
		offset = totalCount
	}
	end := offset + limit
	if end > totalCount {
		end = totalCount
	}
	if end < offset {
		end = offset
	}
	sessions := make([]SessionSummary, 0, end-offset)
	for _, entry := range entries[offset:end] {
		summary, err := loadSessionSummary(entry.sourceApp, entry.entry)
		if err != nil {
			return SessionPage{}, err
		}
		sessions = append(sessions, summary)
	}
	hasMore := end < totalCount

	return SessionPage{
		SourceApp:  selection.requestedSource(),
		Sessions:   sessions,
		Offset:     offset,
		Limit:      limit,
		NextOffset: nextOffsetPointer(hasMore, end),
		TotalCount: totalCount,
		HasMore:    hasMore,
	}, nil
}

func buildFilteredSessionPage(
	selection SourceSelection,
	entries []*MergedEntry,
	offset int,
	limit int,
	query string,
) (SessionPage, error) {
	sessions := make([]SessionSummary, 0, len(entries))
	for _, entry := range entries {
		summary, err := loadSessionSummary(entry.sourceApp, entry.entry)
		if err != nil {
			return SessionPage{}, err
		}
		sessions = append(sessions, summary)
	}
	matching := make([]SessionSummary, 0, len(sessions))
	for i := range sessions {
		if sessionMatchesQuery(&sessions[i], query) {
			matching = append(matching, sessions[i])
		}
	}

	totalCount := len(matching)
	if offset > totalCount {
		offset = totalCount
	}
	end := offset + limit
	if end > totalCount {
		end = totalCount
	}
	if end < offset {
		end = offset
	}
	hasMore := end < totalCount

	return SessionPage{
		SourceApp:  selection.requestedSource(),
		Sessions:   matching[offset:end],
		Offset:     offset,
		Limit:      limit,
		NextOffset: nextOffsetPointer(hasMore, end),
		TotalCount: totalCount,
		HasMore:    hasMore,
	}, nil
}

func nextOffsetPointer(hasMore bool, end int) *int {
	if !hasMore {
		return nil
	}
	return &end
}

func collectMergedEntries(state *SessionIndexState, selection SourceSelection, refresh bool) ([]MergedEntry, error) {
	merged := make([]MergedEntry, 0)
	for _, sourceApp := range selection.sources() {
		catalog, err := sessionCatalogEntries(state, sourceApp, refresh)
		if err != nil {
			return nil, err
		}
		for i := range catalog.Entries {
			merged = append(merged, MergedEntry{sourceApp: sourceApp, entry: catalog.Entries[i]})
		}
	}
	return merged, nil
}

func sessionCatalogEntries(state *SessionIndexState, sourceApp SourceApp, refresh bool) (SessionFileCatalog, error) {
	if !refresh {
		if catalog, ok := state.Catalog(sourceApp); ok {
			return catalog, nil
		}
	}

	catalog, err := buildSessionCatalog(sourceApp)
	if err != nil {
		return SessionFileCatalog{}, err
	}
	state.StoreCatalog(sourceApp, catalog)
	return catalog, nil
}

// buildSessionCatalog：全部扫描入口（detect、刷新、refresh 分页）都经这里
// 重建目录，计时日志落在这才能覆盖每一条扫描路径；缓存命中路径不进本函数，
// 不会刷日志。
func buildSessionCatalog(sourceApp SourceApp) (SessionFileCatalog, error) {
	started := time.Now()
	reader, err := reader(sourceApp)
	if err != nil {
		return SessionFileCatalog{}, err
	}
	entries, err := reader.ListEntries()
	if err != nil {
		return SessionFileCatalog{}, err
	}

	logger.Info(fmt.Sprintf("session_scan source=%s seconds=%.2f sessions=%d",
		sourceApp, time.Since(started).Seconds(), len(entries)))

	return SessionFileCatalog{Entries: entries}, nil
}

func loadSessionSummary(sourceApp SourceApp, entry SessionFileEntry) (SessionSummary, error) {
	if entry.Summary != nil {
		return *entry.Summary, nil
	}
	r, err := reader(sourceApp)
	if err != nil {
		return SessionSummary{}, err
	}
	return r.ParseSummary(entry.SourceSessionID)
}

// normalizeSessionQuery：空查询返回 false（不过滤）。
func normalizeSessionQuery(query string) (string, bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "", false
	}
	return strings.ToLower(trimmed), true
}

func sessionMatchesQuery(summary *SessionSummary, query string) bool {
	return strings.Contains(strings.ToLower(summary.Title), query) ||
		strings.Contains(strings.ToLower(summary.SourceSessionID), query)
}

func inspectSource(state *SessionIndexState, app SourceApp, root string, note *string) (sourceInspection, error) {
	available := pathExists(root)
	var rootPath *string
	if available {
		display := root
		rootPath = &display
	}

	if !available {
		return sourceInspection{app: app, available: false, rootPath: rootPath}, nil
	}

	if catalog, ok := state.Catalog(app); ok {
		return sourceInspection{
			app:          app,
			available:    true,
			rootPath:     rootPath,
			sessionCount: len(catalog.Entries),
			note:         note,
			catalog:      &catalog,
		}, nil
	}

	catalog, err := buildSessionCatalog(app)
	if err != nil {
		return sourceInspection{}, err
	}

	return sourceInspection{
		app:          app,
		available:    true,
		rootPath:     rootPath,
		sessionCount: len(catalog.Entries),
		note:         note,
		catalog:      &catalog,
	}, nil
}

// pickAvailableSource：请求来源可用则保持，否则回退首个可用；全不可用回
// Codex（Rust unwrap_or(Codex) 的现状契约）。
func pickAvailableSource(requested SourceApp, sources []SourceStatus) SourceApp {
	for i := range sources {
		if sources[i].App == requested && sources[i].Available {
			return sources[i].App
		}
	}
	for i := range sources {
		if sources[i].Available {
			return sources[i].App
		}
	}
	return SourceCodex
}
