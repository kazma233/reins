// workspace.go：mod.rs 的平移——应用状态组装、config.yaml 解析、默认模板、
// 路径展开与规范化、路径选择器（经 DialogPort 注入）、盘上链接
// 的建删原语。targets/skills/mcps 各文件的入口从这里取共享工具。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"reins/internal/agents"
	"reins/internal/support"
)

// ---------------------------------------------------------------------------
// App state loading
// ---------------------------------------------------------------------------

// WorkspaceStateInner：全量状态 = 配置文档 + （解析有效时的）盘面巡检。
func WorkspaceStateInner(store *WorkspaceConfigStore) (*WorkspaceState, error) {
	env := store.Env()
	document, err := store.LoadDocument()
	if err != nil {
		return nil, err
	}
	var inspection *WorkspaceInspection
	if document.Validation.Valid {
		inspection, err = inspectAppStateDocument(document.RawContent, store.ConfigPath(), env)
		if err != nil {
			return nil, err
		}
	}
	return &WorkspaceState{Document: document, Inspection: inspection}, nil
}

func documentFromContent(store *WorkspaceConfigStore, configPath string, rawContent string) *WorkspaceConfigDocument {
	config, err := parseManagerConfig(rawContent, configPath, store.Env())
	if err != nil {
		// Rust anyhow 的顶层 context 是「YAML 解析失败：路径」；Go 版把根因
		// 一并带上（同一条错误链，信息只增不减）。
		return &WorkspaceConfigDocument{
			ConfigPath: support.DisplayPath(configPath),
			Exists:     true,
			RawContent: rawContent,
			Config:     nil,
			Validation: ValidationReport{Valid: false, Errors: []string{err.Error()}, Warnings: []string{}},
		}
	}
	return &WorkspaceConfigDocument{
		ConfigPath: support.DisplayPath(configPath),
		Exists:     true,
		RawContent: rawContent,
		Config:     configToView(store, config),
		Validation: ValidationReport{Valid: true, Errors: []string{}, Warnings: []string{}},
	}
}

// ---------------------------------------------------------------------------
// Config view
// ---------------------------------------------------------------------------

func configToView(store *WorkspaceConfigStore, config *ResolvedManagerConfig) *WorkspaceConfigView {
	view := &WorkspaceConfigView{
		SkillPaths:   make([]SkillPathView, 0, len(config.SkillPaths)),
		McpPaths:     make([]McpPathView, 0, len(config.McpPaths)),
		SkillSources: make([]SkillSourceConfigView, 0, len(config.SkillSources)),
		Mcps:         make([]McpConfigView, 0, len(config.Mcps)),
	}
	for _, name := range sortedKeys(config.SkillPaths) {
		path := config.SkillPaths[name]
		view.SkillPaths = append(view.SkillPaths, SkillPathView{
			Name: name,
			Dir:  support.DisplayPath(path.Dir),
		})
	}
	for _, name := range sortedKeys(config.McpPaths) {
		view.McpPaths = append(view.McpPaths, mcpPathToView(config.McpPaths[name]))
	}
	for _, id := range sortedKeys(config.SkillSources) {
		source := config.SkillSources[id]
		switch source.Kind {
		case rawSourceKindLocal:
			view.SkillSources = append(view.SkillSources, SkillSourceConfigView{
				Type:                rawSourceKindLocal,
				ID:                  id,
				RootPath:            support.DisplayPath(source.RootPath),
				Description:         source.Description,
				IncludeNamePatterns: cloneStrings(source.IncludeNamePatterns),
				IncludePathPatterns: cloneStrings(source.IncludePathPatterns),
				Label:               formatLocalSkillSourceLabel(source.RootPath),
			})
		case rawSourceKindGit:
			lastFetched := gitCacheLastFetchedAtMs(store, source.Repo)
			view.SkillSources = append(view.SkillSources, SkillSourceConfigView{
				Type:                rawSourceKindGit,
				ID:                  id,
				Repo:                source.Repo,
				Ref:                 source.Ref,
				LastFetchedAt:       lastFetched,
				Description:         source.Description,
				IncludeNamePatterns: cloneStrings(source.IncludeNamePatterns),
				IncludePathPatterns: cloneStrings(source.IncludePathPatterns),
				Label:               formatGitSkillSourceLabel(source.Repo, source.Ref),
			})
		}
	}
	for _, mcp := range config.Mcps {
		view.Mcps = append(view.Mcps, McpConfigView{
			Name:      mcp.Name,
			Transport: mcp.Transport,
			CreatedAt: mcp.CreatedAt,
			Homepage:  mcp.Homepage,
			Command:   mcp.Command,
			Args:      cloneStrings(mcp.Args),
			Env:       cloneStringMap(mcp.Env),
			URL:       mcp.URL,
			Headers:   cloneStringMap(mcp.Headers),
			Timeout:   mcp.Timeout,
		})
	}
	return view
}

