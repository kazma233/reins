// skills.go：技能/同步域——从 local/git 来源发现 skill、导入进 config、跨
// target 装/卸目录链接。对齐 Rust 版 workspace/skills.rs。
package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"reins/internal/support"
)

// Discover all skills in a source root, without applying include filters.
// The sync dialog lists every skill and tags each with its match state.
func discoverSourceSkillsRaw(ctx context.Context, store *WorkspaceConfigStore, source *ResolvedSkillSourceDefinition) (string, []DiscoveredSkill, error) {
	switch source.Kind {
	case rawSourceKindLocal:
		skills, err := DiscoverSkillsInDirectory(source.RootPath)
		return source.RootPath, skills, err
	case rawSourceKindGit:
		cache, err := cloneGitRepoToCache(ctx, store, source.Repo, source.Ref)
		if err != nil {
			return "", nil, err
		}
		skills, err := DiscoverSkillsInDirectory(cache.CacheDir)
		return cache.CacheDir, skills, err
	}
	return "", nil, fmt.Errorf("未知 skill source type：%s", source.Kind)
}

// includePatternsOf：来源的 (name, path) include patterns。
func includePatternsOf(source *ResolvedSkillSourceDefinition) ([]string, []string) {
	return source.IncludeNamePatterns, source.IncludePathPatterns
}

// BuildSyncTargetOptions：同步弹窗的全部 target 候选（全局 + 项目 agent），
// 并标注落点是目录链接（指向别的受管 target）的条目，让界面禁用并展示。
func BuildSyncTargetOptions(store *WorkspaceConfigStore) ([]SyncTargetOption, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	sources, err := buildSkillLinkSources(store, config)
	if err != nil {
		return nil, err
	}
	return buildSyncTargetOptionsWithSources(config, sources)
}

// skillLinkSource：链接归类用的来源投影。
type skillLinkSource struct {
	id                  string
	rootPath            string
	includeNamePatterns []string
	includePathPatterns []string
}

func buildSkillLinkSources(store *WorkspaceConfigStore, config *ResolvedManagerConfig) ([]skillLinkSource, error) {
	sources := make([]skillLinkSource, 0, len(config.SkillSources))
	for _, id := range sortedKeys(config.SkillSources) {
		source := config.SkillSources[id]
		rootPath, err := resolveSourceRoot(store, source)
		if err != nil {
			return nil, err
		}
		namePatterns, pathPatterns := includePatternsOf(source)
		sources = append(sources, skillLinkSource{
			id:                  id,
			rootPath:            normalizeLinkPath(rootPath),
			includeNamePatterns: namePatterns,
			includePathPatterns: pathPatterns,
		})
	}
	return sources, nil
}

// buildSyncTargetOptionsWithSources：与 source 根路径解析分离，保证关联
// 分类不会 clone/pull git source。
func buildSyncTargetOptionsWithSources(config *ResolvedManagerConfig, sources []skillLinkSource) ([]SyncTargetOption, error) {
	// 落点的链接状态由寻址层统一提供（是不是链接、内容属于谁、解出的真实
	// 目录），本地不再自己 canonicalize 一遍。
	links, err := config.skillDirLinks()
	if err != nil {
		return nil, err
	}

	options := make([]SyncTargetOption, 0, len(links))
	for _, entry := range links {
		skillDir := entry.target.SkillDir
		// 落点是软链接时：能对上受管 target 就给出它的 id（命名归属），对不上
		// 就把链接解出的真实目录带出去——界面的「内容属谁 / 文件在哪」都靠
		// 这两个值，不再由前端猜。
		var skillDirLinkTarget *string
		if entry.link.IsLink {
			displayed := support.DisplayPath(entry.link.LinkTarget)
			skillDirLinkTarget = &displayed
		}
		// 内容属于链接另一端时，那一批物理链接不该算成本 target 自己的
		// 安装记录。
		associations := []SkillLinkAssociation{}
		if !entry.link.IsAlias() {
			associations, err = scanTargetSkillLinks(skillDir, sources)
			if err != nil {
				return nil, err
			}
		}
		var projectID *string
		agentID := string(entry.reference.agentID)
		if entry.reference.projectID != "" {
			id := entry.reference.projectID
			projectID = &id
		}
		id := entry.reference.CompositeID()
		var linkedTargetID *string
		if entry.link.HasLinkedTarget {
			owner := entry.link.LinkedTargetID
			linkedTargetID = &owner
		}
		options = append(options, SyncTargetOption{
			ID:                 id,
			ProjectID:          projectID,
			AgentID:            agentID,
			Label:              id,
			SkillDir:           support.DisplayPath(skillDir),
			LinkedTargetID:     linkedTargetID,
			SkillDirLinkTarget: skillDirLinkTarget,
			Links:              associations,
		})
	}

	return options, nil
}

func scanTargetSkillLinks(skillDir string, sources []skillLinkSource) ([]SkillLinkAssociation, error) {
	links, err := enumerateLinks(skillDir)
	if err != nil {
		return nil, err
	}
	associations := make([]SkillLinkAssociation, 0, len(links))
	for _, link := range links {
		associations = append(associations, classifySkillLink(link.destination, link.resolved, sources))
	}

	sort.SliceStable(associations, func(i, j int) bool {
		if associations[i].SkillName != associations[j].SkillName {
			return associations[i].SkillName < associations[j].SkillName
		}
		return associations[i].DestinationPath < associations[j].DestinationPath
	})
	return associations, nil
}

func classifySkillLink(destination string, sourcePath string, sources []skillLinkSource) SkillLinkAssociation {
	sourcePath = normalizeLinkPath(sourcePath)
	skillName := filepath.Base(destination)
	if skillName == "" || skillName == "." || skillName == string(filepath.Separator) {
		skillName = "skill"
	}
	sourceSkillName := filepath.Base(sourcePath)
	if sourceSkillName == "" || sourceSkillName == "." || sourceSkillName == string(filepath.Separator) {
		sourceSkillName = skillName
	}

	type sourceMatch struct {
		source       skillLinkSource
		relativePath string
	}
	var matches []sourceMatch
	for _, source := range sources {
		if relativePath, ok := relativeToSourceRoot(sourcePath, source.rootPath); ok {
			matches = append(matches, sourceMatch{source: source, relativePath: relativePath})
		}
	}

	matchedSourceIDs := make([]string, 0, len(matches))
	for _, match := range matches {
		matchedSourceIDs = append(matchedSourceIDs, match.source.id)
	}

	state := LinkStateUnmanaged
	if len(matches) > 0 {
		switch {
		case !fileExists(joinPath(sourcePath, "SKILL.md")):
			state = LinkStateSourceMissed
		default:
			matched := false
			for _, match := range matches {
				if matchesInclude(sourceSkillName, match.relativePath, match.source.includeNamePatterns, match.source.includePathPatterns) {
					matched = true
					break
				}
			}
			if matched {
				state = LinkStateLinked
			} else {
				state = LinkStateExcluded
			}
		}
	}

	// 链接另一端有 SKILL.md 就带出描述，非受管控的链接也能靠它认出内容。
	description := skillDescriptionOf(joinPath(sourcePath, "SKILL.md"))

	relatives := make([]SkillSourceRelative, 0, len(matches))
	for _, match := range matches {
		relatives = append(relatives, SkillSourceRelative{
			SourceID:     match.source.id,
			RelativePath: match.relativePath,
		})
	}

	return SkillLinkAssociation{
		SkillName:        skillName,
		DestinationPath:  support.DisplayPath(destination),
		SourcePath:       support.DisplayPath(sourcePath),
		Description:      description,
		MatchedSourceIDs: matchedSourceIDs,
		SourceRelatives:  relatives,
		State:            state,
	}
}

