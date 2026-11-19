// 跨工具共用行为：能力表、定点反显一致性、漂移与门控、平台删除、预览
// 脱敏、外部条目（对齐 tests/providers.rs 的对应小节）。
package providers

import (
	"path/filepath"
	"strings"
	"testing"
)

// 应用弹窗的「不写入的模型元数据」清单来自能力表：各工具都写窗口，Claude
// 另写最大输出，OpenCode 与 Pi 与 ZCode 四类全覆盖，Grok 缺最大输出与图像
// 输入，dsh 缺"推理能力"布尔（只能经思考等级字典或显式 false 间接表达）。
func TestAppStatesDeclareUnwrittenModelFields(t *testing.T) {
	home := newIsolated(t)
	state := home.state(t)
	fields := func(app ProviderApp) []string {
		return findAppState(t, state, app).UnwrittenModelFields
	}

	assertStringSlice(t, fields(agentsGrokbuild), "最大输出", "图像输入")
	assertStringSlice(t, fields(agentsCodex), "最大输出", "图像输入", "推理能力")
	assertStringSlice(t, fields(agentsClaude), "图像输入", "推理能力")
	if len(fields(agentsOpencode)) != 0 {
		t.Fatalf("opencode：%v", fields(agentsOpencode))
	}
	if len(fields(agentsPi)) != 0 {
		t.Fatalf("pi：%v", fields(agentsPi))
	}
	// ZCode 四类都有落点：思考档位写 optionSpecs.reasoningLevel.values。
	if len(fields(agentsZcode)) != 0 {
		t.Fatalf("zcode：%v", fields(agentsZcode))
	}
	// dsh 的模型条目（PiAiModelProfile）有 contextWindow/maxTokens/input/
	// reasoningEfforts 落点；"推理能力"布尔本身没有独立字段。
	assertStringSlice(t, fields(agentsDsh), "推理能力")
}

// 反读当前默认思考等级：应用弹窗据此预选，重新应用才不会悄悄改掉它。
func TestAppliedReasoningLevelIsReadBackForEachApp(t *testing.T) {
	cases := []struct {
		app      ProviderApp
		protocol ProviderProtocol
	}{
		{agentsCodex, ProtocolOpenaiResponses},
		{agentsClaude, ProtocolAnthropicMessages},
		{agentsOpencode, ProtocolOpenaiChatCompletions},
		{agentsPi, ProtocolOpenaiChatCompletions},
		{agentsGrokbuild, ProtocolOpenaiChatCompletions},
		{agentsDsh, ProtocolOpenaiChatCompletions},
	}
	for _, testCase := range cases {
		home := newIsolated(t)
		home.seedProvider(t, "p1", testCase.protocol, "https://p1.test/v1")
		home.apply(t, "p1", testCase.app, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))

		state := home.state(t)
		appState := findAppState(t, state, testCase.app)
		if appState.DefaultReasoningLevel == nil || *appState.DefaultReasoningLevel != ReasoningHigh {
			t.Fatalf("%s 反读等级：%v", testCase.app, appState.DefaultReasoningLevel)
		}
	}
}

// 定点反显（写操作后按工具刷新）与全量状态里的同一张卡片一致。
func TestAppStateInnerMatchesFullStateEntry(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", nil)

	full := home.state(t)
	expected := findAppState(t, full, agentsGrokbuild)
	single, err := appStateInner(home.store, home.env, agentsGrokbuild)
	if err != nil {
		t.Fatalf("app state: %v", err)
	}
	if !reflectDeepEqual(*single, *expected) {
		t.Fatalf("定点反显应与全量状态一致：\n%+v\n---\n%+v", *single, *expected)
	}
}

func TestDriftedBaseURLIsReportedAndProtocolGateBlocksApply(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)

	// 平台 base_url 变化 → 反显为漂移。
	home.seedModel(t, "p1", "model-a")
	if _, err := home.store.Upsert(providerInput("p1", ProtocolOpenaiResponses, "https://p1.test/v2")); err != nil {
		t.Fatal(err)
	}
	state := home.state(t)
	codex := findAppState(t, state, agentsCodex)
	if codex.Entries[0].Status != EntryStatusDrifted {
		t.Fatalf("status: %s", codex.Entries[0].Status)
	}

	// 协议不兼容：chat_completions 平台不能应用到 Codex。
	home.seedProvider(t, "p2", ProtocolOpenaiChatCompletions, "https://p2.test/v1")
	_, err := applyProviderInner(home.store, home.env,
		plan("p2", agentsCodex, []string{"model-a"}, "model-a", nil))
	if err == nil {
		t.Fatal("协议不兼容应报错")
	}
	if !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("错误文案不符：%s", err.Error())
	}
}

