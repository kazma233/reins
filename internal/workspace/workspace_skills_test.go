// workspace_skills_test.go：SkillSourceConfigView 的 camelCase 序列化钉子
// （对齐 tests/workspace_skills.rs）。逐字节断言在 types_wire_test.go 已覆盖
// 两种变体；这里保留 Rust 版的字段级断言口径（键存在性/键不存在性）。
package workspace

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSkillSourceViewsSerializeWithCamelCaseFields(t *testing.T) {
	local := SkillSourceConfigView{
		Type:                rawSourceKindLocal,
		ID:                  "local-source",
		RootPath:            "/tmp/skills",
		Description:         strPtr("个人笔记技能库"),
		IncludeNamePatterns: []string{"foo*"},
		IncludePathPatterns: []string{},
		Label:               "Local",
	}

	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if value["type"] != "local" {
		t.Fatalf("type：%+v", value["type"])
	}
	if value["rootPath"] != "/tmp/skills" {
		t.Fatalf("rootPath：%+v", value["rootPath"])
	}
	if value["description"] != "个人笔记技能库" {
		t.Fatalf("description：%+v", value["description"])
	}
	namePatterns, ok := value["includeNamePatterns"].([]any)
	if !ok || len(namePatterns) != 1 || namePatterns[0] != "foo*" {
		t.Fatalf("includeNamePatterns：%+v", value["includeNamePatterns"])
	}
	for _, forbidden := range []string{"root_path", "exclude_patterns"} {
		if _, exists := value[forbidden]; exists {
			t.Fatalf("不应出现 %s：%v", forbidden, value)
		}
	}
}

func TestGitSkillSourceViewSerializesLastFetchedAt(t *testing.T) {
	git := SkillSourceConfigView{
		Type:                rawSourceKindGit,
		ID:                  "git-source",
		Repo:                "https://github.com/acme/skills.git",
		Ref:                 strPtr("main"),
		LastFetchedAt:       ptrInt64(1_744_366_400_000),
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
		Label:               "Git",
	}

	encoded, err := json.Marshal(git)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got, ok := value["lastFetchedAt"].(float64); !ok || got != 1_744_366_400_000 {
		t.Fatalf("lastFetchedAt：%+v", value["lastFetchedAt"])
	}
	if _, exists := value["last_fetched_at"]; exists {
		t.Fatalf("不应出现 snake_case 键：%v", value)
	}
	// 未填描述时字段整体省略，前端按无标题处理。
	if _, exists := value["description"]; exists {
		t.Fatalf("未填描述应省略：%v", value)
	}
}

// 网络冒烟用例：与 Rust 一样默认忽略（#[ignore = "requires network access"]），
// 手动 -run 触发。
func TestDiscoverGitSkillsSmoke(t *testing.T) {
	if os.Getenv("REINS_DISCOVER_SMOKE") != "1" {
		t.Skip("requires network access; set REINS_DISCOVER_SMOKE=1 to run")
	}
	state := &SkillDiscoveryState{}
	store := NewWorkspaceConfigStore()
	result, err := DiscoverGitSkillsInner(store, state, "https://github.com/anthropics/skills.git", strPtr("main"))
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	if len(result.Skills) == 0 {
		t.Fatal("skills should not be empty")
	}
	hasFrontendDesign := false
	for _, skill := range result.Skills {
		if skill.Name == "frontend-design" {
			hasFrontendDesign = true
		}
		if skill.RelativePath == "" {
			t.Fatalf("relative_path should not be empty：%+v", skill)
		}
	}
	if !hasFrontendDesign {
		t.Fatal("frontend-design skill expected")
	}
}
