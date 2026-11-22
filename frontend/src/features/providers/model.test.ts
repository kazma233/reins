import { describe, expect, it } from "vitest";
import { AGENT_LABELS } from "@shared/lib/agent-labels";
import { ProviderApp } from "@bindings/reins/internal/agents/models";
import {
  ProviderAppEntryStatus,
  ProviderProtocol,
  ProviderWriteMode,
  ReasoningLevel,
  type ProviderAppEntry,
  type ProviderAppState,
  type ProviderModelView,
  type ProviderView,
  type ProvidersState,
} from "@bindings/reins/internal/providers/models";
import {
  APP_LABELS,
  applyBlockers,
  applyCandidates,
  emptyModelForm,
  emptyProviderForm,
  formToInput,
  entriesForProvider,
  hasModelId,
  initialApplySelection,
  initialSyncSelection,
  mergeFetchedModels,
  normalizeProviderIdInput,
  protocolCompatible,
  protocolIncompatibleTip,
  providerApplyInput,
  providerSyncApps,
  providerSyncInput,
  providerSyncResultText,
  providerSyncTargets,
  type ProviderSyncApp,
  reasoningEffortMappingText,
  reasoningEffortWrite,
  reasoningIntersectionNote,
  reasoningLevelChoices,
  reasoningLevelOptions,
  reapplyProvider,
  toolLevelOptions,
  toolLevelText,
  unwrittenModelFieldsText,
  validateProviderForm,
} from "./model";

function appState(overrides: Partial<ProviderAppState> = {}): ProviderAppState {
  return {
    app: ProviderApp.ProviderCodex,
    configPaths: [],
    configExists: false,
    supportedProtocols: [ProviderProtocol.ProtocolOpenaiResponses],
    supportedReasoningLevels: [
      ReasoningLevel.ReasoningMinimal,
      ReasoningLevel.ReasoningLow,
      ReasoningLevel.ReasoningMedium,
      ReasoningLevel.ReasoningHigh,
    ],
    reasoningLevelWrites: [],
    additive: false,
    unwrittenModelFields: [],
    behaviorNotes: [],
    entries: [],
    loadError: null,
    ...overrides,
  };
}

function provider(overrides: Partial<ProviderView> = {}): ProviderView {
  return {
    id: "openrouter",
    label: "OpenRouter",
    protocol: ProviderProtocol.ProtocolOpenaiResponses,
    baseUrl: "https://openrouter.test/v1",
    apiKey: "sk-test-secret",
    models: [],
    ...overrides,
  };
}

describe("protocolCompatible", () => {
  it("returns false when the app does not accept the provider protocol", () => {
    const app = appState({ supportedProtocols: [ProviderProtocol.ProtocolOpenaiChatCompletions] });
    expect(protocolCompatible(app, provider())).toBe(false);
  });

  it("returns true when the protocol is supported", () => {
    expect(protocolCompatible(appState(), provider())).toBe(true);
  });
});

describe("protocolIncompatibleTip", () => {
  it("names the provider protocol and the app's supported ones when incompatible", () => {
    const app = appState({ supportedProtocols: [ProviderProtocol.ProtocolOpenaiResponses] });
    const incompatible = provider({ protocol: ProviderProtocol.ProtocolAnthropicMessages });
    expect(protocolIncompatibleTip(app, incompatible)).toBe(
      "Codex 不支持协议 Anthropic Messages，支持：OpenAI Responses"
    );
  });

  it("returns null when compatible so the tooltip stays hidden", () => {
    expect(protocolIncompatibleTip(appState(), provider())).toBeNull();
  });
});

describe("reasoningLevelOptions", () => {
  it("falls back to the app's supported levels when the model has none", () => {
    const options = reasoningLevelOptions(appState(), { reasoningLevels: undefined });
    expect(options).toEqual(["minimal", "low", "medium", "high"]);
  });

  it("intersects the model levels with the app support set", () => {
    const options = reasoningLevelOptions(appState(), {
      reasoningLevels: [
        ReasoningLevel.ReasoningOff,
        ReasoningLevel.ReasoningLow,
        ReasoningLevel.ReasoningXhigh,
        ReasoningLevel.ReasoningHigh,
      ],
    });
    expect(options).toEqual(["low", "high"]);
  });

  it("returns an empty list when nothing intersects", () => {
    const options = reasoningLevelOptions(appState(), { reasoningLevels: [ReasoningLevel.ReasoningMax] });
    expect(options).toEqual([]);
  });
});

