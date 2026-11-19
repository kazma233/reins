// ZCode 适配器端到级行为（对齐 tests/providers.rs 的 ZCode 一节）。
package providers

import (
	"path/filepath"
	"testing"
)

// 预置一份贴近真实形态的用户自配文件：一条官方模板条目 + 一条自定义条目。
func zcodeSeedUserConfig(t *testing.T, path string) {
	t.Helper()
	mustMkdirAll(t, filepath.Dir(path))
	mustWrite(t, path, `{
		"schemaVersion": 1,
		"config": {
			"providerOrder": ["minimax", "new-provider"],
			"providerConfigRules": {
				"providerRules": [
					{
						"providerId": "minimax",
						"templateId": "minimax",
						"providerName": "MiniMax",
						"config": {
							"group": "standard-personal",
							"access": {"type": "api-key", "apiKey": "sk-user"},
							"personalModelIds": [],
							"modelOrder": ["MiniMax-M2.7"]
						}
					},
					{
						"providerId": "new-provider",
						"providerName": "custom-gateway",
						"config": {
							"group": "standard-personal",
							"access": {"type": "api-key", "apiKey": "sk-user"},
							"api": {
								"type": "anthropic-messages",
								"baseUrl": "http://192.0.2.1:3000"
							},
							"personalModelIds": ["deepseek-flash"],
							"modelOrder": ["deepseek-flash"]
						}
					}
				]
			},
			"modelConfigRules": {
				"providerModelRules": [
					{
						"providerId": "new-provider",
						"modelId": "deepseek-flash",
						"config": {"enabled": true}
					}
				],
				"manualProviderModelRules": []
			}
		}
	}`)
}

func zcodeRule(t *testing.T, root map[string]any, providerID string) map[string]any {
	t.Helper()
	rules := jopt(root, "config", "providerConfigRules", "providerRules").([]any)
	for _, item := range rules {
		rule := item.(map[string]any)
		if id, _ := rule["providerId"].(string); id == providerID {
			return rule
		}
	}
	return nil
}

func zcodeModelRulesOf(t *testing.T, root map[string]any, providerID string) []map[string]any {
	t.Helper()
	var rules []map[string]any
	items, ok := jopt(root, "config", "modelConfigRules", "providerModelRules").([]any)
	if !ok {
		return nil
	}
	for _, item := range items {
		rule := item.(map[string]any)
		if id, _ := rule["providerId"].(string); id == providerID {
			rules = append(rules, rule)
		}
	}
	return rules
}

func zcodeManualModelRulesOf(t *testing.T, root map[string]any, providerID string) []map[string]any {
	t.Helper()
	var rules []map[string]any
	items, ok := jopt(root, "config", "modelConfigRules", "manualProviderModelRules").([]any)
	if !ok {
		return nil
	}
	for _, item := range items {
		rule := item.(map[string]any)
		if id, _ := rule["providerId"].(string); id == providerID {
			rules = append(rules, rule)
		}
	}
	return rules
}

