// skill_install_test.go：安装面的单 target 最小夹具测试（对齐
// skill_install.rs 内嵌测试模块）：装一次建链接、再装幂等、缺失目标跳过、
// 真实目录冲突后替换、别名归属、跨来源清理边界。
package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

// testTarget：一个全局 target；其余字段与解析结果同形。
func testTarget(id string, skillDir string) *ResolvedTargetConfig {
	return &ResolvedTargetConfig{
		ID:              AgentTargetID(id),
		IsProject:       false,
		SkillDir:        skillDir,
		ConfigPath:      nil,
		MCPConfigPrefix: "",
		MCPConfigType:   agents.McpConfigCommon,
	}
}

func testConfigWith(targets ...*ResolvedTargetConfig) *ResolvedManagerConfig {
	config := &ResolvedManagerConfig{
		Targets:      map[AgentTargetID]*ResolvedTargetConfig{},
		Projects:     map[string]*ResolvedProjectConfig{},
		SkillSources: map[string]*ResolvedSkillSourceDefinition{},
		Mcps:         []*ResolvedMcpConfig{},
	}
	for _, target := range targets {
		config.Targets[target.ID] = target
	}
	return config
}

// writeTestSkill：在 root 下建 skill 目录并写 SKILL.md；body 缺省用目录名
// （与 Rust write_skill 的 body 参数同位，可显式给 frontmatter 内容）。
func writeTestSkill(t *testing.T, root string, relativePath string, body ...string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := filepath.Base(dir)
	if len(body) > 0 {
		content = body[0]
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

// 一个目标：装一次创建链接，再装一次是幂等 noop（已指向本来源）。
func TestSkillInstallApplyCreatesLinkThenReportsUnchanged(t *testing.T) {
	root := testsupport.TestDir(t, "install-apply")
	sourceRoot := filepath.Join(root, "source")
	skillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	config := testConfigWith(testTarget("codex", skillDir))
	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}

	install := newSkillInstall(sourceRoot)
	targets, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := make([]*managedTarget, 0, len(targets))
	for i := range targets {
		participating = append(participating, &targets[i])
	}

	applied, err := install.apply(participating, selected)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 1 || applied[0].action != "create" || applied[0].detail != nil {
		t.Fatalf("首次安装应为 create 无 detail：%+v", applied)
	}
	linked, err := os.Readlink(filepath.Join(skillDir, "alpha"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	wantTarget, wantErr := support.Canonicalize(filepath.Join(sourceRoot, "alpha"))
	if wantErr != nil {
		t.Fatalf("canonicalize: %v", wantErr)
	}
	if resolved, err := support.Canonicalize(linked); err != nil || resolved != wantTarget {
		t.Fatalf("链接应指向来源 skill：%s (%v)", linked, err)
	}

	again, err := install.apply(participating, selected)
	if err != nil {
		t.Fatalf("apply again: %v", err)
	}
	if again[0].action != "unchanged" || again[0].detail == nil {
		t.Fatalf("重复安装应为 unchanged 带提示：%+v", again)
	}

	// 冲突预演对已装好的落点必须静默，否则用户每次同步都会被弹窗拦住。
	conflicts, err := install.conflicts(participating, selected)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("已安装落点不应产出冲突：%+v", conflicts)
	}
}

// 不存在的目标产出 skip 原因；调用方据此提示并跳过（agent 没有启停状态，
// 唯一的不参与原因就是「继承自另一个受管目录」）。
func TestSkillInstallMissingTargetsAreSkipped(t *testing.T) {
	root := testsupport.TestDir(t, "install-skip")
	sourceRoot := filepath.Join(root, "source")
	writeTestSkill(t, sourceRoot, "alpha")
	skillDir := filepath.Join(root, "target", "skills")
	config := testConfigWith(testTarget("codex", skillDir))

	targets, err := resolveIDs(config, []string{"codex", "ghost"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	if targets[0].skip != nil {
		t.Fatalf("存在的 target 不应有 skip：%+v", targets[0])
	}
	if targets[1].skip == nil || *targets[1].skip != SkipMissing {
		t.Fatalf("缺失的 target 应产出 SkipMissing：%+v", targets[1])
	}

	// 与 sync 路径同一段收敛逻辑：只有 skip 为空的目标参与安装。
	participating := make([]*managedTarget, 0, len(targets))
	for i := range targets {
		if targets[i].skip == nil {
			participating = append(participating, &targets[i])
		}
	}
	if len(participating) != 1 {
		t.Fatalf("参与分发的目标数应为 1：%d", len(participating))
	}

	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}
	applied, err := newSkillInstall(sourceRoot).apply(participating, selected)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied: %d", len(applied))
	}
	if _, err := os.Lstat(filepath.Join(skillDir, "alpha")); err != nil {
		t.Fatalf("链接应已创建：%v", err)
	}
}

// 落点被真实目录占住时：预演报冲突并说明占位类型，安装走替换。
func TestSkillInstallRealDirectoryIsReportedAsConflictThenReplaced(t *testing.T) {
	root := testsupport.TestDir(t, "install-conflict")
	sourceRoot := filepath.Join(root, "source")
	skillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	occupied := filepath.Join(skillDir, "alpha")
	if err := os.MkdirAll(occupied, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "stray.txt"), []byte("not a link"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	config := testConfigWith(testTarget("codex", skillDir))
	targets, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := []*managedTarget{&targets[0]}
	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}
	install := newSkillInstall(sourceRoot)

	conflicts, err := install.conflicts(participating, selected)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("应产出一条冲突：%+v", conflicts)
	}
	if conflicts[0].existingKind != "目录" || conflicts[0].targetID != "codex" {
		t.Fatalf("冲突详情不符：%+v", conflicts[0])
	}

	applied, err := install.apply(participating, selected)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied[0].action != "replace" || applied[0].detail == nil || *applied[0].detail != "目标位置已经有真实目录，已替换。" {
		t.Fatalf("安装应替换真实目录：%+v", applied[0])
	}
}

// 指向同来源另一个 skill 的链接：安装判定为冲突（位置不对），卸载按来源清理时删掉。
func TestSkillInstallLinkToOtherSkillOfSameSourceIsConflictAndRemoved(t *testing.T) {
	root := testsupport.TestDir(t, "install-other-skill")
	sourceRoot := filepath.Join(root, "source")
	betaDir := writeTestSkill(t, sourceRoot, "beta")
	writeTestSkill(t, sourceRoot, "alpha")
	skillDir := filepath.Join(root, "target", "skills")
	if err := os.MkdirAll(skillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	destination := filepath.Join(skillDir, "alpha")
	if err := createDirectorySymlink(betaDir, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testTarget("codex", skillDir))
	targets, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := []*managedTarget{&targets[0]}
	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}
	install := newSkillInstall(sourceRoot)

	conflicts, err := install.conflicts(participating, selected)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("应产出一条冲突：%+v", conflicts)
	}

	// 卸载按「指进本来源」清理，位置不对的链接同样会被删掉（它是本来源的残留）。
	removed, err := install.removeInstalled(participating)
	if err != nil {
		t.Fatalf("removeInstalled: %v", err)
	}
	if len(removed) != 1 || removed[0].action != "remove" {
		t.Fatalf("应删除残留链接：%+v", removed)
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Fatalf("链接应已删除：%v", err)
	}
}