// BuildSyncSkillOptions：同步弹窗的 skill 候选集，逐个标注是否命中来源的
// include patterns（命中的默认勾选，未命中的折叠展示）。
func BuildSyncSkillOptions(ctx context.Context, store *WorkspaceConfigStore, sourceID string) (*SyncSkillOptionsResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	sourceDefinition, err := sourceDefinitionFromConfig(config, sourceID)
	if err != nil {
		return nil, err
	}
	sourceRoot, skills, err := discoverSourceSkillsRaw(ctx, store, sourceDefinition)
	if err != nil {
		return nil, err
	}
	namePatterns, pathPatterns := includePatternsOf(sourceDefinition)

	options := make([]SyncSkillOption, 0, len(skills))
	for _, skill := range skills {
		options = append(options, SyncSkillOption{
			Name:         skill.Name,
			RelativePath: skill.RelativePath,
			Matched:      matchesInclude(skill.Name, skill.RelativePath, namePatterns, pathPatterns),
			Description:  skill.Description,
		})
	}

	return &SyncSkillOptionsResult{
		Skills:     options,
		SourceRoot: support.DisplayPath(sourceRoot),
	}, nil
}

// SyncSourceToTargetsInner：统一同步：发现来源 → 清掉全部 target 上本来源
// 的旧链接 → 把选中的 skill 按目录链接装进选中的 target。不再读写 config
// 里的技能注册表；只操作链接。
func SyncSourceToTargetsInner(store *WorkspaceConfigStore, input SourceSyncInput) (*SourceSyncResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	sourceDefinition, err := sourceDefinitionFromConfig(config, input.SourceID)
	if err != nil {
		return nil, err
	}

	// 1. Reuse the dialog snapshot when present; it was already refreshed when
	// the sync dialog opened, so confirmation should not fetch and scan again.
	sourceRoot, discovered, err := sourceSyncSnapshot(store, sourceDefinition, &input)
	if err != nil {
		return nil, err
	}

	// 2. Build a set of selected skill relative paths.
	selected := map[string]bool{}
	for _, path := range input.SkillPaths {
		selected[path] = true
	}
	selectedSkills := make([]DiscoveredSkill, 0)
	for _, skill := range discovered {
		if selected[skill.RelativePath] {
			selectedSkills = append(selectedSkills, skill)
		}
	}

	conflicts, err := sourceSyncConflicts(config, sourceRoot, selectedSkills, input.TargetIDs)
	if err != nil {
		return nil, err
	}
	if !input.OverwriteExisting && len(conflicts) > 0 {
		lines := make([]string, 0, len(conflicts))
		for _, conflict := range conflicts {
			lines = append(lines, fmt.Sprintf("%s / %s -> %s", conflict.TargetID, conflict.SkillName, conflict.DestinationPath))
		}
		return nil, fmt.Errorf("同步会覆盖非本来源路径：\n%s", strings.Join(lines, "\n"))
	}

	// 3. 参与分发的目标。两种情况都产出提示而不是静默：正常界面已经把它们
	//    标成不可勾并说明原因，能走到这里说明是客户端直接调用（或界面过期
	//    快照）。
	warnings := []string{}
	resolved, err := resolveIDs(config, input.TargetIDs)
	if err != nil {
		return nil, err
	}
	participating := make([]*managedTarget, 0, len(resolved))
	for i := range resolved {
		target := &resolved[i]
		switch {
		case target.skip == nil:
			participating = append(participating, target)
		// 与报错路径同一说法，只在前面加「已跳过」这个处置结果。
		case *target.skip == SkipMissing:
			warnings = append(warnings, fmt.Sprintf("%s，已跳过。", targetUnavailable(target.id)))
		case *target.skip == SkipLinkedDir:
			warnings = append(warnings, fmt.Sprintf("%s 的 skills 目录是软链接，内容由链接另一端负责，已跳过。", target.id))
		}
	}
	selectedEntries := make([]selectedSkill, 0, len(selectedSkills))
	for _, skill := range selectedSkills {
		selectedEntries = append(selectedEntries, selectedSkill{name: skill.Name, relativePath: skill.RelativePath})
	}

	// 4. 先清掉本来源在这批目标上的旧链接，再按选中集合装回去；两次操作
	//    共用同一份目标列表，同步不会碰用户没选的目标。
	install := newSkillInstall(sourceRoot)
	removedItems, err := install.removeInstalled(participating)
	if err != nil {
		return nil, err
	}
	removed := make([]SkillSyncItem, 0, len(removedItems))
	for _, item := range removedItems {
		removed = append(removed, item.ToSyncItem())
	}
	appliedItems, err := install.apply(participating, selectedEntries)
	if err != nil {
		return nil, err
	}
	applied := make([]SkillSyncItem, 0, len(appliedItems))
	for _, item := range appliedItems {
		applied = append(applied, item.ToSyncItem())
	}

	return &SourceSyncResult{Removed: removed, Applied: applied, Warnings: warnings}, nil
}

func sourceSyncSnapshot(store *WorkspaceConfigStore, source *ResolvedSkillSourceDefinition, input *SourceSyncInput) (string, []DiscoveredSkill, error) {
	if input.SourceRoot != nil && input.Skills != nil {
		sourceRoot, err := validateSyncSnapshotRoot(store, source, *input.SourceRoot)
		if err != nil {
			return "", nil, err
		}
		discovered := make([]DiscoveredSkill, 0, len(input.Skills))
		for i := range input.Skills {
			skill, err := syncSkillOptionToDiscovered(sourceRoot, &input.Skills[i])
			if err != nil {
				return "", nil, err
			}
			discovered = append(discovered, skill)
		}
		return sourceRoot, discovered, nil
	}
	// 同步命令带着弹窗快照走到这里时没有网络拉取可取消。
	return discoverSourceSkillsRaw(context.Background(), store, source)
}

func PreviewSourceSyncConflictsInner(store *WorkspaceConfigStore, input SourceSyncInput) ([]SourceSyncConflict, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	sourceDefinition, err := sourceDefinitionFromConfig(config, input.SourceID)
	if err != nil {
		return nil, err
	}
	sourceRoot, discovered, err := sourceSyncSnapshot(store, sourceDefinition, &input)
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, path := range input.SkillPaths {
		selected[path] = true
	}
	selectedSkills := make([]DiscoveredSkill, 0)
	for _, skill := range discovered {
		if selected[skill.RelativePath] {
			selectedSkills = append(selectedSkills, skill)
		}
	}

	return sourceSyncConflicts(config, sourceRoot, selectedSkills, input.TargetIDs)
}

