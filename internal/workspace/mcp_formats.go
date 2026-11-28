// mcp_formats.go：MCP 配置「格式 writer」——每种 McpConfigType 一个 writer，
// 收敛该类型在目标工具配置里的条目形态、支持的文件格式与前缀约束；跨格式的
// 文件解析、前缀定位与写入编排在 mcps.go 的通用底座上完成。
//
// 格式判定只按扩展名（.json → JSON、.toml → TOML、其余报错）：这是 Go 迁移
// 的已拍板行为变更，取代 Rust detect_mcp_file_format 的两段式判定（先按内容
// 试解析、后缀兜底）——文件该是什么格式是文件所有者的静态契约，不属于
// Reins 该猜的范围，见 docs/decisions/2026-11-19-mcp-format-detection-by-extension.md。
package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
)

// mcpConfigFileFormat：MCP 配置文件的承载格式。
type mcpConfigFileFormat uint8

const (
	mcpFormatJSON mcpConfigFileFormat = iota
	mcpFormatTOML
	mcpFormatYAML
)

// shapeExample：编辑页展示的一段写入形态示例：形态名 + 已序列化的正文。
type shapeExample struct {
	label string
	body  string
}

const (
	stdioExampleLabel  = "本地命令（stdio）"
	remoteExampleLabel = "远端地址（http / sse）"
)

// ---------------------------------------------------------------------------
// writer 契约与分发
// ---------------------------------------------------------------------------

// mcpFormatWriter：一个 config type 的完整格式知识。JSON/TOML 的读/预览/
// 应用/移除由 mcps.go 的共享引擎完成；dsh patch 整体覆写这四个入口。
type mcpFormatWriter interface {
	// prefixRequired：mcp.config_prefix 是否必填（dsh 按 name+serverName 定位，
	// 无 prefix 概念）。
	prefixRequired() bool
	// description：编辑页展示的「大概会怎么配置」中文说明；格式知识归 writer，
	// 前端不另存一份。
	description() string
	// shapeExamples：编辑页展示的写入形态示例，按形态各一段（stdio、远端…）。
	// 每段与真实写入走同一构造路径：样例 server → writer 自己的 desired 条目
	// 构造 → 按 prefix 结构化嵌套 → 序列化，不手拼字符串。
	shapeExamples(prefix string, serverName string) ([]shapeExample, error)
	// defaultJSONRoot：新建空 JSON 配置的默认根（OpenCode 注入 $schema）。
	defaultJSONRoot() any
	// ensureFormatSupported：文件格式强校验（GrokBuild 只接受 TOML）；默认放行。
	ensureFormatSupported(format mcpConfigFileFormat) error
	// desiredTomlEntry / desiredJSONEntry / desiredYAMLEntry：构造 desired
	// 条目；不支持的类型按既定文案报错。
	desiredTomlEntry(server *ResolvedMcpConfig) (any, error)
	desiredJSONEntry(server *ResolvedMcpConfig) (any, error)
	desiredYAMLEntry(server *ResolvedMcpConfig) (*yaml.Node, error)
	// entryKeyFor：读取侧的条目 key：默认与 server 名一致；dsh 按清洗后的
	// serverName。
	entryKeyFor(name string) string
	// readExistingEntries / previewEntry / applyEntry / removeEntry：四个操作
	// 入口。文件形态 writer 全部委托 mcps.go 的引擎；dsh 有自己的 patch 读写。
	readExistingEntries(prefix string, configPath string) (map[string]any, error)
	previewEntry(configPath string, server *ResolvedMcpConfig) (string, string, error)
	applyEntry(path *ResolvedMcpPath, configPath string, server *ResolvedMcpConfig) error
	removeEntry(path *ResolvedMcpPath, configPath string, serverName string) (McpPathMutationResult, error)
}

// 全仓库唯一允许的 config_type 分发点（对齐 mcp_format_writer）。
func mcpFormatWriterFor(configType agents.McpConfigType) mcpFormatWriter {
	switch configType {
	case agents.McpConfigCommon:
		return writerCommon
	case agents.McpConfigGrokBuild:
		return writerGrokBuild
	case agents.McpConfigOpenCode:
		return writerOpenCode
	case agents.McpConfigDsh:
		return writerDshPatch
	case agents.McpConfigHermes:
		// 旧配置归一后的兜底（parse 已把 hermes 归一为 common，不应到达）。
		return writerCommon
	}
	// parse 阶段已校验枚举取值，不应到达。
	return writerCommon
}

var (
	writerCommon    = newFileWriter(&commonWriter{})
	writerGrokBuild = newFileWriter(&grokBuildWriter{})
	writerOpenCode  = newFileWriter(&openCodeWriter{})
	writerDshPatch  = &dshPatchWriter{}
)

