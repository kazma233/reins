// providers.yaml 存储层测试：往返、校验、CRUD（对齐 tests/providers_config.rs）。
package providers

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reins/internal/agents"
	"reins/internal/testsupport"
)

func configTestInput(id string) ProviderUpsertInput {
	input := providerInput(id, ProtocolOpenaiChatCompletions, "https://"+id+".test/v1")
	input.Models = []ProviderModelInput{{
		ID:    "model-a",
		Label: "Model A",
		// 思考等级乱序 + 重复，落盘前应排序去重。
		ContextWindow:   ptrInt64(128_000),
		SupportsImages:  ptrBool(true),
		Reasoning:       ptrBool(true),
		ReasoningLevels: []ReasoningLevel{ReasoningHigh, ReasoningLow, ReasoningLow},
	}}
	return input
}

func ptrInt64(value int64) *int64 { return &value }
func ptrBool(value bool) *bool    { return &value }

func TestUpsertNormalizesAndRoundTrips(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-store")
	store := NewProviderConfigStoreAt(dir)

	id, err := store.Upsert(configTestInput("OpenRouter"))
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if id != "openrouter" {
		t.Fatalf("平台 ID 应归一化为小写：%s", id)
	}

	providers, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	provider, exists := providers["openrouter"]
	if !exists {
		t.Fatal("provider exists")
	}
	if provider.Label != "Label OpenRouter" {
		t.Fatalf("label: %s", provider.Label)
	}
	if len(provider.Models) != 1 {
		t.Fatalf("models: %d", len(provider.Models))
	}
	// 思考等级去重 + 排序。
	levels := provider.Models[0].ReasoningLevels
	if len(levels) != 2 || levels[0] != ReasoningLow || levels[1] != ReasoningHigh {
		t.Fatalf("reasoning levels 应为 [low high]：%v", levels)
	}

	// 落盘内容是 snake_case YAML，且带版本号。
	raw, err := os.ReadFile(store.ConfigPath())
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "version: 1") || !strings.Contains(text, "context_window: 128000") ||
		!strings.Contains(text, "reasoning_levels:") {
		t.Fatalf("落盘形态不符：\n%s", text)
	}
	// API Key 明文落盘（用户确认的取舍，不加密）。
	if key, ok := provider.StoredAPIKey(); !ok || key != "sk-test-secret" {
		t.Fatalf("stored key: %q %v", key, ok)
	}
	if !strings.Contains(text, "api_key: sk-test-secret") {
		t.Fatalf("api_key 应明文落盘：\n%s", text)
	}
}

