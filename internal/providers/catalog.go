// 模型目录：按平台协议拉取模型列表 + models.dev 元数据补全（带缓存）。
// 网络请求只在显式触发时发生，固定超时；API Key 只用于当次请求，
// 不写入日志、错误或缓存。
package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reins/internal/logger"
	"reins/internal/support"
)

const (
	// 聚合平台模型列表请求超时：KB 级响应，10s 覆盖跨州链路。
	providerModelsTimeout = 10 * time.Second
	// models.dev 目录请求超时：响应在 5MB 量级，实测完整下载 ~30s；
	// 之前的 10s 把 `response.json()` 截在 body 中段，导致所有失败
	// 被错误地报为「不是 JSON」。
	modelsdevTimeout = 45 * time.Second
	modelsdevURL     = "https://models.dev/api.json"
	cacheFileName    = "modelsdev-cache.json"
	cacheTTL         = 24 * time.Hour
)

// fetchProviderModelsInner：聚合平台模型列表：GET {base_url}/models。兼容
// 两种返回形态：{ "data": [ { "id": ... } ] }（OpenAI/Anthropic 官方形态）
// 与直接数组。
//
// 拉取路径按 base_url 的生态约定逐级回退：
//  1. {base}/v1/models —— Anthropic 官方约定（官方、智谱兼容层实测存在）；
//  2. {base}/models —— 部分兼容层不挂版本段；
//  3. 去掉 /anthropic 子路径后的 /models —— 兼容层普遍挂 /anthropic
//     子路径（DeepSeek 文档约定），其 OpenAI 形态模型列表在根路径，
//     该跳使用 Bearer 头。仅在 404 / 明确不是模型列表时继续下一候选，
//     其他失败（鉴权等）直接报，避免掩盖真实原因。
//
// 每次候选请求都记入 Attempts；全部候选失败时返回 Error 非空的结果而不是
// error：Wails 的 error 只能带一条字符串，前端失败弹窗要按顺序列举每一跳
// 实际请求了什么、拿到什么响应。
func fetchProviderModelsInner(provider *ResolvedProvider, apiKey string) *FetchedModelsResult {
	base := strings.TrimRight(provider.BaseURL, "/")
	type candidate struct {
		url    string
		bearer bool
	}
	var candidates []candidate
	if provider.Protocol.UsesAPIKeyHeader() {
		if strings.HasSuffix(base, "/v1") {
			candidates = []candidate{{base + "/models", false}}
		} else {
			candidates = []candidate{
				{base + "/v1/models", false},
				{base + "/models", false},
			}
		}
	} else {
		candidates = []candidate{{base + "/models", true}}
	}
	if root, found := strings.CutSuffix(base, "/anthropic"); found {
		candidates = append(candidates, candidate{root + "/models", true})
	}

	client := &http.Client{Timeout: providerModelsTimeout}
	attempts := make([]FetchModelsAttempt, 0, len(candidates))
	fail := func(message string) *FetchedModelsResult {
		return &FetchedModelsResult{Models: []FetchedModel{}, Attempts: attempts, Error: &message}
	}
	for index, item := range candidates {
		request, err := http.NewRequest(http.MethodGet, item.url, nil)
		if err != nil {
			attempts = append(attempts, FetchModelsAttempt{URL: item.url, Detail: "请求地址无法解析"})
			return fail(fmt.Sprintf("请求模型列表失败：%s", item.url))
		}
		if item.bearer {
			request.Header.Set("Authorization", "Bearer "+apiKey)
		} else {
			request.Header.Set("x-api-key", apiKey)
			request.Header.Set("anthropic-version", "2023-06-01")
		}

		response, err := client.Do(request)
		if err != nil {
			// 传输层错误详情可能带出请求信息（含 Key），只保留粗分类。
			attempts = append(attempts, FetchModelsAttempt{URL: item.url, Detail: describeTransportFailure(err)})
			return fail(fmt.Sprintf("请求模型列表失败：%s", item.url))
		}
		status := response.StatusCode
		statusText := http.StatusText(status)
		// 错误响应体可能回显请求内容（含 Key）：只取开头并脱敏后展示，
		// 日志仍只记状态码，不落响应体。
		if status < 200 || status >= 300 {
			detail := describeBodyHead(readBodyHead(response), apiKey)
			_ = response.Body.Close()
			hasNext := index+1 < len(candidates)
			logger.Info(fmt.Sprintf("fetch_models candidate failed url=%s status=%d try_next=%t", item.url, status, hasNext))
			attempts = append(attempts, FetchModelsAttempt{URL: item.url, Status: status, StatusText: statusText, Detail: detail})
			// 仅资源不存在时尝试下一个候选路径。
			if status == http.StatusNotFound && hasNext {
				continue
			}
			return fail(fmt.Sprintf("模型列表请求失败：HTTP %d %s", status, statusText))
		}
		// 先取原始字节再解析：2xx 但不是 JSON 时要把响应体开头带出来，
		// 否则只报「不是 JSON」看不出网关到底回了什么（HTML 错误页 / 纯文本
		// 404）。
		var raw []byte
		raw, err = readAll(response)
		_ = response.Body.Close()
		if err != nil {
			attempts = append(attempts, FetchModelsAttempt{
				URL: item.url, Status: status, StatusText: statusText, Detail: "读取响应失败",
			})
			return fail(fmt.Sprintf("读取模型列表响应失败：%s", item.url))
		}
		detail := describeBodyHead(raw, apiKey)
		body, decodeErr := support.DecodeJSONStrict(string(raw))
		if decodeErr != nil {
			attempts = append(attempts, FetchModelsAttempt{
				URL: item.url, Status: status, StatusText: statusText, Detail: detail,
			})
			return fail(fmt.Sprintf("模型列表返回的不是 JSON：%s：%s", item.url, detail))
		}
		models, payloadErr := parseModelsPayload(body)
		if payloadErr == nil {
			attempts = append(attempts, FetchModelsAttempt{
				URL: item.url, Status: status, StatusText: statusText,
				Detail: fmt.Sprintf("返回 %d 个模型", len(models)),
			})
			return &FetchedModelsResult{URL: item.url, Models: models, Attempts: attempts}
		}
		// 部分网关把 404 包在 HTTP 200 body 里（如智谱
		// {"code":500,"msg":"404 NOT_FOUND"}），同样视为候选路径
		// 不存在，继续尝试下一跳；其余解析错误直接返回。
		hasNext := index+1 < len(candidates)
		logger.Info(fmt.Sprintf("fetch_models candidate not a model list url=%s try_next=%t", item.url, hasNext))
		if payloadErr.missingData {
			// 上游错误文本可能回显请求头里的 Key，展示前统一脱敏。
			payloadErr.detail = maskSecret(payloadErr.detail, apiKey)
		}
		attemptDetail := payloadErr.detail
		if attemptDetail == "" {
			attemptDetail = "无法识别为模型列表"
		}
		attempts = append(attempts, FetchModelsAttempt{
			URL: item.url, Status: status, StatusText: statusText, Detail: attemptDetail,
		})
		if payloadErr.missingData && hasNext {
			continue
		}
		return fail(payloadErr.Error())
	}
	// 候选路径列表非空，循环内必然返回。
	return fail("拉取模型列表失败。")
}

