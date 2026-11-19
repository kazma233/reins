package session

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"reins/internal/logger"
	"reins/internal/support"
)

// usage_stats.go：token 用量统计——按消耗事件的时间戳把各来源用量归到本地
// 时区的小时，日序列与今日小时序列都由小时桶聚合而来。
//
// 跨天会话由消息/turn/事件级时间戳天然拆分，不做会话级近似。归一后的
// SessionTokenUsage 直接累加，与列表页 family 求和同口径（resume 段与
// subagent 线程是独立转录文件，各自归桶后求和）。窗口裁剪与指标推导都在
// 前端完成。

// UsageHourBuckets：小时桶集合，键是 "YYYY-MM-DDTHH" 本地时区串。Rust 侧是
// BTreeMap；需要确定顺序的消费点统一经 SortedKeys（map 迭代无序）。
type UsageHourBuckets map[string]UsageHourBucket

// SortedKeys 返回按小时键升序的全部键。
func (b UsageHourBuckets) SortedKeys() []string {
	keys := make([]string, 0, len(b))
	for key := range b {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// IsEmpty 对齐 Rust buckets.is_empty()。
func (b UsageHourBuckets) IsEmpty() bool { return len(b) == 0 }

// UsageStatsInner：全量统计——并行采集各来源，只保留当前可用且有数据的。
func UsageStatsInner() (UsageStats, error) {
	outcomes := ParallelCollect("Usage stats", func(spec *SourceSpec) (*UsageSourceStats, error) {
		return spec.UsageDays()
	})

	sources := make([]UsageSourceStats, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			return UsageStats{}, outcome.Err
		}
		if outcome.Value != nil {
			sources = append(sources, *outcome.Value)
		}
	}

	return UsageStats{Sources: sources}, nil
}

// DaysFromCachedJSONL：jsonl 目录扫描 + mtime 命中持久缓存；单文件失败只记
// 日志跳过，不阻断统计。
func DaysFromCachedJSONL(
	sourceApp SourceApp,
	root string,
	files func(scanRoot string) ([]string, error),
	extract func(path string) (UsageHourBuckets, error),
) (*UsageSourceStats, error) {
	if !pathExists(root) {
		return nil, nil
	}

	today := TodayPrefix()
	buckets := UsageHourBuckets{}
	sessionCount := 0
	todaySessionCount := 0

	paths, err := files(root)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		mtime, err := support.FileModifiedTimestampMillis(path)
		if err != nil {
			logger.Error(fmt.Sprintf("usage stats skipped %s: %v", path, err))
			continue
		}

		var fileHours UsageHourBuckets
		if cached, ok := UsageDayLoad[UsageHourBuckets](sourceApp, path, mtime); ok {
			fileHours = cached
		} else {
			parsed, err := extract(path)
			if err != nil {
				logger.Error(fmt.Sprintf("usage stats skipped %s: %v", path, err))
				continue
			}
			UsageDayStore(sourceApp, path, mtime, parsed)
			fileHours = parsed
		}

		if fileHours.IsEmpty() {
			continue
		}

		sessionCount++
		if fileHasToday(fileHours, today) {
			todaySessionCount++
		}
		MergeBuckets(buckets, fileHours)
	}

	stats := sourceStats(sourceApp, buckets, sessionCount, todaySessionCount)
	return &stats, nil
}

// DaysFromPaths：根目录不存在表示来源不可用；文件列表由来源自带，与列表
// 扫描同一发现规则，单文件失败只记日志跳过。
func DaysFromPaths(
	sourceApp SourceApp,
	root string,
	paths func() ([]string, error),
	extract func(path string) (UsageHourBuckets, error),
) (*UsageSourceStats, error) {
	if !pathExists(root) {
		return nil, nil
	}

	today := TodayPrefix()
	buckets := UsageHourBuckets{}
	sessionCount := 0
	todaySessionCount := 0

	list, err := paths()
	if err != nil {
		return nil, err
	}
	for _, path := range list {
		fileHours, err := extract(path)
		if err != nil {
			logger.Error(fmt.Sprintf("usage stats skipped %s: %v", path, err))
			continue
		}

		if fileHours.IsEmpty() {
			continue
		}

		sessionCount++
		if fileHasToday(fileHours, today) {
			todaySessionCount++
		}
		MergeBuckets(buckets, fileHours)
	}

	stats := sourceStats(sourceApp, buckets, sessionCount, todaySessionCount)
	return &stats, nil
}

