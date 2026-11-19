// workspace_fetch_cancel_test.go：git 来源拉取的协作式取消（对齐
// tests/workspace_fetch_cancel.rs）：kill 子进程、有缓存退缓存、无缓存清
// 半成品。远端用本地真实 git 仓库，让 clone/fetch 走真实子进程路径。
package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reins/internal/agents"
	"reins/internal/support"
)

// cancelledContext：恒取消的 context——runCommand 在第一个取消检查点就杀掉
// 子进程，行为确定。
func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 失败：%v：%s", args, err, output)
	}
}

// localGitRemote：本地 git 远端：owner/repo 形态的路径保证缓存目录解析稳定
// 且各测试互不冲突。
func localGitRemote(t *testing.T, root string) string {
	t.Helper()
	remote := filepath.Join(root, "acme", "skills-repo")
	writeTestSkill(t, remote, "alpha", "---\nname: alpha\ndescription: 本地远端\n---\n")
	gitIn(t, remote, "init", "-q")
	gitIn(t, remote, "config", "user.email", "reins@test.local")
	gitIn(t, remote, "config", "user.name", "reins test")
	gitIn(t, remote, "add", ".")
	gitIn(t, remote, "commit", "-qm", "seed skill")
	return remote
}

func gitSourceStore(t *testing.T, root string, repo string) *WorkspaceConfigStore {
	t.Helper()
	store := NewWorkspaceConfigStoreAt(root)
	store.inner.env = supportFromEnv(t)
	writeFileT(t, store.ConfigPath(), "skill_sources:\n  - id: git-src\n    type: git\n    repo: "+quoteYAML(repo)+"\n")
	return store
}

func quoteYAML(value string) string {
	// repo 路径可能含反斜杠（Windows）；YAML 双引号串最小转义够用。
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	return `"` + escaped + `"`
}

func refreshMarkerPath(t *testing.T, store *WorkspaceConfigStore, repo string) string {
	t.Helper()
	dir, err := store.GitCacheDirForRepo(repo)
	if err != nil {
		t.Fatalf("GitCacheDirForRepo: %v", err)
	}
	return filepath.Join(dir, ".git", "reins-last-fetch")
}

// 取消生效的直接证据：子进程被 kill，错误是 context.Canceled 且没有等它
// 自然退出。
func TestRunCommandKillsChildOnCancel(t *testing.T) {
	if isWindowsGOOS() {
		t.Skip("sleep 命令按 POSIX 语义验证；Windows 分支的取消语义同一执行器")
	}
	started := time.Now()
	command := exec.Command("sleep", "30")

	err := runCommand(cancelledContext(), command, "测试命令")
	if err == nil {
		t.Fatal("取消必须报错")
	}
	if !cancelled(err) {
		t.Fatalf("错误应为取消：%v", err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("kill 必须立刻生效，实际等了 %v", elapsed)
	}
}

// 已有缓存时取消：缓存原样可用，marker 不回写（下次仍会重试远端），
// 且取消如实上报，不会被当成成功。
func TestCancelledRefreshKeepsStaleCacheAndMarker(t *testing.T) {
	_, root := newTestStore(t, "fetch-cancel-stale")
	repo := localGitRemote(t, root)
	store := gitSourceStore(t, root, repo)

	// 先正常拉取一次，建立缓存与 marker（同时守住成功路径不误报取消）。
	refreshed, err := RefreshGitSkillSourceInner(context.Background(), store, "git-src")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.Cancelled {
		t.Fatal("成功路径不应报取消")
	}

	markerPath := refreshMarkerPath(t, store, repo)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("marker 应存在：%v", err)
	}
	// marker 写成不同的 ref：内容失配让下一次拉取判定为需要刷新。
	writeFileT(t, markerPath, "old-ref")

	refreshed, err = RefreshGitSkillSourceInner(cancelledContext(), store, "git-src")
	if err != nil {
		t.Fatalf("cancelled refresh: %v", err)
	}
	if !refreshed.Cancelled {
		t.Fatal("取消必须如实上报")
	}

	if got := readFileT(t, markerPath); got != "old-ref" {
		t.Fatalf("取消后 marker 不能回写，否则下次打开不会重试远端：%q", got)
	}
	skills, err := DiscoverSkillsInDirectory(mustGitCacheDir(t, store, repo))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	hasAlpha := false
	for _, skill := range skills {
		if skill.Name == "alpha" {
			hasAlpha = true
		}
	}
	if !hasAlpha {
		t.Fatalf("缓存内容必须原样保留：%+v", skills)
	}
}

// 没有本地缓存（首次 clone）时取消：报取消错误，半成品目录必须清理，
// 否则残留的 .git 会让下一次拉取把半成品当成已有仓库去 fetch。
func TestCancelledFreshCloneReportsCancelledAndCleansPartial(t *testing.T) {
	_, root := newTestStore(t, "fetch-cancel-fresh-clone")
	repo := localGitRemote(t, root)
	store := gitSourceStore(t, root, repo)

	// 预置无 .git 的垃圾目录：走「清空 + 全新 clone」路径，clone 立刻被取消。
	cacheDir := mustGitCacheDir(t, store, repo)
	if err := os.MkdirAll(cacheDir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFileT(t, filepath.Join(cacheDir, "junk.txt"), "not a git repo")

	_, err := BuildSyncSkillOptions(cancelledContext(), store, "git-src")
	if err == nil {
		t.Fatal("没有本地缓存时取消必须报错")
	}
	if !cancelled(err) {
		t.Fatalf("错误应为取消：%v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("半成品缓存必须清理，不能留给下次当成已有仓库：%v", err)
	}
}

// 单槽取消状态机：Begin 换新槽，Cancel 只作用于当前槽。
func TestSkillSourceFetchCancelStateCancelsCurrentSlotOnly(t *testing.T) {
	state := &SkillSourceFetchCancelState{}

	// 没有在途拉取时取消是空操作。
	state.Cancel()

	first := state.Begin()
	second := state.Begin()
	// Begin 换槽不取消旧的（Rust 语义：被换下的旧拉取自己跑完）。
	select {
	case <-first.Done():
		t.Fatal("换槽不应取消旧拉取")
	default:
	}

	state.Cancel()
	select {
	case <-second.Done():
	default:
		t.Fatal("Cancel 应取消当前槽")
	}
	// 再 Begin 又是新槽，不受上一次 Cancel 影响。
	third := state.Begin()
	select {
	case <-third.Done():
		t.Fatal("新 Begin 应拿到未取消的 context")
	default:
	}
}

func isWindowsGOOS() bool {
	return runtime.GOOS == "windows"
}

// supportFromEnv：测试环境视图（同 newTestStore 口径）。
func supportFromEnv(t *testing.T) *support.Environment {
	t.Helper()
	return support.FromEnv(agents.Redirects())
}