describe("reasoningEffortWrite", () => {
  it("returns the mapped write value published by the backend", () => {
    const app = appState({
      reasoningLevelWrites: [
        { level: ReasoningLevel.ReasoningLow, writes: "low" },
        { level: ReasoningLevel.ReasoningMax, writes: "xhigh" },
      ],
    });
    expect(reasoningEffortWrite(app, ReasoningLevel.ReasoningMax)).toBe("xhigh");
    expect(reasoningEffortWrite(app, ReasoningLevel.ReasoningLow)).toBe("low");
  });

  it("falls back to the level name when the app publishes no mapping", () => {
    expect(reasoningEffortWrite(appState(), ReasoningLevel.ReasoningHigh)).toBe("high");
  });
});

describe("reasoningEffortMappingText", () => {
  it("lists only the levels whose write value differs", () => {
    const app = appState({
      reasoningLevelWrites: [
        { level: ReasoningLevel.ReasoningLow, writes: "low" },
        { level: ReasoningLevel.ReasoningMax, writes: "xhigh" },
        { level: ReasoningLevel.ReasoningOff, writes: "none" },
      ],
    });
    expect(reasoningEffortMappingText(app)).toBe("max→xhigh、off→none");
  });

  it("returns null when every level writes its own name", () => {
    const app = appState({
      reasoningLevelWrites: [{ level: ReasoningLevel.ReasoningLow, writes: "low" }],
    });
    expect(reasoningEffortMappingText(app)).toBeNull();
  });
});

describe("unwrittenModelFieldsText", () => {
  it("lists the model metadata the app does not write", () => {
    const text = unwrittenModelFieldsText(
      appState({ app: ProviderApp.ProviderGrokbuild, unwrittenModelFields: ["最大输出", "图像输入"] })
    );
    expect(text).toContain("Grok Build");
    expect(text).toContain("最大输出、图像输入");
  });

  it("returns null when the app writes every field", () => {
    expect(unwrittenModelFieldsText(appState({ app: ProviderApp.ProviderPi }))).toBeNull();
  });
});

describe("applyBlockers", () => {
  it("lists protocol, key and model blockers", () => {
    const app = appState({ supportedProtocols: [ProviderProtocol.ProtocolAnthropicMessages] });
    const blockers = applyBlockers(app, provider({ apiKey: null }), []);
    expect(blockers).toHaveLength(3);
    expect(blockers[0]).toContain("不支持协议");
    expect(blockers[1]).toContain("API Key");
    expect(blockers[2]).toContain("至少选择一个模型");
  });

  it("returns nothing when everything is satisfied", () => {
    expect(applyBlockers(appState(), provider(), ["model-a"])).toEqual([]);
  });
});

describe("normalizeProviderIdInput", () => {
  it("lowercases and converts underscores like the backend", () => {
    expect(normalizeProviderIdInput("GLM_OAI")).toBe("glm-oai");
  });

  it("strips characters outside the allowed set", () => {
    expect(normalizeProviderIdInput(" DeepSeek OAI ")).toBe("deepseekoai");
  });

  it("keeps already valid ids untouched", () => {
    expect(normalizeProviderIdInput("glm-oai-2")).toBe("glm-oai-2");
  });
});

describe("entriesForProvider", () => {
  it("filters entries that belong to the provider", () => {
    const app = appState({
      entries: [
        {
          key: "reins-openrouter",
          status: ProviderAppEntryStatus.EntryStatusApplied,
          providerId: "openrouter",
          label: "OpenRouter",
          baseUrl: "https://x",
          modelIds: ["model-a"],
          defaultModelId: "model-a",
          notes: [],
        },
        {
          key: "someone-else",
          status: ProviderAppEntryStatus.EntryStatusExternal,
          providerId: null,
          label: null,
          baseUrl: "https://y",
          modelIds: [],
          defaultModelId: null,
          notes: [],
        },
      ],
    });
    expect(entriesForProvider(app, "openrouter")).toHaveLength(1);
    expect(entriesForProvider(app, "other")).toHaveLength(0);
  });
});