// SqlUsageHours：SQL 来源（opencode/zcode）的小时桶与两级会话数；db 缺失
// 表示来源不可用。
type SqlUsageHours struct {
	Buckets           UsageHourBuckets `json:"buckets"`
	SessionCount      int              `json:"sessionCount"`
	TodaySessionCount int              `json:"todaySessionCount"`
}

// cachedSQLUsage：SQL 来源的持久缓存值——整份统计加采集日期。「今日会话数」
// 是采集时刻的口径，跨午夜后必须重算，日期不符即按 miss 处理。
type cachedSQLUsage struct {
	Day   string        `json:"day"`
	Hours SqlUsageHours `json:"hours"`
}

// UsageColumns：SQL 来源行级用量的原始列——各来源 SQL 只产原始列，口径统一
// 在这里，改口径（如将来加 token 桶）只改这一处。输入列的语义因来源而异：
// zcode 的 input_tokens 是全口径（未命中 + 缓存读），opencode 的 tokens.input
// 已不含缓存读，因此入口分两个函数显式声明。
type UsageColumns struct {
	Input      int64
	Output     int64
	Reasoning  int64
	CacheRead  int64
	CacheWrite int64
}

// SplitReasoningUsage：opencode 形态——输入列已不含缓存读，只并 output +
// reasoning，负值钳 0。
func SplitReasoningUsage(columns UsageColumns) SessionTokenUsage {
	return SessionTokenUsage{
		InputTokens:      u64ClampNonNegative(columns.Input),
		OutputTokens:     u64ClampNonNegative(columns.Output + columns.Reasoning),
		CacheReadTokens:  u64ClampNonNegative(columns.CacheRead),
		CacheWriteTokens: u64ClampNonNegative(columns.CacheWrite),
	}
}

// FullScopeUsage：zcode 形态——输入列是全口径、含缓存读，先拆掉缓存读再走
// 同一口径，对齐 SessionTokenUsage 的 input 不含命中。
func FullScopeUsage(columns UsageColumns) SessionTokenUsage {
	columns.Input -= columns.CacheRead
	return SplitReasoningUsage(columns)
}

func u64ClampNonNegative(value int64) uint64 {
	if value < 0 {
		return 0
	}
	return uint64(value)
}

// UsageRow：SQL 来源行级用量的一行（session_id, 毫秒时间戳, 模型名, 归一后
// 用量）；模型列取不到时为空串，只进总量。
type UsageRow struct {
	SessionID string
	Timestamp int64
	Model     *string
	Usage     SessionTokenUsage
}

// HoursFromUsageRows：SQL 来源行级用量归小时桶的公共尾部——两级会话数与桶
// 合并。
func HoursFromUsageRows(rows []UsageRow) (SqlUsageHours, error) {
	today := TodayPrefix()
	buckets := UsageHourBuckets{}
	sessions := map[string]bool{}
	todaySessions := map[string]bool{}

	for _, row := range rows {
		key := HourKey(row.Timestamp)
		if key != nil {
			if hour, ok := TodayHour(*key, today); ok {
				_ = hour
				todaySessions[row.SessionID] = true
			}
		}
		sessions[row.SessionID] = true
		model := ""
		if row.Model != nil {
			model = *row.Model
		}
		MergeUsageEvent(buckets, key, model, row.Usage)
	}

	return SqlUsageHours{
		Buckets:           buckets,
		SessionCount:      len(sessions),
		TodaySessionCount: len(todaySessions),
	}, nil
}