// readBodyHead：非 2xx 响应体只取开头用于就地展示。响应体可能很大，也可能
// 回显请求头；读取结果只进展示摘要，绝不落日志。
func readBodyHead(response *http.Response) []byte {
	const limit = 4096
	raw, _ := io.ReadAll(io.LimitReader(response.Body, limit))
	return raw
}

// describeTransportFailure：传输层失败没有响应可展示，只给粗分类；原始错误
// 可能带出请求信息（含 Key），不进入展示串。
func describeTransportFailure(err error) string {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "未收到响应：请求超时"
	}
	return "未收到响应：连接失败"
}

func readAll(response *http.Response) ([]byte, error) {
	return io.ReadAll(response.Body)
}

// payloadError：模型列表 payload 的解析错误。missingData 专指「HTTP 200 但
// body 不是模型列表」（网关把业务错误包在 body 里），它是候选路径回退的
// 判断依据；回退与否不能依赖错误文案，改文案不会改变回退行为。
type payloadError struct {
	missingData bool
	detail      string
}

func (e *payloadError) Error() string {
	if e.missingData {
		return fmt.Sprintf("模型列表返回缺少 data 字段：%s", e.detail)
	}
	return "模型列表返回格式无法识别。"
}

func parseModelsPayload(body any) ([]FetchedModel, *payloadError) {
	var items []any
	switch typed := body.(type) {
	case []any:
		items = typed
	case map[string]any:
		data, has := typed["data"]
		if !has {
			// 聚合平台网关常用 HTTP 200 + 业务错误对象（如智谱
			// {"success":false,"code":...,"msg":...}），状态码检查拦不住；
			// 把上游错误字段带出来，避免「缺少 data」掩盖真实失败原因。
			return nil, &payloadError{missingData: true, detail: describeUpstreamError(typed)}
		}
		array, ok := data.([]any)
		if !ok {
			return nil, &payloadError{missingData: true, detail: describeUpstreamError(typed)}
		}
		items = array
	default:
		return nil, &payloadError{}
	}

	models := make([]FetchedModel, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, ok := asString(object["id"])
		if !ok || id == "" {
			continue
		}
		var name *string
		for _, key := range []string{"display_name", "name"} {
			if value, ok := asString(object[key]); ok {
				name = &value
				break
			}
		}
		models = append(models, FetchedModel{ID: id, Name: name})
	}
	sort.SliceStable(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	deduped := models[:0]
	for i, model := range models {
		if i == 0 || models[i-1].ID != model.ID {
			deduped = append(deduped, model)
		}
	}
	return deduped, nil
}

// describeUpstreamError：提取 200 型错误对象里的常见错误字段：code 为数字
// 或字符串，错误文本优先 msg/message，兼容 OpenAI 的嵌套 error.message。
// 只做展示，不做厂商语义判断。
func describeUpstreamError(object map[string]any) string {
	var parts []string
	// code 顶层（智谱形态）或嵌套在 error 对象里（OpenAI 形态）都取。
	codeValue, hasCode := object["code"]
	if !hasCode {
		if errorObject, ok := asObject(object["error"]); ok {
			codeValue, hasCode = errorObject["code"]
		}
	}
	if hasCode {
		switch typed := codeValue.(type) {
		case json.Number:
			parts = append(parts, "code="+typed.String())
		case string:
			if typed != "" {
				parts = append(parts, "code="+typed)
			}
		}
	}
	text := ""
	for _, key := range []string{"msg", "message"} {
		if value, ok := asString(object[key]); ok {
			text = value
			break
		}
	}
	if text == "" {
		switch typed := object["error"].(type) {
		case string:
			text = typed
		case map[string]any:
			if message, ok := asString(typed["message"]); ok {
				text = message
			}
		}
	}
	if strings.TrimSpace(text) != "" {
		// 上游错误文本长度不可控，截断避免错误提示刷屏。
		const maxChars = 200
		runes := []rune(text)
		if len(runes) > maxChars {
			parts = append(parts, string(runes[:maxChars])+"…")
		} else {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "无法从返回中识别错误原因。"
	}
	return strings.Join(parts, "，")
}

// describeBodyHead：响应体的展示形态：单行化并截断到 1000 字符，足够看清
// 网关返回了什么，又不会把整页内容塞进错误串。secret 是本次请求的 API Key：
// 上游可能把请求头回显进响应体，展示前必须替换成掩码。
func describeBodyHead(raw []byte, secret string) string {
	const maxChars = 1000
	text := maskSecret(string(raw), secret)
	flattened := strings.Join(strings.Fields(text), " ")
	if flattened == "" {
		return "（响应体为空）"
	}
	runes := []rune(flattened)
	if len(runes) > maxChars {
		return string(runes[:maxChars]) + "…"
	}
	return flattened
}

// maskSecret：展示摘要里把明文 Key 替换成掩码；空 secret 不替换，避免
// strings.ReplaceAll 的空分隔符插入语义。
func maskSecret(text string, secret string) string {
	if secret == "" || !strings.Contains(text, secret) {
		return text
	}
	return strings.ReplaceAll(text, secret, "******")
}

// ---------------------------------------------------------------------------
// models.dev
// ---------------------------------------------------------------------------

// normalizeReasoningLevels：models.dev 的 reasoning 取值归一化；未知取值
// 丢弃而不是猜测。
func normalizeReasoningLevels(values []string) []ReasoningLevel {
	levels := make([]ReasoningLevel, 0, len(values))
	for _, value := range values {
		if level, ok := ParseReasoningLevel(value); ok {
			levels = append(levels, level)
		}
	}
	return sortDedupReasoningLevels(levels)
}

type modelsDevCache struct {
	FetchedAtMs int64 `json:"fetched_at_ms"`
	Data        any   `json:"data"`
}

func cachePath(appDir string) string {
	return filepath.Join(appDir, cacheFileName)
}

func loadModelsdevData(appDir string, forceRefresh bool) (any, error) {
	path := cachePath(appDir)
	if !forceRefresh {
		if content, err := os.ReadFile(path); err == nil {
			var cache modelsDevCache
			// UseNumber 保住数值字面量（后续 asInt64 不收 float64）；
			// 尾随内容按 serde_json::from_str 同款视为损坏缓存。
			decoder := json.NewDecoder(strings.NewReader(string(content)))
			decoder.UseNumber()
			if err := decoder.Decode(&cache); err == nil {
				if _, err := decoder.Token(); errors.Is(err, io.EOF) &&
					time.Since(time.UnixMilli(cache.FetchedAtMs)) < cacheTTL {
					return cache.Data, nil
				}
			}
		}
	}

	client := &http.Client{Timeout: modelsdevTimeout}
	data, err := fetchModelsdev(client, modelsdevURL)
	if err != nil {
		return nil, err
	}

	cache := modelsDevCache{FetchedAtMs: time.Now().UnixMilli(), Data: data}
	serialized, err := json.Marshal(cache)
	if err != nil {
		return nil, err
	}
	// 缓存写失败只影响下次复用，不阻断本次补全；WriteAtomic 自带建目录。
	_ = support.WriteAtomic(path, string(serialized))
	return data, nil
}

// fetchModelsdev：网络层单独抽出来，给回归测试用 httptest 注入可控响应。
// 错误文案按网络错误分支：
// - 超时给「请求超时（>45s）」，能区分之前的「不是 JSON」误报；
// - 拨号失败给「无法连接到上游」；
// - 真非 JSON 时把响应体开头带出来，便于判断上游返回了什么。
func fetchModelsdev(client *http.Client, url string) (any, error) {
	prefix := "拉取 models.dev 目录失败"
	response, err := client.Get(url)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return nil, fmt.Errorf("%s：请求超时（>%ds），可稍后重试或点「从 models.dev 补全」刷新。",
				prefix, int(modelsdevTimeout.Seconds()))
		}
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return nil, fmt.Errorf("%s：无法连接到上游（%s）", prefix, err)
		}
		return nil, fmt.Errorf("%s：%s", prefix, err)
	}
	status := response.StatusCode
	if status < 200 || status >= 300 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("models.dev 请求失败：HTTP %d %s", status, http.StatusText(status))
	}
	raw, readErr := readAll(response)
	_ = response.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("读取 models.dev 响应失败：%s", readErr)
	}
	value, err := support.DecodeJSONStrict(string(raw))
	if err != nil {
		return nil, fmt.Errorf("models.dev 返回的不是 JSON：%s", describeBodyHead(raw, ""))
	}
	return value, nil
}

