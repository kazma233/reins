// OpenCode 适配器端到级行为（对齐 tests/providers.rs 的 OpenCode 一节）。
package providers

import (
	"path/filepath"
	"strings"
	"testing"
)

func seedOpencodeFile(t *testing.T, home *isolated, index int, content string) string {
	t.Helper()
	path := mustOpencodeCandidatePaths(t, home.env)[index]
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, content)
	return path
}

func TestOpencodeApplyAddsEntryAndRemoveClearsDefaultModel(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{
		"model": "other/model-x",
		"providers": {
			"other": {
				"package": "@opencode/ai/providers/openai",
				"settings": {"baseURL": "https://other.test/v1"}
			}
		}
	}`)
	// chat_completions 平台应用到 OpenCode：写入 v2 规范 providers 节点。
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1", []string{"model-a"})

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, path))
	if jstr(t, applied, "model") != "reins-p1/model-a" {
		t.Fatalf("model: %v", applied["model"])
	}
	if jstr(t, applied, "providers", "reins-p1", "package") != "@opencode/ai/providers/openai-compatible" {
		t.Fatalf("reins 条目 package: %v", jopt(applied, "providers", "reins-p1", "package"))
	}
	if jstr(t, applied, "providers", "other", "package") != "@opencode/ai/providers/openai" {
		t.Fatalf("用户条目应保留：%v", jopt(applied, "providers", "other", "package"))
	}

	// 默认模型仍引用 → 随移除清掉 model 键，条目正常移除，外部条目保留。
	if err := home.removeFrom(t, "p1", agentsOpencode); err != nil {
		t.Fatalf("remove: %v", err)
	}
	removed := mustJSON(t, mustRead(t, path))
	if jopt(removed, "providers", "reins-p1") != nil {
		t.Fatal("reins 条目应被移除")
	}
	if jopt(removed, "providers", "other") == nil {
		t.Fatal("外部条目应保留")
	}
	if jopt(removed, "model") != nil {
		t.Fatalf("默认模型应随移除清掉：%v", removed["model"])
	}
}

// anthropic_messages 平台也允许应用到 OpenCode，package 选 v2 原生
// anthropic 运行时，反读条目回显协议。
func TestOpencodeApplyAnthropicUsesNativeAnthropicPackage(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)
	home.seedProviderWithModels(t, "p1", ProtocolAnthropicMessages, "https://p1.test/anthropic", []string{"model-a"})

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, path))
	if jstr(t, applied, "providers", "reins-p1", "package") != "@opencode/ai/providers/anthropic" {
		t.Fatalf("package: %v", jopt(applied, "providers", "reins-p1", "package"))
	}
	if jstr(t, applied, "model") != "reins-p1/model-a" {
		t.Fatalf("model: %v", applied["model"])
	}

	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	entry := &opencode.Entries[0]
	if entry.Status != EntryStatusApplied {
		t.Fatalf("status: %s", entry.Status)
	}
	if entry.Protocol == nil || *entry.Protocol != ProtocolAnthropicMessages {
		t.Fatalf("protocol: %v", entry.Protocol)
	}
}

// OpenCode 重新应用时未选思考等级：沿用该模型上一次写入的 settings，
// 只换默认模型不会把思考设置写没。
func TestOpencodeReapplyKeepsReasoningSettings(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1", []string{"model-a"})

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	// 反读按同一字段还原等级。
	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	if opencode.DefaultReasoningLevel == nil || *opencode.DefaultReasoningLevel != ReasoningHigh {
		t.Fatalf("反读等级：%v", opencode.DefaultReasoningLevel)
	}

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	reapplied := mustJSON(t, mustRead(t, path))
	if jstr(t, reapplied, "providers", "reins-p1", "models", "model-a", "settings", "reasoningEffort") != "high" {
		t.Fatalf("重应用应沿用上次等级：%v",
			jopt(reapplied, "providers", "reins-p1", "models", "model-a", "settings"))
	}
}

// anthropic 包的思考设置是 thinking 预算，反读要按同一阶梯还原成等级。
func TestOpencodeReadsBackThinkingBudgetLevel(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)
	home.seedProviderWithModels(t, "p1", ProtocolAnthropicMessages, "https://p1.test/anthropic", []string{"model-a"})

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	applied := mustJSON(t, mustRead(t, path))
	if jnum(t, applied, "providers", "reins-p1", "models", "model-a", "settings", "thinking", "budgetTokens") != 16384 {
		t.Fatalf("thinking budget: %v",
			jopt(applied, "providers", "reins-p1", "models", "model-a", "settings", "thinking"))
	}

	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	if opencode.DefaultReasoningLevel == nil || *opencode.DefaultReasoningLevel != ReasoningHigh {
		t.Fatalf("反读等级：%v", opencode.DefaultReasoningLevel)
	}
}

// openai_responses 平台应用到 OpenCode：写入 openai/responses 运行时包
// （compatible/responses 在 opencode v2.0.18 有包解析缺陷，不采用）。
func TestOpencodeApplyResponsesUsesOpenAIResponsesPackage(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiResponses, "https://p1.test/v1", []string{"model-a"})

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, path))
	if jstr(t, applied, "providers", "reins-p1", "package") != "@opencode/ai/providers/openai/responses" {
		t.Fatalf("package: %v", jopt(applied, "providers", "reins-p1", "package"))
	}
	if jstr(t, applied, "model") != "reins-p1/model-a" {
		t.Fatalf("model: %v", applied["model"])
	}

	// 反读自身写入的条目，协议回显为 openai_responses。
	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	entry := &opencode.Entries[0]
	if entry.Status != EntryStatusApplied {
		t.Fatalf("status: %s", entry.Status)
	}
	if entry.Protocol == nil || *entry.Protocol != ProtocolOpenaiResponses {
		t.Fatalf("protocol: %v", entry.Protocol)
	}
}

// 模型级元数据写 v2 的 limit 与 capabilities：官方对目录外模型按 200000
// 上下文 / 32000 输出 / text+image 输入兜底，有元数据就显式声明。
func TestOpencodeApplyWritesModelLimitAndCapabilities(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1", []string{"model-a"})

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, path))
	model := jpath(t, applied, "providers", "reins-p1", "models", "model-a").(map[string]any)
	if jnum(t, model, "limit", "context") != 200_000 || jnum(t, model, "limit", "output") != 64_000 {
		t.Fatalf("limit: %v", model["limit"])
	}
	if jbool(t, model, "capabilities", "tools") != true {
		t.Fatalf("capabilities.tools: %v", jopt(model, "capabilities", "tools"))
	}
	assertJSONStringArray(t, jpath(t, model, "capabilities", "input"), "text", "image")
	assertJSONStringArray(t, jpath(t, model, "capabilities", "output"), "text")
}

// limit 的 context/output 在官方 schema 里成对，缺一项时整段不写；
// supports_images 为假时输入模态收窄为 text。
func TestOpencodeApplyOmitsIncompleteLimitAndNarrowsModalities(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	model := input.Models[0]
	model.ContextWindow = ptrInt64(128_000)
	images := false
	model.SupportsImages = &images
	input.Models = []ProviderModelInput{model}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	applied := mustJSON(t, mustRead(t, path))
	entry := jpath(t, applied, "providers", "reins-p1", "models", "model-a").(map[string]any)
	if jopt(entry, "limit") != nil {
		t.Fatalf("limit 只有一项时整段不写：%v", entry["limit"])
	}
	assertJSONStringArray(t, jpath(t, entry, "capabilities", "input"), "text")
}

// 思考等级：openai 系包写 settings.reasoningEffort（实测请求体带
// reasoning_effort），anthropic 包写 settings.thinking 预算（实测
// reasoningEffort 不上线，thinking 必须带 budgetTokens）。
func TestOpencodeApplyWritesReasoningEffortOrThinkingBudget(t *testing.T) {
	home := newIsolated(t)
	path := seedOpencodeFile(t, home, 0, `{}`)

	home.seedProviderWithModels(t, "oai", ProtocolOpenaiChatCompletions, "https://oai.test/v1", []string{"model-a"})
	home.apply(t, "oai", agentsOpencode, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	applied := mustJSON(t, mustRead(t, path))
	if jstr(t, applied, "providers", "reins-oai", "models", "model-a", "settings", "reasoningEffort") != "high" {
		t.Fatalf("openai 系应写 reasoningEffort：%v",
			jopt(applied, "providers", "reins-oai", "models", "model-a", "settings"))
	}

	home.seedProviderWithModels(t, "ant", ProtocolAnthropicMessages, "https://ant.test/anthropic", []string{"model-a"})
	home.apply(t, "ant", agentsOpencode, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	applied = mustJSON(t, mustRead(t, path))
	thinking := jpath(t, applied, "providers", "reins-ant", "models", "model-a", "settings", "thinking").(map[string]any)
	if jstr(t, thinking, "type") != "enabled" || jnum(t, thinking, "budgetTokens") != 16_384 {
		t.Fatalf("anthropic 包应写 thinking 预算：%v", thinking)
	}

	// 「无」档写入 disabled，不臆造预算。
	home.apply(t, "ant", agentsOpencode, []string{"model-a"}, "model-a", levelPtr(ReasoningOff))
	applied = mustJSON(t, mustRead(t, path))
	thinking = jpath(t, applied, "providers", "reins-ant", "models", "model-a", "settings", "thinking").(map[string]any)
	if jstr(t, thinking, "type") != "disabled" {
		t.Fatalf("off 档应写 disabled：%v", thinking)
	}
	if jopt(thinking, "budgetTokens") != nil {
		t.Fatalf("disabled 不应带预算：%v", thinking)
	}
}

// v2 providers 节点与 v1 遗留 provider 节点都参与反读，
// v2 原生包名与 v1 npm 包名都能反推协议。
func TestOpencodeInspectReadsBothV1AndV2Nodes(t *testing.T) {
	home := newIsolated(t)
	seedOpencodeFile(t, home, 0, `{
		"providers": {
			"gateway-a": {
				"package": "@opencode/ai/providers/anthropic-compatible",
				"settings": {"baseURL": "https://a.test/anthropic"},
				"models": {"m1": {}}
			}
		},
		"provider": {
			"gateway-b": {
				"npm": "@ai-sdk/openai-compatible",
				"options": {"baseURL": "https://b.test/v1"},
				"models": {"m2": {}}
			}
		}
	}`)

	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	if len(opencode.Entries) != 2 {
		t.Fatalf("entries: %d", len(opencode.Entries))
	}
	var a, b *ProviderAppEntry
	for i := range opencode.Entries {
		switch opencode.Entries[i].Key {
		case "gateway-a":
			a = &opencode.Entries[i]
		case "gateway-b":
			b = &opencode.Entries[i]
		}
	}
	if a == nil || b == nil {
		t.Fatalf("entries: %+v", opencode.Entries)
	}
	if a.Status != EntryStatusExternal {
		t.Fatalf("gateway-a status: %s", a.Status)
	}
	if a.Protocol == nil || *a.Protocol != ProtocolAnthropicMessages {
		t.Fatalf("gateway-a protocol: %v", a.Protocol)
	}
	if a.BaseURL == nil || *a.BaseURL != "https://a.test/anthropic" {
		t.Fatalf("gateway-a base url: %v", a.BaseURL)
	}
	if b.Protocol == nil || *b.Protocol != ProtocolOpenaiChatCompletions {
		t.Fatalf("gateway-b protocol: %v", b.Protocol)
	}
}

// 只有 opencode.jsonc（带注释）时：反读可见、apply 写进同一文件，不再
// 另建 opencode.json。
func TestOpencodeReadsAndWritesJSONCWhenOnlyJSONCExists(t *testing.T) {
	home := newIsolated(t)
	jsonc := mustOpencodeCandidatePaths(t, home.env)[1]
	mustMkdirAll(t, filepath.Dir(jsonc))
	mustWrite(t, jsonc, "// 用户注释\n{\"providers\":{\"manual\":{\"name\":\"Manual\",\"package\":\"@opencode/ai/providers/openai-compatible\",\"settings\":{\"baseURL\":\"https://manual.test/v1\"}}}}")
	home.seedModel(t, "p1", "model-a")

	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	if len(opencode.Entries) != 1 {
		t.Fatalf("entries: %d", len(opencode.Entries))
	}
	if opencode.Entries[0].Key != "manual" {
		t.Fatalf("key: %s", opencode.Entries[0].Key)
	}
	if opencode.Entries[0].Status != EntryStatusExternal {
		t.Fatalf("status: %s", opencode.Entries[0].Status)
	}

	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	written := mustJSON(t, mustRead(t, jsonc))
	if jstr(t, written, "model") != "reins-p1/model-a" {
		t.Fatalf("model: %v", written["model"])
	}
	if jopt(written, "providers", "reins-p1") == nil {
		t.Fatal("reins 条目应写进 jsonc")
	}
	// 未另建 opencode.json。
	mustNotExist(t, mustOpencodeCandidatePaths(t, home.env)[0])
}

// 两个文件并存：.jsonc 顶层键整键覆盖 .json；apply 目标选 .jsonc，并把
// 留在 .json 里的旧注册键清掉，避免同一 reins- 条目散在两个文件。
func TestOpencodeJSONCTakesPrecedenceAndApplyMigratesRegistration(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	// 先只在 opencode.json 里应用一次。
	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)

	// 用户随后创建了 opencode.jsonc（只定义 providers 键）。
	jsonc := mustOpencodeCandidatePaths(t, home.env)[1]
	mustWrite(t, jsonc, `{"providers": {"manual": {
		"name": "Manual",
		"package": "@opencode/ai/providers/openai-compatible",
		"settings": {"baseURL": "https://manual.test/v1"}
	}}}`)

	// 反读：.json 里的条目标注被覆盖，不生效。
	state := home.state(t)
	opencode := findAppState(t, state, agentsOpencode)
	var shadowed *ProviderAppEntry
	for i := range opencode.Entries {
		if opencode.Entries[i].Key == "reins-p1" {
			shadowed = &opencode.Entries[i]
			break
		}
	}
	if shadowed == nil {
		t.Fatal("reins-p1 in opencode.json")
	}
	noted := false
	for _, note := range shadowed.Notes {
		if contains(note, "opencode.jsonc") {
			noted = true
			break
		}
	}
	if !noted {
		t.Fatalf("被覆盖条目应注明不生效：%v", shadowed.Notes)
	}

	// 再次应用：写入 .jsonc，.json 里的旧键清掉。
	home.apply(t, "p1", agentsOpencode, []string{"model-a"}, "model-a", nil)
	jsonPath := mustOpencodeCandidatePaths(t, home.env)[0]
	jsonRoot := mustJSON(t, mustRead(t, jsonPath))
	jsoncRoot := mustJSON(t, mustRead(t, jsonc))
	if jopt(jsonRoot, "providers", "reins-p1") != nil {
		t.Fatal(".json 里的旧键应清掉")
	}
	if jopt(jsoncRoot, "providers", "reins-p1") == nil {
		t.Fatal("reins 条目应写进 jsonc")
	}
	if jstr(t, jsoncRoot, "model") != "reins-p1/model-a" {
		t.Fatalf("model: %v", jsoncRoot["model"])
	}

	// 移除：从 .jsonc 清掉注册键与默认模型。
	if err := home.removeFrom(t, "p1", agentsOpencode); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := mustJSON(t, mustRead(t, jsonc))
	if jopt(after, "providers", "reins-p1") != nil {
		t.Fatal("注册键应清掉")
	}
	if jopt(after, "model") != nil {
		t.Fatal("默认模型应清掉")
	}
}

// ---------------------------------------------------------------------------
// 断言小工具
// ---------------------------------------------------------------------------

func assertJSONStringArray(t *testing.T, value any, expected ...string) {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("应为字符串数组：%#v", value)
	}
	if len(array) != len(expected) {
		t.Fatalf("数组长度不符：%#v", array)
	}
	for i, item := range expected {
		if array[i] != item {
			t.Fatalf("第 %d 项应为 %s：%v", i, item, array[i])
		}
	}
}

func contains(text string, needle string) bool {
	return strings.Contains(text, needle)
}