func TestZcodeApplyWritesEntryAndReadsBack(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	home.seedProviderWithModels(t, "p1", ProtocolAnthropicMessages, "https://p1.test/v1", []string{"model-a"})
	home.apply(t, "p1", agentsZcode, []string{"model-a"}, "model-a", nil)

	applied := mustJSON(t, mustRead(t, path))
	rule := zcodeRule(t, applied, "reins-p1")
	if rule == nil {
		t.Fatal("reins 条目应存在")
	}
	if jstr(t, rule, "providerName") != "Label p1" {
		t.Fatalf("providerName: %v", rule["providerName"])
	}
	if jstr(t, rule, "config", "group") != "standard-personal" {
		t.Fatalf("group: %v", jopt(rule, "config", "group"))
	}
	if jstr(t, rule, "config", "access", "apiKey") != "sk-test-secret" {
		t.Fatalf("apiKey: %v", jopt(rule, "config", "access", "apiKey"))
	}
	if jstr(t, rule, "config", "api", "type") != "anthropic-messages" {
		t.Fatalf("api.type: %v", jopt(rule, "config", "api", "type"))
	}
	if jstr(t, rule, "config", "api", "baseUrl") != "https://p1.test/v1" {
		t.Fatalf("api.baseUrl: %v", jopt(rule, "config", "api", "baseUrl"))
	}
	assertJSONStringArray(t, jpath(t, rule, "config", "personalModelIds"), "model-a")
	assertJSONStringArray(t, jpath(t, rule, "config", "modelOrder"), "model-a")

	order := jpath(t, applied, "config", "providerOrder").([]any)
	found := false
	for _, item := range order {
		if item == "reins-p1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("providerOrder 应含 reins-p1：%v", order)
	}

	// 模型元数据落模型级规则（seed 夹具带全部元数据）。不写 enabled：内置
	// `.*` 兜底默认启用，显式写会覆盖用户的启停选择。
	modelRules := zcodeModelRulesOf(t, applied, "reins-p1")
	if len(modelRules) != 1 {
		t.Fatalf("reins 模型规则：%v", modelRules)
	}
	modelRule := modelRules[0]
	if jstr(t, modelRule, "modelId") != "model-a" {
		t.Fatalf("modelId: %v", modelRule["modelId"])
	}
	if jopt(modelRule, "config", "enabled") != nil {
		t.Fatal("不应写 enabled")
	}
	if jnum(t, modelRule, "config", "properties", "contextWindow") != 200_000 {
		t.Fatalf("contextWindow: %v", jopt(modelRule, "config", "properties", "contextWindow"))
	}
	if jbool(t, modelRule, "config", "properties", "inputFormat", "supportsImage") != true {
		t.Fatalf("supportsImage: %v", jopt(modelRule, "config", "properties", "inputFormat", "supportsImage"))
	}
	if jnum(t, modelRule, "config", "optionSpecs", "maxOutputTokens", "max") != 64_000 {
		t.Fatalf("maxOutputTokens.max: %v", jopt(modelRule, "config", "optionSpecs", "maxOutputTokens", "max"))
	}
	// 思考档位写 values，map 留给内置按 API 类型的兜底（不写 map 叶子）。
	assertJSONStringArray(t, jpath(t, modelRule, "config", "optionSpecs", "reasoningLevel", "values"),
		"low", "medium", "high")
	if jopt(modelRule, "config", "optionSpecs", "reasoningLevel", "map") != nil {
		t.Fatal("不应写 reasoningLevel.map")
	}

	// 反读：已应用 + 外部条目照常展示。
	state := home.state(t)
	zcode := findAppState(t, state, agentsZcode)
	var reinsEntry *ProviderAppEntry
	for i := range zcode.Entries {
		if zcode.Entries[i].Key == "reins-p1" {
			reinsEntry = &zcode.Entries[i]
			break
		}
	}
	if reinsEntry == nil {
		t.Fatalf("entries：%+v", zcode.Entries)
	}
	if reinsEntry.Status != EntryStatusApplied {
		t.Fatalf("status: %s", reinsEntry.Status)
	}
	if reinsEntry.BaseURL == nil || *reinsEntry.BaseURL != "https://p1.test/v1" {
		t.Fatalf("base url: %v", reinsEntry.BaseURL)
	}
	hasMinimax := false
	for _, entry := range zcode.Entries {
		if entry.Key == "minimax" {
			hasMinimax = true
			break
		}
	}
	if !hasMinimax {
		t.Fatal("外部条目应照常展示")
	}
}

