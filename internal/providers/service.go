// service.go：providers 域的 Wails service 薄壳（对齐 Rust 版 providers/
// commands.rs 的 11 个命令）。Rust 命令层由 serde 完成 enum 反序列化校验，
// Go 侧入参是 wire 串，parse* 助手承担同一职责；诊断日志与 Rust 逐字对齐
// （Debug 拼写经 debugProviderApp/debugReasoningLevel）。错误经 error 返回
// （Wails 转 Promise reject），文案由 inner 保证与 Rust 逐字一致。
package providers

import (
	"fmt"
	"unicode"

	"reins/internal/agents"
	"reins/internal/logger"
	"reins/internal/support"
)

// ProvidersService：聚合平台模型配置域的 Wails service。ProviderConfigStore
// 由应用层（main）构造注入。
type ProvidersService struct {
	store *ProviderConfigStore
}

// NewProvidersService 构造 service。
func NewProvidersService(store *ProviderConfigStore) *ProvidersService {
	return &ProvidersService{store: store}
}

// env 对齐 Rust 命令层每次 Environment::from_env() 的口径：环境视图不缓存。
func (s *ProvidersService) env() *support.Environment {
	return support.FromEnv(agents.Redirects())
}

// GetProvidersState 对齐 get_providers_state。
func (s *ProvidersService) GetProvidersState() (*ProvidersState, error) {
	return providersStateInner(s.store, s.env())
}

// GetProviderAppState 对齐 get_provider_app_state（单个工具的定点反显）。
func (s *ProvidersService) GetProviderAppState(app string) (*ProviderAppState, error) {
	parsedApp, err := parseProviderApp(app)
	if err != nil {
		return nil, err
	}
	return appStateInner(s.store, s.env(), parsedApp)
}

// UpsertProvider 对齐 upsert_provider：返回落库后的归一化平台 ID。
func (s *ProvidersService) UpsertProvider(input ProviderUpsertInput) (string, error) {
	logger.Info(fmt.Sprintf("upsert_provider provider_id=%s", input.ProviderID))
	return s.store.Upsert(input)
}

// SetProviderAppReasoningLevel 对齐 set_provider_app_reasoning_level；level
// nil 表示不设置。
func (s *ProvidersService) SetProviderAppReasoningLevel(app string, defaultReasoningLevel *string) error {
	parsedApp, err := parseProviderApp(app)
	if err != nil {
		return err
	}
	parsedLevel, err := parseReasoningLevelPtr(defaultReasoningLevel)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf(
		"set_provider_app_reasoning_level app=%s level=%s",
		debugProviderApp(parsedApp), debugReasoningLevelOption(parsedLevel),
	))
	return s.store.SetAppReasoningLevel(parsedApp, parsedLevel)
}

// DeleteProvider 对齐 delete_provider。
func (s *ProvidersService) DeleteProvider(providerID string) (ProviderMutationResult, error) {
	logger.Info(fmt.Sprintf("delete_provider provider_id=%s", providerID))
	return deleteProviderInner(s.store, s.env(), providerID)
}

// FetchProviderModelsDirect 对齐 fetch_provider_models_direct（按表单当前值
// 拉取，不读 providers.yaml）。
func (s *ProvidersService) FetchProviderModelsDirect(protocol string, baseURL string, apiKey string) (*FetchedModelsResult, error) {
	parsedProtocol, err := parseProviderProtocol(protocol)
	if err != nil {
		return nil, err
	}
	return fetchProviderModelsDirectInner(parsedProtocol, baseURL, apiKey)
}

// FetchModelsdevCatalog 对齐 fetch_modelsdev_catalog；forceRefresh nil = false。
func (s *ProvidersService) FetchModelsdevCatalog(providerID string, modelID string, forceRefresh *bool) (*ModelsDevMatchResult, error) {
	refresh := false
	if forceRefresh != nil {
		refresh = *forceRefresh
	}
	return matchModelsdev(s.store.AppDir(), providerID, modelID, refresh)
}

// PreviewProviderApply 对齐 preview_provider_apply。
func (s *ProvidersService) PreviewProviderApply(input ApplyProviderInput) (*ProviderApplyPreview, error) {
	return previewApplyInner(s.store, s.env(), &input)
}

// ApplyProviderToApp 对齐 apply_provider_to_app。
func (s *ProvidersService) ApplyProviderToApp(input ApplyProviderInput) (ProviderMutationResult, error) {
	logger.Info(fmt.Sprintf(
		"apply_provider_to_app provider_id=%s app=%s", input.ProviderID, debugProviderApp(input.App),
	))
	return applyProviderInner(s.store, s.env(), &input)
}

// RemoveProviderFromApp 对齐 remove_provider_from_app。
func (s *ProvidersService) RemoveProviderFromApp(providerID string, app string) (ProviderMutationResult, error) {
	parsedApp, err := parseProviderApp(app)
	if err != nil {
		return ProviderMutationResult{}, err
	}
	logger.Info(fmt.Sprintf("remove_provider_from_app provider_id=%s app=%s", providerID, debugProviderApp(parsedApp)))
	return removeProviderFromAppInner(s.store, s.env(), providerID, parsedApp)
}

// RemoveExternalEntry 对齐 remove_external_entry。
func (s *ProvidersService) RemoveExternalEntry(app string, entryKey string) (ProviderMutationResult, error) {
	parsedApp, err := parseProviderApp(app)
	if err != nil {
		return ProviderMutationResult{}, err
	}
	logger.Info(fmt.Sprintf("remove_external_entry app=%s key=%s", debugProviderApp(parsedApp), entryKey))
	return removeExternalEntryInner(s.env(), parsedApp, entryKey)
}

// ---------------------------------------------------------------------------
// wire 串 → 枚举（Rust 命令层 serde 反序列化边界的对应物）
// ---------------------------------------------------------------------------

// parseProviderApp：工具 wire 串 → 枚举。错误文案与域内未知工具的报错同款。
func parseProviderApp(value string) (ProviderApp, error) {
	app := ProviderApp(value)
	if agents.SpecByProviderApp(app) == nil {
		return "", fmt.Errorf("未知的工具：%s", value)
	}
	return app, nil
}

// parseProviderProtocol：协议 wire 串 → 枚举。
func parseProviderProtocol(value string) (ProviderProtocol, error) {
	protocol := ProviderProtocol(value)
	for _, candidate := range AllProtocols() {
		if candidate == protocol {
			return protocol, nil
		}
	}
	return "", fmt.Errorf("未知协议：%s", value)
}

// parseReasoningLevelPtr：可选思考等级 wire 串 → 枚举指针。
func parseReasoningLevelPtr(value *string) (*ReasoningLevel, error) {
	if value == nil {
		return nil, nil
	}
	level, ok := ParseReasoningLevel(*value)
	if !ok {
		return nil, fmt.Errorf("未知思考等级：%s", *value)
	}
	return &level, nil
}

// debugProviderApp：日志用的 Rust {:?} 拼写（变体 PascalCase：codex → Codex）。
func debugProviderApp(app ProviderApp) string {
	return capitalize(string(app))
}

// debugReasoningLevel：日志用的 Rust {:?} 拼写（off → Off）。
func debugReasoningLevel(level ReasoningLevel) string {
	return capitalize(string(level))
}

// debugReasoningLevelOption：Rust Option<ReasoningLevel> 的 {:?} 拼写。
func debugReasoningLevelOption(level *ReasoningLevel) string {
	if level == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%s)", debugReasoningLevel(*level))
}

// capitalize：Debug 拼写只有首字母大写一个变换（xhigh → Xhigh 同样成立）。
func capitalize(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
