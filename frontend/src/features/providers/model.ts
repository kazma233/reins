import { AGENT_LABELS } from "@shared/lib/agent-labels";
import { ProviderApp } from "@bindings/reins/internal/agents/models";
import {
  ProviderAppEntryStatus,
  ProviderProtocol,
  ReasoningLevel,
  type ApplyProviderInput,
  type FetchedModel,
  type ModelsDevMatchResult,
  type ProviderAppEntry,
  type ProviderAppState,
  type ProviderModelInput,
  type ProviderModelView,
  type ProviderUpsertInput,
  type ProviderView,
  type ProvidersState,
  type ProviderWriteMode,
} from "@bindings/reins/internal/providers/models";

// providers 域前端展示口径：标签文案、能力判定、应用弹窗可选项。

export type ProvidersTab = "providers" | "apps";

export const PROVIDERS_TAB_COPY: Array<{ id: ProvidersTab; label: string }> = [
  { id: "apps", label: "Agent" },
  { id: "providers", label: "提供商" },
];

// 工具名与 target / 会话来源共用同一份产品名常量（生成物）。
// APP_LABELS 与 AGENT_LABELS 有镜像断言（model.test），只含真实工具；
// $zero（bindings 枚举的 Go 零值占位，wire 上不会有意出现）经 appLabel
// 降级为空串。其余标签表的 $zero 键为脏数据兜底行。
export const APP_LABELS: Record<
  Exclude<ProviderApp, ProviderApp.$zero>,
  string
> = {
  [ProviderApp.ProviderCodex]: AGENT_LABELS.codex,
  [ProviderApp.ProviderClaude]: AGENT_LABELS.claude,
  [ProviderApp.ProviderOpencode]: AGENT_LABELS.opencode,
  [ProviderApp.ProviderPi]: AGENT_LABELS.pi,
  [ProviderApp.ProviderGrokbuild]: AGENT_LABELS.grokbuild,
  [ProviderApp.ProviderDsh]: AGENT_LABELS.dsh,
  [ProviderApp.ProviderZcode]: AGENT_LABELS.zcode,
};

export function appLabel(app: ProviderApp): string {
  return app === ProviderApp.$zero ? "" : APP_LABELS[app];
}

export const PROTOCOL_LABELS: Record<ProviderProtocol, string> = {
  [ProviderProtocol.ProtocolOpenaiResponses]: "OpenAI Responses",
  [ProviderProtocol.ProtocolOpenaiChatCompletions]: "OpenAI Chat Completions",
  [ProviderProtocol.ProtocolAnthropicMessages]: "Anthropic Messages",
  [ProviderProtocol.$zero]: "",
};

export const PROTOCOL_OPTIONS: Array<{ value: ProviderProtocol; label: string }> = [
  { value: ProviderProtocol.ProtocolOpenaiChatCompletions, label: "OpenAI Chat Completions" },
  { value: ProviderProtocol.ProtocolOpenaiResponses, label: "OpenAI Responses" },
  { value: ProviderProtocol.ProtocolAnthropicMessages, label: "Anthropic Messages" },
];

export const REASONING_LEVEL_LABELS: Record<ReasoningLevel, string> = {
  [ReasoningLevel.ReasoningOff]: "无",
  [ReasoningLevel.ReasoningMinimal]: "最低",
  [ReasoningLevel.ReasoningLow]: "低",
  [ReasoningLevel.ReasoningMedium]: "中",
  [ReasoningLevel.ReasoningHigh]: "高",
  [ReasoningLevel.ReasoningXhigh]: "超高",
  [ReasoningLevel.ReasoningMax]: "最高",
  [ReasoningLevel.$zero]: "",
};

export const ENTRY_STATUS_LABELS: Record<ProviderAppEntryStatus, string> = {
  [ProviderAppEntryStatus.EntryStatusApplied]: "已应用",
  [ProviderAppEntryStatus.EntryStatusDrifted]: "配置有偏差",
  [ProviderAppEntryStatus.EntryStatusExternal]: "外部配置",
  [ProviderAppEntryStatus.$zero]: "",
};

