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
	"sort"
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
		Targets:      make([]TargetConfigView, 0, len(config.Targets)),
		SkillSources: make([]SkillSourceConfigView, 0, len(config.SkillSources)),
		Mcps:         make([]McpConfigView, 0, len(config.Mcps)),
		Projects:     make([]ProjectConfigView, 0, len(config.Projects)),
	}
	for _, id := range sortedTargetIDs(config.Targets) {
		view.Targets = append(view.Targets, targetToView(config.Targets[id]))
	}
	for _, projectID := range sortedKeys(config.Projects) {
		project := config.Projects[projectID]
		agentsView := make([]TargetConfigView, 0, len(project.Agents))
		for _, agentID := range sortedTargetIDs(project.Agents) {
			agentsView = append(agentsView, targetToView(project.Agents[agentID]))
		}
		view.Projects = append(view.Projects, ProjectConfigView{
			ID:     project.ID,
			Path:   support.DisplayPath(project.ProjectPath),
			Agents: agentsView,
		})
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

func targetToView(target *ResolvedTargetConfig) TargetConfigView {
	writer := mcpFormatWriterFor(target.MCPConfigType)
	var configPath *string
	if target.ConfigPath != nil {
		displayed := support.DisplayPath(*target.ConfigPath)
		configPath = &displayed
	}
	// 示例生成失败（序列化异常）时留空，前端隐藏示例区——说明行仍在。
	examples := make([]McpFormatExample, 0)
	if shaped, err := writer.shapeExamples(target.MCPConfigPrefix, "my-server"); err == nil {
		for _, example := range shaped {
			examples = append(examples, McpFormatExample{Label: example.label, Body: example.body})
		}
	}
	return TargetConfigView{
		ID:                   target.ID,
		SkillDir:             support.DisplayPath(target.SkillDir),
		ConfigPath:           configPath,
		MCPConfigPrefix:      target.MCPConfigPrefix,
		MCPFormatDescription: writer.description(),
		MCPFormatExamples:    examples,
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
	targets := map[AgentTargetID]*ResolvedTargetConfig{}
	skillSources := map[string]*ResolvedSkillSourceDefinition{}

	builtinDefaults := builtinTargetDefaultsMap(env)

	configBaseDir := filepath.Dir(configPath)
	for _, rawID := range sortedKeys(raw.Targets) {
		rawTarget := raw.Targets[rawID]
		id, err := normalizeTargetID(rawID)
		if err != nil {
			return nil, err
		}
		defaults, hasDefaults := builtinDefaults[id]
		skillDirRaw := strings.TrimSpace(rawTarget.SkillDir)
		var skillDir string
		switch {
		case skillDirRaw != "":
			skillDir, err = expandPath(skillDirRaw, configBaseDir)
			if err != nil {
				return nil, err
			}
		case hasDefaults:
			skillDir = defaults.SkillDir
		default:
			return nil, fmt.Errorf("目标 %s 的 skill_dir 不能为空。", id)
		}
		var configFile_path *string
		if rawTarget.Mcp.ConfigPath != nil {
			expanded, err := expandPath(*rawTarget.Mcp.ConfigPath, configBaseDir)
			if err != nil {
				return nil, err
			}
			configFile_path = &expanded
		} else if hasDefaults && defaults.ConfigPath != nil {
			pathCopy := *defaults.ConfigPath
			configFile_path = &pathCopy
		}
		prefix := ""
		if rawTarget.Mcp.ConfigPrefix != nil {
			prefix = *rawTarget.Mcp.ConfigPrefix
		} else if hasDefaults {
			prefix = defaults.ConfigPrefix
		}
		prefix = strings.TrimSpace(prefix)
		configType := agents.McpConfigCommon
		switch {
		case rawTarget.Mcp.ConfigType != nil:
			configType = *rawTarget.Mcp.ConfigType
		case hasDefaults:
			configType = defaults.ConfigType
		}

		// 与 normalizeRawTargetInput 的契约一致：configPrefix 只在真正有 MCP
		// 配置文件可写时才必填（不需要 MCP 的 target 两者皆空）。dsh 按
		// name+serverName 定位条目（writer 声明 prefix 不必填），允许留空。
		if configFile_path != nil && prefix == "" && mcpFormatWriterFor(configType).prefixRequired() {
			return nil, fmt.Errorf("目标 %s 的 mcp.config_prefix 不能为空。", id)
		}

		targets[id] = &ResolvedTargetConfig{
			ID:              id,
			IsProject:       false,
			SkillDir:        skillDir,
			ConfigPath:      configFile_path,
			MCPConfigPrefix: prefix,
			MCPConfigType:   configType,
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

		mcps = append(mcps, &ResolvedMcpConfig{
			Name:      name,
			Transport: mcp.Transport,
			CreatedAt: mcp.CreatedAt,
			Homepage:  mcp.Homepage,
			Command:   mcp.Command,
			Args:      mcp.Args,
			Env:       mcp.Env,
			URL:       mcp.URL,
			Headers:   mcp.Headers,
			Timeout:   mcp.Timeout,
		})
	}

	projects := map[string]*ResolvedProjectConfig{}
	for _, projectID := range sortedKeys(raw.Projects) {
		rawProject := raw.Projects[projectID]
		projectIDTrimmed := strings.TrimSpace(projectID)
		if projectIDTrimmed == "" {
			return nil, errors.New("发现空的 project id。")
		}
		if err := validateProjectID(projectIDTrimmed); err != nil {
			return nil, err
		}
		projectPath, err := expandPath(rawProject.Path, configBaseDir)
		if err != nil {
			return nil, err
		}
		agentDefaults := projectAgentDefaults(projectPath)
		agentsMap := map[AgentTargetID]*ResolvedTargetConfig{}
		for _, agentName := range sortedKeys(rawProject.Agents) {
			agentID, err := normalizeTargetID(agentName)
			if err != nil {
				return nil, err
			}
			var defaults *TargetDefaults
			for i := range agentDefaults {
				if agentDefaults[i].ID == agentID {
					defaults = &agentDefaults[i]
					break
				}
			}
			// 回退落点用规范化后的 id：`agent_name` 是 YAML 原文（可能是
			// `Codex`），用它拼路径会造出一个 `Codex/skills` 空目录，与工具
			// 实际读的位置不一致。
			skillDir := filepath.Join(projectPath, string(agentID), "skills")
			var configPathPtr *string
			prefix := ""
			configType := agents.McpConfigCommon
			if defaults != nil {
				skillDir = defaults.SkillDir
				if defaults.ConfigPath != nil {
					pathCopy := *defaults.ConfigPath
					configPathPtr = &pathCopy
				}
				prefix = defaults.ConfigPrefix
				configType = defaults.ConfigType
			}
			agentsMap[agentID] = &ResolvedTargetConfig{
				ID:              agentID,
				IsProject:       true,
				SkillDir:        skillDir,
				ConfigPath:      configPathPtr,
				MCPConfigPrefix: prefix,
				MCPConfigType:   configType,
			}
		}
		projects[projectIDTrimmed] = &ResolvedProjectConfig{
			ID:          projectIDTrimmed,
			ProjectPath: projectPath,
			Agents:      agentsMap,
		}
	}

	return &ResolvedManagerConfig{
		Targets:      targets,
		Projects:     projects,
		SkillSources: skillSources,
		Mcps:         mcps,
	}, nil
}

// ---------------------------------------------------------------------------
// 默认 config.yaml 模板
// ---------------------------------------------------------------------------

// templateTarget：默认模板的一个 target 条目（从 agents 清单构造再渲染）。
// 注释与字段省略是模板契约的一部分（留空 = 解析时回落 builtin defaults）。
type templateTarget struct {
	id            string
	note          string
	skillDir      string
	mcpConfigPath *string
	configPrefix  string
	configTypeKey string
}

// DefaultConfigTemplate：生成默认 config.yaml。支持 env 重定向的工具
// （grokbuild / pi / codex / claude / dsh）在生成模板时解析并固化具体路径；
// 之后以 config.yaml 里写入的路径为准，环境变量变化不再跟随。默认场景沿用
// ~ 前缀写法，与模板其他条目风格一致。
func DefaultConfigTemplate(env *support.Environment) string {
	targets := templateTargets(env)

	var out strings.Builder
	out.WriteString("targets:\n")
	for index, target := range targets {
		if index > 0 {
			out.WriteString("\n")
		}
		if target.note != "" {
			fmt.Fprintf(&out, "  # %s\n", target.note)
		}
		fmt.Fprintf(&out, "  %s:\n", target.id)
		fmt.Fprintf(&out, "    skill_dir: %s\n", target.skillDir)
		out.WriteString("    mcp:\n")
		if target.mcpConfigPath != nil {
			fmt.Fprintf(&out, "      config_path: %s\n", *target.mcpConfigPath)
		}
		fmt.Fprintf(&out, "      config_prefix: %s\n", target.configPrefix)
		fmt.Fprintf(&out, "      config_type: %s\n", target.configTypeKey)
	}

	out.WriteString("\nmcps: []\n\n# projects:\n#   my-app:\n#     path: ~/code/my-app\n#     agents:\n#       claude: {}\n#       codex: {}\n#       opencode: {}\n#       zcode: {}\n#       grokbuild: {}\n")
	return out.String()
}

func templateTargets(env *support.Environment) []templateTarget {
	var targets []templateTarget
	for i := range agents.AgentSpecs {
		spec := &agents.AgentSpecs[i]
		targetID := spec.TargetID
		if targetID == "" {
			continue
		}
		switch spec.Template {
		case agents.TemplateAbsent:
			continue
		case agents.TemplateStandard:
			entry := templateTarget{
				id:            targetID,
				skillDir:      templateGlobalPath(env, spec.Global.Root, spec.Global.SkillDir),
				configPrefix:  spec.Mcp.Prefix,
				configTypeKey: string(spec.Mcp.ConfigType),
			}
			if spec.Global.McpConfigPath != "" {
				path := templateGlobalPath(env, spec.Global.Root, spec.Global.McpConfigPath)
				entry.mcpConfigPath = &path
			}
			targets = append(targets, entry)
		case agents.TemplateFollowEnv:
			rootPrefix := spec.Global.Root.HomePrefix()
			note := fmt.Sprintf(
				"留空路径以跟随 %s；默认 %s/%s 和 %s/%s。",
				spec.Global.Root.Redirect.EnvVar,
				rootPrefix, spec.Global.SkillDir,
				rootPrefix, spec.Global.McpConfigPath,
			)
			targets = append(targets, templateTarget{
				id:            targetID,
				note:          note,
				skillDir:      `""`,
				configPrefix:  spec.Mcp.Prefix,
				configTypeKey: string(spec.Mcp.ConfigType),
			})
		}
	}
	return targets
}

// templateGlobalPath：模板里的全局路径写法。先按解析口径求出真实落点，再
// 折回 ~ 缩写。解析只有 GlobalRoot.ResolvePath 一处（与 target defaults 同
// 一个函数），所以「模板写出的路径」与「解析后的路径」必然一致；root 被
// env 重定向时求出的绝对路径不是 HOME 子路径，显示为绝对路径。模板是写给
// 用户与 reins 解析器共用的文本，分隔符统一用 /，Windows 上不混入反斜杠
// （解析端两种分隔符都接受）。
func templateGlobalPath(env *support.Environment, root agents.GlobalRoot, rel string) string {
	resolved, ok := root.ResolvePath(env, rel)
	if !ok {
		return fmt.Sprintf("%s/%s", root.HomePrefix(), rel)
	}
	// 落在 HOME 下就折回 ~ 缩写（含 home 自身），否则写绝对路径——后者正是
	// root 被 env 重定向时的形态，用户能一眼看出该工具不受 HOME 影响。
	if env.Home != "" {
		if relative, found := stripPathPrefix(resolved, env.Home); found {
			if relative == "" {
				return "~"
			}
			return "~/" + strings.ReplaceAll(relative, "\\", "/")
		}
	}
	return strings.ReplaceAll(resolved, "\\", "/")
}

// ---------------------------------------------------------------------------
// 路径选择器（经 DialogPort 注入，由 Wails app.Dialog 实现）
// ---------------------------------------------------------------------------

func SelectProjectPathInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
	return pickFolder(dialog, path)
}

func SelectLocalSkillSourceDirectoryInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
	return pickFolder(dialog, path)
}

func SelectTargetSkillDirectoryInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
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

func SelectTargetMCPConfigFileInner(dialog DialogPort, path *string) (*WorkspaceSelection, error) {
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

// validateProjectID：project id 是用户可见标识，也是复合地址 `project:agent`
// 的前半段：含 `:` 会让地址不可拆（前后端都按第一个冒号切），含 `/` 会破坏
// 前端按展示 label 分组。校验只有这一处，写入与解析两侧都调用。
func validateProjectID(value string) error {
	if strings.Contains(value, ":") || strings.Contains(value, "/") {
		return fmt.Errorf("project id 不能包含 `:` 或 `/`：%s", value)
	}
	return nil
}

// normalizeTargetID：小写、下划线折连字符、open-code 别名归一、字符集校验。
func normalizeTargetID(value string) (AgentTargetID, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	trimmed = strings.ReplaceAll(trimmed, "_", "-")
	if trimmed == "open-code" {
		trimmed = "opencode"
	}
	if trimmed == "" {
		return "", errors.New("target id 不能为空。")
	}
	for _, ch := range trimmed {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '-' {
			return "", fmt.Errorf("target id 只能包含小写字母、数字和 - ：%s", strings.TrimSpace(value))
		}
	}
	return AgentTargetID(trimmed), nil
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

// sortedTargetIDs：AgentTargetID 键 map 的 BTreeMap 迭代序。
func sortedTargetIDs(values map[AgentTargetID]*ResolvedTargetConfig) []AgentTargetID {
	keys := make([]AgentTargetID, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
