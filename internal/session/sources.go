package session

import (
	"fmt"
	"sync"

	"reins/internal/support"
)

// sources.go：来源注册表——一个会话来源的全部静态分发知识（根目录、探测
// 文案、读取器、用量采集方式）收敛为一张表。detect / available / clear /
// usage 四条流程都由它驱动；新增来源 = 新 reader 文件 + 这里一条注册，
// 按来源的 match 不得在 catalog / usage_stats / reader 注册表复活。

// UsageKindKind：用量采集的三种接入姿势（归桶机器都在 usage_stats.go，
// 差异只剩函数引用）。
type UsageKindKind uint8

const (
	// UsageCachedJsonl：jsonl 目录扫描 + mtime 命中持久缓存，未变更文件跳过
	// 整文件解析（codex/claude/pi/dsh）。扫描目录是来源根目录或其子目录，
	// 文件发现规则由来源自己声明——codex 的转录可能是压缩形态，归档目录
	// 也算会话。
	UsageCachedJsonl UsageKindKind = iota
	// UsageRawFiles：来源自带的转录文件列表，逐文件直接解析不走持久缓存
	// （grokbuild 的 usage.json、dsh 内嵌 usage 的多帧 zstd）；根目录复用
	// 注册表的 root。
	UsageRawFiles
	// UsageSql：从 sqlite 直查（opencode/zcode）。db 是唯一数据源，其 mtime
	// 作缓存键。
	UsageSql
)

// UsageKind：一个来源的用量采集声明。
type UsageKind struct {
	Kind UsageKindKind
	// CachedJsonl：subdir 相对来源根（nil = 根本身）；files 在扫描根下发现
	// 转录文件；extract 把单文件解析成小时桶。
	Subdir  *string
	Files   func(scanRoot string) ([]string, error)
	Extract func(path string) (UsageHourBuckets, error)
	// RawFiles：paths 由来源自带。
	Paths func() ([]string, error)
	// Sql：db 为模块级 env 版路径；collect 直查并聚合。
	DB      func() (string, error)
	Collect func() (*SqlUsageHours, error)
}

// SourceSpec：注册表的一行——纯静态分发数据。
type SourceSpec struct {
	App SourceApp
	// Root：来源根目录，detect 与 available 的可用性判断共用。
	Root func() (string, error)
	// DetectNote：detect 结果里的说明文案；grokbuild 沿用无说明。
	DetectNote *string
	Reader     Reader
	Usage      UsageKind
}

// UsageDays：按注册的采集方式产出该来源的用量统计；来源不可用时 nil。
func (spec *SourceSpec) UsageDays() (*UsageSourceStats, error) {
	root, err := spec.Root()
	if err != nil {
		return nil, err
	}

	switch spec.Usage.Kind {
	case UsageCachedJsonl:
		scanRoot := root
		if spec.Usage.Subdir != nil {
			scanRoot = joinPath(root, *spec.Usage.Subdir)
		}
		return DaysFromCachedJSONL(spec.App, scanRoot, spec.Usage.Files, spec.Usage.Extract)
	case UsageRawFiles:
		return DaysFromPaths(spec.App, root, spec.Usage.Paths, spec.Usage.Extract)
	case UsageSql:
		return DaysFromSQL(spec.App, spec.Usage.DB, spec.Usage.Collect)
	default:
		return nil, fmt.Errorf("unknown usage kind for %s", spec.App)
	}
}

func detectNotePtr(note string) *string { return &note }

// strPtr：注册表里 Option<&'static str> 字段（Subdir）的构造。
func strPtr(value string) *string { return &value }

