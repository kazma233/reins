// WorkspaceConfigStore：config.yaml 的唯一磁盘交互入口——路径解析、
// read-modify-write 互斥、原子写。对齐 Rust 版 workspace/config.rs：
// 单实例由应用层管理，测试经 NewWorkspaceConfigStoreAt 构造隔离实例；
// WithEnv 返回共享同一份路径的新实例（Rust Arc clone + 新锁语义）。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
)

const defaultConfigName = "config.yaml"

// storeInner 是共享本体：clone 语义下多个句柄指向同一份锁与路径。
type storeInner struct {
	appDir     string
	configPath string
	// env：路径解析用的环境视图（工具目录重定向 + HOME）。生产在应用层读
	// 进程环境；测试用 WithEnv 注入，不依赖进程环境。
	env  *support.Environment
	lock sync.Mutex
}

// WorkspaceConfigStore 是 storeInner 的共享句柄（Rust Arc<StoreInner>）。
type WorkspaceConfigStore struct {
	inner *storeInner
}

// NewWorkspaceConfigStoreAt：测试/注入构造。全部内容（config.yaml、git
// cache）都解析在 appDir 下，不触碰真实用户目录。
func NewWorkspaceConfigStoreAt(appDir string) *WorkspaceConfigStore {
	return &WorkspaceConfigStore{inner: &storeInner{
		appDir:     appDir,
		configPath: filepath.Join(appDir, defaultConfigName),
		env:        &support.Environment{},
	}}
}

// NewWorkspaceConfigStore：生产构造——系统 config dir 下的 reins 目录（目录
// 推导单点在 support.ConfigDirInApp），环境视图读进程环境。
func NewWorkspaceConfigStore() *WorkspaceConfigStore {
	store := NewWorkspaceConfigStoreAt(support.ConfigDirInApp())
	store.inner.env = support.FromEnv(agents.Redirects())
	return store
}

// WithEnv 注入路径解析环境（测试夹具：home 指向临时目录 + 重定向变量）。
// 与 Rust with_env 一致：新实例、新锁，路径共享。
func (s *WorkspaceConfigStore) WithEnv(env *support.Environment) *WorkspaceConfigStore {
	return &WorkspaceConfigStore{inner: &storeInner{
		appDir:     s.inner.appDir,
		configPath: s.inner.configPath,
		env:        env,
	}}
}

// Env 返回路径解析环境视图。
func (s *WorkspaceConfigStore) Env() *support.Environment { return s.inner.env }

// ConfigPath 返回 config.yaml 的绝对路径。
func (s *WorkspaceConfigStore) ConfigPath() string { return s.inner.configPath }

// GitCacheRoot：git 缓存根（app_dir/git）。
func (s *WorkspaceConfigStore) GitCacheRoot() string {
	return filepath.Join(s.inner.appDir, "git")
}

// GitCacheDirForRepo 按 owner/repo 分目录。缓存身份在 import/sync/delete
// 之间必须稳定，所以坏 repo 串直接报错而不是悄悄落到别处。
func (s *WorkspaceConfigStore) GitCacheDirForRepo(repo string) (string, error) {
	owner, name, ok := parseGitOwnerRepo(repo)
	if !ok {
		return "", fmt.Errorf("无法解析 git 仓库 owner/repo：%s", repo)
	}
	return filepath.Join(s.GitCacheRoot(), owner, name), nil
}

// ReadRaw 读 config.yaml 原文；文件不存在是明确错误。
func (s *WorkspaceConfigStore) ReadRaw() (string, error) {
	configPath := s.ConfigPath()
	if _, err := os.Stat(configPath); err == nil {
		content, err := os.ReadFile(configPath)
		if err != nil {
			return "", fmt.Errorf("Failed to read config file %s: %w", configPath, err)
		}
		return string(content), nil
	}
	return "", fmt.Errorf("配置文件不存在：%s", configPath)
}

// Parse 用 store 自带的环境解析配置：配置里写死的绝对路径与清单派生的
// 默认值必须落在同一个 home 口径上。
func (s *WorkspaceConfigStore) Parse() (*ResolvedManagerConfig, error) {
	rawContent, err := s.ReadRaw()
	if err != nil {
		return nil, err
	}
	return parseManagerConfig(rawContent, s.ConfigPath(), s.inner.env)
}

// LoadDocument 读给前端的配置文档；首次启动落默认模板，让用户有东西可编辑。
// 两条路径产出的都是「存在」的文档：文件缺失时先落模板再解析，不存在
// 「缺失文档」的分支。
func (s *WorkspaceConfigStore) LoadDocument() (*WorkspaceConfigDocument, error) {
	configPath := s.ConfigPath()
	rawContent := ""
	if _, err := os.Stat(configPath); err == nil {
		content, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("Failed to read config file %s: %w", configPath, err)
		}
		rawContent = string(content)
	} else {
		rawContent = DefaultConfigTemplate()
		if err := support.WriteAtomic(configPath, rawContent); err != nil {
			return nil, err
		}
	}

	return documentFromContent(s, configPath, rawContent), nil
}

// Locked 把整段 read-modify-write 串行在 store 锁内，并发命令才不会互相
// 覆盖写入。交给 body 的 ConfigLock 是持锁期间写配置文件的唯一合法通道。
// body 返回的错误原样透传；没有 Rust 的锁中毒问题（Go 无此概念）。
func (s *WorkspaceConfigStore) Locked(body func(lock *ConfigLock) error) error {
	s.inner.lock.Lock()
	defer s.inner.lock.Unlock()
	return body(&ConfigLock{store: s})
}

// ConfigLock：锁作用域内的读写句柄。
type ConfigLock struct {
	store *WorkspaceConfigStore
}

// ConfigPath 返回 config.yaml 路径（持锁期间）。
func (l *ConfigLock) ConfigPath() string { return l.store.ConfigPath() }

// Env 返回路径解析环境（持锁期间）。
func (l *ConfigLock) Env() *support.Environment { return l.store.Env() }

// ReadRaw 读 config.yaml 原文（持锁期间）。
func (l *ConfigLock) ReadRaw() (string, error) { return l.store.ReadRaw() }

// ParseRaw 反序列化 raw 文档。故意不加额外 context：serde_yaml 的解析错误
// 本身自描述，这一层从未包过。
func (l *ConfigLock) ParseRaw() (*RawManagerConfig, error) {
	rawContent, err := l.ReadRaw()
	if err != nil {
		return nil, err
	}
	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(rawContent, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

// WriteRaw 序列化 raw 文档并原子落盘（持锁期间）。
func (l *ConfigLock) WriteRaw(config *RawManagerConfig) error {
	serialized, err := marshalRawManagerConfig(config)
	if err != nil {
		return err
	}
	configPath := l.ConfigPath()
	if err := support.WriteAtomic(configPath, serialized); err != nil {
		return fmt.Errorf("Failed to write config file %s: %w", configPath, err)
	}
	return nil
}

// marshalRawManagerConfig：先归一 nil 集合（否则落盘成 null 而不是
// {} / []），再以 serde_yaml 的 2 空格缩进序列化。
func marshalRawManagerConfig(config *RawManagerConfig) (string, error) {
	normalized := normalizeRawManagerConfig(config)
	var builder strings.Builder
	encoder := yaml.NewEncoder(&builder)
	// serde_yaml 缺省缩进 2 空格；yaml.v3 缺省 4，显式对齐落盘形态。
	encoder.SetIndent(2)
	if err := encoder.Encode(normalized); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return builder.String(), nil
}