func TestDeleteProviderBlockedByClaudeReferenceThenSucceeds(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "agg", ProtocolAnthropicMessages, "https://agg.test/api")
	home.apply(t, "agg", agentsClaude, []string{"model-a"}, "model-a", nil)

	_, err := deleteProviderInner(home.store, home.env, "agg")
	if err == nil {
		t.Fatal("Claude 引用中的平台不应允许删除")
	}
	if !strings.Contains(err.Error(), "Claude Code") {
		t.Fatalf("错误应点名 Claude Code：%s", err.Error())
	}

	if err := home.removeFrom(t, "agg", agentsClaude); err != nil {
		t.Fatalf("remove: %v", err)
	}
	result, err := deleteProviderInner(home.store, home.env, "agg")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(result.Detail, "已删除") {
		t.Fatalf("detail: %s", result.Detail)
	}
	state, err := providersStateInner(home.store, home.env)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Providers) != 0 {
		t.Fatalf("providers: %d", len(state.Providers))
	}
	// 明文 Key 随平台条目一起从文件里消失。
	raw := mustRead(t, home.store.ConfigPath())
	if strings.Contains(raw, "sk-test-secret") {
		t.Fatalf("Key 应随删除消失：\n%s", raw)
	}
}

func TestPreviewMasksKeyAndDoesNotWrite(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	preview, err := previewApplyInner(home.store, home.env,
		plan("p1", agentsCodex, []string{"model-a"}, "model-a", nil))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rendered := renderAllPreviews(t, preview)
	if !strings.Contains(rendered, MaskedKey) {
		t.Fatalf("预览应只出现掩码：\n%s", rendered)
	}
	if strings.Contains(rendered, "sk-test-secret") {
		t.Fatal("预览不应泄露明文 Key")
	}
	mustNotExist(t, mustCodexConfigPath(t, home.env))

	// 缺 Key 时预览直接报错。
	home.clearKey(t, "p1")
	if _, err := previewApplyInner(home.store, home.env,
		plan("p1", agentsCodex, []string{"model-a"}, "model-a", nil)); err == nil {
		t.Fatal("缺 Key 预览应报错")
	}
}

// 先 apply 再 preview：旧文件带着上次写入的真实密钥，diff 的
// Context/Remove 行也只允许出现掩码。
func TestPreviewAfterApplyMasksSecretInOldFile(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	raw := mustRead(t, mustCodexConfigPath(t, home.env))
	if !strings.Contains(raw, "sk-test-secret") {
		t.Fatalf("apply 应写入真实密钥：\n%s", raw)
	}

	preview, err := previewApplyInner(home.store, home.env,
		plan("p1", agentsCodex, []string{"model-a"}, "model-a", nil))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	rendered := renderAllPreviews(t, preview)
	if strings.Contains(rendered, "sk-test-secret") {
		t.Fatalf("旧文件里的密钥也应被掩码：\n%s", rendered)
	}
	if !strings.Contains(rendered, MaskedKey) {
		t.Fatalf("diff 应出现掩码：\n%s", rendered)
	}
}

