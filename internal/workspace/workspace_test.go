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

// mustParseT：测试里取已解析配置的统一入口（矩阵同步的格子操作以解析形态
// 为入参，不再各自 store.Parse）。
func mustParseT(t *testing.T, store *WorkspaceConfigStore) *ResolvedManagerConfig {
	t.Helper()
	config, err := store.Parse()
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return config
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

func resolvedSkillPathFor(name string, dir string) *ResolvedSkillPath {
	return testSkillPath(name, dir)
}

func resolvedConfigOf(paths ...*ResolvedSkillPath) *ResolvedManagerConfig {
	return testConfigWith(paths...)
}

// hermesHomeDefaultDir：与 internal/agents 的 hermesDefaultDir 同口径的
// 期望值表达（Windows 默认目录随 GOOS），测试断言独立书写。
func hermesHomeDefaultDir() string {
	if runtime.GOOS == "windows" {
		return "AppData\\Local\\hermes"
	}
	return ".hermes"
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
// 同步弹窗的路径候选与链接归类
// ---------------------------------------------------------------------------

func TestSyncPathOptionsReportAllLinkStates(t *testing.T) {
	root := testsupport.TestDir(t, "sync-path-link-states")
	sourceRoot := filepath.Join(root, "source")
	pathSkillDir := filepath.Join(root, "target", "skills")
	unmanagedRoot := filepath.Join(root, "unmanaged")

	writeTestSkill(t, sourceRoot, "linked")
	writeTestSkill(t, sourceRoot, "excluded")
	writeTestSkill(t, sourceRoot, "removed")
	writeTestSkill(t, unmanagedRoot, "external")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	mustSymlink(t, filepath.Join(sourceRoot, "linked"), filepath.Join(pathSkillDir, "linked"))
	mustSymlink(t, filepath.Join(sourceRoot, "excluded"), filepath.Join(pathSkillDir, "excluded"))
	mustSymlink(t, filepath.Join(sourceRoot, "removed"), filepath.Join(pathSkillDir, "removed"))
	mustSymlink(t, filepath.Join(unmanagedRoot, "external"), filepath.Join(pathSkillDir, "external"))
	if err := os.RemoveAll(filepath.Join(sourceRoot, "removed")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	config := resolvedConfigOf(resolvedSkillPathFor("codex", pathSkillDir))
	sources := []skillLinkSource{linkTestSource("local", sourceRoot, "linked")}
	options, err := buildSyncPathOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncPathOptionsWithSources: %v", err)
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
func TestSyncPathOptionsKeepMissingSkillWhenSourceRootIsLinked(t *testing.T) {
	root := testsupport.TestDir(t, "missing-skill-through-linked-source-root")
	sourceRoot := filepath.Join(root, "source")
	linkedSourceRoot := filepath.Join(root, "linked-source")
	pathSkillDir := filepath.Join(root, "target", "skills")

	writeTestSkill(t, sourceRoot, "removed")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, sourceRoot, linkedSourceRoot)
	mustSymlink(t, filepath.Join(linkedSourceRoot, "removed"), filepath.Join(pathSkillDir, "removed"))
	if err := os.RemoveAll(filepath.Join(sourceRoot, "removed")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	config := resolvedConfigOf(resolvedSkillPathFor("codex", pathSkillDir))
	sources := []skillLinkSource{linkTestSource("local", linkedSourceRoot)}
	options, err := buildSyncPathOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncPathOptionsWithSources: %v", err)
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
func TestSyncPathOptionsKeepAllOverlappingSourceMatches(t *testing.T) {
	root := testsupport.TestDir(t, "sync-target-overlapping-sources")
	outerRoot := filepath.Join(root, "sources")
	innerRoot := filepath.Join(outerRoot, "nested")
	pathSkillDir := filepath.Join(root, "target", "skills")

	writeTestSkill(t, innerRoot, "alpha")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(innerRoot, "alpha"), filepath.Join(pathSkillDir, "alpha"))

	config := resolvedConfigOf(resolvedSkillPathFor("codex", pathSkillDir))
	sources := []skillLinkSource{
		linkTestSource("outer", outerRoot),
		linkTestSource("inner", innerRoot),
	}
	options, err := buildSyncPathOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncPathOptionsWithSources: %v", err)
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
func TestSyncPathOptionsDoNotDuplicateManagedDirectoryInheritance(t *testing.T) {
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
		resolvedSkillPathFor("owner", ownerSkillDir),
		resolvedSkillPathFor("replica", replicaSkillDir),
	)
	sources := []skillLinkSource{linkTestSource("local", sourceRoot)}
	options, err := buildSyncPathOptionsWithSources(config, sources)
	if err != nil {
		t.Fatalf("buildSyncPathOptionsWithSources: %v", err)
	}
	var owner, replica *SyncPathOption
	for i := range options {
		switch options[i].Name {
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
	if replica.LinkedPathID == nil || *replica.LinkedPathID != "owner" {
		t.Fatalf("replica 的命名归属应为 owner：%+v", replica.LinkedPathID)
	}
	if replica.DirLinkTarget == nil || *replica.DirLinkTarget != expectedLinkTarget {
		t.Fatalf("replica 的链接目标应为 canonical 形态：%+v", replica.DirLinkTarget)
	}
	if len(replica.Links) != 0 {
		t.Fatalf("别名路径不应重复列出关联：%+v", replica.Links)
	}
}

// 链接另一端不是受管路径（外部目录或断链）时，命名归属为空，但「落点是别名」
// 这件事必须带给界面——否则前端会保持可勾，勾了之后后端静默跳过（用户可见的
// 静默失效）。这里锁住归属字段仍然有值。
func TestSyncPathOptionsReportAliasSkillDirWithoutNamedOwner(t *testing.T) {
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
		resolvedSkillPathFor("plain", plainSkillDir),
		resolvedSkillPathFor("alias", aliasSkillDir),
		resolvedSkillPathFor("broken", brokenSkillDir),
	)
	options, err := buildSyncPathOptionsWithSources(config, []skillLinkSource{linkTestSource("local", filepath.Join(root, "source"))})
	if err != nil {
		t.Fatalf("buildSyncPathOptionsWithSources: %v", err)
	}
	byID := func(id string) *SyncPathOption {
		for i := range options {
			if options[i].Name == id {
				return &options[i]
			}
		}
		t.Fatalf("路径应在列表里：%s", id)
		return nil
	}

	// 普通落点：没有归属，也就没有被跳过的理由。
	if byID("plain").DirLinkTarget != nil || byID("plain").LinkedPathID != nil {
		t.Fatalf("普通落点不应有归属字段：%+v", byID("plain"))
	}

	// 外部目录：叫不出名字，但归属字段有值（界面据此禁用并说明内容在哪）。
	alias := byID("alias")
	if alias.LinkedPathID != nil {
		t.Fatalf("外部目录不应有命名归属：%+v", alias.LinkedPathID)
	}
	if alias.DirLinkTarget == nil || *alias.DirLinkTarget != support.DisplayPath(linkTarget(t, externalSkillDir)) {
		t.Fatalf("外部目录的链接目标应有值：%+v", alias.DirLinkTarget)
	}

	// 断链：canonicalize 失败，回落到解析出的原路径，同样有值。
	broken := byID("broken")
	if broken.DirLinkTarget == nil || *broken.DirLinkTarget != filepath.Join(root, "gone") {
		t.Fatalf("断链的链接目标应回落原路径：%+v", broken.DirLinkTarget)
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
	pathSkillDir := filepath.Join(root, "target", "skills")
	destination := filepath.Join(pathSkillDir, "alpha")

	if err := os.MkdirAll(sourceSkill, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceSkill, "SKILL.md"), []byte("alpha"), 0o666); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := createDirectorySymlink(sourceSkill, destination); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	config := resolvedConfigOf(resolvedSkillPathFor("codex", pathSkillDir))
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

// 两条路径 + 一个 local 来源的最小 workspace 配置，供链接移除用例复用。
func writeLinkStore(t *testing.T, root string) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = support.FromEnv(agents.Redirects())
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`skill_paths:
  codex:
    dir: %s
  other:
    dir: %s
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
	pathSkillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	writeTestSkill(t, sourceRoot, "beta")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(pathSkillDir, "alpha"))
	mustSymlink(t, filepath.Join(sourceRoot, "beta"), filepath.Join(pathSkillDir, "beta"))

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
	assertLinkMissing(t, filepath.Join(pathSkillDir, "alpha"))
	assertLinkMissing(t, filepath.Join(pathSkillDir, "beta"))
	if _, err := os.Stat(filepath.Join(sourceRoot, "beta", "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}
}

// 整个来源目录被删后，来源根目录也 canonicalize 不了；两侧都必须退回同一套
// 规范化规则，否则删除同步会漏掉这些链接。
func TestRemoveSourceSyncClearsLinkAfterSourceRootIsDeleted(t *testing.T) {
	root := testsupport.TestDir(t, "remove-sync-deleted-root")
	sourceRoot := filepath.Join(root, "source")
	pathSkillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(pathSkillDir, "alpha"))

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
	assertLinkMissing(t, filepath.Join(pathSkillDir, "alpha"))
}

func TestRemovePathSkillLinkOnlyDeletesLinksInsidePathSkillDir(t *testing.T) {
	root := testsupport.TestDir(t, "remove-single-skill-link")
	sourceRoot := filepath.Join(root, "source")
	pathSkillDir := filepath.Join(root, "target", "skills")
	otherSkillDir := filepath.Join(root, "other", "skills")
	writeTestSkill(t, sourceRoot, "alpha")
	writeTestSkill(t, sourceRoot, "beta")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(otherSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(sourceRoot, "alpha"), filepath.Join(pathSkillDir, "alpha"))
	mustSymlink(t, filepath.Join(sourceRoot, "beta"), filepath.Join(otherSkillDir, "beta"))
	if err := os.MkdirAll(filepath.Join(pathSkillDir, "real-dir"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	store := writeLinkStore(t, root)

	removed, err := RemovePathSkillLinkInner(store, "codex", support.DisplayPath(filepath.Join(pathSkillDir, "alpha")))
	if err != nil {
		t.Fatalf("RemovePathSkillLinkInner: %v", err)
	}
	if removed.SkillName != "alpha" {
		t.Fatalf("removed：%+v", removed)
	}
	assertLinkMissing(t, filepath.Join(pathSkillDir, "alpha"))
	if _, err := os.Stat(filepath.Join(sourceRoot, "alpha", "SKILL.md")); err != nil {
		t.Fatalf("来源内容应完好：%v", err)
	}

	// 真实目录不是链接，拒绝且不删除内容。
	if _, err := RemovePathSkillLinkInner(store, "codex", support.DisplayPath(filepath.Join(pathSkillDir, "real-dir"))); err == nil {
		t.Fatal("真实目录必须被拒绝")
	}
	if info, err := os.Stat(filepath.Join(pathSkillDir, "real-dir")); err != nil || !info.IsDir() {
		t.Fatalf("真实目录应完好：%v", err)
	}

	// 别的路径目录下的链接不能借用 codex 的 id 删除。
	if _, err := RemovePathSkillLinkInner(store, "codex", support.DisplayPath(filepath.Join(otherSkillDir, "beta"))); err == nil {
		t.Fatal("别的路径目录下的链接必须被拒绝")
	}
	if _, err := os.Lstat(filepath.Join(otherSkillDir, "beta")); err != nil {
		t.Fatalf("别的链接应完好：%v", err)
	}

	if _, err := RemovePathSkillLinkInner(store, "missing", support.DisplayPath(filepath.Join(pathSkillDir, "alpha"))); err == nil {
		t.Fatal("不存在的路径必须报错")
	}
}

// 弹窗把扫描出来的 destination_path 原样回传给单条移除命令，所以展示用的
// 路径字符串必须能通过后端的目录归属校验。
func TestRemovePathSkillLinkAcceptsDestinationPathFromSyncOptions(t *testing.T) {
	root := testsupport.TestDir(t, "remove-link-round-trip")
	unmanagedRoot := filepath.Join(root, "external")
	pathSkillDir := filepath.Join(root, "target", "skills")
	writeTestSkill(t, unmanagedRoot, "external")
	if err := os.MkdirAll(pathSkillDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, filepath.Join(unmanagedRoot, "external"), filepath.Join(pathSkillDir, "external"))

	store := writeLinkStore(t, root)
	options, err := BuildSyncPathOptions(store)
	if err != nil {
		t.Fatalf("BuildSyncPathOptions: %v", err)
	}
	var option *SyncPathOption
	for i := range options {
		if options[i].Name == "codex" {
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
	removed, err := RemovePathSkillLinkInner(store, option.Name, link.DestinationPath)
	if err != nil {
		t.Fatalf("RemovePathSkillLinkInner: %v", err)
	}
	if removed.SkillName != "external" {
		t.Fatalf("removed：%+v", removed)
	}
	assertLinkMissing(t, filepath.Join(pathSkillDir, "external"))
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

func TestDefaultConfigTemplateIsEmptySkeleton(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "reins-default-template-test.yaml")
	env := envOf(t)
	config, err := parseManagerConfig(DefaultConfigTemplate(), configPath, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 新装空开始：模板不预置任何条目（决策记录第 4 条），预设是唯一的
	// 便捷入口。
	if len(config.SkillPaths) != 0 || len(config.McpPaths) != 0 {
		t.Fatalf("模板应零预置：%+v", config)
	}
	if len(config.SkillSources) != 0 || len(config.Mcps) != 0 {
		t.Fatalf("模板应零预置：%+v", config)
	}
}

// 空目录按名字回落内置 defaults：旧模板「留空跟随环境重定向」的用法
// （grokbuild）迁移后保持语义。
func TestEmptySkillDirFallsBackToBuiltinDefaultsByName(t *testing.T) {
	_, dir := newTestStore(t, "empty-dir-fallback")
	grokRoot := filepath.Join(dir, "grok-home")
	if err := os.MkdirAll(grokRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("GROK_HOME", grokRoot)

	configPath := filepath.Join(t.TempDir(), "reins-empty-dir.yaml")
	config, err := parseManagerConfig("skill_paths:\n  grokbuild:\n    dir: \"\"\n", configPath, envOf(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	grok := config.skillPathByName("grokbuild")
	if grok == nil || grok.Dir != joinPath(grokRoot, "skills") {
		t.Fatalf("grokbuild 空目录应回落 $GROK_HOME/skills：%+v", grok)
	}
	// 名字回落不出 defaults 的空目录在解析阶段就报错，不留到分发时。
	if _, err := parseManagerConfig("skill_paths:\n  ghost:\n    dir: \"\"\n", configPath, envOf(t)); err == nil {
		t.Fatal("非内置名字的空目录必须报错")
	}
}

// MCP 路径条目省略 config_path 时同样按名字回落内置 defaults（跟随环境
// 重定向）；回落不出且未写的在解析阶段报错。
func TestEmptyMcpPathConfigFallsBackToBuiltinDefaultsByName(t *testing.T) {
	_, dir := newTestStore(t, "empty-mcp-fallback")
	grokRoot := filepath.Join(dir, "grok-home")
	if err := os.MkdirAll(grokRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("GROK_HOME", grokRoot)

	configPath := filepath.Join(t.TempDir(), "reins-empty-mcp.yaml")
	config, err := parseManagerConfig("mcp_paths:\n  grokbuild: {}\n", configPath, envOf(t))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	path := config.mcpPathByName("grokbuild")
	if path == nil || path.ConfigPath != joinPath(grokRoot, "config.toml") {
		t.Fatalf("grokbuild 空 config_path 应回落 $GROK_HOME/config.toml：%+v", path)
	}
	if path.MCPConfigPrefix != "mcp_servers" || path.MCPConfigType != agents.McpConfigGrokBuild {
		t.Fatalf("prefix/type 应一并回落：%+v", path)
	}
	if _, err := parseManagerConfig("mcp_paths:\n  ghost: {}\n", configPath, envOf(t)); err == nil {
		t.Fatal("非内置名字的空 MCP 路径必须报错")
	}
}

func TestSkillPathPresetsCoverAllAgentsWithEnvResolvedPaths(t *testing.T) {
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

	presets := GetSkillPathPresetsInner(envOf(t))
	if len(presets) != 8 {
		t.Fatalf("八个工具各一条预设：%d", len(presets))
	}
	byName := func(name string) *SkillPathPreset {
		for i := range presets {
			if presets[i].Name == name {
				return &presets[i]
			}
		}
		t.Fatalf("缺少 %s 预设", name)
		return nil
	}

	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}
	codex := byName("Codex")
	if codex.Dir != filepath.Join(home, ".agents/skills") {
		t.Fatalf("codex preset：%+v", codex)
	}
	hermes := byName("HermesAgent")
	if want := joinPath(joinPath(home, hermesHomeDefaultDir()), "skills"); hermes.Dir != want {
		t.Fatalf("hermes preset：got %q want %q", hermes.Dir, want)
	}
	grokbuild := byName("Grok Build")
	if grokbuild.Dir != joinPath(grokRoot, "skills") {
		t.Fatalf("grokbuild preset：%+v", grokbuild)
	}
	pi := byName("Pi")
	if pi.Dir != joinPath(piRoot, "skills") {
		t.Fatalf("pi preset：%+v", pi)
	}
}

func TestMcpPathPresetsCarryFormatKnowledge(t *testing.T) {
	_, dir := newTestStore(t, "mcp-presets")
	piRoot := filepath.Join(dir, "pi-agent-dir")
	if err := os.MkdirAll(piRoot, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", piRoot)
	env := envOf(t)

	presets := GetMcpPathPresetsInner(env)
	if len(presets) != 8 {
		t.Fatalf("八个工具各一条 MCP 预设：%d", len(presets))
	}
	byName := func(name string) *McpPathPreset {
		for i := range presets {
			if presets[i].Name == name {
				return &presets[i]
			}
		}
		t.Fatalf("缺少 %s 预设", name)
		return nil
	}

	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}
	codex := byName("Codex")
	if codex.ConfigPath != filepath.Join(home, ".codex/config.toml") || codex.ConfigPrefix != "mcp_servers" || codex.ConfigType != "common" {
		t.Fatalf("codex preset：%+v", codex)
	}
	pi := byName("Pi")
	if pi.ConfigPath != joinPath(piRoot, "mcp.json") || pi.ConfigPrefix != "mcpServers" {
		t.Fatalf("pi preset：%+v", pi)
	}
	dsh := byName("DeepSeek Harness")
	if dsh.ConfigPath != filepath.Join(home, ".dsh/cordis.patch.yml") || dsh.ConfigPrefix != "" || dsh.ConfigType != "dsh" {
		t.Fatalf("dsh preset：%+v", dsh)
	}
	hermes := byName("HermesAgent")
	// hermes 的 MCP 条目方言已并入 common（YAML 载体由扩展名决定）。
	if want := joinPath(joinPath(home, hermesHomeDefaultDir()), "config.yaml"); hermes.ConfigPath != want || hermes.ConfigPrefix != "mcp_servers" || hermes.ConfigType != "common" {
		t.Fatalf("hermes preset：got %+v want %q", hermes, want)
	}
	if hermes.FormatDescription != mcpFormatWriterFor(agents.McpConfigCommon).description() {
		t.Fatalf("hermes 格式说明应来自 writer：%s", hermes.FormatDescription)
	}
}

// 编辑页展示的格式说明是面向用户的契约文案，逐字锁定，改动须有意为之。
func TestMcpPathViewCarriesMCPFormatDescriptionPerConfigType(t *testing.T) {
	path := &ResolvedMcpPath{Name: "probe", ConfigPath: t.TempDir(), MCPConfigPrefix: "mcpServers", MCPConfigType: agents.McpConfigCommon}
	if want := "通用 MCP 条目（标准 command/args/env），按配置文件扩展名写入 JSON、TOML 或 YAML 的 mcp 节点下；YAML 载体的条目无 type 判别字段、空集合省略。"; mcpPathToView(path).MCPFormatDescription != want {
		t.Fatalf("common：%s", mcpPathToView(path).MCPFormatDescription)
	}

	path.MCPConfigType = agents.McpConfigOpenCode
	if want := "OpenCode 专属格式（JSON，带 $schema 头，command 为数组、environment 键），写入 mcp.servers 根。"; mcpPathToView(path).MCPFormatDescription != want {
		t.Fatalf("opencode：%s", mcpPathToView(path).MCPFormatDescription)
	}

	path.MCPConfigType = agents.McpConfigGrokBuild
	if want := "Grok Build TOML 格式（远端类型用 headers 携带自定义头），写入 config.toml。"; mcpPathToView(path).MCPFormatDescription != want {
		t.Fatalf("grokbuild：%s", mcpPathToView(path).MCPFormatDescription)
	}

	path.MCPConfigType = agents.McpConfigDsh
	if want := "DeepSeek Harness 的 Cordis patch YAML（insert/remove 操作列表，按 serverName 定位，无 configPrefix）。"; mcpPathToView(path).MCPFormatDescription != want {
		t.Fatalf("dsh：%s", mcpPathToView(path).MCPFormatDescription)
	}

}

// 编辑页展示的格式示例与 desired 条目构造函数的产物形状逐字锁定，
// 键名或结构调整时必须同步改示例并有意为之。
func TestMcpPathViewCarriesMCPFormatExamplePerConfigType(t *testing.T) {
	// dsh 条目的 cwd 读 home，先装隔离环境让断言确定。
	newTestStore(t, "mcp-format-examples")
	home, ok := support.UserHomeDir()
	if !ok {
		t.Fatal("测试环境缺少 HOME")
	}

	labels := func(view *McpPathView) []string {
		result := make([]string, 0, len(view.MCPFormatExamples))
		for _, example := range view.MCPFormatExamples {
			result = append(result, example.Label)
		}
		return result
	}
	body := func(view *McpPathView, index int) string {
		return view.MCPFormatExamples[index].Body
	}

	// codex:common 形态，本地与远端各一段。
	path := &ResolvedMcpPath{Name: "codex", ConfigPath: t.TempDir(), MCPConfigPrefix: "mcpServers", MCPConfigType: agents.McpConfigCommon}
	view := mcpPathToView(path)
	if want := []string{"本地命令（stdio）", "远端地址（http / sse）"}; !equalStrings(labels(&view), want) {
		t.Fatalf("labels：%v", labels(&view))
	}
	wantStdio := "{\n  \"mcpServers\": {\n    \"my-server\": {\n      \"args\": [\n        \"server.js\"\n      ],\n      \"command\": \"node\",\n      \"env\": {\n        \"KEY\": \"value\"\n      },\n      \"type\": \"stdio\"\n    }\n  }\n}\n"
	if got := body(&view, 0); got != wantStdio {
		t.Fatalf("common stdio 示例漂移：\n%s", got)
	}
	wantRemote := "{\n  \"mcpServers\": {\n    \"my-server\": {\n      \"headers\": {\n        \"X-Key\": \"value\"\n      },\n      \"type\": \"http\",\n      \"url\": \"https://example.com/mcp\"\n    }\n  }\n}\n"
	if got := body(&view, 1); got != wantRemote {
		t.Fatalf("common remote 示例漂移：\n%s", got)
	}

	// zcode 的点分 prefix 结构化嵌套为 JSON 层级。
	path.MCPConfigPrefix = "mcp.servers"
	view = mcpPathToView(path)
	wantNested := "{\n  \"mcp\": {\n    \"servers\": {\n      \"my-server\": {\n        \"args\": [\n          \"server.js\"\n        ],\n        \"command\": \"node\",\n        \"env\": {\n          \"KEY\": \"value\"\n        },\n        \"type\": \"stdio\"\n      }\n    }\n  }\n}\n"
	if got := body(&view, 0); got != wantNested {
		t.Fatalf("zcode 嵌套示例漂移：\n%s", got)
	}

	// OpenCode:专属形态（local/remote，command 数组与 environment 键）。
	path.MCPConfigType = agents.McpConfigOpenCode
	view = mcpPathToView(path)
	if want := []string{"本地命令（stdio）", "远端地址（http / sse）"}; !equalStrings(labels(&view), want) {
		t.Fatalf("opencode labels：%v", labels(&view))
	}
	wantOpenCodeLocal := "{\n  \"mcp\": {\n    \"servers\": {\n      \"my-server\": {\n        \"command\": [\n          \"node\",\n          \"server.js\"\n        ],\n        \"environment\": {\n          \"KEY\": \"value\"\n        },\n        \"type\": \"local\"\n      }\n    }\n  }\n}\n"
	if got := body(&view, 0); got != wantOpenCodeLocal {
		t.Fatalf("opencode stdio 示例漂移：\n%s", got)
	}
	wantOpenCodeRemote := "{\n  \"mcp\": {\n    \"servers\": {\n      \"my-server\": {\n        \"headers\": {\n          \"X-Key\": \"value\"\n        },\n        \"type\": \"remote\",\n        \"url\": \"https://example.com/mcp\"\n      }\n    }\n  }\n}\n"
	if got := body(&view, 1); got != wantOpenCodeRemote {
		t.Fatalf("opencode remote 示例漂移：\n%s", got)
	}

	// GrokBuild:TOML 表头，本地与远端各一段（Go 侧 BurntSushi 的拼版与
	// Rust toml crate 不同：父表头会显式写出；这里钉 Go 的确定性输出）。
	path.MCPConfigType = agents.McpConfigGrokBuild
	path.MCPConfigPrefix = "mcp_servers"
	view = mcpPathToView(path)
	wantGrokStdio := "[mcp_servers]\n[mcp_servers.my-server]\nargs = [\"server.js\"]\ncommand = \"node\"\n[mcp_servers.my-server.env]\nKEY = \"value\"\n"
	if got := body(&view, 0); got != wantGrokStdio {
		t.Fatalf("grokbuild stdio 示例漂移：\n%s", got)
	}
	wantGrokRemote := "[mcp_servers]\n[mcp_servers.my-server]\nurl = \"https://example.com/mcp\"\n[mcp_servers.my-server.headers]\nX-Key = \"value\"\n"
	if got := body(&view, 1); got != wantGrokRemote {
		t.Fatalf("grokbuild remote 示例漂移：\n%s", got)
	}

	// DSH:patch YAML，无 prefix 概念，stdio 与 streamable-http 各一段。
	path.MCPConfigType = agents.McpConfigDsh
	view = mcpPathToView(path)
	if want := []string{"本地命令（stdio）", "远端地址（http / sse）"}; !equalStrings(labels(&view), want) {
		t.Fatalf("dsh labels：%v", labels(&view))
	}
	wantDshRemote := "- insert:\n    - id: reins-mcp-my-server\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        transport: streamable-http\n        serverName: my-server\n        url: https://example.com/mcp\n        headers:\n          X-Key: value\n"
	if got := body(&view, 1); got != wantDshRemote {
		t.Fatalf("dsh remote 示例漂移：\n%s", got)
	}
	wantDshStdio := fmt.Sprintf("- insert:\n    - id: reins-mcp-my-server\n      name: '@deepseek-ai/dsh-mcp-client'\n      config:\n        serverName: my-server\n        transport: stdio\n        command: node\n        args:\n          - server.js\n        env:\n          KEY: value\n        cwd: %s\n        toolCallTimeoutMs: 60000\n        failOnStartupError: false\n", home)
	if got := body(&view, 0); got != wantDshStdio {
		t.Fatalf("dsh stdio 示例漂移：\n%s", got)
	}

}

func TestPiMcpPathPresetFollowsPiCodingAgentDir(t *testing.T) {
	_, dir := newTestStore(t, "pi-redirect")
	redirected := filepath.Join(dir, "pi-agent-dir-redirect")
	if err := os.MkdirAll(redirected, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", redirected)

	presets := GetMcpPathPresetsInner(envOf(t))
	var preset *McpPathPreset
	for i := range presets {
		if presets[i].Name == "Pi" {
			preset = &presets[i]
		}
	}
	if preset == nil {
		t.Fatal("缺少 pi 预设")
	}
	if preset.ConfigPath != joinPath(redirected, "mcp.json") {
		t.Fatalf("preset config_path：%+v", preset.ConfigPath)
	}
	if preset.ConfigPrefix != "mcpServers" {
		t.Fatalf("prefix：%s", preset.ConfigPrefix)
	}
}

// SSE 防呆按配置文件判定：条目指向 pi 的 mcp.json 就下发禁格标记，与条目
// 名无关（决策记录 2026-11-28-mcp-matrix-sync.md 第 3 条）；Windows 下
// 手输路径的大小写差异也要命中。
func TestMcpPathViewFlagsSSEUnsupportedByConfigFile(t *testing.T) {
	store, root := newTestStore(t, "mcp-path-sse-flag")
	piConfigPath := ""
	for i := range agents.AgentSpecs {
		if agents.AgentSpecs[i].Key != "pi" {
			continue
		}
		resolved, ok := agents.AgentSpecs[i].Global.Root.ResolvePath(store.Env(), agents.AgentSpecs[i].Global.McpConfigPath)
		if !ok {
			t.Fatal("pi 的 mcp.json 应可解析")
		}
		piConfigPath = resolved
	}
	if piConfigPath == "" {
		t.Fatal("清单里没有 pi 条目")
	}
	caseVariant := piConfigPath
	if runtime.GOOS == "windows" && len(caseVariant) > 1 && caseVariant[1] == ':' {
		caseVariant = "c" + caseVariant[1:]
	}
	otherConfig := filepath.Join(root, "other.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  pi-named:
    config_path: %s
    config_prefix: mcpServers
  pi-case:
    config_path: %s
    config_prefix: mcpServers
  unrelated:
    config_path: %s
    config_prefix: mcpServers
`, piConfigPath, caseVariant, otherConfig))

	document, err := store.LoadDocument()
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	byName := map[string]McpPathView{}
	for _, path := range document.Config.McpPaths {
		byName[path.Name] = path
	}
	if !byName["pi-named"].SSEUnsupported {
		t.Fatal("指向 pi mcp.json 的条目应标记 SSE 不兼容")
	}
	if !byName["pi-case"].SSEUnsupported {
		t.Fatal("大小写不同的同一配置文件也应命中")
	}
	if byName["unrelated"].SSEUnsupported {
		t.Fatal("其他配置文件不应被标记")
	}
}

// ---------------------------------------------------------------------------
// 遗留键、project id 与 inspection
// ---------------------------------------------------------------------------

// target / MCP / 项目 agent 的启停开关已废弃：旧配置里的 enabled 键被忽略、
// 不报错，也不再把目标当作「停用」。这条同时守住反序列化兼容与语义变化。
// 旧 schema（targets:）读时迁移成技能路径 + MCP 路径视图；enabled 键与
// projects 段被自然忽略，语义上不存在「停用」——MCP 照常应用。
func TestLegacyTargetsMigrateToPathsAndEnabledIsIgnored(t *testing.T) {
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

	// 读时迁移：旧 target 拆成同名技能路径 + MCP 路径；projects 丢弃。
	state, err := WorkspaceStateInner(store)
	if err != nil {
		t.Fatalf("WorkspaceStateInner: %v", err)
	}
	if state.Document == nil || state.Document.Config == nil {
		t.Fatal("config view")
	}
	view := state.Document.Config
	if len(view.SkillPaths) != 1 || view.SkillPaths[0].Name != "codex" {
		t.Fatalf("迁移视图 skill_paths：%+v", view.SkillPaths)
	}
	if len(view.McpPaths) != 1 || view.McpPaths[0].Name != "codex" {
		t.Fatalf("迁移视图 mcp_paths：%+v", view.McpPaths)
	}
	if view.SkillPaths[0].Dir != skillDir {
		t.Fatalf("迁移保真 dir：%s", view.SkillPaths[0].Dir)
	}

	// 不再有「停用目标」这一说：MCP 照常应用（此前会报「目标已禁用」）。
	applied, err := applyMCPCell(mustParseT(t, store), "fs-server", "codex")
	if err != nil {
		t.Fatalf("applyMCPCell: %v", err)
	}
	if applied.Action != "apply" {
		t.Fatalf("action：%s", applied.Action)
	}
	if _, err := os.Stat(filepath.Join(root, "codex.toml")); err != nil {
		t.Fatalf("MCP 配置应已写入：%v", err)
	}

	// 迁移发生在读路径：下一次真实写盘才把文件收敛成新 schema。
	if strings.Contains(readFileT(t, store.ConfigPath()), "skill_paths:") {
		t.Fatal("读路径不得改写文件（收敛留给写盘）")
	}

	// 写一盘（无关改动）后旧键消失。
	if _, err := CreateSkillPathInner(store, RawSkillPathInput{Name: "extra", Dir: filepath.Join(root, "extra-skills")}); err != nil {
		t.Fatalf("create: %v", err)
	}
	content := readFileT(t, store.ConfigPath())
	if strings.Contains(content, "targets:") || strings.Contains(content, "projects:") {
		t.Fatalf("写盘后 legacy 键不得残留：%s", content)
	}
}

// 旧 schema 里只有 skill_dir 的 target 迁移后不产生 MCP 路径（原本就没配
// MCP）；config_type 坏值按旧解析口径报错。
func TestLegacyMigrationBoundaries(t *testing.T) {
	env := envOf(t)
	path := filepath.Join(t.TempDir(), "reins-legacy-boundaries.yaml")

	config, err := parseManagerConfig("targets:\n  codex:\n    skill_dir: codex/skills\n", path, env)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := config.SkillPaths["codex"]; !ok {
		t.Fatal("codex 技能路径应迁移")
	}
	if len(config.McpPaths) != 0 {
		t.Fatalf("无 mcp 段的 target 不应迁移出 MCP 路径：%+v", config.McpPaths)
	}

	if _, err := parseManagerConfig("targets:\n  bad:\n    skill_dir: x\n    mcp:\n      config_path: x.json\n      config_type: nope\n", path, env); err == nil {
		t.Fatal("坏 config_type 必须报错")
	}

	// 新键与旧键同名时新键胜出，不被旧值覆盖。
	config, err = parseManagerConfig("targets:\n  codex:\n    skill_dir: old/skills\nskill_paths:\n  codex:\n    dir: new/skills\n", path, env)
	if err != nil {
		t.Fatalf("parse coexist: %v", err)
	}
	if got := config.SkillPaths["codex"]; got.Dir != joinPath(filepath.Dir(path), "new/skills") {
		t.Fatalf("新键应胜出：%+v", got)
	}
}

// inspection 也要带上落点归属：技能路径面板的列表用它标注「这个目录不归它」，
// 与同步弹窗同一份数据，不再让两个界面各自判断。
func TestInspectionReportsSkillDirLinkAttribution(t *testing.T) {
	store, root := newTestStore(t, "inspect-skill-dir-link")

	ownerDir := filepath.Join(root, "owner", "skills")
	aliasDir := filepath.Join(root, "alias", "skills")
	brokenDir := filepath.Join(root, "broken", "skills")
	if err := os.MkdirAll(ownerDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(aliasDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(brokenDir), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustSymlink(t, ownerDir, aliasDir)
	mustSymlink(t, filepath.Join(root, "gone"), brokenDir)

	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`skill_paths:
  owner:
    dir: %s
  alias:
    dir: %s
  broken:
    dir: %s
`, ownerDir, aliasDir, brokenDir))

	state, err := WorkspaceStateInner(store)
	if err != nil {
		t.Fatalf("WorkspaceStateInner: %v", err)
	}
	if state.Inspection == nil {
		t.Fatal("inspection")
	}
	inspection := state.Inspection
	byName := func(name string) *SkillPathInspection {
		for i := range inspection.SkillPaths {
			if inspection.SkillPaths[i].Name == name {
				return &inspection.SkillPaths[i]
			}
		}
		t.Fatalf("路径应在 inspection 里：%s", name)
		return nil
	}

	// 普通落点不标注。
	if byName("owner").DirLink != nil {
		t.Fatalf("普通落点不应标注：%+v", byName("owner").DirLink)
	}

	// 指向受管路径：给出命名归属与真实目录。
	link := byName("alias").DirLink
	if link == nil {
		t.Fatal("alias 应标注")
	}
	if link.LinkedPathID == nil || *link.LinkedPathID != "owner" {
		t.Fatalf("命名归属应为 owner：%+v", link.LinkedPathID)
	}
	if link.LinkTarget != support.DisplayPath(linkTarget(t, ownerDir)) {
		t.Fatalf("链接目标应为 canonical 形态：%s", link.LinkTarget)
	}

	// 断链：叫不出名字，但仍要标注（内容在链接另一端）。
	broken := byName("broken").DirLink
	if broken == nil {
		t.Fatal("断链也要标注")
	}
	if broken.LinkedPathID != nil {
		t.Fatalf("断链叫不出名字：%+v", broken.LinkedPathID)
	}
	if broken.LinkTarget != filepath.Join(root, "gone") {
		t.Fatalf("断链目标应回落原路径：%s", broken.LinkTarget)
	}

	// 两个别名各一条提示，普通路径不提示（面板已按行标注，这里让其它消费方
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
		t.Fatalf("warnings 应说明是哪个路径：%+v", inspection.Warnings)
	}
}

// ---------------------------------------------------------------------------
// MCP apply / preview / remove 主链路
// ---------------------------------------------------------------------------

func TestPiMcpApplyReadRemoveRoundtrip(t *testing.T) {
	store, root := newTestStore(t, "pi-mcp-roundtrip")
	piMcp := filepath.Join(root, "mcp.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`skill_paths:
  pi:
    dir: %s
mcp_paths:
  pi:
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

	preview, err := previewMCPCell(mustParseT(t, store), "fs-server", "pi")
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

	if _, err := applyMCPCell(mustParseT(t, store), "fs-server", "pi"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := applyMCPCell(mustParseT(t, store), "fs-server", "pi"); err != nil {
		t.Fatalf("apply twice: %v", err)
	}
	written := parseJSONT(t, readFileT(t, piMcp))
	if written["mcpServers"].(map[string]any)["fs-server"].(map[string]any)["command"] != "npx" {
		t.Fatalf("written：%+v", written)
	}

	if _, err := removeMCPCell(mustParseT(t, store), "fs-server", "pi"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if strings.Contains(readFileT(t, piMcp), "fs-server") {
		t.Fatal("条目应已删除")
	}
	mutation, err := removeMCPCell(mustParseT(t, store), "fs-server", "pi")
	if err != nil {
		t.Fatalf("remove again: %v", err)
	}
	if mutation.Action != "noop" {
		t.Fatalf("二次删除应为 noop：%+v", mutation)
	}

	// 不存在的路径报稳定错误。
	if _, err := applyMCPCell(mustParseT(t, store), "fs-server", "ghost"); err == nil ||
		!strings.Contains(err.Error(), "MCP 路径不存在：ghost") {
		t.Fatalf("error：%v", err)
	}
}

// CODEX_HOME 是 codex 官方支持的目录重定位：workspace 的 MCP 落点必须与
// providers 认的配置文件是同一个。省略 config_path 的条目按名字回落内置
// defaults（$CODEX_HOME/config.toml）。
func TestCodexMcpApplyFollowsCodexHomeRedirect(t *testing.T) {
	store, home := newTestStore(t, "codex-home-redirect")
	codexHome := filepath.Join(home, "codex-home")
	if err := os.MkdirAll(codexHome, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	store.inner.env = support.FromEnv(agents.Redirects())

	writeFileT(t, store.ConfigPath(), "mcp_paths:\n  codex: {}\nmcps:\n- name: fs-server\n  transport: stdio\n  command: npx\n")

	// 省略 config_path -> 按名字回落内置默认：$CODEX_HOME/config.toml。
	preview, err := previewMCPCell(mustParseT(t, store), "fs-server", "codex")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Format != "toml" {
		t.Fatalf("format：%s", preview.Format)
	}
	if preview.ConfigPath != filepath.Join(codexHome, "config.toml") {
		t.Fatalf("config_path：%s", preview.ConfigPath)
	}

	applied, err := applyMCPCell(mustParseT(t, store), "fs-server", "codex")
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

func TestDeleteWorkspaceMCPRemovesFromAllPaths(t *testing.T) {
	store, root := newTestStore(t, "mcp-delete-all-paths")
	claudeConfig := filepath.Join(root, "claude.json")
	zcodeConfig := filepath.Join(root, "zcode.json")
	writeFileT(t, store.ConfigPath(), fmt.Sprintf(`mcp_paths:
  claude:
    config_path: %s
    config_prefix: mcpServers
    config_type: common
  zcode:
    config_path: %s
    config_prefix: mcp.servers
    config_type: common
mcps:
- name: test-server
  transport: stdio
  command: node
`, claudeConfig, zcodeConfig))
	writeFileT(t, claudeConfig, `{"mcpServers": {"test-server": {"type": "stdio", "command": "node"}}}`)
	writeFileT(t, zcodeConfig, `{"mcp": {"servers": {"test-server": {"type": "stdio", "command": "node"}}}}`)

	result, err := DeleteWorkspaceMCPInner(store, "test-server")
	if err != nil {
		t.Fatalf("DeleteWorkspaceMCPInner: %v", err)
	}
	if result.ServerName != "test-server" {
		t.Fatalf("server_name：%s", result.ServerName)
	}
	if strings.Contains(readFileT(t, claudeConfig), "test-server") {
		t.Fatalf("claude 条目应已删除：%s", readFileT(t, claudeConfig))
	}
	if strings.Contains(readFileT(t, zcodeConfig), "test-server") {
		t.Fatalf("zcode 条目应已删除：%s", readFileT(t, zcodeConfig))
	}
}

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

// ---------------------------------------------------------------------------
// DialogPort（对话框端口化：fake 注入，4 个选择器入口都走同一端口）
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
	selection, err := SelectSkillPathDirectoryInner(dialog, strPtr("~/sub"))
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
	selection, err = SelectSkillPathDirectoryInner(cancelled, nil)
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
	selection, err := SelectMcpPathConfigFileInner(dialog, strPtr(configFile))
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

// participatingPaths：skip 判定与三种呈现（同步记 warnings、预演/卸载
// 丢弃 notes）的归一点，直测处置文案与过滤行为。
func TestParticipatingPathsSkipNotesAndFilter(t *testing.T) {
	missing := SkipMissing
	linked := SkipLinkedDir
	resolved := []managedPath{
		{id: "codex"},
		{id: "gone", skip: &missing},
		{id: "project:pi", skip: &linked},
		{id: "claude"},
	}

	// 不过滤：全部判定的 skip 都有处置文案，顺序与输入一致。
	participating, notes := participatingPaths(resolved, nil)
	if len(participating) != 2 || participating[0].id != "codex" || participating[1].id != "claude" {
		t.Fatalf("participating = %+v", participating)
	}
	if len(notes) != 2 {
		t.Fatalf("notes = %+v", notes)
	}
	if !strings.Contains(notes[0], "gone") || !strings.Contains(notes[0], "已跳过") {
		t.Fatalf("missing 文案应点名路径并说明处置：%q", notes[0])
	}
	if !strings.Contains(notes[1], "project:pi") || !strings.Contains(notes[1], "软链接") {
		t.Fatalf("linked-dir 文案应说明链接语义：%q", notes[1])
	}

	// 按所选地址过滤：未选中的（含 skip）不出现在任何一侧。
	filter := map[string]bool{"claude": true, "gone": true}
	participating, notes = participatingPaths(resolved, &filter)
	if len(participating) != 1 || participating[0].id != "claude" {
		t.Fatalf("过滤后 participating = %+v", participating)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "gone") {
		t.Fatalf("过滤后 notes 只含所选 skip：%+v", notes)
	}
}
