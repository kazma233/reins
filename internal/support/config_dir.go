// config_dir.go：应用数据落盘根的目录推导（Rust dirs::config_dir 的对应物）。
// 只承载平台目录语义，无业务知识：不认识 config.yaml/providers.yaml，也不
// 感知测试夹具的 home 覆盖链。
package support

import (
	"os"
	"path/filepath"
	"runtime"
)

// appDirName：reins 的应用数据目录名。写入用户磁盘的名称是产品契约
// （AGENTS.md「改名即换存储位置」），落盘消费方统一经 ConfigDirInApp 取得，
// 不得各自再拼一份 "reins"——迁移期三份 configDir 副本曾漂移漏掉该段。
const appDirName = "reins"

// ConfigDir 对齐 Rust dirs::config_dir 的口径：darwin 固定 HOME/Library/
// Application Support；windows 取 %AppData%（RoamingAppData 的环境变量投影，
// os.UserConfigDir 同款）；其余平台 XDG_CONFIG_HOME 仅在非空绝对路径时生效
// （dirs 拒绝相对值，os.UserConfigDir 不做绝对性检查，这里补齐），否则
// HOME/.config。解析不到 home 时落到 "."（Rust unwrap_or_else(".")）。
//
// 故意读 os.UserHomeDir 的系统 home、不经 UserHomeDir 的测试覆盖链：这条
// 推导服务生产落盘根（日志、config.yaml、providers.yaml），要贴 dirs 的
// 实际行为；需要隔离落点的测试用 NewXxxStoreAt 显式指定目录。
func ConfigDir() string {
	switch runtime.GOOS {
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support")
		}
	case "windows":
		if appData := os.Getenv("AppData"); appData != "" {
			return appData
		}
	default:
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
			return xdg
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".config")
		}
	}
	return "."
}

// ConfigDirInApp：config dir 下的 reins 应用数据目录，sub 段可继续下钻
// （如 logs）。
func ConfigDirInApp(sub ...string) string {
	return filepath.Join(append([]string{ConfigDir(), appDirName}, sub...)...)
}
