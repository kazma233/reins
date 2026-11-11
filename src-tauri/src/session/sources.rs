//! 来源注册表:一个会话来源的全部静态分发知识(根目录、探测文案、读取器、
//! 用量采集方式)收敛为一张表。detect / available / clear /
//! usage 四条流程都由它驱动;新增来源 = 新 reader 文件 + 这里一条注册,
//! 按来源的 match 不得在 catalog.rs / usage_stats.rs / mod.rs 复活。

use std::path::PathBuf;
use std::thread;

use anyhow::{Result, anyhow};

use super::model::{SourceApp, UsageSourceStats};
use super::usage_stats::{self, UsageKind};
use super::{SessionReader, claude_code, codex, dsh, grokbuild, opencode, pi, zcode};

/// 纯静态分发数据,按值复制进注册表。
#[derive(Clone, Copy)]
pub(crate) struct SourceSpec {
    pub(crate) app: SourceApp,
    /// 来源根目录,detect 与 available 的可用性判断共用。
    pub(crate) root: fn() -> Result<PathBuf>,
    /// detect 结果里的说明文案;grokbuild 沿用无说明。
    pub(crate) detect_note: Option<&'static str>,
    pub(crate) reader: &'static dyn SessionReader,
    pub(crate) usage: UsageKind,
}

impl SourceSpec {
    pub(crate) fn usage_days(&self) -> Result<Option<UsageSourceStats>> {
        let root = (self.root)()?;

        match &self.usage {
            UsageKind::CachedJsonl {
                subdir,
                files,
                extract,
            } => {
                let scan_root = match subdir {
                    Some(subdir) => root.join(subdir),
                    None => root,
                };
                usage_stats::days_from_cached_jsonl(self.app, scan_root, *files, *extract)
            }
            UsageKind::RawFiles { paths, extract } => {
                usage_stats::days_from_paths(self.app, &root, *paths, *extract)
            }
            UsageKind::Sql { db, collect } => usage_stats::days_from_sql(self.app, *db, *collect),
        }
    }
}

static CODEX: SourceSpec = SourceSpec {
    app: SourceApp::Codex,
    root: codex::root,
    detect_note: Some("Using ~/.codex/sessions as the primary transcript source."),
    reader: &codex::BACKEND,
    usage: UsageKind::CachedJsonl {
        subdir: Some("sessions"),
        // codex 的转录可能是压缩形态,归档会话与活跃会话一同计入用量。
        files: codex::transcript_paths,
        extract: codex::usage_hours,
    },
};

static CLAUDE_CODE: SourceSpec = SourceSpec {
    app: SourceApp::ClaudeCode,
    root: claude_code::root,
    detect_note: Some("Reading project-level session JSONL files."),
    reader: &claude_code::BACKEND,
    usage: UsageKind::CachedJsonl {
        subdir: Some("projects"),
        files: crate::support::fs::enumerate_jsonl_files,
        extract: claude_code::usage_hours,
    },
};

static OPENCODE: SourceSpec = SourceSpec {
    app: SourceApp::OpenCode,
    root: opencode::root,
    detect_note: Some("Reading sessions from ~/.local/share/opencode/opencode.db."),
    reader: &opencode::BACKEND,
    usage: UsageKind::Sql {
        db: opencode::db_path,
        collect: opencode::usage_hours,
    },
};

static PI: SourceSpec = SourceSpec {
    app: SourceApp::Pi,
    root: pi::sessions_root,
    detect_note: Some("Reading ~/.pi/agent/sessions JSONL session files."),
    reader: &pi::BACKEND,
    usage: UsageKind::CachedJsonl {
        subdir: None,
        files: crate::support::fs::enumerate_jsonl_files,
        extract: pi::usage_hours,
    },
};

static GROKBUILD: SourceSpec = SourceSpec {
    app: SourceApp::GrokBuild,
    root: grokbuild::root,
    detect_note: None,
    reader: &grokbuild::BACKEND,
    usage: UsageKind::RawFiles {
        paths: grokbuild::session_summary_paths,
        extract: grokbuild::usage_hours,
    },
};

static ZCODE: SourceSpec = SourceSpec {
    app: SourceApp::Zcode,
    root: zcode::root,
    detect_note: Some("Reading sessions from ~/.zcode/cli/db/db.sqlite."),
    reader: &zcode::BACKEND,
    usage: UsageKind::Sql {
        db: zcode::db_path,
        collect: zcode::usage_hours,
    },
};

static DSH: SourceSpec = SourceSpec {
    app: SourceApp::Dsh,
    root: dsh::sessions_root,
    detect_note: Some("Reading ~/.dsh/sessions transcript files."),
    reader: &dsh::BACKEND,
    // dsh 的 usage 与列表同源(逐转录归桶),转录文件 mtime 未变即可复用
    // 持久桶缓存,免去每次整份解压;归档账本不影响 usage,不参与失效。
    usage: UsageKind::CachedJsonl {
        subdir: None,
        files: dsh::session_transcript_paths_at,
        extract: dsh::usage_hours,
    },
};

// 顺序是可观察行为:detect 输出与 usage sources 都按该顺序产出,不得随意
// 重排;与 sources_registry_covers_every_source_app_once_in_order 测试互为约束。
pub(crate) static SOURCES: &[SourceSpec] =
    &[CODEX, CLAUDE_CODE, OPENCODE, PI, GROKBUILD, ZCODE, DSH];

pub(crate) fn spec(app: SourceApp) -> &'static SourceSpec {
    match app {
        SourceApp::Codex => &CODEX,
        SourceApp::ClaudeCode => &CLAUDE_CODE,
        SourceApp::OpenCode => &OPENCODE,
        SourceApp::Pi => &PI,
        SourceApp::GrokBuild => &GROKBUILD,
        SourceApp::Zcode => &ZCODE,
        SourceApp::Dsh => &DSH,
    }
}

/// 按 SOURCES 声明顺序并行采集每个来源并同序返回;线程 panic 折叠为携带
/// label 的错误,与逐来源 spawn 时的文案一致。
pub(crate) fn parallel_collect<T, F>(label: &'static str, collect: F) -> Vec<Result<T>>
where
    F: Fn(&SourceSpec) -> Result<T> + Send + Sync,
    T: Send,
{
    thread::scope(|scope| {
        let collect = &collect;
        let handles: Vec<_> = SOURCES
            .iter()
            .map(|spec| scope.spawn(move || collect(spec)))
            .collect();

        handles
            .into_iter()
            .map(|handle| {
                handle
                    .join()
                    .map_err(|_| anyhow!("{label} thread panicked"))?
            })
            .collect()
    })
}