export function protocolLabel(protocol: ProviderProtocol): string {
  return PROTOCOL_LABELS[protocol];
}

// 落库与展示共用同一个空值口径：trim 后为空视为没有 Key。
export function hasApiKey(provider: ProviderView): boolean {
  return Boolean(provider.apiKey?.trim());
}

export function findAppState(
  state: ProvidersState | null,
  app: ProviderApp
): ProviderAppState | undefined {
  return (state?.apps ?? []).find((item) => item.app === app);
}

// 工具与提供商协议是否兼容；不兼容时应用按钮禁用。
export function protocolCompatible(appState: ProviderAppState, provider: ProviderView): boolean {
  return (appState.supportedProtocols ?? []).includes(provider.protocol);
}

// 应用按钮的 hover 提示：不兼容时说明原因（口径与后端协议校验一致），
// 兼容时返回 null——禁用按钮在 WKWebView 不显示原生 title，提示走 AppTooltip。
export function protocolIncompatibleTip(
  appState: ProviderAppState,
  provider: ProviderView
): string | null {
  if (protocolCompatible(appState, provider)) {
    return null;
  }
  const supported = (appState.supportedProtocols ?? []).map(protocolLabel).join("、");
  return `${appLabel(appState.app)} 不支持协议 ${protocolLabel(provider.protocol)}，支持：${supported}`;
}

// 应用弹窗的思考等级可选项：模型 reasoning_levels 与工具支持集取交集；
// 模型未填等级时视为未约束，直接用工具支持集。为空表示该模型对这个
// 工具没有可写等级（写入时省略字段）。
export function reasoningLevelOptions(
  appState: ProviderAppState,
  model: Pick<ProviderModelView, "reasoningLevels"> | Pick<ProviderModelInput, "reasoningLevels">
): ReasoningLevel[] {
  const supported = appState.supportedReasoningLevels ?? [];
  const levels = model.reasoningLevels;
  if (!levels) {
    return [...supported];
  }
  return supported.filter((level) => levels.includes(level));
}

// 应用弹窗思考等级档位在该提供商的实际写入值；后端按各工具 apply 的
// 写入逻辑同源下发（如 Grok max→xhigh、Pi off→off），前端不复刻规则。
// 支持集为空的工具（OpenCode）查不到时回退为枚举名本身。
export function reasoningEffortWrite(
  appState: ProviderAppState,
  level: ReasoningLevel
): string {
  return (
    (appState.reasoningLevelWrites ?? []).find((item) => item.level === level)
      ?.writes ?? level
  );
}

// 提供商卡片条目行的思考等级映射文案：只列出写入值与档位名不同的档
// （如 Grok 的 max→xhigh）；全同名直写的提供商返回 null，不产生噪音。
export function reasoningEffortMappingText(
  appState: ProviderAppState
): string | null {
  const mappings = (appState.reasoningLevelWrites ?? []).filter(
    (item) => item.writes !== item.level
  );
  if (mappings.length === 0) {
    return null;
  }
  return mappings.map((item) => `${item.level}→${item.writes}`).join("、");
}

// 应用弹窗提示：该工具不会写入的模型元数据（标签由后端能力表下发）。
// 无此类字段的提供商返回 null，不产生噪音。
export function unwrittenModelFieldsText(
  appState: ProviderAppState
): string | null {
  if ((appState.unwrittenModelFields ?? []).length === 0) {
    return null;
  }
  return `${appLabel(appState.app)} 不会写入这些模型元数据：${appState.unwrittenModelFields!.join("、")}；在模型里填写的对应值不会应用到该工具。`;
}

