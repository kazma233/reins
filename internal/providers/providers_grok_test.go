// Grok Build 适配器端到级行为 + 反显能力表（对齐 tests/providers.rs 的
// Grok Build 与共用一节）。
package providers

import (
	"strings"
	"testing"
)

func TestGrokAnthropicApplyUsesExtraHeadersAndRemoveClearsDefault(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "p1", ProtocolAnthropicMessages, "https://p1.test/v1")

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", nil)
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	for _, expected := range []string{
		"[model_providers.reins-p1]",
		`api_backend = "messages"`,
		// base_url 已带 /v1 时原样写入，不重复追加。
		`base_url = "https://p1.test/v1"`,
		"reins-p1--model-a",
		"extra_headers",
		`x-api-key = "sk-test-secret"`,
		`default = "reins-p1--model-a"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("config.toml 缺少 %s：\n%s", expected, content)
		}
	}
	if strings.Contains(content, "api_key = ") {
		t.Fatalf("anthropic 协议不应写 api_key 字段：\n%s", content)
	}

	// 默认模型仍引用该平台 → 随移除清掉 models.default。
	path := mustGrokConfigPath(t, home.env)
	if err := home.removeFrom(t, "p1", agentsGrokbuild); err != nil {
		t.Fatalf("remove: %v", err)
	}
	content = mustRead(t, path)
	if strings.Contains(content, "reins-p1") {
		t.Fatalf("移除后不应残留条目：\n%s", content)
	}
	root := mustTOML(t, content)
	if jopt(root, "models", "default") != nil {
		t.Fatalf("models.default 应随移除清掉：%v", jopt(root, "models", "default"))
	}
}

// 多提供商并存：重新应用已应用的平台只重写自己的模型条目与默认值，另一个
// 平台的条目保留；界面上的「切换默认模型」正是走这条路径。
func TestGrokReapplySwitchesDefaultAndKeepsOtherProvider(t *testing.T) {
	home := newIsolated(t)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiResponses, "https://p1.test/v1", []string{"m1", "m2"})
	home.seedProviderWithModels(t, "p2", ProtocolOpenaiResponses, "https://p2.test/v1", []string{"m3"})

	home.apply(t, "p1", agentsGrokbuild, []string{"m1"}, "m1", nil)
	home.apply(t, "p2", agentsGrokbuild, []string{"m3"}, "m3", nil)
	if content := mustRead(t, mustGrokConfigPath(t, home.env)); !strings.Contains(content, `default = "reins-p2--m3"`) {
		t.Fatalf("p2 应成为默认：\n%s", content)
	}

	// 重新应用 p1 并把默认模型换成自己的另一个模型。
	home.apply(t, "p1", agentsGrokbuild, []string{"m1", "m2"}, "m2", nil)
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	if !strings.Contains(content, `default = "reins-p1--m2"`) {
		t.Fatalf("默认模型应切到 reins-p1--m2：\n%s", content)
	}
	if !strings.Contains(content, "[model_providers.reins-p2]") || !strings.Contains(content, "reins-p2--m3") {
		t.Fatalf("p2 条目应保留：\n%s", content)
	}

	state := home.state(t)
	grok := findAppState(t, state, agentsGrokbuild)
	if len(grok.Entries) != 2 {
		t.Fatalf("entries：%+v", grok.Entries)
	}
	var p1, p2 *ProviderAppEntry
	for i := range grok.Entries {
		switch grok.Entries[i].Key {
		case "reins-p1":
			p1 = &grok.Entries[i]
		case "reins-p2":
			p2 = &grok.Entries[i]
		}
	}
	if p1 == nil || p2 == nil {
		t.Fatalf("entries：%+v", grok.Entries)
	}
	if p1.Status != EntryStatusApplied {
		t.Fatalf("p1 status: %s", p1.Status)
	}
	if len(p1.ModelIDs) != 2 || p1.ModelIDs[0] != "m1" || p1.ModelIDs[1] != "m2" {
		t.Fatalf("p1 model ids: %v", p1.ModelIDs)
	}
	if p1.DefaultModelID == nil || *p1.DefaultModelID != "m2" {
		t.Fatalf("p1 default: %v", p1.DefaultModelID)
	}
	if p2.DefaultModelID != nil {
		t.Fatalf("p2 default 应为空：%v", *p2.DefaultModelID)
	}
}

// 模型声明了思考等级时按等级写 reasoning_efforts 菜单（Grok 1.0.92 的菜单
// 来源），不再写旧的 supports_reasoning_effort；context_window 继续按元数据写。
func TestGrokApplyWritesReasoningMenuFromModelLevels(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	for _, expected := range []string{
		"context_window = 200000",
		"reasoning_efforts",
		`default_reasoning_effort = "high"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("config.toml 缺少 %s：\n%s", expected, content)
		}
	}
	if strings.Contains(content, "supports_reasoning_effort") {
		t.Fatalf("有菜单时不应再写旧字段：\n%s", content)
	}
	root := mustTOML(t, content)
	assertGrokMenu(t, root, "reins-p1--model-a", "low", "medium", "high")
	assertGrokMenuDefault(t, root, "reins-p1--model-a", "high")

	// 新增字段不影响反读归类。
	state := home.state(t)
	grok := findAppState(t, state, agentsGrokbuild)
	if grok.Entries[0].Status != EntryStatusApplied {
		t.Fatalf("status: %s", grok.Entries[0].Status)
	}
}

