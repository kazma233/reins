// workspace_test.go：workspace 域主测试套（对齐 tests/workspace.rs，含
// addressing/skill_install 的共享夹具复用）。环境相关断言统一在
// testsupport.SetHome 的临时 home 下取值，夹具自带隔离。
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"reins/internal/agents"
	"reins/internal/support"
	"reins/internal/testsupport"
)

// ---------------------------------------------------------------------------
// 共享夹具
// ---------------------------------------------------------------------------

// newTestStore：临时 app 目录 + 临时 home 的隔离 store。GROK_HOME 等重定向
// 变量被 SetHome 清空，需要重定向的测试在调用后用 t.Setenv 注入。
func newTestStore(t *testing.T, prefix string) (*WorkspaceConfigStore, string) {
	t.Helper()
	dir := testsupport.TestDir(t, prefix)
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o777); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	testsupport.SetHome(t, home)
	store := NewWorkspaceConfigStoreAt(dir)
	store.inner.env = support.FromEnv(agents.Redirects())
	return store, dir
}

// envOf：与 newTestStore 同口径的环境视图（已隔离重定向变量）。
func envOf(t *testing.T) *support.Environment {
	t.Helper()
	return support.FromEnv(agents.Redirects())
}

func writeFileT(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o666); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFileT(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func linkTestSource(id string, rootPath string, includeNamePatterns ...string) skillLinkSource {
	return skillLinkSource{
		id:                  id,
		rootPath:            normalizeLinkPath(rootPath),
		includeNamePatterns: includeNamePatterns,
		includePathPatterns: nil,
	}
}

func resolvedTargetFor(id string, skillDir string) *ResolvedTargetConfig {
	return testTarget(id, skillDir)
}

func resolvedConfigOf(targets ...*ResolvedTargetConfig) *ResolvedManagerConfig {
	return testConfigWith(targets...)
}

func linkTarget(t *testing.T, path string) string {
	t.Helper()
	resolved, err := support.Canonicalize(path)
	if err != nil {
		t.Fatalf("canonicalize %s: %v", path, err)
	}
	return resolved
}

func assertLinkMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("链接应已删除：%s（%v）", path, err)
	}
}

// ---------------------------------------------------------------------------
// 同步弹窗的 target 候选与链接归类
// ---------------------------------------------------------------------------

func TestSyncTargetOptionsReportAllLinkStates(t *testing.T) {
	root := testsupport.TestDir(t, "sync-target-link-states")
	sourceRoot := filepath.Join(root, "source")
	targetSkillDir := filepath.Join(root, "target", "skills")
	unmanagedRoot := filepath.Join(root, "unmanaged")

	writeTestSkill(t, sourceRoot, "linked")
	writeTestSkill(t, sourceRoot, "excluded")
	writeTestSkill(t, sourceRoot, "removed")
	writeTestSkill(t, unmanagedRoot, "external")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mustSymlink(t, filepath.Join(sourceRoot, "linked"), filepath.Join(targetSkillDir, "linked"))
	mustSymlink(t, filepath.Join(sourceRoot, "excluded"), filepath.Join(targetSkillDir, "excluded"))
	mustSymlink(t, filepath.Join(sourceRoot, "removed"), filepath.Join(targetSkillDir, "removed"))
	mustSymlink(t, filepath.Join(unmanagedRoot, "external"), filepath.Join(targetSkillDir, "external"))
	if err := os.RemoveAll(filepath.Join(sourceRoot, "removed")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	config := resolvedConfigOf(resolvedTargetFor("codex", targetSkillDir))
	sources := []skillLinkSource{linkTestSource("local", sourceRoot, "linked")}
	options, err := buildSyncTargetOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncTargetOptionsWithSources: %v", err)
	}
	links := options[0].Links

	if len(links) != 4 {
		t.Fatalf("所有目录链接都应展示：%d", len(links))
	}
	if links[0].SkillName != "excluded" || links[0].State != LinkStateExcluded {
		t.Fatalf("links[0]：%+v", links[0])
	}
	if want := []string{"local"}; !equalStrings(links[0].MatchedSourceIDs, want) {
		t.Fatalf("links[0].MatchedSourceIDs：%v", links[0].MatchedSourceIDs)
	}
	if links[1].SkillName != "external" || links[1].State != LinkStateUnmanaged || len(links[1].MatchedSourceIDs) != 0 {
		t.Fatalf("links[1]：%+v", links[1])
	}
	if links[2].SkillName != "linked" || links[2].State != LinkStateLinked {
		t.Fatalf("links[2]：%+v", links[2])
	}
	if links[3].SkillName != "removed" || links[3].State != LinkStateSourceMissed {
		t.Fatalf("links[3]：%+v", links[3])
	}
}

// 来源根目录本身是别名时，缺失 skill 的链接仍要被发现并标注 SourceMissing。
func TestSyncTargetOptionsKeepMissingSkillWhenSourceRootIsLinked(t *testing.T) {
	root := testsupport.TestDir(t, "missing-skill-through-linked-source-root")
	sourceRoot := filepath.Join(root, "source")
	linkedSourceRoot := filepath.Join(root, "linked-source")
	targetSkillDir := filepath.Join(root, "target", "skills")

	writeTestSkill(t, sourceRoot, "removed")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, sourceRoot, linkedSourceRoot)
	mustSymlink(t, filepath.Join(linkedSourceRoot, "removed"), filepath.Join(targetSkillDir, "removed"))
	if err := os.RemoveAll(filepath.Join(sourceRoot, "removed")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	config := resolvedConfigOf(resolvedTargetFor("codex", targetSkillDir))
	sources := []skillLinkSource{linkTestSource("local", linkedSourceRoot)}
	options, err := buildSyncTargetOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncTargetOptionsWithSources: %v", err)
	}
	if len(options[0].Links) == 0 {
		t.Fatal("missing skill link should be found")
	}
	link := options[0].Links[0]
	if link.State != LinkStateSourceMissed {
		t.Fatalf("state：%s", link.State)
	}
	if want := []string{"local"}; !equalStrings(link.MatchedSourceIDs, want) {
		t.Fatalf("matched：%v", link.MatchedSourceIDs)
	}
}

// 来源根目录重叠时，一条链接可以同时归属多个来源；包含过滤逐来源独立判定。
func TestSyncTargetOptionsKeepAllOverlappingSourceMatches(t *testing.T) {
	root := testsupport.TestDir(t, "sync-target-overlapping-sources")
	outerRoot := filepath.Join(root, "sources")
	innerRoot := filepath.Join(outerRoot, "nested")
	targetSkillDir := filepath.Join(root, "target", "skills")

	writeTestSkill(t, innerRoot, "alpha")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(innerRoot, "alpha"), filepath.Join(targetSkillDir, "alpha"))

	config := resolvedConfigOf(resolvedTargetFor("codex", targetSkillDir))
	sources := []skillLinkSource{
		linkTestSource("outer", outerRoot),
		linkTestSource("inner", innerRoot),
	}
	options, err := buildSyncTargetOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncTargetOptionsWithSources: %v", err)
	}
	if len(options[0].Links) == 0 {
		t.Fatal("skill link should be found")
	}
	link := options[0].Links[0]
	if link.State != LinkStateLinked {
		t.Fatalf("state：%s", link.State)
	}
	if want := []string{"outer", "inner"}; !equalStrings(link.MatchedSourceIDs, want) {
		t.Fatalf("matched：%v", link.MatchedSourceIDs)
	}
}

// 整目录继承（replica 的 skills 目录指向 owner 的）不重复列出关联：
// 同一批物理链接不误报为多份安装。
func TestSyncTargetOptionsDoNotDuplicateManagedDirectoryInheritance(t *testing.T) {
	root := testsupport.TestDir(t, "sync-target-directory-inheritance")
	sourceRoot := filepath.Join(root, "source")
	ownerSkillDir := filepath.Join(root, "owner", "skills")
	replicaSkillDir := filepath.Join(root, "replica", "skills")

	writeTestSkill(t, sourceRoot, "alpha")
	if err := os.MkdirAll(ownerSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(replicaSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(ownerSkillDir, "alpha"))
	mustSymlink(t, ownerSkillDir, replicaSkillDir)

	// macOS 上 /var 是 /private/var 的软链接：链接解出的路径是 canonical 形态。
	expectedLinkTarget := support.DisplayPath(linkTarget(t, ownerSkillDir))
	config := resolvedConfigOf(
		resolvedTargetFor("owner", ownerSkillDir),
		resolvedTargetFor("replica", replicaSkillDir),
	)
	sources := []skillLinkSource{linkTestSource("local", sourceRoot)}
	options, err := buildSyncTargetOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncTargetOptionsWithSources: %v", err)
	}
	var owner, replica *SyncTargetOption
	for i := range options {
		switch options[i].ID {
		case "owner":
			owner = &options[i]
		case "replica":
			replica = &options[i]
		}
	}
	if owner == nil || replica == nil {
		t.Fatalf("owner/replica 都应在列表里：%+v", options)
	}
	if len(owner.Links) != 1 {
		t.Fatalf("owner 应列出自己的安装记录：%+v", owner.Links)
	}
	if replica.LinkedTargetID == nil || *replica.LinkedTargetID != "owner" {
		t.Fatalf("replica 的命名归属应为 owner：%+v", replica.LinkedTargetID)
	}
	if replica.SkillDirLinkTarget == nil || *replica.SkillDirLinkTarget != expectedLinkTarget {
		t.Fatalf("replica 的链接目标应为 canonical 形态：%+v", replica.SkillDirLinkTarget)
	}
	if len(replica.Links) != 0 {
		t.Fatalf("别名 target 不应重复列出关联：%+v", replica.Links)
	}
}

