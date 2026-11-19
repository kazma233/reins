// dsh 适配器端到级行为：cordis patch 行覆盖 + 凭据 version:1 文档（对齐
// tests/providers.rs 的 dsh 一节）。
package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"reins/internal/support"
	"reins/internal/testsupport"
)

// 测试里全局层与 profile 层指向同一路径（夹具只写 desktop profile）。
func dshProfilePatchOf(t *testing.T, home *isolated) string {
	t.Helper()
	path, err := dshProfilePatchPath(home.env)
	if err != nil {
		t.Fatalf("profile patch 路径: %v", err)
	}
	return path
}

func dshCredentialsOf(t *testing.T, home *isolated) string {
	t.Helper()
	path, err := dshCredentialsPath(home.env)
	if err != nil {
		t.Fatalf("credentials 路径: %v", err)
	}
	return path
}

// mustYAMLDoc：把 patch / 凭据文件解析为可导航的树（map/切片/标量）。
func mustYAMLDoc(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := yaml.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("解析 YAML: %v\n%s", err, text)
	}
	return value
}

// findPatchRow：按 id 取 patch 行（根是操作列表）。
func findPatchRow(t *testing.T, root any, rowID string) map[string]any {
	t.Helper()
	items, ok := root.([]any)
	if !ok {
		t.Fatalf("patch 根不是列表：%#v", root)
	}
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			if id, _ := object["id"].(string); id == rowID {
				return object
			}
		}
	}
	return nil
}

func dshRoute(t *testing.T, root any, route string) map[string]any {
	t.Helper()
	row := findPatchRow(t, root, "llm-pi-ai")
	if row == nil {
		t.Fatal("llm-pi-ai 行缺失")
	}
	providers := jopt(row, "config", "providers")
	if providers == nil {
		t.Fatal("providers 表缺失")
	}
	entry, ok := providers.(map[string]any)[route]
	if !ok {
		t.Fatalf("路由 %s 缺失：%v", route, providers)
	}
	return entry.(map[string]any)
}

func TestDshApplyWritesPatchRowsAndCredential(t *testing.T) {
	home := newIsolated(t)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1", []string{"model-a"})

	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)

	profile := dshProfilePatchOf(t, home)
	root := mustYAMLDoc(t, mustRead(t, profile))
	route := dshRoute(t, root, "reins-p1")
	if jstr(t, route, "baseURL") != "https://p1.test/v1" {
		t.Fatalf("baseURL: %v", route["baseURL"])
	}
	if jstr(t, route, "api") != "openai-completions" {
		t.Fatalf("api: %v", route["api"])
	}
	if jstr(t, route, "apiKeyEnv") != "REINS_P1" {
		t.Fatalf("apiKeyEnv: %v", route["apiKeyEnv"])
	}
	modelsSeq, ok := route["models"].([]any)
	if !ok || len(modelsSeq) != 1 {
		t.Fatalf("models: %v", route["models"])
	}
	// 未选默认档时路由不带 reasoning，默认行也不落 reasoningEffort。
	if jopt(route, "reasoning") != nil {
		t.Fatalf("reasoning: %v", route["reasoning"])
	}

	defaultRow := findPatchRow(t, root, "agent-default-model")
	if defaultRow == nil {
		t.Fatal("默认模型行")
	}
	if jstr(t, defaultRow, "config", "provider") != "reins-p1" {
		t.Fatalf("provider: %v", jopt(defaultRow, "config", "provider"))
	}
	if jstr(t, defaultRow, "config", "model") != "model-a" {
		t.Fatalf("model: %v", jopt(defaultRow, "config", "model"))
	}
	if jopt(defaultRow, "config", "reasoningEffort") != nil {
		t.Fatalf("reasoningEffort: %v", jopt(defaultRow, "config", "reasoningEffort"))
	}

	credentials := mustYAMLDoc(t, mustRead(t, dshCredentialsOf(t, home)))
	if jstr(t, credentials, "refs", "REINS_P1") != "sk-test-secret" {
		t.Fatalf("凭据 ref: %v", jopt(credentials, "refs", "REINS_P1"))
	}

	// 反读：已应用 + 模型清单 + 默认模型 + 协议回显。
	state := home.state(t)
	dsh := findAppState(t, state, agentsDsh)
	if len(dsh.Entries) != 1 {
		t.Fatalf("entries: %d", len(dsh.Entries))
	}
	entry := dsh.Entries[0]
	if entry.Status != EntryStatusApplied {
		t.Fatalf("status: %s", entry.Status)
	}
	if len(entry.ModelIDs) != 1 || entry.ModelIDs[0] != "model-a" {
		t.Fatalf("model ids: %v", entry.ModelIDs)
	}
	if entry.DefaultModelID == nil || *entry.DefaultModelID != "model-a" {
		t.Fatalf("default: %v", entry.DefaultModelID)
	}
	if entry.Protocol == nil || *entry.Protocol != ProtocolOpenaiChatCompletions {
		t.Fatalf("protocol: %v", entry.Protocol)
	}

	// dsh 启动时拒绝加载非 owner-only 的凭据文件;apply 写入必须是 0600,
	// 否则用户下次启动 dsh 整个 app 起不来(真实事故回归)。
	assertOwnerOnly(t, dshCredentialsOf(t, home))
}

