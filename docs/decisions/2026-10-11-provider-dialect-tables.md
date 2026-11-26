# providers 方言表：归一化枚举拼写的单一来源

- 日期：2026-10-11
- 状态：生效中
- 2026-11-26 更新：Grok 的 `max` 已按 `reasoning_efforts` 菜单独立成档，`ReasoningDialect` 的 `lossy` 折叠机制随之移除；路径与测试名已同步为 Go 实现（`internal/providers/dialect.go`、`provider_dialects_test.go`）。

## 背景

收编前，`ProviderProtocol` / `ReasoningLevel` 两个归一化枚举到各工具 wire 拼写的知识，散在六个适配器各自的正反函数对里（`api_name`/`protocol_from_*`、`thinking_level`/`*_from_str`、`reasoning_effort_write` 覆写）。一个适配器作者须手工维持 2-4 个同源点，任何一处脱钩只表现为前端展示错或反读丢失：

- **活 bug**：dsh 未覆写 `reasoning_effort_write`，默认实现按 `as_str()` 把 off 档下发为 "none"，而 apply 实际写 "off"（pi-ai 方言）——应用弹窗的映射文案渲染成 `off→none`，方向反了。测试无 `reasoning_level_writes` 断言，漏网。
- **字节级重复**：pi 与 dsh 的协议表与档位函数完全相同（dsh 的 LLM 层就是 llm-pi-ai），同一份知识两个文件各一份。
- **接口装不下**：opencode 对 anthropic 协议写的是 thinking 预算 JSON 而非字符串，`reasoning_effort_write(level)` 签名没有协议参数。

## 决策

1. **方言表单一来源**：新增 `internal/providers/dialect.go`，声明式 `ReasoningDialect { spells }` 与 `ProtocolDialect { spells, parse_aliases }`，全部为包级常量（共享方言保证单一实例，按指针相等守卫）。`ReasoningDialect` 曾有 `lossy` 折叠字段，2026-11-26 随 Grok `max` 独立成档移除。
2. **接缝**：`AppAdapter` 删除 `reasoning_effort_write` 默认方法，新增 `ReasoningDialect()`（六家必填）与 `ProtocolDialect()`（Claude 配置里没有协议字段、语义固定，返回 nil；其余返回实例）。`EmptyState` 的 `reasoning_level_writes` 从方言派生，dsh bug 随之消失。
3. **pi-ai 方言共享**：`piAIProtocol` / `piAIReasoning` 由 Pi 与 dsh 两个适配器引用同一实例，roundtrip 测试按包级变量指针相等守卫。
4. **opencode 结构化例外**：opencode 的档位方言只覆盖 openai 系包的 `settings.reasoningEffort` 字符串；anthropic 包的 thinking 预算阶梯不经此表，在 `reasoning_dialect` 覆写处以注释显式标注。DTO 不变，弹窗「实际写入值」提示对 anthropic 协议不适用是已知限制，待真实需求再扩契约（不在本次扩 `reasoning_level_writes` 按协议下发）。
5. **反读归一口径逐字保留**：方言 `Parse` 沿 `ParseReasoningLevel` 的 trim + 小写归一，工具拼写之外回落归一化拼写（覆盖外部配置里可能出现、Reins 未必主动产出的取值，如历史 grok `max`、codex 历史档位）。评审讨论阶段曾预估要收紧 pi/dsh 对大写 "OFF" 的容忍，实现时确认无需收紧——原归一化本身就是小写归一，行为零变化。
6. **roundtrip 守卫**：`internal/providers/provider_dialects_test.go` 按适配器遍历——支持档位/协议的 `parse(spell(x)) == x`、dsh off 回归、pi/dsh 同实例、opencode v1 npm 包名只读别名、未知拼写留空。Grok `max` 已是独立档位，不再有有损折叠豁免。
7. **映射取值**：2026-09-24 plan §6 的各工具映射取值（Pi off→off、OpenCode 预算阶梯、Claude 收窄值域、codex off 不放行）原样保留；Grok `max` 自 2026-11-26 起按模型菜单独立成档，只有无等级元数据的旧式兜底折算 `xhigh`（见验证文档 2026-11-26 补充）。该 plan 中「`reasoning_effort_write` 必须与 apply 同源」的注释不变量由方言表结构性与 roundtrip 测试替代。

## 2026-10-12 补记:写入路径闭合

roundtrip 守卫只测「方言表自身 spell↔parse 互逆」,测不到 apply 真的用了
spell——codex 三处(catalog 的档位清单与默认档、config.toml 的
model_reasoning_effort)与 opencode 两处(reasoningEffort 的写与读)当时
仍用 `ReasoningLevel::as_str`/`parse` 直拼,IDENTITY 方言下数值恰好相等,
不是 live bug,但方言表一旦出现非恒等拼写即脱钩(重演 dsh off→none)。
现已全部改走 `dialect().spell`/`dialect().parse`(自由函数由 apply/inspect
传入),守卫范围闭合;pi 的 thinkingLevelMap 值侧是独立第二套拼写(API
effort),已在代码注释声明不得顺手统一。

## 不在本次范围

- grok 的 `grok_base_url` 折算与 `grok_providers_view` 克隆视图（base_url 比较口径问题，属条目归类策略，另行处理）。
- `ModelField::ReasoningLevels` 等零适配器引用的能力表留白（plan 文档已声明的 unverified 项）。

## 验证

- `cargo test provider_dialects`：4/4。
- `cargo test providers`：113/113（含 `applied_reasoning_level_is_read_back_for_each_app` 全工具 apply→inspect 反读闭环、`pi_off_level_writes_off`）。
- 全量 `cargo test`：402 通过；6 个失败为 develop@030f89f 上的存量失败（agents 快照 ×2、pi active_branch、session_delete_plan ×3，Windows 环境相关，已用 stash 对照确认与本次无关）。
