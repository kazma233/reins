package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 纯文本助手（对齐 Rust 版 session/text.rs）：事件摘要、块级清洗、
// 标题归一、注入上下文判定、图片引用还原。

// SummarizeEvent：事件行的单行摘要。优先 message 字段，其次 type 字段（与
// kind 不同时拼 kind 前缀），最后按 name 兜底；72 字符截断。
func SummarizeEvent(kind string, value any) string {
	var summary string
	if message, ok := jsonString(value, "message"); ok {
		summary = NormalizeTitle(message)
	} else if eventType, ok := jsonString(value, "type"); ok {
		if eventType != kind {
			summary = fmt.Sprintf("%s: %s", kind, NormalizeTitle(eventType))
		} else {
			summary = fallbackEventSummary(kind, value)
		}
	} else {
		summary = fallbackEventSummary(kind, value)
	}
	return TruncateSummary(summary)
}

func fallbackEventSummary(kind string, value any) string {
	if name, ok := jsonString(value, "name"); ok {
		return fmt.Sprintf("%s: %s", kind, NormalizeTitle(name))
	}
	return kind
}

// ToolText：tool 事件的提示文本，优先 arguments 字符串，其次 output 的
// JSON 展开。
func ToolText(payload any) (string, bool) {
	if arguments, ok := jsonString(payload, "arguments"); ok {
		return arguments, true
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return "", false
	}
	output, ok := object["output"]
	if !ok {
		return "", false
	}
	return stringifyJSON(output)
}

// stringifyJSON 对齐 serde_json::to_string_pretty：两空格缩进、无尾随换行、
// 不做 HTML 转义（serde_json 原样输出 < > &，Go 默认转义会让前端读到的
// payload 字面量与 Rust 时代不一致）。
func stringifyJSON(value any) (string, bool) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return "", false
	}
	// Encode 按流式约定补一个换行，剥掉以对齐 to_string_pretty。
	return strings.TrimSuffix(buf.String(), "\n"), true
}

func diagnosticBlock(kind string, text string, payload any) ContentBlock {
	return ContentBlock{Kind: kind, Text: &text, Payload: payload}
}

func rawSourceBlock(kind string, value any) ContentBlock {
	text, ok := stringifyJSON(value)
	if !ok {
		text = "null"
	}
	return diagnosticBlock(kind, text, value)
}

// UnsupportedContentBlock：未识别的 content 项原样透出（nil ≡ Value::Null）。
func UnsupportedContentBlock(_source string, content any) ContentBlock {
	return rawSourceBlock("unsupported_content", content)
}

func UnsupportedBlock(_source string, block any) ContentBlock {
	return rawSourceBlock("unsupported_block", block)
}

func EmptyMessageBlock(_source string, _reason string, payload any) ContentBlock {
	return rawSourceBlock("empty_message", payload)
}

// MessageErrorBlock：消息没有可见内容、但来源自己给了错误文案（Pi 的
// errorMessage）：正文放错误文案，让前端按错误样式展示；原始报文留在
// payload 里供展开查看。
func MessageErrorBlock(errorMessage string, payload any) ContentBlock {
	isError := true
	return ContentBlock{Kind: "message_error", Text: &errorMessage, IsError: &isError, Payload: payload}
}

func EmptyToolBlock(_source string, payload any) ContentBlock {
	return rawSourceBlock("empty_tool", payload)
}

// RecordPayload：raw 页展示的原始记录，整条记录原样给出去，但加密字段不下发
// ——不可读的加密载荷不进界面（Grok Build 的思考块同样只投影可展示内容）。
func RecordPayload(record any) any {
	return stripEncrypted(record)
}

func stripEncrypted(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		stripped := make(map[string]any, len(typed))
		for key, item := range typed {
			if strings.Contains(strings.ToLower(key), "encrypted") {
				continue
			}
			stripped[key] = stripEncrypted(item)
		}
		return stripped
	case []any:
		stripped := make([]any, len(typed))
		for i, item := range typed {
			stripped[i] = stripEncrypted(item)
		}
		return stripped
	default:
		return value
	}
}

