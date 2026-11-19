// providers 业务测试夹具：虚拟 HOME 隔离 + providers.yaml 存储桩，对齐
// Rust 版 tests/providers.rs 的 Isolated。它带 providers 域语义
// （store/env 的组合方式即产品装配方式），不进 internal/testsupport。
package providers

import (
	"encoding/json"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

// mustNotExist：断言文件不存在（首次写入前不应创建配置文件等）。
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s 不应存在", path)
	}
}

type isolated struct {
	store   *ProviderConfigStore
	env     *support.Environment
	dataDir string
	homeDir string
}

func newIsolated(t *testing.T) *isolated {
	t.Helper()
	dataDir := testsupport.TestDir(t, "providers-data")
	homeDir := testsupport.TestDir(t, "providers-home")
	testsupport.SetHome(t, homeDir)
	return &isolated{
		store:   NewProviderConfigStoreAt(dataDir),
		env:     support.FromEnv(agents.Redirects()),
		dataDir: dataDir,
		homeDir: homeDir,
	}
}

// envWith：在夹具环境上覆盖一个重定向变量（对齐 Rust
// `Environment { claude_config_dir: Some(dir), ..env.clone() }`）。
func envWith(base *support.Environment, name string, value string) *support.Environment {
	return support.NewEnvironment(base.Home, map[string]string{name: value})
}

func (i *isolated) seedProvider(t *testing.T, id string, protocol ProviderProtocol, baseURL string) {
	t.Helper()
	if _, err := i.store.Upsert(providerInput(id, protocol, baseURL)); err != nil {
		t.Fatalf("seed provider %s: %v", id, err)
	}
}

func (i *isolated) seedModel(t *testing.T, providerID string, modelID string) {
	t.Helper()
	i.seedProviderWithModels(t, providerID, ProtocolOpenaiResponses,
		"https://"+providerID+".test/v1", []string{modelID})
}

func (i *isolated) seedProviderWithModels(t *testing.T, id string, protocol ProviderProtocol, baseURL string, models []string) {
	t.Helper()
	input := ProviderUpsertInput{
		ProviderID: id,
		Mode:       WriteModeUpsert,
		Label:      "Label " + id,
		Protocol:   protocol,
		BaseURL:    baseURL,
		APIKey:     "sk-test-secret",
	}
	for _, modelID := range models {
		input.Models = append(input.Models, fullModelInput(modelID))
	}
	if _, err := i.store.Upsert(input); err != nil {
		t.Fatalf("seed provider %s: %v", id, err)
	}
}

// fullModelInput：元数据齐全的模型夹具（窗口 200000 / 输出 64000 / 图像
// 输入 / 推理 + 三档）。
func fullModelInput(modelID string) ProviderModelInput {
	low, medium, high := ReasoningLow, ReasoningMedium, ReasoningHigh
	window := int64(200_000)
	output := int64(64_000)
	images := true
	reasoning := true
	return ProviderModelInput{
		ID:              modelID,
		Label:           "Label " + modelID,
		ContextWindow:   &window,
		MaxOutputTokens: &output,
		SupportsImages:  &images,
		Reasoning:       &reasoning,
		ReasoningLevels: []ReasoningLevel{low, medium, high},
	}
}

// clearKey：走产品同一条写路径：upsert 留空即清除已存 Key。因为 upsert 是
// 整条替换，这里用 providerInput 的单模型夹具重建输入，只适用于 seedModel
// 造出的平台。
func (i *isolated) clearKey(t *testing.T, providerID string) {
	t.Helper()
	providers, err := i.store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	provider, exists := providers[providerID]
	if !exists {
		t.Fatalf("平台应先 seed：%s", providerID)
	}
	input := providerInput(providerID, provider.Protocol, provider.BaseURL)
	input.APIKey = ""
	if _, err := i.store.Upsert(input); err != nil {
		t.Fatalf("clear key: %v", err)
	}
}

func (i *isolated) apply(t *testing.T, providerID string, app ProviderApp, models []string, defaultModel string, level *ReasoningLevel) {
	t.Helper()
	if _, err := applyProviderInner(i.store, i.env, plan(providerID, app, models, defaultModel, level)); err != nil {
		t.Fatalf("apply %s → %s: %v", providerID, app, err)
	}
}

func (i *isolated) removeFrom(t *testing.T, providerID string, app ProviderApp) error {
	t.Helper()
	_, err := removeProviderFromAppInner(i.store, i.env, providerID, app)
	return err
}

