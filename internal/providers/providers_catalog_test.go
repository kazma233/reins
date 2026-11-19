// 模型目录解析与 models.dev 匹配的纯逻辑测试（不联网）+ httptest 注入的
// 网络层测试（对齐 tests/providers_catalog.rs）。
package providers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// parseJSONValue：测试用 JSON 解析入口（等价 serde_json::json! 构造的树）。
func parseJSONValue(t *testing.T, text string) any {
	t.Helper()
	value, err := decodeJSONNumber(text)
	if err != nil {
		t.Fatalf("解析 JSON: %v\n%s", err, text)
	}
	return value
}

func TestParsesOpenAIAndArrayPayloads(t *testing.T) {
	openai := parseJSONValue(t, `{"data": [
		{"id": "b/model", "display_name": "B Model"},
		{"id": "a/model"},
		{"id": "a/model"},
		{"no-id": true}
	]}`)
	models, err := parseModelsPayload(openai)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models: %d", len(models))
	}
	if models[0].ID != "a/model" {
		t.Fatalf("排序后首条应为 a/model：%s", models[0].ID)
	}
	if models[1].Name == nil || *models[1].Name != "B Model" {
		t.Fatalf("name: %v", models[1].Name)
	}

	array := parseJSONValue(t, `[{"id": "x"}]`)
	models, err = parseModelsPayload(array)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("array models: %d", len(models))
	}
}

func TestMissingDataIncludesUpstreamErrorDetail(t *testing.T) {
	// 智谱形态：HTTP 200 + {"code":...,"msg":...,"success":false}
	zhipu := parseJSONValue(t, `{"code": 401, "msg": "令牌已过期或验证不正确", "success": false}`)
	_, err := parseModelsPayload(zhipu)
	if err == nil {
		t.Fatal("应报缺少 data 字段")
	}
	message := err.Error()
	if !strings.Contains(message, "缺少 data 字段") ||
		!strings.Contains(message, "code=401") ||
		!strings.Contains(message, "令牌已过期或验证不正确") {
		t.Fatalf("错误详情不符：%s", message)
	}

	// OpenAI 形态：嵌套 error.message
	openai := parseJSONValue(t, `{"error": {"message": "Incorrect API key provided", "code": "invalid_api_key"}}`)
	_, err = parseModelsPayload(openai)
	if err == nil {
		t.Fatal("应报缺少 data 字段")
	}
	message = err.Error()
	if !strings.Contains(message, "缺少 data 字段") ||
		!strings.Contains(message, "code=invalid_api_key") ||
		!strings.Contains(message, "Incorrect API key provided") {
		t.Fatalf("错误详情不符：%s", message)
	}

	// 无可识别错误字段时给出兜底提示，不 panic
	unknown := parseJSONValue(t, `{"success": false}`)
	_, err = parseModelsPayload(unknown)
	if err == nil {
		t.Fatal("应报缺少 data 字段")
	}
	message = err.Error()
	if !strings.Contains(message, "缺少 data 字段") ||
		!strings.Contains(message, "无法从返回中识别错误原因") {
		t.Fatalf("错误详情不符：%s", message)
	}
}

func fixtureCatalog(t *testing.T) modelsDevCatalog {
	data := parseJSONValue(t, `{
		"openrouter": {
			"models": {
				"anthropic/claude-sonnet-4-5": {
					"name": "Claude Sonnet 4.5",
					"limit": {"context": 200000, "output": 64000},
					"modalities": {"input": ["text", "image"]},
					"reasoning": true
				},
				"gpt-5": {"reasoning": true}
			}
		},
		"moonshot": {
			"models": {
				"claude-sonnet-4-5": {"limit": {"context": 80000}}
			}
		},
		"zhipu": {
			"models": {
				"claude-sonnet-4-5": {
					"limit": {"context": 100000},
					"reasoning_options": [{"values": ["low", "medium", "high"]}]
				}
			}
		}
	}`)
	return parseModelsdevCatalog(data)
}