func renderAllPreviews(t *testing.T, preview *ProviderApplyPreview) string {
	t.Helper()
	var builder strings.Builder
	for _, file := range preview.Files {
		for _, line := range file.Diff {
			builder.WriteString(line.Text)
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

func TestDiffLinesMarksAddRemoveContext(t *testing.T) {
	diff := DiffLines("a\nb\nc\n", "a\nx\nc\nd\n")
	type mark struct {
		kind DiffLineKind
		text string
	}
	var marks []mark
	for _, line := range diff {
		marks = append(marks, mark{line.Kind, line.Text})
	}
	expected := []mark{
		{DiffContext, "a"},
		{DiffRemove, "b"},
		{DiffAdd, "x"},
		{DiffContext, "c"},
		{DiffAdd, "d"},
	}
	if len(marks) != len(expected) {
		t.Fatalf("diff：%+v", marks)
	}
	for i, item := range expected {
		if marks[i] != item {
			t.Fatalf("第 %d 行应为 %+v：%+v", i, item, marks[i])
		}
	}
}

func TestRegistrationKeyUsesReinsPrefix(t *testing.T) {
	if got := RegistrationKey("openrouter"); got != "reins-openrouter" {
		t.Fatalf("registration key: %s", got)
	}
}

// ---------------------------------------------------------------------------
// External entries
// ---------------------------------------------------------------------------

func TestCodexRemoveExternalClearsActiveProvider(t *testing.T) {
	home := newIsolated(t)
	path := mustCodexConfigPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, "model_provider = \"my-gateway\"\nmodel_reasoning_effort = \"low\"\nmodel = \"m1\"\n"+
		"[model_providers.my-gateway]\nname = \"Gateway\"\nbase_url = \"https://gw.test/v1\"\n")

	// 活动 Provider 仍指向该条目时随删除清空；model 是裸模型名，保持不动。
	if err := home.removeExternal(t, agentsCodex, "my-gateway"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	content := mustRead(t, path)
	if strings.Contains(content, "my-gateway") {
		t.Fatalf("条目应被删除：\n%s", content)
	}
	root := mustTOML(t, content)
	if _, has := root["model_provider"]; has {
		t.Fatal("model_provider 应随删除清空")
	}
	if _, has := root["model_reasoning_effort"]; has {
		t.Fatal("model_reasoning_effort 应随删除清空")
	}
	if model, ok := root["model"].(string); !ok || model != "m1" {
		t.Fatalf("model 应保持不动：%v", root["model"])
	}
}

func TestOpencodeRemoveExternalClearsDefaultModel(t *testing.T) {
	home := newIsolated(t)
	path := mustOpencodeCandidatePaths(t, home.env)[0]
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, `{
		"model": "my-gateway/m1",
		"provider": {
			"my-gateway": {
				"npm": "@ai-sdk/openai",
				"options": { "baseURL": "https://gw.test/v1" }
			}
		}
	}`)

	// 默认模型指向被删条目时随删除清空。
	if err := home.removeExternal(t, agentsOpencode, "my-gateway"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	removed := mustJSON(t, mustRead(t, path))
	if jopt(removed, "provider", "my-gateway") != nil {
		t.Fatal("条目应被删除")
	}
	if jopt(removed, "model") != nil {
		t.Fatal("默认模型应随删除清空")
	}
}

func TestPiRemoveExternalClearsDefaultProvider(t *testing.T) {
	home := newIsolated(t)
	models := mustPiModelsPath(t, home.env)
	settings := mustPiSettingsPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(models))
	mustWrite(t, models, `{
		"providers": {
			"my-gateway": {
				"api": "openai-completions",
				"baseUrl": "https://gw.test/v1",
				"apiKey": "sk-test"
			}
		}
	}`)
	mustWrite(t, settings, `{"defaultProvider":"my-gateway"}`)

	// 默认 Provider 指向被删条目时随删除清空。
	if err := home.removeExternal(t, agentsPi, "my-gateway"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	content := mustRead(t, models)
	if strings.Contains(content, "my-gateway") {
		t.Fatalf("条目应被删除：\n%s", content)
	}
	removed := mustJSON(t, mustRead(t, settings))
	if jopt(removed, "defaultProvider") != nil {
		t.Fatal("默认 Provider 应随删除清空")
	}
}

func TestGrokRemoveExternalRemovesModelsAndClearsDefault(t *testing.T) {
	home := newIsolated(t)
	path := mustGrokConfigPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, "models.default = \"my-gateway--m1\"\n"+
		"[model_providers.my-gateway]\nname = \"Gateway\"\nbase_url = \"https://gw.test/v1\"\n"+
		"[model.my-gateway--m1]\nmodel_provider = \"my-gateway\"\nmodel = \"m1\"\n"+
		"[model.other--m]\nmodel_provider = \"other\"\nmodel = \"m2\"\n")

	// 默认模型仍引用该条目时随删除清空。
	if err := home.removeExternal(t, agentsGrokbuild, "my-gateway"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	content := mustRead(t, path)
	if strings.Contains(content, "my-gateway") {
		t.Fatalf("条目应被删除：\n%s", content)
	}
	if !strings.Contains(content, "other--m") {
		t.Fatalf("其他条目应保留：\n%s", content)
	}
	root := mustTOML(t, content)
	if jopt(root, "models", "default") != nil {
		t.Fatal("models.default 应随删除清空")
	}
}

// 用户手工写的官方简化式 [model.<键>]（自带 base_url、不挂 model_provider）
// 不落在 model_providers 里，按模型键反显为外部配置；reins- 模型条目仍归入
// 对应 Provider 条目，不重复列出。
func TestGrokStandaloneModelEntryShowsAsExternal(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "acme", ProtocolOpenaiResponses, "https://gw.test/v1")
	path := mustGrokConfigPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, "[model.\"my-model\"]\n"+
		"api_backend = \"responses\"\napi_key = \"sk-test-secret\"\n"+
		"base_url = \"https://gw.test/v1\"\nmodel = \"chat-a\"\nname = \"My Model\"\n"+
		"[model.\"reins-acme--chat-b\"]\n"+
		"model = \"chat-b\"\nmodel_provider = \"reins-acme\"\n"+
		"[model_providers.reins-acme]\nbase_url = \"https://gw.test/v1\"\napi_backend = \"responses\"\n"+
		"[models]\ndefault = \"reins-acme--chat-b\"\n")

	state := home.state(t)
	grok := findAppState(t, state, agentsGrokbuild)
	if len(grok.Entries) != 2 {
		t.Fatalf("entries：%+v", grok.Entries)
	}
	var applied, standalone *ProviderAppEntry
	for i := range grok.Entries {
		switch grok.Entries[i].Key {
		case "reins-acme":
			applied = &grok.Entries[i]
		case "my-model":
			standalone = &grok.Entries[i]
		}
	}
	if applied == nil || standalone == nil {
		t.Fatalf("entries：%+v", grok.Entries)
	}
	if applied.Status != EntryStatusApplied {
		t.Fatalf("status: %s", applied.Status)
	}
	if len(applied.ModelIDs) != 1 || applied.ModelIDs[0] != "chat-b" {
		t.Fatalf("model ids: %v", applied.ModelIDs)
	}
	if applied.DefaultModelID == nil || *applied.DefaultModelID != "chat-b" {
		t.Fatalf("default: %v", applied.DefaultModelID)
	}
	if standalone.Status != EntryStatusExternal {
		t.Fatalf("status: %s", standalone.Status)
	}
	if standalone.BaseURL == nil || *standalone.BaseURL != "https://gw.test/v1" {
		t.Fatalf("base url: %v", standalone.BaseURL)
	}
	if standalone.Protocol == nil || *standalone.Protocol != ProtocolOpenaiResponses {
		t.Fatalf("protocol: %v", standalone.Protocol)
	}
}

// 挂在外部 Provider 条目下的模型条目由该 Provider 条目代表，不单独列为外部条目。
func TestGrokModelMountedOnProviderIsNotListedSeparately(t *testing.T) {
	home := newIsolated(t)
	path := mustGrokConfigPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, "[model_providers.my-gateway]\nbase_url = \"https://gw.test/v1\"\n"+
		"[model.my-gateway--m1]\nmodel_provider = \"my-gateway\"\nmodel = \"m1\"\n")

	state := home.state(t)
	grok := findAppState(t, state, agentsGrokbuild)
	if len(grok.Entries) != 1 {
		t.Fatalf("entries：%+v", grok.Entries)
	}
	if grok.Entries[0].Key != "my-gateway" {
		t.Fatalf("key: %s", grok.Entries[0].Key)
	}
	if grok.Entries[0].Status != EntryStatusExternal {
		t.Fatalf("status: %s", grok.Entries[0].Status)
	}
}