func matchModelsdev(appDir string, providerID string, modelID string, forceRefresh bool) (*ModelsDevMatchResult, error) {
	data, err := loadModelsdevData(appDir, forceRefresh)
	if err != nil {
		return nil, err
	}
	catalog := parseModelsdevCatalog(data)
	result := matchModelsdevInCatalog(catalog, providerID, modelID)
	return &result, nil
}

// modelsDevEntry：单个模型的 label 与元数据。
type modelsDevEntry struct {
	label *string
	meta  ModelsDevMeta
}

// modelsDevCatalog：解析后的目录：provider 名（小写）→ 模型 id → entry。
// 迭代一律经排序键（等价 Rust BTreeMap 字典序）。
type modelsDevCatalog map[string]map[string]modelsDevEntry

func parseModelsdevCatalog(data any) modelsDevCatalog {
	catalog := modelsDevCatalog{}
	providers, ok := data.(map[string]any)
	if !ok {
		return catalog
	}
	for providerName, providerAny := range providers {
		providerValue, ok := providerAny.(map[string]any)
		if !ok {
			continue
		}
		models, ok := providerValue["models"].(map[string]any)
		if !ok {
			continue
		}
		key := strings.ToLower(providerName)
		entry := catalog[key]
		if entry == nil {
			entry = map[string]modelsDevEntry{}
			catalog[key] = entry
		}
		for modelID, modelAny := range models {
			modelValue, ok := modelAny.(map[string]any)
			if !ok {
				continue
			}
			meta := parseModelsdevMeta(modelValue)
			var label *string
			if name, ok := asString(modelValue["name"]); ok {
				label = &name
			}
			entry[modelID] = modelsDevEntry{label: label, meta: meta}
		}
	}
	return catalog
}

