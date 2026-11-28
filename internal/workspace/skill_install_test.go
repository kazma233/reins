// skill_install_test.go：安装面的单条技能路径最小夹具测试（对齐
// skill_install.rs 内嵌测试模块）：装一次建链接、再装幂等、缺失条目跳过、
// 真实目录冲突后替换、别名归属与统一跳过规则、跨来源清理边界。
package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"reins/internal/support"
	"reins/internal/testsupport"
)

// testSkillPath：一条技能路径；其余字段与解析结果同形。
func testSkillPath(name string, dir string) *ResolvedSkillPath {
	return &ResolvedSkillPath{Name: name, Dir: dir}
}

func testConfigWith(paths ...*ResolvedSkillPath) *ResolvedManagerConfig {
	config := &ResolvedManagerConfig{
		SkillPaths:   map[string]*ResolvedSkillPath{},
		McpPaths:     map[string]*ResolvedMcpPath{},
		SkillSources: map[string]*ResolvedSkillSourceDefinition{},
		Mcps:         []*ResolvedMcpConfig{},
	}
	for _, path := range paths {
		config.SkillPaths[path.Name] = path
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
	pathDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	config := testConfigWith(testSkillPath("codex", pathDir))
	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}

	install := newSkillInstall(sourceRoot)
	paths, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := make([]*managedPath, 0, len(paths))
	for i := range paths {
		participating = append(participating, &paths[i])
	}

	applied, err := install.apply(participating, selected)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 1 || applied[0].action != "create" || applied[0].detail != nil {
		t.Fatalf("首次安装应为 create 无 detail：%+v", applied)
	}
	linked, err := os.Readlink(filepath.Join(pathDir, "alpha"))
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

// 不存在的条目产出 skip 原因；调用方据此提示并跳过（技能路径没有启停状态，
// 不参与的原因只有「不存在」与「目录整层是链接」两种）。
func TestSkillInstallMissingPathsAreSkipped(t *testing.T) {
	root := testsupport.TestDir(t, "install-skip")
	sourceRoot := filepath.Join(root, "source")
	writeTestSkill(t, sourceRoot, "alpha")
	pathDir := filepath.Join(root, "target", "skills")
	config := testConfigWith(testSkillPath("codex", pathDir))

	paths, err := resolveIDs(config, []string{"codex", "ghost"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	if paths[0].skip != nil {
		t.Fatalf("存在的条目不应有 skip：%+v", paths[0])
	}
	if paths[1].skip == nil || *paths[1].skip != SkipMissing {
		t.Fatalf("缺失的条目应产出 SkipMissing：%+v", paths[1])
	}

	// 与 sync 路径同一段收敛逻辑：只有 skip 为空的条目参与安装。
	participating := make([]*managedPath, 0, len(paths))
	for i := range paths {
		if paths[i].skip == nil {
			participating = append(participating, &paths[i])
		}
	}
	if len(participating) != 1 {
		t.Fatalf("参与分发的条目数应为 1：%d", len(participating))
	}

	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}
	applied, err := newSkillInstall(sourceRoot).apply(participating, selected)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied: %d", len(applied))
	}
	if _, err := os.Lstat(filepath.Join(pathDir, "alpha")); err != nil {
		t.Fatalf("链接应已创建：%v", err)
	}
}

// 落点被真实目录占住时：预演报冲突并说明占位类型，安装走替换。
func TestSkillInstallRealDirectoryIsReportedAsConflictThenReplaced(t *testing.T) {
	root := testsupport.TestDir(t, "install-conflict")
	sourceRoot := filepath.Join(root, "source")
	pathDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	occupied := filepath.Join(pathDir, "alpha")
	if err := os.MkdirAll(occupied, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "stray.txt"), []byte("not a link"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	config := testConfigWith(testSkillPath("codex", pathDir))
	paths, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := []*managedPath{&paths[0]}
	selected := []selectedSkill{{name: "alpha", relativePath: "alpha"}}
	install := newSkillInstall(sourceRoot)

	conflicts, err := install.conflicts(participating, selected)
	if err != nil {
		t.Fatalf("conflicts: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("应产出一条冲突：%+v", conflicts)
	}
	if conflicts[0].existingKind != "目录" || conflicts[0].pathID != "codex" {
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
	pathDir := filepath.Join(root, "target", "skills")
	if err := os.MkdirAll(pathDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	destination := filepath.Join(pathDir, "alpha")
	if err := createDirectorySymlink(betaDir, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testSkillPath("codex", pathDir))
	paths, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := []*managedPath{&paths[0]}
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

// 技能路径的目录整层是目录链接时不参与分发：目录内容由链接的另一端负责，
// 这里不能往它里面装。别名跳过对所有条目统一生效（决策记录第 6 条；旧规则
// 只对项目级 target 生效）。
func TestSkillInstallLinkedPathDirIsSkippedUniformly(t *testing.T) {
	root := testsupport.TestDir(t, "install-skip-linked-dir")
	ownerDir := filepath.Join(root, "owner", "skills")
	if err := os.MkdirAll(ownerDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkedDir := filepath.Join(root, "linked", "skills")
	if err := os.MkdirAll(filepath.Dir(linkedDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(ownerDir, linkedDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testSkillPath("plain", linkedDir))

	paths, err := resolveIDs(config, []string{"plain"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	if paths[0].skip == nil || *paths[0].skip != SkipLinkedDir {
		t.Fatalf("别名路径应产出 SkipLinkedDir：%+v", paths[0])
	}

	managed, err := managedSkillPaths(config)
	if err != nil {
		t.Fatalf("managedSkillPaths: %v", err)
	}
	if len(managed) != 1 || managed[0].id != "plain" || managed[0].skip == nil || *managed[0].skip != SkipLinkedDir {
		t.Fatalf("枚举里 plain 应带 SkipLinkedDir：%+v", managed)
	}
}

// 别名落点的归属比较：一个目录链接不能与它指向的目录认成同一个落点、
// 更不能认成自己。normalizeLinkPath 会解析链接（用于判断「内容去哪了」），
// 归属比较必须走 normalizeOwnDir（最后一段保留原样）。
func TestSkillInstallOwnDirNormalizationKeepsAliasDistinctFromItsTarget(t *testing.T) {
	root := testsupport.TestDir(t, "install-alias-ownership")
	ownerDir := filepath.Join(root, "owner", "skills")
	if err := os.MkdirAll(ownerDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	aliasDir := filepath.Join(root, "project", "skills")
	if err := os.MkdirAll(filepath.Dir(aliasDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(ownerDir, aliasDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if normalizeOwnDir(aliasDir) == normalizeOwnDir(ownerDir) {
		t.Fatal("别名与它的目标必须是两个落点")
	}
	// 父目录被规范化（macOS 上 /var → /private/var），最后一段保留原样。
	canonicalParent, err := support.Canonicalize(filepath.Dir(aliasDir))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	if want := joinPath(canonicalParent, "skills"); normalizeOwnDir(aliasDir) != want {
		t.Fatalf("got %s, want %s", normalizeOwnDir(aliasDir), want)
	}

	// 对照：穿链接的解析把两者指到同一处（内容确实在同一处）。Unix 走
	// 软链接、Windows 走 junction，统一经目录链接读取；support.Canonicalize
	// 在 Windows 不解析 junction，不能在这里当「穿链接」用。
	aliasTarget, err := readDirectoryLinkTarget(aliasDir)
	if err != nil {
		t.Fatalf("read link target: %v", err)
	}
	if aliasTarget == nil || normalizeLinkPath(*aliasTarget) != normalizeLinkPath(ownerDir) {
		t.Fatal("穿链接的解析应把两者解析到同一处")
	}
}

// 别名落点指向另一条受管技能路径的目录时，属主必须报出那条路径的条目名。
func TestSkillInstallAliasOwnerIsReportedAsPathName(t *testing.T) {
	root := testsupport.TestDir(t, "install-alias-owner-address")
	ownerDir := filepath.Join(root, "owner", "skills")
	aliasDir := filepath.Join(root, "alias", "skills")
	if err := os.MkdirAll(ownerDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(aliasDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(ownerDir, aliasDir); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testSkillPath("owner", ownerDir), testSkillPath("alias", aliasDir))
	links, err := config.skillDirLinks()
	if err != nil {
		t.Fatalf("skillDirLinks: %v", err)
	}
	var alias *skillDirLinkEntry
	for i := range links {
		if links[i].name == "alias" {
			alias = &links[i]
		}
	}
	if alias == nil {
		t.Fatal("别名条目应在表里")
	}
	if !alias.link.IsAlias() {
		t.Fatal("别名条目应是别名")
	}
	if !alias.link.HasLinkedPath || alias.link.LinkedPathID != "owner" {
		t.Fatalf("命名归属应为 owner：%+v", alias.link)
	}
}

// 指向别的来源的链接不属于本来源：卸载不动它。
func TestSkillInstallRemoveInstalledKeepsOtherSourceLinks(t *testing.T) {
	root := testsupport.TestDir(t, "install-remove-foreign")
	sourceRoot := filepath.Join(root, "source")
	writeTestSkill(t, sourceRoot, "alpha")
	foreignRoot := filepath.Join(root, "foreign")
	foreignSkill := writeTestSkill(t, foreignRoot, "beta")
	pathDir := filepath.Join(root, "target", "skills")
	if err := os.MkdirAll(pathDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	destination := filepath.Join(pathDir, "beta")
	if err := createDirectorySymlink(foreignSkill, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := testConfigWith(testSkillPath("codex", pathDir))
	paths, err := resolveIDs(config, []string{"codex"})
	if err != nil {
		t.Fatalf("resolveIDs: %v", err)
	}
	participating := []*managedPath{&paths[0]}

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