func mcpPathToView(path *ResolvedMcpPath) McpPathView {
	writer := mcpFormatWriterFor(path.MCPConfigType)
	// 示例生成失败（序列化异常）时留空，前端隐藏示例区——说明行仍在。
	examples := make([]McpFormatExample, 0)
	if shaped, err := writer.shapeExamples(path.MCPConfigPrefix, "my-server"); err == nil {
		for _, example := range shaped {
			examples = append(examples, McpFormatExample{Label: example.label, Body: example.body})
		}
	}
	return McpPathView{
		Name:                 path.Name,
		ConfigPath:           support.DisplayPath(path.ConfigPath),
		ConfigPrefix:         path.MCPConfigPrefix,
		MCPFormatDescription: writer.description(),
		MCPFormatExamples:    examples,
		SSEUnsupported:       path.SSEUnsupported,
	}
}

func formatLocalSkillSourceLabel(rootPath string) string {
	return support.DisplayPath(rootPath)
}

func formatGitSkillSourceLabel(repo string, reference *string) string {
	ref := "default"
	if reference != nil && strings.TrimSpace(*reference) != "" {
		ref = strings.TrimSpace(*reference)
	}
	return fmt.Sprintf("%s · %s", repo, ref)
}

// ---------------------------------------------------------------------------
// config.yaml 解析（serde_yaml → yaml.v3）
// ---------------------------------------------------------------------------