// Sources：注册表本体。顺序是可观察行为——detect 输出与 usage sources 都按
// 该顺序产出，不得随意重排；与注册表覆盖性测试互为约束。
var Sources = []*SourceSpec{
	{
		App:        SourceCodex,
		Root:       CodexRoot,
		DetectNote: detectNotePtr("Using ~/.codex/sessions as the primary transcript source."),
		Reader:     CodexReader,
		Usage: UsageKind{
			Kind:    UsageCachedJsonl,
			Subdir:  strPtr("sessions"),
			Files:   CodexTranscriptPaths,
			Extract: CodexUsageHours,
		},
	},
	{
		App:        SourceClaudeCode,
		Root:       ClaudeCodeRoot,
		DetectNote: detectNotePtr("Reading project-level session JSONL files."),
		Reader:     ClaudeCodeReader,
		Usage: UsageKind{
			Kind:    UsageCachedJsonl,
			Subdir:  strPtr("projects"),
			Files:   enumerateJSONLAtRoot,
			Extract: ClaudeCodeUsageHours,
		},
	},
	{
		App:        SourceOpenCode,
		Root:       OpenCodeRoot,
		DetectNote: detectNotePtr("Reading sessions from ~/.local/share/opencode/opencode.db."),
		Reader:     OpenCodeReader,
		Usage: UsageKind{
			Kind:    UsageSql,
			DB:      OpenCodeDBPath,
			Collect: OpenCodeUsageHours,
		},
	},
	{
		App:        SourcePi,
		Root:       PiSessionsRoot,
		DetectNote: detectNotePtr("Reading ~/.pi/agent/sessions JSONL session files."),
		Reader:     PiReaderInstance,
		Usage: UsageKind{
			Kind:    UsageCachedJsonl,
			Files:   enumerateJSONLAtRoot,
			Extract: PiUsageHours,
		},
	},
	{
		App:    SourceGrokBuild,
		Root:   GrokBuildRoot,
		Reader: GrokBuildReader,
		Usage: UsageKind{
			Kind:    UsageRawFiles,
			Paths:   GrokSessionSummaryPaths,
			Extract: GrokUsageHours,
		},
	},
	{
		App:        SourceZcode,
		Root:       ZcodeRoot,
		DetectNote: detectNotePtr("Reading sessions from ~/.zcode/cli/db/db.sqlite."),
		Reader:     ZcodeReader,
		Usage: UsageKind{
			Kind:    UsageSql,
			DB:      ZcodeDBPath,
			Collect: ZcodeUsageHours,
		},
	},
	{
		App:        SourceDsh,
		Root:       DshSessionsRoot,
		DetectNote: detectNotePtr("Reading ~/.dsh/sessions transcript files."),
		Reader:     DshReader,
		// dsh 的 usage 与列表同源（逐转录归桶），转录文件 mtime 未变即可复用
		// 持久桶缓存，免去每次整份解压；归档账本不影响 usage，不参与失效。
		Usage: UsageKind{
			Kind:    UsageCachedJsonl,
			Files:   DshSessionTranscriptPathsAt,
			Extract: DshUsageHours,
		},
	},
}

// enumerateJSONLAtRoot：claude/pi 的文件发现——直接枚举扫描根下的 .jsonl。
// codex 不走这里：它的转录可能是压缩形态，由自家 rollouts 枚举声明。
func enumerateJSONLAtRoot(scanRoot string) ([]string, error) {
	return support.EnumerateJSONLFiles(scanRoot), nil
}

// Spec：来源 → 注册表条目。match 与表同源：表按引用登记，这里校验每个
// 变体都路由到描述自己的条目（未登记 panic，对齐 Rust 的穷尽 match）。
func Spec(app SourceApp) *SourceSpec {
	for _, spec := range Sources {
		if spec.App == app {
			return spec
		}
	}
	panic("registered in AgentSpecs")
}

// Outcome：parallel_collect 的按序结果（Rust Result<T> 的 Go 承载）。
type Outcome[T any] struct {
	Value T
	Err   error
}

// ParallelCollect：按 Sources 声明顺序并行采集每个来源并同序返回；goroutine
// panic 折叠为携带 label 的错误，与逐来源 spawn 时的文案一致。
func ParallelCollect[T any](label string, collect func(spec *SourceSpec) (T, error)) []Outcome[T] {
	results := make([]Outcome[T], len(Sources))
	var wg sync.WaitGroup
	for i := range Sources {
		wg.Add(1)
		go func(index int) {
			defer func() {
				if r := recover(); r != nil {
					results[index] = Outcome[T]{Err: fmt.Errorf("%s thread panicked", label)}
				}
				wg.Done()
			}()
			value, err := collect(Sources[index])
			results[index] = Outcome[T]{Value: value, Err: err}
		}(i)
	}
	wg.Wait()
	return results
}
