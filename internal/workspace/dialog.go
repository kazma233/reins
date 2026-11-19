// dialog.go：rfd 同步文件/目录对话框的端口化。Rust 版在 5 处直接调 rfd
// （选项目目录、选本地来源目录、选 target skills 目录、选 target MCP 配置
// 文件、选批量导入 JSON 文件），同步阻塞无法在测试里驱动；Go 侧收敛成最小
// DialogPort 接口由调用方注入——生产用 Wails 的 app.Dialog 实现，测试用
// fake。接口只承载这 5 处实际用到的能力，不预支 rfd 的完整面。
package workspace

// DialogFilter：文件选择器的扩展名过滤（rfd add_filter 的对应物）。
type DialogFilter struct {
	Label      string
	Extensions []string
}

// DialogOptions：一次选择对话框的全部可配置项；零值即 rfd 默认行为。
type DialogOptions struct {
	// Directory：初始目录（空串 = 调用方自行决定默认目录）。
	Directory string
	// FileName：预填文件名（仅 PickFile；rfd set_file_name）。当前 Wails 适配器
	// 忽略此字段（wails v3.1 无 rfd set_file_name 对应物），仅供测试钉住端口
	// 契约。
	FileName string
	// Filters：文件类型过滤（仅 PickFile）。
	Filters []DialogFilter
}

// DialogPort：同步目录/文件选择的最小端口。ok=false 表示用户取消（rfd 返回
// None 的对应物）；错误（无法打开对话框等）也折叠进 ok=false——rfd 的
// pick_file/pick_folder 本身不区分「取消」与「失败」，返回 None。
type DialogPort interface {
	PickFolder(options DialogOptions) (string, bool)
	PickFile(options DialogOptions) (string, bool)
}