func parseManagerConfig(rawContent string, configPath string, env *support.Environment) (*ResolvedManagerConfig, error) {
	var raw RawManagerConfig
	if err := yaml.Unmarshal([]byte(rawContent), &raw); err != nil {
		return nil, fmt.Errorf("YAML 解析失败：%s：%v", configPath, err)
	}
	skillPaths := map[string]*ResolvedSkillPath{}
	mcpPaths := map[string]*ResolvedMcpPath{}
	skillSources := map[string]*ResolvedSkillSourceDefinition{}

	skillDefaults := builtinSkillPathDefaultsMap(env)
	mcpDefaults := builtinMcpPathDefaultsMap(env)
	sseUnsupported := sseUnsupportedConfigPaths(env)

	configBaseDir := filepath.Dir(configPath)
	for _, rawName := range sortedKeys(raw.SkillPaths) {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, errors.New("发现空的 skill path 条目名。")
		}
		rawPath := raw.SkillPaths[rawName]
		dirRaw := strings.TrimSpace(rawPath.Dir)
		var dir string
		switch {
		case dirRaw != "":
			expanded, err := expandPath(dirRaw, configBaseDir)
			if err != nil {
				return nil, err
			}
			dir = expanded
		case skillDefaults[name].Dir != "":
			// 空目录按名字回落内置 defaults：旧模板的「留空跟随环境重定向」
			// 用法（如 grokbuild）迁移后保持语义。
			dir = skillDefaults[name].Dir
		default:
			return nil, fmt.Errorf("技能路径 %s 的 dir 不能为空。", name)
		}
		skillPaths[name] = &ResolvedSkillPath{Name: name, Dir: dir}
	}

	for _, rawName := range sortedKeys(raw.McpPaths) {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, errors.New("发现空的 mcp path 条目名。")
		}
		rawPath := raw.McpPaths[rawName]
		defaults, hasDefaults := mcpDefaults[name]
		var expanded string
		switch {
		case rawPath.ConfigPath != nil && strings.TrimSpace(*rawPath.ConfigPath) != "":
			result, err := expandPath(*rawPath.ConfigPath, configBaseDir)
			if err != nil {
				return nil, err
			}
			expanded = result
		case hasDefaults && defaults.ConfigPath != "":
			// 省略 config_path 按名字回落内置 defaults：跟随环境重定向的
			// 用法（如 grokbuild）与技能路径的空目录回落同语义。
			expanded = defaults.ConfigPath
		default:
			return nil, fmt.Errorf("MCP 路径 %s 的 config_path 不能为空。", name)
		}
		prefix := ""
		if rawPath.ConfigPrefix != nil {
			prefix = *rawPath.ConfigPrefix
		} else if hasDefaults {
			prefix = defaults.ConfigPrefix
		}
		prefix = strings.TrimSpace(prefix)
		configType := agents.McpConfigCommon
		switch {
		case rawPath.ConfigType != nil:
			configType = *rawPath.ConfigType
		case hasDefaults:
			configType = defaults.ConfigType
		}

		// configPrefix 只在 writer 声明必填时才要求（dsh/hermes 按文件结构
		// 定位条目，允许空 prefix）。
		if prefix == "" && mcpFormatWriterFor(configType).prefixRequired() {
			return nil, fmt.Errorf("MCP 路径 %s 的 config_prefix 不能为空。", name)
		}

		mcpPaths[name] = &ResolvedMcpPath{
			Name:            name,
			ConfigPath:      expanded,
			MCPConfigPrefix: prefix,
			MCPConfigType:   configType,
			SSEUnsupported:  sseUnsupported[strings.ToLower(support.DisplayPath(expanded))],
		}
	}

	for _, source := range raw.SkillSources {
		id := strings.TrimSpace(source.ID)
		if id == "" {
			return nil, errors.New("发现空的 skill source id。")
		}
		if _, exists := skillSources[id]; exists {
			return nil, fmt.Errorf("skill source id 重复：%s", id)
		}
		definition := source.Source
		resolved := &ResolvedSkillSourceDefinition{
			Kind:                definition.Type,
			Ref:                 definition.Ref,
			Description:         normalizeSourceDescription(definition.Description),
			IncludeNamePatterns: normalizePatterns(definition.IncludeNamePatterns),
			IncludePathPatterns: normalizePatterns(definition.IncludePathPatterns),
		}
		switch definition.Type {
		case rawSourceKindLocal:
			rootPath, err := expandPath(definition.RootPath, configBaseDir)
			if err != nil {
				return nil, err
			}
			resolved.RootPath = rootPath
		case rawSourceKindGit:
			resolved.Repo = definition.Repo
		default:
			return nil, fmt.Errorf("未知 skill source type：%s", definition.Type)
		}
		skillSources[id] = resolved
	}

	seenMcpNames := map[string]bool{}
	mcps := make([]*ResolvedMcpConfig, 0, len(raw.Mcps))
	for _, mcp := range raw.Mcps {
		name := strings.TrimSpace(mcp.Name)
		if name == "" {
			return nil, errors.New("发现空的 mcp name。")
		}
		if seenMcpNames[name] {
			return nil, fmt.Errorf("mcp name 重复：%s", name)
		}
		seenMcpNames[name] = true

		switch mcp.Transport {
		case TransportStdio:
			if strings.TrimSpace(stringOrEmpty(mcp.Command)) == "" {
				return nil, fmt.Errorf("stdio MCP %s 必须提供 command。", name)
			}
		case TransportHTTP, TransportSSE:
			if strings.TrimSpace(stringOrEmpty(mcp.URL)) == "" {
				return nil, fmt.Errorf("远程 MCP %s 必须提供 mcp 链接。", name)
			}
		}

		mcps = append(mcps, resolveRawMcp(name, mcp))
	}

	return &ResolvedManagerConfig{
		SkillPaths:   skillPaths,
		McpPaths:     mcpPaths,
		SkillSources: skillSources,
		Mcps:         mcps,
	}, nil
}