func sourceSyncConflicts(config *ResolvedManagerConfig, sourceRoot string, selectedSkills []DiscoveredSkill, targetIDs []string) ([]SourceSyncConflict, error) {
	// 与同步共用同一套 skip 判定与落点分类：预演会报的，同步才会覆盖。
	resolved, err := resolveIDs(config, targetIDs)
	if err != nil {
		return nil, err
	}
	participating := make([]*managedTarget, 0)
	for i := range resolved {
		if resolved[i].skip == nil {
			participating = append(participating, &resolved[i])
		}
	}
	selected := make([]selectedSkill, 0, len(selectedSkills))
	for _, skill := range selectedSkills {
		selected = append(selected, selectedSkill{name: skill.Name, relativePath: skill.RelativePath})
	}

	installConflicts, err := newSkillInstall(sourceRoot).conflicts(participating, selected)
	if err != nil {
		return nil, err
	}
	conflicts := make([]SourceSyncConflict, 0, len(installConflicts))
	for _, conflict := range installConflicts {
		conflicts = append(conflicts, SourceSyncConflict{
			SkillName:       conflict.skillName,
			TargetID:        AgentTargetID(conflict.targetID),
			SourcePath:      support.DisplayPath(conflict.sourcePath),
			DestinationPath: support.DisplayPath(conflict.destinationPath),
			ExistingKind:    conflict.existingKind,
			Detail:          conflict.detail,
		})
	}
	return conflicts, nil
}

func validateSyncSnapshotRoot(store *WorkspaceConfigStore, source *ResolvedSkillSourceDefinition, sourceRoot string) (string, error) {
	trimmed := strings.TrimSpace(sourceRoot)
	if trimmed == "" {
		return "", errors.New("同步来源路径不能为空。")
	}

	expectedRoot, err := resolveSourceRoot(store, source)
	if err != nil {
		return "", err
	}
	normalizedExpected := expectedRoot
	if canonical, err := support.Canonicalize(expectedRoot); err == nil {
		normalizedExpected = canonical
	}
	normalizedSource, err := support.Canonicalize(trimmed)
	if err != nil {
		return "", fmt.Errorf("同步来源路径不存在：%s", support.DisplayPath(trimmed))
	}

	if _, ok := stripPathPrefix(normalizedSource, normalizedExpected); !ok {
		return "", fmt.Errorf("同步来源路径不属于当前来源：%s", support.DisplayPath(normalizedSource))
	}

	return normalizedSource, nil
}

// resolveSourceRoot：来源定义的本地根：local 用配置的根路径，git 用缓存
// 目录。
func resolveSourceRoot(store *WorkspaceConfigStore, source *ResolvedSkillSourceDefinition) (string, error) {
	switch source.Kind {
	case rawSourceKindLocal:
		return source.RootPath, nil
	case rawSourceKindGit:
		return store.GitCacheDirForRepo(source.Repo)
	}
	return "", fmt.Errorf("未知 skill source type：%s", source.Kind)
}

func syncSkillOptionToDiscovered(sourceRoot string, skill *SyncSkillOption) (DiscoveredSkill, error) {
	sourceDir, err := newSkillInstall(sourceRoot).sourceDir(skill.RelativePath)
	if err != nil {
		return DiscoveredSkill{}, err
	}
	return DiscoveredSkill{
		Name:          skill.Name,
		RelativePath:  skill.RelativePath,
		SkillFilePath: support.DisplayPath(joinPath(sourceDir, "SKILL.md")),
		SourcePath:    strPtr(support.DisplayPath(sourceDir)),
		Description:   skill.Description,
	}, nil
}

// RemoveSourceSyncInner：remove sync 动作：只清该来源在被选 target 上的
// 足迹，其他 target 不动。
func RemoveSourceSyncInner(store *WorkspaceConfigStore, sourceID string, targetIDs []string) (*SourceSyncResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	targetIDFilter := map[string]bool{}
	for _, id := range targetIDs {
		targetIDFilter[id] = true
	}
	removed, err := removeSourceSymlinksFromTargets(store, config, sourceID, &targetIDFilter)
	if err != nil {
		return nil, err
	}
	return &SourceSyncResult{Removed: removed, Applied: []SkillSyncItem{}, Warnings: []string{}}, nil
}

// ---------------------------------------------------------------------------
// Skill discovery（WalkDir + include patterns）
// ---------------------------------------------------------------------------

// skillDescriptionMaxChars：备注只帮用户在列表里认出 skill；超长描述在
// tooltip 里也不可读，硬截断。
const skillDescriptionMaxChars = 500

// SkillDescriptionFromContents：SKILL.md frontmatter 的 description。只解析
// --- 围栏内的 YAML，不引 markdown 解析器；任何一步失败都按无描述处理——
// 备注缺失不该挡住 discovery。
func SkillDescriptionFromContents(contents string) *string {
	if !strings.HasPrefix(strings.TrimLeft(contents, " \t\r\n"), "---") {
		return nil
	}
	body := strings.TrimLeft(contents, " \t\r\n")[3:]
	lines := strings.Split(body, "\n")
	end := -1
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if trimmed == "---" || trimmed == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return nil
	}
	var frontMatter any
	if err := yaml.Unmarshal([]byte(strings.Join(lines[:end], "\n")), &frontMatter); err != nil {
		return nil
	}
	mapping, ok := frontMatter.(map[string]any)
	if !ok {
		return nil
	}
	description, ok := mapping["description"].(string)
	if !ok {
		return nil
	}
	// 折叠 YAML 多行标量的换行与连续空白，备注按单行展示。
	singleLine := strings.Join(strings.Fields(description), " ")
	if singleLine == "" {
		return nil
	}
	runes := []rune(singleLine)
	if len(runes) > skillDescriptionMaxChars {
		singleLine = string(runes[:skillDescriptionMaxChars])
	}
	return &singleLine
}

func skillDescriptionOf(skillFile string) *string {
	content, err := os.ReadFile(skillFile)
	if err != nil {
		return nil
	}
	return SkillDescriptionFromContents(string(content))
}

// DiscoverSkillsInDirectory：来源根下的全部 skill（SKILL.md 所在目录），
// 按 relative_path 排序。符号链接不跟随（与 walkdir 口径一致）。
func DiscoverSkillsInDirectory(root string) ([]DiscoveredSkill, error) {
	skills := []DiscoveredSkill{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // 静默跳过失败条目（walkdir filter_map ok）
		}
		if !entry.Type().IsRegular() || entry.Name() != "SKILL.md" {
			return nil
		}
		parent := filepath.Dir(path)
		// strip_prefix 语义：等于 root 时是空串（不是 "."），不在 root 下时
		// 回退 parent 本身。
		relativePath, relativeOK := stripPathPrefix(parent, root)
		if !relativeOK {
			relativePath = parent
		}
		name := filepath.Base(parent)
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = "skill"
		}
		skills = append(skills, DiscoveredSkill{
			Name:          name,
			RelativePath:  relativePath,
			SkillFilePath: support.DisplayPath(path),
			SourcePath:    strPtr(support.DisplayPath(parent)),
			Description:   skillDescriptionOf(path),
		})
		return nil
	})

	sort.SliceStable(skills, func(i, j int) bool {
		return skills[i].RelativePath < skills[j].RelativePath
	})
	return skills, nil
}

