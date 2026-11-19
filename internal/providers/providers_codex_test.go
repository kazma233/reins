// Codex 适配器端到级行为：「生成 → 反读 → 幂等 → 移除」闭环（对齐
// tests/providers.rs 的 Codex 一节）。
package providers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexApplyWritesEntryAndPointer(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", levelPtr(ReasoningMedium))

	content := mustRead(t, mustCodexConfigPath(t, home.env))
	for _, expected := range []string{
		`model_provider = "reins-p1"`,
		`model = "model-a"`,
		`model_reasoning_effort = "medium"`,
		`base_url = "https://p1.test/v1"`,
		`wire_api = "responses"`,
		`experimental_bearer_token = "sk-test-secret"`,
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("config.toml 缺少 %s：\n%s", expected, content)
		}
	}

	// 反读：已应用 + 默认模型。
	state := home.state(t)
	codex := findAppState(t, state, agentsCodex)
	if len(codex.Entries) != 1 {
		t.Fatalf("entries: %d", len(codex.Entries))
	}
	if codex.Entries[0].Status != EntryStatusApplied {
		t.Fatalf("status: %s", codex.Entries[0].Status)
	}
	if codex.Entries[0].DefaultModelID == nil || *codex.Entries[0].DefaultModelID != "model-a" {
		t.Fatalf("default model: %v", codex.Entries[0].DefaultModelID)
	}
}

// 「目录文件必须先于 config.toml 写入」此前只写在 codex 适配器的注释里：
// config.toml 里的 model_catalog_json 一旦指向缺失或非法的文件，Codex 配置
// 加载会整体失败。这条把注释变成被守卫的事实。
func TestCodexApplyWritesCatalogBeforeConfig(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	preview, err := previewApplyInner(home.store, home.env,
		plan("p1", agentsCodex, []string{"model-a"}, "model-a", nil))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	if len(preview.Files) != 2 {
		t.Fatalf("codex 应用产物应是两个文件：%d", len(preview.Files))
	}
	catalogFile := preview.Files[0]
	configFile := preview.Files[1]
	if !strings.HasSuffix(catalogFile.Path, "reins-models.json") {
		t.Fatalf("目录文件必须排在 config.toml 之前：%s", catalogFile.Path)
	}
	if !strings.HasSuffix(configFile.Path, "config.toml") {
		t.Fatalf("config.toml 必须是最后一个：%s", configFile.Path)
	}

	// 指针值与目录文件的实际路径必须一致，否则「先写目录」也救不了。
	configContent := renderDiff(t, configFile.Diff)
	catalogContent := renderDiff(t, catalogFile.Diff)
	if !strings.Contains(configContent, "model_catalog_json") || !strings.Contains(configContent, catalogFile.Path) {
		t.Fatalf("config.toml 的 model_catalog_json 应指向目录文件 %s：\n%s", catalogFile.Path, configContent)
	}
	// 目录文件是给 Codex 读的模型清单，不该反过来带指针。
	if strings.Contains(catalogContent, "model_catalog_json") {
		t.Fatalf("目录文件不应带指针：\n%s", catalogContent)
	}
}

// off 档无文档依据（写入 "none"），能力表不放行。
func TestCodexOffReasoningLevelIsRejected(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	_, err := applyProviderInner(home.store, home.env,
		plan("p1", agentsCodex, []string{"model-a"}, "model-a", levelPtr(ReasoningOff)))
	if err == nil {
		t.Fatal("off level should be rejected for codex")
	}
	if !strings.Contains(err.Error(), "不支持思考等级") {
		t.Fatalf("错误文案不符：%s", err.Error())
	}
}

