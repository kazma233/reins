package main

// 版本号一致性守卫：版本号在仓库里硬编码多处，分别服务关于页展示、打包元数据
// 与前端构建产物。wails3 模板没有版本单一来源机制，各平台打包文件全靠手动
// 同步；任一处漏改都会造成版本口径分裂（.app / Windows 文件属性 / Linux 包
// 显示旧版本），这里全部对齐 appinfo.go 的 Version 断言。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 版本号只校验各文件里的固定字段，用逐行正则即可拿到确定值；为此把根包测试
// 挂上 YAML 解析依赖不值得（go.mod 不为测试新增依赖面），且解析失败时
// 正则报错信息能直接指向出错行。info.json 用 stdlib 的 encoding/json。
var (
	// 只匹配 info 块内两空格缩进的 version（顶层 `version: '3'` 是 wails 配置
	// 文件格式版本，不是应用版本；更深层缩进属其他块）。
	configYmlVersionRe   = regexp.MustCompile(`^  version: "([^"]+)"\s*$`)
	packageJsonVersionRe = regexp.MustCompile(`"version"\s*:\s*"([^"]+)"`)
	// plist 键值对是稳定排版：key 行后紧跟 string 行。
	plistVersionRes = map[string]*regexp.Regexp{
		"CFBundleShortVersionString": regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`),
		"CFBundleVersion":            regexp.MustCompile(`<key>CFBundleVersion</key>\s*<string>([^<]+)</string>`),
	}
	// wails.exe.manifest 的 assemblyIdentity 携带 exe 版本资源。
	manifestVersionRe = regexp.MustCompile(`assemblyIdentity[^>]*version="([^"]+)"`)
	// nfpm.yaml 顶层 version（包版本，两空格缩进的 overrides 里没有同键）。
	nfpmVersionRe = regexp.MustCompile(`(?m)^version: "([^"]+)"\s*$`)
)

type versionCheck struct {
	label string
	got   string
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", filepath.Join(parts...), err)
	}
	return string(raw)
}

// parseBuildConfigVersion 提取 build/config.yml 的 info.version：只认 info: 块
// 内、非注释行上的两空格缩进 version 键。
func parseBuildConfigVersion(t *testing.T, raw string) string {
	t.Helper()
	inInfo := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			// 顶层键：进入或退出 info 块。
			inInfo = trimmed == "info:"
			continue
		}
		if !inInfo {
			continue
		}
		if m := configYmlVersionRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	t.Fatalf("build/config.yml 的 info 块内未找到 version 字段")
	return ""
}

func parsePackageJsonVersion(t *testing.T, raw string) string {
	t.Helper()
	m := packageJsonVersionRe.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("frontend/package.json 未找到 version 字段")
	}
	return m[1]
}

// parsePlistVersion 提取指定 plist 键的版本值（Info.plist 与 Info.dev.plist
// 排版一致）。
func parsePlistVersion(t *testing.T, raw string, key string) string {
	t.Helper()
	m := plistVersionRes[key].FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("plist 未找到 %s 字段", key)
	}
	return m[1]
}

// parseWindowsInfoJSONVersions 提取 build/windows/info.json 的四处版本：
// fixed 块两个（exe 版本资源）+ info["0409"] 语言块两个（属性面板展示）。
func parseWindowsInfoJSONVersions(t *testing.T, raw string) []versionCheck {
	t.Helper()
	var doc struct {
		Fixed struct {
			FileVersion    string `json:"file_version"`
			ProductVersion string `json:"product_version"`
		} `json:"fixed"`
		Info map[string]struct {
			FileVersion    string `json:"FileVersion"`
			ProductVersion string `json:"ProductVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("build/windows/info.json 解析失败: %v", err)
	}
	entry, ok := doc.Info["0409"]
	if !ok {
		t.Fatalf(`build/windows/info.json 缺少 info["0409"] 语言块`)
	}
	return []versionCheck{
		{"build/windows/info.json fixed.file_version", doc.Fixed.FileVersion},
		{"build/windows/info.json fixed.product_version", doc.Fixed.ProductVersion},
		{`build/windows/info.json info["0409"].FileVersion`, entry.FileVersion},
		{`build/windows/info.json info["0409"].ProductVersion`, entry.ProductVersion},
	}
}

func TestVersionConsistency(t *testing.T) {
	appVersion := new(AppInfoService).Version()

	checks := []versionCheck{
		{"build/config.yml info.version", parseBuildConfigVersion(t, readRepoFile(t, "build", "config.yml"))},
		{"frontend/package.json version", parsePackageJsonVersion(t, readRepoFile(t, "frontend", "package.json"))},
		{"build/windows/wails.exe.manifest assemblyIdentity version",
			extractVersion(t, manifestVersionRe, readRepoFile(t, "build", "windows", "wails.exe.manifest"), "assemblyIdentity version")},
		{"build/linux/nfpm/nfpm.yaml version",
			extractVersion(t, nfpmVersionRe, readRepoFile(t, "build", "linux", "nfpm", "nfpm.yaml"), "nfpm version")},
	}
	for _, file := range []string{"Info.plist", "Info.dev.plist"} {
		raw := readRepoFile(t, "build", "darwin", file)
		for key := range plistVersionRes {
			checks = append(checks, versionCheck{"build/darwin/" + file + " " + key, parsePlistVersion(t, raw, key)})
		}
	}
	checks = append(checks, parseWindowsInfoJSONVersions(t, readRepoFile(t, "build", "windows", "info.json"))...)

	var drifted []string
	for _, c := range checks {
		if c.got != appVersion {
			drifted = append(drifted, c.label+"="+c.got)
		}
	}
	if len(drifted) > 0 {
		t.Fatalf("版本号与 appinfo.go Version=%q 不一致（各打包文件必须同步改）：\n%s",
			appVersion, strings.Join(drifted, "\n"))
	}
}

// extractVersion 是单正则提取的公共壳，失败时报出字段名而非裸正则。
func extractVersion(t *testing.T, re *regexp.Regexp, raw string, what string) string {
	t.Helper()
	m := re.FindStringSubmatch(raw)
	if m == nil {
		t.Fatalf("未找到 %s 字段", what)
	}
	return m[1]
}
