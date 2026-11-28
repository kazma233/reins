// types_wire_test.go：wire 契约形状的逐字节钉子。契约面是 frontend/bindings/
// （wails 生成、前端唯一类型来源）：camelCase 字段、snake_case 枚举
// （SkillLinkState 是唯一 camelCase 例外）、Option 区分省略与写 null。
// 这里的断言与 wire 实际输出逐字节一致，漂移即测试红。
package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"reins/internal/agents"
)

// SkillSourceConfigView 是 tag="type" 判别联合的 Go 单 struct 建模：local
// 变体不含 git 字段、git 变体不含 root_path，公共字段恒出现。整条 wire 文本
// 与 Rust serde 输出逐字节一致（含键序：tag 在首位、变体字段按声明序）。
func TestSkillSourceConfigViewLocalWireShapeIsByteExact(t *testing.T) {
	view := SkillSourceConfigView{
		Type:                rawSourceKindLocal,
		ID:                  "local-source",
		RootPath:            "/tmp/skills",
		Description:         strPtr("个人笔记技能库"),
		IncludeNamePatterns: []string{"foo*"},
		IncludePathPatterns: []string{},
		Label:               "Local",
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"local","id":"local-source","rootPath":"/tmp/skills","description":"个人笔记技能库","includeNamePatterns":["foo*"],"includePathPatterns":[],"label":"Local"}`
	if string(encoded) != want {
		t.Fatalf("local 变体 wire 形状漂移：\n got: %s\nwant: %s", encoded, want)
	}
	// 防御性断言：git 变体字段不得出现在 local 变体里（逐字节一致已蕴含，
	// 显式写出防未来加字段时 omitempty 被误删）。
	for _, forbidden := range []string{"repo", "ref", "lastFetchedAt"} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("local 变体不应携带 git 字段 %s：%s", forbidden, encoded)
		}
	}
}

func TestSkillSourceConfigViewGitWireShapeIsByteExact(t *testing.T) {
	view := SkillSourceConfigView{
		Type:                rawSourceKindGit,
		ID:                  "git-source",
		Repo:                "https://github.com/acme/skills.git",
		Ref:                 strPtr("main"),
		LastFetchedAt:       ptrInt64(1_744_366_400_000),
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
		Label:               "Git",
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"git","id":"git-source","repo":"https://github.com/acme/skills.git","ref":"main","lastFetchedAt":1744366400000,"includeNamePatterns":[],"includePathPatterns":[],"label":"Git"}`
	if string(encoded) != want {
		t.Fatalf("git 变体 wire 形状漂移：\n got: %s\nwant: %s", encoded, want)
	}
	if strings.Contains(string(encoded), `rootPath`) {
		t.Fatalf("git 变体不应携带 local 字段：%s", encoded)
	}
}