// 重新应用是条目级替换：未选中的模型不再保留规则；用户自配条目与其模型
// 规则原样保留（additive）。
func TestZcodeReapplyReplacesModelsAndKeepsUserEntries(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	home.seedProviderWithModels(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1",
		[]string{"model-a", "model-b"})
	home.apply(t, "p1", agentsZcode, []string{"model-a", "model-b"}, "model-a", nil)
	home.apply(t, "p1", agentsZcode, []string{"model-b"}, "model-b", nil)

	reapplied := mustJSON(t, mustRead(t, path))
	rules := jpath(t, reapplied, "config", "providerConfigRules", "providerRules").([]any)
	reinsCount := 0
	for _, item := range rules {
		rule := item.(map[string]any)
		if id, _ := rule["providerId"].(string); len(id) >= 6 && id[:6] == "reins-" {
			reinsCount++
		}
	}
	if reinsCount != 1 {
		t.Fatalf("reins 条目应是条目级替换，不重复追加：%d", reinsCount)
	}
	var reinsModels []string
	for _, rule := range zcodeModelRulesOf(t, reapplied, "reins-p1") {
		if id, ok := rule["modelId"].(string); ok {
			reinsModels = append(reinsModels, id)
		}
	}
	if len(reinsModels) != 1 || reinsModels[0] != "model-b" {
		t.Fatalf("旧模型规则应随重选清掉：%v", reinsModels)
	}
	if jstr(t, zcodeRule(t, reapplied, "reins-p1"), "config", "api", "type") != "openai-chat-completions" {
		t.Fatalf("api.type: %v", jopt(zcodeRule(t, reapplied, "reins-p1"), "config", "api", "type"))
	}
	if len(zcodeModelRulesOf(t, reapplied, "new-provider")) == 0 {
		t.Fatal("外部条目的模型规则应保留")
	}
	if zcodeRule(t, reapplied, "new-provider") == nil {
		t.Fatal("外部条目应保留")
	}
}

// 档位值域：off 写 "disabled"（ZCode 的关闭拼写，其余档位原样透传）；乱序与
// 重复输入按语义强度排序去重（schema 要求非空、不重复、低到高）。
func TestZcodeApplySortsReasoningValuesAndSpellsOffAsDisabled(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	input := providerInput("p1", ProtocolOpenaiResponses, "https://p1.test/v1")
	input.Models = []ProviderModelInput{{
		ID:    "model-a",
		Label: "Model A",
		ReasoningLevels: []ReasoningLevel{
			ReasoningHigh,
			ReasoningOff,
			ReasoningLow,
			ReasoningHigh,
		},
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}
	home.apply(t, "p1", agentsZcode, []string{"model-a"}, "model-a", nil)

	applied := mustJSON(t, mustRead(t, path))
	rules := zcodeModelRulesOf(t, applied, "reins-p1")
	if len(rules) != 1 {
		t.Fatalf("rules: %d", len(rules))
	}
	assertJSONStringArray(t, jpath(t, rules[0], "config", "optionSpecs", "reasoningLevel", "values"),
		"disabled", "low", "high")
}

// 只声明 reasoning 布尔时给保守三档（对齐 codex 目录条目口径）；显式 false
// 只有关闭档；无任何信息时不写 reasoningLevel（继承内置兜底）。
func TestZcodeApplyDerivesReasoningValuesFromBooleanFlag(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	makeModel := func(id string, reasoning *bool) ProviderModelInput {
		return ProviderModelInput{ID: id, Label: "Label " + id, Reasoning: reasoning}
	}
	reasoningOn := true
	reasoningOff := false
	input := providerInput("p1", ProtocolOpenaiResponses, "https://p1.test/v1")
	input.Models = []ProviderModelInput{
		makeModel("model-on", &reasoningOn),
		makeModel("model-off", &reasoningOff),
		makeModel("model-unknown", nil),
	}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}
	home.apply(t, "p1", agentsZcode, []string{"model-on", "model-off", "model-unknown"}, "model-on", nil)

	applied := mustJSON(t, mustRead(t, path))
	ruleOfModel := func(modelID string) map[string]any {
		for _, rule := range zcodeModelRulesOf(t, applied, "reins-p1") {
			if id, _ := rule["modelId"].(string); id == modelID {
				return rule
			}
		}
		return nil
	}
	onRule := ruleOfModel("model-on")
	if onRule == nil {
		t.Fatal("reasoning=true 应写值域")
	}
	assertJSONStringArray(t, jpath(t, onRule, "config", "optionSpecs", "reasoningLevel", "values"),
		"low", "medium", "high")
	offRule := ruleOfModel("model-off")
	if offRule == nil {
		t.Fatal("reasoning=false 应写关闭档")
	}
	assertJSONStringArray(t, jpath(t, offRule, "config", "optionSpecs", "reasoningLevel", "values"),
		"disabled")
	// 无元数据且无既有叶子：不产生空规则，全部继承内置兜底。
	if ruleOfModel("model-unknown") != nil {
		t.Fatal("无信息模型不应产生空规则")
	}
}

