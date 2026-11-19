// addressing_test.go：复合地址的往返与边界（对齐 addressing.rs 内嵌测试）。
// 格式只有这一处所有者，行为必须有直测守着。
package workspace

import "testing"

func TestTargetRefRoundTripsThroughCompositeID(t *testing.T) {
	global := targetRef{agentID: "codex"}
	if got := global.CompositeID(); got != "codex" {
		t.Fatalf("got %s", got)
	}
	if parseTargetID(global.CompositeID()) != global {
		t.Fatalf("往返失真：%+v", parseTargetID(global.CompositeID()))
	}

	project := targetRef{projectID: "my-app", agentID: "claude"}
	if got := project.CompositeID(); got != "my-app:claude" {
		t.Fatalf("got %s", got)
	}
	if parseTargetID(project.CompositeID()) != project {
		t.Fatalf("往返失真：%+v", parseTargetID(project.CompositeID()))
	}
}

// 空段一律按全局 id 处理（`:codex` / `demo:` / `:` 都不是合法项目地址）；
// 多冒号归第一段——项目 id 含 `:` 时这个地址必然查不到，读取侧会明确报错。
func TestTargetRefRejectsEmptySegments(t *testing.T) {
	// 与前端 model.test.ts::parseTargetId 的同一用例集：两端口径必须一致，
	// 否则同一个 id 在前后端会落到不同项目分组。
	for _, malformed := range []string{":codex", "demo:", ":"} {
		parsed := parseTargetID(malformed)
		if parsed.projectID != "" || string(parsed.agentID) != malformed {
			t.Fatalf("%s 应落回全局 id，得到 %+v", malformed, parsed)
		}
	}

	parsed := parseTargetID("a:b:codex")
	want := targetRef{projectID: "a", agentID: "b:codex"}
	if parsed != want {
		t.Fatalf("got %+v, want %+v", parsed, want)
	}
}