func parseModelsdevMeta(modelValue map[string]any) ModelsDevMeta {
	var contextWindow, maxOutputTokens *int64
	if limit, ok := asObject(modelValue["limit"]); ok {
		if value, ok := asInt64(limit["context"]); ok {
			contextWindow = &value
		}
		if value, ok := asInt64(limit["output"]); ok {
			maxOutputTokens = &value
		}
	}
	var supportsImages *bool
	if modalities, ok := asObject(modelValue["modalities"]); ok {
		if inputs, ok := asArray(modalities["input"]); ok {
			hasImage := false
			for _, input := range inputs {
				if text, ok := asString(input); ok && text == "image" {
					hasImage = true
					break
				}
			}
			supportsImages = &hasImage
		}
	}
	var reasoning *bool
	if value, ok := asBool(modelValue["reasoning"]); ok {
		reasoning = &value
	}
	// reasoning_options 形态 unverified：兼容 [{values: [...] }] 与
	// [string] 两种，识别不了的取值直接丢弃。
	var reasoningLevels []ReasoningLevel
	if options, ok := asArray(modelValue["reasoning_options"]); ok {
		var values []string
		for _, option := range options {
			switch typed := option.(type) {
			case string:
				values = append(values, typed)
			case map[string]any:
				if inner, ok := asArray(typed["values"]); ok {
					for _, value := range inner {
						if text, ok := asString(value); ok {
							values = append(values, text)
						}
					}
				}
			}
		}
		normalized := normalizeReasoningLevels(values)
		if len(normalized) > 0 {
			reasoningLevels = normalized
		}
	}

	return ModelsDevMeta{
		ContextWindow:   contextWindow,
		MaxOutputTokens: maxOutputTokens,
		SupportsImages:  supportsImages,
		Reasoning:       reasoning,
		ReasoningLevels: reasoningLevels,
	}
}