func (i *isolated) removeExternal(t *testing.T, app ProviderApp, entryKey string) error {
	t.Helper()
	_, err := removeExternalEntryInner(i.env, app, entryKey)
	return err
}

func (i *isolated) state(t *testing.T) *ProvidersState {
	t.Helper()
	result, err := providersStateInner(i.store, i.env)
	if err != nil {
		t.Fatalf("providers state: %v", err)
	}
	return result
}

func providerInput(id string, protocol ProviderProtocol, baseURL string) ProviderUpsertInput {
	return ProviderUpsertInput{
		ProviderID: id,
		Mode:       WriteModeUpsert,
		Label:      "Label " + id,
		Protocol:   protocol,
		BaseURL:    baseURL,
		APIKey:     "sk-test-secret",
		Models: []ProviderModelInput{
			{ID: "model-a", Label: "Model A"},
		},
	}
}

func plan(providerID string, app ProviderApp, models []string, defaultModel string, level *ReasoningLevel) *ApplyProviderInput {
	return &ApplyProviderInput{
		ProviderID:            providerID,
		App:                   app,
		ModelIDs:              models,
		DefaultModelID:        defaultModel,
		DefaultReasoningLevel: level,
	}
}

func levelPtr(level ReasoningLevel) *ReasoningLevel {
	return &level
}

// ---------------------------------------------------------------------------
// 域内别名：测试正文用短名指代 7 个工具。
// ---------------------------------------------------------------------------

var (
	agentsCodex     = agents.ProviderCodex
	agentsClaude    = agents.ProviderClaude
	agentsOpencode  = agents.ProviderOpencode
	agentsPi        = agents.ProviderPi
	agentsGrokbuild = agents.ProviderGrokbuild
	agentsDsh       = agents.ProviderDsh
	agentsZcode     = agents.ProviderZcode
)

// ---------------------------------------------------------------------------
// 文件与断言小工具
// ---------------------------------------------------------------------------

func mustRead(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	return string(content)
}

func mustWrite(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s: %v", path, err)
	}
}

func mustWriteTOML(t *testing.T, path string, value map[string]any) {
	t.Helper()
	serialized, err := toml.Marshal(value)
	if err != nil {
		t.Fatalf("序列化 TOML: %v", err)
	}
	mustWrite(t, path, string(serialized))
}

// findAppState：按工具键取状态卡片。
func findAppState(t *testing.T, state *ProvidersState, app ProviderApp) *ProviderAppState {
	t.Helper()
	for i := range state.Apps {
		if state.Apps[i].App == app {
			return &state.Apps[i]
		}
	}
	t.Fatalf("状态里没有 %s 卡片", app)
	return nil
}

// mustXxxPath：路径解析失败即 Fatal 的测试入口（生产路径的两值形态保留）。
func mustCodexConfigPath(t *testing.T, env *support.Environment) string {
	t.Helper()
	path, err := codexConfigPath(env)
	if err != nil {
		t.Fatalf("codex 配置路径: %v", err)
	}
	return path
}

func mustCodexHome(t *testing.T, env *support.Environment) string {
	t.Helper()
	home, err := codexHomeDir(env)
	if err != nil {
		t.Fatalf("codex home: %v", err)
	}
	return home
}

func mustClaudeSettingsPath(t *testing.T, env *support.Environment) string {
	t.Helper()
	path, err := claudeSettingsPath(env)
	if err != nil {
		t.Fatalf("claude settings 路径: %v", err)
	}
	return path
}

func mustOpencodeCandidatePaths(t *testing.T, env *support.Environment) []string {
	t.Helper()
	paths, err := opencodeCandidatePaths(env)
	if err != nil {
		t.Fatalf("opencode 候选路径: %v", err)
	}
	return paths
}

func mustPiModelsPath(t *testing.T, env *support.Environment) string {
	t.Helper()
	path, err := piModelsPath(env)
	if err != nil {
		t.Fatalf("pi models 路径: %v", err)
	}
	return path
}

func mustPiSettingsPath(t *testing.T, env *support.Environment) string {
	t.Helper()
	path, err := piSettingsPath(env)
	if err != nil {
		t.Fatalf("pi settings 路径: %v", err)
	}
	return path
}

func mustGrokConfigPath(t *testing.T, env *support.Environment) string {
	t.Helper()
	path, err := grokConfigPath(env)
	if err != nil {
		t.Fatalf("grok 配置路径: %v", err)
	}
	return path
}

