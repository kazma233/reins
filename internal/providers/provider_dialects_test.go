// 方言表 roundtrip 守卫：empty_state 的映射文案、apply 写入、inspect 反读
// 三者同源的前提是正反互逆。收编前 dsh 因缺一份覆写把 off 档的映射文案
// 下发成 "none"（实际写 "off"），这里按适配器遍历堵住同类脱钩。
package providers

import (
	"testing"

	"reins/internal/agents"
)

func dialectTestApps() []ProviderApp {
	return agents.ProviderApps()
}

// 支持档位的拼写能反读回原档位。
func TestReasoningDialectRoundtrip(t *testing.T) {
	for _, app := range dialectTestApps() {
		adapter := AdapterFor(app)
		dialect := adapter.ReasoningDialect()
		for _, level := range adapter.Capability().SupportedReasoningLevels {
			spelled := dialect.Spell(level)
			if parsed, ok := dialect.Parse(spelled); !ok || parsed != level {
				t.Fatalf("%s 档位 %s 拼写 %s 反读不闭合：%v %s", app, level, spelled, ok, parsed)
			}
		}
		if _, ok := dialect.Parse("banana"); ok {
			t.Fatalf("%s 未知拼写应留空", app)
		}
	}
}

// dsh 回归：off 档写入 "off"（pi-ai 方言），映射文案不得再出现 off→none。
func TestDshOffLevelSpellsOff(t *testing.T) {
	dialect := AdapterFor(agents.ProviderDsh).ReasoningDialect()
	if spelled := dialect.Spell(ReasoningOff); spelled != "off" {
		t.Fatalf("off 拼写：%s", spelled)
	}
	if parsed, ok := dialect.Parse("off"); !ok || parsed != ReasoningOff {
		t.Fatalf("off 反读：%v %s", ok, parsed)
	}
}

// ZCode 回归：off 档写 "disabled"（唯一的关闭拼写；"none" 只在
// chat-completions 兜底里被当关闭，anthropic-messages 与 openai-responses
// 会把它当普通 effort 透传）。
func TestZcodeOffLevelSpellsDisabled(t *testing.T) {
	dialect := AdapterFor(agents.ProviderZcode).ReasoningDialect()
	if spelled := dialect.Spell(ReasoningOff); spelled != "disabled" {
		t.Fatalf("off 拼写：%s", spelled)
	}
	if parsed, ok := dialect.Parse("disabled"); !ok || parsed != ReasoningOff {
		t.Fatalf("disabled 反读：%v %s", ok, parsed)
	}
	if spelled := dialect.Spell(ReasoningMax); spelled != "max" {
		t.Fatalf("max 拼写：%s", spelled)
	}
}

// dsh 的 LLM 层就是 pi-ai：两个适配器必须引用同一份方言常量，防止再长出
// 两份拷贝（Rust 按指针地址守卫，Go 按同一包级变量的指针相等）。
func TestPiAndDshSharePiAIDialect(t *testing.T) {
	pi := AdapterFor(agents.ProviderPi)
	dsh := AdapterFor(agents.ProviderDsh)
	if pi.ReasoningDialect() != dsh.ReasoningDialect() {
		t.Fatal("pi 与 dsh 应引用同一份思考档位方言实例")
	}
	piProtocol := pi.ProtocolDialect()
	dshProtocol := dsh.ProtocolDialect()
	if piProtocol == nil || dshProtocol == nil {
		t.Fatal("Pi 协议方言必填")
	}
	if piProtocol != dshProtocol {
		t.Fatal("pi 与 dsh 应引用同一份协议方言实例")
	}
	if dsh.ReasoningDialect() != piAIReasoning {
		t.Fatal("dsh 应引用 piAIReasoning 常量本体")
	}
}

// 支持协议的拼写能反读回原协议；opencode 的 v1 遗留包名只出现在反读里。
func TestProtocolDialectRoundtripAndAliases(t *testing.T) {
	for _, app := range dialectTestApps() {
		adapter := AdapterFor(app)
		dialect := adapter.ProtocolDialect()
		if dialect == nil {
			// Claude 的协议语义固定，无方言可声明。
			if app != agents.ProviderClaude {
				t.Fatalf("%s 无协议方言", app)
			}
			continue
		}
		for _, protocol := range adapter.Capability().SupportedProtocols {
			spelled := dialect.Spell(protocol)
			if parsed, ok := dialect.Parse(spelled); !ok || parsed != protocol {
				t.Fatalf("%s 协议 %s 拼写 %s 反读不闭合：%v %s", app, protocol, spelled, ok, parsed)
			}
		}
	}

	opencode := AdapterFor(agents.ProviderOpencode).ProtocolDialect()
	if opencode == nil {
		t.Fatal("OpenCode 协议方言必填")
	}
	cases := map[string]ProviderProtocol{
		"@ai-sdk/openai-compatible":                          ProtocolOpenaiChatCompletions,
		"@opencode/ai/providers/openai-compatible/responses": ProtocolOpenaiResponses,
		"@opencode/ai/providers/anthropic-compatible":        ProtocolAnthropicMessages,
		"@ai-sdk/anthropic":                                  ProtocolAnthropicMessages,
	}
	for alias, expected := range cases {
		if parsed, ok := opencode.Parse(alias); !ok || parsed != expected {
			t.Fatalf("别名 %s 反读不符：%v %s", alias, ok, parsed)
		}
	}
	if _, ok := opencode.Parse("banana"); ok {
		t.Fatal("未知拼写应留空")
	}
}