func sourceDefinitionFromConfig(config *ResolvedManagerConfig, sourceID string) (*ResolvedSkillSourceDefinition, error) {
	source, ok := config.SkillSources[sourceID]
	if !ok {
		return nil, fmt.Errorf("未找到来源：%s", sourceID)
	}
	return source, nil
}

// ---------------------------------------------------------------------------
// Skill discovery commands（import 弹窗的预览链路）
// ---------------------------------------------------------------------------

// DiscoverGitSkillsInner：git 来源发现（clone/pull 到持久缓存再扫描）。
func DiscoverGitSkillsInner(store *WorkspaceConfigStore, state *SkillDiscoveryState, repo string, reference *string) (*SkillDiscoveryResultView, error) {
	// 导入弹窗尚未接入取消机制；走 Background 即不可取消。
	cache, err := cloneGitRepoToCache(context.Background(), store, repo, reference)
	if err != nil {
		return nil, err
	}
	discoveryID := mustNewUUID()
	skills, err := DiscoverSkillsInDirectory(cache.CacheDir)
	if err != nil {
		return nil, err
	}

	state.Store(discoveryID, &SkillDiscoverySnapshot{
		Source: DiscoverySourceDefinition{
			Kind: rawSourceKindGit,
			Repo: repo,
			Ref:  cloneStrPtr(reference),
		},
		Skills: skills,
	})

	return &SkillDiscoveryResultView{
		DiscoveryID:         discoveryID,
		Repo:                repo,
		Ref:                 cloneStrPtr(reference),
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
		Skills:              skills,
		ExcludedSkills:      []DiscoveredSkill{},
	}, nil
}

// DiscoverLocalSkillsInner：本地目录来源发现。
func DiscoverLocalSkillsInner(state *SkillDiscoveryState, sourcePath string) (*SkillDiscoveryResultView, error) {
	sourceRoot, err := expandPath(sourcePath, "")
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(sourceRoot); err != nil {
		return nil, fmt.Errorf("目录不存在：%s", support.DisplayPath(sourceRoot))
	}

	discoveryID := mustNewUUID()
	skills, err := DiscoverSkillsInDirectory(sourceRoot)
	if err != nil {
		return nil, err
	}

	state.Store(discoveryID, &SkillDiscoverySnapshot{
		Source: DiscoverySourceDefinition{Kind: rawSourceKindLocal, RootPath: sourceRoot},
		Skills: skills,
	})

	return &SkillDiscoveryResultView{
		DiscoveryID:         discoveryID,
		Repo:                support.DisplayPath(sourceRoot),
		Ref:                 nil,
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
		Skills:              skills,
		ExcludedSkills:      []DiscoveredSkill{},
	}, nil
}

// FilterDiscoveredSkillsInner：在快照上试 include patterns，命中与排除各
// 返回一份，导入预览把被排除的也展示出来。
func FilterDiscoveredSkillsInner(
	state *SkillDiscoveryState,
	discoveryID string,
	includeNamePatterns []string,
	includePathPatterns []string,
) (*SkillDiscoveryResultView, error) {
	snapshot, ok := state.Load(discoveryID)
	if !ok {
		return nil, fmt.Errorf("未找到 discovery：%s", discoveryID)
	}
	namePatterns := normalizePatterns(includeNamePatterns)
	pathPatterns := normalizePatterns(includePathPatterns)
	allSkills := snapshot.Skills
	matched := make([]DiscoveredSkill, len(allSkills))
	copy(matched, allSkills)
	ApplyIncludePatterns(&matched, namePatterns, pathPatterns)
	excludedSkills := partitionExcluded(allSkills, matched)

	return &SkillDiscoveryResultView{
		DiscoveryID:         discoveryID,
		Repo:                discoverySourceLabel(&snapshot.Source),
		Ref:                 discoverySourceRef(&snapshot.Source),
		IncludeNamePatterns: namePatterns,
		IncludePathPatterns: pathPatterns,
		Skills:              matched,
		ExcludedSkills:      excludedSkills,
	}, nil
}

// partitionExcluded：apply_include_patterns 之后，all 里没进 kept 的就是
// 被排除的集合。
func partitionExcluded(all []DiscoveredSkill, kept []DiscoveredSkill) []DiscoveredSkill {
	keptPaths := map[string]bool{}
	for _, skill := range kept {
		keptPaths[skill.RelativePath] = true
	}
	excluded := []DiscoveredSkill{}
	for _, skill := range all {
		if !keptPaths[skill.RelativePath] {
			excluded = append(excluded, skill)
		}
	}
	return excluded
}

// ImportDiscoveredSkillsInner：把发现结果落成 config 里的来源定义（没有
// 技能注册表）；目录链接在之后的 sync_source_to_targets 里创建。
func ImportDiscoveredSkillsInner(
	store *WorkspaceConfigStore,
	state *SkillDiscoveryState,
	discoveryID string,
	description *string,
	includeNamePatterns []string,
	includePathPatterns []string,
) (*SkillDiscoveryResultView, error) {
	snapshot, ok := state.Load(discoveryID)
	if !ok {
		return nil, fmt.Errorf("未找到 discovery：%s", discoveryID)
	}
	sourceID := mustNewUUID()
	namePatterns := normalizePatterns(includeNamePatterns)
	pathPatterns := normalizePatterns(includePathPatterns)
	// 描述与批量导入同口径：trim 后为空按未填写处理。
	normalizedDescription := normalizeSourceDescription(description)
	discovered := make([]DiscoveredSkill, len(snapshot.Skills))
	copy(discovered, snapshot.Skills)
	ApplyIncludePatterns(&discovered, namePatterns, pathPatterns)

	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		if err := ensureSkillSource(rawConfig, &RawSkillSourceConfig{
			ID:     sourceID,
			Source: discoverySourceToRawDefinition(&snapshot.Source, normalizedDescription, namePatterns, pathPatterns),
		}); err != nil {
			return err
		}

		return lock.WriteRaw(rawConfig)
	})
	if err != nil {
		return nil, err
	}
	state.Remove(discoveryID)

	return &SkillDiscoveryResultView{
		DiscoveryID:         discoveryID,
		Repo:                discoverySourceLabel(&snapshot.Source),
		Ref:                 discoverySourceRef(&snapshot.Source),
		IncludeNamePatterns: namePatterns,
		IncludePathPatterns: pathPatterns,
		Skills:              discovered,
		ExcludedSkills:      []DiscoveredSkill{},
	}, nil
}