describe("reapplyProvider", () => {
  function entry(
    status: ProviderAppEntry["status"],
    providerId: string | null
  ): ProviderAppEntry {
    return {
      key: "entry",
      status,
      providerId,
      label: null,
      baseUrl: null,
      modelIds: [],
      defaultModelId: null,
      notes: [],
    };
  }

  it("resolves applied and drifted entries to their provider", () => {
    const providers = [provider({ id: "glm" })];
    expect(reapplyProvider(entry(ProviderAppEntryStatus.EntryStatusApplied, "glm"), providers)?.id).toBe("glm");
    expect(reapplyProvider(entry(ProviderAppEntryStatus.EntryStatusDrifted, "glm"), providers)?.id).toBe("glm");
  });

  it("returns null for external entries and for deleted providers", () => {
    const providers = [provider({ id: "glm" })];
    expect(reapplyProvider(entry(ProviderAppEntryStatus.EntryStatusExternal, null), providers)).toBeNull();
    expect(reapplyProvider(entry(ProviderAppEntryStatus.EntryStatusDrifted, "gone"), providers)).toBeNull();
  });
});

describe("initialApplySelection", () => {
  function appliedEntry(
    modelIds: string[],
    defaultModelId: string | null
  ): ProviderAppEntry {
    return {
      key: "reins-openrouter",
      status: ProviderAppEntryStatus.EntryStatusApplied,
      providerId: "openrouter",
      label: "OpenRouter",
      baseUrl: "https://openrouter.test/v1",
      modelIds,
      defaultModelId,
      notes: [],
    };
  }

  const catalog = provider({
    models: [
      { id: "model-a", label: "Model A" },
      { id: "model-b", label: "Model B" },
      { id: "model-c", label: "Model C" },
    ],
  });

  it("reuses the applied models and keeps the current default model", () => {
    const app = appState({ entries: [appliedEntry(["model-b", "model-a"], "model-b")] });
    expect(initialApplySelection(app, catalog)).toEqual({
      modelIds: ["model-b", "model-a"],
      defaultModelId: "model-b",
      defaultReasoningLevel: null,
    });
  });

  it("drops applied models that are no longer in the catalog", () => {
    const app = appState({ entries: [appliedEntry(["model-a", "removed"], "removed")] });
    expect(initialApplySelection(app, catalog)).toEqual({
      modelIds: ["model-a"],
      defaultModelId: "model-a",
      defaultReasoningLevel: null,
    });
  });

  it("preselects the reasoning level currently written in the tool config", () => {
    const app = appState({ defaultReasoningLevel: ReasoningLevel.ReasoningHigh });
    expect(initialApplySelection(app, catalog).defaultReasoningLevel).toBe("high");
  });

  it("drops a current level the app does not support", () => {
    // fixture 的支持集是 minimal/low/medium/high；max 不在其中，写入会被后端拒绝。
    const app = appState({ defaultReasoningLevel: ReasoningLevel.ReasoningMax });
    expect(initialApplySelection(app, catalog).defaultReasoningLevel).toBeNull();
  });

  it("preselects the whole catalog when nothing is applied yet", () => {
    expect(initialApplySelection(appState(), catalog)).toEqual({
      modelIds: ["model-a", "model-b", "model-c"],
      defaultModelId: "model-a",
      defaultReasoningLevel: null,
    });
    expect(initialApplySelection(undefined, catalog)).toEqual({
      modelIds: ["model-a", "model-b", "model-c"],
      defaultModelId: "model-a",
      defaultReasoningLevel: null,
    });
  });
});

describe("reasoningLevelChoices", () => {
  const models = provider({
    models: [
      { id: "model-a", label: "Model A", reasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh] },
      { id: "model-b", label: "Model B", reasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningMedium] },
    ],
  });

  it("intersects the selected models' levels with the app support set", () => {
    expect(reasoningLevelChoices(appState(), models, ["model-a", "model-b"], null)).toEqual(["low"]);
  });

  it("keeps the current level even when the intersection excludes it", () => {
    // model-a 单独选中时交集是 low/high，medium 只有当前值提供。
    expect(reasoningLevelChoices(appState(), models, ["model-a"], ReasoningLevel.ReasoningHigh)).toEqual(["low", "high"]);
    expect(reasoningLevelChoices(appState(), models, ["model-a"], ReasoningLevel.ReasoningMedium)).toEqual([
      "low",
      "high",
      "medium",
    ]);
  });

  it("returns nothing when no model is selected", () => {
    expect(reasoningLevelChoices(appState(), models, [], null)).toEqual([]);
  });

  it("offers the full tool set for additive apps regardless of model levels", () => {
    // 多供应商并存的工具档位只是默认预填值，模型声明不收窄候选。
    const additive = appState({
      additive: true,
      supportedReasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh, ReasoningLevel.ReasoningMax],
    });
    expect(
      reasoningLevelChoices(additive, models, ["model-a", "model-b"], null)
    ).toEqual(["low", "high", "max"]);
    expect(
      reasoningLevelChoices(additive, models, ["model-a"], ReasoningLevel.ReasoningMax)
    ).toEqual(["low", "high", "max"]);
  });
});

