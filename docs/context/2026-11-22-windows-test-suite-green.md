# Windows 测试套件全绿的排障记录

- 日期：2026-11-22
- 状态：已完成（`go test ./internal/...` 在 Windows 本机首次全绿）
- 背景：Go/Wails 迁移后测试在 Unix 开发机上验收，Windows 本机长期 109+ 红灯。
  本文记录根因与修法，供后续「在 Windows 上跑测试 / 写夹具 / 动链接层」时对照。

## 根因清单（按修法归类）

### 1. 测试收尾必须先释放 SQLite 连接（夹具纪律）

Windows 删不掉仍被打开的文件：`t.TempDir` 清理时若 SQLite 连接未 Close，
`unlinkat` 报 "being used by another process"，测试即红（Unix 允许删打开中的
文件，所以只在 Windows 爆）。修法（约 109 例一并治愈）：

- `homeGuard`（session 包）收尾注册 `closeReaderSlots`：关闭全部全局读取器
  槽位（FamilyReader ×5、PiReaderInstance）与 usage-day 进程级连接；
- `engineAt` 夹具：引擎直构统一走它，收尾注册 `engine.Close`（在构造助手
  函数里 `defer` 会在函数返回时过早关闭，必须 `t.Cleanup`）；
- 直构 db 的测试（rows/summary）`defer db.Close()`；`SummaryDbAtStoreDirMust`
  注册 `t.Cleanup`。

### 2. junction 的两个生产 bug（link_windows.go）

skill 分发在 Windows 上此前实际不可用：

- **reparse 缓冲区**：substitute/print name 必须是 NUL 结尾宽串，长度不含
  NUL、偏移与 DataLength 含（`PrintNameOffset` 跨过 substitute 的 NUL）。
  少了 NUL 或偏移不含 NUL，NTFS 报 "data present in the reparse point
  buffer is invalid"。以 `mklink /J` 落盘的字节为对照逐字节校准。
- **句柄形态**：新内核（26300 起）不接受「OPEN_ALWAYS 建目录的同一句柄直接
  FSCTL_SET_REPARSE_POINT」（报 ERROR_DIRECTORY）。必须先 `os.MkdirAll`
  （顺带解决父目录不存在）再以 OPEN_EXISTING 重开句柄。junction crate 宣称
  的 OPEN_ALWAYS 行为在该形态上已失效。

### 3. `os.ReadDir` 对「路径是文件」在 Windows 报 NotExist 类错误

`enumerateLinks`（skill_install.go）原先把 `os.ErrNotExist` 一律当「目录不存
在=空清单」，Windows 上 skill_dir 是真实文件时被静默吞成空。修法：NotExist
分支用 `os.Stat` 复核确实不存在才按空处理，否则上报。

### 4. 路径断言的表达式要对齐生产的拼接语义（约 15 例测试移植 bug）

生产侧三种拼接、分隔符形态各不相同，期望表达式用错即在 Windows 红灯：

| 生产路径 | 语义 | Windows 形态 |
| --- | --- | --- |
| `GlobalRoot.Resolve` / `ResolvePath`（env.Resolve 兜底） | home + 平台分隔符 + 清单拼写 | `home\.pi/agent`（内嵌 `/` 保留） |
| workspace `joinPath`（项目级布局/相对 config） | 同上 | `root\project\.grok/skills` |
| `support.JoinManifestRelative` | pushPath 逐段 | 清洗为平台分隔符 |
| `DefaultConfigTemplate` 落盘 | `ToSlash` 归一（可移植） | 全 `/` |
| TOML 基本字符串值 | 反斜杠转义 | `\\`（断言要匹配转义形） |

判别口诀：先跑出 actual，再对齐到生产那条拼接链的同语义表达式；不要随手
`filepath.Join`。

### 5. 其余单点

- **`comparePaths`**：`splitPathComponents` 原按 `filepath.Separator` 切分，
  Windows 下转录里的 `/` 路径整串成分量，与 Rust `PathBuf` Ord 契约分叉；
  修为 `/` 与 `\` 都认（Rust 在 Windows 本就双分隔符）。
- **`parseGitOwnerRepo`**：Windows 本地路径反斜杠先归一 `/` 再取 owner/repo，
  否则本地 git 源（fetch-cancel 系测试）解析失败。
- **logger 测试**：Windows 的 `UserConfigDir` 读 `%AppData%` 而非 HOME，
  夹具需一并重定向 APPDATA，否则把真实应用日志读进断言。
- **符号链接特权**：无开发者模式/管理员时 `os.Symlink` 报
  ERROR_PRIVILEGE_NOT_HELD（0x522=**1314**，注意不是 1313）。夹具不可满足
  的前提以 `t.Skipf` 显式跳过（grokbuild 符号链接防线测试）。
- **`OpenCodeUsageHours` 连接泄漏（生产）**：函数级 `RowsDbAtStoreDir()` 句柄
  用完必须 `defer cache.Close()`——每次用量采集泄漏一条 SQLite 连接。

## 相关决策

- 槽位/连接的关闭纪律与换根重建同一条路径（`closeReaderSlots` 只清槽位，
  下个测试按新根重建），见 reader-engine 决策的「换根重建」口径。
- junction 修复不动安装面决策的判定边界，只修 Windows 物理层实现。
