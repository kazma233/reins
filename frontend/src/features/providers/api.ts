import { bindingsCall } from "@shared/lib/bindings";
import { ProvidersService } from "@bindings/reins/internal/providers";
import type { ProviderApp } from "@bindings/reins/internal/agents/models";
import type {
  ApplyProviderInput,
  FetchedModelsResult,
  ModelsDevMatchResult,
  ProviderAppState,
  ProviderApplyPreview,
  ProviderMutationResult,
  ProviderProtocol,
  ProviderUpsertInput,
  ProvidersState,
  ReasoningLevel,
} from "@bindings/reins/internal/providers/models";

// 全部后端调用经 bindings service；类型与运行时同源（frontend/bindings/）。
// 明文 API Key 只经 upsertProvider 的输入下发，单独的密钥读写命令已废弃。
// Promise 收口断言见 @shared/lib/bindings；带枚举字段的 struct 入参
// （upsert/preview/apply）类型同型，直接满足 bindings 接口。bindings 的
// ProviderApp 即旧 ts-rs 契约里的 ProviderAppId（providers 域键，值集一致），
// 域内沿用 bindings 拼写。

export function getProvidersState(): Promise<ProvidersState> {
  return bindingsCall(ProvidersService.GetProvidersState());
}

// 单个工具的反显状态：写操作只改一个工具的配置文件，用它做定点刷新，
// 不必重读全部工具。
export function getProviderAppState(app: ProviderApp): Promise<ProviderAppState> {
  return bindingsCall(ProvidersService.GetProviderAppState(app));
}

// 返回落库后的归一化提供商 ID（小写、`_`→`-`），写密钥等后续调用以它为准。
export function upsertProvider(input: ProviderUpsertInput): Promise<string> {
  return ProvidersService.UpsertProvider(input);
}

// 工具级默认思考等级（app_settings 段）；null 表示不设置，应用不改档位。
export function setProviderAppReasoningLevel(
  app: ProviderApp,
  defaultReasoningLevel: ReasoningLevel | null
): Promise<void> {
  return ProvidersService.SetProviderAppReasoningLevel(app, defaultReasoningLevel);
}

export function deleteProvider(providerId: string): Promise<ProviderMutationResult> {
  return bindingsCall(ProvidersService.DeleteProvider(providerId));
}

// 新增提供商未保存时直连拉取；密钥只作当次请求参数，不落盘。
export function fetchProviderModelsDirect(
  protocol: ProviderProtocol,
  baseUrl: string,
  apiKey: string
): Promise<FetchedModelsResult> {
  return bindingsCall(ProvidersService.FetchProviderModelsDirect(protocol, baseUrl, apiKey));
}

export function fetchModelsdevCatalog(
  providerId: string,
  modelId: string,
  forceRefresh = false
): Promise<ModelsDevMatchResult> {
  return bindingsCall(ProvidersService.FetchModelsdevCatalog(providerId, modelId, forceRefresh));
}

export function previewProviderApply(input: ApplyProviderInput): Promise<ProviderApplyPreview> {
  return bindingsCall(ProvidersService.PreviewProviderApply(input));
}

export function applyProviderToApp(input: ApplyProviderInput): Promise<ProviderMutationResult> {
  return bindingsCall(ProvidersService.ApplyProviderToApp(input));
}

export function removeProviderFromApp(
  providerId: string,
  app: ProviderApp
): Promise<ProviderMutationResult> {
  return bindingsCall(ProvidersService.RemoveProviderFromApp(providerId, app));
}

// 外部条目不属于 providers.yaml，按工具配置里的原样键删除；
// Claude 外部配置内嵌在 env 中，后端固定拒绝。
export function removeExternalEntry(
  app: ProviderApp,
  entryKey: string
): Promise<ProviderMutationResult> {
  return bindingsCall(ProvidersService.RemoveExternalEntry(app, entryKey));
}