// fileWriterBase：文件形态四个操作入口的默认实现（Rust trait 默认方法的 Go
// 对应物）——全部委托 mcps.go 的共享引擎，嵌入即得；YAML 条目构造默认拒绝
// （只有声明支持 YAML 载体的方言覆写）。引擎要回调外层 writer
// 本体（ensureFormatSupported、desired 条目构造等），嵌入基自身拿不到接收
// 者，self 由 newFileWriter 在构造时回填；文件形态 writer 一律经它构造，
// 不得直接以零值 struct 参与分发（self 为 nil 会空回调）。
type fileWriterBase struct {
	self mcpFormatWriter
}

// initSelf 回填外层 writer 本体（仅 newFileWriter 调用）。
func (b *fileWriterBase) initSelf(self mcpFormatWriter) {
	b.self = self
}

func (b *fileWriterBase) desiredYAMLEntry(server *ResolvedMcpConfig) (*yaml.Node, error) {
	return nil, errors.New("该 config type 不支持 YAML 载体。")
}

func (b *fileWriterBase) readExistingEntries(prefix string, configPath string) (map[string]any, error) {
	return readFileEntries(b.self, prefix, configPath)
}

func (b *fileWriterBase) previewEntry(configPath string, server *ResolvedMcpConfig) (string, string, error) {
	return previewFileEntry(b.self, configPath, server)
}

func (b *fileWriterBase) applyEntry(path *ResolvedMcpPath, configPath string, server *ResolvedMcpConfig) error {
	return applyFileEntry(b.self, path, configPath, server)
}

func (b *fileWriterBase) removeEntry(path *ResolvedMcpPath, configPath string, serverName string) (McpPathMutationResult, error) {
	return removeFileEntry(b.self, path, configPath, serverName)
}

// newFileWriter：构造文件形态 writer 并回填嵌入基的 self。新增文件形态
// writer：嵌入 fileWriterBase + 声明真差异（前缀约束、条目形态、格式约束），
// 四个操作入口不用再写。
func newFileWriter[W interface {
	mcpFormatWriter
	initSelf(mcpFormatWriter)
}](w W) W {
	w.initSelf(w)
	return w
}

// ---------------------------------------------------------------------------
// Common / GrokBuild / OpenCode writer
// ---------------------------------------------------------------------------

// commonWriter：通用 MCP 条目（标准 command/args/env），按配置文件扩展名写入
// JSON 或 TOML 的 mcp 节点下。
type commonWriter struct{ fileWriterBase }

func (w *commonWriter) prefixRequired() bool { return true }

func (w *commonWriter) description() string {
	return "通用 MCP 条目（标准 command/args/env），按配置文件扩展名写入 JSON、TOML 或 YAML 的 mcp 节点下；YAML 载体的条目无 type 判别字段、空集合省略。"
}

func (w *commonWriter) shapeExamples(prefix string, serverName string) ([]shapeExample, error) {
	// 与真实写入同一路径：样例 server 走 desired 构造，再结构化嵌套序列化。
	stdio, err := w.desiredJSONEntry(exampleServer(serverName))
	if err != nil {
		return nil, err
	}
	remote, err := w.desiredJSONEntry(exampleRemoteServer(serverName))
	if err != nil {
		return nil, err
	}
	stdioBody, err := marshalJSONPretty(nestJSONDocument(prefix, serverName, stdio))
	if err != nil {
		return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
	}
	remoteBody, err := marshalJSONPretty(nestJSONDocument(prefix, serverName, remote))
	if err != nil {
		return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
	}
	return []shapeExample{
		{label: stdioExampleLabel, body: stdioBody},
		{label: remoteExampleLabel, body: remoteBody},
	}, nil
}

func (w *commonWriter) defaultJSONRoot() any { return map[string]any{} }

func (w *commonWriter) ensureFormatSupported(mcpConfigFileFormat) error { return nil }

func (w *commonWriter) desiredTomlEntry(server *ResolvedMcpConfig) (any, error) {
	return desiredOpenAIToMLMCP(server)
}

func (w *commonWriter) desiredJSONEntry(server *ResolvedMcpConfig) (any, error) {
	return desiredOpenAIJSONMCP(server)
}

// desiredYAMLEntry：common 的 YAML 载体条目（hermes config.yaml 同方言）：
// 无 type 判别字段，空集合省略；headers 键与 JSON 形态一致。
func (w *commonWriter) desiredYAMLEntry(server *ResolvedMcpConfig) (*yaml.Node, error) {
	return desiredStandardYAMLEntry(server)
}

func (w *commonWriter) entryKeyFor(name string) string { return name }

