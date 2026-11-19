package session

import (
	"strings"
	"testing"
)

// 对齐 Rust 版 session/text.rs 内联测试。

func longText() string {
	return strings.Repeat("很长的标题", 30)
}

func TestNormalizeTitleKeepsLongTitlesIntact(t *testing.T) {
	title := NormalizeTitle(longText())
	// 标题不再截断：会话列表整宽铺开后按需换行展示全文。
	if strings.HasSuffix(title, "...") {
		t.Errorf("title should not be truncated: %q", title)
	}
	if len([]rune(title)) != len([]rune("很长的标题"))*30 {
		t.Errorf("title rune count = %d, want %d", len([]rune(title)), len([]rune("很长的标题"))*30)
	}
}

func TestNormalizeTitleStillFlattensNewlinesAndLinks(t *testing.T) {
	title := NormalizeTitle("第一行\n[名字](/very/long/path)")
	if title != "第一行 名字" {
		t.Errorf("title = %q, want %q", title, "第一行 名字")
	}
}

func TestTruncateSummaryCapsAt72Chars(t *testing.T) {
	if got := TruncateSummary("短摘要"); got != "短摘要" {
		t.Errorf("short summary = %q, want unchanged", got)
	}

	capped := TruncateSummary(longText())
	if !strings.HasSuffix(capped, "...") {
		t.Errorf("capped summary should end with ...: %q", capped)
	}
	if len([]rune(capped)) != 75 {
		t.Errorf("capped rune count = %d, want 75", len([]rune(capped)))
	}
}

// 块起始标记必须先于单行噪音判断（Rust 注释里的顺序约束）：
// <system-reminder> 等开标签自身命中噪音判定，但必须进入跳过状态而不是被
// 当作单行噪音剥掉，否则块内容逐行泄漏。
func TestSanitizeUserMessageTextSkipsWholeNoiseBlocks(t *testing.T) {
	text := "<system-reminder>\ninternal leak line\n</system-reminder>\nreal question"
	got, ok := SanitizeUserMessageText(text)
	if !ok {
		t.Fatal("sanitize returned None, want Some")
	}
	if got != "real question" {
		t.Errorf("sanitized = %q, want %q", got, "real question")
	}
}

// 闭合标记命中后恢复产出；<xx> 形状的单行残渣按尖括号规则剥掉。
func TestSanitizeUserMessageTextKeepsFollowingContent(t *testing.T) {
	text := "<INSTRUCTIONS>\nsecret\n</INSTRUCTIONS>\n<tag-like>\nafter"
	got, ok := SanitizeUserMessageText(text)
	if !ok {
		t.Fatal("sanitize returned None, want Some")
	}
	if got != "after" {
		t.Errorf("sanitized = %q, want %q", got, "after")
	}
}

func TestSanitizeUserBlocksKeepsToolBlocksAndDropsEmptyText(t *testing.T) {
	tool := diagnosticBlock("tool_use", "run", nil)
	textOnly := diagnosticBlock("text", "clean text", nil)
	empty := diagnosticBlock("text", "noise", nil)
	empty.Text = nil

	out := SanitizeUserBlocks([]ContentBlock{tool, textOnly, empty})
	if len(out) != 2 {
		t.Fatalf("blocks = %d, want 2", len(out))
	}
	if out[0].Kind != "tool_use" || out[1].Kind != "text" {
		t.Errorf("kept kinds = %s, %s", out[0].Kind, out[1].Kind)
	}
}