// matchModelsdevInCatalog：匹配规则：模型 id 去掉 provider/ 前缀后优先在
// 同名 provider 下精确匹配；否则全库按 id 搜索，多候选交由用户选择。
func matchModelsdevInCatalog(catalog modelsDevCatalog, providerID string, modelID string) ModelsDevMatchResult {
	bareID := modelID
	if _, rest, found := strings.Cut(modelID, "/"); found {
		bareID = rest
	}
	// 目录里可能以完整 id 或裸 id 作键，两种都尝试。
	lookupIDs := []string{modelID, bareID}

	providerKey := strings.ToLower(providerID)
	if providerModels, has := catalog[providerKey]; has {
		for _, lookupID := range lookupIDs {
			if entry, hit := providerModels[lookupID]; hit {
				return ModelsDevMatchResult{
					Status:     MatchExact,
					Meta:       &entry.meta,
					Candidates: []ModelsDevCandidate{modelsDevCandidate(providerKey, lookupID, entry)},
				}
			}
		}
	}

	var candidates []ModelsDevCandidate
	for _, providerName := range sortedMapKeys(catalog) {
		models := catalog[providerName]
		for _, lookupID := range lookupIDs {
			if entry, hit := models[lookupID]; hit {
				candidates = append(candidates, modelsDevCandidate(providerName, lookupID, entry))
				break
			}
		}
	}
	if len(candidates) == 1 {
		return ModelsDevMatchResult{
			Status:     MatchExact,
			Meta:       &candidates[0].Meta,
			Candidates: candidates,
		}
	}
	if len(candidates) == 0 {
		return ModelsDevMatchResult{Status: MatchNotFound, Candidates: []ModelsDevCandidate{}}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Provider != candidates[j].Provider {
			return candidates[i].Provider < candidates[j].Provider
		}
		return candidates[i].ModelID < candidates[j].ModelID
	})
	return ModelsDevMatchResult{Status: MatchCandidates, Candidates: candidates}
}

func modelsDevCandidate(provider string, modelID string, entry modelsDevEntry) ModelsDevCandidate {
	return ModelsDevCandidate{
		Provider: provider,
		ModelID:  modelID,
		Label:    entry.label,
		Meta:     entry.meta,
	}
}