// grokBuildWriter：Grok Build TOML 格式（远端类型用 headers 携带自定义头），
// 写入 config.toml。
type grokBuildWriter struct{ fileWriterBase }

func (w *grokBuildWriter) prefixRequired() bool { return true }

func (w *grokBuildWriter) description() string {
	return "Grok Build TOML 格式（远端类型用 headers 携带自定义头），写入 config.toml。"
}

func (w *grokBuildWriter) shapeExamples(prefix string, serverName string) ([]shapeExample, error) {
	stdio, err := w.desiredTomlEntry(exampleServer(serverName))
	if err != nil {
		return nil, err
	}
	remote, err := w.desiredTomlEntry(exampleRemoteServer(serverName))
	if err != nil {
		return nil, err
	}
	stdioBody, err := marshalTOML(nestTOMLDocument(prefix, serverName, stdio))
	if err != nil {
		return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
	}
	remoteBody, err := marshalTOML(nestTOMLDocument(prefix, serverName, remote))
	if err != nil {
		return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
	}
	return []shapeExample{
		{label: stdioExampleLabel, body: stdioBody},
		{label: remoteExampleLabel, body: remoteBody},
	}, nil
}

func (w *grokBuildWriter) defaultJSONRoot() any { return map[string]any{} }

func (w *grokBuildWriter) ensureFormatSupported(format mcpConfigFileFormat) error {
	if format != mcpFormatTOML {
		return errors.New("Grok Build config type 仅支持 TOML。")
	}
	return nil
}

func (w *grokBuildWriter) desiredTomlEntry(server *ResolvedMcpConfig) (any, error) {
	return desiredGrokToMLMCP(server)
}

func (w *grokBuildWriter) desiredJSONEntry(*ResolvedMcpConfig) (any, error) {
	return nil, errors.New("Grok Build config type 仅支持 TOML。")
}

func (w *grokBuildWriter) entryKeyFor(name string) string { return name }

// openCodeWriter：OpenCode 专属格式（JSON，带 $schema 头，command 为数组、
// environment 键），写入 mcp.servers 根。
type openCodeWriter struct{ fileWriterBase }

func (w *openCodeWriter) prefixRequired() bool { return true }

func (w *openCodeWriter) description() string {
	return "OpenCode 专属格式（JSON，带 $schema 头，command 为数组、environment 键），写入 mcp.servers 根。"
}

func (w *openCodeWriter) shapeExamples(prefix string, serverName string) ([]shapeExample, error) {
	local, err := w.desiredJSONEntry(exampleServer(serverName))
	if err != nil {
		return nil, err
	}
	remote, err := w.desiredJSONEntry(exampleRemoteServer(serverName))
	if err != nil {
		return nil, err
	}
	localBody, err := marshalJSONPretty(nestJSONDocument(prefix, serverName, local))
	if err != nil {
		return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
	}
	remoteBody, err := marshalJSONPretty(nestJSONDocument(prefix, serverName, remote))
	if err != nil {
		return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
	}
	return []shapeExample{
		{label: stdioExampleLabel, body: localBody},
		{label: remoteExampleLabel, body: remoteBody},
	}, nil
}

func (w *openCodeWriter) defaultJSONRoot() any {
	return map[string]any{"$schema": "https://opencode.ai/config.json"}
}

func (w *openCodeWriter) ensureFormatSupported(mcpConfigFileFormat) error { return nil }

func (w *openCodeWriter) desiredTomlEntry(*ResolvedMcpConfig) (any, error) {
	return nil, errors.New("OpenCode config type 不支持 TOML。")
}

func (w *openCodeWriter) desiredJSONEntry(server *ResolvedMcpConfig) (any, error) {
	return desiredOpencodeMCP(server)
}

func (w *openCodeWriter) entryKeyFor(name string) string { return name }

// ---------------------------------------------------------------------------
// 条目构造：Common / GrokBuild 的 TOML 形态，Common / OpenCode 的 JSON 形态
// ---------------------------------------------------------------------------

func desiredOpenAIToMLMCP(server *ResolvedMcpConfig) (any, error) {
	var timeout any
	if server.Timeout != nil {
		// Common 的 TOML 形态把毫秒 timeout 写成秒（浮点）。
		timeout = float64(*server.Timeout) / 1000.0
	}
	return desiredToMLMCP(server, "http_headers", timeout)
}

func desiredGrokToMLMCP(server *ResolvedMcpConfig) (any, error) {
	var timeout any
	if server.Timeout != nil {
		// Grok 1.0.30 要求 u64 秒，不能静默丢弃毫秒精度。
		if *server.Timeout%1000 != 0 {
			return nil, errors.New("Grok Build MCP timeout 必须是整秒（毫秒值须为 1000 的倍数）。")
		}
		timeout = int64(*server.Timeout / 1000)
	}
	return desiredToMLMCP(server, "headers", timeout)
}