func TestDshApplyWritesModelCapabilitiesAndDefaultReasoning(t *testing.T) {
	home := newIsolated(t)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1", []string{"model-a"})

	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", levelPtr(ReasoningMax))

	root := mustYAMLDoc(t, mustRead(t, dshProfilePatchOf(t, home)))
	route := dshRoute(t, root, "reins-p1")
	if jstr(t, route, "reasoning") != "max" {
		t.Fatalf("路由级 reasoning: %v", route["reasoning"])
	}
	modelsSeq := route["models"].([]any)
	model := modelsSeq[0].(map[string]any)
	input, ok := model["input"].([]any)
	if !ok || len(input) != 2 || input[0] != "text" || input[1] != "image" {
		t.Fatalf("input: %v", model["input"])
	}
	efforts := model["reasoningEfforts"].(map[string]any)
	if efforts["low"] != "low" || efforts["medium"] != "medium" || efforts["high"] != "high" {
		t.Fatalf("reasoningEfforts: %v", efforts)
	}
	// 未选档为 null/缺席（支持但不发参数）。
	if _, has := efforts["off"]; has {
		t.Fatalf("off 档不应出现在 reasoningEfforts：%v", efforts)
	}
	if _, has := efforts["max"]; has {
		t.Fatalf("max 档不应出现在 reasoningEfforts：%v", efforts)
	}

	// 默认档必须落在 agent-default-model 行：dsh 的默认档插件值是 high，
	// 只写路由级 reasoning 纠正不了旧导入行。
	profileRoot := root
	defaultRow := findPatchRow(t, profileRoot, "agent-default-model")
	if defaultRow == nil {
		t.Fatal("默认模型行")
	}
	if jstr(t, defaultRow, "config", "reasoningEffort") != "max" {
		t.Fatalf("默认行 reasoningEffort: %v", jopt(defaultRow, "config", "reasoningEffort"))
	}
	state := home.state(t)
	dsh := findAppState(t, state, agentsDsh)
	if dsh.DefaultReasoningLevel == nil || *dsh.DefaultReasoningLevel != ReasoningMax {
		t.Fatalf("反读默认档：%v", dsh.DefaultReasoningLevel)
	}
}

func TestDshApplyWritesReasoningFalseForNonReasoningModel(t *testing.T) {
	home := newIsolated(t)
	input := providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	reasoning := false
	input.Models = []ProviderModelInput{{
		ID:        "model-a",
		Label:     "Label model-a",
		Reasoning: &reasoning,
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}

	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)
	root := mustYAMLDoc(t, mustRead(t, dshProfilePatchOf(t, home)))
	route := dshRoute(t, root, "reins-p1")
	model := route["models"].([]any)[0].(map[string]any)
	if flag, ok := model["reasoningEfforts"].(bool); !ok || flag {
		t.Fatalf("reasoningEfforts 应为显式 false：%#v", model["reasoningEfforts"])
	}
	if jopt(model, "input") != nil {
		t.Fatalf("未声明模态不应写 input：%v", model["input"])
	}
}

