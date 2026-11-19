package session

import (
	"strings"
	"testing"
)

// 对齐 Rust 版 tests/tool_blocks.rs：tool part 装配的形态测试，两种
// ToolShape 共用同一装配体，断言字段差异由 shape 承载、payload 手术（输入
// 块剥输出、输出提到顶层）与前端契约。

func mustJSON(t *testing.T, raw string) any {
	t.Helper()
	value, err := parseJSONLine(raw)
	if err != nil {
		t.Fatalf("fixture parse failed: %v", err)
	}
	return value
}

var toolShapeZcode = ToolShape{
	ToolNameKey:   "tool",
	CallIDKey:     "callID",
	OutputKey:     "output",
	ExtractOutput: TextOutput,
}

var toolShapeOpencode = ToolShape{
	ToolNameKey:   "name",
	CallIDKey:     "id",
	OutputKey:     "content",
	ExtractOutput: ContentBlocksOutput,
}

func TestZcodeShapeSplitsInputAndStringOutput(t *testing.T) {
	part := mustJSON(t, `{
		"type": "tool",
		"tool": "read_file",
		"callID": "call-1",
		"state": {
			"status": "completed",
			"input": {"path": "a.rs"},
			"output": "file body"
		}
	}`)

	blocks := ToolBlocks(part, toolShapeZcode)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}

	first := blocks[0]
	if first.Kind != "function_call" {
		t.Errorf("kind = %q, want function_call", first.Kind)
	}
	if first.ToolName == nil || *first.ToolName != "read_file" {
		t.Errorf("toolName = %v, want read_file", first.ToolName)
	}
	if first.ToolCallID == nil || *first.ToolCallID != "call-1" {
		t.Errorf("toolCallID = %v, want call-1", first.ToolCallID)
	}
	if first.Text == nil || !strings.Contains(*first.Text, "a.rs") {
		t.Errorf("text = %v, want it to contain a.rs", first.Text)
	}
	// 输入块 payload 剥掉输出，不重复放大。
	state, ok := first.Payload.(map[string]any)["state"].(map[string]any)
	if !ok {
		t.Fatalf("payload.state missing")
	}
	if _, present := state["output"]; present {
		t.Error("input block payload should strip state.output")
	}

	second := blocks[1]
	if second.Kind != "function_call_output" {
		t.Errorf("kind = %q, want function_call_output", second.Kind)
	}
	if second.Text == nil || *second.Text != "file body" {
		t.Errorf("text = %v, want file body", second.Text)
	}
	// 输出提到顶层，前端 resultOutputText 直接读 payload.output。
	if top, ok := second.Payload.(map[string]any)["output"].(string); !ok || top != "file body" {
		t.Errorf("payload.output = %v, want file body", second.Payload)
	}
	if second.IsError != nil {
		t.Errorf("isError = %v, want nil", second.IsError)
	}
}

func TestOpencodeShapeJoinsContentBlocks(t *testing.T) {
	part := mustJSON(t, `{
		"type": "tool",
		"name": "bash",
		"id": "call-2",
		"state": {
			"status": "error",
			"input": "cargo test",
			"content": [
				{"type": "text", "text": "line 1"},
				{"type": "text", "text": "line 2"}
			]
		}
	}`)

	blocks := ToolBlocks(part, toolShapeOpencode)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if blocks[0].ToolName == nil || *blocks[0].ToolName != "bash" {
		t.Errorf("toolName = %v, want bash", blocks[0].ToolName)
	}
	if blocks[0].Text == nil || *blocks[0].Text != "cargo test" {
		t.Errorf("input text = %v, want cargo test", blocks[0].Text)
	}
	if blocks[1].Text == nil || *blocks[1].Text != "line 1\nline 2" {
		t.Errorf("output text = %v, want joined lines", blocks[1].Text)
	}
	if blocks[1].IsError == nil || !*blocks[1].IsError {
		t.Errorf("isError = %v, want true", blocks[1].IsError)
	}
	// 输入块剥掉的是 content 键。
	state, ok := blocks[0].Payload.(map[string]any)["state"].(map[string]any)
	if !ok {
		t.Fatalf("payload.state missing")
	}
	if _, present := state["content"]; present {
		t.Error("input block payload should strip state.content")
	}
}

func TestRunningToolWithoutOutputHasInputBlockOnly(t *testing.T) {
	part := mustJSON(t, `{
		"name": "bash",
		"id": "call-3",
		"state": {
			"status": "running",
			"input": "cargo build",
			"content": []
		}
	}`)

	blocks := ToolBlocks(part, toolShapeOpencode)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if blocks[0].Kind != "function_call" {
		t.Errorf("kind = %q, want function_call", blocks[0].Kind)
	}
}

func TestNonTextContentFallsBackToJSON(t *testing.T) {
	content := mustJSON(t, `[{"type": "image", "url": "file://x.png"}]`)
	text, ok := ContentBlocksOutput(content)
	if !ok || !strings.Contains(text, "image") {
		t.Errorf("output = %q %v, want JSON fallback mentioning image", text, ok)
	}
}

// 输入缺失（或 null）时只有输出块；payload 手术不得改动解析出的原始 part。
func TestNullInputStillEmitsOutputBlockAndOriginalUntouched(t *testing.T) {
	part := mustJSON(t, `{
		"tool": "read_file",
		"callID": "call-4",
		"state": {"status": "completed", "input": null, "output": "done"}
	}`)
	state := part.(map[string]any)["state"].(map[string]any)

	blocks := ToolBlocks(part, toolShapeZcode)
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(blocks))
	}
	if blocks[0].Kind != "function_call_output" {
		t.Errorf("kind = %q, want function_call_output", blocks[0].Kind)
	}
	// 原始 part 不被 payload 手术改写。
	if _, present := state["output"]; !present {
		t.Error("original part state.output was mutated")
	}
	if _, present := state["input"]; !present {
		t.Error("original part state.input was mutated")
	}
}
