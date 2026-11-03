// git 来源拉取的协作式取消：kill 子进程、有缓存退缓存、无缓存清半成品。
// 远端用本地真实 git 仓库，让 clone/fetch 走真实子进程路径。

use super::*;
use crate::support::cancel::{CancelCheck, Cancelled};
use crate::test_support::TestDir;

// 恒真取消脚本：run_command 在第一次轮询就 kill 子进程，行为确定。
fn cancelled_now() -> CancelCheck {
    CancelCheck::scripted(|| true)
}

fn git_in(dir: &Path, args: &[&str]) -> Result<()> {
    let output = Command::new("git").current_dir(dir).args(args).output()?;
    anyhow::ensure!(
        output.status.success(),
        "git {args:?} 失败：{}",
        String::from_utf8_lossy(&output.stderr)
    );
    Ok(())
}

// 本地 git 远端：owner/repo 形态的路径保证缓存目录解析稳定且各测试互不冲突。
fn local_git_remote(root: &Path) -> Result<String> {
    let remote = root.join("acme").join("skills-repo");
    fs::create_dir_all(remote.join("alpha"))?;
    fs::write(
        remote.join("alpha").join("SKILL.md"),
        "---\nname: alpha\ndescription: 本地远端\n---\n",
    )?;
    git_in(&remote, &["init", "-q"])?;
    git_in(&remote, &["config", "user.email", "reins@test.local"])?;
    git_in(&remote, &["config", "user.name", "reins test"])?;
    git_in(&remote, &["add", "."])?;
    git_in(&remote, &["commit", "-qm", "seed skill"])?;
    Ok(remote.display().to_string())
}

fn git_source_store(root: &TestDir, repo: &str) -> Result<WorkspaceConfigStore> {
    let store = WorkspaceConfigStore::at(root.path()).with_env(Environment::from_env());
    let config = serde_json::json!({
        "skill_sources": [{"id": "git-src", "type": "git", "repo": repo}],
    });
    fs::write(store.config_path(), serde_yaml::to_string(&config)?)?;
    Ok(store)
}

fn refresh_marker_path(store: &WorkspaceConfigStore, repo: &str) -> Result<PathBuf> {
    Ok(store
        .git_cache_dir_for_repo(repo)?
        .join(".git")
        .join("reins-last-fetch"))
}

// 取消生效的直接证据：子进程被 kill，错误是 Cancelled 且没有等它自然退出。
#[cfg(unix)]
#[test]
fn run_command_kills_child_on_cancel() -> Result<()> {
    let started = std::time::Instant::now();
    let mut command = Command::new("sleep");
    command.arg("30");

    let error = run_command(&mut command, "测试命令", &cancelled_now()).expect_err("取消必须报错");

    assert!(error.is::<Cancelled>(), "错误应为取消：{error:#}");
    assert!(
        started.elapsed() < Duration::from_secs(10),
        "kill 必须立刻生效，实际等了 {:?}",
        started.elapsed()
    );
    Ok(())
}

// 已有缓存时取消：缓存原样可用，marker 不回写（下次仍会重试远端），
// 且取消如实上报，不会被当成成功。
#[test]
fn cancelled_refresh_keeps_stale_cache_and_marker() -> Result<()> {
    let root = TestDir::new("fetch-cancel-stale")?;
    let repo = local_git_remote(root.path())?;
    let store = git_source_store(&root, &repo)?;

    // 先正常拉取一次，建立缓存与 marker（同时守住成功路径不误报取消）。
    let refreshed = refresh_git_skill_source_inner(&store, "git-src", &CancelCheck::never())?;
    assert!(!refreshed.cancelled);

    let marker_path = refresh_marker_path(&store, &repo)?;
    assert!(marker_path.exists());
    // marker 写成不同的 ref：内容失配让下一次拉取判定为需要刷新。
    fs::write(&marker_path, "old-ref")?;

    let refreshed = refresh_git_skill_source_inner(&store, "git-src", &cancelled_now())?;
    assert!(refreshed.cancelled, "取消必须如实上报");

    assert_eq!(
        fs::read_to_string(&marker_path)?,
        "old-ref",
        "取消后 marker 不能回写，否则下次打开不会重试远端"
    );
    let skills = discover_skills_in_directory(&store.git_cache_dir_for_repo(&repo)?)?;
    assert!(
        skills.iter().any(|skill| skill.name == "alpha"),
        "缓存内容必须原样保留"
    );

    Ok(())
}

// 没有本地缓存（首次 clone）时取消：报取消错误，半成品目录必须清理，
// 否则残留的 .git 会让下一次拉取把半成品当成已有仓库去 fetch。
#[test]
fn cancelled_fresh_clone_reports_cancelled_and_cleans_partial() -> Result<()> {
    let root = TestDir::new("fetch-cancel-fresh-clone")?;
    let repo = local_git_remote(root.path())?;
    let store = git_source_store(&root, &repo)?;

    // 预置无 .git 的垃圾目录：走「清空 + 全新 clone」路径，clone 立刻被取消。
    let cache_dir = store.git_cache_dir_for_repo(&repo)?;
    fs::create_dir_all(&cache_dir)?;
    fs::write(cache_dir.join("junk.txt"), "not a git repo")?;

    let error = build_sync_skill_options(&store, "git-src", &cancelled_now())
        .expect_err("没有本地缓存时取消必须报错");
    assert!(error.is::<Cancelled>(), "错误应为取消：{error:#}");
    assert!(
        !cache_dir.exists(),
        "半成品缓存必须清理，不能留给下次当成已有仓库"
    );

    Ok(())
}
