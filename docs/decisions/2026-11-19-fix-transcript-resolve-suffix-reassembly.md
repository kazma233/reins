# 修正 transcript.resolve 的后缀换拼配方

- 日期：2026-11-19
- 状态：生效中
- 来源：Go 迁移逐字复刻时发现 Rust 缺陷，用户拍板修正

## 结论

`transcript.Resolve`（Rust `transcript.rs` / Go `internal/session/transcript.go`）的换拼
配方修正为与 codex reader 自扫的 shadow 判定一致（`codex.rs` `transcript_paths` 的配方）：

- 记的是压缩路径、磁盘已明文化：剥压缩后缀（`.jsonl.zst`/`.jsonl.zstd`）得 stem，
  **补回 `.jsonl`** 得明文名；
- 记的是明文路径、磁盘已压缩：明文名剥 `.jsonl` 接压缩后缀。

## Rust 缺陷描述（迁移期已逐字复刻并测试钉住）

Rust 版两条分支都还原不出目标：

1. 压缩→明文分支：`strip_suffix(".jsonl.zst")` 得到的是不含 `.jsonl` 的 stem（如
   `rollout.jsonl.zst` 剥出 `rollout`），直接找裸 stem——而明文实际是 `rollout.jsonl`；
2. 明文→压缩分支：`name + ".jsonl.zst"` 拼出 `rollout.jsonl.jsonl.zst`（实际压缩名是
   `rollout.jsonl + ".zst"`）。

即换拼归一从未生效，函数实际语义退化为「存在即原样返回」。同一文件里 codex reader 的
shadow 判定（`codex.rs:330`）用的是正确配方，两处不一致。

## 影响（缺陷存在期间的后果）

按 catalog 记录路径打开转录的三个入口都受影响（转录读取前 resolve、codex mtime 计算前
resolve）。触发条件是索引建立后、读取前文件形态变化：

- 明文→压缩（冷会话压缩，7 天未修改触发）：已索引会话详情打开失败，刷新目录缓存后自愈；
- 压缩→明文（用户续跑会话触发 materialize）：`.zst` 还在则读到旧内容（续跑新消息不显示），
  `.zst` 被删则打开失败；同样刷新自愈。

不是数据损坏，但老会话正是最久未刷新缓存的一批，实际使用会遇到。

## 关键取舍

- **修正而非继续复刻**：迁移验收期按「行为等价优先」逐字复刻并测试钉住；缺陷确认后按
  用户拍板转为修正。修正口径取 codex reader 自己的配方——同一仓库内两处对「压缩↔明文
  文件名变换」的知识必须一致，以 reader 的配方为准（它是真正扫盘的一方）。
- **调用方无需改动**：resolve 语义恢复注释所宣称的「换拼归一」，调用方（转录打开、mtime
  计算）原逻辑受益，无接口变化。
- **行为增量**：修复前「存在即原样」掩盖的路径全部开始真实换拼；不存在形态变化的场景
  （绝大多数）行为不变。