func TestMatchingPrefersSameProviderThenFallsBackToSearch(t *testing.T) {
	catalog := fixtureCatalog(t)

	// 同名 provider 下以完整 id 命中。
	exact := matchModelsdevInCatalog(catalog, "openrouter", "anthropic/claude-sonnet-4-5")
	if exact.Status != MatchExact {
		t.Fatalf("status: %s", exact.Status)
	}
	if exact.Meta == nil {
		t.Fatal("exact match has meta")
	}
	meta := exact.Meta
	if meta.ContextWindow == nil || *meta.ContextWindow != 200_000 ||
		meta.MaxOutputTokens == nil || *meta.MaxOutputTokens != 64_000 ||
		meta.SupportsImages == nil || !*meta.SupportsImages ||
		meta.Reasoning == nil || !*meta.Reasoning {
		t.Fatalf("meta 不符：%+v", meta)
	}

	// 同名 provider 未命中 → 全库搜索出多候选（按 provider 名排序）。
	multi := matchModelsdevInCatalog(catalog, "unknown", "claude-sonnet-4-5")
	if multi.Status != MatchCandidates {
		t.Fatalf("status: %s", multi.Status)
	}
	if len(multi.Candidates) != 2 {
		t.Fatalf("candidates: %d", len(multi.Candidates))
	}
	if multi.Candidates[0].Provider != "moonshot" || multi.Candidates[1].Provider != "zhipu" {
		t.Fatalf("候选顺序：%s, %s", multi.Candidates[0].Provider, multi.Candidates[1].Provider)
	}

	// 全库未命中。
	none := matchModelsdevInCatalog(catalog, "openrouter", "no-such-model")
	if none.Status != MatchNotFound {
		t.Fatalf("status: %s", none.Status)
	}
}

func TestReasoningOptionsValuesAreNormalized(t *testing.T) {
	catalog := fixtureCatalog(t)
	multi := matchModelsdevInCatalog(catalog, "zhipu", "claude-sonnet-4-5")
	// zhipu 下唯一命中 → 归为精确匹配。
	if multi.Status != MatchExact {
		t.Fatalf("status: %s", multi.Status)
	}
	if multi.Meta == nil {
		t.Fatal("meta")
	}
	levels := multi.Meta.ReasoningLevels
	if len(levels) != 3 || levels[0] != ReasoningLow || levels[1] != ReasoningMedium || levels[2] != ReasoningHigh {
		t.Fatalf("reasoning levels：%v", levels)
	}
}

// ---------------------------------------------------------------------------
// 直连拉取（httptest.Server 替代 Rust 的 raw socket fake server）
// ---------------------------------------------------------------------------

// 直连拉取（新增平台未落盘形态）：请求必须打向 {base_url}/models、
// OpenAI 协议用 Bearer 头，返回 OpenAI data 形态可正常解析。
func TestFetchDirectUsesBaseURLModelsAndBearerHeader(t *testing.T) {
	var gotPath, gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"deepseek-chat"},{"id":"deepseek-reasoner"}]}`)
	}))
	defer server.Close()

	result, err := fetchProviderModelsDirectInner(ProtocolOpenaiChatCompletions, server.URL+"/v1", "sk-probe")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("请求路径：%s", gotPath)
	}
	// header 名比较不区分大小写（net/http 已规范化），这里断言值。
	if gotAuthorization != "Bearer sk-probe" {
		t.Fatalf("Authorization：%s", gotAuthorization)
	}
	if result.URL != server.URL+"/v1/models" {
		t.Fatalf("最终 URL：%s", result.URL)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "deepseek-chat" || result.Models[1].ID != "deepseek-reasoner" {
		t.Fatalf("models：%v", result.Models)
	}
}

// Anthropic 协议的 base_url 按官方约定不含版本段（…/api/anthropic），
// models 端点在 {base}/v1/models；已带 /v1 的填法不重复拼接。
func TestFetchDirectAnthropicResolvesModelsPath(t *testing.T) {
	serveModelList := func(t *testing.T) (*httptest.Server, *string) {
		t.Helper()
		var gotPath string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"data":[{"id":"claude-x"}]}`)
		}))
		t.Cleanup(server.Close)
		return server, &gotPath
	}

	// base_url 不带 /v1：拼 {base}/v1/models。
	server, gotPath := serveModelList(t)
	if _, err := fetchProviderModelsDirectInner(ProtocolAnthropicMessages, server.URL+"/anthropic", "sk-probe"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if *gotPath != "/anthropic/v1/models" {
		t.Fatalf("请求路径：%s", *gotPath)
	}

	// base_url 已带 /v1：不重复拼。
	server, gotPath = serveModelList(t)
	if _, err := fetchProviderModelsDirectInner(ProtocolAnthropicMessages, server.URL+"/anthropic/v1", "sk-probe"); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if *gotPath != "/anthropic/v1/models" {
		t.Fatalf("已带 /v1 时不应重复拼接：%s", *gotPath)
	}
}