// ParseBatchGitSkillImportSources：批量导入 JSON → 校验并归一的来源列表。
func ParseBatchGitSkillImportSources(jsonContent string) ([]BatchGitSkillImportSourceInput, error) {
	trimmed := strings.TrimSpace(jsonContent)
	if trimmed == "" {
		return nil, errors.New("批量导入内容不能为空。")
	}

	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	// serde deny_unknown_fields 的等价物：未知字段报错。
	decoder.DisallowUnknownFields()
	var sources []BatchGitSkillImportSourceInput
	if err := decoder.Decode(&sources); err != nil {
		return nil, fmt.Errorf("解析批量导入 JSON 失败：%v", err)
	}

	if len(sources) == 0 {
		return nil, errors.New("批量导入内容至少需要 1 个 git 来源。")
	}

	for index := range sources {
		source := &sources[index]
		sourceNumber := index + 1
		repo := strings.TrimSpace(source.Repo)
		reference := strings.TrimSpace(source.Ref)

		if repo == "" {
			return nil, fmt.Errorf("第 %d 个来源缺少 repo。", sourceNumber)
		}
		if reference == "" {
			return nil, fmt.Errorf("第 %d 个来源缺少 ref。", sourceNumber)
		}
		for _, pattern := range append(append([]string{}, source.IncludeNamePatterns...), source.IncludePathPatterns...) {
			if strings.TrimSpace(pattern) == "" {
				return nil, fmt.Errorf("第 %d 个来源的 include patterns 不能包含空字符串。", sourceNumber)
			}
		}

		source.Repo = repo
		source.Ref = reference
		// 描述与 repo/ref 同口径：trim 后为空按未填写处理。
		source.Description = normalizeSourceDescription(source.Description)
		source.IncludeNamePatterns = normalizePatterns(source.IncludeNamePatterns)
		source.IncludePathPatterns = normalizePatterns(source.IncludePathPatterns)
	}

	return sources, nil
}

// ImportBatchGitSkillsInner：批量导入 git 来源：逐个 clone + 扫描 + 写入
// config；单条失败记录 error 继续其余条目，最后一次性落盘。
func ImportBatchGitSkillsInner(store *WorkspaceConfigStore, input BatchGitSkillImportInput) (*BatchGitSkillImportResult, error) {
	sources, err := ParseBatchGitSkillImportSources(input.JSONContent)
	if err != nil {
		return nil, err
	}

	var result *BatchGitSkillImportResult
	err = store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}
		items := make([]BatchGitSkillImportItemResult, 0, len(sources))

		for i := range sources {
			source := &sources[i]
			if item, err := importGitSource(store, rawConfig, source); err == nil {
				items = append(items, item)
			} else {
				items = append(items, BatchGitSkillImportItemResult{
					Repo:                source.Repo,
					Ref:                 strPtr(source.Ref),
					IncludeNamePatterns: source.IncludeNamePatterns,
					IncludePathPatterns: source.IncludePathPatterns,
					ImportedCount:       0,
					SkillNames:          []string{},
					Error:               strPtr(err.Error()),
				})
			}
		}

		if err := lock.WriteRaw(rawConfig); err != nil {
			return err
		}

		importedCount := 0
		failedCount := 0
		for _, item := range items {
			importedCount += item.ImportedCount
			if item.Error != nil {
				failedCount++
			}
		}

		result = &BatchGitSkillImportResult{
			ImportedCount:  importedCount,
			SucceededCount: len(items) - failedCount,
			FailedCount:    failedCount,
			Items:          items,
		}
		return nil
	})
	return result, err
}

func importGitSource(store *WorkspaceConfigStore, rawConfig *RawManagerConfig, source *BatchGitSkillImportSourceInput) (BatchGitSkillImportItemResult, error) {
	namePatterns := source.IncludeNamePatterns
	pathPatterns := source.IncludePathPatterns
	// 批量导入尚未接入取消机制；走 Background 即不可取消。
	ref := source.Ref
	cache, err := cloneGitRepoToCache(context.Background(), store, source.Repo, &ref)
	if err != nil {
		return BatchGitSkillImportItemResult{}, err
	}
	discovered, err := DiscoverSkillsInDirectory(cache.CacheDir)
	if err != nil {
		return BatchGitSkillImportItemResult{}, err
	}
	ApplyIncludePatterns(&discovered, namePatterns, pathPatterns)
	sourceID := mustNewUUID()
	skillNames := make([]string, 0, len(discovered))
	for _, skill := range discovered {
		skillNames = append(skillNames, skill.Name)
	}

	refCopy := source.Ref
	descriptionCopy := source.Description
	nameCopy := cloneStrings(namePatterns)
	pathCopy := cloneStrings(pathPatterns)
	if err := ensureSkillSource(rawConfig, &RawSkillSourceConfig{
		ID: sourceID,
		Source: RawSkillSourceDefinition{
			Type:                rawSourceKindGit,
			Repo:                source.Repo,
			Ref:                 &refCopy,
			Description:         descriptionCopy,
			IncludeNamePatterns: nameCopy,
			IncludePathPatterns: pathCopy,
		},
	}); err != nil {
		return BatchGitSkillImportItemResult{}, err
	}

	return BatchGitSkillImportItemResult{
		Repo:                source.Repo,
		Ref:                 &refCopy,
		IncludeNamePatterns: namePatterns,
		IncludePathPatterns: pathPatterns,
		ImportedCount:       len(discovered),
		SkillNames:          skillNames,
		Error:               nil,
	}, nil
}

// ---------------------------------------------------------------------------
// Remove directory links pointing at skills of `source_id` from every target.
// Only links are touched — real dirs/files are user-managed.
// ---------------------------------------------------------------------------

// removeSourceSymlinksFromTargets：删掉各 target 上指向 `source_id` 技能的
// 目录链接并逐条收集。targetIDs = nil 表示全部 target；否则只清列出的。
func removeSourceSymlinksFromTargets(
	store *WorkspaceConfigStore,
	config *ResolvedManagerConfig,
	sourceID string,
	targetIDs *map[string]bool,
) ([]SkillSyncItem, error) {
	sourceDefinition, err := sourceDefinitionFromConfig(config, sourceID)
	if err != nil {
		return nil, err
	}
	sourceRoot, err := resolveSourceRoot(store, sourceDefinition)
	if err != nil {
		return nil, err
	}
	return removeSourceSymlinksWithRoot(config, sourceRoot, targetIDs)
}

// removeSourceSymlinksWithRoot：无 store 的卸载入口：来源根已知时直接清理
// （测试与已解析配置的调用方用）。
func removeSourceSymlinksWithRoot(config *ResolvedManagerConfig, sourceRoot string, targetIDs *map[string]bool) ([]SkillSyncItem, error) {
	targets, err := managedTargets(config)
	if err != nil {
		return nil, err
	}
	participating := make([]*managedTarget, 0)
	for i := range targets {
		target := &targets[i]
		if target.skip != nil {
			continue
		}
		if targetIDs != nil && !(*targetIDs)[target.id] {
			continue
		}
		participating = append(participating, target)
	}

	items, err := newSkillInstall(sourceRoot).removeInstalled(participating)
	if err != nil {
		return nil, err
	}
	removed := make([]SkillSyncItem, 0, len(items))
	for _, item := range items {
		removed = append(removed, item.ToSyncItem())
	}
	return removed, nil
}

