# 移除会话删除功能

- 日期：2026-11-11
- 状态：生效中
- 来源：产品定位调整

## 结论

会话域整体移除删除能力，Reins 对历史会话只做统计、查看与分析，不做任何写删动作。

移除的完整面：

1. **后端**：`session/delete.rs`（删除引擎、`DeletePlanInput` 折叠、`run_official_cli`、
   `prune_empty_parents`）、来源注册表的 `DeletePolicy`/`DeleteCopy` 策略轴、
   `SessionReader::delete_plan_input`、五个读取器的删除实现与专属辅助、
   `get_delete_plan` / `delete_session` 两个 command、`DeletePlan` /
   `DeletePlanAction` / `DeleteSessionResult` 三个 DTO。
2. **前端**：详情页删除入口与确认框、`useSessionDetailActions`、api wrapper、
   store 的 plan 状态与 `removeSession`。
3. **测试与夹具**：`tests/session_delete.rs`、`tests/session_delete_plan.rs`、各文件
   散点删除用例，以及只被删除测试消费的假 CLI bin（`src/bin/reins-fake-cli.rs`）
   与 Cargo.toml 的 `default-run`。
4. **ZCode/DSH 的「暂不支持删除」一并消失**：不支持的原因是"不做删除"这一产品
   决策本身，策略轴不存在后该文案没有展示位，不留残骸。

## 关键取舍

- **应用定位是唯一依据**：删除各工具会话属于破坏性动作，且各 CLI 工具自带的
  官方删除能力已经覆盖该需求；Reins 定位为只读的统计查看分析面，不复制这份
  风险。这不是实现层面的取舍，五家可删来源的删除实现（官方命令 + sidecar
  清理）本身工作正常。
- **策略轴整体退役，不保留"仅预演"**：`DeletePolicy` 分裂出 Deleter/Unsupported
  两支的唯一理由是删除能力的有无；删除入口不存在后，注册表回到
  根目录 / 探测 / 读取器 / 用量采集四轴。
- **连带清理不留孤儿**：`reins-fake-cli` bin 只被删除测试消费；`removeSession`、
  `remove_dir_if_exists`、`thread_exists`/`state_dbs`、`grok_binary` 等辅助均无
  第二消费方，随删除实现一起移除。保留的公共件（`SessionIndexState::clear`、
  `rescan_all_caches`、`sourcePaths`、`ConfirmDialog`）均有删除以外的消费方。

## 失效文档

- `2026-10-12-delete-sidecar-deepening.md`（删除侧线收编）整体失效，移入
  `aidocs/archive/`。
- `2026-10-08-cargo-constraints.md` 的 `default-run` 节随 bin 移除删除。
- 契约对照表删除 `get_delete_plan` / `delete_session` 两行与对应 DTO 行。
- `GLOSSARY.md` 删除「删除计划（delete plan）」词条。

## 证据

- 全量 `cargo test` 基线下降约 17 个用例：行为移除后用例随行为一起消失，
  属预期，不是回归。
