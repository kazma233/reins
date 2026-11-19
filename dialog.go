// 对话框适配器：workspace.DialogPort 的 Wails 实现。Rust 版经 rfd 同步阻塞
// 选择（pick_folder / pick_file），Wails v3 的对应物是 app.Dialog 的
// OpenFileDialogStruct.PromptForSingleSelection（同样阻塞到用户选择）；返回
// 空串（取消或出错）折叠为 ok=false，与 DialogPort 注释的 rfd 语义一致。
//
// 为什么用 application.Get() 惰性取 app：service 实例在 application.New 之前
// 构造，此时全局实例尚不存在；而前端只能在 app.Run 之后调用 service，取到的
// 一定非 nil。
package main

import (
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"reins/internal/workspace"
)

// wailsDialogPort：无状态适配器；对话框配置每次按调用参数现造。
type wailsDialogPort struct{}

// PickFolder：目录选择。CanChooseDirectories + CanChooseFiles=false 对齐 rfd
// pick_folder（只能选目录）。
func (wailsDialogPort) PickFolder(options workspace.DialogOptions) (string, bool) {
	app := application.Get()
	if app == nil {
		return "", false
	}
	selected, err := app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseDirectories: true,
		CanChooseFiles:       false,
		Directory:            options.Directory,
	}).PromptForSingleSelection()
	if err != nil || selected == "" {
		return "", false
	}
	return selected, true
}

// PickFile：文件选择。Filters 映射为 Wails 的分号分隔通配模式；rfd 的
// set_file_name（预填文件名）在 Wails v3.1 的 OpenFile 选项里没有对应物，
// 只能退化成「打开到所在目录」，预填能力缺失。
func (wailsDialogPort) PickFile(options workspace.DialogOptions) (string, bool) {
	app := application.Get()
	if app == nil {
		return "", false
	}
	wailsOptions := &application.OpenFileDialogOptions{
		CanChooseFiles: true,
		Directory:      options.Directory,
	}
	for _, filter := range options.Filters {
		wailsOptions.Filters = append(wailsOptions.Filters, application.FileFilter{
			DisplayName: filter.Label,
			Pattern:     filterPattern(filter.Extensions),
		})
	}
	selected, err := app.Dialog.OpenFileWithOptions(wailsOptions).PromptForSingleSelection()
	if err != nil || selected == "" {
		return "", false
	}
	return selected, true
}

// filterPattern：DialogFilter 扩展名列表 → "*.json;*.md" 形态的模式串。
func filterPattern(extensions []string) string {
	parts := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		extension = strings.TrimSpace(strings.TrimPrefix(extension, "*."))
		if extension == "" {
			continue
		}
		parts = append(parts, "*."+extension)
	}
	return strings.Join(parts, ";")
}
