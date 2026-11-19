// 命令层：Phase 5 会以薄壳 service 方法暴露给 Wails；本文件只收可测试的
// inner 核心（对齐 Rust 版 providers/commands.rs 的 *_inner），环境
// 视图由调用方注入。
package providers

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"reins/internal/agents"
	"reins/internal/support"
)

// ProvidersStateInner：全量状态：providers.yaml 全部平台 + 7 个工具卡片。
func providersStateInner(store *ProviderConfigStore, env *support.Environment) (*ProvidersState, error) {
	providers, err := store.Load()
	if err != nil {
		return nil, err
	}
	appSettings, err := store.AppSettings()
	if err != nil {
		return nil, err
	}
	apps := make([]ProviderAppState, 0, len(agents.ProviderApps()))
	for _, app := range agents.ProviderApps() {
		apps = append(apps, *inspectApp(providers, env, app, appSettings))
	}

	views := make([]ProviderView, 0, len(providers))
	for _, id := range sortedProviderIDs(providers) {
		view, err := providerView(providers[id])
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}

	return &ProvidersState{
		ConfigPath: support.DisplayPath(store.ConfigPath()),
		Providers:  views,
		Apps:       apps,
	}, nil
}

// sortedProviderIDs：BTreeMap 迭代序（平台列表按 ID 字典序展示）。
func sortedProviderIDs(providers map[string]*ResolvedProvider) []string {
	ids := make([]string, 0, len(providers))
	for id := range providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AppStateInner：单个工具的定点反显：写操作只影响一个工具的配置文件，据此
// 只重读该工具，不重读其余工具，也不查密钥（providers.yaml 与密钥都不是
// 写操作的对象）。
func appStateInner(store *ProviderConfigStore, env *support.Environment, app ProviderApp) (*ProviderAppState, error) {
	providers, err := store.Load()
	if err != nil {
		return nil, err
	}
	appSettings, err := store.AppSettings()
	if err != nil {
		return nil, err
	}
	return inspectApp(providers, env, app, appSettings), nil
}

func inspectApp(
	providers map[string]*ResolvedProvider,
	env *support.Environment,
	app ProviderApp,
	appSettings map[ProviderApp]ProviderAppSetting,
) *ProviderAppState {
	adapter := AdapterFor(app)
	state, err := adapter.Inspect(env, providers)
	if err != nil {
		// 单个工具配置损坏不拖垮整页；错误挂到对应卡片上。
		state = EmptyState(app, AppConfigPaths(env, app))
		message := err.Error()
		state.LoadError = &message
	}
	if setting, has := appSettings[app]; has && setting.DefaultReasoningLevel != nil {
		level := *setting.DefaultReasoningLevel
		state.AppReasoningLevel = &level
	}
	return state
}

// AppConfigPaths：inspect 失败的兜底展示与外部调用共用；路径解析失败由
// 调用方兜底为空列表。
func AppConfigPaths(env *support.Environment, app ProviderApp) []string {
	adapter := AdapterFor(app)
	if adapter == nil {
		return nil
	}
	paths, err := adapter.ConfigPaths(env)
	if err != nil {
		return nil
	}
	return paths
}

func providerView(provider *ResolvedProvider) (ProviderView, error) {
	models := make([]ProviderModelView, 0, len(provider.Models))
	for i := range provider.Models {
		model := &provider.Models[i]
		models = append(models, ProviderModelView{
			ID:              model.ID,
			Label:           model.Label,
			ContextWindow:   model.ContextWindow,
			MaxOutputTokens: model.MaxOutputTokens,
			SupportsImages:  model.SupportsImages,
			Reasoning:       model.Reasoning,
			ReasoningLevels: model.ReasoningLevels,
		})
	}
	var apiKey *string
	if key, ok := provider.StoredAPIKey(); ok {
		apiKey = &key
	}
	return ProviderView{
		ID:       provider.ID,
		Label:    provider.Label,
		Protocol: provider.Protocol,
		BaseURL:  provider.BaseURL,
		APIKey:   apiKey,
		Models:   models,
	}, nil
}

// DeleteProviderInner：Claude 的写入没有结构化注册键，平台删掉后就无法
// 安全移除了，所以删除前强制先从 Claude Code 摘除；其他工具可按 reins-
// 前缀结构化移除，不阻断。
func deleteProviderInner(store *ProviderConfigStore, env *support.Environment, providerID string) (ProviderMutationResult, error) {
	providers, err := store.Load()
	if err != nil {
		return ProviderMutationResult{}, err
	}
	if _, exists := providers[providerID]; exists {
		claudeState, err := claudeAdapter.Inspect(env, providers)
		if err != nil {
			return ProviderMutationResult{}, err
		}
		referenced := false
		for _, entry := range claudeState.Entries {
			if entry.ProviderID != nil && *entry.ProviderID == providerID {
				referenced = true
				break
			}
		}
		if referenced {
			claudeLabel := ProviderAppLabel(agents.ProviderClaude)
			return ProviderMutationResult{}, fmt.Errorf(
				"%s 仍在引用该平台，请先从 %s 移除后再删除平台。", claudeLabel, claudeLabel)
		}
	}

	if err := store.Delete(providerID); err != nil {
		return ProviderMutationResult{}, err
	}
	return ProviderMutationResult{
		App:        nil,
		ProviderID: stringPtr(providerID),
		Action:     "delete",
		Detail:     fmt.Sprintf("平台 %s 已删除。", providerID),
	}, nil
}

// computeApply：生成应用产物，随产物一并带回平台的 StoredAPIKey——preview
// 的旧文件脱敏与 apply 的写入共用同一次 Load，消除双读之间的配置漂移窗口。
// preview=true 时产物内容里的密钥用脱敏占位符，真实密钥只用于旧文件脱敏。
func computeApply(store *ProviderConfigStore, env *support.Environment, input *ApplyProviderInput, preview bool) ([]FileOutput, []string, string, error) {
	providers, err := store.Load()
	if err != nil {
		return nil, nil, "", err
	}
	provider, exists := providers[input.ProviderID]
	if !exists {
		return nil, nil, "", fmt.Errorf("平台不存在：%s", input.ProviderID)
	}
	adapter := AdapterFor(input.App)
	if adapter == nil {
		return nil, nil, "", fmt.Errorf("未知的工具：%s", input.App)
	}
	if err := EnsureProtocolSupported(adapter, provider); err != nil {
		return nil, nil, "", err
	}
	warnings, err := ValidatePlan(adapter, provider, input)
	if err != nil {
		return nil, nil, "", err
	}

	storedKey, hasKey := provider.StoredAPIKey()
	if !hasKey {
		return nil, nil, "", fmt.Errorf("请先设置平台 %s 的 API Key。", input.ProviderID)
	}
	apiKey := storedKey
	if preview {
		apiKey = MaskedKey
	}

	files, err := adapter.Apply(env, provider, input, apiKey)
	if err != nil {
		return nil, nil, "", err
	}
	return files, warnings, storedKey, nil
}

// writeAndRenderPaths：落盘 adapter 生成的文件并渲染展示路径列表——apply /
// remove / remove_external 三条命令共享的写入与 detail 渲染口径；受限清单
// （owner-only 权限）由调用方从 adapter 取。
func writeAndRenderPaths(files []FileOutput, restricted []string) ([]string, error) {
	if err := WriteFiles(files, restricted); err != nil {
		return nil, err
	}
	written := make([]string, 0, len(files))
	for _, file := range files {
		written = append(written, support.DisplayPath(file.Path))
	}
	return written, nil
}

// PreviewApplyInner：diff 的 Context/Remove 行只允许出现掩码：旧文件可能
// 带着上次 apply 写入的真实密钥，用当前密钥值替换。密钥轮换后的历史旧值
// 无从得知，超出此处的能力边界。
func previewApplyInner(store *ProviderConfigStore, env *support.Environment, input *ApplyProviderInput) (*ProviderApplyPreview, error) {
	// secret 与产物来自同一份 providers.yaml 快照：二次 Load 会引入「生成时
	// 与脱敏时密钥不同」的一致性窗口。
	files, warnings, secret, err := computeApply(store, env, input, true)
	if err != nil {
		return nil, err
	}
	previews := make([]ProviderFilePreview, 0, len(files))
	for _, file := range files {
		exists := fileExists(file.Path)
		// 读失败要让预览失败，而不是把不可读的旧文件当成空文件展示成
		// 全量新增。
		oldContent := ""
		if exists {
			raw, err := os.ReadFile(file.Path)
			if err != nil {
				return nil, fmt.Errorf("Failed to read %s", file.Path)
			}
			oldContent = string(raw)
		}
		if secret != "" {
			oldContent = strings.ReplaceAll(oldContent, secret, MaskedKey)
		}
		previews = append(previews, ProviderFilePreview{
			Path:   support.DisplayPath(file.Path),
			Exists: exists,
			Diff:   DiffLines(oldContent, file.Content),
		})
	}
	return &ProviderApplyPreview{
		App:      input.App,
		Files:    previews,
		Warnings: warnings,
	}, nil
}

// ApplyProviderInner：原子写逐个文件执行；中途失败时已写文件保持新内容，
// 靠预览 + 幂等重试收敛，不做跨文件回滚。敏感文件（如 dsh 凭据）强制
// owner-only 权限，按 adapter 的声明分发。
func applyProviderInner(store *ProviderConfigStore, env *support.Environment, input *ApplyProviderInput) (ProviderMutationResult, error) {
	files, warnings, _, err := computeApply(store, env, input, false)
	if err != nil {
		return ProviderMutationResult{}, err
	}
	adapter := AdapterFor(input.App)
	written, err := writeAndRenderPaths(files, adapter.RestrictedPaths(env))
	if err != nil {
		return ProviderMutationResult{}, err
	}
	detail := fmt.Sprintf("已写入：%s", strings.Join(written, "、"))
	if len(warnings) > 0 {
		detail += fmt.Sprintf("（%d 条能力警告）", len(warnings))
	}
	return ProviderMutationResult{
		App:        &input.App,
		ProviderID: stringPtr(input.ProviderID),
		Action:     "apply",
		Detail:     detail,
	}, nil
}

func removeProviderFromAppInner(store *ProviderConfigStore, env *support.Environment, providerID string, app ProviderApp) (ProviderMutationResult, error) {
	providers, err := store.Load()
	if err != nil {
		return ProviderMutationResult{}, err
	}
	provider := providers[providerID]
	adapter := AdapterFor(app)
	if adapter == nil {
		return ProviderMutationResult{}, fmt.Errorf("未知的工具：%s", app)
	}
	files, err := adapter.Remove(env, providerID, provider)
	if err != nil {
		return ProviderMutationResult{}, err
	}
	written, err := writeAndRenderPaths(files, adapter.RestrictedPaths(env))
	if err != nil {
		return ProviderMutationResult{}, err
	}
	return ProviderMutationResult{
		App:        &app,
		ProviderID: stringPtr(providerID),
		Action:     "remove",
		Detail:     fmt.Sprintf("已从 %s 移除：%s", ProviderAppLabel(app), strings.Join(written, "、")),
	}, nil
}

// RemoveExternalEntryInner：外部条目不属于 providers.yaml，按工具配置里的
// 原样键结构化删除；破坏性由前端确认弹窗承担，后端只做键存在性检查；
// 默认模型/活动 Provider 仍指向被删条目时随删除清空，不再要求先切换。
func removeExternalEntryInner(env *support.Environment, app ProviderApp, entryKey string) (ProviderMutationResult, error) {
	adapter := AdapterFor(app)
	if adapter == nil {
		return ProviderMutationResult{}, fmt.Errorf("未知的工具：%s", app)
	}
	files, err := adapter.RemoveExternal(env, entryKey)
	if err != nil {
		return ProviderMutationResult{}, err
	}
	written, err := writeAndRenderPaths(files, adapter.RestrictedPaths(env))
	if err != nil {
		return ProviderMutationResult{}, err
	}
	return ProviderMutationResult{
		App:        &app,
		ProviderID: nil,
		Action:     "remove_external",
		Detail:     fmt.Sprintf("已从 %s 删除外部条目 %s：%s", ProviderAppLabel(app), entryKey, strings.Join(written, "、")),
	}, nil
}

// FetchProviderModelsDirectInner：按表单当前值拉取模型列表：不读
// providers.yaml、不写任何持久状态，因此用户改了表单还没保存时，拉取也
// 按改后的值走。
func fetchProviderModelsDirectInner(protocol ProviderProtocol, baseURL string, apiKey string) (*FetchedModelsResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("请先填写 API Key，再拉取模型列表。")
	}
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("请先填写 Base URL，再拉取模型列表。")
	}
	provider := &ResolvedProvider{
		ID:       "",
		Label:    "",
		Protocol: protocol,
		BaseURL:  baseURL,
		APIKey:   nil,
		Models:   nil,
	}
	return fetchProviderModelsInner(provider, strings.TrimSpace(apiKey))
}