describe("reasoningIntersectionNote", () => {
  const models = provider({
    models: [
      { id: "model-a", label: "Model A", reasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh] },
      { id: "model-b", label: "Model B", reasoningLevels: null },
      { id: "model-c", label: "Model C", reasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningMedium] },
    ],
  });

  it("names the excluded levels and the models that exclude them", () => {
    // 工具支持 minimal/low/medium/high，交集是 low；被排除的档位按排除方分组。
    const note = reasoningIntersectionNote(
      appState(),
      models,
      ["model-a", "model-c"],
      null
    );
    expect(note).toContain("最低（minimal） 因 model-a、model-c 未声明");
    expect(note).toContain("高（high） 因 model-c 未声明");
    expect(note).toContain("中（medium） 因 model-a 未声明");
  });

  it("returns null when no model constrains the levels", () => {
    expect(
      reasoningIntersectionNote(appState(), models, ["model-b"], null)
    ).toBeNull();
  });

  it("returns null when nothing is excluded", () => {
    const covering = provider({
      models: [{ id: "model-a", label: "Model A", reasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh, ReasoningLevel.ReasoningMinimal, ReasoningLevel.ReasoningMedium] }],
    });
    expect(
      reasoningIntersectionNote(appState(), covering, ["model-a"], null)
    ).toBeNull();
  });

  it("keeps the current level out of the excluded list even when unconstrained models drop it", () => {
    // 当前已写入的档位会追加进候选，不属于「未列出」。
    const note = reasoningIntersectionNote(
      appState(),
      models,
      ["model-c"],
      ReasoningLevel.ReasoningHigh
    );
    expect(note).toBe("候选档位取的是选中模型支持范围的交集：最低（minimal） 因 model-c 未声明。");
  });

  it("returns null when the intersection is empty (empty-state hint covers it)", () => {
    const exclusive = provider({
      models: [{ id: "model-a", label: "Model A", reasoningLevels: [ReasoningLevel.ReasoningMax] }],
    });
    expect(
      reasoningIntersectionNote(appState(), exclusive, ["model-a"], null)
    ).toBeNull();
  });

  it("returns null for additive apps since candidates are never narrowed", () => {
    const additive = appState({ additive: true });
    expect(
      reasoningIntersectionNote(additive, models, ["model-a", "model-c"], null)
    ).toBeNull();
  });
});

describe("toolLevelOptions and toolLevelText", () => {
  it("builds tool level options from the app's supported set with a no-op entry", () => {
    const additive = appState({
      additive: true,
      supportedReasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh],
      reasoningLevelWrites: [{ level: ReasoningLevel.ReasoningHigh, writes: "xhigh" }],
    });
    expect(toolLevelOptions(additive)).toEqual([
      { value: "", label: "不设置（应用不改档位）" },
      { value: "low", label: "低（low）" },
      { value: "high", label: "高（high，写入 xhigh）" },
    ]);
  });

  it("describes the unset tool level as keep-as-is", () => {
    const additive = appState({ additive: true });
    expect(toolLevelText(additive)).toContain("不改各模型的档位");
  });

  it("describes the configured tool level with its write value", () => {
    const additive = appState({
      additive: true,
      appReasoningLevel: ReasoningLevel.ReasoningMax,
      reasoningLevelWrites: [{ level: ReasoningLevel.ReasoningMax, writes: "max" }],
    });
    expect(toolLevelText(additive)).toContain("最高（max）");
    expect(toolLevelText(additive)).toContain("Agent 页");
  });

  it("says the tool does not write levels when the support set is empty", () => {
    const noLevels = appState({ additive: true, supportedReasoningLevels: [] });
    expect(toolLevelText(noLevels)).toContain("不写入思考档位");
    expect(toolLevelOptions(noLevels)).toEqual([
      { value: "", label: "不设置（应用不改档位）" },
    ]);
  });
});