// 独立模型条目按模型键结构化删除；被 [models].default 引用时一并清默认值，
// 其余模型键与 Provider 条目不受影响。
func TestGrokRemoveExternalRemovesStandaloneModelAndClearsDefault(t *testing.T) {
	home := newIsolated(t)
	path := mustGrokConfigPath(t, home.env)
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, "models.default = \"standalone-a\"\nmodels.default_reasoning_effort = \"high\"\n"+
		"[model.\"standalone-a\"]\nbase_url = \"https://gw.test/v1\"\nmodel = \"chat-a\"\n"+
		"[model.\"standalone-b\"]\napi_key = \"sk-test-secret\"\n"+
		"[model_providers.my-gateway]\nbase_url = \"https://gw.test/v1\"\n")

	if err := home.removeExternal(t, agentsGrokbuild, "standalone-a"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	content := mustRead(t, path)
	if strings.Contains(content, "standalone-a") {
		t.Fatalf("独立模型应被删除：\n%s", content)
	}
	if !strings.Contains(content, "standalone-b") || !strings.Contains(content, "my-gateway") {
		t.Fatalf("其余条目应保留：\n%s", content)
	}
	root := mustTOML(t, content)
	models, ok := root["models"].(map[string]any)
	if !ok {
		t.Fatalf("models 段缺失：%v", root["models"])
	}
	if _, has := models["default"]; has {
		t.Fatal("default 应被清掉")
	}
	if _, has := models["default_reasoning_effort"]; has {
		t.Fatal("default_reasoning_effort 应被清掉")
	}
}

func TestClaudeRemoveExternalUnsupported(t *testing.T) {
	home := newIsolated(t)
	// Claude 外部配置内嵌在 env 中，没有可结构化删除的条目。
	if err := home.removeExternal(t, agentsClaude, "env"); err == nil {
		t.Fatal("Claude 外部条目删除应固定报错")
	}
}

// ---------------------------------------------------------------------------
// 断言小工具
// ---------------------------------------------------------------------------

func assertStringSlice(t *testing.T, got []string, expected ...string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("应为 %v：%v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("应为 %v：%v", expected, got)
		}
	}
}
