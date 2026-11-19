// skill_install.go：skill 分发的「target 安装面」——把「一个 skill 装进一个
// target」的物理语义收在一处：目标是否参与分发、链接落在哪、落点上现在是
// 什么、装/卸的结果。安装、冲突预演、链接枚举、按来源卸载四条路径都经这里，
// skip 规则、落点计算、链接分类与路径规范化只有一份实现；各调用方只决定
// 「拿这些结果做什么文案」。盘上真实链接是安装状态的权威来源，config.yaml
// 只声明意图。
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"reins/internal/support"
)

// managedTarget：一个参与分发决策的 target。
type managedTarget struct {
	id string
	// target：目标不存在（SkipMissing）时没有配置可指，调用方必须先看 skip。
	target *ResolvedTargetConfig
	// skip：非 nil = 不参与安装与清理；枚举（UI 只读展示）仍需要它。
	skip *TargetSkip
}

// skillDir：只在 skip == nil（目标存在且参与分发）时调用。
func (m *managedTarget) skillDir() string {
	if m.target == nil {
		panic("跳过的目标不参与分发，调用方必须先看 skip")
	}
	return m.target.SkillDir
}

// resolveIDs：解析一批 target id（含项目复合 id），保持输入顺序：不存在的
// id 也占一位（SkipMissing），由调用方决定是警告还是忽略。同步与冲突预演
// 共用，才能保证「预演会报的问题」与「同步会跳过的目标」是同一批。
func resolveIDs(config *ResolvedManagerConfig, targetIDs []string) ([]managedTarget, error) {
	targets := make([]managedTarget, 0, len(targetIDs))
	for _, targetID := range targetIDs {
		reference := parseTargetID(targetID)
		skip, err := config.skipReason(reference)
		if err != nil {
			return nil, err
		}
		targets = append(targets, managedTarget{
			id:     targetID,
			target: config.target(reference),
			skip:   skip,
		})
	}
	return targets, nil
}

// managedTargets：配置里全部全局 target + 项目级 target；skip 已判定，
// 调用方只决定呈现。
func managedTargets(config *ResolvedManagerConfig) ([]managedTarget, error) {
	refs, err := config.managedRefs()
	if err != nil {
		return nil, err
	}
	targets := make([]managedTarget, 0, len(refs))
	for _, entry := range refs {
		targets = append(targets, managedTarget{
			id:     entry.reference.CompositeID(),
			target: entry.target,
			skip:   entry.skip,
		})
	}
	return targets, nil
}

// destinationOf：一个 skill 在 target 里的落点：skills 目录 + skill 名。
func destinationOf(target *managedTarget, skillName string) string {
	return joinPath(target.skillDir(), skillName)
}

// selectedSkill：本次分发选中的 skill：展示名 + 来源内的相对路径。
type selectedSkill struct {
	name         string
	relativePath string
}

// skillInstall：一个分发来源（来源根 + 该来源下的 skill）。
type skillInstall struct {
	root string
	// normalizedRoot：规范化后的来源根：判定「落点是否指进本来源」的同一
	// 口径，两侧都规范化才能让 /var → /private/var 这类系统软链接下的比对
	// 成立。
	normalizedRoot string
}

func newSkillInstall(root string) *skillInstall {
	return &skillInstall{root: root, normalizedRoot: normalizeLinkPath(root)}
}

// sourceDir：一个 skill 的源目录（规范化）：来源根 + 清单相对路径。
func (s *skillInstall) sourceDir(relativePath string) (string, error) {
	if relativePathEscapesRoot(relativePath) {
		return "", fmt.Errorf("skill 路径不能越过来源根目录：%s", relativePath)
	}

	normalizedRoot := s.root
	if canonical, err := support.Canonicalize(s.root); err == nil {
		normalizedRoot = canonical
	}
	sourceDir := joinPath(normalizedRoot, relativePath)
	normalizedSourceDir, err := support.Canonicalize(sourceDir)
	if err != nil {
		return "", fmt.Errorf("skill 来源目录不存在：%s", support.DisplayPath(sourceDir))
	}

	if _, ok := stripPathPrefix(normalizedSourceDir, normalizedRoot); !ok {
		return "", fmt.Errorf("skill 路径不属于来源根目录：%s", relativePath)
	}

	return normalizedSourceDir, nil
}

// relativePathEscapesRoot：相对路径里的组件只能是普通目录段；绝对路径、
// Windows 卷前缀与 '..' 都视为越界（Rust Component 判定的等价物）。
func relativePathEscapesRoot(relativePath string) bool {
	if filepath.IsAbs(relativePath) || filepath.VolumeName(relativePath) != "" {
		return true
	}
	for _, segment := range splitPathComponentAware(relativePath) {
		if segment == ".." {
			return true
		}
	}
	return false
}