describe("applyCandidates", () => {
  function entry(
    key: string,
    status: ProviderAppEntry["status"],
    providerId: string | null
  ): ProviderAppEntry {
    return {
      key,
      status,
      providerId,
      label: null,
      baseUrl: null,
      modelIds: [],
      defaultModelId: null,
      notes: [],
    };
  }

  it("hides providers that already have a reins entry, applied or drifted", () => {
    const app = appState({
      entries: [
        entry("reins-glm", ProviderAppEntryStatus.EntryStatusApplied, "glm"),
        entry("reins-ds", ProviderAppEntryStatus.EntryStatusDrifted, "deepseek"),
      ],
    });
    const candidates = applyCandidates(app, [
      provider({ id: "glm" }),
      provider({ id: "deepseek" }),
      provider({ id: "platform-a" }),
    ]);
    expect(candidates.map((item) => item.id)).toEqual(["platform-a"]);
  });

  it("keeps providers whose entries are only external", () => {
    const app = appState({ entries: [entry("someone-else", ProviderAppEntryStatus.EntryStatusExternal, null)] });
    const candidates = applyCandidates(app, [provider({ id: "glm" })]);
    expect(candidates.map((item) => item.id)).toEqual(["glm"]);
  });

  it("keeps every provider when the app has no entries", () => {
    const candidates = applyCandidates(appState(), [provider()]);
    expect(candidates).toHaveLength(1);
  });
});

describe("hasModelId", () => {
  it("matches ids after trimming both sides", () => {
    expect(hasModelId(["glm-4.5", "deepseek-chat"], " glm-4.5 ")).toBe(true);
    expect(hasModelId(["glm-4.5"], "glm-4.6")).toBe(false);
  });

  it("stays case sensitive like the backend", () => {
    expect(hasModelId(["glm-4.5"], "GLM-4.5")).toBe(false);
  });
});

describe("mergeFetchedModels", () => {
  it("adds new models and skips ids already in the catalog", () => {
    const existing = [{ ...emptyModelForm("glm-4.5"), id: "glm-4.5", label: "GLM 4.5" }];
    const { added, skipped } = mergeFetchedModels(existing, [
      { id: "glm-4.5", name: "GLM 4.5" },
      { id: "glm-4.6", name: "GLM 4.6" },
    ]);
    expect(skipped.map((model) => model.id)).toEqual(["glm-4.5"]);
    expect(added).toHaveLength(1);
    expect(added[0]).toMatchObject({ id: "glm-4.6", label: "GLM 4.6" });
  });

  it("falls back to the id when the fetched model has no name", () => {
    const { added } = mergeFetchedModels([], [{ id: "glm-4.6" }]);
    expect(added[0].label).toBe("glm-4.6");
  });

  it("skips repeated ids inside the fetched list", () => {
    const { added, skipped } = mergeFetchedModels([], [
      { id: "glm-4.6" },
      { id: "glm-4.6" },
    ]);
    expect(added).toHaveLength(1);
    expect(skipped.map((model) => model.id)).toEqual(["glm-4.6"]);
  });

  it("compares ids with the same trimming as the backend", () => {
    const existing = [{ ...emptyModelForm("glm-4.5 "), id: "glm-4.5 " }];
    const { added, skipped } = mergeFetchedModels(existing, [{ id: "glm-4.5" }]);
    expect(added).toHaveLength(0);
    expect(skipped).toHaveLength(1);
  });
});

describe("providerApplyInput", () => {
  it("passes the raw selection through with the provider id and app", () => {
    const input = providerApplyInput(appState(), provider(), ProviderApp.ProviderCodex, {
      modelIds: ["glm-4.5", "glm-4.6"],
      defaultModelId: "glm-4.6",
      defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
    });
    expect(input).toEqual({
      providerId: "openrouter",
      app: "codex",
      modelIds: ["glm-4.5", "glm-4.6"],
      defaultModelId: "glm-4.6",
      defaultReasoningLevel: "high",
    });
  });

  it("writes the tool-level default for additive apps regardless of the dialog selection", () => {
    const input = providerApplyInput(
      appState({ additive: true, appReasoningLevel: ReasoningLevel.ReasoningLow }),
      provider(),
      ProviderApp.ProviderCodex,
      { modelIds: ["glm-4.5"], defaultModelId: "glm-4.5", defaultReasoningLevel: ReasoningLevel.ReasoningHigh }
    );
    expect(input.defaultReasoningLevel).toBe("low");
  });

  it("writes no level for additive apps without a tool-level default", () => {
    const input = providerApplyInput(
      appState({ additive: true }),
      provider(),
      ProviderApp.ProviderCodex,
      { modelIds: ["glm-4.5"], defaultModelId: "glm-4.5", defaultReasoningLevel: ReasoningLevel.ReasoningHigh }
    );
    expect(input.defaultReasoningLevel).toBeNull();
  });

  it("keeps the chosen level when it stays in the intersection candidates", () => {
    const input = providerApplyInput(appState(), provider(), ProviderApp.ProviderCodex, {
      modelIds: ["glm-4.5"],
      defaultModelId: "glm-4.5",
      defaultReasoningLevel: ReasoningLevel.ReasoningMedium,
    });
    expect(input.defaultReasoningLevel).toBe("medium");
  });

  it("keeps the chosen level even when the catalog narrowed — current is always a candidate", () => {
    const narrowed = provider({
      models: [{ id: "glm-4.5", label: "glm-4.5", reasoningLevels: [ReasoningLevel.ReasoningLow] }],
    });
    const input = providerApplyInput(appState(), narrowed, ProviderApp.ProviderCodex, {
      modelIds: ["glm-4.5"],
      defaultModelId: "glm-4.5",
      defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
    });
    // 与 providerSyncInput 同一契约：目录收窄不把已选档位静默抹掉。
    expect(input.defaultReasoningLevel).toBe("high");
  });

  it("writes no level when the dialog selection has none", () => {
    const input = providerApplyInput(appState(), provider(), ProviderApp.ProviderCodex, {
      modelIds: ["glm-4.5"],
      defaultModelId: "glm-4.5",
      defaultReasoningLevel: null,
    });
    expect(input.defaultReasoningLevel).toBeNull();
  });

  it("writes no level when the app state is missing (unreachable in practice, dialog opens from an app card)", () => {
    const input = providerApplyInput(undefined, provider(), ProviderApp.ProviderCodex, {
      modelIds: ["glm-4.5"],
      defaultModelId: "glm-4.5",
      defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
    });
    expect(input.defaultReasoningLevel).toBeNull();
    expect(input.modelIds).toEqual(["glm-4.5"]);
  });
});