// desiredToMLMCP：TOML 条目构造。键在 map 里、序列化时按字典序输出（与
// Rust toml::Value 的 BTreeMap 同口径）。
func desiredToMLMCP(server *ResolvedMcpConfig, headersKey string, timeout any) (any, error) {
	table := map[string]any{}
	if timeout != nil {
		table["tool_timeout_sec"] = timeout
	}

	switch server.Transport {
	case TransportStdio:
		if server.Command == nil || *server.Command == "" {
			return nil, errors.New("stdio MCP 缺少 command")
		}
		table["command"] = *server.Command
		if len(server.Args) > 0 {
			table["args"] = cloneStrings(server.Args)
		}
		if len(server.Env) > 0 {
			table["env"] = cloneStringMap(server.Env)
		}
	case TransportHTTP, TransportSSE:
		if server.URL == nil || *server.URL == "" {
			return nil, errors.New("远程 MCP 缺少 mcp 链接")
		}
		table["url"] = *server.URL
		if len(server.Headers) > 0 {
			table[headersKey] = cloneStringMap(server.Headers)
		}
	}
	return table, nil
}

// desiredOpenAIJSONMCP：通用 JSON 条目（Codex / Claude / ZCode / Pi 共用）。
func desiredOpenAIJSONMCP(server *ResolvedMcpConfig) (any, error) {
	object := map[string]any{}
	switch server.Transport {
	case TransportStdio:
		object["type"] = "stdio"
		if server.Command == nil || *server.Command == "" {
			return nil, errors.New("stdio MCP 缺少 command")
		}
		object["command"] = *server.Command
		object["args"] = cloneStrings(server.Args)
		object["env"] = cloneStringMap(server.Env)
	case TransportHTTP, TransportSSE:
		if server.Transport == TransportHTTP {
			object["type"] = "http"
		} else {
			object["type"] = "sse"
		}
		if server.URL == nil || *server.URL == "" {
			return nil, errors.New("远程 MCP 缺少 mcp 链接")
		}
		object["url"] = *server.URL
		if len(server.Headers) > 0 {
			object["headers"] = cloneStringMap(server.Headers)
		}
	}
	return object, nil
}

// DesiredOpencodeMCP：OpenCode 条目（local/remote，command 数组与 environment
// 键）。导出供测试（对齐 Rust pub(super) desired_opencode_mcp 的测试通道）。
func DesiredOpencodeMCP(server *ResolvedMcpConfig) (any, error) {
	return desiredOpencodeMCP(server)
}

func desiredOpencodeMCP(server *ResolvedMcpConfig) (any, error) {
	object := map[string]any{}
	if server.Timeout != nil {
		object["timeout"] = *server.Timeout
	}
	switch server.Transport {
	case TransportStdio:
		object["type"] = "local"
		if server.Command == nil || *server.Command == "" {
			return nil, errors.New("stdio MCP 缺少 command")
		}
		commandParts := []string{*server.Command}
		commandParts = append(commandParts, server.Args...)
		object["command"] = commandParts
		if len(server.Env) > 0 {
			object["environment"] = cloneStringMap(server.Env)
		}
	case TransportHTTP, TransportSSE:
		object["type"] = "remote"
		if server.URL == nil || *server.URL == "" {
			return nil, errors.New("远程 MCP 缺少 mcp 链接")
		}
		object["url"] = *server.URL
		if len(server.Headers) > 0 {
			object["headers"] = cloneStringMap(server.Headers)
		}
	}
	return object, nil
}

// desiredStandardYAMLEntry：标准方言的 YAML 条目形态（hermes config.yaml
// 契约）：stdio 是 command/args/env、远程是 url/headers，无 type 判别字段；
// 空集合省略（headers 为空省略是官方示例口径）。键序即 YAML 落盘键序。
func desiredStandardYAMLEntry(server *ResolvedMcpConfig) (*yaml.Node, error) {
	switch server.Transport {
	case TransportStdio:
		if server.Command == nil || *server.Command == "" {
			return nil, errors.New("stdio MCP 缺少 command")
		}
		pairs := [][2]*yaml.Node{{support.YStr("command"), support.YStr(*server.Command)}}
		if len(server.Args) > 0 {
			pairs = append(pairs, [2]*yaml.Node{support.YStr("args"), stringsNodeSeq(server.Args)})
		}
		if len(server.Env) > 0 {
			pairs = append(pairs, [2]*yaml.Node{support.YStr("env"), stringMapNode(server.Env)})
		}
		return support.YMap(pairs...), nil
	case TransportHTTP, TransportSSE:
		if server.URL == nil || *server.URL == "" {
			return nil, errors.New("远程 MCP 缺少 mcp 链接")
		}
		pairs := [][2]*yaml.Node{{support.YStr("url"), support.YStr(*server.URL)}}
		if len(server.Headers) > 0 {
			pairs = append(pairs, [2]*yaml.Node{support.YStr("headers"), stringMapNode(server.Headers)})
		}
		return support.YMap(pairs...), nil
	default:
		return nil, fmt.Errorf("未知 transport：%s", server.Transport)
	}
}