// RemoveTargetSkillLinkInner：移除单个 target 上的目录链接，用于同步弹窗里
// 逐条清理非受管控条目。只删链接本身：先确认目标确实是目录链接，再删，
// 真实目录/文件一律拒绝。
func RemoveTargetSkillLinkInner(store *WorkspaceConfigStore, targetID string, destinationPath string) (*SkillSyncItem, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	target := config.targetByID(targetID)
	if target == nil {
		return nil, targetUnavailable(targetID)
	}

	// 链接归属校验与删除都在安装面：真实目录/文件一律拒绝。
	removed, err := newSkillInstall(target.SkillDir).removeAt(targetID, target.SkillDir, destinationPath)
	if err != nil {
		return nil, err
	}

	// 落点是软链接时，删掉的是链接另一端目录里的条目，而这条记录挂着本
	// target 的名字。穿链接本身是正常语义，但称呼必须说清，否则「删了自己的」
	// 这个印象是错的。
	linked, err := readSkillDirLink(target.SkillDir)
	if err != nil {
		return nil, err
	}
	if linked != nil {
		note := fmt.Sprintf("实际删除的是 %s 里的条目", support.DisplayPath(*linked))
		if removed.detail != nil {
			merged := fmt.Sprintf("%s；%s", *removed.detail, note)
			removed.detail = &merged
		} else {
			removed.detail = &note
		}
	}
	item := removed.ToSyncItem()
	return &item, nil
}

// ---------------------------------------------------------------------------
// Delete skill source
// ---------------------------------------------------------------------------

// UpdateSkillSourceInner：只更新 source 配置；已有 target 软链接在用户下次
// 同步时才按新规则调整。
func UpdateSkillSourceInner(store *WorkspaceConfigStore, input SkillSourceMutationInput) (*WorkspaceState, error) {
	err := store.Locked(func(lock *ConfigLock) error {
		rawConfig, err := lock.ParseRaw()
		if err != nil {
			return err
		}

		var source *RawSkillSourceConfig
		for _, item := range rawConfig.SkillSources {
			if item.ID == input.SourceID {
				source = item
				break
			}
		}
		if source == nil {
			return fmt.Errorf("未找到来源：%s", input.SourceID)
		}

		namePatterns := normalizePatterns(input.IncludeNamePatterns)
		pathPatterns := normalizePatterns(input.IncludePathPatterns)

		// 描述是编辑弹窗的全量回写：nil 就是用户清空。
		source.Source.Description = normalizeSourceDescription(input.Description)
		source.Source.IncludeNamePatterns = namePatterns
		source.Source.IncludePathPatterns = pathPatterns
		if source.Source.Type == rawSourceKindGit && input.Ref != nil {
			ref := *input.Ref
			source.Source.Ref = &ref
		}

		return lock.WriteRaw(rawConfig)
	})
	if err != nil {
		return nil, err
	}

	return WorkspaceStateInner(store)
}

// DeleteSkillSourceInner：删除单个来源（配置项 + 软链接足迹 + git 缓存）。
func DeleteSkillSourceInner(store *WorkspaceConfigStore, sourceID string) (*WorkspaceState, error) {
	err := store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		var rawConfig RawManagerConfig
		if err := yaml.Unmarshal([]byte(rawContent), &rawConfig); err != nil {
			return err
		}
		if err := deleteSkillSourceEntries(store, lock.ConfigPath(), rawContent, &rawConfig, sourceID, lock.Env()); err != nil {
			return err
		}
		return lock.WriteRaw(&rawConfig)
	})
	if err != nil {
		return nil, err
	}

	return WorkspaceStateInner(store)
}

// deleteSkillSourceEntries：从内存 raw 文档删一个来源：存在性检查、软链接
// 清理、git 缓存清理与配置项本身。单删与批删共用；只能在 store 锁内调用。
func deleteSkillSourceEntries(
	store *WorkspaceConfigStore,
	configPath string,
	rawContent string,
	rawConfig *RawManagerConfig,
	sourceID string,
	env *support.Environment,
) error {
	found := false
	for _, source := range rawConfig.SkillSources {
		if source.ID == sourceID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("未找到来源：%s", sourceID)
	}

	// Remove softlinks for this source's skills from every target.
	resolvedConfig, err := parseManagerConfig(rawContent, configPath, env)
	if err != nil {
		return err
	}
	if _, err := removeSourceSymlinksFromTargets(store, resolvedConfig, sourceID, nil); err != nil {
		return err
	}

	// Remove git cache if this is a git source AND no other source references
	// the same repo (cache is now keyed by owner/repo and shared across refs).
	for _, source := range rawConfig.SkillSources {
		if source.ID != sourceID || source.Source.Type != rawSourceKindGit {
			continue
		}
		repo := source.Source.Repo
		stillReferenced := false
		for _, other := range rawConfig.SkillSources {
			if other.ID != sourceID && other.Source.Type == rawSourceKindGit && other.Source.Repo == repo {
				stillReferenced = true
				break
			}
		}
		if !stillReferenced {
			cache, err := store.GitCacheDirForRepo(repo)
			if err != nil {
				return err
			}
			if err := removeExistingPath(cache); err != nil {
				return err
			}
			parent := filepath.Dir(cache)
			removeEmptyParentDirs(store.GitCacheRoot(), &parent)
		}
	}

	kept := rawConfig.SkillSources[:0]
	for _, source := range rawConfig.SkillSources {
		if source.ID != sourceID {
			kept = append(kept, source)
		}
	}
	rawConfig.SkillSources = kept

	return nil
}

// DeleteSkillSourcesInner：批量删除。前端逐条发请求批量删，这里的单次
// read-modify-write 避免与并发命令互相竞争、也避免把文件重写 N 次。
func DeleteSkillSourcesInner(store *WorkspaceConfigStore, sourceIDs []string) error {
	if len(sourceIDs) == 0 {
		return errors.New("source_ids 不能为空")
	}

	return store.Locked(func(lock *ConfigLock) error {
		rawContent, err := lock.ReadRaw()
		if err != nil {
			return err
		}
		var rawConfig RawManagerConfig
		if err := yaml.Unmarshal([]byte(rawContent), &rawConfig); err != nil {
			return err
		}

		for _, sourceID := range sourceIDs {
			if err := deleteSkillSourceEntries(store, lock.ConfigPath(), rawContent, &rawConfig, sourceID, lock.Env()); err != nil {
				return err
			}
		}

		return lock.WriteRaw(&rawConfig)
	})
}

// ensureSkillSource：同 id 来源重复导入时按序列化形态比对：完全一致视为
// 幂等成功，不一致报冲突（serde 比对序列化串的语义）。
func ensureSkillSource(config *RawManagerConfig, source *RawSkillSourceConfig) error {
	for _, existing := range config.SkillSources {
		if existing.ID != source.ID {
			continue
		}
		existingRaw, err := yaml.Marshal(existing)
		if err != nil {
			return err
		}
		nextRaw, err := yaml.Marshal(source)
		if err != nil {
			return err
		}
		if string(existingRaw) != string(nextRaw) {
			return fmt.Errorf("skill source 配置冲突：%s", source.ID)
		}
		return nil
	}

	config.SkillSources = append(config.SkillSources, source)
	return nil
}