describe("provider sync", () => {
  function entry(providerId: string, overrides: Partial<ProviderAppEntry> = {}): ProviderAppEntry {
    return {
      key: `reins-${providerId}`,
      status: ProviderAppEntryStatus.EntryStatusApplied,
      providerId,
      label: null,
      baseUrl: null,
      modelIds: [],
      defaultModelId: null,
      notes: [],
      protocol: null,
      ...overrides,
    };
  }

  function stateOf(apps: ProviderAppState[], providers: ProviderView[]): ProvidersState {
    return { configPath: "providers.yaml", providers, apps };
  }

  function model(id: string, overrides: Partial<ProviderModelView> = {}): ProviderModelView {
    return { id, label: id, ...overrides };
  }

  describe("providerSyncApps", () => {
    it("plans applied and drifted entries and ignores external ones", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5")] });
      const apps = providerSyncApps(
        stateOf(
          [
            appState({ app: ProviderApp.ProviderCodex, entries: [entry("glm")] }),
            appState({ app: ProviderApp.ProviderClaude, entries: [entry("glm", { status: ProviderAppEntryStatus.EntryStatusDrifted })] }),
            appState({
              app: ProviderApp.ProviderOpencode,
              entries: [
                {
                  key: "someone-else",
                  status: ProviderAppEntryStatus.EntryStatusExternal,
                  providerId: null,
                  label: null,
                  baseUrl: null,
                  modelIds: [],
                  defaultModelId: null,
                  notes: [],
                },
              ],
            }),
          ],
          [target]
        ),
        target
      );
      expect(apps.map((item) => item.app)).toEqual(["codex", "claude"]);
      expect(apps.every((item) => item.blockers.length === 0)).toBe(true);
    });

    it("reports protocol and key blockers as hard stops without dropping the candidate", () => {
      const target = provider({ id: "glm", apiKey: "", models: [model("glm-4.5")] });
      const apps = [appState({ supportedProtocols: [ProviderProtocol.ProtocolAnthropicMessages], entries: [entry("glm")] })];
      const result = providerSyncApps(stateOf(apps, [target]), target);
      expect(result).toHaveLength(1);
      const reasons = result[0].blockers.join("");
      expect(reasons).toContain("不支持协议");
      expect(reasons).toContain("API Key");
    });

    it("plans nothing for a provider that is not applied anywhere", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5")] });
      expect(providerSyncApps(stateOf([appState()], [target]), target)).toEqual([]);
    });
  });

  describe("initialSyncSelection", () => {
    it("unions applied models across targets and drops ids out of the catalog", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5"), model("glm-4.6")] });
      const apps = [
        appState({ app: ProviderApp.ProviderCodex, entries: [entry("glm", { modelIds: ["glm-4.5", "glm-legacy"] })] }),
        appState({ app: ProviderApp.ProviderClaude, entries: [entry("glm", { modelIds: ["glm-4.6"] })] }),
      ];
      expect(
        initialSyncSelection(stateOf(apps, [target]), target, [
          ProviderApp.ProviderCodex,
          ProviderApp.ProviderClaude,
        ])
      ).toEqual(["glm-4.5", "glm-4.6"]);
    });

    it("falls back to the whole catalog when no applied model remains", () => {
      const target = provider({ id: "glm", models: [model("glm-4.6")] });
      const apps = [appState({ entries: [entry("glm", { modelIds: ["glm-legacy"] })] })];
      expect(
        initialSyncSelection(stateOf(apps, [target]), target, [ProviderApp.ProviderCodex])
      ).toEqual(["glm-4.6"]);
    });
  });

  describe("providerSyncInput", () => {
    it("swaps the model list and falls the default model back to the first selected one", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5"), model("glm-4.6")] });
      const apps = [
        appState({
          supportedReasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh],
          defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
          entries: [entry("glm", { modelIds: ["glm-4.5", "glm-4.6"], defaultModelId: "glm-4.5" })],
        }),
      ];
      const input = providerSyncInput(stateOf(apps, [target]), target, ProviderApp.ProviderCodex, ["glm-4.6"]);
      expect(input).toEqual({
        providerId: "glm",
        app: "codex",
        modelIds: ["glm-4.6"],
        defaultModelId: "glm-4.6",
        defaultReasoningLevel: "high",
      });
    });

    it("keeps the current default model and reasoning level when the selection contains them", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5"), model("glm-4.6")] });
      const apps = [
        appState({
          supportedReasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh],
          defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
          entries: [entry("glm", { modelIds: ["glm-4.5", "glm-4.6"], defaultModelId: "glm-4.5" })],
        }),
      ];
      const input = providerSyncInput(
        stateOf(apps, [target]),
        target,
        ProviderApp.ProviderCodex,
        ["glm-4.5", "glm-4.6"]
      );
      expect(input.defaultModelId).toBe("glm-4.5");
      expect(input.defaultReasoningLevel).toBe("high");
    });

    it("keeps the written level even when the selected model declares a narrower set", () => {
      const target = provider({
        id: "glm",
        models: [model("glm-4.5", { reasoningLevels: [ReasoningLevel.ReasoningLow] })],
      });
      const apps = [
        appState({
          supportedReasoningLevels: [ReasoningLevel.ReasoningLow, ReasoningLevel.ReasoningHigh],
          defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
          entries: [entry("glm", { modelIds: ["glm-4.5"], defaultModelId: "glm-4.5" })],
        }),
      ];
      // 与重新应用同一契约：目录收窄不把工具里已写入的档位静默抹掉。
      const input = providerSyncInput(stateOf(apps, [target]), target, ProviderApp.ProviderCodex, ["glm-4.5"]);
      expect(input.defaultReasoningLevel).toBe("high");
      expect(input.defaultModelId).toBe("glm-4.5");
    });

    it("drops the current level when the tool no longer supports it", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5")] });
      const apps = [
        appState({
          supportedReasoningLevels: [ReasoningLevel.ReasoningLow],
          defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
          entries: [entry("glm", { modelIds: ["glm-4.5"], defaultModelId: "glm-4.5" })],
        }),
      ];
      const input = providerSyncInput(stateOf(apps, [target]), target, ProviderApp.ProviderCodex, ["glm-4.5"]);
      expect(input.defaultReasoningLevel).toBeNull();
    });

    it("keeps the current level for additive apps regardless of model levels", () => {
      const target = provider({ id: "glm", models: [model("glm-4.5")] });
      const apps = [
        appState({
          additive: true,
          supportedReasoningLevels: [ReasoningLevel.ReasoningHigh],
          defaultReasoningLevel: ReasoningLevel.ReasoningHigh,
          entries: [entry("glm", { modelIds: ["glm-4.5"], defaultModelId: "glm-4.5" })],
        }),
      ];
      const input = providerSyncInput(stateOf(apps, [target]), target, ProviderApp.ProviderCodex, ["glm-4.5"]);
      expect(input.defaultReasoningLevel).toBe("high");
    });
  });

  describe("providerSyncTargets", () => {
    it("says not applied yet when there is no candidate app", () => {
      expect(providerSyncTargets([])).toEqual({
        targets: [],
        skipLines: [],
        buttonTitle: "尚未应用到任何 Agent。",
      });
    });

    it("blocks the sync and lists per-app reasons when every candidate is blocked", () => {
      const apps: ProviderSyncApp[] = [
        { app: ProviderApp.ProviderCodex, blockers: ["协议不兼容。"] },
        { app: ProviderApp.ProviderClaude, blockers: ["未设 API Key。", "协议不兼容。"] },
      ];
      const result = providerSyncTargets(apps);
      expect(result.targets).toEqual([]);
      // 阻断句嵌入行内时去掉句尾句号，多条原因按原顺序拼接。
      expect(result.skipLines).toEqual([
        "Codex（协议不兼容）",
        "Claude Code（未设 API Key。协议不兼容）",
      ]);
      expect(result.buttonTitle).toBe(
        "无法同步：Codex（协议不兼容）；Claude Code（未设 API Key。协议不兼容）。"
      );
    });

    it("keeps unblocked apps as targets and leaves the button enabled on partial block", () => {
      const apps: ProviderSyncApp[] = [
        { app: ProviderApp.ProviderCodex, blockers: [] },
        { app: ProviderApp.ProviderClaude, blockers: ["未设 API Key。"] },
      ];
      const result = providerSyncTargets(apps);
      expect(result.targets).toEqual([ProviderApp.ProviderCodex]);
      expect(result.skipLines).toEqual(["Claude Code（未设 API Key）"]);
      expect(result.buttonTitle).toBe("");
    });
  });

  describe("providerSyncResultText", () => {
    it("composes a synced-only sentence", () => {
      expect(providerSyncResultText(["Codex"], [], [])).toBe("已同步到 Codex。");
    });

    it("composes synced, skipped and failed segments into one sentence", () => {
      expect(
        providerSyncResultText(
          ["Codex", "ZCode"],
          ["Grok Build：连接超时"],
          ["Claude Code（未设 API Key）"]
        )
      ).toBe(
        "已同步到 Codex、ZCode；未同步：Claude Code（未设 API Key）；失败：Grok Build：连接超时。"
      );
    });

    it("says no syncable agent when nothing succeeded", () => {
      expect(providerSyncResultText([], [], ["Claude Code（未设 API Key）"])).toBe(
        "没有可同步的 Agent；未同步：Claude Code（未设 API Key）。"
      );
    });
  });
});