// ImageReference：会话里的图片项，各来源字段不同（Codex `image_url`、Pi
// `data`+`mimeType`、Claude `source.data`/`media_type`、Grok Build `url`），
// 统一还原成前端能直接用的引用：能拼成 data: URL 的拼成 data: URL，否则
// 原样给出外链/路径。缺字段时不猜：返回 false，调用方仍按未支持块展示
// 原始报文。
func ImageReference(item any) (string, bool) {
	if reference, ok := jsonString(item, "image_url"); ok {
		return reference, true
	}
	if reference, ok := jsonString(item, "url"); ok {
		return reference, true
	}
	if url, ok := jsonString(item, "source", "url"); ok {
		return url, true
	}

	data, dataOK := jsonString(item, "data")
	if !dataOK {
		data, dataOK = jsonString(item, "source", "data")
	}
	mime, mimeOK := jsonString(item, "mimeType")
	if !mimeOK {
		mime, mimeOK = jsonString(item, "source", "media_type")
	}
	if !dataOK || !mimeOK {
		return "", false
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, data), true
}

// InjectedContextBlock：宿主注入的上下文（AGENTS.md 指令、
// environment_context、system-reminder 等）清洗后会让消息没有可展示的对话
// 内容，但原文本身要能看。判定复用清洗用的同一批前缀，且要求整条消息的
// 文本项都是注入内容；只要有一条真对话文本就不算。nil 返回值 ≡ Rust None。
func InjectedContextBlock(content any, payload any) *ContentBlock {
	var texts []string
	switch typed := content.(type) {
	case nil:
		return nil
	case string:
		// Claude Code 的 user 消息允许直接给字符串正文。
		texts = []string{typed}
	case []any:
		texts = make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := jsonString(item, "text")
			if !ok {
				// 任一项取不到 text 即整体放弃（Rust collect::<Option<_>>）。
				return nil
			}
			texts = append(texts, text)
		}
	default:
		return nil
	}

	if len(texts) == 0 {
		return nil
	}
	for _, text := range texts {
		if !IsTransportMessage(text) && !IsTitleNoise(text) {
			return nil
		}
	}

	joined := strings.Join(texts, "\n\n")
	// Rust 在此处 payload.clone()：同一 payload 还会被调用方放回消息原文，
	// 深拷贝避免两处共享同一 map。
	block := diagnosticBlock("context_injection", joined, deepCloneJSON(payload))
	return &block
}

func IsTransportMessage(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{
		"<environment_context>",
		"<local-command-caveat>",
		"<turn_aborted>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<skills_instructions>",
		"<subagent_notification>",
	} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func IsTitleNoise(text string) bool {
	trimmed := trimStartSpace(text)
	if trimmed == "" {
		return false
	}
	stripped := trimStartSpace(strings.TrimLeft(trimmed, "#"))
	return strings.HasPrefix(stripped, "AGENTS.md instructions for") ||
		strings.HasPrefix(stripped, "<system-reminder>") ||
		strings.HasPrefix(stripped, "</system-reminder>")
}

func TitleCandidateFromText(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false
	}

	if !IsTitleNoise(trimmed) && !IsTransportMessage(trimmed) {
		return NormalizeTitle(trimmed), true
	}

	// 带噪音前缀的消息（如 Codex 注入的 AGENTS.md 指令）复用消息展示的块级
	// 清洗，保证 <INSTRUCTIONS> 等块内容不会被逐行扫描误判为标题。
	sanitized, ok := SanitizeUserMessageText(trimmed)
	if !ok {
		return "", false
	}

	for _, line := range strings.Split(sanitized, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return NormalizeTitle(line), true
		}
	}
	return "", false
}