// ---------------------------------------------------------------------------
// Git cache —— clone into persistent <config>/reins/git/<owner>/<repo>/.
// ---------------------------------------------------------------------------

const (
	gitAutoRefreshInterval = 24 * time.Hour
	gitCacheRefreshMarker  = "reins-last-fetch"
)

type gitCacheRefreshPolicy int

const (
	refreshIfStale gitCacheRefreshPolicy = iota
	refreshForce
)

func discoverySourceLabel(source *DiscoverySourceDefinition) string {
	switch source.Kind {
	case rawSourceKindLocal:
		return support.DisplayPath(source.RootPath)
	default:
		return source.Repo
	}
}

func discoverySourceRef(source *DiscoverySourceDefinition) *string {
	if source.Kind == rawSourceKindGit {
		return cloneStrPtr(source.Ref)
	}
	return nil
}

func discoverySourceToRawDefinition(
	source *DiscoverySourceDefinition,
	description *string,
	includeNamePatterns []string,
	includePathPatterns []string,
) RawSkillSourceDefinition {
	if source.Kind == rawSourceKindLocal {
		return RawSkillSourceDefinition{
			Type:                rawSourceKindLocal,
			RootPath:            support.DisplayPath(source.RootPath),
			Description:         description,
			IncludeNamePatterns: includeNamePatterns,
			IncludePathPatterns: includePathPatterns,
		}
	}
	return RawSkillSourceDefinition{
		Type:                rawSourceKindGit,
		Repo:                source.Repo,
		Ref:                 cloneStrPtr(source.Ref),
		Description:         description,
		IncludeNamePatterns: includeNamePatterns,
		IncludePathPatterns: includePathPatterns,
	}
}

// gitCacheRefresh：一次缓存解析的结果：CacheDir 指向可用的本地缓存；
// FetchCancelled 说明远端刷新被取消、缓存停留在旧内容（marker 未写，下次
// 仍会重试远端）。
type gitCacheRefresh struct {
	CacheDir       string
	FetchCancelled bool
}

// cloneGitRepoToCache：常规发现的缓存解析。已有缓存只在间隔到期后刷新，
// 打开工作区保持本地。刷新被取消时退回现有缓存（哪怕过期）：本地数据是
// 真实的，只是可能落后。
func cloneGitRepoToCache(ctx context.Context, store *WorkspaceConfigStore, repo string, reference *string) (*gitCacheRefresh, error) {
	return refreshGitRepoCache(ctx, store, repo, reference, refreshIfStale)
}

// RefreshGitSkillSourceInner：强制拉取。首次克隆被取消：没有本地缓存可用，
// 但对「强制拉取」本身而言就是未完成而已，如实报取消而不是错误。
func RefreshGitSkillSourceInner(ctx context.Context, store *WorkspaceConfigStore, sourceID string) (*SkillSourceRefreshResult, error) {
	config, err := store.Parse()
	if err != nil {
		return nil, err
	}
	source, err := sourceDefinitionFromConfig(config, sourceID)
	if err != nil {
		return nil, err
	}
	if source.Kind != rawSourceKindGit {
		return nil, fmt.Errorf("来源不是 git：%s", sourceID)
	}

	refreshed, err := refreshGitRepoCache(ctx, store, source.Repo, source.Ref, refreshForce)
	if err != nil {
		// 首次克隆被取消：没有本地缓存可用，但对「强制拉取」本身而言就是
		// 未完成而已，如实报取消而不是错误。
		if errors.Is(err, context.Canceled) {
			return &SkillSourceRefreshResult{Cancelled: true}, nil
		}
		return nil, err
	}

	return &SkillSourceRefreshResult{Cancelled: refreshed.FetchCancelled}, nil
}

func refreshGitRepoCache(
	ctx context.Context,
	store *WorkspaceConfigStore,
	repo string,
	reference *string,
	policy gitCacheRefreshPolicy,
) (*gitCacheRefresh, error) {
	cacheDir, err := store.GitCacheDirForRepo(repo)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(store.GitCacheRoot(), 0o777); err != nil {
		return nil, err
	}

	// A path that already exists but isn't a git worktree would break checkout,
	// so wipe it and fall through to a clean clone.
	gitDir := joinPath(cacheDir, ".git")
	if pathExistsFollow(cacheDir) && !pathExistsFollow(gitDir) {
		if err := removeExistingPath(cacheDir); err != nil {
			return nil, err
		}
	}

	if pathExistsFollow(cacheDir) {
		markerPath := gitCacheRefreshMarkerPath(cacheDir)
		var recordedReference *string
		if content, err := os.ReadFile(markerPath); err == nil {
			text := string(content)
			recordedReference = &text
		}
		var lastRefreshedAt *time.Time
		if metadata, err := os.Stat(markerPath); err == nil {
			modified := metadata.ModTime()
			lastRefreshedAt = &modified
		}
		shouldRefresh := policy == refreshForce
		if !shouldRefresh {
			shouldRefresh = GitCacheRefreshIsDue(recordedReference, reference, lastRefreshedAt, time.Now())
		}

		if shouldRefresh {
			if err := updateGitCache(ctx, cacheDir, reference); err != nil {
				if errors.Is(err, context.Canceled) {
					// 取消不是失败：缓存原样可用，marker 不写（保持过期，下次重试）。
					return &gitCacheRefresh{CacheDir: cacheDir, FetchCancelled: true}, nil
				}
				return nil, err
			}
			if err := writeGitCacheRefreshMarker(cacheDir, reference); err != nil {
				return nil, err
			}
		}
		return &gitCacheRefresh{CacheDir: cacheDir, FetchCancelled: false}, nil
	}

	// Fresh clone. 取消后 git 留下的半成品会让下一次更新当成已有仓库去
	// fetch，必须清掉才能恢复干净重试。
	cloneArgs := []string{"clone"}
	if reference != nil && strings.TrimSpace(*reference) != "" {
		cloneArgs = append(cloneArgs, "--branch", strings.TrimSpace(*reference))
	}
	cloneArgs = append(cloneArgs, repo, cacheDir)
	cloneCmd := exec.CommandContext(ctx, "git", cloneArgs...)
	if err := runCommand(ctx, cloneCmd, "拉取 git 仓库失败"); err != nil {
		if errors.Is(err, context.Canceled) {
			if cleanupErr := removeExistingPath(cacheDir); cleanupErr != nil {
				return nil, fmt.Errorf("已取消拉取，且清理未完成的仓库缓存失败：%w", cleanupErr)
			}
			return nil, fmt.Errorf("已取消拉取，本地没有可用的仓库缓存：%w", err)
		}
		return nil, err
	}
	if err := writeGitCacheRefreshMarker(cacheDir, reference); err != nil {
		return nil, err
	}

	return &gitCacheRefresh{CacheDir: cacheDir, FetchCancelled: false}, nil
}