func TestDshApplyPreservesUnrelatedContentAndExternalProvider(t *testing.T) {
	home := newIsolated(t)
	profile := dshProfilePatchOf(t, home)
	mustMkdirAll(t, filepath.Dir(profile))
	mustWrite(t, profile, `- insert:
    - id: memory-memorix
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: memorix
        cwd: !!js process.cwd()
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      other-gw:
        baseURL: https://other.test/v1
        api: anthropic-messages
        apiKeyEnv: OTHER_KEY
        models:
          - id: other-model
- id: agent-default-model
  name: '@deepseek-ai/dsh-agent-default-model'
  config:
    provider: other-gw
    model: other-model
    reasoningEffort: high
- id: ui-chat
  name: '@deepseek-ai/dsh-client-ui-chat'
  config:
    transcriptView: standard
`)
	credentials := dshCredentialsOf(t, home)
	mustWrite(t, credentials, "# managed by dsh\nOTHER_KEY: 'other-secret'\n")

	home.seedProviderWithModels(t, "p1", ProtocolOpenaiResponses, "https://p1.test/v1", []string{"model-a"})
	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))

	content := mustRead(t, profile)
	for _, expected := range []string{
		"!!js process.cwd()", // 用户 !!js 标签保留
		"memory-memorix",     // MCP 条目保留
		"other-gw",           // 外部路由保留
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("patch 应保留 %s：\n%s", expected, content)
		}
	}
	root := mustYAMLDoc(t, content)
	other := dshRoute(t, root, "other-gw")
	if jstr(t, other, "apiKeyEnv") != "OTHER_KEY" {
		t.Fatalf("外部路由 apiKeyEnv: %v", other["apiKeyEnv"])
	}

	defaultRow := findPatchRow(t, root, "agent-default-model")
	if defaultRow == nil {
		t.Fatal("默认模型行")
	}
	if jstr(t, defaultRow, "config", "provider") != "reins-p1" {
		t.Fatalf("默认模型切换到新平台：%v", jopt(defaultRow, "config", "provider"))
	}
	if jstr(t, defaultRow, "config", "reasoningEffort") != "high" {
		t.Fatalf("reasoningEffort: %v", jopt(defaultRow, "config", "reasoningEffort"))
	}

	credentialsRoot := mustYAMLDoc(t, mustRead(t, credentials))
	// 注释会因结构化往返丢失（设计取舍），但其它凭据值必须原样保留。
	if jstr(t, credentialsRoot, "refs", "OTHER_KEY") != "other-secret" {
		t.Fatalf("外部凭据: %v", jopt(credentialsRoot, "refs", "OTHER_KEY"))
	}
	if jstr(t, credentialsRoot, "refs", "REINS_P1") != "sk-test-secret" {
		t.Fatalf("reins 凭据: %v", jopt(credentialsRoot, "refs", "REINS_P1"))
	}

	// 反读：reins 条目 Applied，外部条目 External。
	state := home.state(t)
	dsh := findAppState(t, state, agentsDsh)
	var reins, external *ProviderAppEntry
	for i := range dsh.Entries {
		switch dsh.Entries[i].Key {
		case "reins-p1":
			reins = &dsh.Entries[i]
		case "other-gw":
			external = &dsh.Entries[i]
		}
	}
	if reins == nil || external == nil {
		t.Fatalf("entries：%+v", dsh.Entries)
	}
	if reins.Status != EntryStatusApplied {
		t.Fatalf("reins status: %s", reins.Status)
	}
	if external.Status != EntryStatusExternal {
		t.Fatalf("external status: %s", external.Status)
	}
}

func TestDshReapplyIsIdempotentAndProvidersCoexist(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	home.seedProvider(t, "p2", ProtocolAnthropicMessages, "https://p2.test/v1")

	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)
	first := mustRead(t, dshProfilePatchOf(t, home))
	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)
	if second := mustRead(t, dshProfilePatchOf(t, home)); first != second {
		t.Fatalf("重应用幂等：\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	home.apply(t, "p2", agentsDsh, []string{"model-a"}, "model-a", nil)
	root := mustYAMLDoc(t, mustRead(t, dshProfilePatchOf(t, home)))
	if dshRoute(t, root, "reins-p1") == nil || dshRoute(t, root, "reins-p2") == nil {
		t.Fatal("多 provider 共存")
	}
	state := home.state(t)
	dsh := findAppState(t, state, agentsDsh)
	if len(dsh.Entries) != 2 {
		t.Fatalf("entries: %d", len(dsh.Entries))
	}
}

func TestDshChangedBaseURLReportsDrifted(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)

	if _, err := home.store.Upsert(providerInput("p1", ProtocolOpenaiChatCompletions, "https://p1.test/v2")); err != nil {
		t.Fatal(err)
	}
	state := home.state(t)
	dsh := findAppState(t, state, agentsDsh)
	if dsh.Entries[0].Status != EntryStatusDrifted {
		t.Fatalf("status: %s", dsh.Entries[0].Status)
	}
}