// 示例用的样例 server：与真实写入走同一 desired 构造路径，只是喂固定值。
func exampleServer(name string) *ResolvedMcpConfig {
	return &ResolvedMcpConfig{
		Name:      name,
		Transport: TransportStdio,
		Command:   strPtr("node"),
		Args:      []string{"server.js"},
		Env:       map[string]string{"KEY": "value"},
	}
}

func exampleRemoteServer(name string) *ResolvedMcpConfig {
	return &ResolvedMcpConfig{
		Name:      name,
		Transport: TransportHTTP,
		URL:       strPtr("https://example.com/mcp"),
		Headers:   map[string]string{"X-Key": "value"},
	}
}

// nestJSONDocument：把条目值按点分 prefix 逐段嵌套、再挂到 server 名字键下
// ——结构化构造（"mcp.servers" → {"mcp": {"servers": {"<name>": <entry>}}}），
// 不拼字符串。与 Rust 一致：空段不过滤（引擎侧的 split 才过滤）。
func nestJSONDocument(prefix string, serverName string, entry any) any {
	document := map[string]any{serverName: entry}
	segments := strings.Split(prefix, ".")
	for i := len(segments) - 1; i >= 0; i-- {
		document = map[string]any{segments[i]: document}
	}
	return document
}

// nestTOMLDocument：TOML 形态。条目值按 prefix 段与 server 名逐层挂表，交给
// toml 序列化出 [prefix.name] 表头，不手拼表名；空段跳过（与 Rust 一致）。
func nestTOMLDocument(prefix string, serverName string, entry any) any {
	document := map[string]any{serverName: entry}
	segments := strings.Split(prefix, ".")
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] == "" {
			continue
		}
		document = map[string]any{segments[i]: document}
	}
	return document
}

// ---------------------------------------------------------------------------
// dsh (DeepSeek Harness)：Cordis patch YAML (~/.dsh/cordis.patch.yml)
// ---------------------------------------------------------------------------

// dsh 的 MCP server 是 Cordis 插件树里的 dsh-mcp-client 条目。Reins 只认
// name 为该插件且 config.serverName 匹配的 insert 条目；定位与其它 MCP 路径
// 按 server key 定位的语义一致，条目 id 不参与定位。patch 文件其余内容
// （无关插件、未知操作）原样保留，绝不整文件覆盖。
const (
	dshMcpClientPlugin          = "@deepseek-ai/dsh-mcp-client"
	dshDefaultToolCallTimeoutMs = uint64(60_000)
	dshServerNameMaxLength      = 32
)

// dshPatchWriter：DeepSeek Harness 的 Cordis patch YAML（insert/remove 操作
// 列表，按 serverName 定位，无 configPrefix）。
type dshPatchWriter struct{}

func (w *dshPatchWriter) prefixRequired() bool { return false }

func (w *dshPatchWriter) description() string {
	return "DeepSeek Harness 的 Cordis patch YAML（insert/remove 操作列表，按 serverName 定位，无 configPrefix）。"
}

func (w *dshPatchWriter) shapeExamples(_ string, serverName string) ([]shapeExample, error) {
	// patch YAML 没有 prefix 概念；示例即真实写入的 insert 操作序列，stdio
	// 与远端各一段。
	servers := []struct {
		label  string
		server *ResolvedMcpConfig
	}{
		{stdioExampleLabel, exampleServer(serverName)},
		{remoteExampleLabel, exampleRemoteServer(serverName)},
	}
	examples := make([]shapeExample, 0, len(servers))
	for _, item := range servers {
		op, err := desiredDshInsertOp(serverName, item.server)
		if err != nil {
			return nil, err
		}
		body, err := support.SerializeOps([]*yaml.Node{op})
		if err != nil {
			return nil, fmt.Errorf("MCP 示例序列化失败：%v", err)
		}
		examples = append(examples, shapeExample{label: item.label, body: body})
	}
	return examples, nil
}

// dsh 的配置根是操作列表；dsh 写入路径不走 JSON 根，该方法仅为穷尽接口。
func (w *dshPatchWriter) defaultJSONRoot() any { return []any{} }

func (w *dshPatchWriter) ensureFormatSupported(mcpConfigFileFormat) error { return nil }