func mustZcodeProviderConfigPath(t *testing.T, env *support.Environment) string {
	t.Helper()
	path, err := zcodeProviderConfigPath(env)
	if err != nil {
		t.Fatalf("zcode 配置路径: %v", err)
	}
	return path
}

// mustWriteJSON：序列化 JSON 树并落盘（与生产 serializeJSON 同形：排序键）。
func mustWriteJSON(t *testing.T, path string, value map[string]any) {
	t.Helper()
	content, err := serializeJSON(value)
	if err != nil {
		t.Fatalf("序列化 JSON: %v", err)
	}
	mustWrite(t, path, content)
}

// renderDiff：把 diff 行按行拼回文本（新增预览即全部内容）。
func renderDiff(t *testing.T, lines []DiffLine) string {
	t.Helper()
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(line.Text)
		builder.WriteString("\n")
	}
	return builder.String()
}

// reflectDeepEqual：结构体深比较；指针字段按指向值比较（两份独立构建的
// 状态各自分配指针，格式化比较会因地址不同误判）。
func reflectDeepEqual(a any, b any) bool {
	return reflect.DeepEqual(a, b)
}

// ---------------------------------------------------------------------------
// 结构化读取辅助：测试断言按解析后的树取值，不按文本形态。
// ---------------------------------------------------------------------------

func mustJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("解析 JSON: %v\n%s", err, text)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("JSON 顶层不是对象：%s", text)
	}
	return object
}

func mustYAML(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := yaml.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("解析 YAML: %v\n%s", err, text)
	}
	return value
}

func mustTOML(t *testing.T, text string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := toml.Unmarshal([]byte(text), &root); err != nil {
		t.Fatalf("解析 TOML: %v\n%s", err, text)
	}
	return root
}

// jpath：按段取 JSON 树的值；任何一段缺失即 Fatal（路径打错立刻暴露）。
func jpath(t *testing.T, value any, path ...string) any {
	t.Helper()
	current := value
	for index, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("JSON 路径 %v 在第 %d 段不是对象", path, index)
		}
		next, has := object[segment]
		if !has {
			t.Fatalf("JSON 路径 %v 缺少段 %q", path, segment)
		}
		current = next
	}
	return current
}

// jopt：路径缺失返回 nil 而不 Fatal（断言「字段不存在」时用）。
func jopt(value any, path ...string) any {
	current := value
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		next, has := object[segment]
		if !has {
			return nil
		}
		current = next
	}
	return current
}

func jstr(t *testing.T, value any, path ...string) string {
	t.Helper()
	text, ok := jpath(t, value, path...).(string)
	if !ok {
		t.Fatalf("JSON 路径 %v 不是字符串：%#v", path, jpath(t, value, path...))
	}
	return text
}

func jnum(t *testing.T, value any, path ...string) int64 {
	t.Helper()
	number, ok := jpath(t, value, path...).(json.Number)
	if !ok {
		t.Fatalf("JSON 路径 %v 不是数字：%#v", path, jpath(t, value, path...))
	}
	parsed, err := number.Int64()
	if err != nil {
		t.Fatalf("JSON 路径 %v 不是整数：%s", path, number)
	}
	return parsed
}

func jbool(t *testing.T, value any, path ...string) bool {
	t.Helper()
	flag, ok := jpath(t, value, path...).(bool)
	if !ok {
		t.Fatalf("JSON 路径 %v 不是布尔：%#v", path, jpath(t, value, path...))
	}
	return flag
}

// ypath / tpath：YAML / TOML 树的取值（解码产物同形：map + 标量）。
func ypath(t *testing.T, value any, path ...string) any {
	return jpath(t, value, path...)
}

func ystr(t *testing.T, value any, path ...string) string {
	return jstr(t, value, path...)
}

func ynum(t *testing.T, value any, path ...string) int64 {
	t.Helper()
	switch typed := jpath(t, value, path...).(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	}
	t.Fatalf("YAML 路径 %v 不是整数：%#v", path, jpath(t, value, path...))
	return 0
}

// assertOwnerOnly：敏感文件（dsh 凭据）必须 0600；非 unix 平台 applyMode
// 是 no-op，守卫跳过（对齐 Rust #[cfg(unix)] 断言）。
func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("credentials 必须是 owner-only，实际 %o", mode)
	}
}
