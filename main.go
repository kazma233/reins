// main.go：Wails 组装层（对齐 Rust 版 lib.rs run()）。三个业务域
// service + AppInfoService 在此注册；域内运行时状态（SessionIndexState、
// WorkspaceConfigStore、ProviderConfigStore、SkillDiscoveryState、单槽取消）
// 由这里构造注入。前端产物打进二进制，桌面形态保持单文件分发。
package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"

	"reins/internal/logger"
	"reins/internal/providers"
	"reins/internal/session"
	"reins/internal/workspace"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	logger.Init()
	logger.Info("application start")

	app := application.New(application.Options{
		Name:        "Reins",
		Description: "Read and inspect local coding-agent sessions.",
		Services: []application.Service{
			application.NewService(session.NewSessionService(session.NewSessionIndexState())),
			application.NewService(workspace.NewWorkspaceService(
				workspace.NewWorkspaceConfigStore(),
				&workspace.SkillDiscoveryState{},
				&workspace.SkillSourceFetchCancelState{},
				wailsDialogPort{},
			)),
			application.NewService(providers.NewProvidersService(providers.NewProviderConfigStore())),
			application.NewService(&AppInfoService{}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// 尺寸对齐 Tauri 版窗口契约：初始与最小都是 1200x800。
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Reins",
		Width:     1200,
		Height:    800,
		MinWidth:  1200,
		MinHeight: 800,
		URL:       "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
