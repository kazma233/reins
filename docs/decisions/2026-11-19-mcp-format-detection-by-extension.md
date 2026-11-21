# MCP 配置文件格式判定改为仅按扩展名

- 日期：2026-11-19
- 状态：生效中（Go 迁移版落地，Rust 版未回改）
- 来源：Go + Wails v3 迁移评审（用户拍板）

## 结论

MCP 配置文件（target 的 `mcp_config_path`）的格式判定只看扩展名：`.json` → JSON writer、
`.toml` → TOML writer、其余一律报错「无法从文件名推断 MCP 配置格式」。文件不存在与已存在
走同一条判定逻辑，不再做内容探测。

Go 版实现：`internal/workspace` 的格式判定函数（对齐 Rust `detect_mcp_file_format` 的后缀
兜底分支，删除其内容探测分支）。

## 被移除的行为（Rust 现状）

Rust 版（`workspace/mcps.rs` `detect_mcp_file_format`）是两段式：

1. 文件已存在且有内容 → 先按 JSON5 试解析成功归 Json，再按 TOML 试解析成功归 Toml，
   都失败报错；
2. 文件不存在或为空 → 按扩展名推断（仅 `.json`/`.toml`）。

## 关键取舍

- **格式归属文件所有者，不是 Reins 该猜的**：内置 preset 的 MCP 配置路径后缀清一色
  `.json`/`.toml`（`agents.rs` 清单），内容探测对这些场景零增量；自定义 target 指向的文件
  该是什么格式是文件所有者的静态契约。用内容猜格式属于「兜底只让现象消失」反模式，且与
  仓库「没有真实契约支持时不加 fallback」的设计原则相悖。
- **唯一行为增量是显式报错**：自定义 target 指向「奇形怪状后缀但内容可解析」的既有文件
  （如 `mcp-conf`、`.json5`）从静默成功变为报错提示改名。该路径在 Rust 测试中零覆盖
  （`tests/` 无任何内容探测用例），不是受保护的行为分区。
- **读写不对称不变**：读容忍注释/尾逗号（JSONC，见 providers 域 `read_jsonc_object` 的
  Go 等价实现），写是整文件重写为纯 JSON——`.jsonc` 后缀本来就不在新建文件的白名单里，
  本决策不改变这一点。
- **Rust 版未回改**：迁移期 Rust 版与 Go 版按各自口径并存，`src-tauri/` 移除后以本记录
  与 Go 实现为准。

## 失效文档

无（本记录取代 Rust 源码中 `detect_mcp_file_format` 的内容探测语义；Go 侧对应实现注释
引用本记录）。
