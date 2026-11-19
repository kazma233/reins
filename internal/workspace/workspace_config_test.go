// workspace_config_test.go：config.yaml 存储层测试（对齐 tests/workspace_config.rs）：
// 删除来源的单/批语义与回滚、target CRUD 的派生与保留规则、并发写互斥、
// 原子写、首启模板落盘。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

// storeWithLocalSources：带若干 local 来源的隔离 store。local 来源让测试不碰
// 网络与真实 git 缓存目录。
func storeWithLocalSources(t *testing.T, root string, sourceIDs ...string) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	var sources strings.Builder
	for _, id := range sourceIDs {
		fmt.Fprintf(&sources, "  - id: %s\n    type: local\n    root_path: %s\n",
			id, filepath.Join(root, "sources", id))
	}
	writeFileT(t, store.ConfigPath(), fmt.Sprintf("skill_sources:\n%s", sources.String()))
	if err := os.MkdirAll(filepath.Join(root, "sources"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return store
}

func readConfigSources(t *testing.T, store *WorkspaceConfigStore) []string {
	t.Helper()
	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	ids := make([]string, 0, len(raw.SkillSources))
	for _, source := range raw.SkillSources {
		ids = append(ids, source.ID)
	}
	return ids
}

func TestDeleteSkillSourceInnerRemovesEntryFromConfigFile(t *testing.T) {
	_, root := newTestStore(t, "config-delete-single")
	store := storeWithLocalSources(t, root, "source-a", "source-b")

	state, err := DeleteSkillSourceInner(store, "source-a")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := readConfigSources(t, store); !equalStrings(got, []string{"source-b"}) {
		t.Fatalf("sources：%v", got)
	}
	if state.Document == nil || state.Document.Config == nil {
		t.Fatal("state carries document")
	}
	if len(state.Document.Config.SkillSources) != 1 {
		t.Fatalf("config view sources：%d", len(state.Document.Config.SkillSources))
	}
	if strings.Contains(readFileT(t, store.ConfigPath()), "source-a") {
		t.Fatal("deleted id must be gone from the file on disk")
	}
}

func TestDeleteSkillSourceInnerRejectsUnknownID(t *testing.T) {
	_, root := newTestStore(t, "config-delete-unknown")
	store := storeWithLocalSources(t, root, "source-a")

	_, err := DeleteSkillSourceInner(store, "ghost")
	if err == nil {
		t.Fatal("unknown source id must fail")
	}
	if !strings.Contains(err.Error(), "未找到来源：ghost") {
		t.Fatalf("error message must stay stable, got: %v", err)
	}
	if got := readConfigSources(t, store); !equalStrings(got, []string{"source-a"}) {
		t.Fatalf("failed delete must not touch the file：%v", got)
	}
}

func TestDeleteSkillSourcesInnerBatchRemovesAllInOneWrite(t *testing.T) {
	_, root := newTestStore(t, "config-delete-batch")
	store := storeWithLocalSources(t, root, "a", "b", "c")

	if err := DeleteSkillSourcesInner(store, []string{"a", "c"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := readConfigSources(t, store); !equalStrings(got, []string{"b"}) {
		t.Fatalf("sources：%v", got)
	}
}

func TestDeleteSkillSourcesInnerRequiresKnownIDs(t *testing.T) {
	_, root := newTestStore(t, "config-delete-batch-unknown")
	store := storeWithLocalSources(t, root, "a")

	if err := DeleteSkillSourcesInner(store, []string{"a", "ghost"}); err == nil {
		t.Fatal("batch with unknown id must fail")
	}

	if got := readConfigSources(t, store); !equalStrings(got, []string{"a"}) {
		t.Fatalf("single write-at-end means a failing batch is rolled back：%v", got)
	}
}

func TestDeleteSkillSourcesInnerRejectsEmptyInput(t *testing.T) {
	_, root := newTestStore(t, "config-delete-empty")
	store := storeWithLocalSources(t, root, "a")

	err := DeleteSkillSourcesInner(store, nil)
	if err == nil {
		t.Fatal("empty batch must fail")
	}
	if !strings.Contains(err.Error(), "source_ids 不能为空") {
		t.Fatalf("error：%v", err)
	}
}

func TestCreateWorkspaceTargetInnerWritesTargetToConfig(t *testing.T) {
	_, root := newTestStore(t, "config-create-target")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "targets: {}\nprojects: {}\nskill_sources: []\nmcps: []\n")

	result, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID:   "codex",
		SkillDir:   filepath.Join(root, "codex-skills"),
		ConfigPath: strPtr("~/.codex/config.toml"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if string(result.TargetID) != "codex" {
		t.Fatalf("target_id：%s", result.TargetID)
	}
	if want := []string{store.ConfigPath()}; !equalStrings(result.UpdatedPaths, want) {
		t.Fatalf("updated_paths：%v", result.UpdatedPaths)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	target, ok := raw.Targets["codex"]
	if !ok {
		t.Fatal("codex target exists")
	}
	if !strings.Contains(target.SkillDir, "codex-skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
}

func TestCreateWorkspaceTargetInnerAllowsTargetWithoutMcpConfig(t *testing.T) {
	_, root := newTestStore(t, "config-create-target-no-mcp")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "targets: {}\nprojects: {}\nskill_sources: []\nmcps: []\n")

	if _, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID: "pi",
		SkillDir: filepath.Join(root, "pi-skills"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	pi, ok := raw.Targets["pi"]
	if !ok {
		t.Fatal("pi target exists")
	}
	if pi.Mcp.ConfigPath != nil || pi.Mcp.ConfigPrefix != nil {
		t.Fatalf("无 MCP 的 target 不应写 mcp 字段：%+v", pi.Mcp)
	}
}

func TestCreateWorkspaceTargetInnerRejectsNonBuiltinID(t *testing.T) {
	_, root := newTestStore(t, "config-create-target-unknown-id")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "targets: {}\nprojects: {}\nskill_sources: []\nmcps: []\n")

	_, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID: "cursor",
		SkillDir: filepath.Join(root, "skills"),
	})
	if err == nil {
		t.Fatal("非内置 target id 必须被拒绝")
	}
	if !strings.Contains(err.Error(), "仅支持创建内置工具的 target") {
		t.Fatalf("错误信息需说明仅支持内置工具，got: %v", err)
	}
	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(raw.Targets) != 0 {
		t.Fatalf("被拒绝的 create 不能写入文件：%+v", raw.Targets)
	}
}

// MCP 配置节点路径是各工具的固定知识，客户端不再传 prefix：create 时
// 只有 prefix_required 的格式（codex/claude/opencode/common）才在 path
// 缺失 + prefix 非空时报错；dsh 这类 prefix 不必填的格式允许无 MCP 配置。
// 旧「codex 显式传 path 但 prefix 由客户端漏传」的失败用例已无意义——
// prefix 现在由 defaults 自动填，落盘后 codex 的 path + mcp_servers
// 是配套的合理状态。
func TestCreateWorkspaceTargetInnerKeepsPrefixInSyncWithPath(t *testing.T) {
	_, root := newTestStore(t, "config-create-target-mcp-pair")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "targets: {}\nprojects: {}\nskill_sources: []\nmcps: []\n")

	// 显式传 path 时，prefix 由 defaults 派生（此处为 codex 的 mcp_servers），
	// 落盘后两者配对——不再是错误。
	if _, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID:   "codex",
		SkillDir:   filepath.Join(root, "skills"),
		ConfigPath: strPtr("~/.codex/config.toml"),
	}); err != nil {
		t.Fatalf("create codex: %v", err)
	}
	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := raw.Targets["codex"].Mcp.ConfigPrefix; got == nil || *got != "mcp_servers" {
		t.Fatalf("codex prefix 应为 mcp_servers：%+v", raw.Targets["codex"].Mcp.ConfigPrefix)
	}
	if got := raw.Targets["codex"].Mcp.ConfigType; got == nil || *got != agents.McpConfigCommon {
		t.Fatalf("codex config_type 应为 common：%+v", raw.Targets["codex"].Mcp.ConfigType)
	}

	// 隐式缺 path 时，create 不假设想要 MCP——prefix 留空，只有 dsh 这类
	// prefix_required=false 的格式天然不报错；common 隐式缺 path 同样不报错
	// （因 prefix 留空、prefix_required 校验无事可校）。
	if _, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID: "zcode",
		SkillDir: filepath.Join(root, "skills"),
	}); err != nil {
		t.Fatalf("create zcode: %v", err)
	}
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if raw.Targets["zcode"].Mcp.ConfigPrefix != nil {
		t.Fatalf("zcode prefix 应缺省：%+v", raw.Targets["zcode"].Mcp.ConfigPrefix)
	}
	if raw.Targets["zcode"].Mcp.ConfigPath != nil {
		t.Fatalf("zcode config_path 应缺省：%+v", raw.Targets["zcode"].Mcp.ConfigPath)
	}
}

