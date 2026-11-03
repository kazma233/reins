# 来源 git 拉取支持协作式取消

- 日期：2026-11-03
- 状态：生效中

## 背景

「同步来源」弹窗在 git 来源上的两类操作会发起网络 git 拉取且原先无法中断：

1. 打开弹窗读列表（`get_sync_skill_options` → 缓存过期时 fetch/clone）；
2. 强制拉取（`refresh_git_skill_source`，Force）。

网络慢或不可达时弹窗被 busy 锁死（close-disabled），用户没有任何出路。本地操作（同步/移除/冲突预演的软链接操作）通常毫秒级完成，明确不做取消——取消机制只覆盖网络拉取。

## 机制

- **单槽取消标记**（`state/skill_source_fetch.rs`）：`begin()` 在每次拉取命令开头换新标记并返回 `CancelCheck`，`cancel_skill_source_fetch` command 置位当前标记。UI 由 busy 串行保证同时最多一个网络拉取；被换下的旧拉取自然不可取消，没有在途拉取时 cancel 是无害空操作。
- **子进程边界取消**（`workspace/mod.rs::run_command`）：从 `Command::output()` 改为 spawn + 排空线程读管道（防输出塞满缓冲区死锁）+ 50ms 轮询；先查取消再 `try_wait`，取消时 kill 子进程并以 `Cancelled` 错误返回。stdio 与 `output()` 对齐（stdin 关闭，git 不会停下等凭据输入）。
- **错误识别**：`Cancelled` 是裸错误类型（`support/cancel.rs`），中间层 `?` 透传（含 context 包装后仍可 `is::<Cancelled>()` 识别），只有决定「取消之后怎么办」的层做识别，不做字符串匹配。

## 取消语义

| 场景 | 行为 |
| --- | --- |
| 已有缓存，刷新被取消 | 退回现有缓存（哪怕过期）正常返回数据；marker 不回写（下次仍会重试远端）。本地缓存数据是真实的，只是可能落后。 |
| 首次 clone 被取消（无缓存） | 清理半成品目录（否则残留 `.git` 会让下次把半成品当已有仓库去 fetch），报 `Cancelled`；`refresh_git_skill_source` 捕获后如实返回 `cancelled=true`，不当成功上报。 |
| 拉取刚好在取消瞬间完成 | 按已取消处理且不写 marker——调用方靠它跳过「已拉取」的收尾上报。 |

## UX（用户指定口径）

网络拉取在途时关闭按钮保持可点击；点击关闭弹二次确认（取消拉取并关闭 / 继续拉取）；确认后调用取消命令并关闭弹窗。不做独立的「取消拉取」按钮。本地操作期间关闭按钮维持禁用。

`SourceSyncDialog` 判定「拉取在途」= git 来源 且（选项读取 loading 或 强制拉取 refreshingSource）；同步流程（本地操作）不解锁关闭。

## 已知限制

- kill 只终止 git 父进程；`git-remote-https` 等孙进程依赖管道 EOF 自行退出（毫秒级）。未做进程组 kill（需要平台相关代码），当前规模不值得。
- 半成品清理与残留孙进程存在极小的竞态窗口；清理失败会带上下文如实报错，不静默。
- 导入弹窗（`discover_git_skills`）与批量导入同样走 `clone_git_repo_to_cache`，本次传 `CancelCheck::never()` 暂不参与取消；机制就绪后接入只需 `begin()` + UI 入口。

## 关联

- 契约变更记录见 `2026-05-22-frontend-backend-contracts.md`（`refresh_git_skill_source` 返回值、新增 `cancel_skill_source_fetch`）。
- 行为测试：`src-tauri/src/tests/workspace_fetch_cancel.rs`。