// 项目级 target 的 skill_dir 整层是目录链接时不参与分发：目录内容由链接
// 的另一端负责，这里不能往它里面装。全局 target 不受此判定（既有行为）。
func TestSkillInstallLinkedProjectSkillDirIsSkipped(t *testing.T) {
	root := testsupport.TestDir(t, "install-skip-linked-dir")
	ownerSkillDir := filepath.Join(root, "owner", "skills")
	if err := os.MkdirAll(ownerSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkedSkillDir := filepath.Join(root, "linked", "skills")
	if err := os.MkdirAll(filepath.Dir(linkedSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(ownerSkillDir, linkedSkillDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	projectTarget := testTarget("claude", linkedSkillDir)
	projectTarget.IsProject = true
	config := testConfigWith(testTarget("codex", linkedSkillDir), projectTarget)

	targets, err := resolveIDs(config, []string{"codex", "claude"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	// 全局 target 的链接不触发跳过；项目级触发。
	if targets[0].skip != nil {
		t.Fatalf("全局 target 不应跳过：%+v", targets[0])
	}
	if targets[1].skip == nil || *targets[1].skip != SkipLinkedDir {
		t.Fatalf("项目级链接落点应产出 SkipLinkedDir：%+v", targets[1])
	}

	managed, err := managedTargets(config)
	if err != nil {
		t.Fatalf("managedTargets: %v", err)
	}
	for _, item := range managed {
		if item.id == "claude" && (item.skip == nil || *item.skip != SkipLinkedDir) {
			t.Fatalf("枚举里 claude 应带 SkipLinkedDir：%+v", item)
		}
	}
}

// 别名落点的归属比较：一个目录链接不能与它指向的目录认成同一个落点、
// 更不能认成自己。normalizeLinkPath 会解析链接（用于判断「内容去哪了」），
// 归属比较必须走 normalizeOwnDir（最后一段保留原样）。
func TestSkillInstallOwnDirNormalizationKeepsAliasDistinctFromItsTarget(t *testing.T) {
	root := testsupport.TestDir(t, "install-alias-ownership")
	ownerSkillDir := filepath.Join(root, "owner", "skills")
	if err := os.MkdirAll(ownerSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	aliasSkillDir := filepath.Join(root, "project", "skills")
	if err := os.MkdirAll(filepath.Dir(aliasSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(ownerSkillDir, aliasSkillDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if normalizeOwnDir(aliasSkillDir) == normalizeOwnDir(ownerSkillDir) {
		t.Fatal("别名与它的目标必须是两个落点")
	}
	// 父目录被规范化（macOS 上 /var → /private/var），最后一段保留原样。
	canonicalParent, err := support.Canonicalize(filepath.Dir(aliasSkillDir))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if want := joinPath(canonicalParent, "skills"); normalizeOwnDir(aliasSkillDir) != want {
		t.Fatalf("got %s, want %s", normalizeOwnDir(aliasSkillDir), want)
	}

	// 对照：穿链接的解析把两者指到同一处（内容确实在同一处）。Unix 走
	// 软链接、Windows 走 junction，统一经目录链接读取；support.Canonicalize
	// 在 Windows 不解析 junction，不能在这里当「穿链接」用。
	aliasTarget, err := readDirectoryLinkTarget(aliasSkillDir)
	if err != nil {
		t.Fatalf("read link target: %v", err)
	}
	if aliasTarget == nil || normalizeLinkPath(*aliasTarget) != normalizeLinkPath(ownerSkillDir) {
		t.Fatal("穿链接的解析应把两者解析到同一处")
	}
}

// 项目级落点指向全局 target 的目录时，属主必须报完整地址：只报 agent 名
// 的话，跨项目就看不出它属于哪个 target（`claude` 到底指哪个）。
func TestSkillInstallAliasOwnerIsReportedAsFullAddress(t *testing.T) {
	root := testsupport.TestDir(t, "install-alias-owner-address")
	globalSkillDir := filepath.Join(root, "global", "skills")
	projectSkillDir := filepath.Join(root, "project", "skills")
	if err := os.MkdirAll(globalSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(projectSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(globalSkillDir, projectSkillDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testTarget("claude", globalSkillDir))
	config.Projects["my-app"] = &ResolvedProjectConfig{
		ID:          "my-app",
		ProjectPath: filepath.Join(root, "project"),
		Agents:      map[AgentTargetID]*ResolvedTargetConfig{AgentTargetID("claude"): testTarget("claude", projectSkillDir)},
	}

	links, err := config.skillDirLinks()
	if err != nil {
		t.Fatalf("skillDirLinks: %v", err)
	}
	var project *skillDirLinkEntry
	for i := range links {
		if links[i].reference.CompositeID() == "my-app:claude" {
			project = &links[i]
		}
	}
	if project == nil {
		t.Fatal("项目级落点应在表里")
	}
	if !project.link.IsAlias() {
		t.Fatal("项目级落点应是别名")
	}
	if !project.link.HasLinkedTarget || project.link.LinkedTargetID != "claude" {
		t.Fatalf("全局 target 的地址就是它的 id：%+v", project.link)
	}

	// 反向：全局落点是链接、指向项目级目录时，属主报的是复合地址。
	reversed := testConfigWith(testTarget("claude", projectSkillDir))
	reversed.Projects["my-app"] = &ResolvedProjectConfig{
		ID:          "my-app",
		ProjectPath: filepath.Join(root, "project"),
		Agents:      map[AgentTargetID]*ResolvedTargetConfig{AgentTargetID("codex"): testTarget("codex", globalSkillDir)},
	}
	links, err = reversed.skillDirLinks()
	if err != nil {
		t.Fatalf("skillDirLinks: %v", err)
	}
	var global *skillDirLinkEntry
	for i := range links {
		if links[i].reference.CompositeID() == "claude" {
			global = &links[i]
		}
	}
	if global == nil {
		t.Fatal("全局落点应在表里")
	}
	if !global.link.HasLinkedTarget || global.link.LinkedTargetID != "my-app:codex" {
		t.Fatalf("属主是项目级 target 时要报复合地址：%+v", global.link)
	}
}

// 指向别的来源的链接不属于本来源：卸载不动它。
func TestSkillInstallRemoveInstalledKeepsOtherSourceLinks(t *testing.T) {
	root := testsupport.TestDir(t, "install-remove-foreign")
	sourceRoot := filepath.Join(root, "source")
	writeTestSkill(t, sourceRoot, "alpha")
	foreignRoot := filepath.Join(root, "foreign")
	foreignSkill := writeTestSkill(t, foreignRoot, "beta")
	skillDir := filepath.Join(root, "target", "skills")
	if err := os.MkdirAll(skillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	destination := filepath.Join(skillDir, "beta")
	if err := createDirectorySymlink(foreignSkill, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testTarget("codex", skillDir))
	targets, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := []*managedTarget{&targets[0]}

	removed, err := newSkillInstall(sourceRoot).removeInstalled(participating)
	if err != nil {
		t.Fatalf("removeInstalled: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("别处来源的链接不应被清理：%+v", removed)
	}
	if _, err := os.Lstat(destination); err != nil {
		t.Fatalf("链接应仍在：%v", err)
	}
}