// splitPathComponentAware：按平台分隔符拆段（Rust Path::components 的比较
// 口径；Windows 接受两种分隔符）。
func splitPathComponentAware(path string) []string {
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, "/", "\\")
	}
	return strings.Split(path, string(filepath.Separator))
}

// conflicts：预演：哪些落点会被非本来源路径覆盖。只有会真正阻断同步的
// 条目才产出。
func (s *skillInstall) conflicts(targets []*managedTarget, selected []selectedSkill) ([]installConflict, error) {
	var conflicts []installConflict
	for _, target := range targets {
		for _, skill := range selected {
			sourceDir, err := s.sourceDir(skill.relativePath)
			if err != nil {
				return nil, err
			}
			destination := destinationOf(target, skill.name)
			state, err := s.classify(sourceDir, destination)
			if err != nil {
				return nil, err
			}
			kind, detail, blocked := conflictOf(state)
			if !blocked {
				continue
			}
			conflicts = append(conflicts, installConflict{
				targetID:        target.id,
				skillName:       skill.name,
				sourcePath:      sourceDir,
				destinationPath: destination,
				existingKind:    kind,
				detail:          detail,
			})
		}
	}
	return conflicts, nil
}

// apply：安装：把选中的 skill 装进给定 target。已指向本来源的落点记为
// unchanged，由调用方决定提示；覆盖非本来源路径是调用方的明确选择。
func (s *skillInstall) apply(targets []*managedTarget, selected []selectedSkill) ([]installItem, error) {
	items := make([]installItem, 0)
	for _, target := range targets {
		for _, skill := range selected {
			sourceDir, err := s.sourceDir(skill.relativePath)
			if err != nil {
				return nil, err
			}
			destination := destinationOf(target, skill.name)
			state, err := s.classify(sourceDir, destination)
			if err != nil {
				return nil, err
			}

			if state.kind == stateLinkedHere {
				items = append(items, s.unchangedItem(skill, target.id, sourceDir, destination, state))
				continue
			}

			if _, err := os.Lstat(destination); err == nil {
				if err := removeExistingPath(destination); err != nil {
					return nil, err
				}
			}
			if parent := filepath.Dir(destination); parent != "" {
				if err := os.MkdirAll(parent, 0o777); err != nil {
					return nil, fmt.Errorf("创建目标目录失败：%s", parent)
				}
			}
			if err := createDirectorySymlink(sourceDir, destination); err != nil {
				return nil, err
			}
			items = append(items, s.installedItem(skill, target.id, sourceDir, destination, state))
		}
	}
	return items, nil
}

// removeInstalled：卸载：删掉所有指进本来源的链接（targets 只含参与分发的
// 目标）。
func (s *skillInstall) removeInstalled(targets []*managedTarget) ([]installItem, error) {
	removed := make([]installItem, 0)
	for _, target := range targets {
		links, err := enumerateLinks(target.skillDir())
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if !s.linksIntoSource(link.resolved) {
				continue
			}
			if err := removeExistingPath(link.destination); err != nil {
				return nil, err
			}
			removed = append(removed, removedItem(target.id, link.destination, link.resolved))
		}
	}
	return removed, nil
}

// removeAt：单个落点的卸载（同步弹窗逐条清理）：先确认它属于该 target 的
// 目录，再确认确实是链接；真实目录/文件一律拒绝。
func (s *skillInstall) removeAt(targetID string, skillDir string, destination string) (installItem, error) {
	// 只规范化父目录：对链接自身 canonicalize 会把链接解析到目标路径，
	// 无法再判断它位于哪个 skill_dir 下。
	parent := filepath.Dir(destination)
	if normalizeLinkPath(parent) != normalizeLinkPath(skillDir) {
		return installItem{}, fmt.Errorf("路径不属于该 target 的 skills 目录：%s", support.DisplayPath(destination))
	}

	resolved, err := readDirectoryLinkTarget(destination)
	if err != nil {
		return installItem{}, err
	}
	if resolved == nil {
		return installItem{}, fmt.Errorf("目标不是软链接，已跳过：%s", support.DisplayPath(destination))
	}
	if err := removeExistingPath(destination); err != nil {
		return installItem{}, err
	}

	return removedItem(targetID, destination, *resolved), nil
}