func TestDshRemoveClearsRouteCredentialAndDefaultRow(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)

	if err := home.removeFrom(t, "p1", agentsDsh); err != nil {
		t.Fatalf("remove: %v", err)
	}

	root := mustYAMLDoc(t, mustRead(t, dshProfilePatchOf(t, home)))
	if findPatchRow(t, root, "llm-pi-ai") != nil {
		t.Fatal("providers 清空后整行删除")
	}
	credentialsText := mustRead(t, dshCredentialsOf(t, home))
	if strings.Contains(credentialsText, "REINS_P1") {
		t.Fatalf("凭据 ref 应清掉：%s", credentialsText)
	}
	profileRoot := mustYAMLDoc(t, mustRead(t, dshProfilePatchOf(t, home)))
	if findPatchRow(t, profileRoot, "agent-default-model") != nil {
		t.Fatal("指向被删路由的默认行一并清理")
	}
}

// 凭据文档 version:1 布局：refs 段级 upsert/remove，version/records 保留。
func TestDshCredentialRefUpsertAndRemoveInVersionedDocument(t *testing.T) {
	existing := "version: 1\nrefs:\n  OTHER_KEY: 'other-secret'\nrecords:\n  gw/x:\n    key: v1\n"
	key := "sk-secret"
	updated, err := PatchCredentialRef(existing, "REINS_P1", &key)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	root := mustYAMLDoc(t, updated)
	if version := jpath(t, root, "version"); version != 1 {
		t.Fatalf("version: %v", version)
	}
	if jstr(t, root, "refs", "REINS_P1") != "sk-secret" {
		t.Fatalf("reins ref: %v", jopt(root, "refs", "REINS_P1"))
	}
	if jstr(t, root, "refs", "OTHER_KEY") != "other-secret" {
		t.Fatalf("外部 ref: %v", jopt(root, "refs", "OTHER_KEY"))
	}
	if jopt(root, "records", "gw/x") == nil {
		t.Fatal("records 保留")
	}

	// 再次应用同 ref 更新值（幂等语义由行覆盖承担）。
	key2 := "sk-v2"
	updatedAgain, err := PatchCredentialRef(updated, "REINS_P1", &key2)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !strings.Contains(updatedAgain, "sk-v2") {
		t.Fatalf("应更新为 sk-v2：\n%s", updatedAgain)
	}

	// 移除：refs 清自己的 ref，文档与其余条目保留。
	removed, err := PatchCredentialRef(updatedAgain, "REINS_P1", nil)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	removedRoot := mustYAMLDoc(t, removed)
	if jopt(removedRoot, "refs", "REINS_P1") != nil {
		t.Fatal("ref 应被移除")
	}
	if jstr(t, removedRoot, "refs", "OTHER_KEY") != "other-secret" {
		t.Fatalf("外部 ref 保留: %v", jopt(removedRoot, "refs", "OTHER_KEY"))
	}
	if version := jpath(t, removedRoot, "version"); version != 1 {
		t.Fatalf("version: %v", version)
	}
}

// 平铺旧布局（无 version）自动迁移到 version:1 后再写入。
func TestDshCredentialRefMigratesFlatLayout(t *testing.T) {
	flat := "DEEPSEEK_API_KEY: 'sk-legacy'\n"
	key := "sk-secret"
	updated, err := PatchCredentialRef(flat, "REINS_P1", &key)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	root := mustYAMLDoc(t, updated)
	if version := jpath(t, root, "version"); version != 1 {
		t.Fatalf("version: %v", version)
	}
	if jstr(t, root, "refs", "DEEPSEEK_API_KEY") != "sk-legacy" {
		t.Fatalf("平铺键应迁入 refs：%v", jopt(root, "refs", "DEEPSEEK_API_KEY"))
	}
	if jstr(t, root, "refs", "REINS_P1") != "sk-secret" {
		t.Fatalf("reins ref: %v", jopt(root, "refs", "REINS_P1"))
	}
}