// 链接另一端不是受管 target（外部目录或断链）时，命名归属为空，但「落点是别名」
// 这件事必须带给界面——否则前端会保持可勾，勾了之后后端静默跳过（用户可见的
// 静默失效）。这里锁住归属字段仍然有值。
func TestSyncTargetOptionsReportAliasSkillDirWithoutNamedOwner(t *testing.T) {
	root := testsupport.TestDir(t, "sync-target-external-alias")
	externalSkillDir := filepath.Join(root, "external", "skills")
	aliasSkillDir := filepath.Join(root, "project", "skills")
	brokenSkillDir := filepath.Join(root, "broken", "skills")
	if err := os.MkdirAll(externalSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(aliasSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(brokenSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, externalSkillDir, aliasSkillDir)
	mustSymlink(t, filepath.Join(root, "gone"), brokenSkillDir)

	plainSkillDir := filepath.Join(root, "plain", "skills")
	if err := os.MkdirAll(plainSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	config := resolvedConfigOf(
		resolvedTargetFor("plain", plainSkillDir),
		resolvedTargetFor("alias", aliasSkillDir),
		resolvedTargetFor("broken", brokenSkillDir),
	)
	options, err := buildSyncTargetOptionsWithSources(config, []skillLinkSource{linkTestSource("local", filepath.Join(root, "source"))})
	if err != nil {
		t.Fatalf("buildSyncTargetOptionsWithSources: %v", err)
	}
	byID := func(id string) *SyncTargetOption {
		for i := range options {
			if options[i].ID == id {
				return &options[i]
			}
		}
		t.Fatalf("target 应在列表里：%s", id)
		return nil
	}

	// 普通落点：没有归属，也就没有被跳过的理由。
	if byID("plain").SkillDirLinkTarget != nil || byID("plain").LinkedTargetID != nil {
		t.Fatalf("普通落点不应有归属字段：%+v", byID("plain"))
	}

	// 外部目录：叫不出名字，但归属字段有值（界面据此禁用并说明内容在哪）。
	alias := byID("alias")
	if alias.LinkedTargetID != nil {
		t.Fatalf("外部目录不应有命名归属：%+v", alias.LinkedTargetID)
	}
	if alias.SkillDirLinkTarget == nil || *alias.SkillDirLinkTarget != support.DisplayPath(linkTarget(t, externalSkillDir)) {
		t.Fatalf("外部目录的链接目标应有值：%+v", alias.SkillDirLinkTarget)
	}

	// 断链：canonicalize 失败，回落到解析出的原路径，同样有值。
	broken := byID("broken")
	if broken.SkillDirLinkTarget == nil || *broken.SkillDirLinkTarget != filepath.Join(root, "gone") {
		t.Fatalf("断链的链接目标应回落原路径：%+v", broken.SkillDirLinkTarget)
	}
}

// Windows junction 的断链行为：junction 目标删除后 reparse 数据仍在。
// darwin 上无法运行，交叉编译（GOOS=windows）保证分支可编译。
func TestReadDirectoryLinkTargetKeepsMissingJunctionTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junction 只在 Windows 存在；Unix 分支由符号链接等价用例覆盖")
	}
	root := testsupport.TestDir(t, "missing-junction-target")
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "target", "alpha")

	if err := os.MkdirAll(source, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(source, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("remove: %v", err)
	}

	target, err := readDirectoryLinkTarget(destination)
	if err != nil {
		t.Fatalf("readDirectoryLinkTarget: %v", err)
	}
	if target == nil || *target != source {
		t.Fatalf("junction target should persist：%+v", target)
	}
}

// Unix 等价用例：软链接指向的目标删除后，读出的仍是解析出的原目标路径。
func TestReadDirectoryLinkTargetKeepsMissingSymlinkTargetOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 走 junction 用例")
	}
	root := testsupport.TestDir(t, "missing-symlink-target")
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "target", "alpha")

	if err := os.MkdirAll(source, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(source, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("remove: %v", err)
	}

	target, err := readDirectoryLinkTarget(destination)
	if err != nil {
		t.Fatalf("readDirectoryLinkTarget: %v", err)
	}
	if target == nil || *target != source {
		t.Fatalf("断链仍应给出解析出的目标路径：%+v", target)
	}
}

// ---------------------------------------------------------------------------
// 安装面（UI 只读呈现与链接清理）
// ---------------------------------------------------------------------------