// Codex 的模型目录由已运行的进程缓存（0.159 起为托管守护进程），写入后
// 不会自动生效；应用与同步两处弹窗共用这段提示，不各留一份文案。
export const CODEX_RESTART_HINT =
  "已运行的 Codex 不会自动读取本次变更：请完全退出 Codex 后重开；仍未生效时执行 codex app-server daemon restart（Codex 0.159 起模型目录由后台守护进程缓存）。";

// 勾选语义两处模型表单共用：选中且未填等级时给常用默认集，
// 取消时连同思考等级一并清空（应用时不写该元数据）。
export function toggleReasoning(
  model: Pick<ProviderModelInput, "reasoning" | "reasoningLevels">,
  value: boolean
): void {
  model.reasoning = value;
  if (!value) {
    model.reasoningLevels = null;
  } else if (!model.reasoningLevels) {
    model.reasoningLevels = [
      ReasoningLevel.ReasoningLow,
      ReasoningLevel.ReasoningMedium,
      ReasoningLevel.ReasoningHigh,
    ];
  }
}

// 应用前置条件：协议兼容 + 已设 Key，与模型勾选无关；同步候选的硬阻断
// 只取这一层。
export function applyPrerequisites(
  appState: ProviderAppState,
  provider: ProviderView
): string[] {
  const blockers: string[] = [];
  if (!protocolCompatible(appState, provider)) {
    blockers.push(
      `${appLabel(appState.app)} 不支持协议 ${protocolLabel(provider.protocol)}。`
    );
  }
  if (!hasApiKey(provider)) {
    blockers.push("尚未设置该提供商的 API Key。");
  }
  return blockers;
}

// 应用前置条件清单 + 至少一个模型；非空时应用按钮禁用并逐条展示原因。
export function applyBlockers(
  appState: ProviderAppState,
  provider: ProviderView,
  selectedModelIds: string[]
): string[] {
  const blockers = applyPrerequisites(appState, provider);
  if (selectedModelIds.length === 0) {
    blockers.push("至少选择一个模型。");
  }
  return blockers;
}

// 工具卡片上属于指定提供商的条目（已应用 / 配置有偏差），外部配置不算。
export function entriesForProvider(
  appState: ProviderAppState,
  providerId: string
): ProviderAppEntry[] {
  return (appState.entries ?? []).filter((entry) => entry.providerId === providerId);
}

// 「应用提供商」候选列表：提供商在本工具已写入 reins- 条目（已应用或配
// 置有偏差）时不再重复出现，应用状态由条目行 tag 表达；外部配置不阻止
// 候选，因为它不属于任何 Reins 平台。
export function applyCandidates(
  appState: ProviderAppState,
  providers: ProviderView[]
): ProviderView[] {
  const managedIds = new Set(
    (appState.entries ?? [])
      .map((entry) => entry.providerId)
      .filter((id): id is string => typeof id === "string" && id !== "")
  );
  return providers.filter((provider) => !managedIds.has(provider.id));
}

// 条目行上重新打开应用弹窗的目标：已应用与配置有偏差的条目都指向某个
// 平台，可以据此改默认模型（多提供商并存时也用它切换默认）；外部配置
// 不属于任何平台、平台已删除的偏差条目查不到元数据，都返回 null。
export function reapplyProvider(
  entry: ProviderAppEntry,
  providers: ProviderView[]
): ProviderView | null {
  if (!entry.providerId) {
    return null;
  }
  return providers.find((provider) => provider.id === entry.providerId) ?? null;
}