// /v1/models 返回 404 时回退 {base}/models 一次（DeepSeek 兼容层形态）。
func TestFetchDirectAnthropicFallsBackOn404(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if len(paths) == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"deepseek-chat"}]}`)
	}))
	defer server.Close()

	result, err := fetchProviderModelsDirectInner(ProtocolAnthropicMessages, server.URL+"/anthropic", "sk-probe")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/anthropic/v1/models" || paths[1] != "/anthropic/models" {
		t.Fatalf("请求序列：%v", paths)
	}
	if result.URL != server.URL+"/anthropic/models" {
		t.Fatalf("最终 URL：%s", result.URL)
	}
	if len(result.Models) != 1 || result.Models[0].ID != "deepseek-chat" {
		t.Fatalf("models：%v", result.Models)
	}
}

// 前两跳都 404 且 base 挂在 /anthropic 子路径时，回退到根路径 /models
// （OpenAI 形态，Bearer 头）——DeepSeek 兼容层的实际布局。
func TestFetchDirectAnthropicFallsBackToRootModels(t *testing.T) {
	var paths []string
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		if len(paths) <= 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[{"id":"deepseek-chat"}]}`)
	}))
	defer server.Close()

	result, err := fetchProviderModelsDirectInner(ProtocolAnthropicMessages, server.URL+"/anthropic", "sk-probe")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(paths) != 3 || paths[0] != "/anthropic/v1/models" || paths[1] != "/anthropic/models" || paths[2] != "/models" {
		t.Fatalf("请求序列：%v", paths)
	}
	// 第三跳切回 Bearer 头。
	if authorizations[2] != "Bearer sk-probe" {
		t.Fatalf("回退跳的鉴权头：%s", authorizations[2])
	}
	if result.URL != server.URL+"/models" {
		t.Fatalf("最终 URL：%s", result.URL)
	}
}

// 2xx 但不是 JSON：错误串要带出响应体开头，便于判断网关返回了什么。
func TestFetchDirectReportsNonJSONBodyHead(t *testing.T) {
	serveBody := func(t *testing.T, body string) *httptest.Server {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, body)
		}))
		t.Cleanup(server.Close)
		return server
	}

	// HTML 错误页：原样带出（单行化后）
	server := serveBody(t, "<html>\n  <body>404 page not found</body>\n</html>")
	_, err := fetchProviderModelsDirectInner(ProtocolAnthropicMessages, server.URL, "sk-probe")
	if err == nil {
		t.Fatal("应报错")
	}
	message := err.Error()
	if !strings.Contains(message, server.URL+"/v1/models") {
		t.Fatalf("错误应带出请求 URL：%s", message)
	}
	if !strings.Contains(message, "<html> <body>404 page not found</body> </html>") {
		t.Fatalf("错误应带出响应体开头：%s", message)
	}
	if strings.Contains(message, "\n") {
		t.Fatalf("错误串应压成单行：%s", message)
	}

	// 超长响应体：截断并标记省略（截断阈值 1000 字符）
	long := "<html>" + strings.Repeat("x", 1200) + "</html>"
	server = serveBody(t, long)
	_, err = fetchProviderModelsDirectInner(ProtocolAnthropicMessages, server.URL, "sk-probe")
	if err == nil {
		t.Fatal("应报错")
	}
	if !strings.HasSuffix(err.Error(), "…") {
		t.Fatalf("超长响应体应截断：%s", err.Error())
	}
}

// models.dev 拉取：2xx 但 body 是 HTML 错误页时，错误串要带出响应体开头，
// 并明确是「不是 JSON」而不是超时。
func TestFetchModelsdevReportsNonJSONBodyHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>\n  <body>502 Bad Gateway</body>\n</html>")
	}))
	defer server.Close()

	client := &http.Client{}
	_, err := fetchModelsdev(client, server.URL+"/api.json")
	if err == nil {
		t.Fatal("应报错")
	}
	message := err.Error()
	if !strings.Contains(message, "返回的不是 JSON") || !strings.Contains(message, "502 Bad Gateway") {
		t.Fatalf("错误不符：%s", message)
	}
	if strings.Contains(message, "\n") {
		t.Fatalf("错误串应压成单行：%s", message)
	}
}

// models.dev 拉取：客户端超时小于服务端响应延迟时，应报「请求超时」而不是
// 把网络层错误错挂成「不是 JSON」。
func TestFetchModelsdevReportsTimeoutWhenExceedsClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 保持长于 client 超时的时间，否则后续测试可能偶发命中 FIN。
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	_, err := fetchModelsdev(client, server.URL+"/api.json")
	if err == nil {
		t.Fatal("应报错")
	}
	message := err.Error()
	if !strings.Contains(message, "请求超时") {
		t.Fatalf("应报请求超时：%s", message)
	}
	if strings.Contains(message, "不是 JSON") {
		t.Fatalf("超时分支不应被包装为「不是 JSON」：%s", message)
	}
}

// models.dev 拉取：合法 JSON 直接吐出解析后的 Value。
func TestFetchModelsdevReturnsParsedValueForValidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"openrouter":{"models":{"claude-x":{"limit":{"context":200000}}}}}`)
	}))
	defer server.Close()

	client := &http.Client{}
	parsed, err := fetchModelsdev(client, server.URL+"/api.json")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got := jnum(t, parsed, "openrouter", "models", "claude-x", "limit", "context"); got != 200000 {
		t.Fatalf("context：%d", got)
	}
}
