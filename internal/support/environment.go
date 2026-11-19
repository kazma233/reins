package support

import (
	"fmt"
	"os"
	"strings"
)

// Redirect 描述一个工具目录重定向的静态知识：官方变量、未重定向时的默认
// 目录、错误文案主语。具体映射（哪个工具认哪个变量、默认目录是什么）由
// internal/agents 的清单持有——GLOSSARY 环境重定向词条的红线：support 只
// 提供解析机制，本包不得再出现任何工具名或变量名，否则就是第二张变量表。
type Redirect struct {
	EnvVar     string // 官方环境变量（如 GROK_HOME）
	DefaultDir string // 未重定向时相对 HOME 的目录名（如 .grok）
	Label      string // 解析失败文案里的工具名主语
}

// Environment：一个进程环境里与工具路径相关的取值。Home 空串表示解析不到
// （Rust Option::None 的零值形态）；values 只含「已设置且非空」的重定向
// 变量（键为 Redirect.EnvVar）。
type Environment struct {
	Home   string
	values map[string]string
}

// FromEnv 读进程环境：home 走 UserHomeDir（测试覆盖生效）；重定向变量空串
// 归一为未设置。redirects 由调用方从 agents 清单取（agents.Redirects()），
// 本包不持有清单。
func FromEnv(redirects []Redirect) *Environment {
	env := &Environment{values: make(map[string]string, len(redirects))}
	if home, ok := UserHomeDir(); ok {
		env.Home = home
	}
	for _, redirect := range redirects {
		if value, ok := os.LookupEnv(redirect.EnvVar); ok && value != "" {
			env.values[redirect.EnvVar] = value
		}
	}
	return env
}

// NewEnvironment 供测试与域内构造：显式指定 home 与重定向取值（键为
// EnvVar）。空串值与 FromEnv 同口径归一为未设置。
func NewEnvironment(home string, values map[string]string) *Environment {
	env := &Environment{Home: home, values: make(map[string]string, len(values))}
	for name, value := range values {
		if value != "" {
			env.values[name] = value
		}
	}
	return env
}

// Value 返回某重定向变量在环境里的取值（未设置返回 false）。
func (e *Environment) Value(redirect Redirect) (string, bool) {
	value, ok := e.values[redirect.EnvVar]
	return value, ok
}

// Resolve：重定向优先，HOME 兜底；兜底不可用时返回 ("", false)。
//
// 兜底目录只做单次拼接、不逐段 join，镜像 Rust Environment::resolve 的
// home.join(default_dir())——Windows 上会保留 DefaultDir 内的 '/'（混合
// 分隔符）。这不是疏漏：该结果多经 canonicalize/path_key 归一后才进比较
// 或展示；逐段 join 的口径只用于清单路径（ManifestPath）。
func (e *Environment) Resolve(redirect Redirect) (string, bool) {
	if value, ok := e.values[redirect.EnvVar]; ok {
		return value, true
	}
	if e.Home == "" {
		return "", false
	}
	return pushPath(e.Home, redirect.DefaultDir), true
}

// ResolveRequired：同 Resolve，但不可解析时报错——调用方要一个确定路径时用。
// 文案与 Rust 逐字一致，主语来自清单（用户看到的错误主语是工具名）。
func (e *Environment) ResolveRequired(redirect Redirect) (string, error) {
	if path, ok := e.Resolve(redirect); ok {
		return path, nil
	}
	return "", fmt.Errorf("无法解析 %s 目录。", redirect.Label)
}

// ManifestPath：清单路径的唯一解析。`~/` 前缀落在 HOME 下（不随该根的
// 重定向搬移），其余相对 root 的解析结果；root 不可用（rootOK=false）时
// 返回 false。workspace 的 defaults 表与 providers 的配置路径都调它，
// `~/` 语义只有这一份。
func ManifestPath(env *Environment, root string, rootOK bool, path string) (string, bool) {
	if homeRelative, found := strings.CutPrefix(path, "~/"); found {
		if env.Home == "" {
			return "", false
		}
		return JoinManifestRelative(env.Home, homeRelative), true
	}
	if !rootOK {
		return "", false
	}
	return JoinManifestRelative(root, path), true
}