// 用户已手动配置的模型跳过不写：schema 禁止同一 (provider, model) 同时存在
// 智能与手动规则，写了会让整份配置校验失败，用户的显式选择优先；智能规则
// 里的非自有叶子（如启用开关）在重写时保留。
func TestZcodeApplySkipsManualModelsAndPreservesUserLeaves(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	// 预置：model-a 有用户手动规则；model-b 的智能规则带 enabled=false。
	seeded := mustJSON(t, mustRead(t, path))
	seeded["config"].(map[string]any)["modelConfigRules"].(map[string]any)["manualProviderModelRules"] = []any{map[string]any{
		"providerId": "reins-p1",
		"modelId":    "model-a",
		"config": map[string]any{
			"enabled":     false,
			"properties":  map[string]any{"contextWindow": 100000},
			"optionSpecs": map[string]any{"reasoningLevel": map[string]any{"values": []any{"low"}, "map": "{}"}},
		},
	}}
	modelRulesSection := seeded["config"].(map[string]any)["modelConfigRules"].(map[string]any)["providerModelRules"].([]any)
	modelRulesSection = append(modelRulesSection, map[string]any{
		"providerId": "reins-p1",
		"modelId":    "model-b",
		"config":     map[string]any{"enabled": false},
	})
	seeded["config"].(map[string]any)["modelConfigRules"].(map[string]any)["providerModelRules"] = modelRulesSection
	mustWriteJSON(t, path, seeded)

	home.seedProviderWithModels(t, "p1", ProtocolAnthropicMessages, "https://p1.test/v1",
		[]string{"model-a", "model-b"})
	home.apply(t, "p1", agentsZcode, []string{"model-a", "model-b"}, "model-a", nil)

	applied := mustJSON(t, mustRead(t, path))
	// model-a 有手动规则：不写智能规则，手动规则原样保留（含用户的 map）。
	for _, rule := range zcodeModelRulesOf(t, applied, "reins-p1") {
		if id, _ := rule["modelId"].(string); id == "model-a" {
			t.Fatal("手动配置的 model-a 不应有智能规则")
		}
	}
	manual := zcodeManualModelRulesOf(t, applied, "reins-p1")
	if len(manual) != 1 {
		t.Fatalf("手动规则：%v", manual)
	}
	if jstr(t, manual[0], "modelId") != "model-a" {
		t.Fatalf("modelId: %v", manual[0]["modelId"])
	}
	if jstr(t, manual[0], "config", "optionSpecs", "reasoningLevel", "map") != "{}" {
		t.Fatalf("用户的 map 应保留：%v", jopt(manual[0], "config", "optionSpecs", "reasoningLevel", "map"))
	}
	// model-b：保留用户的 enabled=false，同时并入本次元数据。
	var ruleB map[string]any
	for _, rule := range zcodeModelRulesOf(t, applied, "reins-p1") {
		if id, _ := rule["modelId"].(string); id == "model-b" {
			ruleB = rule
			break
		}
	}
	if ruleB == nil {
		t.Fatal("model-b 应有智能规则")
	}
	if jbool(t, ruleB, "config", "enabled") != false {
		t.Fatalf("enabled 应保留用户值：%v", jopt(ruleB, "config", "enabled"))
	}
	if jnum(t, ruleB, "config", "properties", "contextWindow") != 200_000 {
		t.Fatalf("contextWindow 应并入：%v", jopt(ruleB, "config", "properties", "contextWindow"))
	}
	assertJSONStringArray(t, jpath(t, ruleB, "config", "optionSpecs", "reasoningLevel", "values"),
		"low", "medium", "high")
}