// 应用弹窗的思考等级可选项：多供应商并存的工具（additive）档位只是各
// 模型条目的默认预填值，直接给工具全集，不因某个模型的声明收窄；其余
// 工具按选中模型可用等级取交集，外加当前已写入的等级——当前值即使不在
// 交集里（模型目录后来收窄）也要能显示与保留，否则重新应用会把它写没。
export function reasoningLevelChoices(
  appState: ProviderAppState,
  provider: ProviderView,
  selectedModelIds: string[],
  current: ReasoningLevel | null
): ReasoningLevel[] {
  if (appState.additive) {
    const levels = [...(appState.supportedReasoningLevels ?? [])];
    return current && !levels.includes(current) ? [...levels, current] : levels;
  }
  const selected = (provider.models ?? []).filter((model) =>
    selectedModelIds.includes(model.id)
  );
  // 取所有选中模型可用等级的交集；任一模型未约束等级则不收紧。
  let options: ReasoningLevel[] | null = null;
  for (const model of selected) {
    const modelOptions = reasoningLevelOptions(appState, model);
    options = options === null ? modelOptions : options.filter((level) => modelOptions.includes(level));
  }
  const levels = options ?? [];
  return current && !levels.includes(current) ? [...levels, current] : levels;
}

// 应用弹窗的交集收窄说明：列出因选中模型未声明而被排除出候选的档位及
// 排除方，让「为什么候选比工具支持集少」在界面上可解释。additive 工具
// 不做交集、模型都未约束、没有档位被排除、或交集为空（另有空态文案）
// 时返回 null 不展示。
export function reasoningIntersectionNote(
  appState: ProviderAppState,
  provider: ProviderView,
  selectedModelIds: string[],
  current: ReasoningLevel | null
): string | null {
  if (appState.additive) {
    return null;
  }
  const selected = (provider.models ?? []).filter((model) =>
    selectedModelIds.includes(model.id)
  );
  const constrained = selected.filter((model) => model.reasoningLevels);
  if (constrained.length === 0) {
    return null;
  }
  // 当前已写入的档位即使不在交集里也会追加进候选（见 reasoningLevelChoices），
  // 不算「未列出」。
  const excluded = (appState.supportedReasoningLevels ?? []).filter(
    (level) =>
      level !== current &&
      constrained.some((model) => !model.reasoningLevels!.includes(level))
  );
  if (excluded.length === 0) {
    return null;
  }
  const remaining = (appState.supportedReasoningLevels ?? []).filter(
    (level) =>
      level === current ||
      constrained.every((model) => model.reasoningLevels!.includes(level))
  );
  if (remaining.length === 0) {
    return null;
  }
  // 按排除方分组，同一批模型排除的档位合并成一句。
  const byBlockers = new Map<string, ReasoningLevel[]>();
  for (const level of excluded) {
    const blockers = constrained
      .filter((model) => !model.reasoningLevels!.includes(level))
      .map((model) => model.id)
      .join("、");
    byBlockers.set(blockers, [...(byBlockers.get(blockers) ?? []), level]);
  }
  const parts = [...byBlockers].map(
    ([blockers, levels]) =>
      `${levels
        .map((level) => `${REASONING_LEVEL_LABELS[level]}（${level}）`)
        .join("、")} 因 ${blockers} 未声明`
  );
  return `候选档位取的是选中模型支持范围的交集：${parts.join("；")}。`;
}

// Agent 页 additive 工具的「默认思考等级」下拉：候选 = 工具支持全集 +
// 不设置。档位与供应商无关，写入值映射（如有）随档位标注。
export function toolLevelOptions(
  appState: ProviderAppState
): { value: string; label: string }[] {
  return [
    { value: "", label: "不设置（应用不改档位）" },
    ...(appState.supportedReasoningLevels ?? []).map((level) => {
      const writes = reasoningEffortWrite(appState, level);
      const base = `${REASONING_LEVEL_LABELS[level]}（${level}）`;
      return {
        value: level,
        label:
          writes === level ? base : `${REASONING_LEVEL_LABELS[level]}（${level}，写入 ${writes}）`,
      };
    }),
  ];
}