func (s *skillInstall) linksIntoSource(resolved string) bool {
	_, ok := relativeToSourceRoot(normalizeLinkPath(resolved), s.normalizedRoot)
	return ok
}

// 落点状态的可区分事实；「指向本来源」与「指向别处」必须分开，前者是幂等
// noop，后者是冲突。
const (
	stateUninstalled = iota
	stateLinkedHere
	stateLinkedOtherSkill
	stateLinkedElsewhere
	stateRealDirectory
	stateRealFile
	stateRealPath
)

type destinationState struct {
	kind int
	// kindLabel：落点形态的措辞（目录链接 / 软链接）。
	kindLabel string
	resolved  string
}

// classify：落点上现在是什么。create / unchanged / replace / conflict 四种
// 呈现都从这一个状态派生，调用方不再各自比较路径。
func (s *skillInstall) classify(sourceDir string, destination string) (destinationState, error) {
	metadata, err := os.Lstat(destination)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return destinationState{kind: stateUninstalled}, nil
		}
		return destinationState{}, fmt.Errorf("Failed to inspect %s: %w", destination, err)
	}

	if linked, linkErr := readDirectoryLinkTarget(destination); linkErr != nil {
		return destinationState{}, linkErr
	} else if linked != nil {
		return s.linkState(*linked, sourceDir, "目录链接"), nil
	}

	if metadata.Mode()&os.ModeSymlink != 0 {
		linkTarget, err := os.Readlink(destination)
		if err != nil {
			return destinationState{}, fmt.Errorf("读取 %s 的软链接失败：%v", destination, err)
		}
		resolved := resolveSymlinkTargetPathNoCanonicalize(destination, linkTarget)
		return s.linkState(resolved, sourceDir, "软链接"), nil
	}

	if metadata.IsDir() {
		return destinationState{kind: stateRealDirectory}, nil
	}
	if metadata.Mode().IsRegular() {
		return destinationState{kind: stateRealFile}, nil
	}
	return destinationState{kind: stateRealPath}, nil
}

// linkState：恰好指到要装的那个 skill = 已安装（幂等 noop）；指进本来源但
// 指到别的 skill = 位置不对，算冲突；指向本来源之外 = 非受管路径，同样冲突。
func (s *skillInstall) linkState(resolved string, sourceDir string, kindLabel string) destinationState {
	normalized := normalizeLinkPath(resolved)
	if s.sameSkill(normalized, sourceDir) {
		return destinationState{kind: stateLinkedHere, kindLabel: kindLabel}
	}
	if _, ok := relativeToSourceRoot(normalized, s.normalizedRoot); ok {
		return destinationState{kind: stateLinkedOtherSkill, resolved: resolved, kindLabel: kindLabel}
	}
	return destinationState{kind: stateLinkedElsewhere, resolved: resolved, kindLabel: kindLabel}
}

func (s *skillInstall) sameSkill(normalizedResolved string, sourceDir string) bool {
	normalizedSource := sourceDir
	if canonical, err := support.Canonicalize(sourceDir); err == nil {
		normalizedSource = canonical
	}
	return normalizedResolved == normalizedSource
}

// ---------------------------------------------------------------------------
// 呈现层（同步弹窗的 state/detail 与安装判定共用同一状态机）
// ---------------------------------------------------------------------------

// destinationSnapshot：落点的对外呈现。state 取值 missing / installed /
// conflict / error，action 取值 create / replace / unchanged。
type destinationSnapshot struct {
	state  string
	detail string
	action string
}

func (st destinationState) snapshot(destination string) destinationSnapshot {
	switch st.kind {
	case stateUninstalled:
		return destinationSnapshot{state: "missing", detail: fmt.Sprintf("尚未安装到 %s", support.DisplayPath(destination)), action: "create"}
	case stateLinkedHere:
		return destinationSnapshot{state: "installed", detail: fmt.Sprintf("%s已存在：%s", st.kindLabel, support.DisplayPath(destination)), action: "unchanged"}
	case stateLinkedOtherSkill:
		return destinationSnapshot{state: "conflict", detail: fmt.Sprintf("%s指向本来源的其它位置：%s", st.kindLabel, support.DisplayPath(st.resolved)), action: "replace"}
	case stateLinkedElsewhere:
		return destinationSnapshot{state: "conflict", detail: fmt.Sprintf("当前链接指向 %s", support.DisplayPath(st.resolved)), action: "replace"}
	case stateRealDirectory:
		return destinationSnapshot{state: "conflict", detail: "目标位置已经有真实目录。", action: "replace"}
	case stateRealFile:
		return destinationSnapshot{state: "conflict", detail: "目标位置已经有真实文件。", action: "replace"}
	default:
		return destinationSnapshot{state: "conflict", detail: fmt.Sprintf("目标路径已存在：%s", support.DisplayPath(destination)), action: "replace"}
	}
}