func TestInspectSkillDestinationTreatsSymlinkToAnotherSkillDirAsConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的目录链接是 junction，语义由 junction 用例覆盖")
	}
	root := testsupport.TestDir(t, "inspect-symlink")
	source := filepath.Join(root, "source")
	claudeDir := filepath.Join(root, "claude", "skills", "alpha")
	codexDestination := filepath.Join(root, "codex", "skills", "alpha")

	if err := os.MkdirAll(source, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("alpha"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(claudeDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "SKILL.md"), []byte("alpha"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(codexDestination), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, claudeDir, codexDestination)

	state, _ := inspectSkillDestination(source, codexDestination)
	if state != "conflict" {
		t.Fatalf("软链接指向另一份同名 skill（来自不同 source）应判 conflict，got %s", state)
	}
}

func TestInspectSkillDestinationTreatsSymlinkToEmptyDirAsConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 的目录链接是 junction，语义由 junction 用例覆盖")
	}
	root := testsupport.TestDir(t, "inspect-symlink-empty")
	source := filepath.Join(root, "source")
	emptyDir := filepath.Join(root, "empty")
	destination := filepath.Join(root, "destination")

	if err := os.MkdirAll(source, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("alpha"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(emptyDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, emptyDir, destination)

	state, _ := inspectSkillDestination(source, destination)
	if state != "conflict" {
		t.Fatalf("软链接指向空目录应判 conflict，got %s", state)
	}
}

func TestCreateDirectoryLinkIsDetectedAsInstalled(t *testing.T) {
	root := testsupport.TestDir(t, "directory-link-detect")
	source := filepath.Join(root, "source", "alpha")
	destination := filepath.Join(root, "target", "alpha")

	if err := os.MkdirAll(source, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("alpha"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(source, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	linkTargetValue, err := readSkillDirLink(destination)
	if err != nil {
		t.Fatalf("readSkillDirLink: %v", err)
	}
	if linkTargetValue == nil || *linkTargetValue != linkTarget(t, source) {
		t.Fatalf("directory link should exist：%+v", linkTargetValue)
	}

	install := newSkillInstall(source)
	state, err := install.classify(source, destination)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if snapshot := state.snapshot(destination); snapshot.action != "unchanged" {
		t.Fatalf("应判定为已安装（unchanged）：%+v", snapshot)
	}

	if err := removeExistingPath(destination); err != nil {
		t.Fatalf("removeExistingPath: %v", err)
	}
	assertLinkMissing(t, destination)
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}
}

func TestRemoveSourceLinksKeepsSourceDirectory(t *testing.T) {
	root := testsupport.TestDir(t, "directory-link-cleanup")
	sourceRoot := filepath.Join(root, "source")
	sourceSkill := filepath.Join(sourceRoot, "alpha")
	targetSkillDir := filepath.Join(root, "target", "skills")
	destination := filepath.Join(targetSkillDir, "alpha")

	if err := os.MkdirAll(sourceSkill, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkill, "SKILL.md"), []byte("alpha"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(sourceSkill, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := resolvedConfigOf(resolvedTargetFor("codex", targetSkillDir))
	removed, err := removeSourceSymlinksWithRoot(config, sourceRoot, nil)
	if err != nil {
		t.Fatalf("removeSourceSymlinksWithRoot: %v", err)
	}

	if len(removed) != 1 {
		t.Fatalf("应删除一条链接：%+v", removed)
	}
	assertLinkMissing(t, destination)
	if _, err := os.Stat(filepath.Join(sourceSkill, "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}
}

// 两个 target + 一个 local 来源的最小 workspace 配置，供链接移除用例复用。
func writeLinkStore(t *testing.T, root string) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  codex:
    skill_dir: %s
  other:
    skill_dir: %s
skill_sources:
  - id: src
    type: local
    root_path: %s
`, filepath.Join(root, "target", "skills"), filepath.Join(root, "other", "skills"), filepath.Join(root, "source")))
	return store
}

// 来源根目录还在、单独 skill 目录被删时，链接目标 canonicalize 会失败并退回
// 原始路径，与规范化后的来源根目录在 /var → /private/var 这类系统软链接下失配，
// 已被识别为待清理的链接会删不掉。
func TestRemoveSourceSyncClearsLinkWhoseSkillDirectoryIsGone(t *testing.T) {
	root := testsupport.TestDir(t, "remove-sync-missing-skill")
	sourceRoot := filepath.Join(root, "source")
	targetSkillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	writeTestSkill(t, sourceRoot, "beta")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(targetSkillDir, "alpha"))
	mustSymlink(t, filepath.Join(sourceRoot, "beta"), filepath.Join(targetSkillDir, "beta"))

	store := writeLinkStore(t, root)
	if err := os.RemoveAll(filepath.Join(sourceRoot, "alpha")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	result, err := RemoveSourceSyncInner(store, "src", []string{"codex"})
	if err != nil {
		t.Fatalf("RemoveSourceSyncInner: %v", err)
	}
	if len(result.Removed) != 2 {
		t.Fatalf("应删除两条链接：%+v", result.Removed)
	}
	assertLinkMissing(t, filepath.Join(targetSkillDir, "alpha"))
	assertLinkMissing(t, filepath.Join(targetSkillDir, "beta"))
	if _, err := os.Stat(filepath.Join(sourceRoot, "beta", "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}
}

// 整个来源目录被删后，来源根目录也 canonicalize 不了；两侧都必须退回同一套
// 规范化规则，否则删除同步会漏掉这些链接。
func TestRemoveSourceSyncClearsLinkAfterSourceRootIsDeleted(t *testing.T) {
	root := testsupport.TestDir(t, "remove-sync-deleted-root")
	sourceRoot := filepath.Join(root, "source")
	targetSkillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(targetSkillDir, "alpha"))

	store := writeLinkStore(t, root)
	if err := os.RemoveAll(sourceRoot); err != nil {
		t.Fatalf("remove: %v", err)
	}

	result, err := RemoveSourceSyncInner(store, "src", []string{"codex"})
	if err != nil {
		t.Fatalf("RemoveSourceSyncInner: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0].SkillName != "alpha" {
		t.Fatalf("应删除 alpha 链接：%+v", result.Removed)
	}
	assertLinkMissing(t, filepath.Join(targetSkillDir, "alpha"))
}

func TestRemoveTargetSkillLinkOnlyDeletesLinksInsideTargetSkillDir(t *testing.T) {
	root := testsupport.TestDir(t, "remove-single-skill-link")
	sourceRoot := filepath.Join(root, "source")
	targetSkillDir := filepath.Join(root, "target", "skills")
	otherSkillDir := filepath.Join(root, "other", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	writeTestSkill(t, sourceRoot, "beta")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(otherSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(targetSkillDir, "alpha"))
	mustSymlink(t, filepath.Join(sourceRoot, "beta"), filepath.Join(otherSkillDir, "beta"))
	if err := os.MkdirAll(filepath.Join(targetSkillDir, "real-dir"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	store := writeLinkStore(t, root)

	removed, err := RemoveTargetSkillLinkInner(store, "codex", support.DisplayPath(filepath.Join(targetSkillDir, "alpha")))
	if err != nil {
		t.Fatalf("RemoveTargetSkillLinkInner: %v", err)
	}
	if removed.SkillName != "alpha" {
		t.Fatalf("removed：%+v", removed)
	}
	assertLinkMissing(t, filepath.Join(targetSkillDir, "alpha"))
	if _, err := os.Stat(filepath.Join(sourceRoot, "alpha", "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}

	// 真实目录不是链接，拒绝且不删除内容。
	if _, err := RemoveTargetSkillLinkInner(store, "codex", support.DisplayPath(filepath.Join(targetSkillDir, "real-dir"))); err == nil {
		t.Fatal("真实目录必须被拒绝")
	}
	if info, err := os.Stat(filepath.Join(targetSkillDir, "real-dir")); err != nil || !info.IsDir() {
		t.Fatalf("真实目录应完好：%v", err)
	}

	// 别的 target 目录下的链接不能借用 codex 的 id 删除。
	if _, err := RemoveTargetSkillLinkInner(store, "codex", support.DisplayPath(filepath.Join(otherSkillDir, "beta"))); err == nil {
		t.Fatal("别的 target 目录下的链接必须被拒绝")
	}
	if _, err := os.Lstat(filepath.Join(otherSkillDir, "beta")); err != nil {
		t.Fatalf("别的链接应完好：%v", err)
	}

	if _, err := RemoveTargetSkillLinkInner(store, "missing", support.DisplayPath(filepath.Join(targetSkillDir, "alpha"))); err == nil {
		t.Fatal("不存在的 target 必须报错")
	}
}

// 弹窗把扫描出来的 destination_path 原样回传给单条移除命令，所以展示用的
// 路径字符串必须能通过后端的目录归属校验。
func TestRemoveTargetSkillLinkAcceptsDestinationPathFromSyncOptions(t *testing.T) {
	root := testsupport.TestDir(t, "remove-link-round-trip")
	unmanagedRoot := filepath.Join(root, "external")
	targetSkillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, unmanagedRoot, "external")
	if err := os.MkdirAll(targetSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(unmanagedRoot, "external"), filepath.Join(targetSkillDir, "external"))

	store := writeLinkStore(t, root)
	options, err := BuildSyncTargetOptions(store)
	if err != nil {
		t.Fatalf("BuildSyncTargetOptions: %v", err)
	}
	var option *SyncTargetOption
	for i := range options {
		if options[i].ID == "codex" {
			option = &options[i]
		}
	}
	if option == nil || len(option.Links) == 0 {
		t.Fatal("unmanaged link is scanned")
	}
	link := option.Links[0]
	if link.State != LinkStateUnmanaged {
		t.Fatalf("state：%s", link.State)
	}
	removed, err := RemoveTargetSkillLinkInner(store, option.ID, link.DestinationPath)
	if err != nil {
		t.Fatalf("RemoveTargetSkillLinkInner: %v", err)
	}
	if removed.SkillName != "external" {
		t.Fatalf("removed：%+v", removed)
	}
	assertLinkMissing(t, filepath.Join(targetSkillDir, "external"))
	if _, err := os.Stat(filepath.Join(unmanagedRoot, "external", "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}
}

// 目标目录读不出来时必须上报：静默跳过会让调用方把「链接没删掉」当成功。
func TestRemoveSourceSyncReportsUnreadableTargetDirectory(t *testing.T) {
	root := testsupport.TestDir(t, "remove-sync-unreadable-target")
	writeTestSkill(t, filepath.Join(root, "source"), "alpha")
	// skill_dir 指向一个真实文件：read_dir 会失败，且不是「目录不存在」。
	brokenSkillDir := filepath.Join(root, "target", "skills")
	if err := os.MkdirAll(filepath.Dir(brokenSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, brokenSkillDir, "not a directory")

	store := writeLinkStore(t, root)

	if _, err := RemoveSourceSyncInner(store, "src", []string{"codex"}); err == nil {
		t.Fatal("读不出的目标目录必须上报")
	}
}

// ---------------------------------------------------------------------------
// 批量导入 JSON 解析
// ---------------------------------------------------------------------------

func TestParseBatchGitSkillImportSourcesRequiresJSONArray(t *testing.T) {
	// 单个对象不是数组：批量导入的最小单位是一个来源列表。
	if _, err := ParseBatchGitSkillImportSources(`{"repo": "https://example.com/repo.git"}`); err == nil {
		t.Fatal("单个对象必须被拒绝")
	}
}

func TestParseBatchGitSkillImportSourcesRejectsInvalidItem(t *testing.T) {
	if _, err := ParseBatchGitSkillImportSources(`[{"repo": "https://example.com/repo.git", "ref": "main", "include_name_patterns": [""]}]`); err == nil {
		t.Fatal("空 pattern 必须被拒绝")
	}
}

func TestParseBatchGitSkillImportSourcesAcceptsGitSources(t *testing.T) {
	sources, err := ParseBatchGitSkillImportSources(`[{"repo": "https://example.com/repo.git", "ref": "main", "description": " 官方技能库 ", "include_name_patterns": ["archive/*"]}]`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources: %d", len(sources))
	}
	source := sources[0]
	if source.Repo != "https://example.com/repo.git" || source.Ref != "main" {
		t.Fatalf("repo/ref：%+v", source)
	}
	if source.Description == nil || *source.Description != "官方技能库" {
		t.Fatalf("description 应 trim：%+v", source.Description)
	}
	if !equalStrings(source.IncludeNamePatterns, []string{"archive/*"}) {
		t.Fatalf("patterns：%v", source.IncludeNamePatterns)
	}
}

func TestParseBatchGitSkillImportSourcesFoldsBlankDescriptionToNone(t *testing.T) {
	sources, err := ParseBatchGitSkillImportSources(`[{"repo": "https://example.com/repo.git", "ref": "main", "description": "   "}]`)
	if err != nil {
		t.Fatalf("空白描述应被接受：%v", err)
	}
	if sources[0].Description != nil {
		t.Fatalf("空白描述应折叠为 nil：%+v", sources[0].Description)
	}
}

// ---------------------------------------------------------------------------
// 技能发现与 include patterns
// ---------------------------------------------------------------------------

func TestIncludePatternsMatchNameAndPath(t *testing.T) {
	root := testsupport.TestDir(t, "manager-include-skill-name")
	writeTestSkill(t, root, filepath.Join("skills", "beeper"))
	writeTestSkill(t, root, filepath.Join("skills", "agent-helper"))

	skills, err := DiscoverSkillsInDirectory(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	ApplyIncludePatterns(&skills, []string{"agent-*"}, nil)

	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	if !equalStrings(names, []string{"agent-helper"}) {
		t.Fatalf("应只剩 agent-helper：%v", names)
	}
}

func TestDiscoveredSkillsCarryFrontmatterDescription(t *testing.T) {
	root := testsupport.TestDir(t, "manager-skill-description")
	writeTestSkill(t, root, filepath.Join("skills", "tdd"), "---\nname: tdd\ndescription: Test-driven development for bugs.\n---\n\n# TDD\n")
	writeTestSkill(t, root, filepath.Join("skills", "legacy"), "正文没有 frontmatter")

	skills, err := DiscoverSkillsInDirectory(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var tdd, legacy *DiscoveredSkill
	for i := range skills {
		switch skills[i].Name {
		case "tdd":
			tdd = &skills[i]
		case "legacy":
			legacy = &skills[i]
		}
	}
	if tdd == nil || legacy == nil {
		t.Fatalf("两个 skill 都应被发现：%+v", skills)
	}
	if tdd.Description == nil || *tdd.Description != "Test-driven development for bugs." {
		t.Fatalf("tdd description：%+v", tdd.Description)
	}
	if legacy.Description != nil {
		t.Fatalf("无 frontmatter 应无备注：%+v", legacy.Description)
	}
}

func TestSkillDescriptionFoldsWhitespaceAndCapsLength(t *testing.T) {
	long := strings.Repeat("词", 600)
	contents := fmt.Sprintf("---\ndescription: >-\n  第一行\n  第二行\n  %s\n---\n\n正文\n", long)

	description := SkillDescriptionFromContents(contents)
	if description == nil {
		t.Fatal("description parsed")
	}
	if !strings.HasPrefix(*description, "第一行 第二行 ") {
		t.Fatalf("应折叠换行与空白：%q", *description)
	}
	runes := []rune(*description)
	if len(runes) != 500 {
		t.Fatalf("应截断到 500 字符：%d", len(runes))
	}
}

func TestSkillSourceDescriptionNormalizesOnParse(t *testing.T) {
	env := envOf(t)
	configPath := filepath.Join(t.TempDir(), "reins-source-description-test.yaml")
	raw := `skill_sources:
  - id: notes
    type: local
    root_path: /tmp/notes
    description: "  个人笔记技能库  "
  - id: blank
    type: local
    root_path: /tmp/blank
    description: "   "
  - id: missing
    type: local
    root_path: /tmp/missing
`

	config, err := parseManagerConfig(raw, configPath, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	descriptionOf := func(id string) *string {
		source, ok := config.SkillSources[id]
		if !ok || source.Kind != rawSourceKindLocal {
			return nil
		}
		return source.Description
	}
	if got := descriptionOf("notes"); got == nil || *got != "个人笔记技能库" {
		t.Fatalf("描述应 trim：%+v", got)
	}
	// 纯空白与缺字段都按未填处理。
	if got := descriptionOf("blank"); got != nil {
		t.Fatalf("空白描述应折叠为 nil：%+v", got)
	}
	if got := descriptionOf("missing"); got != nil {
		t.Fatalf("缺字段应折叠为 nil：%+v", got)
	}
}

func TestPartitionExcludedReturnsSkillsNotKept(t *testing.T) {
	all := []DiscoveredSkill{
		{
			Name:          "beeper",
			RelativePath:  "skills/beeper",
			SkillFilePath: "/tmp/source/skills/beeper/SKILL.md",
			SourcePath:    strPtr("/tmp/source/skills/beeper"),
		},
		{
			Name:          "agent-helper",
			RelativePath:  "skills/agent-helper",
			SkillFilePath: "/tmp/source/skills/agent-helper/SKILL.md",
			SourcePath:    strPtr("/tmp/source/skills/agent-helper"),
		},
	}
	kept := []DiscoveredSkill{all[1]}

	excluded := partitionExcluded(all, kept)

	if len(excluded) != 1 || excluded[0].Name != "beeper" {
		t.Fatalf("应只剩 beeper：%+v", excluded)
	}
}

// ---------------------------------------------------------------------------
// 默认模板与预设
// ---------------------------------------------------------------------------

func TestDefaultConfigTemplateParsesWithBuiltinTargets(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "reins-default-template-test.yaml")
	env := envOf(t)
	config, err := parseManagerConfig(DefaultConfigTemplate(env), configPath, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(config.Targets) != 6 {
		t.Fatalf("模板应有 6 个 target：%d", len(config.Targets))
	}
	for _, id := range []string{"codex", "claude", "opencode", "zcode", "pi"} {
		if _, ok := config.Targets[AgentTargetID(id)]; !ok {
			t.Fatalf("缺 %s target", id)
		}
	}
	for id, target := range config.Targets {
		if strings.TrimSpace(target.SkillDir) == "" {
			t.Fatalf("%s 的 skill_dir 不应为空", id)
		}
	}

	// 模板渲染与清单解析必须用同一份重定向判据：未设变量时 codex / claude
	// 的路径都写 ~ 缩写（解析时按当前 HOME 展开），设了 env 重定向的工具
	// 才固化绝对路径。两者分叉会让首次启动生成的 config.yaml 指向别处。
	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}
	codex := config.Targets[AgentTargetID("codex")]
	// codex 的 skills 不随 CODEX_HOME 搬移：只有 config.toml 在 CODEX_HOME 下。
	if codex.SkillDir != joinPath(home, ".agents/skills") {
		t.Fatalf("codex skill_dir：%s", codex.SkillDir)
	}
	if codex.ConfigPath == nil || *codex.ConfigPath != joinPath(home, ".codex/config.toml") {
		t.Fatalf("codex config_path：%+v", codex.ConfigPath)
	}

	claude := config.Targets[AgentTargetID("claude")]
	if claude.SkillDir != joinPath(home, ".claude/skills") {
		t.Fatalf("claude skill_dir：%s", claude.SkillDir)
	}
	if claude.ConfigPath == nil || *claude.ConfigPath != joinPath(home, ".claude/.claude.json") {
		t.Fatalf("claude config_path：%+v", claude.ConfigPath)
	}

	if _, ok := config.Targets[AgentTargetID("dsh")]; ok {
		t.Fatal("dsh 不在默认模板（TemplateAbsent）")
	}

	opencode := config.Targets[AgentTargetID("opencode")]
	if !strings.HasSuffix(opencode.SkillDir, ".config/opencode/skills") {
		t.Fatalf("opencode skill_dir：%s", opencode.SkillDir)
	}
	if opencode.ConfigPath == nil || *opencode.ConfigPath != joinPath(home, ".config/opencode/opencode.json") {
		t.Fatalf("opencode config_path：%+v", opencode.ConfigPath)
	}
	if opencode.MCPConfigPrefix != "mcp.servers" || opencode.MCPConfigType != agents.McpConfigOpenCode {
		t.Fatalf("opencode mcp 布局：%+v", opencode)
	}

	zcode := config.Targets[AgentTargetID("zcode")]
	if !strings.HasSuffix(zcode.SkillDir, ".zcode/skills") {
		t.Fatalf("zcode skill_dir：%s", zcode.SkillDir)
	}
	if zcode.ConfigPath == nil || *zcode.ConfigPath != joinPath(home, ".zcode/cli/config.json") {
		t.Fatalf("zcode config_path：%+v", zcode.ConfigPath)
	}
	if zcode.MCPConfigPrefix != "mcp.servers" || zcode.MCPConfigType != agents.McpConfigCommon {
		t.Fatalf("zcode mcp 布局：%+v", zcode)
	}

	pi := config.Targets[AgentTargetID("pi")]
	if !strings.HasSuffix(pi.SkillDir, ".pi/agent/skills") {
		t.Fatalf("pi skill_dir：%s", pi.SkillDir)
	}
	// pi ≥0.99 支持 MCP：模板固化 <agentDir>/mcp.json（env 视图与模板同源）。
	wantPiDir, piOK := env.Value(agents.RootPiAgentDir.Redirect)
	if !piOK {
		wantPiDir = joinPath(home, ".pi/agent")
	}
	if pi.ConfigPath == nil || *pi.ConfigPath != joinPath(wantPiDir, "mcp.json") {
		t.Fatalf("pi config_path：%+v", pi.ConfigPath)
	}
	if pi.MCPConfigPrefix != "mcpServers" || pi.MCPConfigType != agents.McpConfigCommon {
		t.Fatalf("pi mcp 布局：%+v", pi)
	}
}

func TestGlobalPiTargetWithoutMcpSectionFallsBackToPreset(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "reins-pi-target-parse-test.yaml")
	raw := `targets:
  pi:
    enabled: true
    skill_dir: ~/.pi/agent/skills
`

	// 环境与解析同源：skill_dir 里的 ~ 按当前 HOME 展开（parse 侧口径），
	// mcp 段缺失则回落内置默认（<agentDir>/mcp.json）。
	env := envOf(t)
	config, err := parseManagerConfig(raw, configPath, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pi, ok := config.Targets[AgentTargetID("pi")]
	if !ok {
		t.Fatal("pi target exists")
	}

	// 省略 mcp 段时回落到内置默认：<agentDir>/mcp.json + mcpServers。
	wantPiDir, piOK := env.Value(agents.RootPiAgentDir.Redirect)
	if !piOK {
		home, ok := support.UserHomeDir()
		if !ok {
			t.Fatal("测试环境缺少 HOME")
		}
		wantPiDir = joinPath(home, ".pi/agent")
	}
	if pi.ConfigPath == nil || *pi.ConfigPath != joinPath(wantPiDir, "mcp.json") {
		t.Fatalf("pi config_path：%+v", pi.ConfigPath)
	}
	if pi.MCPConfigPrefix != "mcpServers" {
		t.Fatalf("pi prefix：%s", pi.MCPConfigPrefix)
	}
}

func TestTargetPresetsCoverBuiltinAgentsWithEnvResolvedPaths(t *testing.T) {
	// 预设路径在派生时读取 GROK_HOME / PI_CODING_AGENT_DIR，先装好隔离环境。
	_, dir := newTestStore(t, "presets-env")
	grokRoot := filepath.Join(dir, "grok-home")
	piRoot := filepath.Join(dir, "pi-agent-dir")
	if err := os.MkdirAll(grokRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(piRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("GROK_HOME", grokRoot)
	t.Setenv("PI_CODING_AGENT_DIR", piRoot)

	presets := TargetPresetsInner(envOf(t))
	if len(presets) != 7 {
		t.Fatalf("七个内置工具各一条预设：%d", len(presets))
	}
	byID := func(id string) *TargetPreset {
		for i := range presets {
			if string(presets[i].TargetID) == id {
				return &presets[i]
			}
		}
		t.Fatalf("缺少 %s 预设", id)
		return nil
	}

	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}

	codex := byID("codex")
	if codex.Label != "Codex" || codex.SkillDir != joinPath(home, ".agents/skills") {
		t.Fatalf("codex preset：%+v", codex)
	}
	if codex.ConfigPath == nil || *codex.ConfigPath != joinPath(home, ".codex/config.toml") {
		t.Fatalf("codex config_path：%+v", codex.ConfigPath)
	}
	if codex.MCPConfigPrefix != "mcp_servers" {
		t.Fatalf("codex prefix：%s", codex.MCPConfigPrefix)
	}

	claude := byID("claude")
	if claude.Label != "Claude Code" || claude.SkillDir != joinPath(home, ".claude/skills") {
		t.Fatalf("claude preset：%+v", claude)
	}
	if claude.ConfigPath == nil || *claude.ConfigPath != joinPath(home, ".claude/.claude.json") {
		t.Fatalf("claude config_path：%+v", claude.ConfigPath)
	}
	if claude.MCPConfigPrefix != "mcpServers" {
		t.Fatalf("claude prefix：%s", claude.MCPConfigPrefix)
	}

	opencode := byID("opencode")
	if opencode.Label != "OpenCode" || opencode.SkillDir != joinPath(home, ".config/opencode/skills") {
		t.Fatalf("opencode preset：%+v", opencode)
	}
	if opencode.ConfigPath == nil || *opencode.ConfigPath != joinPath(home, ".config/opencode/opencode.json") {
		t.Fatalf("opencode config_path：%+v", opencode.ConfigPath)
	}
	if opencode.MCPConfigPrefix != "mcp.servers" {
		t.Fatalf("opencode prefix：%s", opencode.MCPConfigPrefix)
	}

	zcode := byID("zcode")
	if zcode.Label != "ZCode" || zcode.SkillDir != joinPath(home, ".zcode/skills") {
		t.Fatalf("zcode preset：%+v", zcode)
	}
	if zcode.ConfigPath == nil || *zcode.ConfigPath != joinPath(home, ".zcode/cli/config.json") {
		t.Fatalf("zcode config_path：%+v", zcode.ConfigPath)
	}
	if zcode.MCPConfigPrefix != "mcp.servers" {
		t.Fatalf("zcode prefix：%s", zcode.MCPConfigPrefix)
	}

	// grokbuild / pi 的路径跟随 env 重定向：这正是前端静态预设无法预填、
	// 需要后端下发的原因。
	grokbuild := byID("grokbuild")
	if grokbuild.Label != "Grok Build" || grokbuild.SkillDir != joinPath(grokRoot, "skills") {
		t.Fatalf("grokbuild preset：%+v", grokbuild)
	}
	if grokbuild.ConfigPath == nil || *grokbuild.ConfigPath != joinPath(grokRoot, "config.toml") {
		t.Fatalf("grokbuild config_path：%+v", grokbuild.ConfigPath)
	}
	if grokbuild.MCPConfigPrefix != "mcp_servers" {
		t.Fatalf("grokbuild prefix：%s", grokbuild.MCPConfigPrefix)
	}

	pi := byID("pi")
	if pi.Label != "Pi" || pi.SkillDir != joinPath(piRoot, "skills") {
		t.Fatalf("pi preset：%+v", pi)
	}
	if pi.ConfigPath == nil || *pi.ConfigPath != joinPath(piRoot, "mcp.json") {
		t.Fatalf("pi config_path：%+v", pi.ConfigPath)
	}
	if pi.MCPConfigPrefix != "mcpServers" {
		t.Fatalf("pi prefix：%s", pi.MCPConfigPrefix)
	}

	// dsh 按现状 defaults：cordis patch 路径 + 空 prefix。
	dsh := byID("dsh")
	if dsh.Label != "DeepSeek Harness" || dsh.SkillDir != joinPath(home, ".dsh/skills") {
		t.Fatalf("dsh preset：%+v", dsh)
	}
	if dsh.ConfigPath == nil || *dsh.ConfigPath != joinPath(home, ".dsh/cordis.patch.yml") {
		t.Fatalf("dsh config_path：%+v", dsh.ConfigPath)
	}
	if dsh.MCPConfigPrefix != "" {
		t.Fatalf("dsh prefix：%q", dsh.MCPConfigPrefix)
	}
}

// 编辑页展示的格式说明是面向用户的契约文案，逐字锁定，改动须有意为之。
func TestTargetViewCarriesMCPFormatDescriptionPerConfigType(t *testing.T) {
	target := resolvedTargetFor("codex", t.TempDir())
	view := targetToView(target)
	if want := "通用 MCP 条目（标准 command/args/env），按配置文件扩展名写入 JSON 或 TOML 的 mcp 节点下。"; view.MCPFormatDescription != want {
		t.Fatalf("common：%s", view.MCPFormatDescription)
	}

	target.MCPConfigType = agents.McpConfigOpenCode
	if want := "OpenCode 专属格式（JSON，带 $schema 头，command 为数组、environment 键），写入 mcp.servers 根。"; targetToView(target).MCPFormatDescription != want {
		t.Fatalf("opencode：%s", targetToView(target).MCPFormatDescription)
	}

	target.MCPConfigType = agents.McpConfigGrokBuild
	if want := "Grok Build TOML 格式（远端类型用 headers 携带自定义头），写入 config.toml。"; targetToView(target).MCPFormatDescription != want {
		t.Fatalf("grokbuild：%s", targetToView(target).MCPFormatDescription)
	}

	target.MCPConfigType = agents.McpConfigDsh
	if want := "DeepSeek Harness 的 Cordis patch YAML（insert/remove 操作列表，按 serverName 定位，无 configPrefix）。"; targetToView(target).MCPFormatDescription != want {
		t.Fatalf("dsh：%s", targetToView(target).MCPFormatDescription)
	}
}

// 编辑页展示的格式示例与 desired 条目构造函数的产物形状逐字锁定，
// 键名或结构调整时必须同步改示例并有意为之。
func TestTargetViewCarriesMCPFormatExamplePerConfigType(t *testing.T) {
	// dsh 条目的 cwd 读 home，先装隔离环境让断言确定。
	newTestStore(t, "mcp-format-examples")
	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}

	labels := func(view *TargetConfigView) []string {
		result := make([]string, 0, len(view.MCPFormatExamples))
		for _, example := range view.MCPFormatExamples {
			result = append(result, example.Label)
		}
		return result
	}
	body := func(view *TargetConfigView, index int) string {
		return view.MCPFormatExamples[index].Body
	}

	// codex:common 形态，本地与远端各一段。
	target := resolvedTargetFor("codex", t.TempDir())
	target.MCPConfigPrefix = "mcpServers"
	view := targetToView(target)
	if want := []string{"本地命令（stdio）", "远端地址（http / sse）"}; !equalStrings(labels(&view), want) {
		t.Fatalf("labels：%v", labels(&view))
	}
	// Go 侧 JSON 序列化（encoding/json）与 Rust serde_json 同为键序字典序、
	// 2 空格缩进；逐字节钉住。
	wantStdio := `{
  "mcpServers": {
    "my-server": {
      "args": [
        "server.js"
      ],
      "command": "node",
      "env": {
        "KEY": "value"
      },
      "type": "stdio"
    }
  }
}
`
	if got := body(&view, 0); got != wantStdio {
		t.Fatalf("common stdio 示例漂移：\n%s", got)
	}
	wantRemote := `{
  "mcpServers": {
    "my-server": {
      "headers": {
        "X-Key": "value"
      },
      "type": "http",
      "url": "https://example.com/mcp"
    }
  }
}
`
	if got := body(&view, 1); got != wantRemote {
		t.Fatalf("common remote 示例漂移：\n%s", got)
	}

	// zcode 的点分 prefix 结构化嵌套为 JSON 层级。
	target.MCPConfigPrefix = "mcp.servers"
	view = targetToView(target)
	wantNested := `{
  "mcp": {
    "servers": {
      "my-server": {
        "args": [
          "server.js"
        ],
        "command": "node",
        "env": {
          "KEY": "value"
        },
        "type": "stdio"
      }
    }
  }
}
`
	if got := body(&view, 0); got != wantNested {
		t.Fatalf("zcode 嵌套示例漂移：\n%s", got)
	}

	// OpenCode:专属形态（local/remote，command 数组与 environment 键）。
	target.MCPConfigType = agents.McpConfigOpenCode
	view = targetToView(target)
	if want := []string{"本地命令（stdio）", "远端地址（http / sse）"}; !equalStrings(labels(&view), want) {
		t.Fatalf("opencode labels：%v", labels(&view))
	}
	wantOpenCodeLocal := `{
  "mcp": {
    "servers": {
      "my-server": {
        "command": [
          "node",
          "server.js"
        ],
        "environment": {
          "KEY": "value"
        },
        "type": "local"
      }
    }
  }
}
`
	if got := body(&view, 0); got != wantOpenCodeLocal {
		t.Fatalf("opencode stdio 示例漂移：\n%s", got)
	}
	wantOpenCodeRemote := `{
  "mcp": {
    "servers": {
      "my-server": {
        "headers": {
          "X-Key": "value"
        },
        "type": "remote",
        "url": "https://example.com/mcp"
      }
    }
  }
}
`
	if got := body(&view, 1); got != wantOpenCodeRemote {
		t.Fatalf("opencode remote 示例漂移：\n%s", got)
	}

	// GrokBuild:TOML 表头，本地与远端各一段（Go 侧 BurntSushi 的拼版与
	// Rust toml crate 不同：父表头会显式写出；这里钉 Go 的确定性输出）。
	target.MCPConfigType = agents.McpConfigGrokBuild
	target.MCPConfigPrefix = "mcp_servers"
	view = targetToView(target)
	wantGrokStdio := `[mcp_servers]
[mcp_servers.my-server]
args = ["server.js"]
command = "node"
[mcp_servers.my-server.env]
KEY = "value"
`
	if got := body(&view, 0); got != wantGrokStdio {
		t.Fatalf("grokbuild stdio 示例漂移：\n%s", got)
	}
	wantGrokRemote := `[mcp_servers]
[mcp_servers.my-server]
url = "https://example.com/mcp"
[mcp_servers.my-server.headers]
X-Key = "value"
`
	if got := body(&view, 1); got != wantGrokRemote {
		t.Fatalf("grokbuild remote 示例漂移：\n%s", got)
	}

	// DSH:patch YAML，无 prefix 概念，stdio 与 streamable-http 各一段。
	target.MCPConfigType = agents.McpConfigDsh
	view = targetToView(target)
	if want := []string{"本地命令（stdio）", "远端地址（http / sse）"}; !equalStrings(labels(&view), want) {
		t.Fatalf("dsh labels：%v", labels(&view))
	}
	wantDshRemote := `- insert:
    - id: reins-mcp-my-server
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        transport: streamable-http
        serverName: my-server
        url: https://example.com/mcp
        headers:
          X-Key: value
`
	if got := body(&view, 1); got != wantDshRemote {
		t.Fatalf("dsh remote 示例漂移：\n%s", got)
	}
	wantDshStdio := fmt.Sprintf(`- insert:
    - id: reins-mcp-my-server
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: my-server
        transport: stdio
        command: node
        args:
          - server.js
        env:
          KEY: value
        cwd: %s
        toolCallTimeoutMs: 60000
        failOnStartupError: false
`, home)
	if got := body(&view, 0); got != wantDshStdio {
		t.Fatalf("dsh stdio 示例漂移：\n%s", got)
	}
}

func TestPiPresetAndTemplateFollowPiCodingAgentDir(t *testing.T) {
	_, dir := newTestStore(t, "pi-redirect")
	redirected := filepath.Join(dir, "pi-agent-dir-redirect")
	if err := os.MkdirAll(redirected, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", redirected)

	presets := TargetPresetsInner(envOf(t))
	var preset *TargetPreset
	for i := range presets {
		if string(presets[i].TargetID) == "pi" {
			preset = &presets[i]
		}
	}
	if preset == nil {
		t.Fatal("缺少 pi 预设")
	}
	if preset.ConfigPath == nil || *preset.ConfigPath != joinPath(redirected, "mcp.json") {
		t.Fatalf("preset config_path：%+v", preset.ConfigPath)
	}
	if preset.MCPConfigPrefix != "mcpServers" {
		t.Fatalf("prefix：%s", preset.MCPConfigPrefix)
	}

	// 模板在生成时把重定向路径固化为具体值，config.yaml 是之后的权威。
	path := filepath.Join(t.TempDir(), "reins-pi-defaults.yaml")
	env := envOf(t)
	config, err := parseManagerConfig(DefaultConfigTemplate(env), path, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pi, ok := config.Targets[AgentTargetID("pi")]
	if !ok {
		t.Fatal("pi target exists")
	}
	if pi.SkillDir != joinPath(redirected, "skills") {
		t.Fatalf("pi skill_dir：%s", pi.SkillDir)
	}
	if pi.ConfigPath == nil || *pi.ConfigPath != joinPath(redirected, "mcp.json") {
		t.Fatalf("pi config_path：%+v", pi.ConfigPath)
	}
}

// ---------------------------------------------------------------------------
// 遗留键、project id 与 inspection
// ---------------------------------------------------------------------------

// target / MCP / 项目 agent 的启停开关已废弃：旧配置里的 enabled 键被忽略、
// 不报错，也不再把目标当作「停用」。这条同时守住反序列化兼容与语义变化。
func TestLegacyEnabledKeyIsIgnoredAndNeverDisables(t *testing.T) {
	store, root := newTestStore(t, "legacy-enabled")
	skillDir := filepath.Join(root, "codex-skills")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  codex:
    enabled: false
    skill_dir: %s
    mcp:
      enabled: false
      config_path: %s
      config_prefix: mcp_servers
      config_type: common
projects:
  my-app:
    path: %s
    agents:
      codex:
        enabled: false
mcps:
- name: fs-server
  enabled: false
  transport: stdio
  command: npx
`, skillDir, filepath.Join(root, "codex.toml"), filepath.Join(root, "my-app")))

	// 照常解析（旧键不报错），且线格式里不再出现 enabled 字段。
	state, err := WorkspaceStateInner(store)
	if err != nil {
		t.Fatalf("WorkspaceStateInner: %v", err)
	}
	if state.Document == nil || state.Document.Config == nil {
		t.Fatal("config view")
	}
	view := state.Document.Config
	var codex *TargetConfigView
	for i := range view.Targets {
		if string(view.Targets[i].ID) == "codex" {
			codex = &view.Targets[i]
		}
	}
	if codex == nil {
		t.Fatal("codex target view")
	}
	if codex.ConfigPath == nil || *codex.ConfigPath != filepath.Join(root, "codex.toml") {
		t.Fatalf("codex config_path：%+v", codex.ConfigPath)
	}
	if len(view.Projects) != 1 || len(view.Projects[0].Agents) != 1 {
		t.Fatalf("projects view：%+v", view.Projects)
	}

	// 不再有「停用目标」这一说：MCP 照常应用（此前会报「目标已禁用」）。
	applied, err := ApplyMCPToTargetInner(store, "fs-server", "codex")
	if err != nil {
		t.Fatalf("ApplyMCPToTargetInner: %v", err)
	}
	if applied.Action != "apply" {
		t.Fatalf("action：%s", applied.Action)
	}
	if _, err := os.Stat(filepath.Join(root, "codex.toml")); err != nil {
		t.Fatalf("MCP 配置应已写入：%v", err)
	}
}

// 项目 id 是复合地址的前半段：含 `:` 会让地址不可拆（前后端都按第一个冒号切），
// 含 `/` 会破坏前端按展示 label 分组。写入与解析两侧都必须拒绝，且解析阶段就
// 报清楚——不能等到用户点某个 target 才报「target 不存在」。
func TestProjectIDRejectsColonAndSlash(t *testing.T) {
	store, root := newTestStore(t, "project-id-charset")
	writeFileT(t, store.ConfigPath(), "{}\n")

	for _, bad := range []string{"a:b", "web/apps"} {
		_, err := CreateWorkspaceProjectInner(store, ProjectMutationInput{
			ProjectID: bad,
			Path:      filepath.Join(root, "project"),
			Agents:    []string{"codex"},
		})
		if err == nil {
			t.Fatalf("含 : 或 / 的 project id 必须被拒绝：%s", bad)
		}
		if !strings.Contains(err.Error(), "不能包含") {
			t.Fatalf("错误文案要说清原因：%v", err)
		}
	}

	// 手写的 config.yaml 同样在解析阶段被拦下，而不是留到寻址时才暴露。
	writeFileT(t, store.ConfigPath(), fmt.Sprintf("projects:\n  a:b:\n    path: %s\n    agents:\n      codex: {}\n", filepath.Join(root, "project")))
	if _, err := store.Parse(); err == nil {
		t.Fatal("解析阶段就要拒绝含 : 的 project id")
	} else if !strings.Contains(err.Error(), "不能包含") {
		t.Fatalf("错误文案：%v", err)
	}
}

// agent 名在 config.yaml 里写成大写（或带下划线）时，id 会被规范成小写连字符，
// 落点回退必须用规范化后的 id：否则会凭空造出一个 `<project>/Codex/skills`
// 空目录，用户真正在用的 `<project>/codex/skills` 不被识别。
func TestProjectAgentDirFollowsNormalizedAgentID(t *testing.T) {
	store, root := newTestStore(t, "project-agent-normalized")
	projectPath := filepath.Join(root, "my-app")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf("projects:\n  my-app:\n    path: %s\n    agents:\n      Custom: {}\n", projectPath))

	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	target := config.target(targetRef{projectID: "my-app", agentID: "custom"})
	if target == nil {
		t.Fatal("规范化后的 id 应能寻址到该 target")
	}

	// 没有内置项目布局的 agent 走回退路径：目录名必须是规范化后的 id。
	if target.SkillDir != filepath.Join(projectPath, "custom", "skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
	if target.ConfigPath != nil {
		t.Fatalf("config_path 应为 nil：%+v", target.ConfigPath)
	}
}

// inspection 也要带上落点归属：目标面板的列表用它标注「这个落点不归它」，
// 与同步弹窗同一份数据，不再让两个界面各自判断。
func TestInspectionReportsSkillDirLinkAttribution(t *testing.T) {
	store, root := newTestStore(t, "inspect-skill-dir-link")

	ownerSkillDir := filepath.Join(root, "owner", "skills")
	aliasSkillDir := filepath.Join(root, "alias", "skills")
	brokenSkillDir := filepath.Join(root, "broken", "skills")
	if err := os.MkdirAll(ownerSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(aliasSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(brokenSkillDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, ownerSkillDir, aliasSkillDir)
	mustSymlink(t, filepath.Join(root, "gone"), brokenSkillDir)

	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  owner:
    skill_dir: %s
  alias:
    skill_dir: %s
  broken:
    skill_dir: %s
`, ownerSkillDir, aliasSkillDir, brokenSkillDir))

	state, err := WorkspaceStateInner(store)
	if err != nil {
		t.Fatalf("WorkspaceStateInner: %v", err)
	}
	if state.Inspection == nil {
		t.Fatal("inspection")
	}
	inspection := state.Inspection
	byID := func(id string) *TargetInspection {
		for i := range inspection.Targets {
			if string(inspection.Targets[i].ID) == id {
				return &inspection.Targets[i]
			}
		}
		t.Fatalf("target 应在 inspection 里：%s", id)
		return nil
	}

	// 普通落点不标注。
	if byID("owner").SkillDirLink != nil {
		t.Fatalf("普通落点不应标注：%+v", byID("owner").SkillDirLink)
	}

	// 指向受管 target：给出命名归属与真实目录。
	link := byID("alias").SkillDirLink
	if link == nil {
		t.Fatal("alias 应标注")
	}
	if link.LinkedTargetID == nil || *link.LinkedTargetID != "owner" {
		t.Fatalf("命名归属应为 owner：%+v", link.LinkedTargetID)
	}
	if link.LinkTarget != support.DisplayPath(linkTarget(t, ownerSkillDir)) {
		t.Fatalf("链接目标应为 canonical 形态：%s", link.LinkTarget)
	}

	// 断链：叫不出名字，但仍要标注（内容在链接另一端）。
	broken := byID("broken").SkillDirLink
	if broken == nil {
		t.Fatal("断链也要标注")
	}
	if broken.LinkedTargetID != nil {
		t.Fatalf("断链叫不出名字：%+v", broken.LinkedTargetID)
	}
	if broken.LinkTarget != filepath.Join(root, "gone") {
		t.Fatalf("断链目标应回落原路径：%s", broken.LinkTarget)
	}

	// 两个别名各一条提示，普通 target 不提示（面板已按行标注，这里让其它消费方
	// 也能看到「它为什么不参与分发」）。
	if len(inspection.Warnings) != 2 {
		t.Fatalf("应为两个别名各一条提示：%+v", inspection.Warnings)
	}
	anyAliasWarning := false
	for _, warning := range inspection.Warnings {
		if strings.HasPrefix(warning, "alias ") {
			anyAliasWarning = true
		}
	}
	if !anyAliasWarning {
		t.Fatalf("warnings 应说明是哪个 target：%+v", inspection.Warnings)
	}
}

// ---------------------------------------------------------------------------
// MCP apply / preview / remove 主链路
// ---------------------------------------------------------------------------

func TestPiMcpApplyReadRemoveRoundtrip(t *testing.T) {
	store, root := newTestStore(t, "pi-mcp-roundtrip")
	piMcp := filepath.Join(root, "mcp.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  pi:
    skill_dir: %s
    mcp:
      config_path: %s
      config_prefix: mcpServers
      config_type: common
mcps:
- name: fs-server
  transport: stdio
  command: npx
  args:
  - -y
  - server-filesystem
`, filepath.Join(root, "pi-skills"), piMcp))

	preview, err := PreviewMCPTargetInner(store, "fs-server", "pi")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Format != "json" {
		t.Fatalf("format：%s", preview.Format)
	}
	entry := parseJSONT(t, preview.Content)
	if entry["type"] != "stdio" || entry["command"] != "npx" {
		t.Fatalf("entry：%+v", entry)
	}
	args, ok := entry["args"].([]any)
	if !ok || len(args) != 2 || args[0] != "-y" || args[1] != "server-filesystem" {
		t.Fatalf("args：%+v", entry["args"])
	}

	if _, err := ApplyMCPToTargetInner(store, "fs-server", "pi"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := ApplyMCPToTargetInner(store, "fs-server", "pi"); err != nil {
		t.Fatalf("apply twice: %v", err)
	}
	written := parseJSONT(t, readFileT(t, piMcp))
	if written["mcpServers"].(map[string]any)["fs-server"].(map[string]any)["command"] != "npx" {
		t.Fatalf("written：%+v", written)
	}

	if _, err := RemoveMCPFromTargetInner(store, "fs-server", "pi"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(readFileT(t, piMcp), "fs-server") {
		t.Fatal("条目应已删除")
	}
	mutation, err := RemoveMCPFromTargetInner(store, "fs-server", "pi")
	if err != nil {
		t.Fatalf("remove again: %v", err)
	}
	if mutation.Action != "noop" {
		t.Fatalf("二次删除应为 noop：%+v", mutation)
	}
}

// CODEX_HOME 是 codex 官方支持的目录重定位：workspace 的 MCP 落点必须与
// providers 认的配置文件是同一个。此前 workspace 只认 GROK_HOME /
// PI_CODING_AGENT_DIR，用户设了 CODEX_HOME 后两个域会写两份配置。
func TestCodexMcpApplyFollowsCodexHomeRedirect(t *testing.T) {
	store, home := newTestStore(t, "codex-home-redirect")
	codexHome := filepath.Join(home, "codex-home")
	if err := os.MkdirAll(codexHome, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	store.inner.env = support.FromEnv(agents.Redirects())

	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  codex:
    skill_dir: %s
mcps:
- name: fs-server
  transport: stdio
  command: npx
`, filepath.Join(codexHome, "skills")))

	// 省略 mcp 段 -> 回落内置默认：$CODEX_HOME/config.toml。
	preview, err := PreviewMCPTargetInner(store, "fs-server", "codex")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Format != "toml" {
		t.Fatalf("format：%s", preview.Format)
	}
	if preview.ConfigPath != filepath.Join(codexHome, "config.toml") {
		t.Fatalf("config_path：%s", preview.ConfigPath)
	}

	applied, err := ApplyMCPToTargetInner(store, "fs-server", "codex")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	written := filepath.Join(codexHome, "config.toml")
	if applied.UpdatedPath == nil || *applied.UpdatedPath != support.DisplayPath(written) {
		t.Fatalf("updated_path：%+v", applied.UpdatedPath)
	}
	if !strings.Contains(readFileT(t, written), "fs-server") {
		t.Fatal("config.toml 应包含条目")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("重定向生效时不该再写 ~/.codex：%v", err)
	}
}

func TestProjectPiAgentUsesPiProjectLayout(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "reins-pi-project-layout-test")
	configPath := filepath.Join(t.TempDir(), "reins-pi-project-layout-test.yaml")
	raw := fmt.Sprintf(`projects:
  my-app:
    path: %s
    agents:
      pi:
        enabled: true
`, projectPath)

	config, err := parseManagerConfig(raw, configPath, &support.Environment{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	project, ok := config.Projects["my-app"]
	if !ok {
		t.Fatal("project exists")
	}
	target, ok := project.Agents[AgentTargetID("pi")]
	if !ok {
		t.Fatal("pi project agent exists")
	}

	if target.SkillDir != filepath.Join(projectPath, ".pi", "skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
	if target.ConfigPath == nil || *target.ConfigPath != filepath.Join(projectPath, ".pi", "mcp.json") {
		t.Fatalf("config_path：%+v", target.ConfigPath)
	}
	if target.MCPConfigPrefix != "mcpServers" || target.MCPConfigType != agents.McpConfigCommon {
		t.Fatalf("mcp 布局：%+v", target)
	}
}

func TestProjectOpencodeAgentUsesOpencodeProjectLayout(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "reins-opencode-project-layout-test")
	configPath := filepath.Join(t.TempDir(), "reins-opencode-project-layout-test.yaml")
	raw := fmt.Sprintf(`projects:
  my-app:
    path: %s
    agents:
      opencode:
        enabled: true
`, projectPath)

	config, err := parseManagerConfig(raw, configPath, &support.Environment{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	project, ok := config.Projects["my-app"]
	if !ok {
		t.Fatal("project exists")
	}
	target, ok := project.Agents[AgentTargetID("opencode")]
	if !ok {
		t.Fatal("opencode project agent exists")
	}
	mcpConfigPath := filepath.Join(projectPath, "opencode.json")

	if target.SkillDir != filepath.Join(projectPath, ".opencode", "skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
	if target.ConfigPath == nil || *target.ConfigPath != mcpConfigPath {
		t.Fatalf("config_path：%+v", target.ConfigPath)
	}
	if target.MCPConfigPrefix != "mcp.servers" || target.MCPConfigType != agents.McpConfigOpenCode {
		t.Fatalf("mcp 布局：%+v", target)
	}
}

func TestProjectZcodeAgentUsesZcodeProjectLayout(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "reins-zcode-project-layout-test")
	configPath := filepath.Join(t.TempDir(), "reins-zcode-project-layout-test.yaml")
	raw := fmt.Sprintf(`projects:
  my-app:
    path: %s
    agents:
      zcode:
        enabled: true
`, projectPath)

	config, err := parseManagerConfig(raw, configPath, &support.Environment{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	project, ok := config.Projects["my-app"]
	if !ok {
		t.Fatal("project exists")
	}
	target, ok := project.Agents[AgentTargetID("zcode")]
	if !ok {
		t.Fatal("zcode project agent exists")
	}
	mcpConfigPath := filepath.Join(projectPath, ".zcode", "config.json")

	if target.SkillDir != filepath.Join(projectPath, ".zcode", "skills") {
		t.Fatalf("skill_dir：%s", target.SkillDir)
	}
	if target.ConfigPath == nil || *target.ConfigPath != mcpConfigPath {
		t.Fatalf("config_path：%+v", target.ConfigPath)
	}
	if target.MCPConfigPrefix != "mcp.servers" || target.MCPConfigType != agents.McpConfigCommon {
		t.Fatalf("mcp 布局：%+v", target)
	}
}

func TestDeleteWorkspaceMCPSkipsTargetsWithoutMcpConfig(t *testing.T) {
	store, root := newTestStore(t, "mcp-delete-skips-unconfigured")
	claudeConfig := filepath.Join(root, "claude.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`targets:
  skillonly:
    enabled: true
    skill_dir: %s
  claude:
    enabled: true
    skill_dir: %s
    mcp:
      enabled: true
      config_path: %s
      config_prefix: mcpServers
      config_type: common
mcps:
- name: test-server
  transport: stdio
  command: node
`, filepath.Join(root, "skillonly-skills"), filepath.Join(root, "claude-skills"), claudeConfig))
	writeFileT(t, claudeConfig, `{"mcpServers": {"test-server": {"type": "stdio", "command": "node"}}}`)

	result, err := DeleteWorkspaceMCPInner(store, "test-server")
	if err != nil {
		t.Fatalf("DeleteWorkspaceMCPInner: %v", err)
	}
	if result.ServerName != "test-server" {
		t.Fatalf("server_name：%s", result.ServerName)
	}
	// 无 MCP 配置文件的自定义 target（非内置 id，无 preset 兜底）删除时必须
	// 被跳过而不是中断整个删除流程。
	cleaned := readFileT(t, claudeConfig)
	if strings.Contains(cleaned, "test-server") {
		t.Fatalf("条目应已删除：%s", cleaned)
	}
}

// OpenCode 条目保留 transport 与 timeout；启用状态已不是 MCP 的字段，
// 写出的条目里不该再出现 enabled 键。
func TestOpencodeMcpEntryWritesTransportAndTimeoutWithoutEnabled(t *testing.T) {
	server := &ResolvedMcpConfig{
		Name:      "probe-server",
		Transport: TransportStdio,
		Command:   strPtr("node"),
		Args:      []string{"server.js"},
		Env:       map[string]string{},
		Timeout:   ptrUint64(3000),
	}

	entry, err := DesiredOpencodeMCP(server)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	object := entry.(map[string]any)
	if _, has := object["enabled"]; has {
		t.Fatal("条目里不该再出现 enabled 键")
	}
	if object["type"] != "local" {
		t.Fatalf("type：%+v", object["type"])
	}
	if want := []string{"node", "server.js"}; !equalStrings(object["command"].([]string), want) {
		t.Fatalf("command：%+v", object["command"])
	}
	if object["timeout"] != uint64(3000) {
		t.Fatalf("timeout：%+v", object["timeout"])
	}
}

// ---------------------------------------------------------------------------
// git 缓存
// ---------------------------------------------------------------------------

func TestParseGitOwnerRepoHandlesCommonURLForms(t *testing.T) {
	cases := []struct{ input, owner, repo string }{
		{"https://github.com/owner/repo.git", "owner", "repo"},
		{"https://github.com/owner/repo", "owner", "repo"},
		{"git@github.com:owner/repo.git", "owner", "repo"},
		{"ssh://git@github.com/owner/repo.git", "owner", "repo"},
		{"ssh://git@github.com:22/owner/repo.git", "owner", "repo"},
		{"https://gitlab.com/group/sub/repo.git", "sub", "repo"},
		{"owner/repo", "owner", "repo"},
	}
	for _, item := range cases {
		owner, repo, ok := parseGitOwnerRepo(item.input)
		if !ok {
			t.Fatalf("should parse: %s", item.input)
		}
		if owner != item.owner || repo != item.repo {
			t.Fatalf("%s：got %s/%s", item.input, owner, repo)
		}
	}
}

func TestParseGitOwnerRepoRejectsMalformed(t *testing.T) {
	if _, _, ok := parseGitOwnerRepo(""); ok {
		t.Fatal("空串应被拒绝")
	}
	if _, _, ok := parseGitOwnerRepo("just-a-name"); ok {
		t.Fatal("单段应被拒绝")
	}
}

func TestGitCacheDirForRepoUsesOwnerNameLayout(t *testing.T) {
	store := NewWorkspaceConfigStoreAt(filepath.Join(t.TempDir(), "reins-cache-layout"))
	dir, err := store.GitCacheDirForRepo("https://github.com/acme/skills.git")
	if err != nil {
		t.Fatalf("GitCacheDirForRepo: %v", err)
	}
	if filepath.Base(dir) != "skills" || filepath.Base(filepath.Dir(dir)) != "acme" || filepath.Base(filepath.Dir(filepath.Dir(dir))) != "git" {
		t.Fatalf("缓存目录布局：%s", dir)
	}
	if _, err := store.GitCacheDirForRepo("just-a-name"); err == nil {
		t.Fatal("坏 repo 串应报错")
	}
}

func TestGitCacheLastFetchedAtReadsRefreshMarker(t *testing.T) {
	store, _ := newTestStore(t, "git-cache-last-fetched-at")
	repo := "https://github.com/acme/skills.git"

	if got := gitCacheLastFetchedAtMs(store, repo); got != nil {
		t.Fatalf("无 marker 应为 nil：%+v", got)
	}

	markerPath := filepath.Join(mustGitCacheDir(t, store, repo), ".git", "reins-last-fetch")
	writeFileT(t, markerPath, "main")

	info, err := os.Stat(markerPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	expected := info.ModTime().UnixMilli()
	got := gitCacheLastFetchedAtMs(store, repo)
	if got == nil || *got != expected {
		t.Fatalf("marker mtime：%+v != %d", got, expected)
	}
}

func TestGitCacheRefreshRespects24HourInterval(t *testing.T) {
	now := time.Unix(2_000_000, 0)
	recent := now.Add(-24*time.Hour + time.Second)
	expired := now.Add(-24 * time.Hour)

	if GitCacheRefreshIsDue(strPtr("main"), strPtr("main"), &recent, now) {
		t.Fatal("未到期不应刷新")
	}
	if !GitCacheRefreshIsDue(strPtr("main"), strPtr("main"), &expired, now) {
		t.Fatal("到期应刷新")
	}
	if !GitCacheRefreshIsDue(strPtr("main"), strPtr("release"), &recent, now) {
		t.Fatal("ref 失配应刷新")
	}
	if !GitCacheRefreshIsDue(nil, strPtr("main"), &recent, now) {
		t.Fatal("无 marker 应刷新")
	}
	if !GitCacheRefreshIsDue(strPtr("main"), strPtr("main"), nil, now) {
		t.Fatal("无时间戳应刷新")
	}
	future := now.Add(time.Second)
	if !GitCacheRefreshIsDue(strPtr("main"), strPtr("main"), &future, now) {
		t.Fatal("时钟回拨应刷新")
	}
}

func TestDisplayPathStripsWindowsExtendedLengthPrefix(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("扩展长度前缀只在 Windows 出现")
	}
	if got := support.DisplayPath(`\\?\C:\Users\<user>\skills\agent-model-config`); got != `C:\Users\<user>\skills\agent-model-config` {
		t.Fatalf("got %s", got)
	}
	if got := support.DisplayPath(`\\?\UNC\server\share\skills`); got != `\\server\share\skills` {
		t.Fatalf("got %s", got)
	}
}

// 旧配置兼容路径：target 的 mcp_config_prefix 为空时，读侧按 id 回落到内置布局
// 再取条目（readExistingMCPEntries 的文档承诺）。此前这条路径没有任何测试
// 覆盖，删掉兜底实现全部用例仍然全绿。
func TestEmptyMcpPrefixFallsBackToBuiltinLayoutWhenReading(t *testing.T) {
	_, root := newTestStore(t, "reins-empty-prefix-read")
	env := envOf(t)

	// 表名必须与内置布局一致才能命中；从内置默认值取，不写死 `mcp_servers`。
	var builtinPrefix string
	for _, item := range builtinTargetDefaults(env) {
		if string(item.ID) == "codex" {
			builtinPrefix = item.ConfigPrefix
		}
	}
	if strings.TrimSpace(builtinPrefix) == "" {
		t.Fatal("内置前缀不该为空")
	}

	configPath := filepath.Join(root, "codex.toml")
	writeFileT(t, configPath, fmt.Sprintf("[%s.fs]\ncommand = \"npx\"\n\n[%s.other]\ncommand = \"go\"\n", builtinPrefix, builtinPrefix))

	target := &ResolvedTargetConfig{
		ID:         AgentTargetID("codex"),
		IsProject:  false,
		SkillDir:   filepath.Join(root, "skills"),
		ConfigPath: strPtr(configPath),
		// 旧配置留下的空前缀：读侧必须自行补回内置布局。
		MCPConfigPrefix: "",
		MCPConfigType:   agents.McpConfigCommon,
	}

	entries, err := ReadExistingMCPEntries(target, configPath, env)
	if err != nil {
		t.Fatalf("ReadExistingMCPEntries: %v", err)
	}
	if _, hasFS := entries["fs"]; !hasFS {
		t.Fatalf("空前缀时应回落到内置布局读到条目：%v", entries)
	}
	if _, hasOther := entries["other"]; !hasOther {
		t.Fatalf("空前缀时应回落到内置布局读到条目：%v", entries)
	}

	// 未内置的 id 补不出布局，只能按空前缀读到空表——不报错、不猜测。
	unknown := *target
	unknown.ID = AgentTargetID("not-a-builtin")
	entries, err = ReadExistingMCPEntries(&unknown, configPath, env)
	if err != nil {
		t.Fatalf("ReadExistingMCPEntries(unknown): %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("未内置 id 应读到空表：%v", entries)
	}
}

// ---------------------------------------------------------------------------
// DialogPort（对话框端口化：fake 注入，5 个选择器入口都走同一端口）
// ---------------------------------------------------------------------------

type fakeDialog struct {
	folderValue string
	folderOK    bool
	file        string
	fileOK      bool
	got         DialogOptions
}

func (d *fakeDialog) PickFolder(options DialogOptions) (string, bool) {
	d.got = options
	return d.folderValue, d.folderOK
}

func (d *fakeDialog) PickFile(options DialogOptions) (string, bool) {
	d.got = options
	return d.file, d.fileOK
}

func TestDialogPortPowersFolderPickers(t *testing.T) {
	dir := t.TempDir()
	dialog := &fakeDialog{folderValue: filepath.Join(dir, "picked"), folderOK: true}
	selection, err := SelectProjectPathInner(dialog, strPtr("~/sub"))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if selection == nil || selection.WorkspaceDir != filepath.Join(dir, "picked") {
		t.Fatalf("selection：%+v", selection)
	}
	// ~/ 前缀按 HOME 展开成初始目录。
	home, _ := support.UserHomeDir()
	if dialog.got.Directory != joinPath(home, "sub") {
		t.Fatalf("初始目录应展开 ~：%+v", dialog.got)
	}

	// 用户取消 → nil, nil（端口契约：ok=false）。
	cancelled := &fakeDialog{}
	selection, err = SelectProjectPathInner(cancelled, nil)
	if err != nil || selection != nil {
		t.Fatalf("取消应返回 nil, nil：%+v, %v", selection, err)
	}
}

func TestDialogPortPowersBatchImportFilePicker(t *testing.T) {
	dir := t.TempDir()
	importFile := filepath.Join(dir, "sources.json")
	writeFileT(t, importFile, `[{"repo":"https://example.com/r.git","ref":"main"}]`)

	dialog := &fakeDialog{file: importFile, fileOK: true}
	result, err := SelectSkillBatchImportFileInner(dialog)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if result == nil || result.Path != importFile || !strings.Contains(result.Content, "repo") {
		t.Fatalf("result：%+v", result)
	}
	if len(dialog.got.Filters) != 1 || dialog.got.Filters[0].Extensions[0] != "json" {
		t.Fatalf("应有 JSON 过滤器：%+v", dialog.got.Filters)
	}

	cancelled := &fakeDialog{}
	result, err = SelectSkillBatchImportFileInner(cancelled)
	if err != nil || result != nil {
		t.Fatalf("取消应返回 nil, nil：%+v, %v", result, err)
	}
}

func TestDialogPortPowersMCPConfigFilePicker(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "mcp.json")
	writeFileT(t, configFile, "{}")

	dialog := &fakeDialog{file: configFile, fileOK: true}
	selection, err := SelectTargetMCPConfigFileInner(dialog, strPtr(configFile))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if selection == nil || selection.WorkspaceDir != configFile {
		t.Fatalf("selection：%+v", selection)
	}
	// 既有文件路径 → 初始目录 + 预填文件名。
	if dialog.got.Directory != dir || dialog.got.FileName != "mcp.json" {
		t.Fatalf("初始位置：%+v", dialog.got)
	}
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func mustSymlink(t *testing.T, source string, destination string) {
	t.Helper()
	if err := createDirectorySymlink(source, destination); err != nil {
		t.Fatalf("symlink %s -> %s: %v", destination, source, err)
	}
}

func mustGitCacheDir(t *testing.T, store *WorkspaceConfigStore, repo string) string {
	t.Helper()
	dir, err := store.GitCacheDirForRepo(repo)
	if err != nil {
		t.Fatalf("GitCacheDirForRepo: %v", err)
	}
	return dir
}

func parseJSONT(t *testing.T, content string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader([]byte(content)))
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	return value
}

func equalStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func anyToStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func ptrUint64(value uint64) *uint64 { return &value }

// testsupportTestDir：grokbuild/dsh 测试的 TestDir 别名（保持与 Rust 测试同
// 命名节奏，避免与既有局部变量 dir 混淆）。
func testsupportTestDir(t *testing.T, prefix string) string {
	return testsupport.TestDir(t, prefix)
}

// parseTOMLT：测试里解析 TOML 文本为 map（BurntSushi 同一枚举器，键为
// string、值为 int64/float64/bool/string/map/[]any）。
func parseTOMLT(t *testing.T, content string) map[string]any {
	t.Helper()
	var value map[string]any
	if _, err := toml.Decode(content, &value); err != nil {
		t.Fatalf("parse toml: %v", err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value
}

// tomlEqual：TOML map 的语义相等（BurntSushi 输出的类型在同一枚举器下稳定）。
func tomlEqual(left map[string]any, right map[string]any) bool {
	return fmt.Sprintf("%#v", left) == fmt.Sprintf("%#v", right)
}