func TestCodexApplyIsIdempotentAndReplacesPreviousPlatform(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.seedModel(t, "p2", "model-b")

	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	first := mustRead(t, mustCodexConfigPath(t, home.env))
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	if second := mustRead(t, mustCodexConfigPath(t, home.env)); first != second {
		t.Fatalf("重复应用不应产生变更：\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	// 替换语义：应用 p2 后 p1 的条目和指针都应消失。
	home.apply(t, "p2", agentsCodex, []string{"model-b"}, "model-b", nil)
	content := mustRead(t, mustCodexConfigPath(t, home.env))
	if strings.Contains(content, "reins-p1") {
		t.Fatalf("旧平台条目应被替换：\n%s", content)
	}
	if !strings.Contains(content, `model_provider = "reins-p2"`) {
		t.Fatalf("新平台应成为活动 Provider：\n%s", content)
	}
}

func TestCodexRemoveCleansPointerAndRejectsModifiedEntry(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", levelPtr(ReasoningLow))

	if err := home.removeFrom(t, "p1", agentsCodex); err != nil {
		t.Fatalf("remove: %v", err)
	}
	content := mustRead(t, mustCodexConfigPath(t, home.env))
	for _, unexpected := range []string{"reins-p1", "model_provider =", "model_reasoning_effort", "\nmodel ="} {
		if strings.Contains(content, unexpected) {
			t.Fatalf("移除后不应残留 %q：\n%s", unexpected, content)
		}
	}

	// 漂移配置（手工删掉 wire_api）不应被自动删。
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	path := mustCodexConfigPath(t, home.env)
	edited := strings.Replace(mustRead(t, path), `wire_api = "responses"`, "# removed", 1)
	mustWrite(t, path, edited)
	if err := home.removeFrom(t, "p1", agentsCodex); err == nil {
		t.Fatal("手工改过的漂移配置不应被自动删除")
	}
}

// 同一平台重新应用（例如只换默认模型）不选思考等级时保留已写入的值；
// 换到另一个平台才清掉，避免旧平台的等级留在唯一的活动配置上。
func TestCodexReapplyKeepsReasoningEffortButSwitchClearsIt(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.seedModel(t, "p2", "model-a")

	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	if content := mustRead(t, mustCodexConfigPath(t, home.env)); !strings.Contains(content, `model_reasoning_effort = "high"`) {
		t.Fatalf("重新应用应保留已写等级：\n%s", content)
	}

	home.apply(t, "p2", agentsCodex, []string{"model-a"}, "model-a", nil)
	if content := mustRead(t, mustCodexConfigPath(t, home.env)); strings.Contains(content, "model_reasoning_effort") {
		t.Fatalf("换平台应清掉旧等级：\n%s", content)
	}
}

// 聚合模型的窗口写入顶层 model_context_window：codex 对不在内置目录的模型
// 会走兜底元数据（272000）并告警 Unknown model。
func TestCodexApplyWritesAndClearsModelContextWindow(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	if content := mustRead(t, mustCodexConfigPath(t, home.env)); !strings.Contains(content, "model_context_window = 200000") {
		t.Fatalf("应写入 model_context_window：\n%s", content)
	}

	// 元数据清空后替换应用不再写该键，避免残留旧窗口。
	input := providerInput("p1", ProtocolOpenaiResponses, "https://p1.test/v1")
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)
	if content := mustRead(t, mustCodexConfigPath(t, home.env)); strings.Contains(content, "model_context_window") {
		t.Fatalf("元数据清空后不应写窗口：\n%s", content)
	}
}

// 移除时窗口随配置清掉（值仍等于该模型元数据 → 归属可确认）。
func TestCodexRemoveClearsModelContextWindow(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)

	if err := home.removeFrom(t, "p1", agentsCodex); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if content := mustRead(t, mustCodexConfigPath(t, home.env)); strings.Contains(content, "model_context_window") {
		t.Fatalf("移除后窗口应清掉：\n%s", content)
	}
}

// model_catalog_json + reins-models.json：条目字段对齐 codex 0.158.0 的
// ModelInfo（不再写 0.145 时代的 supports_parallel_tool_calls），系统提示用
// model_messages.instructions_template，visibility=list 让模型进选择器。
func TestCodexApplyWritesModelCatalogAndRemoveClearsPointer(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")

	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", levelPtr(ReasoningHigh))
	config := mustTOML(t, mustRead(t, mustCodexConfigPath(t, home.env)))
	catalogPath := filepath.Join(mustCodexHome(t, home.env), catalogFileName)
	if pointer, ok := config["model_catalog_json"].(string); !ok || pointer != catalogPath {
		t.Fatalf("model_catalog_json 应指向 %s：%v", catalogPath, config["model_catalog_json"])
	}
	catalog := mustJSON(t, mustRead(t, catalogPath))
	entry := jpath(t, catalog, "models").([]any)[0].(map[string]any)
	if jstr(t, entry, "slug") != "model-a" {
		t.Fatalf("slug: %v", entry["slug"])
	}
	if jstr(t, entry, "visibility") != "list" || jbool(t, entry, "supported_in_api") != true ||
		jstr(t, entry, "shell_type") != "default" {
		t.Fatalf("entry 字段不符：%+v", entry)
	}
	// supports_parallel_tool_calls 在 codex 0.158.0 已删除，不再写。
	if jopt(entry, "supports_parallel_tool_calls") != nil {
		t.Fatal("不应再写 supports_parallel_tool_calls")
	}
	if jstr(t, entry, "truncation_policy", "mode") != "bytes" {
		t.Fatalf("truncation policy: %v", entry["truncation_policy"])
	}
	if jnum(t, entry, "context_window") != 200_000 {
		t.Fatalf("context window: %v", entry["context_window"])
	}
	modalities := jpath(t, entry, "input_modalities").([]any)
	if len(modalities) != 2 || modalities[1] != "image" {
		t.Fatalf("input modalities: %v", modalities)
	}
	if jstr(t, entry, "default_reasoning_level") != "high" {
		t.Fatalf("default reasoning level: %v", entry["default_reasoning_level"])
	}
	// instructions_template 非空：空提示词会让会话直接失焦。
	template := jstr(t, entry, "model_messages", "instructions_template")
	if len(template) <= 100 {
		t.Fatalf("instructions_template 应为完整兜底提示词（长度 %d）", len(template))
	}

	// 移除平台：指针随条目清掉；目录文件不再被引用，留在原地无害。
	if err := home.removeFrom(t, "p1", agentsCodex); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if content := mustRead(t, mustCodexConfigPath(t, home.env)); strings.Contains(content, "model_catalog_json") {
		t.Fatalf("指针应随移除清掉：\n%s", content)
	}
	if _, err := os.Stat(catalogPath); err != nil {
		t.Fatalf("目录文件应留在原地：%v", err)
	}
}