// additive 应用弹窗的档位只读文案：档位跟随工具级默认设置，不在弹窗里选。
export function toolLevelText(appState: ProviderAppState): string {
  if ((appState.supportedReasoningLevels ?? []).length === 0) {
    return "该工具不写入思考档位。";
  }
  const level = appState.appReasoningLevel;
  if (!level) {
    return "该工具未设置默认思考等级，本次应用不改各模型的档位（保留现状）。可在 Agent 页的工具卡片上设置。";
  }
  const writes = reasoningEffortWrite(appState, level);
  const label = `${REASONING_LEVEL_LABELS[level]}（${level}）`;
  return writes === level
    ? `将按工具默认档「${label}」写入所有勾选模型；档位在 Agent 页的工具卡片上设置。`
    : `将按工具默认档「${REASONING_LEVEL_LABELS[level]}（${level}，写入 ${writes}）」写入所有勾选模型；档位在 Agent 页的工具卡片上设置。`;
}

// 应用弹窗的初始选择：优先沿用该平台已写入的模型，并以当前默认模型为
// 默认项，避免重新应用时顺带换掉默认模型；没有可复用的写入记录（首次
// 应用）时预选平台目录里的全部模型。默认思考等级取该工具配置里的当前
// 值（工具支持的前提下），重新应用不会因为弹窗默认值把它改掉。
export function initialApplySelection(
  appState: ProviderAppState | undefined,
  provider: ProviderView
): {
  modelIds: string[];
  defaultModelId: string;
  defaultReasoningLevel: ReasoningLevel | null;
} {
  const applied = appState ? entriesForProvider(appState, provider.id) : [];
  const providerModels = provider.models ?? [];
  const known = applied
    .flatMap((entry) => entry.modelIds ?? [])
    .filter((modelId) => providerModels.some((model) => model.id === modelId));
  const modelIds = known.length > 0 ? known : providerModels.map((model) => model.id);
  const currentDefault = applied.find((entry) => entry.defaultModelId)?.defaultModelId;
  const defaultModelId =
    currentDefault && modelIds.includes(currentDefault) ? currentDefault : (modelIds[0] ?? "");
  const currentLevel = appState?.defaultReasoningLevel ?? null;
  const defaultReasoningLevel =
    currentLevel && appState && (appState.supportedReasoningLevels ?? []).includes(currentLevel)
      ? currentLevel
      : null;
  return { modelIds, defaultModelId, defaultReasoningLevel };
}

// 应用弹窗确认时的选择状态（原始值，档位规则在 providerApplyInput 内部
// 派生，弹窗无须理解候选的追加规则）。
export type ProviderApplySelection = {
  modelIds: string[];
  defaultModelId: string;
  defaultReasoningLevel: ReasoningLevel | null;
};

// 应用弹窗确认后的应用输入。档位规则与候选规则同居一处：additive 工具
// 档位跟随工具级设置（弹窗不参与选择）；其余工具候选由
// reasoningLevelChoices 派生——当前值必被追加进候选（目录收窄不抹已选
// 档位），因此「候选外不写」的回退只在未选档位时发生。
export function providerApplyInput(
  appState: ProviderAppState | undefined,
  provider: ProviderView,
  app: ProviderApp,
  selection: ProviderApplySelection
): ApplyProviderInput {
  let defaultReasoningLevel: ReasoningLevel | null = null;
  if (appState?.additive) {
    defaultReasoningLevel = appState.appReasoningLevel ?? null;
  } else {
    const chosen = selection.defaultReasoningLevel;
    defaultReasoningLevel =
      chosen &&
      appState &&
      reasoningLevelChoices(appState, provider, selection.modelIds, chosen).includes(chosen)
        ? chosen
        : null;
  }
  return {
    providerId: provider.id,
    app,
    modelIds: selection.modelIds,
    defaultModelId: selection.defaultModelId,
    defaultReasoningLevel,
  };
}

// ---------------------------------------------------------------------------
// 提供商同步（卡片「同步」→ 勾选模型弹窗 → 逐工具应用）
// ---------------------------------------------------------------------------