func gitCacheRefreshMarkerPath(cacheDir string) string {
	return joinPath(joinPath(cacheDir, ".git"), gitCacheRefreshMarker)
}

// gitCacheLastFetchedAtMs：列表展示复用刷新 marker 的修改时间，避免为展示
// 状态增加另一份持久化数据。
func gitCacheLastFetchedAtMs(store *WorkspaceConfigStore, repo string) *int64 {
	cacheDir, err := store.GitCacheDirForRepo(repo)
	if err != nil {
		return nil
	}
	metadata, err := os.Stat(gitCacheRefreshMarkerPath(cacheDir))
	if err != nil {
		return nil
	}
	modified := metadata.ModTime().UnixMilli()
	if modified < 0 {
		// Rust duration_since(UNIX_EPOCH) 失败按 None 处理。
		return nil
	}
	return &modified
}

func writeGitCacheRefreshMarker(cacheDir string, reference *string) error {
	markerPath := gitCacheRefreshMarkerPath(cacheDir)
	content := ""
	if reference != nil {
		content = strings.TrimSpace(*reference)
	}
	if err := os.WriteFile(markerPath, []byte(content), 0o666); err != nil {
		return fmt.Errorf("记录 git 仓库更新时间失败：%s", support.DisplayPath(markerPath))
	}
	return nil
}

// GitCacheRefreshIsDue：ref 失配、无 marker、超期或时钟回拨都判定需要刷新。
func GitCacheRefreshIsDue(
	recordedReference *string,
	requestedReference *string,
	lastRefreshedAt *time.Time,
	now time.Time,
) bool {
	requested := ""
	if requestedReference != nil {
		requested = strings.TrimSpace(*requestedReference)
	}
	recorded := ""
	if recordedReference != nil {
		recorded = strings.TrimSpace(*recordedReference)
	}
	if recorded != requested {
		return true
	}
	if lastRefreshedAt == nil {
		return true
	}
	elapsed := now.Sub(*lastRefreshedAt)
	if elapsed < 0 {
		// 时钟回拨（Rust duration_since Err）。
		return true
	}
	return elapsed >= gitAutoRefreshInterval
}

// updateGitCache：bare fetch then checkout handles both branches and tags;
// if the ref is missing remotely the fetch/pull surfaces the git error to
// the user.
func updateGitCache(ctx context.Context, cacheDir string, reference *string) error {
	if err := runCommand(ctx, gitCommandIn(ctx, cacheDir, "fetch", "origin"), "更新 git 仓库失败"); err != nil {
		return err
	}

	if reference != nil && strings.TrimSpace(*reference) != "" {
		if err := runCommand(ctx, gitCommandIn(ctx, cacheDir, "checkout", strings.TrimSpace(*reference)), "切换 git 分支失败"); err != nil {
			return err
		}
	}

	return runCommand(ctx, gitCommandIn(ctx, cacheDir, "pull", "--ff-only"), "更新 git 仓库失败")
}

func gitCommandIn(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd
}

// ---------------------------------------------------------------------------
// include patterns
// ---------------------------------------------------------------------------

func matchesAnyPattern(value string, patterns []string) bool {
	for _, pattern := range patterns {
		if wildcardMatch(value, pattern) {
			return true
		}
	}
	return false
}

// matchesInclude：skill 同时通过 name 与 path 过滤才算命中；空 pattern 集
// 表示该维度不设限。
func matchesInclude(name string, path string, namePatterns []string, pathPatterns []string) bool {
	nameOK := len(namePatterns) == 0 || matchesAnyPattern(name, namePatterns)
	pathOK := len(pathPatterns) == 0 || matchesAnyPattern(path, pathPatterns)
	return nameOK && pathOK
}

// ApplyIncludePatterns：只保留命中 include 过滤的 skill。import/discovery
// 路径用它只持久化命中的 skill；同步弹窗不调它——它保留全部 skill 并按
// matched 标注分组。
func ApplyIncludePatterns(skills *[]DiscoveredSkill, namePatterns []string, pathPatterns []string) {
	if len(namePatterns) == 0 && len(pathPatterns) == 0 {
		return
	}
	kept := (*skills)[:0]
	for _, skill := range *skills {
		if matchesInclude(skill.Name, skill.RelativePath, namePatterns, pathPatterns) {
			kept = append(kept, skill)
		}
	}
	*skills = kept
}

// wildcardMatch：Rust 版逐字节算法的平移（'?' 匹配单字节、'*' 跨段），
// 多字节字符按 UTF-8 字节比较——与原实现完全一致。
func wildcardMatch(value string, pattern string) bool {
	valueBytes := []byte(value)
	patternBytes := []byte(pattern)
	valueIndex := 0
	patternIndex := 0
	starIndex := -1
	matchIndex := 0

	for valueIndex < len(valueBytes) {
		if patternIndex < len(patternBytes) && (patternBytes[patternIndex] == '?' || patternBytes[patternIndex] == valueBytes[valueIndex]) {
			valueIndex++
			patternIndex++
			continue
		}

		if patternIndex < len(patternBytes) && patternBytes[patternIndex] == '*' {
			starIndex = patternIndex
			patternIndex++
			matchIndex = valueIndex
			continue
		}

		if starIndex >= 0 {
			patternIndex = starIndex + 1
			matchIndex++
			valueIndex = matchIndex
			continue
		}

		return false
	}

	for patternIndex < len(patternBytes) && patternBytes[patternIndex] == '*' {
		patternIndex++
	}

	return patternIndex == len(patternBytes)
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// parseGitOwnerRepo：从常见 git URL 形态取 owner/repo（https、ssh、scp
// 风格、裸 owner/repo）。
func parseGitOwnerRepo(repo string) (string, string, bool) {
	trimmed := strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	if trimmed == "" {
		return "", "", false
	}

	// ssh scp-style: git@host:owner/repo
	if left, rest, found := strings.Cut(trimmed, ":"); found {
		// left looks like a user@host token (no scheme, no slash)
		if !strings.Contains(left, "://") && !strings.Contains(left, "/") {
			return splitOwnerRepo(rest)
		}
	}

	// scheme-style: https://host/owner/repo or ssh://git@host[:port]/owner/repo
	afterScheme := trimmed
	if _, rest, found := strings.Cut(trimmed, "://"); found {
		afterScheme = rest
	}
	return splitOwnerRepo(afterScheme)
}

// splitOwnerRepo：最后两段是 owner/repo。
func splitOwnerRepo(path string) (string, string, bool) {
	path = strings.TrimLeft(path, "/")
	var parts []string
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			parts = append(parts, segment)
		}
	}
	if len(parts) < 2 {
		return "", "", false
	}
	repo := parts[len(parts)-1]
	owner := parts[len(parts)-2]
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

func fileExists(path string) bool {
	metadata, err := os.Stat(path)
	return err == nil && metadata.Mode().IsRegular()
}

func strPtr(value string) *string { return &value }

func cloneStrPtr(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// mustNewUUID：discovery/source 的标识；crypto/rand 读取失败按 panic 处理
// （Rust Uuid::new_v4 同样不可能失败）。
func mustNewUUID() string {
	return uuid.NewString()
}