// SanitizeUserMessageText：剥除宿主注入块与噪音行，保留真实对话文本；全空
// 时返回 false（≡ Rust None）。
func SanitizeUserMessageText(text string) (string, bool) {
	var lines []string
	var skipUntil *string

	for _, rawLine := range splitLines(text) {
		line := strings.TrimSpace(rawLine)

		if skipUntil != nil {
			if strings.HasPrefix(line, *skipUntil) {
				skipUntil = nil
			}
			continue
		}

		if line == "" {
			lines = append(lines, "")
			continue
		}

		// 块起始标记必须先于单行噪音判断：<system-reminder> 等开标签本身
		// 也命中 is_title_noise/is_transport_message，顺序反了会导致
		// skip_until 永远不生效、块内容泄漏出来。
		if blockEnd, ok := blockStartTag(line); ok {
			skipUntil = &blockEnd
			continue
		}

		if IsTitleNoise(line) || IsTransportMessage(line) {
			continue
		}

		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}

		lines = append(lines, strings.TrimRightFunc(rawLine, unicode.IsSpace))
	}

	sanitized := strings.Join(lines, "\n")
	trimmed := strings.TrimSpace(sanitized)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// blockStartTag：块起始标记 → 对应闭合标记。独立成表便于对照 Rust 里顺序
// 展开的四个 if 分支。
func blockStartTag(line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "<INSTRUCTIONS>"):
		return "</INSTRUCTIONS>", true
	case strings.HasPrefix(line, "<system-reminder>"):
		return "</system-reminder>", true
	case strings.HasPrefix(line, "<environment_context>"):
		return "</environment_context>", true
	case strings.HasPrefix(line, "<subagent_notification>"):
		return "</subagent_notification>", true
	default:
		return "", false
	}
}

// splitLines 对齐 Rust str::lines：按 \n 切分、每行剥掉紧邻的一个 \r、末行
// 允许无换行；空串产出零行。
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	rawLines := strings.Split(text, "\n")
	for i, line := range rawLines {
		if trimmed, found := strings.CutSuffix(line, "\r"); found {
			rawLines[i] = trimmed
		}
	}
	return rawLines
}

func SanitizeUserBlocks(blocks []ContentBlock) []ContentBlock {
	var out []ContentBlock
	for _, block := range blocks {
		if block.Text != nil {
			if sanitized, ok := SanitizeUserMessageText(*block.Text); ok {
				block.Text = &sanitized
			} else {
				block.Text = nil
			}
		}
		switch block.Kind {
		case "tool_use", "tool_result", "function_call", "function_call_output":
		default:
			if block.Text == nil {
				continue
			}
		}
		out = append(out, block)
	}
	return out
}

// NormalizeTitle：标题只做归一（剥链接语法、压平换行、去首尾空白），不截断
// ：会话列表整屏铺开后标题按需换行完整展示，截断是窄侧栏时代的产物。
func NormalizeTitle(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(stripMarkdownLinks(text), "\n", " "))
}

// TruncateSummary：单行摘要（事件行、工具提示）仍是一行渲染，过长会撑高行
// 也会让载荷变大，统一在这里按 72 字符截断。
func TruncateSummary(text string) string {
	const maxChars = 72

	if utf8.RuneCountInString(text) <= maxChars {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxChars]) + "..."
}

// stripMarkdownLinks：标题里剥掉 markdown 链接语法，[label](url) 只留
// label。不剥的话长标题（或截断后的摘要）可能全是链接语法（如
// [name](/very/long/path)），看不到任何实际文本。
func stripMarkdownLinks(text string) string {
	var result strings.Builder
	result.Grow(len(text))
	rest := text

	for {
		open := strings.IndexByte(rest, '[')
		if open < 0 {
			break
		}
		labelOffset := strings.Index(rest[open:], "](")
		if labelOffset < 0 {
			break
		}
		labelEnd := open + labelOffset
		urlOffset := strings.IndexByte(rest[labelEnd+2:], ')')
		if urlOffset < 0 {
			break
		}
		urlEnd := labelEnd + 2 + urlOffset

		result.WriteString(rest[:open])
		result.WriteString(rest[open+1 : labelEnd])
		rest = rest[urlEnd+1:]
	}

	result.WriteString(rest)
	return result.String()
}

// trimStartSpace 对齐 Rust trim_start（仅去头部 Unicode 空白）。
func trimStartSpace(text string) string {
	return strings.TrimLeftFunc(text, unicode.IsSpace)
}