// 同步候选与硬阻断：已应用与配置有偏差的条目都属于该提供商，外部配置不
// 算。阻断只取与模型勾选无关的前置条件（协议不兼容、未设 Key）；模型层
// 面的取舍由同步弹窗的勾选表达，不再由计划代答。
export type ProviderSyncApp = {
  app: ProviderApp;
  // 非空 = 该工具本次不参与同步，逐条给出原因。
  blockers: string[];
};

export function providerSyncApps(
  state: ProvidersState,
  provider: ProviderView
): ProviderSyncApp[] {
  const apps: ProviderSyncApp[] = [];
  for (const appState of state.apps ?? []) {
    if (entriesForProvider(appState, provider.id).length === 0) {
      continue;
    }
    apps.push({ app: appState.app, blockers: applyPrerequisites(appState, provider) });
  }
  return apps;
}

// 同步弹窗的初始勾选：各参与工具已应用模型的并集与当前目录取交集；
// 并集为空（条目漂移、已应用模型全部下架）时预选整个目录。
export function initialSyncSelection(
  state: ProvidersState,
  provider: ProviderView,
  apps: readonly ProviderApp[]
): string[] {
  const catalog = provider.models ?? [];
  const selected = new Set<string>();
  for (const app of apps) {
    const appState = findAppState(state, app);
    for (const entry of appState ? entriesForProvider(appState, provider.id) : []) {
      for (const modelId of entry.modelIds ?? []) {
        if (catalog.some((model) => model.id === modelId)) {
          selected.add(modelId);
        }
      }
    }
  }
  return selected.size > 0 ? [...selected] : catalog.map((model) => model.id);
}

// 同步确认后的逐工具应用输入：模型清单取弹窗勾选（后端按清单整体重写
// reins- 条目，未勾选即移除）；默认模型沿用该工具当前写入值，不在勾选内
// 时顺延为勾选第一项；默认思考等级保持当前写入值——工具不再支持该档位
// 时 initialApplySelection 已降级为 null，模型目录收窄不抹已写档位。
export function providerSyncInput(
  state: ProvidersState,
  provider: ProviderView,
  app: ProviderApp,
  modelIds: string[]
): ApplyProviderInput {
  const appState = findAppState(state, app);
  const current = initialApplySelection(appState, provider);
  return {
    providerId: provider.id,
    app,
    modelIds,
    defaultModelId:
      current.defaultModelId && modelIds.includes(current.defaultModelId)
        ? current.defaultModelId
        : (modelIds[0] ?? ""),
    defaultReasoningLevel: current.defaultReasoningLevel,
  };
}

// 同步的三态判定与跳过清单。阻断原因是完整句子，嵌入「工具名（原因）」
// 行时去掉句尾句号；行格式由同步弹窗与卡片常驻结果共用，不得各写一份。
export type ProviderSyncTargets = {
  // 前置条件通过、本次会参与同步的工具。
  targets: ProviderApp[];
  // 被阻断工具的「工具名（原因）」清单；无阻断为空。
  skipLines: string[];
  // 同步按钮的 hover 提示；空串 = 可同步。
  buttonTitle: string;
};

export function providerSyncTargets(apps: ProviderSyncApp[]): ProviderSyncTargets {
  const targets = apps.filter((item) => item.blockers.length === 0).map((item) => item.app);
  const skipLines = apps
    .filter((item) => item.blockers.length > 0)
    .map(
      (item) => `${appLabel(item.app)}（${item.blockers.join("").replace(/。+$/, "")}）`
    );
  if (apps.length === 0) {
    return { targets, skipLines, buttonTitle: "尚未应用到任何 Agent。" };
  }
  if (targets.length === 0) {
    return { targets, skipLines, buttonTitle: `无法同步：${skipLines.join("；")}。` };
  }
  return { targets, skipLines, buttonTitle: "" };
}