// 模型声明 max 时按 max 写菜单项与默认档，不再折算 xhigh（1.0.92 的
// reasoning_efforts 菜单能以 max 作为独立档位）。
func TestGrokDeclaredMaxWritesMax(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := true
	input.Models = []ProviderModelInput{{
		ID:              "model-a",
		Label:           "Model A",
		Reasoning:       &reasoning,
		ReasoningLevels: []ReasoningLevel{ReasoningLow, ReasoningHigh, ReasoningMax},
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningMax))
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	if !strings.Contains(content, `default_reasoning_effort = "max"`) {
		t.Fatalf("声明的 max 应按 max 落盘：\n%s", content)
	}
	root := mustTOML(t, content)
	assertGrokMenu(t, root, "reins-p1--model-a", "low", "high", "max")
	assertGrokMenuDefault(t, root, "reins-p1--model-a", "max")
}

// 菜单必须有 default 项：没有工具默认档时取 high，避免 Grok 退回菜单第一项
// （按枚举序可能是 none，等于把默认思考关掉）。
func TestGrokMenuDefaultsToHighWithoutToolLevel(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := true
	input.Models = []ProviderModelInput{{
		ID:              "model-a",
		Label:           "Model A",
		Reasoning:       &reasoning,
		ReasoningLevels: []ReasoningLevel{ReasoningOff, ReasoningLow, ReasoningHigh},
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", nil)
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	if strings.Contains(content, "default_reasoning_effort") {
		t.Fatalf("未设置工具默认档时不应写全局默认档：\n%s", content)
	}
	root := mustTOML(t, content)
	assertGrokMenu(t, root, "reins-p1--model-a", "none", "low", "high")
	assertGrokMenuDefault(t, root, "reins-p1--model-a", "high")
}

// 没有等级元数据的推理模型保留旧式 supports_reasoning_effort，菜单由
// Grok/端点决定；旧式默认菜单（low/medium/high/xhigh）没有 max，Max 仍
// 折算成 xhigh。
func TestGrokMaxFoldsToXhighWithoutModelLevels(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := true
	input.Models = []ProviderModelInput{{
		ID:        "model-a",
		Label:     "Model A",
		Reasoning: &reasoning,
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningMax))
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	for _, expected := range []string{
		"supports_reasoning_effort = true",
		`default_reasoning_effort = "xhigh"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("config.toml 缺少 %s：\n%s", expected, content)
		}
	}
	if strings.Contains(content, "reasoning_efforts") {
		t.Fatalf("没有等级元数据时不应凭空写菜单：\n%s", content)
	}
}

// 明确声明不推理的模型写 supports_reasoning_effort = false，阻断与内置同名
// 模型（grok-4.5/4.6）的菜单继承；没有落点的字段仍不写。
func TestGrokApplyWritesExplicitFalseForNonReasoningModel(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := false
	input.Models = []ProviderModelInput{{
		ID:              "model-a",
		Label:           "Model A",
		MaxOutputTokens: ptrInt64(64_000),
		SupportsImages:  ptrBool(true),
		Reasoning:       &reasoning,
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", nil)
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	if !strings.Contains(content, "supports_reasoning_effort = false") {
		t.Fatalf("非推理模型应写显式 false：\n%s", content)
	}
	for _, unexpected := range []string{"context_window", "reasoning_efforts", "max_completion_tokens"} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("不应写 %s：\n%s", unexpected, content)
		}
	}
}

// 默认档只作用于默认模型，必须是该模型菜单内的一档；Grok 的 /effort 与
// --effort 只接受菜单值，写盘前必须拦住，不能等用户进 Grok 才报错。
func TestGrokRejectsDefaultLevelOutsideModelMenu(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := true
	input.Models = []ProviderModelInput{{
		ID:              "model-a",
		Label:           "Model A",
		Reasoning:       &reasoning,
		ReasoningLevels: []ReasoningLevel{ReasoningLow, ReasoningHigh, ReasoningMax},
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	_, err := applyProviderInner(home.store, home.env,
		plan("p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningXhigh)))
	if err == nil {
		t.Fatal("默认档不在菜单内应拒绝应用")
	}
	for _, expected := range []string{"不在模型", "low、high、max"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("错误应包含 %s：%v", expected, err)
		}
	}
}

// 默认模型明确不支持思考等级时，默认档没有生效对象，应在写盘前拒绝。
func TestGrokRejectsDefaultLevelForNonReasoningModel(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := false
	input.Models = []ProviderModelInput{{
		ID:        "model-a",
		Label:     "Model A",
		Reasoning: &reasoning,
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	_, err := applyProviderInner(home.store, home.env,
		plan("p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh)))
	if err == nil || !strings.Contains(err.Error(), "不支持思考等级") {
		t.Fatalf("应拒绝非推理模型的默认档：%v", err)
	}
}

// Grok 的 messages 后端固定请求 {base_url}/messages 不补 /v1（grok 1.0.41
// 实测）：providers.yaml 按 anthropic 生态习惯不带 /v1 时，写入补 /v1，
// 且反读按同一口径比对、不误判漂移。
func TestGrokAnthropicBaseURLGetsV1SuffixAndInspectStaysApplied(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "p1", ProtocolAnthropicMessages, "https://p1.test/anthropic")

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", nil)
	if content := mustRead(t, mustGrokConfigPath(t, home.env)); !strings.Contains(content, `base_url = "https://p1.test/anthropic/v1"`) {
		t.Fatalf("写入应补 /v1：\n%s", content)
	}

	state := home.state(t)
	grok := findAppState(t, state, agentsGrokbuild)
	if grok.Entries[0].Status != EntryStatusApplied {
		t.Fatalf("反读按同一口径比对后应保持已应用：%s", grok.Entries[0].Status)
	}
}

// grokMenuItems：取 [model.<key>].reasoning_efforts 的数组项；TOML 解码的
// 数组元素类型随库实现可能是 []map[string]any 或 []any，两种都收。
func grokMenuItems(t *testing.T, root map[string]any, modelKey string) []map[string]any {
	t.Helper()
	modelTable, ok := tomlTable(tomlGet(root, "model"))
	if !ok {
		t.Fatalf("缺少 model 表：%#v", root)
	}
	entry, ok := tomlTable(modelTable[modelKey])
	if !ok {
		t.Fatalf("缺少模型条目 %s：%#v", modelKey, modelTable)
	}
	items, _ := entry["reasoning_efforts"].([]map[string]any)
	if items == nil {
		generic, ok := entry["reasoning_efforts"].([]any)
		if !ok {
			t.Fatalf("reasoning_efforts 不是数组：%#v", entry["reasoning_efforts"])
		}
		for _, item := range generic {
			table, ok := tomlTable(item)
			if !ok {
				t.Fatalf("reasoning_efforts 项不是表：%#v", item)
			}
			items = append(items, table)
		}
	}
	return items
}

// assertGrokMenu：断言菜单 value 序列。
func assertGrokMenu(t *testing.T, root map[string]any, modelKey string, expected ...string) {
	t.Helper()
	items := grokMenuItems(t, root, modelKey)
	values := make([]string, 0, len(items))
	for _, item := range items {
		value, ok := nodeAsTOMLStr(tomlGet(item, "value"))
		if !ok {
			t.Fatalf("reasoning_efforts.value 不是字符串：%#v", item)
		}
		values = append(values, value)
	}
	if len(values) != len(expected) {
		t.Fatalf("菜单项：%v，期望：%v", values, expected)
	}
	for index := range expected {
		if values[index] != expected[index] {
			t.Fatalf("菜单项：%v，期望：%v", values, expected)
		}
	}
}

// assertGrokMenuDefault：断言菜单的 default 项。没有 default 时 Grok 会取
// 第一项，所以写菜单必须标一个。
func assertGrokMenuDefault(t *testing.T, root map[string]any, modelKey string, expected string) {
	t.Helper()
	for _, item := range grokMenuItems(t, root, modelKey) {
		flag, _ := item["default"].(bool)
		if !flag {
			continue
		}
		value, _ := nodeAsTOMLStr(tomlGet(item, "value"))
		if value != expected {
			t.Fatalf("菜单默认项：%s，期望：%s", value, expected)
		}
		return
	}
	t.Fatalf("菜单没有 default 项，期望：%s", expected)
}