func (w *dshPatchWriter) desiredTomlEntry(*ResolvedMcpConfig) (any, error) {
	return nil, errors.New("Dsh config type 仅支持 Cordis patch YAML。")
}

func (w *dshPatchWriter) desiredJSONEntry(*ResolvedMcpConfig) (any, error) {
	return nil, errors.New("Dsh config type 仅支持 Cordis patch YAML。")
}

func (w *dshPatchWriter) desiredYAMLEntry(*ResolvedMcpConfig) (*yaml.Node, error) {
	return nil, errors.New("Dsh config type 仅支持 Cordis patch YAML。")
}

func (w *dshPatchWriter) entryKeyFor(name string) string {
	return dshServerName(name)
}

func (w *dshPatchWriter) readExistingEntries(_ string, configPath string) (map[string]any, error) {
	return readExistingDshPatchEntries(configPath)
}

func (w *dshPatchWriter) previewEntry(configPath string, server *ResolvedMcpConfig) (string, string, error) {
	// 与 JSON/TOML 路径一致：preview 先确认文件可解析，再展示条目。
	if _, err := support.ReadOps(configPath); err != nil {
		return "", "", err
	}
	op, err := desiredDshInsertOp(server.Name, server)
	if err != nil {
		return "", "", err
	}
	content, err := support.SerializeOps([]*yaml.Node{op})
	if err != nil {
		return "", "", fmt.Errorf("MCP 预览序列化失败：%v", err)
	}
	return "yaml", content, nil
}

func (w *dshPatchWriter) applyEntry(_ *ResolvedMcpPath, configPath string, server *ResolvedMcpConfig) error {
	return applyDshPatchEntry(configPath, server)
}

func (w *dshPatchWriter) removeEntry(path *ResolvedMcpPath, configPath string, serverName string) (McpPathMutationResult, error) {
	return removeDshPatchEntry(path, configPath, serverName)
}

// dshServerName：dsh 要求 serverName 匹配 [A-Za-z0-9_-]{1,32}：非法字符折叠
// 为 -，超长截断。读取侧（inspect 状态归类）与写入侧（DshPatchWriter）共用
// 这一清洗规则。
func dshServerName(serverName string) string {
	trimmed := strings.TrimSpace(serverName)
	var sanitized strings.Builder
	for _, ch := range trimmed {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			sanitized.WriteRune(ch)
		} else {
			sanitized.WriteByte('-')
		}
	}
	// 映射后只有 ASCII，按字节截断与 Rust String::truncate(32) 等价。
	result := sanitized.String()
	if len(result) > dshServerNameMaxLength {
		result = result[:dshServerNameMaxLength]
	}
	return result
}

// desiredDshInsertEntry：dsh-mcp-client 的 config 是 stdio / streamable-http
// 两种形态的判别联合（见 dsh-mcp-client 的类型契约）：stdio 走子进程、必填
// command；远端只有 url+headers，没有 command/args/env/cwd。键序即 YAML 落盘
// 键序，与 Rust serde 结构体声明序一致；判别由各形态自己的 transport 字段
// 携带。
func desiredDshInsertEntry(serverName string, server *ResolvedMcpConfig) (*yaml.Node, error) {
	var config *yaml.Node
	switch server.Transport {
	case TransportStdio:
		if server.Command == nil || *server.Command == "" {
			return nil, errors.New("stdio MCP 缺少 command")
		}
		home, ok := support.UserHomeDir()
		if !ok {
			return nil, errors.New("无法解析 HOME 目录。")
		}
		timeout := dshDefaultToolCallTimeoutMs
		if server.Timeout != nil {
			timeout = *server.Timeout
		}
		config = support.YMap(
			[2]*yaml.Node{support.YStr("serverName"), support.YStr(serverName)},
			[2]*yaml.Node{support.YStr("transport"), support.YStr("stdio")},
			[2]*yaml.Node{support.YStr("command"), support.YStr(*server.Command)},
			[2]*yaml.Node{support.YStr("args"), stringsNodeSeq(server.Args)},
			[2]*yaml.Node{support.YStr("env"), stringMapNode(server.Env)},
			[2]*yaml.Node{support.YStr("cwd"), support.YStr(home)},
			[2]*yaml.Node{support.YStr("toolCallTimeoutMs"), support.YInt(int64(timeout))},
			[2]*yaml.Node{support.YStr("failOnStartupError"), support.YBool(false)},
		)
	case TransportHTTP, TransportSSE:
		// dsh-mcp-client 的 streamable-http 形态本身覆盖 SSE 系协议，故 http/sse
		// 两种 transport 都写这个形态；headers 为空时省略（Input 形态可选）。
		if server.URL == nil || *server.URL == "" {
			return nil, errors.New("远程 MCP 缺少 mcp 链接")
		}
		pairs := [][2]*yaml.Node{
			{support.YStr("transport"), support.YStr("streamable-http")},
			{support.YStr("serverName"), support.YStr(serverName)},
			{support.YStr("url"), support.YStr(*server.URL)},
		}
		if len(server.Headers) > 0 {
			pairs = append(pairs, [2]*yaml.Node{support.YStr("headers"), stringMapNode(server.Headers)})
		}
		config = support.YMap(pairs...)
	default:
		return nil, fmt.Errorf("未知 transport：%s", server.Transport)
	}

	entry := support.YMap(
		[2]*yaml.Node{support.YStr("id"), support.YStr(fmt.Sprintf("reins-mcp-%s", serverName))},
		[2]*yaml.Node{support.YStr("name"), dshPluginNameNode()},
		[2]*yaml.Node{support.YStr("config"), config},
	)
	return entry, nil
}

