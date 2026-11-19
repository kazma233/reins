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

// 选「最高 (max)」写出 xhigh：Grok UI 最高档是 xhigh 且请求层两者等价。
func TestGrokMaxReasoningWritesXhigh(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningMax))
	if content := mustRead(t, mustGrokConfigPath(t, home.env)); !strings.Contains(content, `default_reasoning_effort = "xhigh"`) {
		t.Fatalf("max 应折算成 xhigh 落盘：\n%s", content)
	}
}

// 模型元数据齐全时逐模型写 context_window 与 supports_reasoning_effort：
// 缺 context_window 时 grok 按 200000 兜底计算自动压缩时机；未声明
// supports_reasoning_effort 的模型会被判为不支持思考等级，[models] 里写的
// default_reasoning_effort 被静默丢弃（grok 1.0.41 debug 日志实测）。
func TestGrokApplyWritesContextWindowAndReasoningSupport(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsGrokbuild, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	content := mustRead(t, mustGrokConfigPath(t, home.env))
	for _, expected := range []string{
		"context_window = 200000",
		"supports_reasoning_effort = true",
		`default_reasoning_effort = "high"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("config.toml 缺少 %s：\n%s", expected, content)
		}
	}
	// 新增字段不影响反读归类。
	state := home.state(t)
	grok := findAppState(t, state, agentsGrokbuild)
	if grok.Entries[0].Status != EntryStatusApplied {
		t.Fatalf("status: %s", grok.Entries[0].Status)
	}
}

// 元数据缺失或明确不支持思考时两个字段都不写，不落无依据的猜测值；
// 最大输出（max_completion_tokens）与图像输入本就没有落点。
func TestGrokApplyOmitsAbsentModelMetadata(t *testing.T) {
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
	for _, unexpected := range []string{"context_window", "supports_reasoning_effort", "max_completion_tokens"} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("不应写 %s：\n%s", unexpected, content)
		}
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
