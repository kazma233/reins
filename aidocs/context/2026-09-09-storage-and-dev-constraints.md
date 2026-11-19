# 工程约束

- 前端引用 `frontend/src/shared` 下的模块一律用 `@shared/...` 别名（vite alias + tsconfig paths 已配置），不写多级相对路径。

## 存储位置契约

以下名称写入用户磁盘或会话数据，属产品契约，改动即换存储位置/标记：

- 应用数据目录：`~/Library/Application Support/reins/`（macOS；Linux 为 `~/.config/reins`、遵循 `XDG_CONFIG_HOME`，Windows 为 `%APPDATA%\reins`；config.yaml、providers.yaml、git cache 所在）
- 会话摘要缓存：`~/.reins/session-summary-cache_v5.db`（`SummaryDBFileName`，`internal/session/summary_cache.go`）
- 会话目录行缓存：`~/.reins/session-rows-cache_v1.db`（`RowsDBFileName`，`internal/session/rows_cache.go`）
- 用量小时桶缓存：`~/.reins/usage-day-cache_v3.db`（`UsageDayDBFileName`，`internal/session/usage_day_cache.go`）
- Git 刷新标记 key：`reins-last-fetch`（`internal/workspace/skills.go`）

2026-09-09 项目由 agent-tools 改名 Reins，存储位置全新启用，旧 `agent-tools` 数据目录与 `.agent-tools` 缓存不做迁移、不做兼容。

2026-11-19 清理：导入器版本标记 `reins/0.1.0` 与 Pi 导入事件 customType `reins-import` / `reins-import-unknown-role` 在 Rust 源码（首提交）与 Go 全树均无代码引用，属文档残留，不再列为存储契约；Rust 时代的格式化工程约束随迁移退役（Go 侧用 `gofmt -w`，见 AGENTS.md 收尾检查）。