func TestZcodeRemoveClearsEntryAndKeepsExternal(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	home.seedProviderWithModels(t, "p1", ProtocolOpenaiResponses, "https://p1.test/v1", []string{"model-a"})
	home.apply(t, "p1", agentsZcode, []string{"model-a"}, "model-a", nil)

	// 用户给该平台的一个模型加了手动规则；ZCode 自己的 deletePersonalProvider
	// 会连同手动规则一起清（deleteExactForProvider 覆盖两类）。
	withManual := mustJSON(t, mustRead(t, path))
	withManual["config"].(map[string]any)["modelConfigRules"].(map[string]any)["manualProviderModelRules"] = []any{map[string]any{
		"providerId": "reins-p1",
		"modelId":    "model-b",
		"config":     map[string]any{"enabled": false},
	}}
	mustWriteJSON(t, path, withManual)

	if err := home.removeFrom(t, "p1", agentsZcode); err != nil {
		t.Fatalf("remove: %v", err)
	}

	removed := mustJSON(t, mustRead(t, path))
	if zcodeRule(t, removed, "reins-p1") != nil {
		t.Fatal("reins 条目应被删除")
	}
	if zcodeRule(t, removed, "minimax") == nil || zcodeRule(t, removed, "new-provider") == nil {
		t.Fatal("外部条目应保留")
	}
	order := jpath(t, removed, "config", "providerOrder").([]any)
	for _, item := range order {
		if item == "reins-p1" {
			t.Fatalf("providerOrder 应清掉 reins-p1：%v", order)
		}
	}
	if len(zcodeModelRulesOf(t, removed, "reins-p1")) != 0 {
		t.Fatal("智能模型规则应清掉")
	}
	if len(zcodeManualModelRulesOf(t, removed, "reins-p1")) != 0 {
		t.Fatal("手动模型规则应清掉")
	}

	// 反读只剩外部条目。
	state := home.state(t)
	zcode := findAppState(t, state, agentsZcode)
	for _, entry := range zcode.Entries {
		if len(entry.Key) >= 6 && entry.Key[:6] == "reins-" {
			t.Fatalf("反读不应再有 reins 条目：%s", entry.Key)
		}
	}
}

func TestZcodeRemoveRejectsModifiedEntry(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsZcode, []string{"model-a"}, "model-a", nil)

	// 手工把 api.type 改成协议表外的取值：内容不可识别，不自动删。
	modified := mustJSON(t, mustRead(t, path))
	rules := jpath(t, modified, "config", "providerConfigRules", "providerRules").([]any)
	for _, item := range rules {
		rule := item.(map[string]any)
		if id, _ := rule["providerId"].(string); id == "reins-p1" {
			config := rule["config"].(map[string]any)
			api := config["api"].(map[string]any)
			api["type"] = "custom-thing"
		}
	}
	mustWriteJSON(t, path, modified)

	err := home.removeFrom(t, "p1", agentsZcode)
	if err == nil {
		t.Fatal("漂移条目应拒绝移除")
	}
	if !contains(err.Error(), "已被手工修改") {
		t.Fatalf("错误文案不符：%s", err.Error())
	}
}

func TestZcodeRemoveExternalDeletesUserEntry(t *testing.T) {
	home := newIsolated(t)
	path := mustZcodeProviderConfigPath(t, home.env)
	zcodeSeedUserConfig(t, path)

	if err := home.removeExternal(t, agentsZcode, "new-provider"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	removed := mustJSON(t, mustRead(t, path))
	if zcodeRule(t, removed, "new-provider") != nil {
		t.Fatal("new-provider 条目应被删除")
	}
	if zcodeRule(t, removed, "minimax") == nil {
		t.Fatal("minimax 应保留")
	}
	order := jpath(t, removed, "config", "providerOrder").([]any)
	for _, item := range order {
		if item == "new-provider" {
			t.Fatalf("providerOrder 应清掉 new-provider：%v", order)
		}
	}
	modelRules := jpath(t, removed, "config", "modelConfigRules", "providerModelRules").([]any)
	for _, item := range modelRules {
		rule := item.(map[string]any)
		if id, _ := rule["providerId"].(string); id == "new-provider" {
			t.Fatalf("new-provider 的模型规则应清掉：%v", rule)
		}
	}
}