// 同步结果句：已同步 / 跳过原因 / 逐项失败明细合成一句，常驻在卡片上。
export function providerSyncResultText(
  synced: string[],
  failures: string[],
  skipLines: string[]
): string {
  const parts = [synced.length > 0 ? `已同步到 ${synced.join("、")}` : "没有可同步的 Agent"];
  if (skipLines.length > 0) {
    parts.push(`未同步：${skipLines.join("；")}`);
  }
  if (failures.length > 0) {
    parts.push(`失败：${failures.join("；")}`);
  }
  return `${parts.join("；")}。`;
}

// ---------------------------------------------------------------------------
// 编辑弹窗表单状态
// ---------------------------------------------------------------------------

export type ProviderModelForm = ProviderModelInput & {
  // v-for 稳定键；id 在用户输入过程中可能为空。
  rowKey: string;
};

export type ProviderFormState = {
  // null = 新建；非 null = 编辑且 ID 不可改（落库 ID 同时是 Key 的归属字段）。
  originalProviderId: string | null;
  providerId: string;
  label: string;
  protocol: ProviderProtocol;
  baseUrl: string;
  // 明文回显：表单里的值就是 providers.yaml 里的值，留空保存即清除。
  apiKey: string;
  models: ProviderModelForm[];
  // 校验错误：ProviderEditDialog 内联展示，用户改动对应字段时清空。
  // form 是整体失败（提交被后端拒绝）的落点。
  errors?: {
    providerId?: string;
    label?: string;
    baseUrl?: string;
    models?: string;
    form?: string;
  };
};

let modelRowSeq = 0;

export function emptyModelForm(modelId = ""): ProviderModelForm {
  modelRowSeq += 1;
  return {
    rowKey: `model-${modelRowSeq}`,
    id: modelId,
    label: "",
    contextWindow: null,
    maxOutputTokens: null,
    supportsImages: null,
    reasoning: null,
    reasoningLevels: null,
  };
}

// models.dev 元数据只预填空缺字段（null/undefined 视为空缺），不覆盖用户已填值；
// 行内补全与手工新增弹窗共用。
export function applyModelsDevMeta(
  target: ProviderModelInput,
  meta: NonNullable<ModelsDevMatchResult["meta"]>
): void {
  target.contextWindow ??= meta.contextWindow ?? null;
  target.maxOutputTokens ??= meta.maxOutputTokens ?? null;
  target.supportsImages ??= meta.supportsImages ?? null;
  target.reasoning ??= meta.reasoning ?? null;
  target.reasoningLevels ??= meta.reasoningLevels ?? null;
}

export function reasoningLevelsText(levels: ProviderModelInput["reasoningLevels"]): string {
  return (levels ?? []).join(",");
}

// 只保留已知等级，全部无效时视为未设置。
export function parseReasoningLevels(text: string): ProviderModelInput["reasoningLevels"] {
  const levels = text
    .split(",")
    .map((part) => part.trim())
    .filter((part): part is ReasoningLevel => part in REASONING_LEVEL_LABELS);
  return levels.length > 0 ? levels : null;
}

export function emptyProviderForm(): ProviderFormState {
  return {
    originalProviderId: null,
    providerId: "",
    label: "",
    protocol: ProviderProtocol.ProtocolOpenaiChatCompletions,
    baseUrl: "",
    apiKey: "",
    models: [],
    errors: {},
  };
}

export function formFromProvider(provider: ProviderView): ProviderFormState {
  return {
    originalProviderId: provider.id,
    providerId: provider.id,
    label: provider.label,
    protocol: provider.protocol,
    baseUrl: provider.baseUrl,
    apiKey: provider.apiKey ?? "",
    models: (provider.models ?? []).map((model) => ({
      ...emptyModelForm(model.id),
      id: model.id,
      label: model.label,
      contextWindow: model.contextWindow ?? null,
      maxOutputTokens: model.maxOutputTokens ?? null,
      supportsImages: model.supportsImages ?? null,
      reasoning: model.reasoning ?? null,
      reasoningLevels: model.reasoningLevels ?? null,
    })),
    errors: {},
  };
}