// 收紧只影响新建：config.yaml 里已有的自定义 target 仍可照常编辑（改名/改路径）。
func TestUpdateWorkspaceTargetInnerStillAcceptsExistingCustomID(t *testing.T) {
	_, root := newTestStore(t, "config-update-custom-target")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	gatewayConfigPath := filepath.Join(root, "gateway", "config.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  my-gateway:
    skill_dir: %s
    mcp:
      config_path: %s
      config_prefix: mcpServers
      config_type: common
projects: {}
skill_sources: []
mcps: []
`, filepath.Join(root, "gateway-skills"), gatewayConfigPath))

	result, err := UpdateWorkspaceTargetInner(store, "my-gateway", RawTargetInput{
		TargetID:   "my-gateway-2",
		SkillDir:   filepath.Join(root, "gateway-skills-v2"),
		ConfigPath: strPtr(gatewayConfigPath),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if string(result.TargetID) != "my-gateway-2" {
		t.Fatalf("target_id：%s", result.TargetID)
	}
	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := raw.Targets["my-gateway-2"]; !ok {
		t.Fatal("my-gateway-2 exists")
	}
	if _, ok := raw.Targets["my-gateway"]; ok {
		t.Fatal("my-gateway 应已改名")
	}
}

// create 的 MCP 格式从 AgentSpec 派生落盘，客户端不传也不可信客户端。
func TestCreateWorkspaceTargetInnerDerivesConfigTypeFromBuiltinDefaults(t *testing.T) {
	_, root := newTestStore(t, "config-create-target-derives-type")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "targets: {}\nprojects: {}\nskill_sources: []\nmcps: []\n")

	if _, err := CreateWorkspaceTargetInner(store, RawTargetInput{
		TargetID:   "opencode",
		SkillDir:   "skills",
		ConfigPath: strPtr("opencode.json"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := raw.Targets["opencode"].Mcp.ConfigType
	if got == nil || *got != agents.McpConfigOpenCode {
		t.Fatalf("config_type 应从 defaults 派生为 opencode：%+v", got)
	}
}

// update 保留存量存储值：显式 config_type 原样保留，省略仍省略（解析时回落
// defaults 的口径不变）；校验用有效类型与解析侧一致。
func TestUpdateWorkspaceTargetInnerPreservesStoredConfigType(t *testing.T) {
	_, root := newTestStore(t, "config-update-keeps-type")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), `targets:
  custom-dsh:
    skill_dir: custom-dsh/skills
    mcp:
      config_path: custom/patch.yml
      config_type: dsh
  codex:
    skill_dir: codex/skills
    mcp:
      config_path: codex/config.toml
      config_prefix: mcp_servers
projects: {}
skill_sources: []
mcps: []
`)

	if _, err := UpdateWorkspaceTargetInner(store, "custom-dsh", RawTargetInput{
		TargetID:   "custom-dsh",
		SkillDir:   "custom-dsh/skills-v2",
		ConfigPath: strPtr("custom/patch.yml"),
	}); err != nil {
		t.Fatalf("update dsh: %v", err)
	}
	if _, err := UpdateWorkspaceTargetInner(store, "codex", RawTargetInput{
		TargetID:   "codex",
		SkillDir:   "codex/skills-v2",
		ConfigPath: strPtr("codex/config.toml"),
	}); err != nil {
		t.Fatalf("update codex: %v", err)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	dshType := raw.Targets["custom-dsh"].Mcp.ConfigType
	if dshType == nil || *dshType != agents.McpConfigDsh {
		t.Fatalf("显式存量 config_type 必须原样保留：%+v", dshType)
	}
	if raw.Targets["codex"].Mcp.ConfigType != nil {
		t.Fatalf("省略的 config_type 必须保持省略（解析回落 defaults）：%+v", raw.Targets["codex"].Mcp.ConfigType)
	}
}

func TestConcurrentMutationsDoNotLoseUpdates(t *testing.T) {
	_, root := newTestStore(t, "config-concurrent")
	store := storeWithLocalSources(t, root)

	// Mimic two overlapping run_blocking commands: each does its own full
	// read-modify-write, so without the store lock one write would clobber
	// the other.
	var wg sync.WaitGroup
	wg.Add(2)
	for _, id := range []string{"left", "right"} {
		go func(id string) {
			defer wg.Done()
			err := store.Locked(func(lock *ConfigLock) error {
				raw, err := lock.ParseRaw()
				if err != nil {
					return err
				}
				raw.SkillSources = append(raw.SkillSources, &RawSkillSourceConfig{
					ID: id,
					Source: RawSkillSourceDefinition{
						Type:                rawSourceKindLocal,
						RootPath:            "/tmp/" + id,
						Description:         nil,
						IncludeNamePatterns: []string{},
						IncludePathPatterns: []string{},
					},
				})
				return lock.WriteRaw(raw)
			})
			if err != nil {
				t.Errorf("locked(%s): %v", id, err)
			}
		}(id)
	}
	wg.Wait()

	sources := readConfigSources(t, store)
	hasLeft, hasRight := false, false
	for _, id := range sources {
		switch id {
		case "left":
			hasLeft = true
		case "right":
			hasRight = true
		}
	}
	if !hasLeft || !hasRight {
		t.Fatalf("both mutations must survive, got: %v", sources)
	}
}

func TestWriteAtomicLeavesNoTempFilesAndFullContent(t *testing.T) {
	root := testsupport.TestDir(t, "config-atomic")
	target := filepath.Join(root, "agent-config.json")

	if err := support.WriteAtomic(target, "{\n  \"first\": 1\n}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := support.WriteAtomic(target, "{\n  \"first\": 1,\n  \"second\": 2\n}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := readFileT(t, target); got != "{\n  \"first\": 1,\n  \"second\": 2\n}\n" {
		t.Fatalf("second write must fully replace the first：%q", got)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var leftovers []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			leftovers = append(leftovers, entry.Name())
		}
	}
	if len(leftovers) != 0 {
		t.Fatalf("temp files must be gone: %v", leftovers)
	}
}

func TestFirstLoadBootstrapsTemplateThatParses(t *testing.T) {
	store, _ := newTestStore(t, "config-bootstrap")

	if _, err := os.Stat(store.ConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("初始不应有 config.yaml：%v", err)
	}
	document, err := store.LoadDocument()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if _, err := os.Stat(store.ConfigPath()); err != nil {
		t.Fatalf("template must be written on first load：%v", err)
	}
	// First load bootstraps the template and reports exists=true so callers
	// can parse it immediately — no need for a second load to surface config.
	if !document.Exists {
		t.Fatal("document.exists")
	}
	if !document.Validation.Valid {
		t.Fatalf("bootstrapped template must parse：%+v", document.Validation)
	}
	if document.Config == nil || len(document.Config.Targets) != 6 {
		t.Fatalf("template targets：%+v", document.Config)
	}

	again, err := store.LoadDocument()
	if err != nil {
		t.Fatalf("load again: %v", err)
	}
	if again.RawContent != document.RawContent {
		t.Fatal("二次读取内容应一致")
	}
	if !again.Exists || !again.Validation.Valid {
		t.Fatalf("二次读取应有效：%+v", again.Validation)
	}
	if again.Config == nil || len(again.Config.Targets) != 6 {
		t.Fatalf("二次读取 targets：%+v", again.Config)
	}
}

// 生产构造落点守卫：config.yaml 的 reins 段是存储契约（同
// TestProviderConfigStoreProductionLayout 的说明）。
func TestWorkspaceConfigStoreProductionLayout(t *testing.T) {
	home := t.TempDir()
	testsupport.SetHome(t, home)

	store := NewWorkspaceConfigStore()
	switch runtime.GOOS {
	case "windows":
		if !strings.HasSuffix(store.ConfigPath(), filepath.Join("reins", "config.yaml")) {
			t.Fatalf("ConfigPath() = %q, want suffix ...reins/config.yaml", store.ConfigPath())
		}
	default:
		want := filepath.Join(home, ".config", "reins", "config.yaml")
		if runtime.GOOS == "darwin" {
			want = filepath.Join(home, "Library", "Application Support", "reins", "config.yaml")
		}
		if store.ConfigPath() != want {
			t.Fatalf("ConfigPath() = %q, want %q", store.ConfigPath(), want)
		}
	}
}