// 未填的 description/ref/lastFetchedAt 整体省略（serde skip_serializing_if）。
func TestSkillSourceConfigViewOptionalFieldsOmittedWhenUnset(t *testing.T) {
	git := SkillSourceConfigView{
		Type:                rawSourceKindGit,
		ID:                  "git-source",
		Repo:                "repo",
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
		Label:               "Git",
	}
	encoded, err := json.Marshal(git)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"git","id":"git-source","repo":"repo","includeNamePatterns":[],"includePathPatterns":[],"label":"Git"}`
	if string(encoded) != want {
		t.Fatalf("未填 Option 应整体省略：\n got: %s\nwant: %s", encoded, want)
	}

	local := SkillSourceConfigView{
		Type:                rawSourceKindLocal,
		ID:                  "local-source",
		RootPath:            "/tmp/skills",
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
		Label:               "Local",
	}
	encoded, err = json.Marshal(local)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want = `{"type":"local","id":"local-source","rootPath":"/tmp/skills","includeNamePatterns":[],"includePathPatterns":[],"label":"Local"}`
	if string(encoded) != want {
		t.Fatalf("未填 Option 应整体省略：\n got: %s\nwant: %s", encoded, want)
	}
}

// SkillLinkState 是前端契约里唯一的 camelCase 枚举；McpPathState 是
// snake_case。拼写漂移会让前端 narrow 失效。
func TestEnumWireSpellings(t *testing.T) {
	states := map[SkillLinkState]string{
		LinkStateLinked:       `"linked"`,
		LinkStateExcluded:     `"excluded"`,
		LinkStateSourceMissed: `"sourceMissing"`,
		LinkStateUnmanaged:    `"unmanaged"`,
	}
	for value, want := range states {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", value, err)
		}
		if string(encoded) != want {
			t.Fatalf("SkillLinkState 拼写漂移：%s != %s", encoded, want)
		}
	}

	pathStates := map[McpPathState]string{
		PathStatePresent: `"present"`,
		PathStateMissing: `"missing"`,
		PathStateError:   `"error"`,
	}
	for value, want := range pathStates {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", value, err)
		}
		if string(encoded) != want {
			t.Fatalf("McpPathState 拼写漂移：%s != %s", encoded, want)
		}
	}

	transports := map[McpTransport]string{
		TransportStdio: `"stdio"`,
		TransportHTTP:  `"http"`,
		TransportSSE:   `"sse"`,
	}
	for value, want := range transports {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", value, err)
		}
		if string(encoded) != want {
			t.Fatalf("McpTransport 拼写漂移：%s != %s", encoded, want)
		}
	}
}

// RawSkillSourceDefinition 的 JSON 形态：tag="type" snake_case、键序按 Rust
// serde 字段声明序、无 skip 属性的 Option 写 null、patterns 恒出现。
func TestRawSkillSourceDefinitionJSONShape(t *testing.T) {
	local := RawSkillSourceDefinition{
		Type:                rawSourceKindLocal,
		RootPath:            "/tmp/notes",
		IncludeNamePatterns: []string{},
		IncludePathPatterns: []string{},
	}
	encoded, err := json.Marshal(local)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"local","root_path":"/tmp/notes","description":null,"include_name_patterns":[],"include_path_patterns":[]}`
	if string(encoded) != want {
		t.Fatalf("local 变体 JSON 漂移：\n got: %s\nwant: %s", encoded, want)
	}

	git := RawSkillSourceDefinition{
		Type:                rawSourceKindGit,
		Repo:                "https://github.com/acme/skills.git",
		Ref:                 strPtr("main"),
		Description:         strPtr("官方"),
		IncludeNamePatterns: []string{"a*"},
		IncludePathPatterns: []string{},
	}
	encoded, err = json.Marshal(git)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want = `{"type":"git","repo":"https://github.com/acme/skills.git","ref":"main","description":"官方","include_name_patterns":["a*"],"include_path_patterns":[]}`
	if string(encoded) != want {
		t.Fatalf("git 变体 JSON 漂移：\n got: %s\nwant: %s", encoded, want)
	}

	// 往返：解析回同一变体；未知 type 报错（serde unknown variant）。
	var decoded RawSkillSourceDefinition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != rawSourceKindGit || decoded.Repo != git.Repo || decoded.Ref == nil || *decoded.Ref != "main" {
		t.Fatalf("往返失真：%+v", decoded)
	}
	if err := json.Unmarshal([]byte(`{"type":"svn","root_path":"/x"}`), &decoded); err == nil {
		t.Fatal("未知 type 必须报错")
	}
}

// McpConfigType 的 wire 拼写（config.yaml 存储串同源）。
func TestMcpConfigTypeSpellings(t *testing.T) {
	spellings := map[agents.McpConfigType]string{
		agents.McpConfigCommon:    "common",
		agents.McpConfigOpenCode:  "opencode",
		agents.McpConfigGrokBuild: "grokbuild",
		agents.McpConfigDsh:       "dsh",
		agents.McpConfigHermes:    "hermes",
	}
	for value, want := range spellings {
		if string(value) != want {
			t.Fatalf("McpConfigType 拼写漂移：%s != %s", value, want)
		}
	}
}

func ptrInt64(value int64) *int64 { return &value }