// dshPluginNameNode：插件名以 @ 开头，YAML 里必须带引号；Rust serde_yaml 输出
// 单引号形态，这里显式指定同款风格保持落盘文本一致。
func dshPluginNameNode() *yaml.Node {
	node := support.YStr(dshMcpClientPlugin)
	node.Style = yaml.SingleQuotedStyle
	return node
}

// stringMapNode：字符串映射按键字典序展开（Rust BTreeMap 序列化序）。
func stringMapNode(values map[string]string) *yaml.Node {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([][2]*yaml.Node, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, [2]*yaml.Node{support.YStr(key), support.YStr(values[key])})
	}
	return support.YMap(pairs...)
}

func desiredDshInsertOp(serverName string, server *ResolvedMcpConfig) (*yaml.Node, error) {
	entry, err := desiredDshInsertEntry(serverName, server)
	if err != nil {
		return nil, err
	}
	return support.YMap(
		[2]*yaml.Node{support.YStr("insert"), support.YSeq(entry)},
	), nil
}

func isDshClaimedEntry(entry *yaml.Node, serverName string) bool {
	name := support.NodeGet(entry, "name")
	if name == nil {
		return false
	}
	nameStr, ok := support.NodeAsStr(name)
	if !ok || nameStr != dshMcpClientPlugin {
		return false
	}
	configServerName := support.NodeGet(support.NodeGet(entry, "config"), "serverName")
	if configServerName == nil {
		return false
	}
	serverNameStr, ok := support.NodeAsStr(configServerName)
	return ok && serverNameStr == serverName
}

// locateDshClaimedEntry：找 Reins 认领条目在操作列表里的 (op 下标, entry 下标)。
func locateDshClaimedEntry(ops []*yaml.Node, serverName string) (int, int, bool) {
	for opIndex, op := range ops {
		entries := support.NodeGet(op, "insert")
		if entries == nil || entries.Kind != yaml.SequenceNode {
			continue
		}
		for entryIndex, entry := range entries.Content {
			if isDshClaimedEntry(entry, serverName) {
				return opIndex, entryIndex, true
			}
		}
	}
	return 0, 0, false
}

// dshInsertList：取指定 op 的 insert 列表（可变切片语义：修改 Content 即改原
// 节点）。
func dshInsertList(ops []*yaml.Node, opIndex int) ([]*yaml.Node, error) {
	if opIndex < 0 || opIndex >= len(ops) {
		return nil, errors.New("Cordis patch 的 insert 操作必须是条目列表。")
	}
	list := support.NodeGet(ops[opIndex], "insert")
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil, errors.New("Cordis patch 的 insert 操作必须是条目列表。")
	}
	return list.Content, nil
}

// readExistingDshPatchEntries：既有条目按清洗后的 serverName 建键，config 转
// JSON 值供 inspect 归类与反显。
func readExistingDshPatchEntries(configPath string) (map[string]any, error) {
	ops, err := support.ReadOps(configPath)
	if err != nil {
		return nil, err
	}
	entries := map[string]any{}
	for _, op := range ops {
		list := support.NodeGet(op, "insert")
		if list == nil || list.Kind != yaml.SequenceNode {
			continue
		}
		for _, entry := range list.Content {
			name := support.NodeGet(entry, "name")
			if name == nil {
				continue
			}
			nameStr, ok := support.NodeAsStr(name)
			if !ok || nameStr != dshMcpClientPlugin {
				continue
			}
			config := support.NodeGet(entry, "config")
			serverNameNode := support.NodeGet(config, "serverName")
			serverName, ok := support.NodeAsStr(serverNameNode)
			if !ok {
				continue
			}
			entries[serverName] = yamlNodeToJSONValue(support.Untag(config))
		}
	}
	return entries, nil
}