// resolveRawMcp：raw MCP 条目 → 解析形态（映射只此一份：解析侧与改名手术
// 共用；名字由调用方给出——解析侧传归一后的条目名，手术侧传改名目标）。
func resolveRawMcp(name string, raw *RawMcpConfig) *ResolvedMcpConfig {
	return &ResolvedMcpConfig{
		Name:      name,
		Transport: raw.Transport,
		CreatedAt: raw.CreatedAt,
		Homepage:  raw.Homepage,
		Command:   raw.Command,
		Args:      raw.Args,
		Env:       raw.Env,
		URL:       raw.URL,
		Headers:   raw.Headers,
		Timeout:   raw.Timeout,
	}
}

// ---------------------------------------------------------------------------
// 默认 config.yaml 模板
// ---------------------------------------------------------------------------

// DefaultConfigTemplate：生成默认 config.yaml。新装空开始（决策记录
// 2026-11-28-skill-and-mcp-paths.md 第 4 条）：不预置任何工具条目，落点由
// 用户从创建弹窗的预设里挑（预设值由后端按 agents 清单实时解析下发），或
// 手写任意路径。空集合写显式形态，与 normalizeRawManagerConfig 的落盘
// 口径一致。
func DefaultConfigTemplate() string {
	var out strings.Builder
	out.WriteString("# 技能路径：接受 skills 分发的命名目录条目；从界面「添加」选择预设（按工具预填）或自定义任意目录。\n")
	out.WriteString("skill_paths: {}\n\n")
	out.WriteString("# MCP 路径：接受 MCP 分发的命名条目（配置文件路径 + 格式 + 前缀），与技能路径互不约束。\n")
	out.WriteString("mcp_paths: {}\n\n")
	out.WriteString("skill_sources: []\n\n")
	out.WriteString("mcps: []\n")
	return out.String()
}

// ---------------------------------------------------------------------------
// 路径选择器（经 DialogPort 注入，由 Wails app.Dialog 实现）
// ---------------------------------------------------------------------------

func SelectLocalSkillSourceDirectoryInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
	return pickFolder(dialog, path)
}

func SelectSkillPathDirectoryInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
	return pickFolder(dialog, path)
}

// SelectSkillBatchImportFileInner：批量导入 JSON 文件；选完即读出内容一并
// 返回，前端不再有读任意路径的通道。
func SelectSkillBatchImportFileInner(dialog DialogPort) (*SkillBatchImportFile, error) {
	file, ok := dialog.PickFile(DialogOptions{
		Filters: []DialogFilter{{Label: "JSON", Extensions: []string{"json"}}},
	})
	if !ok {
		return nil, nil
	}
	content, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("读取批量导入文件失败：%s", support.DisplayPath(file))
	}
	return &SkillBatchImportFile{
		Path:    support.DisplayPath(file),
		Content: string(content),
	}, nil
}

func SelectMcpPathConfigFileInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
	options := DialogOptions{}
	if path != nil && strings.TrimSpace(*path) != "" {
		expanded, err := expandPath(*path, "")
		if err != nil {
			return nil, err
		}
		options.Directory = filepath.Dir(expanded)
		if fileInfo, err := os.Stat(expanded); err == nil && !fileInfo.IsDir() {
			options.FileName = filepath.Base(expanded)
		}
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		options.Directory = cwd
	}
	selected, ok := dialog.PickFile(options)
	if !ok {
		return nil, nil
	}
	return &WorkspaceSelection{WorkspaceDir: support.DisplayPath(selected)}, nil
}

func pickFolder(dialog DialogPort, initial *string) (*WorkspaceSelection, error) {
	initialDir := ""
	if initial != nil && strings.TrimSpace(*initial) != "" {
		expanded, err := expandPath(*initial, "")
		if err != nil {
			return nil, err
		}
		initialDir = expanded
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		initialDir = cwd
	}
	selected, ok := dialog.PickFolder(DialogOptions{Directory: initialDir})
	if !ok {
		return nil, nil
	}
	return &WorkspaceSelection{WorkspaceDir: support.DisplayPath(selected)}, nil
}