// inspectSkillDestination：UI 只读呈现的 (state, detail)。
func inspectSkillDestination(sourcePath string, destination string) (string, string) {
	install := newSkillInstall(sourcePath)
	state, err := install.classify(sourcePath, destination)
	if err != nil {
		// 读不到落点元数据时的兜底呈现（只有 UI 只读路径会遇到）。
		return "error", fmt.Sprintf("无法读取 %s", support.DisplayPath(destination))
	}
	snapshot := state.snapshot(destination)
	return snapshot.state, snapshot.detail
}

// conflictOf：只有会阻断同步的落点产出冲突；「没东西」与「已指向本来源」
// 都不阻断。
func conflictOf(state destinationState) (string, string, bool) {
	switch state.kind {
	case stateUninstalled, stateLinkedHere:
		return "", "", false
	case stateLinkedOtherSkill, stateLinkedElsewhere:
		return state.kindLabel, fmt.Sprintf("当前指向 %s", support.DisplayPath(state.resolved)), true
	case stateRealDirectory:
		return "目录", "当前是同名目录", true
	case stateRealFile:
		return "文件", "当前是同名文件", true
	default:
		return "路径", "当前是同名路径", true
	}
}

// installItem：安装/卸载的事实条目；wire 类型（SkillSyncItem /
// SourceSyncConflict）在调用方转换，这里不承担展示格式。
type installItem struct {
	skillName       string
	targetID        AgentTargetID
	sourcePath      string
	destinationPath string
	action          string
	detail          *string
}

func (s *skillInstall) installedItem(skill selectedSkill, targetID string, sourceDir string, destination string, state destinationState) installItem {
	action, detail := "create", (*string)(nil)
	switch state.kind {
	case stateRealDirectory:
		text := "目标位置已经有真实目录，已替换。"
		action, detail = "replace", &text
	case stateRealFile:
		text := "目标位置已经有真实文件，已替换。"
		action, detail = "replace", &text
	case stateRealPath:
		text := "目标路径已存在，已替换。"
		action, detail = "replace", &text
	}
	return installItem{
		skillName:       skill.name,
		targetID:        AgentTargetID(targetID),
		sourcePath:      support.DisplayPath(sourceDir),
		destinationPath: support.DisplayPath(destination),
		action:          action,
		detail:          detail,
	}
}

func (s *skillInstall) unchangedItem(skill selectedSkill, targetID string, sourceDir string, destination string, state destinationState) installItem {
	detail := fmt.Sprintf("%s已存在：%s", state.kindLabel, support.DisplayPath(destination))
	return installItem{
		skillName:       skill.name,
		targetID:        AgentTargetID(targetID),
		sourcePath:      support.DisplayPath(sourceDir),
		destinationPath: support.DisplayPath(destination),
		action:          "unchanged",
		detail:          &detail,
	}
}

func removedItem(targetID string, destination string, resolved string) installItem {
	skillName := filepath.Base(destination)
	if skillName == "" || skillName == "." || skillName == string(filepath.Separator) {
		skillName = "skill"
	}
	return installItem{
		skillName:       skillName,
		targetID:        AgentTargetID(targetID),
		sourcePath:      support.DisplayPath(resolved),
		destinationPath: support.DisplayPath(destination),
		action:          "remove",
		detail:          nil,
	}
}

// ToSyncItem：wire 转换（detail 缺省为空串）。
func (item installItem) ToSyncItem() SkillSyncItem {
	detail := ""
	if item.detail != nil {
		detail = *item.detail
	}
	return SkillSyncItem{
		SkillName:       item.skillName,
		TargetID:        item.targetID,
		SourcePath:      item.sourcePath,
		DestinationPath: item.destinationPath,
		Action:          item.action,
		Detail:          detail,
	}
}

// installConflict：冲突条目的事实形态。
type installConflict struct {
	skillName       string
	targetID        string
	sourcePath      string
	destinationPath string
	existingKind    string
	detail          string
}

// ---------------------------------------------------------------------------
// 目录链接枚举与路径规范化（安装面的物理细节）
// ---------------------------------------------------------------------------

// enumeratedLink：枚举出的一个目录链接。
type enumeratedLink struct {
	destination string
	resolved    string
}