// 表单值即落盘值：留空写入即清除 Key，两侧空白不计入。
func TestUpsertOverwritesAndClearsAPIKey(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-store")
	store := NewProviderConfigStoreAt(dir)
	if _, err := store.Upsert(configTestInput("p1")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	renamed := configTestInput("p1")
	renamed.Label = "Renamed"
	if _, err := store.Upsert(renamed); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	providers, _ := store.Load()
	if key, ok := providers["p1"].StoredAPIKey(); !ok || key != "sk-test-secret" {
		t.Fatalf("元数据更新不应清掉已存 Key：%q %v", key, ok)
	}

	blank := configTestInput("p1")
	blank.APIKey = "   "
	if _, err := store.Upsert(blank); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	providers, _ = store.Load()
	if _, ok := providers["p1"].StoredAPIKey(); ok {
		t.Fatal("留空写入应清除已存 Key")
	}
	raw, _ := os.ReadFile(store.ConfigPath())
	if strings.Contains(string(raw), "api_key") {
		t.Fatalf("清除后不应再落 api_key 键：\n%s", raw)
	}
}

func TestUpsertRejectsInvalidInput(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-store")
	store := NewProviderConfigStoreAt(dir)

	bad := configTestInput("ok-id")
	bad.ProviderID = "reins-x"
	if _, err := store.Upsert(bad); err == nil {
		t.Fatal("reins- 前缀平台 ID 应被拒绝")
	}

	bad = configTestInput("bad id")
	if _, err := store.Upsert(bad); err == nil {
		t.Fatal("带空格的 ID 应被拒绝")
	}

	bad.BaseURL = "ftp://nope"
	bad.ProviderID = "ok-id"
	if _, err := store.Upsert(bad); err == nil {
		t.Fatal("非 http(s) URL 应被拒绝")
	}

	bad = configTestInput("dup-model")
	bad.Models = append(bad.Models, ProviderModelInput{ID: "model-a"})
	if _, err := store.Upsert(bad); err == nil {
		t.Fatal("重复模型 ID 应被拒绝")
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-store")
	store := NewProviderConfigStoreAt(dir)
	if _, err := store.Upsert(configTestInput("p1")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(configTestInput("p2")); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("p1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	providers, _ := store.Load()
	if _, exists := providers["p2"]; !exists {
		t.Fatal("p2 应保留")
	}
	if _, exists := providers["p1"]; exists {
		t.Fatal("p1 应被删除")
	}
	if err := store.Delete("p1"); err == nil {
		t.Fatal("重复删除应报错")
	}
}

func TestMissingFileLoadsAsEmpty(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-store")
	store := NewProviderConfigStoreAt(dir)
	providers, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("缺文件应加载为空：%d", len(providers))
	}
	mustNotExist(t, store.ConfigPath())
}

func TestCreateModeRejectsAnExistingProvider(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-create-conflict")
	store := NewProviderConfigStoreAt(dir)
	if _, err := store.Upsert(configTestInput("p1")); err != nil {
		t.Fatal(err)
	}

	create := configTestInput("p1")
	create.Mode = WriteModeCreate
	create.Label = "覆盖后的名字"

	if _, err := store.Upsert(create); err == nil {
		t.Fatal("新增同 ID 的平台应被拒绝")
	}
	providers, _ := store.Load()
	if providers["p1"].Label != "Label p1" {
		t.Fatal("原有平台不应被新增覆盖")
	}
}

func TestCreateModeAcceptsANewProvider(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-create-new")
	store := NewProviderConfigStoreAt(dir)

	create := configTestInput("p2")
	create.Mode = WriteModeCreate

	id, err := store.Upsert(create)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if id != "p2" {
		t.Fatalf("id: %s", id)
	}
}

func TestUpdateModeRequiresAnExistingProvider(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-update-missing")
	store := NewProviderConfigStoreAt(dir)

	update := configTestInput("missing")
	update.Mode = WriteModeUpdate

	if _, err := store.Upsert(update); err == nil {
		t.Fatal("更新不存在的平台应被拒绝")
	}
}

func TestDefaultUpsertModeStillReplacesInPlace(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-upsert-default")
	store := NewProviderConfigStoreAt(dir)
	if _, err := store.Upsert(configTestInput("p1")); err != nil {
		t.Fatal(err)
	}

	next := configTestInput("p1")
	next.Label = "改名后的平台"
	if _, err := store.Upsert(next); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	providers, _ := store.Load()
	if providers["p1"].Label != "改名后的平台" {
		t.Fatalf("label: %s", providers["p1"].Label)
	}
}

func TestAppReasoningLevelRoundTripsAndSurvivesProviderWrites(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-app-settings")
	store := NewProviderConfigStoreAt(dir)
	if _, err := store.Upsert(configTestInput("p1")); err != nil {
		t.Fatal(err)
	}

	// 未设置时读取为空。
	settings, err := store.AppSettings()
	if err != nil {
		t.Fatalf("app settings: %v", err)
	}
	if len(settings) != 0 {
		t.Fatalf("未设置时应为空：%v", settings)
	}

	// 设置 + 覆盖 + 清除。
	if err := store.SetAppReasoningLevel(agents.ProviderOpencode, levelPtr(ReasoningMax)); err != nil {
		t.Fatal(err)
	}
	settings, _ = store.AppSettings()
	if level := settings[agents.ProviderOpencode].DefaultReasoningLevel; level == nil || *level != ReasoningMax {
		t.Fatalf("default reasoning level: %v", settings[agents.ProviderOpencode].DefaultReasoningLevel)
	}
	if err := store.SetAppReasoningLevel(agents.ProviderOpencode, levelPtr(ReasoningLow)); err != nil {
		t.Fatal(err)
	}
	settings, _ = store.AppSettings()
	if level := settings[agents.ProviderOpencode].DefaultReasoningLevel; level == nil || *level != ReasoningLow {
		t.Fatalf("覆盖后的等级应为 low：%v", settings[agents.ProviderOpencode].DefaultReasoningLevel)
	}
	if err := store.SetAppReasoningLevel(agents.ProviderOpencode, nil); err != nil {
		t.Fatal(err)
	}
	settings, _ = store.AppSettings()
	if len(settings) != 0 {
		t.Fatalf("清除后应为空：%v", settings)
	}

	// 供应商增删改不丢工具级设置（同一份文档读写）。
	if err := store.SetAppReasoningLevel(agents.ProviderOpencode, levelPtr(ReasoningHigh)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(configTestInput("p2")); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("p1"); err != nil {
		t.Fatal(err)
	}
	settings, _ = store.AppSettings()
	if level := settings[agents.ProviderOpencode].DefaultReasoningLevel; level == nil || *level != ReasoningHigh {
		t.Fatalf("供应商增删后设置应保留：%v", settings)
	}
	providers, _ := store.Load()
	if _, exists := providers["p1"]; exists {
		t.Fatal("p1 应已删除")
	}
}

// 存量 providers.yaml 可能没有 app_settings 键（旧版本写入或手工编辑）：
// 解码侧要维持与 emptyDocument 相同的不变量——两个 map 恒非 nil，否则第一次
// 写工具级设置就在 nil map 上 panic。
func TestReasoningLevelOnLegacyFileWithoutAppSettingsKey(t *testing.T) {
	dir := testsupport.TestDir(t, "providers-legacy-settings")
	configPath := filepath.Join(dir, providersConfigName)
	if err := os.WriteFile(configPath, []byte("version: 1\nproviders:\n  p1:\n    label: 平台一\n    protocol: openai_chat_completions\n    base_url: https://p1.test/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewProviderConfigStoreAt(dir)

	settings, err := store.AppSettings()
	if err != nil {
		t.Fatalf("读取存量文件 app settings: %v", err)
	}
	if len(settings) != 0 {
		t.Fatalf("缺 app_settings 键时应读到空：%v", settings)
	}

	if err := store.SetAppReasoningLevel(agents.ProviderOpencode, levelPtr(ReasoningMax)); err != nil {
		t.Fatalf("存量文件上首次设置推理等级: %v", err)
	}
	settings, _ = store.AppSettings()
	if level := settings[agents.ProviderOpencode].DefaultReasoningLevel; level == nil || *level != ReasoningMax {
		t.Fatalf("设置应生效：%v", settings[agents.ProviderOpencode].DefaultReasoningLevel)
	}
	if err := store.SetAppReasoningLevel(agents.ProviderOpencode, nil); err != nil {
		t.Fatalf("存量文件上清除推理等级: %v", err)
	}
	settings, _ = store.AppSettings()
	if len(settings) != 0 {
		t.Fatalf("清除后应为空：%v", settings)
	}
}

// 生产构造落点守卫：providers.yaml 是明文 API Key 文件，reins 段是存储契约
// （AGENTS.md「改名即换存储位置」）。迁移期 configDir 副本漂移曾漏掉该段
// （落点变成 config_dir 根、升级后读不到既有配置），此测试钉住生产路径，
// 不经 At 注入的任何构造改动都会在这里暴露。
func TestProviderConfigStoreProductionLayout(t *testing.T) {
	home := t.TempDir()
	testsupport.SetHome(t, home)

	store := NewProviderConfigStore()
	switch runtime.GOOS {
	case "windows":
		// support.ConfigDir 读 %AppData%（无法在本测试模拟 known-folder 语义），
		// 至少断言 reins 段存在。
		if !strings.HasSuffix(store.ConfigPath(), filepath.Join("reins", "providers.yaml")) {
			t.Fatalf("ConfigPath() = %q, want suffix ...reins/providers.yaml", store.ConfigPath())
		}
	default:
		want := filepath.Join(home, ".config", "reins", "providers.yaml")
		if runtime.GOOS == "darwin" {
			want = filepath.Join(home, "Library", "Application Support", "reins", "providers.yaml")
		}
		if store.ConfigPath() != want {
			t.Fatalf("ConfigPath() = %q, want %q", store.ConfigPath(), want)
		}
		if got := store.AppDir(); got != filepath.Dir(want) {
			t.Fatalf("AppDir() = %q, want %q（models.dev 缓存随此落点）", got, filepath.Dir(want))
		}
	}
}