// 空文档从零建立 version:1 布局。
func TestDshCredentialRefCreatesDocumentFromEmpty(t *testing.T) {
	key := "sk-1"
	created, err := PatchCredentialRef("", "REINS_P1", &key)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	root := mustYAMLDoc(t, created)
	if version := jpath(t, root, "version"); version != 1 {
		t.Fatalf("version: %v", version)
	}
	if jstr(t, root, "refs", "REINS_P1") != "sk-1" {
		t.Fatalf("ref: %v", jopt(root, "refs", "REINS_P1"))
	}
}

func TestDshRemoveRejectsMissingEntry(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	// 预置只有外部路由的 patch:reins-p1 无条目可删。
	profile := dshProfilePatchOf(t, home)
	mustMkdirAll(t, filepath.Dir(profile))
	mustWrite(t, profile, "- id: llm-pi-ai\n  name: '@deepseek-ai/dsh-llm-pi-ai'\n  config:\n    providers:\n      other-gw:\n        baseURL: https://other.test/v1\n")

	_, err := removeProviderFromAppInner(home.store, home.env, "p1", agentsDsh)
	if err == nil {
		t.Fatal("没有可识别条目应报错")
	}
	if !strings.Contains(err.Error(), "没有可识别") {
		t.Fatalf("错误文案不符：%s", err.Error())
	}
}

// dsh 的可识别守卫:手工改过(去掉 baseURL)的 reins- 路由不自动删,与其余
// 四家同口径(apply 必写 baseURL,pi-ai 方言同源)。
func TestDshRemoveRejectsModifiedEntry(t *testing.T) {
	home := newIsolated(t)
	home.seedProvider(t, "p1", ProtocolOpenaiChatCompletions, "https://p1.test/v1")
	home.apply(t, "p1", agentsDsh, []string{"model-a"}, "model-a", nil)

	// 篡改:去掉 reins-p1 路由的 baseURL,模拟用户手工改过内容。
	profile := dshProfilePatchOf(t, home)
	editPatch(t, profile, func(root *yaml.Node) {
		row := findRow(root.Content, "llm-pi-ai")
		if row == nil {
			t.Fatal("llm-pi-ai 行缺失")
		}
		providers := support.NodeGet(support.NodeGet(row, "config"), "providers")
		route := support.NodeGet(providers, "reins-p1")
		if route == nil {
			t.Fatal("reins-p1 路由应存在")
		}
		support.NodeRemove(route, "baseURL")
	})

	_, err := removeProviderFromAppInner(home.store, home.env, "p1", agentsDsh)
	if err == nil {
		t.Fatal("手工改过的条目应报错")
	}
	if !strings.Contains(err.Error(), "已被手工修改") {
		t.Fatalf("错误文案不符：%s", err.Error())
	}
	// 守卫生效时条目保持原样,不被静默删除。
	root := mustYAMLDoc(t, mustRead(t, profile))
	row := findPatchRow(t, root, "llm-pi-ai")
	if row == nil || jopt(row, "config", "providers", "reins-p1") == nil {
		t.Fatal("拒绝移除时不应动配置")
	}
}

// editPatch：以 yaml.Node 形态原位编辑 patch 文件（保标签、保键序）。
func editPatch(t *testing.T, path string, mutate func(root *yaml.Node)) {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(mustRead(t, path)), &document); err != nil {
		t.Fatalf("解析 patch: %v", err)
	}
	if len(document.Content) == 0 {
		t.Fatal("patch 根缺失")
	}
	mutate(document.Content[0])
	serialized, err := yaml.Marshal(document.Content[0])
	if err != nil {
		t.Fatalf("序列化 patch: %v", err)
	}
	mustWrite(t, path, string(serialized))
}