// 用户所选档位不在模型元数据档位里时（如三档元数据选了 max），所选档位
// 按强度序并入条目档位列表并写为默认档：codex /model 只认条目列表，当前
// 档位不在列表里会被回落到列表中间档，用户所选被静默丢弃。
func TestCodexCatalogAppendsSelectedEffortBeyondMetadata(t *testing.T) {
	home := newIsolated(t)
	// 元数据三档、所选 max：追加到列表尾部。
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", levelPtr(ReasoningMax))
	catalogPath := filepath.Join(mustCodexHome(t, home.env), catalogFileName)
	entry := firstCatalogModel(t, mustRead(t, catalogPath))
	if jstr(t, entry, "default_reasoning_level") != "max" {
		t.Fatalf("default reasoning level: %v", entry["default_reasoning_level"])
	}
	assertCatalogLevels(t, entry, "low", "medium", "high", "max")

	// 元数据跳档（low/max）、所选 medium：按强度序插中间，列表保持有序。
	input := providerInput("p2", ProtocolOpenaiResponses, "https://p2.test/v1")
	input.Models = []ProviderModelInput{{
		ID:            "model-b",
		Label:         "Label model-b",
		ContextWindow: ptrInt64(200_000),
		Reasoning:     ptrBool(true),
		ReasoningLevels: []ReasoningLevel{
			ReasoningLow,
			ReasoningMax,
		},
	}}
	if _, err := home.store.Upsert(input); err != nil {
		t.Fatal(err)
	}
	home.apply(t, "p2", agentsCodex, []string{"model-b"}, "model-b", levelPtr(ReasoningMedium))
	entry = firstCatalogModel(t, mustRead(t, catalogPath))
	if jstr(t, entry, "default_reasoning_level") != "medium" {
		t.Fatalf("default reasoning level: %v", entry["default_reasoning_level"])
	}
	assertCatalogLevels(t, entry, "low", "medium", "max")
}

func assertCatalogLevels(t *testing.T, entry map[string]any, expected ...string) {
	t.Helper()
	levels := jpath(t, entry, "supported_reasoning_levels").([]any)
	if len(levels) != len(expected) {
		t.Fatalf("档位数量不符：%v", levels)
	}
	for i, level := range expected {
		if got := jpath(t, levels[i].(map[string]any), "effort"); got != level {
			t.Fatalf("第 %d 档应为 %s：%v", i, level, got)
		}
		if got := jpath(t, levels[i].(map[string]any), "description"); got != "" {
			t.Fatalf("档位描述应为空：%v", got)
		}
	}
}

func firstCatalogModel(t *testing.T, content string) map[string]any {
	t.Helper()
	catalog := mustJSON(t, content)
	return jpath(t, catalog, "models").([]any)[0].(map[string]any)
}

// 用户自己的 model_catalog_json 指向不归 Reins 管，移除时不动。
func TestCodexRemoveKeepsUserModelCatalogJSON(t *testing.T) {
	home := newIsolated(t)
	home.seedModel(t, "p1", "model-a")
	home.apply(t, "p1", agentsCodex, []string{"model-a"}, "model-a", nil)

	path := mustCodexConfigPath(t, home.env)
	edited := mustTOML(t, mustRead(t, path))
	edited["model_catalog_json"] = "C:/custom/models.json"
	mustWriteTOML(t, path, edited)

	if err := home.removeFrom(t, "p1", agentsCodex); err != nil {
		t.Fatalf("remove: %v", err)
	}
	after := mustTOML(t, mustRead(t, mustCodexConfigPath(t, home.env)))
	if pointer, ok := after["model_catalog_json"].(string); !ok || pointer != "C:/custom/models.json" {
		t.Fatalf("用户自设指针不应被清掉：%v", after["model_catalog_json"])
	}
}