// ---------------------------------------------------------------------------
// 路径展开与规范化
// ---------------------------------------------------------------------------

// expandPath：~/ 前缀按 HOME 展开（与清单解析、providers 路径同一个 home
// 口径，测试覆盖才生效）；绝对路径原样；相对路径落 base_dir 或 cwd。
func expandPath(value string, baseDir string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("路径不能为空。")
	}

	if relative, found := strings.CutPrefix(trimmed, "~/"); found {
		home, ok := support.UserHomeDir()
		if !ok {
			return "", errors.New("无法解析 HOME 目录。")
		}
		return joinPath(home, relative), nil
	}

	if filepath.IsAbs(trimmed) {
		return trimmed, nil
	}

	if baseDir != "" {
		return joinPath(baseDir, trimmed), nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return joinPath(cwd, trimmed), nil
}

// joinPath 对齐 Rust PathBuf::join：不清洗、不解析 '..'，保留 base 原有
// 分隔符风格（Windows 上的混合分隔符是既有口径）。
func joinPath(base string, part string) string {
	if base == "" {
		return part
	}
	if strings.HasSuffix(base, "/") || (runtime.GOOS == "windows" && strings.HasSuffix(base, "\\")) {
		return base + part
	}
	return base + string(filepath.Separator) + part
}

// stripPathPrefix：组件级的 strip_prefix。rest 为空表示两者相等；后随
// 分隔符被吃掉。
func stripPathPrefix(path string, prefix string) (string, bool) {
	if prefix == "" {
		return path, true
	}
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := path[len(prefix):]
	if rest == "" {
		return "", true
	}
	if rest[0] == '/' || (runtime.GOOS == "windows" && rest[0] == '\\') {
		return rest[1:], true
	}
	return "", false
}

// fileExtension 对齐 Rust Path::extension：首字符的 '.' 属于文件名本身
// （".json" 整体是 stem、无扩展名）。
func fileExtension(name string) string {
	index := strings.LastIndexByte(name, '.')
	if index <= 0 {
		return ""
	}
	return name[index+1:]
}

// ---------------------------------------------------------------------------
// 文件系统原语
// ---------------------------------------------------------------------------

// removeExistingPath：链接按链接本体删（Unix rm link / Windows junction
// 先摘 reparse 数据再删目录）；真实目录递归删、文件直接删；不存在即成功。
func removeExistingPath(path string) error {
	metadata, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("Failed to inspect %s: %w", path, err)
	}

	if metadata.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			return err
		}
		return nil
	}

	removed, err := removePlatformDirectoryLink(path)
	if err != nil {
		return err
	}
	if removed {
		return nil
	}

	if metadata.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// removeEmptyParentDirs：从 start 向上删空目录，遇到非空/删除失败/越过
// root 即停。
func removeEmptyParentDirs(root string, start *string) {
	current := start
	for current != nil {
		if *current == root {
			break
		}
		entries, err := os.ReadDir(*current)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := os.Remove(*current); err != nil {
			break
		}
		parent := filepath.Dir(*current)
		if parent == *current {
			break
		}
		current = &parent
	}
}

// currentTimestampMs：系统时钟早于 Unix 纪元时归零（Rust unwrap_or(0)）。
func currentTimestampMs() int64 {
	now := time.Now().UnixMilli()
	if now < 0 {
		return 0
	}
	return now
}

// ---------------------------------------------------------------------------
// 规范化小工具
// ---------------------------------------------------------------------------

func normalizePatterns(patterns []string) []string {
	normalized := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		trimmed := strings.TrimSpace(pattern)
		if trimmed != "" {
			normalized = append(normalized, trimmed)
		}
	}
	return normalized
}

// normalizeSourceDescription：来源描述与 patterns 同一口径：解析与写入两侧
// 都归一，空白串视同未填。
func normalizeSourceDescription(description *string) *string {
	if description == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*description)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// stringOrEmpty：写 null 的 Option 的读取侧（nil → ""）。
func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func cloneStrings(values []string) []string {
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