func applyDshPatchEntry(configPath string, server *ResolvedMcpConfig) error {
	serverName := dshServerName(server.Name)
	ops, err := support.ReadOps(configPath)
	if err != nil {
		return err
	}

	if opIndex, entryIndex, found := locateDshClaimedEntry(ops, serverName); found {
		entry, err := desiredDshInsertEntry(serverName, server)
		if err != nil {
			return err
		}
		list, err := dshInsertList(ops, opIndex)
		if err != nil {
			return err
		}
		list[entryIndex] = entry
	} else {
		op, err := desiredDshInsertOp(serverName, server)
		if err != nil {
			return err
		}
		ops = append(ops, op)
	}

	return support.WriteOps(configPath, ops)
}

func removeDshPatchEntry(path *ResolvedMcpPath, configPath string, serverName string) (McpPathMutationResult, error) {
	claimedName := dshServerName(serverName)
	ops, err := support.ReadOps(configPath)
	if err != nil {
		return McpPathMutationResult{}, err
	}

	opIndex, entryIndex, found := locateDshClaimedEntry(ops, claimedName)
	if !found {
		return McpPathMutationResult{
			ServerName:  serverName,
			PathID:      path.Name,
			UpdatedPath: nil,
			Action:      "noop",
			Detail:      "目标配置里不存在该 MCP。",
		}, nil
	}

	list, err := dshInsertList(ops, opIndex)
	if err != nil {
		return McpPathMutationResult{}, err
	}
	list = append(list[:entryIndex], list[entryIndex+1:]...)
	ops[opIndex].Content[1].Content = list
	if len(list) == 0 {
		ops = append(ops[:opIndex], ops[opIndex+1:]...)
	}
	// 删空后保留空列表文件（`[]`），与「不整文件覆盖」的合并语义一致。
	if err := support.WriteOps(configPath, ops); err != nil {
		return McpPathMutationResult{}, err
	}

	updatedPath := support.DisplayPath(configPath)
	return McpPathMutationResult{
		ServerName:  serverName,
		PathID:      path.Name,
		UpdatedPath: &updatedPath,
		Action:      "remove",
		Detail:      fmt.Sprintf("已从 %s 移除 %s", path.Name, serverName),
	}, nil
}

// ---------------------------------------------------------------------------
// yaml.Node → JSON 值转换（展示与状态归类用）
// ---------------------------------------------------------------------------

// yamlNodeToJSONValue：把 yaml 节点转成可 JSON 序列化的值（map/slice/string/
// json.Number/bool/nil）。标量按 tag 解释，未知自定义 tag 的标量按字符串处理
// （untag 之后 tag 已清空）。
func yamlNodeToJSONValue(node *yaml.Node) any {
	if support.IsNullNode(node) {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!bool":
			value, _ := support.NodeAsBool(node)
			return value
		case "!!int":
			if _, ok := support.NodeAsInt64(node); ok {
				return json.Number(node.Value)
			}
			return node.Value
		case "!!float":
			return json.Number(node.Value)
		default:
			return node.Value
		}
	case yaml.SequenceNode:
		items := make([]any, 0, len(node.Content))
		for _, item := range node.Content {
			items = append(items, yamlNodeToJSONValue(item))
		}
		return items
	case yaml.MappingNode:
		object := map[string]any{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, ok := support.NodeAsStr(node.Content[i])
			if !ok {
				continue
			}
			object[key] = yamlNodeToJSONValue(node.Content[i+1])
		}
		return object
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// 序列化原语
// ---------------------------------------------------------------------------

// marshalJSONPretty：serde_json::to_string_pretty 的对应物（2 空格缩进、
// 键按字典序、不转义 HTML 字符），尾部带换行（Rust 写入侧显式补 \n）。
func marshalJSONPretty(value any) (string, error) {
	var builder strings.Builder
	encoder := json.NewEncoder(&builder)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// marshalTOML：toml::to_string_pretty 的对应物。BurntSushi 的键序稳定（map
// 键排序）但表头/空行拼版与 Rust toml crate 不同——两个 formatter 的确定性
// 输出本就不同，落盘契约是「合法 TOML + 语义往返 + 幂等」，不是逐字节一致。
// Indent 置空避免嵌套表内容缩进（Rust 不缩进）。
func marshalTOML(value any) (string, error) {
	var builder strings.Builder
	encoder := toml.NewEncoder(&builder)
	encoder.Indent = ""
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// filePathExtension：MCP 配置文件的后缀（不含点，小写）。fileExtension 已对齐
// Rust Path::extension（首字符的点属于文件名本身）；"MCP.JSON" → "json"。
func filePathExtension(configPath string) string {
	return strings.ToLower(fileExtension(filepath.Base(configPath)))
}
