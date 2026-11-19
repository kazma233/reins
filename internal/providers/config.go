// providers.yaml 的唯一读写入口：与 Rust ProviderConfigStore 相同的模式——
// 单实例由应用层管理，进程内锁串行化 read-modify-write，原子写落盘。
// 凭据边界：API Key 明文只出现在本文件与目标工具配置文件；这里的任何错误
// 文案都不携带文件内容，只带路径。
package providers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reins/internal/support"
)

const providersConfigName = "providers.yaml"

// 平台 ID 与 workspace target id 同一字符集：小写字母、数字和 -。
func normalizeProviderID(value string) (string, error) {
	trimmed := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "_", "-")
	if trimmed == "" {
		return "", errors.New("平台 ID 不能为空。")
	}
	for _, ch := range trimmed {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return "", fmt.Errorf("平台 ID 只能包含小写字母、数字和 -：%s", strings.TrimSpace(value))
		}
	}
	if strings.HasPrefix(trimmed, "reins-") {
		// reins- 前缀是工具配置里的内部注册键空间，平台 ID 不允许占用。
		return "", errors.New("平台 ID 不能以 reins- 开头。")
	}
	return trimmed, nil
}

func validateBaseURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "https://") && !strings.HasPrefix(trimmed, "http://") {
		return "", fmt.Errorf("Base URL 必须以 http:// 或 https:// 开头：%s", trimmed)
	}
	return trimmed, nil
}

func normalizeModelRecord(input ProviderModelInput) (ProviderModelRecord, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return ProviderModelRecord{}, errors.New("模型 ID 不能为空。")
	}
	return ProviderModelRecord{
		ID:              id,
		Label:           normalizeModelLabel(input.Label, id),
		ContextWindow:   input.ContextWindow,
		MaxOutputTokens: input.MaxOutputTokens,
		SupportsImages:  input.SupportsImages,
		Reasoning:       input.Reasoning,
		ReasoningLevels: sortDedupReasoningLevels(input.ReasoningLevels),
	}, nil
}

func normalizeModelLabel(label string, id string) string {
	trimmed := strings.TrimSpace(label)
	if trimmed == "" {
		return id
	}
	return trimmed
}

// sortDedupReasoningLevels 按枚举 Ord 序排序去重（Rust sort + dedup）。
func sortDedupReasoningLevels(levels []ReasoningLevel) []ReasoningLevel {
	if levels == nil {
		return nil
	}
	sorted := make([]ReasoningLevel, len(levels))
	copy(sorted, levels)
	sort.SliceStable(sorted, func(i, j int) bool {
		return reasoningRank(sorted[i]) < reasoningRank(sorted[j])
	})
	deduped := sorted[:0]
	for i, level := range sorted {
		if i == 0 || sorted[i-1] != level {
			deduped = append(deduped, level)
		}
	}
	return deduped
}

func emptyDocument() rawProvidersDocument {
	return rawProvidersDocument{
		Version:     documentVersion,
		Providers:   map[string]rawProvider{},
		AppSettings: map[ProviderApp]ProviderAppSetting{},
	}
}

// resolvedProviders / rawProviders 与 Rust 的两份转换一一对应：Raw 是落盘
// 形态，Resolved 是内存工作形态；API Key 经 StoredAPIKey 过滤空白。
func resolvedProviders(document *rawProvidersDocument) map[string]*ResolvedProvider {
	providers := make(map[string]*ResolvedProvider, len(document.Providers))
	for id, raw := range document.Providers {
		models := make([]ProviderModelRecord, len(raw.Models))
		copy(models, raw.Models)
		providers[id] = &ResolvedProvider{
			ID:       id,
			Label:    raw.Label,
			Protocol: raw.Protocol,
			BaseURL:  raw.BaseURL,
			APIKey:   raw.APIKey,
			Models:   models,
		}
	}
	return providers
}

func rawProviders(providers map[string]*ResolvedProvider) map[string]rawProvider {
	raw := make(map[string]rawProvider, len(providers))
	for id, provider := range providers {
		models := make([]ProviderModelRecord, len(provider.Models))
		copy(models, provider.Models)
		var apiKey *string
		if key, ok := provider.StoredAPIKey(); ok {
			keyCopy := key
			apiKey = &keyCopy
		}
		raw[id] = rawProvider{
			Label:    provider.Label,
			Protocol: provider.Protocol,
			BaseURL:  provider.BaseURL,
			APIKey:   apiKey,
			Models:   models,
		}
	}
	return raw
}

// ProviderConfigStore：providers.yaml 存储层。
type ProviderConfigStore struct {
	// mu 串行化全部读改写（对齐 Rust Mutex<()> 的进程内锁语义）。
	mu         sync.Mutex
	configPath string
}

// NewProviderConfigStoreAt 以显式应用数据目录构造（测试与调用方注入用）。
func NewProviderConfigStoreAt(appDir string) *ProviderConfigStore {
	return &ProviderConfigStore{configPath: filepath.Join(appDir, providersConfigName)}
}

