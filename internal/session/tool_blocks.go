package session

import "strings"

// OpenCode 家族的 tool part 装配（对齐 Rust 版 session/tool_blocks.rs）
// ：opencode（v2 session_message 的 data）与 zcode（part 表的行）的 tool 块
// 同构——state.input 是参数对象或字符串、state.status 标记错误，差异只在
// 字段名与输出承载，由 ToolShape 声明。
// 输入/输出拆成两条 UI 块，与前端 function_call/function_call_output 的
// 分组契约一致；输出文本提到 payload 顶层，前端 resultOutputText 直接读
// payload.output。

// ToolShape：一个来源 tool part 的形态差异。
type ToolShape struct {
	// ToolNameKey：tool 名的字段（zcode "tool"，opencode "name"）。
	ToolNameKey string
	// CallIDKey：call id 的字段（zcode "callID"，opencode "id"）。
	CallIDKey string
	// OutputKey：state 内承载输出的键；输入块的 payload 要剥掉它，避免成倍变大。
	OutputKey string
	// ExtractOutput：输出键取值到文本的提取（zcode 是字符串，opencode v2 是块数组）。
	ExtractOutput func(value any) (string, bool)
}

// TextOutput：zcode——state.output 直接是字符串。
func TextOutput(value any) (string, bool) {
	text, ok := value.(string)
	return text, ok
}

// ContentBlocksOutput：opencode v2——state.content 是块数组（通常为
// {type:"text",text}），提取其中文本拼接；数组为空返回 false（running 中的
// 工具），非文本块降级为 JSON 展示。
func ContentBlocksOutput(content any) (string, bool) {
	items, ok := content.([]any)
	if !ok {
		return "", false
	}
	var texts []string
	for _, item := range items {
		if text, ok := jsonString(item, "text"); ok {
			texts = append(texts, text)
		}
	}

	if len(texts) == 0 {
		if len(items) == 0 {
			return "", false
		}
		return stringifyJSON(content)
	}

	return strings.Join(texts, "\n"), true
}

// ToolBlocks：按 shape 把一个 tool part 装配成 function_call /
// function_call_output 两条 UI 块。
func ToolBlocks(value any, shape ToolShape) []ContentBlock {
	toolName, toolNamePresent := jsonString(value, shape.ToolNameKey)
	toolCallID, toolCallIDPresent := jsonString(value, shape.CallIDKey)
	state, _ := getJSONObjectPath(value, "state")
	input, inputPresent := getJSONObjectPath(state, "input")
	outputText := ""
	outputPresent := false
	if raw, ok := getJSONObjectPath(state, shape.OutputKey); ok {
		if text, ok := shape.ExtractOutput(raw); ok {
			outputText = text
			outputPresent = true
		}
	}
	status, _ := getJSONObjectPath(state, "status")
	statusText, _ := status.(string)
	isError := statusText == "error"

	var blocks []ContentBlock

	// JSON null 不算输入（Rust is_some_and(|item| !item.is_null())）。
	if inputPresent && input != nil {
		payload := deepCloneJSON(value)
		// 输出内容不重复放进输入块，避免 payload 成倍变大。
		if payloadState, ok := getJSONObjectPath(payload, "state"); ok {
			if stateObject, ok := payloadState.(map[string]any); ok {
				delete(stateObject, shape.OutputKey)
			}
		}
		if payloadObject, ok := payload.(map[string]any); ok {
			payloadObject["input"] = deepCloneJSON(input)
		}

		blocks = append(blocks, ContentBlock{
			Kind:       "function_call",
			Text:       textPointer(toolInputText(input, inputPresent)),
			ToolName:   optionalString(toolName, toolNamePresent),
			ToolCallID: optionalString(toolCallID, toolCallIDPresent),
			IsError:    nil,
			Payload:    payload,
		})
	}

	if outputPresent {
		payload := deepCloneJSON(value)
		// 输出文本提到顶层，前端 resultOutputText 直接读 payload.output。
		if payloadObject, ok := payload.(map[string]any); ok {
			payloadObject["output"] = outputText
			if inputPresent {
				payloadObject["input"] = deepCloneJSON(input)
			}
		}

		var isErrorPtr *bool
		if isError {
			isErrorPtr = &isError
		}
		outputCopy := outputText
		blocks = append(blocks, ContentBlock{
			Kind:       "function_call_output",
			Text:       &outputCopy,
			ToolName:   optionalString(toolName, toolNamePresent),
			ToolCallID: optionalString(toolCallID, toolCallIDPresent),
			IsError:    isErrorPtr,
			Payload:    payload,
		})
	}

	return blocks
}

// toolInputText：输入是字符串原样给，其余展开成 JSON。
func toolInputText(input any, present bool) (string, bool) {
	if !present {
		return "", false
	}
	if text, ok := input.(string); ok {
		return text, true
	}
	return stringifyJSON(input)
}

// getJSONObjectPath：对象键取值，返回 (值, 是否存在)。JSON null 与缺失都让
// 调用方看到 ok=false 以外还能区分（null 时值本身就是 nil）。
func getJSONObjectPath(value any, key string) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := object[key]
	return raw, ok
}

// deepCloneJSON：JSON 值深拷贝。Go 的 map/slice 是引用类型，payload 手术
// （剥输出、插 input）必须基于独立副本，否则会改写解析出的原始 part。
func deepCloneJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			cloned[key] = deepCloneJSON(item)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for i, item := range typed {
			cloned[i] = deepCloneJSON(item)
		}
		return cloned
	default:
		return value
	}
}

// optionalString：字段存在即给指针（含空串——serde Some("") 序列化为 ""，
// 只有缺失才该是 nil）。
func optionalString(text string, present bool) *string {
	if !present {
		return nil
	}
	return &text
}

func textPointer(text string, ok bool) *string {
	if !ok {
		return nil
	}
	return &text
}