// DaysFromSQL：SQL 来源的采集主入口。db 是 SQL 来源唯一的数据源，mtime 作
// 缓存键；db 缺失时没有键，交给 collect 判定来源不可用，不写缓存。
func DaysFromSQL(
	sourceApp SourceApp,
	db func() (string, error),
	collect func() (*SqlUsageHours, error),
) (*UsageSourceStats, error) {
	type cacheKey struct {
		path  string
		mtime int64
	}
	var key *cacheKey
	if dbPath, err := db(); err == nil {
		if mtime, err := support.FileModifiedTimestampMillis(dbPath); err == nil {
			key = &cacheKey{path: dbPath, mtime: mtime}
		}
	}

	if key != nil {
		if cached, ok := UsageDayLoad[cachedSQLUsage](sourceApp, key.path, key.mtime); ok && cached.Day == TodayPrefix() {
			stats := cachedSQLUsageToStats(sourceApp, cached)
			return &stats, nil
		}
	}

	hours, err := collect()
	if err != nil {
		return nil, err
	}
	if hours == nil {
		return nil, nil
	}
	cached := cachedSQLUsage{Day: TodayPrefix(), Hours: *hours}

	if key != nil {
		UsageDayStore(sourceApp, key.path, key.mtime, cached)
	}

	stats := cachedSQLUsageToStats(sourceApp, cached)
	return &stats, nil
}

func cachedSQLUsageToStats(sourceApp SourceApp, cached cachedSQLUsage) UsageSourceStats {
	return sourceStats(sourceApp, cached.Hours.Buckets, cached.Hours.SessionCount, cached.Hours.TodaySessionCount)
}

func sourceStats(sourceApp SourceApp, buckets UsageHourBuckets, sessionCount int, todaySessionCount int) UsageSourceStats {
	today := TodayPrefix()
	return UsageSourceStats{
		SourceApp:         sourceApp,
		Days:              dayPoints(buckets),
		TodayHours:        todayHourPoints(buckets, today),
		SessionCount:      sessionCount,
		TodaySessionCount: todaySessionCount,
		ModelDays:         modelDayPoints(buckets),
		ModelTodayHours:   modelTodayHourPoints(buckets, today),
	}
}

// dayPoints：小时桶按天前缀合并成日序列（按天升序，对齐 BTreeMap 迭代序）。
func dayPoints(buckets UsageHourBuckets) []UsageDayPoint {
	days := map[string]*SessionTokenUsage{}
	var dayKeys []string
	for _, hourKey := range buckets.SortedKeys() {
		day := dayOfDay(hourKey)
		if _, seen := days[day]; !seen {
			dayKeys = append(dayKeys, day)
			days[day] = &SessionTokenUsage{}
		}
		bucket := buckets[hourKey]
		days[day].Accumulate(&bucket.Usage)
	}

	sort.Strings(dayKeys)
	points := make([]UsageDayPoint, 0, len(dayKeys))
	for _, day := range dayKeys {
		points = append(points, UsageDayPoint{Day: day, Usage: *days[day]})
	}
	return points
}

// todayHourPoints：今日窗口的小时序列（按小时键升序）。
func todayHourPoints(buckets UsageHourBuckets, today string) []UsageHourPoint {
	points := make([]UsageHourPoint, 0)
	for _, hourKey := range buckets.SortedKeys() {
		hour, ok := TodayHour(hourKey, today)
		if !ok {
			continue
		}
		bucket := buckets[hourKey]
		points = append(points, UsageHourPoint{Hour: hour, Usage: bucket.Usage})
	}
	return points
}