// NewProviderConfigStore 用生产应用数据目录（<config_dir>/reins，目录推导
// 单点在 support.ConfigDirInApp）构造。`reins` 段是存储契约（AGENTS.md：改名
// 即换存储位置），漏掉会把 providers.yaml 落到 config_dir 根、升级后读不到
// 既有配置——生产路径有测试守卫（TestProviderConfigStoreProductionLayout），
// 改这里必须同步它。
func NewProviderConfigStore() *ProviderConfigStore {
	return NewProviderConfigStoreAt(support.ConfigDirInApp())
}

func (s *ProviderConfigStore) ConfigPath() string { return s.configPath }

func (s *ProviderConfigStore) AppDir() string {
	parent := filepath.Dir(s.configPath)
	if parent == "" || parent == "." {
		return "."
	}
	return parent
}

// Load 读全部平台。文件不存在视为空目录，而不是错误；首次写入时才创建文件。
func (s *ProviderConfigStore) Load() (map[string]*ResolvedProvider, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.loadDocumentLocked()
	if err != nil {
		return nil, err
	}
	return resolvedProviders(document), nil
}

// loadDocumentLocked 的调用方必须已持有 s.mu。
func (s *ProviderConfigStore) loadDocumentLocked() (*rawProvidersDocument, error) {
	configPath := s.configPath
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			document := emptyDocument()
			return &document, nil
		}
		return nil, err
	}
	rawContent, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read %s: %w", configPath, err)
	}
	if strings.TrimSpace(string(rawContent)) == "" {
		document := emptyDocument()
		return &document, nil
	}
	document, err := unmarshalProvidersDocument(rawContent)
	if err != nil {
		return nil, fmt.Errorf("providers.yaml 解析失败：%s", configPath)
	}
	return document, nil
}

func (s *ProviderConfigStore) saveDocumentLocked(document *rawProvidersDocument) error {
	serialized, err := marshalProvidersDocument(document)
	if err != nil {
		return err
	}
	if err := support.WriteAtomic(s.configPath, serialized); err != nil {
		return fmt.Errorf("Failed to write %s: %w", s.configPath, err)
	}
	return nil
}

func (s *ProviderConfigStore) AppSettings() (map[ProviderApp]ProviderAppSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.loadDocumentLocked()
	if err != nil {
		return nil, err
	}
	settings := make(map[ProviderApp]ProviderAppSetting, len(document.AppSettings))
	for app, setting := range document.AppSettings {
		settings[app] = setting
	}
	return settings, nil
}

// SetAppReasoningLevel 是工具级设置的唯一写入口；供应商增删改与此共用同一
// 份文档，互不覆盖。
func (s *ProviderConfigStore) SetAppReasoningLevel(app ProviderApp, level *ReasoningLevel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.loadDocumentLocked()
	if err != nil {
		return err
	}
	setting := document.AppSettings[app]
	setting.DefaultReasoningLevel = level
	if level == nil {
		// 全空条目不落盘，避免文件里积累空壳。
		delete(document.AppSettings, app)
	} else {
		document.AppSettings[app] = setting
	}
	return s.saveDocumentLocked(document)
}

func (s *ProviderConfigStore) Upsert(input ProviderUpsertInput) (string, error) {
	providerID, err := normalizeProviderID(input.ProviderID)
	if err != nil {
		return "", err
	}
	mode := input.Mode
	label := strings.TrimSpace(input.Label)
	if label == "" {
		return "", errors.New("平台名称不能为空。")
	}
	baseURL, err := validateBaseURL(input.BaseURL)
	if err != nil {
		return "", err
	}

	models := make([]ProviderModelRecord, 0, len(input.Models))
	seenModels := map[string]bool{}
	for _, model := range input.Models {
		record, err := normalizeModelRecord(model)
		if err != nil {
			return "", err
		}
		if seenModels[record.ID] {
			return "", fmt.Errorf("模型 ID 重复：%s", record.ID)
		}
		seenModels[record.ID] = true
		models = append(models, record)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.loadDocumentLocked()
	if err != nil {
		return "", err
	}
	providers := resolvedProviders(document)
	// 冲突判定与写入在同一把锁内：新增不能把已存在的平台静默替换成新表单的
	// 元数据（连同它的模型目录一起丢）。
	switch mode {
	case WriteModeCreate:
		if _, exists := providers[providerID]; exists {
			return "", fmt.Errorf("提供商 ID 已存在：%s", providerID)
		}
	case WriteModeUpdate:
		if _, exists := providers[providerID]; !exists {
			return "", fmt.Errorf("提供商不存在：%s", providerID)
		}
	}
	var apiKey *string
	if trimmed := strings.TrimSpace(input.APIKey); trimmed != "" {
		keyCopy := trimmed
		apiKey = &keyCopy
	}
	providers[providerID] = &ResolvedProvider{
		ID:       providerID,
		Label:    label,
		Protocol: input.Protocol,
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Models:   models,
	}
	document.Providers = rawProviders(providers)
	if err := s.saveDocumentLocked(document); err != nil {
		return "", err
	}
	return providerID, nil
}

func (s *ProviderConfigStore) Delete(providerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	document, err := s.loadDocumentLocked()
	if err != nil {
		return err
	}
	providers := resolvedProviders(document)
	if _, exists := providers[providerID]; !exists {
		return fmt.Errorf("平台不存在：%s", providerID)
	}
	delete(providers, providerID)
	document.Providers = rawProviders(providers)
	return s.saveDocumentLocked(document)
}
