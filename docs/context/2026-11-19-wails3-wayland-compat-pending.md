# Wails3 迁移待真机复验：Linux Wayland 兼容环境变量

- 日期：2026-11-19
- 状态：待确认（挂起，用户明确当前无法处理）
- 来源：Rust/Tauri2 → Go/Wails3 迁移收尾检查；决策记录见 [go-wails3-migration](../decisions/2026-11-19-go-wails3-migration.md)

## 背景

Rust 版在 Linux 上为 WebKitGTK 预设两项兼容性环境变量（是渲染 workaround，不是后端选择）。
迁移到 Wails3 后该逻辑未随迁，Linux Wayland 会话下的表现未经真机复验。

## 旧行为（原 src-tauri/src/main.rs，cfg(target_os = "linux")）

1. `WAYLAND_DISPLAY` 存在且用户未设 `WEBKIT_DISABLE_DMABUF_RENDERER` 时，设为 `1`——
   规避 WebKitGTK DMABUF 渲染器在部分 Wayland 合成器上的花屏/崩溃。
2. 存在 `/proc/driver/nvidia/version` 且用户未设 `__NV_DISABLE_EXPLICIT_SYNC` 时，设为 `1`——
   规避 NVIDIA explicit sync 下的渲染异常。
3. 两项都尊重用户已设值，不覆盖。

## 待确认项

1. Wails3 构建在 Linux Wayland 会话（GNOME / KDE / wlroots 类合成器）下窗口渲染与输入是否正常；
   注意 Wails3 默认 GTK 版本与 `-tags gtk3` 回退开关（见迁移决策记录），与 Rust 时期的
   WebKitGTK 版本不同，变量是否仍生效以实测为准。
2. NVIDIA + Wayland 组合是否需要恢复 `__NV_DISABLE_EXPLICIT_SYNC`。
3. 若确认需要恢复：在 `main.go` 组装 app 之前按旧逻辑预设（仅 Linux 构建标签生效、
   尊重用户已设值）；完成后更新本文状态与迁移决策记录。