// enumerateLinks：列出一个 skills 目录里的目录链接：(落点, 链接目标)。目录
// 不存在 = 没有链接；其他读取错误必须上报，否则调用方会把部分成功当成成功。
func enumerateLinks(skillDir string) ([]enumeratedLink, error) {
	entries, err := os.ReadDir(skillDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []enumeratedLink{}, nil
		}
		return nil, fmt.Errorf("读取 target skills 目录失败：%s", skillDir)
	}

	links := make([]enumeratedLink, 0, len(entries))
	for _, entry := range entries {
		destination := joinPath(skillDir, entry.Name())
		resolved, err := readDirectoryLinkTarget(destination)
		if err != nil {
			return nil, fmt.Errorf("读取 target skills 目录项失败：%s：%w", skillDir, err)
		}
		if resolved == nil {
			continue
		}
		links = append(links, enumeratedLink{destination: destination, resolved: *resolved})
	}
	return links, nil
}

// normalizeLinkPath：一侧是系统软链接（/var → /private/var）、另一侧不是时，
// canonicalize 会失配，已安装的链接会既认不出来也删不掉。逐级向上找第一个
// 能规范化的祖先，再补回缺失层级；所有比较路径的调用方都走这里，两侧口径
// 才不会分叉。
func normalizeLinkPath(path string) string {
	var missing []string
	current := path
	for {
		if normalized, err := support.Canonicalize(current); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				normalized = joinPath(normalized, missing[i])
			}
			return normalized
		}
		parent := filepath.Dir(current)
		if parent == current {
			// 到达根目录仍不可规范化：回退原路径。
			return path
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// normalizeOwnDir：同 normalizeLinkPath，但用于「这个目录是不是我的」这类
// 归属比较：只规范化父目录，最后一段保留原样。对一个目录链接调用
// canonicalize 会解析到链接的另一端，于是「别名目录」与「被指向的目录」会
// 都等于目标目录——别名会认成自己的目标。
func normalizeOwnDir(path string) string {
	parent := filepath.Dir(path)
	name := filepath.Base(path)
	if parent == path || name == "" || name == "." || name == string(filepath.Separator) {
		return normalizeLinkPath(path)
	}
	return joinPath(normalizeLinkPath(parent), name)
}

// relativeToSourceRoot：path 是否落在 sourceRoot 下，命中时返回剩余相对
// 路径。Windows 分支剥 \\?\ 前缀并忽略大小写（前缀不影响语义，拼写大小写
// 可能不同），与 Rust 版逐字一致；两个分支都是纯字符串运算，任意平台均可
// 编译。
func relativeToSourceRoot(path string, sourceRoot string) (string, bool) {
	if runtime.GOOS != "windows" {
		return stripPathPrefix(path, sourceRoot)
	}

	pathComponents := splitWindowsComponents(path)
	sourceComponents := splitWindowsComponents(sourceRoot)
	if len(sourceComponents) > len(pathComponents) {
		return "", false
	}
	for i, sourceComponent := range sourceComponents {
		if !windowsPathComponentEq(pathComponents[i], sourceComponent) {
			return "", false
		}
	}
	remainder := pathComponents[len(sourceComponents):]
	if len(remainder) == 0 {
		return "", true
	}
	return strings.Join(remainder, "\\"), true
}

// splitWindowsComponents：按 Windows 的两种分隔符拆路径组件（空段折叠；
// Go 的 Canonicalize 不产出 \\?\ verbatim 前缀，两侧同构拆段即等价于
// Rust 的组件比较）。
func splitWindowsComponents(path string) []string {
	normalized := strings.ReplaceAll(path, "/", "\\")
	var components []string
	for _, segment := range strings.Split(normalized, "\\") {
		if segment == "" {
			continue
		}
		components = append(components, segment)
	}
	return components
}

// windowsPathComponentEq：Windows 路径组件比较：去掉扩展长度前缀后忽略
// 大小写。
func windowsPathComponentEq(left string, right string) bool {
	left = strings.TrimPrefix(left, `\\?\`)
	right = strings.TrimPrefix(right, `\\?\`)
	return strings.EqualFold(left, right)
}

// resolveSymlinkTargetPathNoCanonicalize：软链接目标解析（相对链接基于链接
// 所在目录），不做 canonicalize——classify 时保留解析出的拼写。与
// targets.go 的 resolveSymlinkTargetPath（canonicalize 版）职责不同。
func resolveSymlinkTargetPathNoCanonicalize(linkPath string, linkTarget string) string {
	if filepath.IsAbs(linkTarget) {
		return linkTarget
	}
	return joinPath(filepath.Dir(linkPath), linkTarget)
}
