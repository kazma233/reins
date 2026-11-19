package session

import (
	"testing"
)

// 对齐 Rust 版 session/model.rs 内联测试。

func modelFixture(id string, role string, provider string, model string) SessionMessage {
	message := SessionMessage{ID: id, Role: role, Blocks: []ContentBlock{}}
	if model != "" {
		m := NewSessionModel(provider, model)
		message.Model = &m
	}
	return message
}

func TestModelTallyOrdersByCallsThenFirstSeen(t *testing.T) {
	var tally ModelTally
	tally.Record("custom", "gpt-5.6-terra")
	tally.Record("custom", "deepseek-flash")
	tally.Record("custom", "gpt-5.6-terra")

	models := tally.IntoModels()
	// 调用次数多的排前；并列时保持首次出现顺序。
	want := []SessionModel{
		NewSessionModel("custom", "gpt-5.6-terra"),
		NewSessionModel("custom", "deepseek-flash"),
	}
	if len(models) != len(want) {
		t.Fatalf("model count = %d, want %d", len(models), len(want))
	}
	for i := range want {
		if models[i] != want[i] {
			t.Errorf("models[%d] = %+v, want %+v", i, models[i], want[i])
		}
	}
}

func TestModelTallyKeepsSameModelFromDifferentProvidersApart(t *testing.T) {
	var tally ModelTally
	tally.Record("first", "deepseek-flash")
	tally.Record("second", "deepseek-flash")
	tally.Record("", "deepseek-flash")
	tally.Record("first", "deepseek-flash")

	models := tally.IntoModels()
	if len(models) != 3 {
		t.Fatalf("model count = %d, want 3", len(models))
	}
	if models[0] != NewSessionModel("first", "deepseek-flash") {
		t.Errorf("models[0] = %+v, want first/deepseek-flash", models[0])
	}
}

func TestModelTallySkipsBlankModelNames(t *testing.T) {
	var tally ModelTally
	tally.Record("", "   ")
	if models := tally.IntoModels(); len(models) != 0 {
		t.Fatalf("models = %+v, want empty", models)
	}
}

func TestAbsorbKeepsRootPrimaryFirst(t *testing.T) {
	var tally ModelTally
	tally.Absorb([]SessionModel{
		NewSessionModel("custom", "gpt-5.6-terra"),
		NewSessionModel("custom", "gpt-5.5"),
	})
	tally.Absorb([]SessionModel{NewSessionModel("custom", "gpt-5.5")})

	// 并列次数时保持并入顺序：root 的主模型仍排最前。
	models := tally.IntoModels()
	if len(models) == 0 || models[0] != NewSessionModel("custom", "gpt-5.6-terra") {
		t.Fatalf("models[0] = %+v, want gpt-5.6-terra first", models)
	}
}

func TestUserMessagesTakeTheModelOfTheFollowingAnswer(t *testing.T) {
	messages := []SessionMessage{
		modelFixture("u1", "user", "", ""),
		modelFixture("a1", "assistant", "custom", "deepseek-flash"),
		modelFixture("u2", "user", "", ""),
		modelFixture("t1", "tool", "", ""),
		modelFixture("a2", "assistant", "custom", "gpt-6-luna"),
		modelFixture("u3", "user", "", ""),
	}
	FillUserMessageModels(messages)

	want := []string{"deepseek-flash", "deepseek-flash", "gpt-6-luna", "", "gpt-6-luna", ""}
	for i, message := range messages {
		got := ""
		if message.Model != nil {
			got = message.Model.Model
		}
		if got != want[i] {
			t.Errorf("messages[%d].model = %q, want %q", i, got, want[i])
		}
	}
	// 末尾没有后续回答，保持未知而不是猜。
	if messages[5].Model != nil {
		t.Errorf("trailing user message model = %+v, want nil", messages[5].Model)
	}
}

// NewSessionModel 的归一化契约：空白 provider 归 None（空串）。
func TestNewSessionModelNormalizesProvider(t *testing.T) {
	if sm := NewSessionModel("  ", "m"); sm.Provider != "" {
		t.Errorf("provider = %q, want empty", sm.Provider)
	}
	if sm := NewSessionModel(" custom ", "m"); sm.Provider != "custom" {
		t.Errorf("provider = %q, want custom", sm.Provider)
	}
}