// modelDayPoints：模型切片的日序列——同一模型跨小时合并成天，按 (model, day)
// 排序，前端按模型分组消费。
func modelDayPoints(buckets UsageHourBuckets) []UsageModelDayPoint {
	type modelDay struct {
		model string
		day   string
	}
	entries := map[modelDay]*SessionTokenUsage{}
	var keys []modelDay
	for _, hourKey := range buckets.SortedKeys() {
		// Rust 侧 split_once('T') 失配的小时键不进模型日序列。
		if !strings.Contains(hourKey, "T") {
			continue
		}
		bucket := buckets[hourKey]
		for model, usage := range bucket.Models {
			key := modelDay{model: model, day: dayOfDay(hourKey)}
			if _, seen := entries[key]; !seen {
				keys = append(keys, key)
				entries[key] = &SessionTokenUsage{}
			}
			entries[key].Accumulate(&usage)
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].day < keys[j].day
	})
	points := make([]UsageModelDayPoint, 0, len(keys))
	for _, key := range keys {
		points = append(points, UsageModelDayPoint{Model: key.model, Day: key.day, Usage: *entries[key]})
	}
	return points
}

// modelTodayHourPoints：模型维度的今日小时切片，按 (model, hour) 排序。
func modelTodayHourPoints(buckets UsageHourBuckets, today string) []UsageModelHourPoint {
	type modelHour struct {
		model string
		hour  uint8
	}
	entries := map[modelHour]*SessionTokenUsage{}
	var keys []modelHour
	for _, hourKey := range buckets.SortedKeys() {
		hour, ok := TodayHour(hourKey, today)
		if !ok {
			continue
		}
		bucket := buckets[hourKey]
		for model, usage := range bucket.Models {
			key := modelHour{model: model, hour: hour}
			if _, seen := entries[key]; !seen {
				keys = append(keys, key)
				entries[key] = &SessionTokenUsage{}
			}
			entries[key].Accumulate(&usage)
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].model != keys[j].model {
			return keys[i].model < keys[j].model
		}
		return keys[i].hour < keys[j].hour
	})
	points := make([]UsageModelHourPoint, 0, len(keys))
	for _, key := range keys {
		points = append(points, UsageModelHourPoint{Model: key.model, Hour: key.hour, Usage: *entries[key]})
	}
	return points
}

// dayOfDay：从 "YYYY-MM-DDTHH" 取日期段。
func dayOfDay(hourKey string) string {
	day, _, _ := strings.Cut(hourKey, "T")
	return day
}

// TodayHour："YYYY-MM-DDTHH" 命中今天时取出小时数；跨午夜后旧前缀自然失配。
func TodayHour(hourKey string, today string) (uint8, bool) {
	rest, ok := strings.CutPrefix(hourKey, today)
	if !ok {
		return 0, false
	}
	hour, ok := strings.CutPrefix(rest, "T")
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseUint(hour, 10, 8)
	if err != nil {
		return 0, false
	}
	return uint8(value), true
}

func fileHasToday(buckets UsageHourBuckets, today string) bool {
	for key := range buckets {
		if _, ok := TodayHour(key, today); ok {
			return true
		}
	}
	return false
}

// MergeBuckets：文件级桶并入总量。
func MergeBuckets(total UsageHourBuckets, next UsageHourBuckets) {
	for hourKey, bucket := range next {
		entry := total[hourKey]
		entry.Accumulate(&bucket)
		total[hourKey] = entry
	}
}

// MergeUsageEvent：消耗事件归桶入口——时间戳缺失的记录不进曲线（总量仍由
// 列表 summary 保证）；model 为空串表示该事件取不到模型信息，只进总量。
func MergeUsageEvent(buckets UsageHourBuckets, hourKey *string, model string, usage SessionTokenUsage) {
	if hourKey == nil {
		return
	}
	entry := buckets[*hourKey]
	entry.AccumulateEvent(model, &usage)
	buckets[*hourKey] = entry
}

// HourKey：天/小时边界取本地时区，与用户在 CLI 工具里看到的活跃日期一致。
// 时间戳非法（超出 time.Time 表达范围）时 nil，对齐 chrono from_timestamp_millis。
func HourKey(timestampMillis int64) *string {
	value := time.UnixMilli(timestampMillis)
	key := value.Format("2006-01-02T15")
	return &key
}

// TodayPrefix：本地时区的今天（YYYY-MM-DD）。
func TodayPrefix() string {
	return time.Now().Format("2006-01-02")
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