// number input 空值会得到 ""；统一把非有限值收敛为 null。
function numberOrNull(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) {
    return Number(value);
  }
  return null;
}

// 输入即时归一化，保证界面所见 ID 与落库 ID 一致；
// 字符集规则以后端 normalize_provider_id 为准，这里只做同样的转换。
export function normalizeProviderIdInput(value: string): string {
  return value
    .toLowerCase()
    .replace(/_/g, "-")
    .replace(/[^a-z0-9-]/g, "");
}

export type ProviderFormValidation = {
  // 新增第一步只落库元数据，模型目录留到第二步
  allowEmptyModels: boolean;
  // 已存在的平台 ID
  existingProviderIds: readonly string[];
};

// 表单校验：非空字段 + 新增时的平台 ID 冲突。
// 撞名必须在提交前拦住：后端的写入会把同 ID 当成更新，静默覆盖已有平台
// 的元数据与模型目录。
export function validateProviderForm(
  form: ProviderFormState,
  context: ProviderFormValidation,
): NonNullable<ProviderFormState["errors"]> {
  const errors: NonNullable<ProviderFormState["errors"]> = {};
  const providerId = normalizeProviderIdInput(form.providerId.trim());

  if (!providerId) {
    errors.providerId = "请填写提供商 ID。";
  } else if (
    form.originalProviderId === null &&
    context.existingProviderIds.includes(providerId)
  ) {
    errors.providerId = "该提供商 ID 已存在，请换一个。";
  }
  if (!form.label.trim()) {
    errors.label = "请填写名称。";
  }
  if (!form.baseUrl.trim()) {
    errors.baseUrl = "请填写 Base URL。";
  }
  if (!context.allowEmptyModels && form.models.length === 0) {
    errors.models = "至少添加一个模型。";
  }

  return errors;
}

export function formToInput(
  form: ProviderFormState,
  mode: ProviderWriteMode,
): ProviderUpsertInput {
  return {
    providerId: form.providerId,
    mode,
    label: form.label,
    protocol: form.protocol,
    baseUrl: form.baseUrl,
    apiKey: form.apiKey.trim(),
    models: form.models.map((model) => ({
      id: model.id,
      label: model.label,
      contextWindow: numberOrNull(model.contextWindow),
      maxOutputTokens: numberOrNull(model.maxOutputTokens),
      supportsImages: model.supportsImages ?? null,
      reasoning: model.reasoning ?? null,
      reasoningLevels: model.reasoningLevels ?? null,
    })),
  };
}

// ---------------------------------------------------------------------------
// 模型目录加入
// ---------------------------------------------------------------------------

// 模型 ID 比较口径与后端 normalize_model_record 一致：trim 后精确匹配。
function modelIdKey(id: string): string {
  return id.trim();
}

export function hasModelId(ids: readonly string[], id: string): boolean {
  const key = modelIdKey(id);
  return ids.some((existing) => modelIdKey(existing) === key);
}

// 拉取结果并入模型目录：与目录已有 ID（含本次先加入的）重复的模型默认忽略，
// 忽略项返回给调用方做弹窗提示。
export function mergeFetchedModels(
  existing: ProviderModelForm[],
  fetched: FetchedModel[]
): { added: ProviderModelForm[]; skipped: FetchedModel[] } {
  const seen = new Set(existing.map((model) => modelIdKey(model.id)));
  const added: ProviderModelForm[] = [];
  const skipped: FetchedModel[] = [];
  for (const model of fetched) {
    const key = modelIdKey(model.id);
    if (seen.has(key)) {
      skipped.push(model);
      continue;
    }
    seen.add(key);
    added.push({ ...emptyModelForm(model.id), id: model.id, label: model.name ?? model.id });
  }
  return { added, skipped };
}

