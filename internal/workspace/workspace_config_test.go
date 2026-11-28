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

func TestCreateSkillPathInnerWritesEntryToConfig(t *testing.T) {
	_, root := newTestStore(t, "config-create-skill-path")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "skill_paths: {}\nmcp_paths: {}\nskill_sources: []\nmcps: []\n")

	result, err := CreateSkillPathInner(store, RawSkillPathInput{
		Name: "codex",
		Dir:  filepath.Join(root, "codex-skills"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if result.PathID != "codex" {
		t.Fatalf("path_id：%s", result.PathID)
	}
	if want := []string{store.ConfigPath()}; !equalStrings(result.UpdatedPaths, want) {
		t.Fatalf("updated_paths：%v", result.UpdatedPaths)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	path, ok := raw.SkillPaths["codex"]
	if !ok {
		t.Fatal("codex skill path exists")
	}
	if !strings.Contains(path.Dir, "codex-skills") {
		t.Fatalf("dir：%s", path.Dir)
	}
}

func TestCreateSkillPathInnerRejectsBlankDirAndDuplicateName(t *testing.T) {
	_, root := newTestStore(t, "config-create-skill-path-invalid")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "skill_paths:\n  codex:\n    dir: codex/skills\nskill_sources: []\nmcps: []\n")

	if _, err := CreateSkillPathInner(store, RawSkillPathInput{Name: "codex", Dir: "another"}); err == nil {
		t.Fatal("重复条目名必须报错")
	}
	if _, err := CreateSkillPathInner(store, RawSkillPathInput{Name: "other", Dir: "  "}); err == nil {
		t.Fatal("空目录必须报错")
	}
	if _, err := CreateSkillPathInner(store, RawSkillPathInput{Name: "  ", Dir: "x"}); err == nil {
		t.Fatal("空条目名必须报错")
	}
}

// 预设名字的 MCP 路径：prefix 省略时按名字回落内置 defaults，落盘后
// config_path + config_prefix 配套。
func TestCreateMcpPathInnerDerivesPrefixFromDefaults(t *testing.T) {
	_, root := newTestStore(t, "config-create-mcp-path-defaults")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "skill_paths: {}\nmcp_paths: {}\nskill_sources: []\nmcps: []\n")

	if _, err := CreateMcpPathInner(store, RawMcpPathInput{
		Name:       "codex",
		ConfigPath: "~/.codex/config.toml",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	path := raw.McpPaths["codex"]
	if path == nil || path.ConfigPrefix == nil || *path.ConfigPrefix != "mcp_servers" {
		t.Fatalf("codex prefix 应为 mcp_servers：%+v", path)
	}
	if path == nil || path.ConfigType == nil || *path.ConfigType != agents.McpConfigCommon {
		t.Fatalf("codex config_type 应为 common：%+v", path)
	}
}

// 自定义名字的 MCP 路径：config_type 由创建时显式给定；prefix_required 的
// 格式缺 prefix 报错，dsh/hermes 这类按结构定位的格式允许空 prefix。
func TestCreateMcpPathInnerPrefixRulesFollowWriterContract(t *testing.T) {
	_, root := newTestStore(t, "config-create-mcp-path-prefix")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "skill_paths: {}\nmcp_paths: {}\nskill_sources: []\nmcps: []\n")

	if _, err := CreateMcpPathInner(store, RawMcpPathInput{
		Name:       "my-gateway",
		ConfigPath: "gateway/config.json",
		ConfigType: "common",
	}); err == nil {
		t.Fatal("common 格式缺 prefix 必须报错")
	}

	if _, err := CreateMcpPathInner(store, RawMcpPathInput{
		Name:         "my-gateway",
		ConfigPath:   "gateway/config.json",
		ConfigType:   "common",
		ConfigPrefix: "mcpServers",
	}); err != nil {
		t.Fatalf("create common with prefix: %v", err)
	}

	// hermes 已并入 common（YAML 载体由扩展名决定）：空 prefix 同样报错。
	if _, err := CreateMcpPathInner(store, RawMcpPathInput{
		Name:       "my-hermes",
		ConfigPath: "hermes/config.yaml",
		ConfigType: "common",
	}); err == nil {
		t.Fatal("common 载体的空 prefix 必须报错")
	}

	if _, err := CreateMcpPathInner(store, RawMcpPathInput{
		Name:       "missing-type",
		ConfigPath: "x.json",
		ConfigType: "nope",
	}); err == nil {
		t.Fatal("未知 config_type 必须报错")
	}
}

// update 保留存量 config_type：格式知识是创建时选定的，更新不换格式。
func TestUpdateMcpPathInnerPreservesStoredConfigType(t *testing.T) {
	_, root := newTestStore(t, "config-update-mcp-path-keeps-type")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), `mcp_paths:
  custom-dsh:
    config_path: custom/patch.yml
    config_type: dsh
skill_sources: []
mcps: []
`)

	if _, err := UpdateMcpPathInner(store, "custom-dsh", RawMcpPathInput{
		Name:       "custom-dsh-2",
		ConfigPath: "custom/patch-v2.yml",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	path, ok := raw.McpPaths["custom-dsh-2"]
	if !ok {
		t.Fatal("custom-dsh-2 exists")
	}
	if path.ConfigType == nil || *path.ConfigType != agents.McpConfigDsh {
		t.Fatalf("存量 config_type 必须原样保留：%+v", path.ConfigType)
	}
}

func TestUpdateSkillPathInnerRenamesEntry(t *testing.T) {
	_, root := newTestStore(t, "config-update-skill-path")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "skill_paths:\n  old-name:\n    dir: old/skills\nskill_sources: []\nmcps: []\n")

	result, err := UpdateSkillPathInner(store, "old-name", RawSkillPathInput{
		Name: "new-name",
		Dir:  "new/skills",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if result.PathID != "new-name" {
		t.Fatalf("path_id：%s", result.PathID)
	}

	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := raw.SkillPaths["new-name"]; !ok {
		t.Fatal("new-name exists")
	}
	if _, ok := raw.SkillPaths["old-name"]; ok {
		t.Fatal("old-name 应已改名")
	}
}

func TestDeleteSkillPathInnerRemovesEntryAndKeepsForeignContent(t *testing.T) {
	_, root := newTestStore(t, "config-delete-skill-path")
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), "skill_paths:\n  mine:\n    dir: mine/skills\nskill_sources: []\nmcps: []\n")
	if err := os.MkdirAll(filepath.Join(root, "mine", "skills", "user-skill"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// cleanup=false：目录里的真实内容原样保留。
	if _, err := DeleteSkillPathInner(store, "mine", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	var raw RawManagerConfig
	if err := unmarshalRawManagerConfig(readFileT(t, store.ConfigPath()), &raw); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(raw.SkillPaths) != 0 {
		t.Fatalf("条目应已删除：%+v", raw.SkillPaths)
	}
	if _, err := os.Stat(filepath.Join(root, "mine", "skills", "user-skill")); err != nil {
		t.Fatalf("cleanup=false 不得动盘上内容：%v", err)
	}

	// 再删除一次：不存在的条目报稳定错误。
	if _, err := DeleteSkillPathInner(store, "mine", false); err == nil ||
		!strings.Contains(err.Error(), "技能路径不存在：mine") {
		t.Fatalf("error：%v", err)
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
	// 新装空开始：模板不预置任何条目（决策记录第 4 条）。
	if document.Config == nil || len(document.Config.SkillPaths) != 0 || len(document.Config.McpPaths) != 0 {
		t.Fatalf("template must start empty：%+v", document.Config)
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
	if again.Config == nil || len(again.Config.SkillPaths) != 0 || len(again.Config.McpPaths) != 0 {
		t.Fatalf("二次读取应保持空开始：%+v", again.Config)
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
