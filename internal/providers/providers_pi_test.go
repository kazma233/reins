// Pi 适配器端到级行为（对齐 tests/providers.rs 的 Pi 一节）。
package providers

import (
	"testing"
)

func TestPiApplyWritesModelsAndSettingsThenRemoveClearsDefaults(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsPi, []string{"model-a"}, "model-a", levelPtr(ReasoningLow))
	models := mustJSON(t, mustRead(t, mustPiModelsPath(t, home.env)))
	if jstr(t, models, "providers", "reins-p1", "baseUrl") != "https://p1.test/v1" {
		t.Fatalf("baseUrl: %v", jopt(models, "providers", "reins-p1", "baseUrl"))
	}
	if jstr(t, models, "providers", "reins-p1", "apiKey") != "sk-test-secret" {
		t.Fatalf("apiKey: %v", jopt(models, "providers", "reins-p1", "apiKey"))
	}
	settings := mustJSON(t, mustRead(t, mustPiSettingsPath(t, home.env)))
	if jstr(t, settings, "defaultProvider") != "reins-p1" {
		t.Fatalf("defaultProvider: %v", settings["defaultProvider"])
	}
	if jstr(t, settings, "defaultThinkingLevel") != "low" {
		t.Fatalf("defaultThinkingLevel: %v", settings["defaultThinkingLevel"])
	}

	// 默认 Provider 仍指向 → 随移除清掉默认三元组。
	if err := home.removeFrom(t, "p1", agentsPi); err != nil {
		t.Fatalf("remove: %v", err)
	}
	models = mustJSON(t, mustRead(t, mustPiModelsPath(t, home.env)))
	if jopt(models, "providers", "reins-p1") != nil {
		t.Fatal("reins 条目应被移除")
	}
	settings = mustJSON(t, mustRead(t, mustPiSettingsPath(t, home.env)))
	if jopt(settings, "defaultProvider") != nil ||
		jopt(settings, "defaultModel") != nil ||
		jopt(settings, "defaultThinkingLevel") != nil {
		t.Fatalf("默认三元组应清掉：%+v", settings)
	}
}

// Pi 的思考等级首档写 "off" 而非归一化集合的 "none"（官方枚举）。
func TestPiOffLevelWritesOff(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsPi, []string{"model-a"}, "model-a", levelPtr(ReasoningOff))
	settings := mustJSON(t, mustRead(t, mustPiSettingsPath(t, home.env)))
	if jstr(t, settings, "defaultThinkingLevel") != "off" {
		t.Fatalf("off 档应写 off：%v", settings["defaultThinkingLevel"])
	}

	// 反读要把 "off" 还原成 off，否则应用弹窗预选不出来。
	state := home.state(t)
	pi := findAppState(t, state, agentsPi)
	if pi.DefaultReasoningLevel == nil || *pi.DefaultReasoningLevel != ReasoningOff {
		t.Fatalf("反读等级：%v", pi.DefaultReasoningLevel)
	}
}

// Pi 只对 thinkingLevelMap 显式声明的模型开放 xhigh/max 档，apply 要把
// reasoning_levels 逐档下发；未填等级的模型不写 map（Pi 默认开放 off～high）。
func TestPiApplyWritesThinkingLevelMapFromModelLevels(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiResponses, "https://p1.test/v1")
	input.Models = []ProviderModelInput{
		{
			ID:        "model-levels",
			Label:     "Model Levels",
			Reasoning: ptrBool(true),
			ReasoningLevels: []ReasoningLevel{
				ReasoningOff,
				ReasoningLow,
				ReasoningHigh,
				ReasoningXhigh,
				ReasoningMax,
			},
		},
		{
			ID:        "model-unconstrained",
			Label:     "Model Unconstrained",
			Reasoning: ptrBool(true),
		},
	}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsPi, []string{"model-levels", "model-unconstrained"}, "model-levels", nil)
	models := mustJSON(t, mustRead(t, mustPiModelsPath(t, home.env)))
	entries := jpath(t, models, "providers", "reins-p1", "models").([]any)
	var levelsEntry, unconstrained map[string]any
	for _, item := range entries {
		entry := item.(map[string]any)
		switch entry["id"] {
		case "model-levels":
			levelsEntry = entry
		case "model-unconstrained":
			unconstrained = entry
		}
	}
	if levelsEntry == nil || unconstrained == nil {
		t.Fatalf("模型条目缺失：%v", entries)
	}
	// 键是 Pi 档位名，值是该档发往 API 的 effort（off 档为 none）；
	// 不在档位集里的 minimal/medium 写 null，Pi 选择器据此隐藏该档。
	levelMap := jpath(t, levelsEntry, "thinkingLevelMap").(map[string]any)
	expected := map[string]any{
		"off":     "none",
		"minimal": nil,
		"low":     "low",
		"medium":  nil,
		"high":    "high",
		"xhigh":   "xhigh",
		"max":     "max",
	}
	if len(levelMap) != len(expected) {
		t.Fatalf("thinkingLevelMap：%v", levelMap)
	}
	for key, value := range expected {
		got, has := levelMap[key]
		if !has {
			t.Fatalf("thinkingLevelMap 缺少 %s：%v", key, levelMap)
		}
		if value == nil {
			if got != nil {
				t.Fatalf("%s 应为 null：%v", key, got)
			}
			continue
		}
		if got != value {
			t.Fatalf("%s 应为 %v：%v", key, value, got)
		}
	}
	if jopt(unconstrained, "thinkingLevelMap") != nil {
		t.Fatalf("未填等级的模型不应写 map：%v", unconstrained["thinkingLevelMap"])
	}
}