// 移除策略同型守卫:五个带注册键的适配器,无条目语义必须一致——remove 报
// 「没有可识别」、remove_external 报「没有外部条目」。可识别删除与「已被
// 手工修改」拒绝由各家专项用例覆盖;claude 没有注册键(契约是 base_url
// 归属校验),不在这张表里。
func TestRemovalNoEntryPolicyIsUniformAcrossEntryBearingAdapters(t *testing.T) {
	for _, app := range []ProviderApp{
		agentsCodex,
		agentsOpencode,
		agentsPi,
		agentsGrokbuild,
		agentsDsh,
	} {
		// dsh 的路由只认 chat 协议(pi-ai 同源),其余四家走 responses。
		protocol := ProtocolOpenaiResponses
		if app == agentsDsh {
			protocol = ProtocolOpenaiChatCompletions
		}
		home := newIsolated(t)
		home.seedProvider(t, "p1", protocol, "https://p1.test/v1")
		home.apply(t, "p1", app, []string{"model-a"}, "model-a", nil)
		if err := home.removeFrom(t, "p1", app); err != nil {
			t.Fatalf("%s 首次移除: %v", app, err)
		}

		err := home.removeFrom(t, "p1", app)
		if err == nil {
			t.Fatalf("%s 再次移除应报无条目", app)
		}
		if !strings.Contains(err.Error(), "没有可识别") {
			t.Fatalf("%s：%s", app, err.Error())
		}

		err = home.removeExternal(t, app, "vendor-gw")
		if err == nil {
			t.Fatalf("%s 无外部条目应报错", app)
		}
		if !strings.Contains(err.Error(), "没有外部条目") {
			t.Fatalf("%s：%s", app, err.Error())
		}
	}
}

func TestDshRemoveExternalClearsEntryAndDefault(t *testing.T) {
	home := newIsolated(t)
	profile := dshProfilePatchOf(t, home)
	mustMkdirAll(t, filepath.Dir(profile))
	mustWrite(t, profile, "- id: llm-pi-ai\n  name: '@deepseek-ai/dsh-llm-pi-ai'\n  config:\n    providers:\n      other-gw:\n        baseURL: https://other.test/v1\n        api: anthropic-messages\n- id: agent-default-model\n  name: '@deepseek-ai/dsh-agent-default-model'\n  config:\n    provider: other-gw\n    model: other-model\n")

	if err := home.removeExternal(t, agentsDsh, "other-gw"); err != nil {
		t.Fatalf("remove external: %v", err)
	}

	root := mustYAMLDoc(t, mustRead(t, profile))
	if findPatchRow(t, root, "llm-pi-ai") != nil {
		t.Fatal("llm-pi-ai 行应删除")
	}
	if findPatchRow(t, root, "agent-default-model") != nil {
		t.Fatal("agent-default-model 行应删除")
	}
}

func TestDshHonorsDshHome(t *testing.T) {
	home := newIsolated(t)
	home.seedProviderWithModels(t, "p1", ProtocolOpenaiResponses, "https://p1.test/v1", []string{"model-a"})
	customDir := testsupport.TestDir(t, "dsh-home")
	env := envWith(home.env, "DSH_HOME", customDir)

	if _, err := applyProviderInner(home.store, env,
		plan("p1", agentsDsh, []string{"model-a"}, "model-a", nil)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(customDir, "profiles", "desktop", "cordis.patch.yml")); err != nil {
		t.Fatalf("重定向目录应被使用：%v", err)
	}
	if _, err := os.Stat(filepath.Join(customDir, ".credentials.yaml")); err != nil {
		t.Fatalf("凭据应写入重定向目录：%v", err)
	}
	mustNotExist(t, dshProfilePatchOf(t, home))
}

func TestDshBrokenPatchReportsLoadError(t *testing.T) {
	home := newIsolated(t)
	profile := dshProfilePatchOf(t, home)
	mustMkdirAll(t, filepath.Dir(profile))
	mustWrite(t, profile, "- id: llm-pi-ai\n  config: [unclosed\n")

	state := home.state(t)
	dsh := findAppState(t, state, agentsDsh)
	if dsh.LoadError == nil {
		t.Fatalf("损坏的 patch 应挂在 load_error：%+v", dsh)
	}
	if len(dsh.Entries) != 0 {
		t.Fatalf("entries: %d", len(dsh.Entries))
	}
}