describe("APP_LABELS", () => {
  it("uses the same product names as targets and session sources", () => {
    for (const [appId, label] of Object.entries(APP_LABELS)) {
      expect(label).toBe(AGENT_LABELS[appId]);
    }
  });
});

describe("validateProviderForm", () => {
  function form(overrides: Partial<ReturnType<typeof emptyProviderForm>> = {}) {
    return {
      ...emptyProviderForm(),
      label: "Label",
      baseUrl: "https://a.test/v1",
      ...overrides,
    };
  }

  it("rejects a new provider that reuses an existing id", () => {
    const errors = validateProviderForm(form({ providerId: "custom-gateway" }), {
      allowEmptyModels: true,
      existingProviderIds: ["custom-gateway"],
    });

    expect(errors.providerId).toBe("该提供商 ID 已存在，请换一个。");
  });

  it("compares the normalized id, not the raw input", () => {
    // 输入侧会归一化，这里兜住绕过输入归一化的调用（大小写、下划线）
    const errors = validateProviderForm(form({ providerId: "Custom_Gateway" }), {
      allowEmptyModels: true,
      existingProviderIds: ["custom-gateway"],
    });

    expect(errors.providerId).toBeDefined();
  });

  it("lets an edit keep its own id", () => {
    const errors = validateProviderForm(
      form({ originalProviderId: "custom-gateway", providerId: "custom-gateway" }),
      { allowEmptyModels: false, existingProviderIds: ["custom-gateway"] },
    );

    expect(errors.providerId).toBeUndefined();
  });

  it("skips the model requirement only while creating the first step", () => {
    expect(
      validateProviderForm(form({ providerId: "new-one" }), {
        allowEmptyModels: true,
        existingProviderIds: [],
      }).models,
    ).toBeUndefined();

    expect(
      validateProviderForm(form({ providerId: "new-one" }), {
        allowEmptyModels: false,
        existingProviderIds: [],
      }).models,
    ).toBe("至少添加一个模型。");
  });
});

describe("formToInput", () => {
  it("carries the caller's write intent", () => {
    const state = { ...emptyProviderForm(), providerId: "p1", label: "L", baseUrl: "https://a.test/v1" };

    expect(formToInput(state, ProviderWriteMode.WriteModeCreate).mode).toBe("create");
    expect(formToInput(state, ProviderWriteMode.WriteModeUpdate).mode).toBe("update");
  });
});
